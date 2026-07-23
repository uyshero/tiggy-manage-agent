package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ProviderFake                  = "fake"
	ProviderOpenAICompatible      = "openai-compatible"
	ProviderTypeOpenAI            = "openai"
	DefaultModel                  = "fake-demo"
	DefaultOpenAIBaseURL          = "https://api.openai.com/v1"
	DefaultProviderMaxAttempts    = 3
	DefaultProviderRetryBaseDelay = 250 * time.Millisecond
	MaxProviderRetryDelay         = 30 * time.Second
)

type ErrorClass string

const (
	ErrorClassAuth           ErrorClass = "auth"
	ErrorClassRateLimit      ErrorClass = "rate_limit"
	ErrorClassContextLength  ErrorClass = "context_length"
	ErrorClassTimeout        ErrorClass = "timeout"
	ErrorClassServer         ErrorClass = "server"
	ErrorClassInvalidRequest ErrorClass = "invalid_request"
	ErrorClassUnknown        ErrorClass = "unknown"
)

type ProviderError struct {
	Class      ErrorClass
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	Attempts   int
	Message    string
	Cause      error
}

func (e *ProviderError) Error() string {
	detail := strings.TrimSpace(e.Message)
	if detail == "" {
		detail = "provider request failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("provider request failed (%s, HTTP %d): %s", e.Class, e.StatusCode, detail)
	}
	return fmt.Sprintf("provider request failed (%s): %s", e.Class, detail)
}

