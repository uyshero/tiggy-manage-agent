package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/managedagents"
)

// AgentRuntimeTurnExecutor 把 WorkerRunner 的 TurnExecutor 接口适配到 AgentRuntime。
type AgentRuntimeTurnExecutor struct {
	Runtime agentruntime.Runtime
	Store   managedagents.Store
	Timeout time.Duration
}

func (e AgentRuntimeTurnExecutor) RunTurn(ctx context.Context, request TurnRequest) (json.RawMessage, error) {
	if e.Runtime == nil {
		return nil, errors.New("agent runtime is required")
	}
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	payload, err := e.Runtime.RunTurn(ctx, agentruntime.TurnRequest{
		SessionID:   request.SessionID,
		TurnID:      request.TurnID,
		UserPayload: request.UserPayload,
		EmitStep:    e.emitStep(request),
	})
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, request, err)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return append(json.RawMessage(nil), payload...), nil
}

func (e AgentRuntimeTurnExecutor) emitStep(request TurnRequest) func(context.Context, agentruntime.Step) error {
	return func(ctx context.Context, step agentruntime.Step) error {
		if e.Store == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		eventType := step.Type
		if eventType == "" {
			return errors.New("runtime step type is required")
		}
		payload, err := json.Marshal(map[string]any{
			"message": step.Message,
			"data":    step.Data,
		})
		if err != nil {
			return fmt.Errorf("encode runtime step: %w", err)
		}
		_, err = e.Store.AppendRuntimeEvent(request.SessionID, request.TurnID, managedagents.AppendEventInput{
			Type:    eventType,
			Payload: payload,
		})
		return err
	}
}

func (e AgentRuntimeTurnExecutor) recordRuntimeFailed(ctx context.Context, request TurnRequest, err error) error {
	if e.Store == nil || err == nil || ctx.Err() != nil {
		return nil
	}
	return e.emitStep(request)(ctx, agentruntime.Step{
		Type:    managedagents.EventRuntimeFailed,
		Message: err.Error(),
	})
}
