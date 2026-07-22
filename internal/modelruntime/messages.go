package modelruntime

import (
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/llm"
	coremodel "tiggy-manage-agent/internal/model"
)

func MessagesFromLLM(messages []llm.Message) ([]coremodel.Message, error) {
	converted := make([]coremodel.Message, 0, len(messages))
	for index, message := range messages {
		role := coremodel.Role(message.Role)
		switch role {
		case coremodel.RoleSystem, coremodel.RoleUser, coremodel.RoleAssistant, coremodel.RoleTool:
		default:
			return nil, fmt.Errorf("unsupported llm message role %q", message.Role)
		}
		visibility := coremodel.VisibilityPublic
		if role == coremodel.RoleSystem || role == coremodel.RoleTool {
			visibility = coremodel.VisibilityInternal
		}
		content := make([]coremodel.Content, 0, len(message.Content)+len(message.ToolCalls))
		for _, part := range message.Content {
			switch part.Type {
			case "", "text":
				if part.Text != "" {
					content = append(content, coremodel.Content{Type: coremodel.ContentText, Text: part.Text})
				}
			case "image_url":
				if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
					return nil, fmt.Errorf("llm message %d contains an empty image URL", index)
				}
				content = append(content, coremodel.Content{Type: coremodel.ContentImage, Image: &coremodel.ImageReference{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}})
			default:
				return nil, fmt.Errorf("unsupported llm content type %q", part.Type)
			}
		}
		for callIndex, call := range message.ToolCalls {
			id := strings.TrimSpace(call.ID)
			if id == "" {
				id = fmt.Sprintf("context_%06d_tool_%06d", index+1, callIndex+1)
			}
			arguments := append(json.RawMessage(nil), call.Function.Arguments...)
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			content = append(content, coremodel.Content{Type: coremodel.ContentToolCall, ToolCall: &coremodel.ToolCall{ID: id, Name: call.Function.Name, Arguments: arguments}})
		}
		if role == coremodel.RoleTool {
			if strings.TrimSpace(message.ToolCallID) == "" {
				return nil, fmt.Errorf("llm tool message %d is missing tool call id", index)
			}
			content = []coremodel.Content{{Type: coremodel.ContentToolResult, ToolResult: &coremodel.ToolResult{
				CallID:  message.ToolCallID,
				Name:    "legacy.tool",
				Content: content,
			}}}
		}
		convertedMessage := coremodel.Message{
			ID:         fmt.Sprintf("context_%06d", index+1),
			Role:       role,
			Visibility: visibility,
			Content:    content,
		}
		if err := convertedMessage.Validate(); err != nil {
			return nil, fmt.Errorf("converted llm message %d: %w", index, err)
		}
		converted = append(converted, convertedMessage)
	}
	return converted, nil
}
