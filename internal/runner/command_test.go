package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandTurnExecutorReturnsAgentPayload(t *testing.T) {
	command := writeExecutable(t, `#!/bin/sh
input=$(cat)
case "$input" in
  *'"protocol_version":"tma.command.v1"'*hello*) printf '{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"command: hello"}]}' ;;
  *) printf '{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"missing input"}]}' ;;
esac
`)

	executor := CommandTurnExecutor{
		Command: command,
		Timeout: 5 * time.Second,
	}
	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "command: hello" {
		t.Fatalf("expected command payload, got %q", got)
	}
}

func TestCommandTurnExecutorRejectsMissingOutputProtocolVersion(t *testing.T) {
	command := writeExecutable(t, `#!/bin/sh
printf '{"content":[{"type":"text","text":"legacy output"}]}'
`)

	executor := CommandTurnExecutor{
		Command: command,
		Timeout: 5 * time.Second,
	}
	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected missing protocol version error")
	}
}

func TestCommandTurnExecutorRejectsInvalidJSON(t *testing.T) {
	command := writeExecutable(t, `#!/bin/sh
printf 'not-json'
`)

	executor := CommandTurnExecutor{
		Command: command,
		Timeout: 5 * time.Second,
	}
	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestCommandTurnExecutorRejectsUnsupportedOutputProtocolVersion(t *testing.T) {
	command := writeExecutable(t, `#!/bin/sh
printf '{"protocol_version":"tma.command.v2","content":[]}'
`)

	executor := CommandTurnExecutor{
		Command: command,
		Timeout: 5 * time.Second,
	}
	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected unsupported protocol version error")
	}
}

func writeExecutable(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "executor.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executor: %v", err)
	}
	return path
}