func (e *ProviderError) Unwrap() error {
	return e.Cause
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type Message struct {
	Role       string        `json:"role"`
	Content    []ContentPart `json:"content"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type Request struct {
	Provider        string    `json:"provider,omitempty"`
	ProviderType    string    `json:"-"`
	Model           string    `json:"model,omitempty"`
	BaseURL         string    `json:"-"`
	APIKey          string    `json:"-"`
	MaxOutputTokens int       `json:"max_output_tokens,omitempty"`
	Messages        []Message `json:"messages"`
	Tools           []Tool    `json:"tools,omitempty"`
}

type Response struct {
	Message   Message         `json:"message"`
	Reasoning []ReasoningPart `json:"reasoning,omitempty"`
	Usage     Usage           `json:"usage,omitempty"`
}

type Delta struct {
	Index        int            `json:"index"`
	Kind         string         `json:"kind,omitempty"`
	Text         string         `json:"text,omitempty"`
	ToolCall     *ToolCallDelta `json:"tool_call,omitempty"`
	Usage        *Usage         `json:"usage,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Error        *StreamError   `json:"error,omitempty"`
}

const (
	DeltaKindText      = "text"
	DeltaKindReasoning = "reasoning"
	DeltaKindToolCall  = "tool_call"
	DeltaKindUsage     = "usage"
	DeltaKindStop      = "stop"
	DeltaKindError     = "error"
)

type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type StreamError struct {
	Class      ErrorClass `json:"class"`
	StatusCode int        `json:"status_code,omitempty"`
	Retryable  bool       `json:"retryable"`
	Message    string     `json:"message"`
}

type ReasoningPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Usage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
	TotalTokens       int64 `json:"total_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoning_tokens,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Client 是 AgentRuntime 调用模型的最小边界。
// 当前先用 FakeClient 跑通链路，后续再接具体模型厂商实现。
type Client interface {
	Generate(ctx context.Context, request Request) (Response, error)
}

type StreamingClient interface {
	GenerateStream(ctx context.Context, request Request, onDelta func(Delta) error) (Response, error)
}

// Provider 负责按模型名创建具体 LLM Client。
// 后续 OpenAI、本地模型或企业内部网关都应该作为 Provider 注册进 Manager。
type Provider interface {
	NewClient(model string) (Client, error)
}

type ManagerConfig struct {
	Provider       string
	ProviderType   string
	Model          string
	BaseURL        string
	APIKey         string
	MaxAttempts    int
	RetryBaseDelay time.Duration
}

// FakeProvider 是当前默认 Provider，不访问外部模型 API。
type FakeProvider struct{}

func (FakeProvider) NewClient(string) (Client, error) {
	return FakeClient{}, nil
}

// OpenAICompatibleProvider 适配 OpenAI Chat Completions 兼容接口。
type OpenAICompatibleProvider struct {
	BaseURL        string
	APIKey         string
	Client         *http.Client
	Label          string
	MaxAttempts    int
	RetryBaseDelay time.Duration
}

func (p OpenAICompatibleProvider) NewClient(string) (Client, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, fmt.Errorf("TMA_LLM_API_KEY is required for provider %q", defaultString(p.Label, ProviderOpenAICompatible))
	}
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	httpClient := p.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return OpenAICompatibleClient{
		BaseURL:        baseURL,
		APIKey:         p.APIKey,
		Client:         httpClient,
		MaxAttempts:    p.MaxAttempts,
		RetryBaseDelay: p.RetryBaseDelay,
	}, nil
}

// Manager 持有当前 Provider / Model，并把调用转发给当前 Client。
// 未来做热切换时，只需要 Switch 当前配置，Runner 和 Runtime 不需要重建。
type Manager struct {
	mu             sync.RWMutex
	provider       string
	model          string
	client         Client
	providers      map[string]Provider
	maxAttempts    int
	retryBaseDelay time.Duration
}

func NewManager(provider string, model string) (*Manager, error) {
	return NewManagerWithConfig(ManagerConfig{Provider: provider, Model: model})
}

func NewManagerWithConfig(config ManagerConfig) (*Manager, error) {
	manager := &Manager{
		providers:      providersFromConfig(config),
		maxAttempts:    config.MaxAttempts,
		retryBaseDelay: config.RetryBaseDelay,
	}
	if err := manager.Switch(config.Provider, config.Model); err != nil {
		return nil, err
	}
	return manager, nil
}

func providersFromConfig(config ManagerConfig) map[string]Provider {
	providers := map[string]Provider{
		ProviderFake: FakeProvider{},
		ProviderOpenAICompatible: OpenAICompatibleProvider{
			BaseURL:        config.BaseURL,
			APIKey:         config.APIKey,
			Label:          ProviderOpenAICompatible,
			MaxAttempts:    config.MaxAttempts,
			RetryBaseDelay: config.RetryBaseDelay,
		},
	}

	providerID := strings.TrimSpace(config.Provider)
	if providerID == "" || providers[providerID] != nil {
		return providers
	}

	providerType := ResolveProviderType(providerID, config.ProviderType)
	if providerType == ProviderFake {
		providers[providerID] = FakeProvider{}
		return providers
	}
	if isOpenAIProviderType(providerType) {
		providers[providerID] = OpenAICompatibleProvider{
			BaseURL:        config.BaseURL,
			APIKey:         config.APIKey,
			Label:          providerID,
			MaxAttempts:    config.MaxAttempts,
			RetryBaseDelay: config.RetryBaseDelay,
		}
	}
	return providers
}

// ResolveProviderType 把业务 Provider ID 归一到具体协议类型。
// fake 永远使用内置 fake 协议；其他自定义 ID 默认按 OpenAI Chat Completions 兼容协议处理。
func ResolveProviderType(providerID string, providerType string) string {
	providerID = strings.TrimSpace(providerID)
	providerType = strings.TrimSpace(providerType)
	if providerID == "" || providerID == ProviderFake {
		return ProviderFake
	}
	if providerType == "" {
		return ProviderTypeOpenAI
	}
	return providerType
}

func isOpenAIProviderType(providerType string) bool {
	return providerType == ProviderTypeOpenAI || providerType == ProviderOpenAICompatible
}

// Current 返回当前 Client 和它对应的 Provider / Model 快照。
func (m *Manager) Current() (Client, string, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client, m.provider, m.model
}

// CurrentConfig 返回当前 Provider / Model，供 Runtime 写入调试事件。
func (m *Manager) CurrentConfig() (string, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider, m.model
}

// Generate 让 Manager 自身也满足 Client 接口。
// Runtime 持有 Manager 后，未来 Provider 热切换可以自然影响后续模型调用。
func (m *Manager) Generate(ctx context.Context, request Request) (Response, error) {
	client, provider, model, err := m.clientForRequest(request)
	if err != nil {
		return Response{}, err
	}
	if request.Provider == "" {
		request.Provider = provider
	}
	if request.Model == "" {
		request.Model = model
	}
	return client.Generate(ctx, request)
}

func (m *Manager) GenerateStream(ctx context.Context, request Request, onDelta func(Delta) error) (Response, error) {
	client, provider, model, err := m.clientForRequest(request)
	if err != nil {
		return Response{}, err
	}
	if request.Provider == "" {
		request.Provider = provider
	}
	if request.Model == "" {
		request.Model = model
	}

	if streamingClient, ok := client.(StreamingClient); ok {
		return streamingClient.GenerateStream(ctx, request, onDelta)
	}
	return client.Generate(ctx, request)
}

func (m *Manager) clientForRequest(request Request) (Client, string, string, error) {
	currentClient, currentProvider, currentModel := m.Current()
	provider := defaultString(request.Provider, currentProvider)
	model := defaultString(request.Model, currentModel)
	hasRequestProviderConfig := strings.TrimSpace(request.ProviderType) != "" ||
		strings.TrimSpace(request.BaseURL) != "" ||
		strings.TrimSpace(request.APIKey) != ""
	if provider == currentProvider && model == currentModel && !hasRequestProviderConfig {
		return currentClient, provider, model, nil
	}

	if hasRequestProviderConfig {
		client, err := clientFromProviderConfig(provider, request.ProviderType, model, request.BaseURL, request.APIKey, m.maxAttempts, m.retryBaseDelay)
		if err != nil {
			return nil, "", "", err
		}
		return client, provider, model, nil
	}

	m.mu.RLock()
	factory, ok := m.providers[provider]
	m.mu.RUnlock()
	if !ok {
		return nil, "", "", fmt.Errorf("unsupported LLM provider %q", provider)
	}
	client, err := factory.NewClient(model)
	if err != nil {
		return nil, "", "", err
	}
	return client, provider, model, nil
}

func clientFromProviderConfig(provider string, providerType string, model string, baseURL string, apiKey string, maxAttempts int, retryBaseDelay time.Duration) (Client, error) {
	resolvedType := ResolveProviderType(provider, providerType)
	if resolvedType == ProviderFake {
		return FakeProvider{}.NewClient(model)
	}
	if isOpenAIProviderType(resolvedType) {
		return OpenAICompatibleProvider{
			BaseURL:        baseURL,
			APIKey:         apiKey,
			Label:          provider,
			MaxAttempts:    maxAttempts,
			RetryBaseDelay: retryBaseDelay,
		}.NewClient(model)
	}
	return nil, fmt.Errorf("unsupported LLM provider type %q for provider %q", providerType, provider)
}

// Switch 切换当前 Provider / Model。当前没有暴露 HTTP API，先保留内部能力。
func (m *Manager) Switch(provider string, model string) error {
	if provider == "" {
		provider = ProviderFake
	}
	if model == "" {
		model = DefaultModel
	}

	factory, ok := m.providers[provider]
	if !ok {
		return fmt.Errorf("unsupported LLM provider %q", provider)
	}
	client, err := factory.NewClient(model)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.provider = provider
	m.model = model
	m.client = client
	return nil
}

type FakeClient struct{}

func (FakeClient) Generate(ctx context.Context, request Request) (Response, error) {
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	default:
	}

	if toolText := lastToolText(request.Messages); strings.Contains(toolText, `"id":"call_verify_network_download"`) {
		return Response{
			Message: Message{
				Role: "assistant",
				Content: []ContentPart{{
					Type: "text",
					Text: "Network download verification result: " + toolText,
				}},
			},
		}, nil
	}

	if toolText := lastToolText(request.Messages); containsAny(toolText, "tma-session-tool-ok", "tma-upload-sync-ok", "tma-session-data-seeded", "tma-session-data-persisted", "tma-worker-export-ok", "tma-worker-large-export-ok", "tma-worker-plugin-ok", "tma-computer-plugin-ok", "tma-mcp-filesystem-ok", "computer.get_state completed via cua", "computer.screenshot completed via cua", "tma-web-search-ok", "tma-web-crawl-ok", "tma-browser-flow-ok", "tma-browser-takeover-ok", "Browser session closed.") {
		return Response{
			Message: Message{
				Role: "assistant",
				Content: []ContentPart{{
					Type: "text",
					Text: "Tool verification result: " + toolText,
				}},
			},
		}, nil
	}

	text := "Agent runtime received your message."
	if userText := lastUserText(request.Messages); userText != "" {
		if strings.Contains(userText, "tma.verify_tool_call") {
			return fakeToolCallResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_uploaded_file_seed") {
			return fakeUploadedFileSeedResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_uploaded_file_read") {
			return fakeUploadedFileReadResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_uploaded_file_export") {
			return fakeUploadedFileExportResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_worker_export") {
			return fakeWorkerExportResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_worker_large_export") {
			return fakeWorkerLargeExportResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_worker_plugin_tool") {
			return fakeWorkerPluginToolResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_mcp_tool") {
			return fakeMCPToolResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_computer_plugin_tool") {
			return fakeComputerPluginToolResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_computer_plugin_screenshot") {
			return fakeComputerPluginScreenshotResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_web_crawl") {
			return fakeWebCrawlResponse(userText), nil
		}
		if strings.Contains(userText, "tma.verify_web_search") {
			return fakeWebSearchResponse(userText), nil
		}
		if strings.Contains(userText, "tma.verify_browser_flow") {
			return fakeBrowserFlowResponse(userText), nil
		}
		if strings.Contains(userText, "tma.verify_browser_takeover") {
			return fakeBrowserTakeoverResponse(userText), nil
		}
		if strings.Contains(userText, "tma.verify_browser_close") {
			return fakeBrowserCloseResponse(), nil
		}
		if strings.Contains(userText, "tma.verify_network_download") {
			return fakeNetworkDownloadResponse(userText), nil
		}
		text = "Agent runtime received: " + userText
	}

	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: text,
			}},
		},
	}, nil
}

func fakeToolCallResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Running tool verification command.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_tool",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","pwd && printf '\\n' && printf tma-session-tool-ok"],"work_dir":"."}`),
				},
			}},
		},
	}
}

func fakeUploadedFileSeedResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Seeding session data verification command.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_upload_seed",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","for f in /workspace/uploads/*/*; do [ -f \"$f\" ] && cat \"$f\"; done; printf '\\n'; printf tma-session-data-seeded > /mnt/data/state.txt; cat /mnt/data/state.txt"],"work_dir":"."}`),
				},
			}},
		},
	}
}

func fakeUploadedFileReadResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Reading session data verification command.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_upload_read",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","for f in /workspace/uploads/*/*; do [ -f \"$f\" ] && cat \"$f\"; done; printf '\\n'; cat /mnt/data/state.txt; printf '\\n'; printf tma-session-data-persisted"],"work_dir":"."}`),
				},
			}},
		},
	}
}

func fakeUploadedFileExportResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Exporting generated sandbox file as a session artifact.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_upload_export",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","mkdir -p /workspace/outputs && { for f in /workspace/uploads/*/*; do [ -f \"$f\" ] && cat \"$f\"; done; printf 'tma-session-output-exported\\n'; } > /workspace/outputs/export.txt && cat /workspace/outputs/export.txt"],"work_dir":".","output_paths":["/workspace/outputs/export.txt"]}`),
				},
			}},
		},
	}
}

func fakeWorkerExportResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Exporting worker-generated file as a session artifact.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_worker_export",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","printf tma-worker-export-ok > worker-export.txt && cat worker-export.txt"],"work_dir":".","output_paths":["worker-export.txt"]}`),
				},
			}},
		},
	}
}

func fakeWorkerLargeExportResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Exporting large worker-generated file as a session artifact.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_worker_large_export",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","printf 'tma-worker-large-export-ok\n' > worker-large-export.txt && dd if=/dev/zero bs=1048576 count=9 >> worker-large-export.txt 2>/dev/null && printf tma-worker-large-export-ok"],"work_dir":".","output_paths":["worker-large-export.txt"]}`),
				},
			}},
		},
	}
}

func fakeWorkerPluginToolResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Reading worker plugin state.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_worker_plugin",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "robot_get_state",
					Arguments: json.RawMessage(`{}`),
				},
			}},
		},
	}
}

func fakeMCPToolResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Running MCP filesystem verification tool.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_mcp_tool",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "filesystem_read_file",
					Arguments: json.RawMessage(`{"path":"README.md"}`),
				},
			}},
		},
	}
}

func fakeComputerPluginToolResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Reading computer UI tree.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_computer_plugin",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "computer_get_state",
					Arguments: json.RawMessage(`{"capture_mode":"ax"}`),
				},
			}},
		},
	}
}

func fakeComputerPluginScreenshotResponse() Response {
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Capturing computer screenshot.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_computer_plugin_screenshot",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "computer_screenshot",
					Arguments: json.RawMessage(`{}`),
				},
			}},
		},
	}
}

func fakeWebCrawlResponse(userText string) Response {
	targetURL := ""
	if markerIndex := strings.Index(userText, "tma.verify_web_crawl"); markerIndex >= 0 {
		targetURL = strings.TrimSpace(userText[markerIndex+len("tma.verify_web_crawl"):])
	}
	if targetURL == "" {
		targetURL = "http://127.0.0.1:18084/"
	}
	arguments, _ := json.Marshal(map[string]any{
		"url": targetURL,
	})
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Crawling web verification page.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_web_crawl",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "web_crawl",
					Arguments: arguments,
				},
			}},
		},
	}
}

func fakeWebSearchResponse(userText string) Response {
	query := ""
	if markerIndex := strings.Index(userText, "tma.verify_web_search"); markerIndex >= 0 {
		query = strings.TrimSpace(userText[markerIndex+len("tma.verify_web_search"):])
	}
	if query == "" {
		query = "tma-web-search-ok"
	}
	arguments, _ := json.Marshal(map[string]any{
		"query": query,
		"limit": 3,
	})
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Searching web verification provider.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_web_search",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "web_search",
					Arguments: arguments,
				},
			}},
		},
	}
}

func fakeBrowserFlowResponse(userText string) Response {
	targetURL := ""
	if markerIndex := strings.Index(userText, "tma.verify_browser_flow"); markerIndex >= 0 {
		targetURL = strings.TrimSpace(userText[markerIndex+len("tma.verify_browser_flow"):])
	}
	if targetURL == "" {
		targetURL = "data:text/html,<html><title>TMA browser verification</title><body><h1>tma-browser-fixture</h1><input id=verify-input><button id=verify-button onclick=\"document.body.insertAdjacentHTML('beforeend','<p>tma-browser-flow-ok</p>')\">Verify</button></body></html>"
	}
	sessionID := "verify-browser-flow"
	openArguments, _ := json.Marshal(map[string]any{
		"url":                targetURL,
		"browser_session_id": sessionID,
		"viewport": map[string]any{
			"width":  1280,
			"height": 720,
		},
	})
	screenshotArguments, _ := json.Marshal(map[string]any{
		"browser_session_id": sessionID,
		"full_page":          true,
	})
	typeArguments, _ := json.Marshal(map[string]any{
		"browser_session_id": sessionID,
		"selector":           "#verify-input",
		"text":               "tma-browser-typed-ok",
		"clear":              true,
	})
	clickArguments, _ := json.Marshal(map[string]any{
		"browser_session_id": sessionID,
		"selector":           "#verify-button",
	})
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Running browser verification flow.",
			}},
			ToolCalls: []ToolCall{
				{
					ID:   "call_verify_browser_open",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "browser_open",
						Arguments: openArguments,
					},
				},
				{
					ID:   "call_verify_browser_screenshot",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "browser_screenshot",
						Arguments: screenshotArguments,
					},
				},
				{
					ID:   "call_verify_browser_type",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "browser_type",
						Arguments: typeArguments,
					},
				},
				{
					ID:   "call_verify_browser_click",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "browser_click",
						Arguments: clickArguments,
					},
				},
			},
		},
	}
}

func fakeBrowserTakeoverResponse(userText string) Response {
	targetURL := ""
	if markerIndex := strings.Index(userText, "tma.verify_browser_takeover"); markerIndex >= 0 {
		targetURL = strings.TrimSpace(userText[markerIndex+len("tma.verify_browser_takeover"):])
	}
	if targetURL == "" {
		targetURL = "data:text/html,<html><title>TMA browser takeover verification</title><body><h1>tma-browser-takeover-ok</h1><p>Close this browser window to finish verification.</p></body></html>"
	}
	arguments, _ := json.Marshal(map[string]any{
		"url":                targetURL,
		"browser_session_id": "verify-browser-takeover",
		"wait_seconds":       300,
		"viewport": map[string]any{
			"width":  1280,
			"height": 720,
		},
	})
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Opening local browser for manual takeover verification.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_browser_takeover",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "browser_takeover",
					Arguments: arguments,
				},
			}},
		},
	}
}

func fakeBrowserCloseResponse() Response {
	arguments, _ := json.Marshal(map[string]any{
		"browser_session_id": "verify-browser-takeover",
	})
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Closing local browser takeover verification session.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_browser_close",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "browser_close",
					Arguments: arguments,
				},
			}},
		},
	}
}

func fakeNetworkDownloadResponse(userText string) Response {
	targetURL := ""
	if markerIndex := strings.Index(userText, "tma.verify_network_download"); markerIndex >= 0 {
		targetURL = strings.TrimSpace(userText[markerIndex+len("tma.verify_network_download"):])
	}
	if targetURL == "" {
		targetURL = "https://example.com/"
	}
	code := fmt.Sprintf(`import urllib.request
