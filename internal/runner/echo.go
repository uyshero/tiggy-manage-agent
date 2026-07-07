package runner

import (
	"context"
	"encoding/json"
)

// EchoExecutor 是最小可运行 TurnExecutor，用来验证 WorkerRunner 的完整链路。
// 它不调用模型，只把用户第一段文本包装成 agent.message payload。
type EchoExecutor struct{}

func (EchoExecutor) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	default:
	}

	text := "Echo Agent received your message."
	if userText := firstTextContent(request.UserPayload); userText != "" {
		text = "Echo Agent received: " + userText
	}

	encoded, err := json.Marshal(map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
	})
	if err != nil {
		return TurnResult{AgentPayload: json.RawMessage(`{"content":[{"type":"text","text":"Echo Agent received your message."}]}`)}, nil
	}
	return TurnResult{AgentPayload: encoded}, nil
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
