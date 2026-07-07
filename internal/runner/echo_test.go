package runner

import (
	"encoding/json"
	"testing"
)

func TestEchoExecutorUsesFirstTextContent(t *testing.T) {
	executor := EchoExecutor{}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello worker"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "Echo Agent received: hello worker" {
		t.Fatalf("expected echo payload, got %q", got)
	}
}

func TestEchoExecutorHandlesEmptyText(t *testing.T) {
	executor := EchoExecutor{}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "Echo Agent received your message." {
		t.Fatalf("expected fallback payload, got %q", got)
	}
}
