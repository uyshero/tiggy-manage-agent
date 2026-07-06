package llm

import (
	"context"
	"strings"
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
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages"`
}

type Response struct {
	Message Message `json:"message"`
}

// Client 是 AgentRuntime 调用模型的最小边界。
// 当前先用 FakeClient 跑通链路，后续再接具体模型厂商实现。
type Client interface {
	Generate(ctx context.Context, request Request) (Response, error)
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
