package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestIsContinuationRequest(t *testing.T) {
	for _, text := range []string{"继续", "继续完成剩余页面", "接着做", "continue", "Resume please", "keep going with the plan"} {
		payload, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}})
		if !isContinuationRequest(payload) {
			t.Fatalf("expected continuation request for %q", text)
		}
	}
	payload := json.RawMessage(`{"content":[{"type":"text","text":"重新做一份"}]}`)
	if isContinuationRequest(payload) {
		t.Fatal("replacement request must not be treated as continuation")
	}
}

func TestBuildContinuationContextUsesPreviousTurnExecutionEvidence(t *testing.T) {
	events := []managedagents.Event{
		{Seq: 1, TurnID: "turn_old", Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"unrelated"}]}`)},
		{Seq: 2, TurnID: "turn_old", Type: managedagents.EventAgentMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"old result"}]}`)},
		{Seq: 10, TurnID: "turn_previous", Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"转成 ppt"}]}`)},
		{Seq: 11, TurnID: "turn_previous", Type: "tool.batch_planned", Payload: json.RawMessage(`{
			"data":{"calls":[{"call":{"id":"call_build","name":"default_run_command","arguments":{
				"command":"editppt","args":["page","build","/mnt/data/job/page_001"],"content":"SECRET-CONTENT"
			}}}]}
		}`)},
		{Seq: 12, TurnID: "turn_previous", Type: "tool.call_result", Payload: json.RawMessage(`{
			"data":{"name":"default_run_command","call_id":"call_build","result":{"status":"succeeded","state":{
				"status":"completed","exit_code":0,"stdout":"built page_001"
			}}}
		}`)},
		{Seq: 13, TurnID: "turn_previous", Type: managedagents.EventAgentMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"validation still needed"}]}`)},
		{Seq: 14, TurnID: "turn_previous", Type: "runtime.completed", Payload: json.RawMessage(`{}`)},
		{Seq: 20, TurnID: "turn_current", Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"content":[{"type":"text","text":"继续"}]}`)},
	}

	context := buildContinuationContext(events, "turn_current", 20)
	for _, expected := range []string{
		"Previous turn: turn_previous", "terminal status: completed", "Original objective: 转成 ppt",
		"default_run_command", "editppt", "/mnt/data/job/page_001", "built page_001", "validation still needed",
	} {
		if !strings.Contains(context, expected) {
			t.Fatalf("continuation context missing %q:\n%s", expected, context)
		}
	}
	for _, excluded := range []string{"unrelated", "old result", "SECRET-CONTENT"} {
		if strings.Contains(context, excluded) {
			t.Fatalf("continuation context leaked excluded text %q:\n%s", excluded, context)
		}
	}
	if !strings.Contains(context, "omitted 16 bytes") {
		t.Fatalf("expected large write content to be represented as omitted: %s", context)
	}
}

func TestBuildContinuationContextReturnsEmptyWithoutPreviousTurn(t *testing.T) {
	events := []managedagents.Event{{Seq: 1, TurnID: "turn_current", Type: managedagents.EventUserMessage}}
	if context := buildContinuationContext(events, "turn_current", 1); context != "" {
		t.Fatalf("expected empty context, got %q", context)
	}
}
