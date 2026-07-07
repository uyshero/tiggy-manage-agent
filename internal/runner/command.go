package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tiggy-manage-agent/internal/capability"
)

const CommandTurnProtocolVersion = "tma.command.v1"

// CommandTurnInput 是传给外部命令的 JSON 输入协议。
type CommandTurnInput struct {
	ProtocolVersion string          `json:"protocol_version"`
	SessionID       string          `json:"session_id"`
	TurnID          string          `json:"turn_id"`
	UserPayload     json.RawMessage `json:"user_payload"`
}

// CommandTurnExecutor 通过外部命令执行一次 turn。
// 它是 turn 层适配器，底层命令执行能力默认来自 LocalSystemProvider。
type CommandTurnExecutor struct {
	Command  string
	Args     []string
	Timeout  time.Duration
	Provider capability.Provider
}

func (e CommandTurnExecutor) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if e.Command == "" {
		return TurnResult{}, fmt.Errorf("turn command is required")
	}
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	input, err := json.Marshal(CommandTurnInput{
		ProtocolVersion: CommandTurnProtocolVersion,
		SessionID:       request.SessionID,
		TurnID:          request.TurnID,
		UserPayload:     request.UserPayload,
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode command turn input: %w", err)
	}

	provider := e.Provider
	if provider == nil {
		provider = capability.LocalSystemProvider{}
	}

	var deadline *time.Time
	if value, ok := ctx.Deadline(); ok {
		deadline = &value
	}
	result, err := provider.RunCommand(ctx, capability.RunCommandRequest{
		Meta:    capability.NewRequestMeta(request.SessionID, request.TurnID, deadline),
		Command: e.Command,
		Args:    e.Args,
		Stdin:   input,
	})
	if err != nil {
		if ctx.Err() != nil {
			return TurnResult{}, ctx.Err()
		}
		return TurnResult{}, fmt.Errorf("command turn failed: %w", err)
	}
	if result.ExitCode != 0 {
		return TurnResult{}, fmt.Errorf("command turn failed: exit code %d: %s", result.ExitCode, truncate(result.Stderr, 500))
	}

	payload := bytes.TrimSpace([]byte(result.Stdout))
	if len(payload) == 0 {
		return TurnResult{}, fmt.Errorf("command turn returned empty stdout")
	}
	if !json.Valid(payload) {
		return TurnResult{}, fmt.Errorf("command turn returned invalid JSON")
	}
	if err := validateCommandTurnOutputProtocol(payload); err != nil {
		return TurnResult{}, err
	}

	return TurnResult{AgentPayload: append(json.RawMessage(nil), payload...)}, nil
}

func validateCommandTurnOutputProtocol(payload []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil
	}

	rawVersion, ok := object["protocol_version"]
	if !ok {
		return fmt.Errorf("command turn returned missing protocol_version")
	}

	var version string
	if err := json.Unmarshal(rawVersion, &version); err != nil {
		return fmt.Errorf("command turn returned invalid protocol_version")
	}
	if version != CommandTurnProtocolVersion {
		return fmt.Errorf("command turn returned unsupported protocol_version %q", version)
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
