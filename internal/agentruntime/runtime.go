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
	EmitStep    func(context.Context, Step) error
}

// Runtime 负责把一次 user.message 转换为 agent.message payload。
// 后续 LLM loop、tool calling 和 sandbox 编排都会收敛到这一层。
type Runtime interface {
	RunTurn(ctx context.Context, request TurnRequest) (json.RawMessage, error)
}

// DemoRuntime 是当前内置的最小 AgentRuntime。
// 它通过可替换的 LLM Client 生成回复；默认使用 FakeClient，不调用外部模型。
type DemoRuntime struct {
	Client llm.Client
	Model  string
}

func (runtime DemoRuntime) RunTurn(ctx context.Context, request TurnRequest) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeStarted,
		Message: "Demo runtime started.",
	}); err != nil {
		return nil, err
	}

	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeThinking,
		Message: "Reading user message.",
	}); err != nil {
		return nil, err
	}

	client := runtime.Client
	if client == nil {
		client = llm.FakeClient{}
	}
	model := runtime.Model
	if model == "" {
		model = "fake-demo"
	}

	llmRequest := llm.Request{
		Model: model,
		Messages: []llm.Message{{
			Role:    "user",
			Content: userContent(request.UserPayload),
		}},
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeLLMRequest,
		Message: "Sending request to LLM client.",
		Data: map[string]any{
			"model":         model,
			"message_count": len(llmRequest.Messages),
		},
	}); err != nil {
		return nil, err
	}

	llmResponse, err := client.Generate(ctx, llmRequest)
	if err != nil {
		return nil, err
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeLLMResponse,
		Message: "Received response from LLM client.",
		Data: map[string]any{
			"role":          llmResponse.Message.Role,
			"content_count": len(llmResponse.Message.Content),
		},
	}); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(map[string]any{
		"protocol_version": DemoProtocolVersion,
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return json.RawMessage(`{"protocol_version":"tma.agent_runtime.demo.v1","content":[{"type":"text","text":"Agent runtime received your message."}]}`), nil
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeCompleted,
		Message: "Demo runtime completed.",
	}); err != nil {
		return nil, err
	}
	return encoded, nil
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