url = %q
with urllib.request.urlopen(url, timeout=10) as response:
    data = response.read(128)
    print("tma-network-download-ok")
    print("status=%%s" %% getattr(response, "status", ""))
    print("bytes=%%d" %% len(data))
`, targetURL)
	arguments, _ := json.Marshal(map[string]any{
		"language": "python3",
		"code":     code,
		"work_dir": ".",
	})
	return Response{
		Message: Message{
			Role: "assistant",
			Content: []ContentPart{{
				Type: "text",
				Text: "Running network download verification code.",
			}},
			ToolCalls: []ToolCall{{
				ID:   "call_verify_network_download",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "default_execute_code",
					Arguments: arguments,
				},
			}},
		},
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

type OpenAICompatibleClient struct {
	BaseURL        string
	APIKey         string
	Client         *http.Client
	MaxAttempts    int
	RetryBaseDelay time.Duration
}

func (c OpenAICompatibleClient) Generate(ctx context.Context, request Request) (Response, error) {
	return c.generate(ctx, request, false, nil)
}

func (c OpenAICompatibleClient) GenerateStream(ctx context.Context, request Request, onDelta func(Delta) error) (Response, error) {
	return c.generate(ctx, request, true, onDelta)
}

func (c OpenAICompatibleClient) generate(ctx context.Context, request Request, stream bool, onDelta func(Delta) error) (Response, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		return Response{}, fmt.Errorf("llm model is required")
	}

	body, err := json.Marshal(openAIChatRequest{
		Model:         model,
		MaxTokens:     request.MaxOutputTokens,
		Messages:      openAIMessages(request.Messages),
		Tools:         openAITools(request.Tools),
		Stream:        stream,
		StreamOptions: openAIStreamOptionsForRequest(stream),
	})
	if err != nil {
		return Response{}, fmt.Errorf("encode openai-compatible request: %w", err)
	}

	httpClient := c.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResponse, err := c.sendWithRetry(ctx, httpClient, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", body)
	if err != nil {
		return Response{}, err
	}
	defer httpResponse.Body.Close()
	if stream {
		return decodeOpenAIStream(httpResponse.Body, onDelta)
	}

	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, 1<<20))
	if err != nil {
		return Response{}, fmt.Errorf("read openai-compatible response: %w", err)
	}

	var decoded openAIChatResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode openai-compatible response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("openai-compatible response has no choices")
	}

	return Response{
		Message: Message{
			Role:      defaultString(decoded.Choices[0].Message.Role, "assistant"),
			ToolCalls: toolCallsFromOpenAI(decoded.Choices[0].Message.ToolCalls),
			Content: []ContentPart{{
				Type: "text",
				Text: openAIContentText(decoded.Choices[0].Message.Content),
			}},
		},
		Reasoning: reasoningParts(openAIReasoningText(decoded.Choices[0].Message)),
		Usage:     openAIUsage(decoded.Usage),
	}, nil
}

func (c OpenAICompatibleClient) sendWithRetry(ctx context.Context, httpClient *http.Client, endpoint string, body []byte) (*http.Response, error) {
	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultProviderMaxAttempts
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create openai-compatible request: %w", err)
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)

		httpResponse, err := httpClient.Do(httpRequest)
		if err != nil {
			classified := classifyProviderTransportError(ctx, err)
			setProviderErrorAttempt(classified, attempt, 0)
			if attempt == maxAttempts || !isRetryableProviderError(classified) {
				return nil, classified
			}
			if err := c.waitForRetry(ctx, attempt, 0, false); err != nil {
				return nil, err
			}
			continue
		}
		if httpResponse.StatusCode >= 200 && httpResponse.StatusCode < 300 {
			return httpResponse, nil
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, 1<<20))
		httpResponse.Body.Close()
		classified := classifyProviderHTTPError(httpResponse.StatusCode, responseBody)
		retryAfter, hasRetryAfter := parseProviderRetryAfter(httpResponse.Header.Get("Retry-After"), time.Now())
		setProviderErrorAttempt(classified, attempt, retryAfter)
		if readErr != nil {
			if providerError, ok := classified.(*ProviderError); ok {
				providerError.Message = "read provider error response: " + readErr.Error()
				providerError.Cause = readErr
			}
		}
		if attempt == maxAttempts || !isRetryableProviderError(classified) {
			return nil, classified
		}
		if err := c.waitForRetry(ctx, attempt, retryAfter, hasRetryAfter); err != nil {
			return nil, err
		}
	}
	return nil, &ProviderError{Class: ErrorClassUnknown, Attempts: maxAttempts, Message: "provider retries exhausted"}
}

func setProviderErrorAttempt(err error, attempt int, retryAfter time.Duration) {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		providerError.Attempts = attempt
		providerError.RetryAfter = retryAfter
	}
}

func isRetryableProviderError(err error) bool {
	var providerError *ProviderError
	return errors.As(err, &providerError) && providerError.Retryable
}

func (c OpenAICompatibleClient) waitForRetry(ctx context.Context, attempt int, retryAfter time.Duration, hasRetryAfter bool) error {
	delay := retryAfter
	if !hasRetryAfter {
		baseDelay := c.RetryBaseDelay
		if baseDelay <= 0 {
			baseDelay = DefaultProviderRetryBaseDelay
		}
		delay = baseDelay * time.Duration(1<<min(attempt-1, 16))
	}
	delay = min(delay, MaxProviderRetryDelay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseProviderRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, MaxProviderRetryDelay), true
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return min(max(retryAt.Sub(now), 0), MaxProviderRetryDelay), true
	}
	return 0, false
}

func classifyProviderTransportError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ProviderError{Class: ErrorClassTimeout, Retryable: true, Message: err.Error(), Cause: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &ProviderError{Class: ErrorClassUnknown, Retryable: false, Message: err.Error(), Cause: err}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &ProviderError{Class: ErrorClassTimeout, Retryable: true, Message: err.Error(), Cause: err}
	}
	return &ProviderError{Class: ErrorClassServer, Retryable: true, Message: err.Error(), Cause: err}
}

func classifyProviderHTTPError(statusCode int, body []byte) error {
	message, code, errorType := decodeProviderError(body)
	searchable := strings.ToLower(strings.Join([]string{message, code, errorType}, " "))
	if strings.Contains(searchable, "context_length") ||
		strings.Contains(searchable, "context length") ||
		strings.Contains(searchable, "maximum context") ||
		strings.Contains(searchable, "too many tokens") {
		return &ProviderError{Class: ErrorClassContextLength, StatusCode: statusCode, Retryable: false, Message: message}
	}

	class := ErrorClassUnknown
	retryable := false
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		class = ErrorClassAuth
	case statusCode == http.StatusTooManyRequests:
		class = ErrorClassRateLimit
		retryable = true
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusGatewayTimeout:
		class = ErrorClassTimeout
		retryable = true
	case statusCode == http.StatusConflict || statusCode == http.StatusTooEarly || statusCode >= 500:
		class = ErrorClassServer
		retryable = true
	case statusCode >= 400 && statusCode < 500:
		class = ErrorClassInvalidRequest
	}
	return &ProviderError{Class: class, StatusCode: statusCode, Retryable: retryable, Message: message}
}

func decodeProviderError(body []byte) (message string, code string, errorType string) {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		message = strings.TrimSpace(envelope.Error.Message)
		errorType = strings.TrimSpace(envelope.Error.Type)
		if envelope.Error.Code != nil {
			code = strings.TrimSpace(fmt.Sprint(envelope.Error.Code))
		}
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = "provider returned an empty error response"
	}
	return message, code, errorType
}

type openAIChatRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
	Messages      []openAIMessage      `json:"messages"`
	Tools         []openAITool         `json:"tools,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          json.RawMessage  `json:"content,omitempty"`
	ReasoningContent json.RawMessage  `json:"reasoning_content,omitempty"`
	ReasoningDetails json.RawMessage  `json:"reasoning_details,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	Index    int                    `json:"index,omitempty"`
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage openAIUsageResponse `json:"usage"`
}

type openAIStreamResponse struct {
	Choices []struct {
		Delta        openAIMessage `json:"delta"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsageResponse `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type openAIUsageResponse struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func decodeOpenAIStream(reader io.Reader, onDelta func(Delta) error) (Response, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var builder strings.Builder
	var reasoningBuilder strings.Builder
	var pendingReasoning strings.Builder
	index := 0
	role := "assistant"
	var usage Usage
	stopped := false
	type streamedToolCall struct {
		id        string
		typeName  string
		name      strings.Builder
		arguments strings.Builder
	}
	var streamedToolCalls []*streamedToolCall
	emitDelta := func(delta Delta) error {
		index++
		delta.Index = index
		if onDelta == nil {
			return nil
		}
		return onDelta(delta)
	}
	emitStreamFailure := func(providerError *ProviderError) error {
		if err := emitDelta(Delta{Kind: DeltaKindError, Error: streamErrorFromProviderError(providerError)}); err != nil {
			return err
		}
		return providerError
	}
	flushReasoning := func() error {
		if pendingReasoning.Len() == 0 {
			return nil
		}
		text := pendingReasoning.String()
		pendingReasoning.Reset()
		return emitDelta(Delta{Kind: DeltaKindReasoning, Text: text})
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if err := flushReasoning(); err != nil {
				return Response{}, err
			}
			if !stopped {
				if err := emitDelta(Delta{Kind: DeltaKindStop, FinishReason: "done"}); err != nil {
					return Response{}, err
				}
				stopped = true
			}
			break
		}

		var chunk openAIStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			providerError := &ProviderError{
				Class:    ErrorClassUnknown,
				Attempts: 1,
				Message:  "decode openai-compatible stream chunk: " + err.Error(),
				Cause:    err,
			}
			return Response{}, emitStreamFailure(providerError)
		}
		if chunk.Error != nil {
			if err := flushReasoning(); err != nil {
				return Response{}, err
			}
			providerError := classifyProviderStreamError(chunk.Error.Message, chunk.Error.Type, chunk.Error.Code)
			if err := emitDelta(Delta{Kind: DeltaKindError, Error: streamErrorFromProviderError(providerError)}); err != nil {
				return Response{}, err
			}
			return Response{}, providerError
		}
		if chunk.Usage != nil {
			usage = openAIUsage(*chunk.Usage)
			usageDelta := usage
			if err := emitDelta(Delta{Kind: DeltaKindUsage, Usage: &usageDelta}); err != nil {
				return Response{}, err
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Role != "" {
				role = choice.Delta.Role
			}
			reasoningText := openAIReasoningText(choice.Delta)
			if reasoningText != "" {
				reasoningBuilder.WriteString(reasoningText)
				pendingReasoning.WriteString(reasoningText)
			}
			if len(choice.Delta.ToolCalls) > 0 {
				if err := flushReasoning(); err != nil {
					return Response{}, err
				}
			}
			for _, partialCall := range choice.Delta.ToolCalls {
				for len(streamedToolCalls) <= partialCall.Index {
					streamedToolCalls = append(streamedToolCalls, &streamedToolCall{})
				}
				call := streamedToolCalls[partialCall.Index]
				if partialCall.ID != "" {
					call.id = partialCall.ID
				}
				if partialCall.Type != "" {
					call.typeName = partialCall.Type
				}
				call.name.WriteString(partialCall.Function.Name)
				call.arguments.WriteString(partialCall.Function.Arguments)
				if err := emitDelta(Delta{Kind: DeltaKindToolCall, ToolCall: &ToolCallDelta{
					Index:     partialCall.Index,
					ID:        partialCall.ID,
					Type:      partialCall.Type,
					Name:      partialCall.Function.Name,
					Arguments: partialCall.Function.Arguments,
				}}); err != nil {
					return Response{}, err
				}
			}
			text := openAIContentText(choice.Delta.Content)
			if text != "" {
				if err := flushReasoning(); err != nil {
					return Response{}, err
				}
				builder.WriteString(text)
				if err := emitDelta(Delta{Kind: DeltaKindText, Text: text}); err != nil {
					return Response{}, err
				}
			}
			if choice.FinishReason != "" {
				if err := flushReasoning(); err != nil {
					return Response{}, err
				}
				if err := emitDelta(Delta{Kind: DeltaKindStop, FinishReason: choice.FinishReason}); err != nil {
					return Response{}, err
				}
				stopped = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		providerError := &ProviderError{
			Class:     ErrorClassServer,
			Retryable: true,
			Attempts:  1,
			Message:   "read openai-compatible stream: " + err.Error(),
			Cause:     err,
		}
		return Response{}, emitStreamFailure(providerError)
	}
	if err := flushReasoning(); err != nil {
		return Response{}, err
	}

	toolCalls := make([]openAIToolCall, 0, len(streamedToolCalls))
	for _, streamedCall := range streamedToolCalls {
		if streamedCall == nil || (streamedCall.id == "" && streamedCall.name.Len() == 0) {
			continue
		}
		toolCalls = append(toolCalls, openAIToolCall{
			ID:   streamedCall.id,
			Type: streamedCall.typeName,
			Function: openAIToolCallFunction{
				Name:      streamedCall.name.String(),
				Arguments: streamedCall.arguments.String(),
			},
		})
	}
	content := []ContentPart(nil)
	if builder.Len() > 0 {
		content = []ContentPart{{Type: "text", Text: builder.String()}}
	}

	return Response{
		Message: Message{
			Role:      role,
			Content:   content,
			ToolCalls: toolCallsFromOpenAI(toolCalls),
		},
		Reasoning: reasoningParts(reasoningBuilder.String()),
		Usage:     usage,
	}, nil
}

func classifyProviderStreamError(message string, errorType string, code any) *ProviderError {
	codeText := strings.TrimSpace(fmt.Sprint(code))
	if code == nil {
		codeText = ""
	}
	searchable := strings.ToLower(strings.Join([]string{message, errorType, codeText}, " "))
	providerError := &ProviderError{Class: ErrorClassUnknown, Attempts: 1, Message: strings.TrimSpace(message)}
	switch {
	case strings.Contains(searchable, "context_length"), strings.Contains(searchable, "context length"), strings.Contains(searchable, "maximum context"), strings.Contains(searchable, "too many tokens"):
		providerError.Class = ErrorClassContextLength
	case strings.Contains(searchable, "authentication"), strings.Contains(searchable, "unauthorized"), strings.Contains(searchable, "invalid_api_key"):
		providerError.Class = ErrorClassAuth
	case strings.Contains(searchable, "rate_limit"), strings.Contains(searchable, "rate limit"), strings.Contains(searchable, "overloaded"):
		providerError.Class = ErrorClassRateLimit
		providerError.Retryable = true
	case strings.Contains(searchable, "timeout"), strings.Contains(searchable, "timed out"):
		providerError.Class = ErrorClassTimeout
		providerError.Retryable = true
	case strings.Contains(searchable, "server_error"), strings.Contains(searchable, "internal error"), strings.Contains(searchable, "unavailable"):
		providerError.Class = ErrorClassServer
		providerError.Retryable = true
	case strings.Contains(searchable, "invalid_request"):
		providerError.Class = ErrorClassInvalidRequest
	}
	if providerError.Message == "" {
		providerError.Message = "provider returned a stream error"
	}
	return providerError
}

func streamErrorFromProviderError(providerError *ProviderError) *StreamError {
	return &StreamError{
		Class:      providerError.Class,
		StatusCode: providerError.StatusCode,
		Retryable:  providerError.Retryable,
		Message:    providerError.Message,
	}
}

func reasoningParts(text string) []ReasoningPart {
	if text == "" {
		return nil
	}
	return []ReasoningPart{{Type: DeltaKindReasoning, Text: text}}
}

func openAIReasoningText(message openAIMessage) string {
	if text := openAIContentText(message.ReasoningContent); text != "" {
		return text
	}
	return openAIReasoningDetailsText(message.ReasoningDetails)
}

func openAIReasoningDetailsText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	type reasoningDetail struct {
		Summary json.RawMessage `json:"summary"`
		Text    json.RawMessage `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	var details []reasoningDetail
	if err := json.Unmarshal(raw, &details); err != nil {
		var detail reasoningDetail
		if json.Unmarshal(raw, &detail) != nil {
			return ""
		}
		details = []reasoningDetail{detail}
	}
	values := make([]string, 0, len(details))
	for _, detail := range details {
		for _, field := range []json.RawMessage{detail.Summary, detail.Text, detail.Content} {
			if text := openAIContentText(field); text != "" {
				values = append(values, text)
				break
			}
		}
	}
	return strings.Join(values, "")
}

