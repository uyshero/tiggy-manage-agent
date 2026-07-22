package managedagents

import (
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
)

func TestAgentLoopProgressMessageDataPersistsToolRoundText(t *testing.T) {
	state := agentcore.State{
		ModelAttempts: 2,
		Messages: []model.Message{{
			ID:         "message_2",
			Role:       model.RoleAssistant,
			Visibility: model.VisibilityInternal,
			Content: []model.Content{
				{Type: model.ContentThinking, Thinking: &model.ThinkingBlock{Text: "private reasoning"}},
				{Type: model.ContentText, Text: "I will inspect the current files."},
				{Type: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call_1", Name: "filesystem.read", Arguments: json.RawMessage(`{"path":"App.jsx"}`)}},
			},
		}},
	}

	data, ok := agentLoopProgressMessageData(state, agentcore.RuntimeEvent{Type: agentcore.EventModelResponded})
	if !ok {
		t.Fatal("expected tool-round progress message")
	}
	if got := data["text"]; got != "I will inspect the current files." {
		t.Fatalf("unexpected progress text: %v", got)
	}
	if got := data["message_id"]; got != "message_2" {
		t.Fatalf("unexpected message id: %v", got)
	}
	if got := data["tool_round"]; got != 2 {
		t.Fatalf("unexpected tool round: %v", got)
	}
}

func TestAgentLoopProgressMessageDataSkipsFinalAndToolOnlyResponses(t *testing.T) {
	tests := []model.Message{
		{
			ID: "final", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
			Content: []model.Content{{Type: model.ContentText, Text: "Final answer"}},
		},
		{
			ID: "tool_only", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
			Content: []model.Content{{Type: model.ContentToolCall, ToolCall: &model.ToolCall{ID: "call_1", Name: "filesystem.read", Arguments: json.RawMessage(`{}`)}}},
		},
	}
	for _, message := range tests {
		state := agentcore.State{ModelAttempts: 1, Messages: []model.Message{message}}
		if data, ok := agentLoopProgressMessageData(state, agentcore.RuntimeEvent{Type: agentcore.EventModelResponded}); ok {
			t.Fatalf("did not expect progress message for %s: %#v", message.ID, data)
		}
	}
}
