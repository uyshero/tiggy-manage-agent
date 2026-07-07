package agentruntime

import (
	"context"
	"encoding/json"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

const DemoProtocolVersion = "tma.agent_runtime.demo.v1"

type Step struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// TurnRequest 是 AgentRuntime 执行一轮对话所需的最小输入。
type TurnRequest struct {
	SessionID   string
	TurnID      string
	UserPayload json.RawMessage
	History     []managedagents.ConversationMessage
	Config      Config
	EmitStep    func(context.Context, Step) error
}

type TurnResult struct {
	AgentPayload json.RawMessage
	Usage        llm.Usage
	Provider     string
	ProviderType string
	Model        string
}

type Config struct {
	LLMProvider     string
	LLMProviderType string
	LLMModel        string
	LLMBaseURL      string
	LLMAPIKey       string
	System          string
	Tools           json.RawMessage
	Skills          json.RawMessage
}

// Runtime 负责把一次 user.message 转换为 agent.message payload。
// 后续 LLM loop、tool calling 和 sandbox 编排都会收敛到这一层。
type Runtime interface {
	RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error)
}

// DemoRuntime 是当前内置的最小 AgentRuntime。
// 它通过可替换的 LLM Client 生成回复；默认使用 FakeClient，不调用外部模型。
type DemoRuntime struct {
	Client llm.Client
	Model  string
}

func (runtime DemoRuntime) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	default:
	}

	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeStarted,
		Message: "Demo runtime started.",
	}); err != nil {
		return TurnResult{}, err
	}

	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeThinking,
		Message: "Reading user message.",
	}); err != nil {
		return TurnResult{}, err
	}

	client := runtime.Client
	if client == nil {
		client = llm.FakeClient{}
	}
	provider := currentProvider(client, request.Config.LLMProvider)
	model := currentModel(client, defaultString(request.Config.LLMModel, runtime.Model))

	llmRequest := llm.Request{
		Provider:     provider,
		ProviderType: request.Config.LLMProviderType,
		Model:        model,
		BaseURL:      request.Config.LLMBaseURL,
		APIKey:       request.Config.LLMAPIKey,
		Messages:     llmMessages(request),
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeLLMRequest,
		Message: "Sending request to LLM client.",
		Data: map[string]any{
			"provider":      provider,
			"provider_type": request.Config.LLMProviderType,
			"model":         model,
			"base_url":      request.Config.LLMBaseURL,
			"message_count": len(llmRequest.Messages),
		},
	}); err != nil {
		return TurnResult{}, err
	}

	llmResponse, err := generateLLM(ctx, client, llmRequest, request)
	if err != nil {
		return TurnResult{}, err
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeLLMResponse,
		Message: "Received response from LLM client.",
		Data: map[string]any{
			"role":          llmResponse.Message.Role,
			"content_count": len(llmResponse.Message.Content),
			"usage":         llmResponse.Usage,
		},
	}); err != nil {
		return TurnResult{}, err
	}

	encoded, err := json.Marshal(map[string]any{
		"protocol_version": DemoProtocolVersion,
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return TurnResult{
			AgentPayload: json.RawMessage(`{"protocol_version":"tma.agent_runtime.demo.v1","content":[{"type":"text","text":"Agent runtime received your message."}]}`),
			Usage:        llmResponse.Usage,
			Provider:     provider,
			ProviderType: request.Config.LLMProviderType,
			Model:        model,
		}, nil
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeCompleted,
		Message: "Demo runtime completed.",
	}); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{
		AgentPayload: encoded,
		Usage:        llmResponse.Usage,
		Provider:     provider,
		ProviderType: request.Config.LLMProviderType,
		Model:        model,
	}, nil
}

func generateLLM(ctx context.Context, client llm.Client, llmRequest llm.Request, turnRequest TurnRequest) (llm.Response, error) {
	streamingClient, ok := client.(llm.StreamingClient)
	if !ok {
		return client.Generate(ctx, llmRequest)
	}

	return streamingClient.GenerateStream(ctx, llmRequest, func(delta llm.Delta) error {
		if delta.Text == "" {
			return nil
		}
		return emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeLLMDelta,
			Message: "Received streamed LLM text.",
			Data: map[string]any{
				"index": delta.Index,
				"text":  delta.Text,
			},
		})
	})
}

func emitStep(ctx context.Context, request TurnRequest, step Step) error {
	if request.EmitStep == nil {
		return nil
	}
	return request.EmitStep(ctx, step)
}

func userContent(payload json.RawMessage) []llm.ContentPart {
	return []llm.ContentPart{{
		Type: "text",
		Text: firstTextContent(payload),
	}}
}

func llmMessages(request TurnRequest) []llm.Message {
	messages := make([]llm.Message, 0, len(request.History)+2)
	if request.Config.System != "" {
		messages = append(messages, llm.Message{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: request.Config.System,
			}},
		})
	}
	for _, history := range request.History {
		if history.Role != "user" && history.Role != "assistant" {
			continue
		}
		content := userContent(history.Payload)
		if len(content) == 0 || content[0].Text == "" {
			continue
		}
		messages = append(messages, llm.Message{
			Role:    history.Role,
			Content: content,
		})
	}
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: userContent(request.UserPayload),
	})
	return messages
}

func firstTextContent(payload json.RawMessage) string {
	var object struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	for _, content := range object.Content {
		if content.Type == "text" && content.Text != "" {
			return content.Text
		}
	}
	return ""
}

type llmConfigSource interface {
	CurrentConfig() (string, string)
}

// currentModel 优先使用显式 Runtime 配置；没有显式配置时读取 LLM Manager 当前模型。
func currentModel(client llm.Client, fallback string) string {
	if fallback != "" {
		return fallback
	}
	if source, ok := client.(llmConfigSource); ok {
		_, model := source.CurrentConfig()
		if model != "" {
			return model
		}
	}
	return llm.DefaultModel
}

func currentProvider(client llm.Client, fallback string) string {
	if fallback != "" {
		return fallback
	}
	if source, ok := client.(llmConfigSource); ok {
		provider, _ := source.CurrentConfig()
		if provider != "" {
			return provider
		}
	}
	return llm.ProviderFake
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