func openAIStreamOptionsForRequest(stream bool) *openAIStreamOptions {
	if !stream {
		return nil
	}
	return &openAIStreamOptions{IncludeUsage: true}
}

func openAIUsage(usage openAIUsageResponse) Usage {
	total := usage.TotalTokens
	if total == 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	return Usage{
		InputTokens:       usage.PromptTokens,
		OutputTokens:      usage.CompletionTokens,
		TotalTokens:       total,
		CachedInputTokens: usage.PromptTokensDetails.CachedTokens,
		ReasoningTokens:   usage.CompletionTokensDetails.ReasoningTokens,
	}
}

func openAIMessages(messages []Message) []openAIMessage {
	result := make([]openAIMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, openAIMessage{
			Role:       message.Role,
			Content:    openAIContent(message.Content),
			ToolCalls:  openAIToolCalls(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		})
	}
	return result
}

func openAIContent(parts []ContentPart) json.RawMessage {
	hasImage := false
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		encoded, _ := json.Marshal(textContent(parts))
		return encoded
	}
	content := make([]ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			content = append(content, ContentPart{Type: "text", Text: part.Text})
		}
		if part.Type == "image_url" && part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
			content = append(content, part)
		}
	}
	encoded, _ := json.Marshal(content)
	return encoded
}

