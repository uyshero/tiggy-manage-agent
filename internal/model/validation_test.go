package model

import (
	"encoding/json"
	"testing"
)

func TestMessageValidateRejectsInvalidContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content Content
	}{
		{name: "empty text", content: Content{Type: ContentText}},
		{name: "missing image", content: Content{Type: ContentImage}},
		{name: "missing thinking", content: Content{Type: ContentThinking}},
		{name: "missing tool call", content: Content{Type: ContentToolCall}},
		{name: "missing tool result", content: Content{Type: ContentToolResult}},
		{name: "unknown type", content: Content{Type: "audio"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			message := Message{ID: "message_1", Role: RoleAssistant, Visibility: VisibilityInternal, Content: []Content{test.content}}
			if err := message.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want content validation error")
			}
		})
	}
}

func TestToolCallValidateRequiresJSONObjectArguments(t *testing.T) {
	t.Parallel()

	for _, arguments := range []string{`null`, `[]`, `"value"`, `42`, `{broken`} {
		call := ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(arguments)}
		if err := call.Validate(); err == nil {
			t.Errorf("Validate() with %q error = nil, want error", arguments)
		}
	}
	for _, arguments := range []string{`{"query":"pi"}`, " \n\t {\"query\":\"pi\"} \r"} {
		valid := ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(arguments)}
		if err := valid.Validate(); err != nil {
			t.Fatalf("Validate() valid object %q error = %v", arguments, err)
		}
	}
}

func TestCloneMessageIsDeeplyIsolated(t *testing.T) {
	t.Parallel()

	original := Message{
		ID:         "message_1",
		Role:       RoleTool,
		Visibility: VisibilityInternal,
		Metadata:   json.RawMessage(`{"source":"original"}`),
		Content: []Content{{
			Type: ContentToolResult,
			ToolResult: &ToolResult{
				CallID: "call_1",
				Name:   "lookup",
				State:  json.RawMessage(`{"cursor":1}`),
				Content: []Content{{
					Type:     ContentToolCall,
					ToolCall: &ToolCall{ID: "nested", Name: "next", Arguments: json.RawMessage(`{"page":2}`)},
				}},
			},
		}},
	}
	cloned := CloneMessage(original)
	cloned.Metadata[2] = 'X'
	cloned.Content[0].ToolResult.State[2] = 'X'
	cloned.Content[0].ToolResult.Content[0].ToolCall.Arguments[2] = 'X'

	if string(original.Metadata) != `{"source":"original"}` || string(original.Content[0].ToolResult.State) != `{"cursor":1}` || string(original.Content[0].ToolResult.Content[0].ToolCall.Arguments) != `{"page":2}` {
		t.Fatal("mutating clone changed original message")
	}
}

func TestUsageValidateAndAdd(t *testing.T) {
	t.Parallel()

	if err := (Usage{InputTokens: -1}).Validate(); err == nil {
		t.Fatal("negative usage accepted")
	}
	if err := (Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 4}).Validate(); err == nil {
		t.Fatal("inconsistent total tokens accepted")
	}

	combined := (Usage{InputTokens: 3, TotalTokens: 3, CostMicros: 7, Source: UsageSourceProvider}).Add(Usage{OutputTokens: 2, TotalTokens: 2, CostMicros: 5, Source: UsageSourceEstimated})
	if combined.InputTokens != 3 || combined.OutputTokens != 2 || combined.TotalTokens != 5 || combined.CostMicros != 12 {
		t.Fatalf("Add() = %+v", combined)
	}
	if combined.Source != UsageSourceEstimated {
		t.Fatalf("Add() source = %q, want %q", combined.Source, UsageSourceEstimated)
	}
}
