package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
)

const commandTestTimeout = 15 * time.Second

func TestCommandTurnExecutorReturnsAgentPayload(t *testing.T) {
	executor := CommandTurnExecutor{
		Command: "agent-command",
		Timeout: commandTestTimeout,
		Provider: commandTestProvider{
			stdout: `{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"command: hello"}]}`,
			assertRequest: func(request capability.RunCommandRequest) {
				t.Helper()
				if request.Command != "agent-command" {
					t.Fatalf("expected command to be forwarded, got %q", request.Command)
				}
				var input CommandTurnInput
				if err := json.Unmarshal(request.Stdin, &input); err != nil {
					t.Fatalf("decode stdin: %v", err)
				}
				if input.ProtocolVersion != CommandTurnProtocolVersion || input.SessionID != "sesn_000001" || input.TurnID != "turn_000001" || !json.Valid(input.UserPayload) {
					t.Fatalf("unexpected command input: %+v", input)
				}
			},
		},
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
	executor := CommandTurnExecutor{
		Command:  "agent-command",
		Timeout:  commandTestTimeout,
		Provider: commandTestProvider{stdout: `{"content":[{"type":"text","text":"legacy output"}]}`},
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
	executor := CommandTurnExecutor{
		Command:  "agent-command",
		Timeout:  commandTestTimeout,
		Provider: commandTestProvider{stdout: "not-json"},
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
	executor := CommandTurnExecutor{
		Command:  "agent-command",
		Timeout:  commandTestTimeout,
		Provider: commandTestProvider{stdout: `{"protocol_version":"tma.command.v2","content":[]}`},
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

type commandTestProvider struct {
	capability.UnavailableProvider

	stdout        string
	stderr        string
	exitCode      int
	assertRequest func(capability.RunCommandRequest)
}

func (p commandTestProvider) RunCommand(_ context.Context, request capability.RunCommandRequest) (capability.CommandResult, error) {
	if p.assertRequest != nil {
		p.assertRequest(request)
	}
	return capability.CommandResult{Status: "completed", ExitCode: p.exitCode, Stdout: p.stdout, Stderr: p.stderr}, nil
}
