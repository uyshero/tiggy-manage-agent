package runner

import (
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/agentruntime"
)

func TestAgentRuntimeTurnExecutorReturnsRuntimePayload(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: agentruntime.DemoRuntime{},
		Store:   store,
	}

	payload, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(payload); got != "Agent runtime received: hello" {
		t.Fatalf("expected runtime payload, got %q", got)
	}
	if got := store.runtimeEventTypes(); len(got) != 5 ||
		got[0] != "runtime.started" ||
		got[1] != "runtime.thinking" ||
		got[2] != "runtime.llm_request" ||
		got[3] != "runtime.llm_response" ||
		got[4] != "runtime.completed" {
		t.Fatalf("unexpected runtime events: %#v", got)
	}
}

func TestAgentRuntimeTurnExecutorRequiresRuntime(t *testing.T) {
	executor := AgentRuntimeTurnExecutor{}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected missing runtime error")
	}
}
