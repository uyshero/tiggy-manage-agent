package model

import "encoding/json"

func CloneMessage(message Message) Message {
	cloned := message
	cloned.Content = CloneContent(message.Content)
	cloned.Metadata = cloneRaw(message.Metadata)
	return cloned
}

func CloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	cloned := make([]Message, len(messages))
	for index, message := range messages {
		cloned[index] = CloneMessage(message)
	}
	return cloned
}

func CloneContent(content []Content) []Content {
	if content == nil {
		return nil
	}
	cloned := make([]Content, len(content))
	for index, part := range content {
		cloned[index] = part
		if part.Image != nil {
			image := *part.Image
			cloned[index].Image = &image
		}
		if part.Thinking != nil {
			thinking := *part.Thinking
			cloned[index].Thinking = &thinking
		}
		if part.ToolCall != nil {
			call := *part.ToolCall
			call.Arguments = cloneRaw(call.Arguments)
			cloned[index].ToolCall = &call
		}
		if part.ToolResult != nil {
			result := *part.ToolResult
			result.Content = CloneContent(result.Content)
			result.State = cloneRaw(result.State)
			cloned[index].ToolResult = &result
		}
	}
	return cloned
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