func openAITools(tools []Tool) []openAITool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, openAITool{
			Type: defaultString(tool.Type, "function"),
			Function: openAIToolFunction{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  openAICompatibleToolParameters(tool.Function.Parameters),
			},
		})
	}
	return result
}

func openAICompatibleToolParameters(parameters json.RawMessage) json.RawMessage {
	if len(parameters) == 0 {
		return parameters
	}
	var schema any
	if err := json.Unmarshal(parameters, &schema); err != nil {
		return parameters
	}
	stripUnsupportedToolSchemaKeywords(schema)
	encoded, err := json.Marshal(schema)
	if err != nil {
		return parameters
	}
	return encoded
}

func stripUnsupportedToolSchemaKeywords(value any) {
	switch current := value.(type) {
	case map[string]any:
		for _, keyword := range []string{"anyOf", "oneOf", "allOf", "if", "then", "else", "const"} {
			delete(current, keyword)
		}
		for _, child := range current {
			stripUnsupportedToolSchemaKeywords(child)
		}
	case []any:
		for _, child := range current {
			stripUnsupportedToolSchemaKeywords(child)
		}
	}
}

func openAIToolCalls(calls []ToolCall) []openAIToolCall {
	if len(calls) == 0 {
		return nil
	}
	result := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, openAIToolCall{
			ID:   call.ID,
			Type: defaultString(call.Type, "function"),
			Function: openAIToolCallFunction{
				Name:      call.Function.Name,
				Arguments: string(call.Function.Arguments),
			},
		})
	}
	return result
}

func toolCallsFromOpenAI(calls []openAIToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		arguments := json.RawMessage(strings.TrimSpace(call.Function.Arguments))
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		result = append(result, ToolCall{
			ID:   call.ID,
			Type: defaultString(call.Type, "function"),
			Function: ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: arguments,
			},
		})
	}
	return result
}

func openAIContentText(content json.RawMessage) string {
	if len(content) == 0 || string(content) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []ContentPart
	if json.Unmarshal(content, &parts) == nil {
		return textContent(parts)
	}
	return ""
}

func textContent(parts []ContentPart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

func lastUserText(messages []Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != "user" {
			continue
		}
		for _, part := range message.Content {
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				return part.Text
			}
		}
	}
	return ""
}

func lastToolText(messages []Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != "tool" {
			continue
		}
		for _, part := range message.Content {
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				return part.Text
			}
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
