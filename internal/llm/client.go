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
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

type Request struct {
	Provider     string    `json:"provider,omitempty"`
	ProviderType string    `json:"-"`
	Model        string    `json:"model,omitempty"`
	BaseURL      string    `json:"-"`
	APIKey       string    `json:"-"`
	Messages     []Message `json:"messages"`
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

	text := "Agent runtime received your message."
	if userText := lastUserText(request.Messages); userText != "" {
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
		Model:    model,
		Messages: openAIMessages(request.Messages),
		Stream:   stream,
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
			Role: defaultString(decoded.Choices[0].Message.Role, "assistant"),
			Content: []ContentPart{{
				Type: "text",
				Text: decoded.Choices[0].Message.Content,
			}},
		},
	}, nil
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}

type openAIStreamResponse struct {
	Choices []struct {
		Delta openAIMessage `json:"delta"`
	} `json:"choices"`
}

func decodeOpenAIStream(reader io.Reader, onDelta func(Delta) error) (Response, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var builder strings.Builder
	index := 0
	role := "assistant"
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
		for _, choice := range chunk.Choices {
			if choice.Delta.Role != "" {
				role = choice.Delta.Role
			}
			text := choice.Delta.Content
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
	}, nil
}

func openAIMessages(messages []Message) []openAIMessage {
	result := make([]openAIMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, openAIMessage{
			Role:    message.Role,
			Content: textContent(message.Content),
		})
	}
	return result
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

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
