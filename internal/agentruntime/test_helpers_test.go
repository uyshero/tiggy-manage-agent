package agentruntime

import (
	"strings"

	"tiggy-manage-agent/internal/llm"
)

func textResponse(text string) llm.Response {
	return llm.Response{Message: llm.Message{
		Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: text}},
	}}
}

func messagesText(messages []llm.Message) string {
	parts := make([]string, 0)
	for _, message := range messages {
		for _, content := range message.Content {
			if content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
