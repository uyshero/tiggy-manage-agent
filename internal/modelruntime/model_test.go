package modelruntime

import (
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/llm"
	coremodel "tiggy-manage-agent/internal/model"
)

func TestFromLLMResponseConvertsSeedTextToolCall(t *testing.T) {
	response := fromLLMResponse("attempt_1", llm.Response{Message: llm.Message{
		Role: "assistant",
		Content: []llm.ContentPart{{Type: "text", Text: `
<seed:tool_call><function name="default_run_command"><parameter name="command" string="true">editppt</parameter><parameter name="args" string="false">["page","validate","/mnt/data/job/pages/page_001"]</parameter><parameter name="timeout_ms" string="false">60000</parameter></function></seed:tool_call>`}},
	}}, "stop", []coremodel.ToolDefinition{{Name: "default_run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}})

	if response.StopReason != coremodel.StopReasonToolCall {
		t.Fatalf("expected tool-call stop reason, got %q", response.StopReason)
	}
	if len(response.Message.Content) != 1 || response.Message.Content[0].Type != coremodel.ContentToolCall {
		t.Fatalf("unexpected converted content: %#v", response.Message.Content)
	}
	call := response.Message.Content[0].ToolCall
	if call == nil || call.ID != "attempt_1_tool_1" || call.Name != "default_run_command" || call.ArgumentsError != "" {
		t.Fatalf("unexpected converted tool call: %#v", call)
	}
	var arguments struct {
		Command   string   `json:"command"`
		Args      []string `json:"args"`
		TimeoutMS int      `json:"timeout_ms"`
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		t.Fatalf("decode converted arguments: %v", err)
	}
	if arguments.Command != "editppt" || len(arguments.Args) != 3 || arguments.Args[1] != "validate" || arguments.TimeoutMS != 60000 {
		t.Fatalf("unexpected converted arguments: %#v", arguments)
	}
}

func TestFromLLMResponseKeepsMalformedSeedTextAsText(t *testing.T) {
	raw := `<seed:tool_call><function name="default_run_command"><parameter name="args" string="false">[invalid]</parameter></function></seed:tool_call>`
	response := fromLLMResponse("attempt_1", llm.Response{Message: llm.Message{
		Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: raw}},
	}}, "stop", nil)

	if response.StopReason != coremodel.StopReasonComplete || len(response.Message.Content) != 1 || response.Message.Content[0].Text != raw {
		t.Fatalf("malformed seed text must remain ordinary text: %#v", response)
	}
}

func TestFromLLMResponseDoesNotDuplicateStructuredToolCalls(t *testing.T) {
	response := fromLLMResponse("attempt_1", llm.Response{Message: llm.Message{
		Role:    "assistant",
		Content: []llm.ContentPart{{Type: "text", Text: `<seed:tool_call><function name="duplicate"><parameter name="value" string="true">ignored</parameter></function></seed:tool_call>`}},
		ToolCalls: []llm.ToolCall{{
			ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "default_run_command", Arguments: json.RawMessage(`{"command":"true"}`)},
		}},
	}}, "tool_calls", []coremodel.ToolDefinition{{Name: "default_run_command", InputSchema: json.RawMessage(`{"type":"object"}`)}})

	toolCalls := 0
	for _, content := range response.Message.Content {
		if content.Type == coremodel.ContentToolCall {
			toolCalls++
		}
	}
	if toolCalls != 1 {
		t.Fatalf("expected only the structured tool call, got %#v", response.Message.Content)
	}
}
