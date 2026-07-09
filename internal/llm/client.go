package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	ProviderFake             = "fake"
	ProviderOpenAICompatible = "openai-compatible"
	ProviderTypeOpenAI       = "openai"
	DefaultModel             = "fake-demo"
	DefaultOpenAIBaseURL     = "https://api.openai.com/v1"
)

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type Message struct {
	Role       string        `json:"role"`
	Content    []ContentPart `json:"content"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type Request struct {
	Provider     string    `json:"provider,omitempty"`
	ProviderType string    `json:"-"`
	Model        string    `json:"model,omitempty"`
	BaseURL      string    `json:"-"`
	APIKey       string    `json:"-"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

type Response struct {
	Message Message `json:"message"`
	Usage   Usage   `json:"usage,omitempty"`
}

type Delta struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
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
	Provider     string
	ProviderType string
	Model        string
	BaseURL      string
	APIKey       string
}

// FakeProvider 是当前默认 Provider，不访问外部模型 API。
type FakeProvider struct{}

func (FakeProvider) NewClient(string) (Client, error) {
	return FakeClient{}, nil
}

// OpenAICompatibleProvider 适配 OpenAI Chat Completions 兼容接口。
type OpenAICompatibleProvider struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
	Label   string
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
		BaseURL: baseURL,
		APIKey:  p.APIKey,
		Client:  httpClient,
	}, nil
}

// Manager 持有当前 Provider / Model，并把调用转发给当前 Client。
// 未来做热切换时，只需要 Switch 当前配置，Runner 和 Runtime 不需要重建。
type Manager struct {
	mu        sync.RWMutex
	provider  string
	model     string
	client    Client
	providers map[string]Provider
}

func NewManager(provider string, model string) (*Manager, error) {
	return NewManagerWithConfig(ManagerConfig{Provider: provider, Model: model})
}

func NewManagerWithConfig(config ManagerConfig) (*Manager, error) {
	manager := &Manager{
		providers: providersFromConfig(config),
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
			BaseURL: config.BaseURL,
			APIKey:  config.APIKey,
			Label:   ProviderOpenAICompatible,
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
			BaseURL: config.BaseURL,
			APIKey:  config.APIKey,
			Label:   providerID,
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
		client, err := clientFromProviderConfig(provider, request.ProviderType, model, request.BaseURL, request.APIKey)
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

func clientFromProviderConfig(provider string, providerType string, model string, baseURL string, apiKey string) (Client, error) {
	resolvedType := ResolveProviderType(provider, providerType)
	if resolvedType == ProviderFake {
		return FakeProvider{}.NewClient(model)
	}
	if isOpenAIProviderType(resolvedType) {
		return OpenAICompatibleProvider{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Label:   provider,
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

	if toolText := lastToolText(request.Messages); containsAny(toolText, "tma-session-tool-ok", "tma-upload-sync-ok", "tma-session-data-seeded", "tma-session-data-persisted", "tma-worker-export-ok", "tma-worker-large-export-ok", "tma-web-search-ok", "tma-web-crawl-ok") {
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
		if strings.Contains(userText, "tma.verify_web_crawl") {
			return fakeWebCrawlResponse(userText), nil
		}
		if strings.Contains(userText, "tma.verify_web_search") {
			return fakeWebSearchResponse(userText), nil
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
					Name:      "default.run_command",
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
					Name:      "default.run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","for f in /mnt/data/uploads/*/*; do [ -f \"$f\" ] && cat \"$f\"; done; printf '\\n'; printf tma-session-data-seeded > /mnt/data/state.txt; cat /mnt/data/state.txt"],"work_dir":"."}`),
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
					Name:      "default.run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","for f in /mnt/data/uploads/*/*; do [ -f \"$f\" ] && cat \"$f\"; done; printf '\\n'; cat /mnt/data/state.txt; printf '\\n'; printf tma-session-data-persisted"],"work_dir":"."}`),
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
					Name:      "default.run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","mkdir -p /mnt/data/outputs && { for f in /mnt/data/uploads/*/*; do [ -f \"$f\" ] && cat \"$f\"; done; printf 'tma-session-output-exported\\n'; } > /mnt/data/outputs/export.txt && cat /mnt/data/outputs/export.txt"],"work_dir":".","output_paths":["/mnt/data/outputs/export.txt"]}`),
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
					Name:      "default.run_command",
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
					Name:      "default.run_command",
					Arguments: json.RawMessage(`{"command":"sh","args":["-c","printf 'tma-worker-large-export-ok\n' > worker-large-export.txt && dd if=/dev/zero bs=1048576 count=9 >> worker-large-export.txt 2>/dev/null && printf tma-worker-large-export-ok"],"work_dir":".","output_paths":["worker-large-export.txt"]}`),
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
					Name:      "web.crawl",
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
					Name:      "web.search",
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
					Name:      "default.execute_code",
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
	BaseURL string
	APIKey  string
	Client  *http.Client
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
		Messages:      openAIMessages(request.Messages),
		Tools:         openAITools(request.Tools),
		Stream:        stream,
		StreamOptions: openAIStreamOptionsForRequest(stream),
	})
	if err != nil {
		return Response{}, fmt.Errorf("encode openai-compatible request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("create openai-compatible request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)

	httpClient := c.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("call openai-compatible chat completions: %w", err)
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, 1<<20))
		if err != nil {
			return Response{}, fmt.Errorf("read openai-compatible error response: %w", err)
		}
		return Response{}, fmt.Errorf("openai-compatible chat completions returned %s: %s", httpResponse.Status, strings.TrimSpace(string(responseBody)))
	}
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
		Usage: openAIUsage(decoded.Usage),
	}, nil
}

type openAIChatRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	Tools         []openAITool         `json:"tools,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
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
		Delta openAIMessage `json:"delta"`
	} `json:"choices"`
	Usage openAIUsageResponse `json:"usage"`
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
	index := 0
	role := "assistant"
	var usage Usage
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
			break
		}

		var chunk openAIStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Response{}, fmt.Errorf("decode openai-compatible stream chunk: %w", err)
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = openAIUsage(chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Role != "" {
				role = choice.Delta.Role
			}
			text := openAIContentText(choice.Delta.Content)
			if text == "" {
				continue
			}
			index++
			builder.WriteString(text)
			if onDelta != nil {
				if err := onDelta(Delta{Index: index, Text: text}); err != nil {
					return Response{}, err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Response{}, fmt.Errorf("read openai-compatible stream: %w", err)
	}

	return Response{
		Message: Message{
			Role: role,
			Content: []ContentPart{{
				Type: "text",
				Text: builder.String(),
			}},
		},
		Usage: usage,
	}, nil
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
		content := textContent(message.Content)
		result = append(result, openAIMessage{
			Role:       message.Role,
			Content:    &content,
			ToolCalls:  openAIToolCalls(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		})
	}
	return result
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
				Parameters:  tool.Function.Parameters,
			},
		})
	}
	return result
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

func openAIContentText(content *string) string {
	if content == nil {
		return ""
	}
	return *content
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
