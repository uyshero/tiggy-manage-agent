package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

// AgentRuntimeTurnExecutor 把 WorkerRunner 的 TurnExecutor 接口适配到 AgentRuntime。
type AgentRuntimeTurnExecutor struct {
	Runtime agentruntime.Runtime
	Store   managedagents.Store
	Timeout time.Duration
}

func (e AgentRuntimeTurnExecutor) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if e.Runtime == nil {
		return TurnResult{}, errors.New("agent runtime is required")
	}
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	config, err := e.resolveRuntimeConfig(request.SessionID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, request, err)
		return TurnResult{}, err
	}
	history, err := e.resolveConversationHistory(request.SessionID, request.UserEventSeq)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, request, err)
		return TurnResult{}, err
	}

	startedAt := time.Now()
	toolRegistry := tools.DefaultRegistry()
	result, err := e.Runtime.RunTurn(ctx, agentruntime.TurnRequest{
		SessionID:   request.SessionID,
		TurnID:      request.TurnID,
		UserPayload: request.UserPayload,
		History:     history,
		Config: agentruntime.Config{
			LLMProvider:           config.LLMProvider,
			LLMProviderType:       config.LLMProviderType,
			LLMModel:              config.LLMModel,
			LLMBaseURL:            config.LLMBaseURL,
			LLMAPIKey:             llmAPIKey(config.LLMAPIKeyEnv),
			ContextWindowTokens:   config.ContextWindowTokens,
			SummaryText:           config.SummaryText,
			SummarySourceUntilSeq: config.SummarySourceUntilSeq,
			System:                config.System,
			RuntimeSettings:       config.RuntimeSettings,
			Tools:                 toolRegistry.ModelContext(),
			ModelTools:            toolRegistry.ModelTools(),
			Skills:                config.Skills,
			InterventionMode:      tools.ParseInterventionMode(config.RuntimeSettings),
			ToolRegistry:          toolRegistry,
			ToolExecutor:          tools.RegistryExecutor{Registry: toolRegistry},
			ToolExecutionContext: tools.ExecutionContext{
				Provider: capability.LocalSystemProvider{},
			},
		},
		EmitStep: e.emitStep(request),
	})
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, request, err)
		if ctx.Err() != nil {
			return TurnResult{}, ctx.Err()
		}
		return TurnResult{
			Usage: e.failedUsageRecord(request, config, result, time.Since(startedAt), err),
		}, err
	}
	if result.SummaryText != "" && e.Store != nil {
		if _, saveErr := e.Store.SaveSessionSummary(request.SessionID, managedagents.UpsertSessionSummaryInput{
			SummaryText:    result.SummaryText,
			SourceUntilSeq: result.SummarySourceUntilSeq,
		}); saveErr != nil {
			slog.Default().Warn("runtime summary save failed",
				"session_id", request.SessionID,
				"turn_id", request.TurnID,
				"source_until_seq", result.SummarySourceUntilSeq,
				"error", saveErr,
			)
		}
	}
	return TurnResult{
		AgentPayload: append(json.RawMessage(nil), result.AgentPayload...),
		Usage:        e.usageRecord(request, config, result, time.Since(startedAt)),
	}, nil
}

func (e AgentRuntimeTurnExecutor) resolveRuntimeConfig(sessionID string) (managedagents.AgentRuntimeConfig, error) {
	if e.Store == nil {
		return managedagents.AgentRuntimeConfig{}, nil
	}
	return e.Store.ResolveAgentRuntimeConfig(sessionID)
}

func (e AgentRuntimeTurnExecutor) resolveConversationHistory(sessionID string, beforeSeq int64) ([]managedagents.ConversationMessage, error) {
	if e.Store == nil || beforeSeq <= 0 {
		return nil, nil
	}
	return e.Store.ListConversationMessages(sessionID, beforeSeq)
}

func llmAPIKey(envName string) string {
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
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

func (e AgentRuntimeTurnExecutor) usageRecord(request TurnRequest, config managedagents.AgentRuntimeConfig, result agentruntime.TurnResult, latency time.Duration) *managedagents.RecordLLMUsageInput {
	providerID := defaultString(result.Provider, config.LLMProvider)
	model := defaultString(result.Model, config.LLMModel)
	if providerID == "" || model == "" {
		return nil
	}
	if config.WorkspaceID == "" || config.AgentID == "" || config.AgentConfigVersion <= 0 {
		return nil
	}

	return &managedagents.RecordLLMUsageInput{
		WorkspaceID:        config.WorkspaceID,
		AgentID:            config.AgentID,
		AgentConfigVersion: config.AgentConfigVersion,
		SessionID:          request.SessionID,
		TurnID:             request.TurnID,
		ProviderID:         providerID,
		ProviderType:       defaultString(result.ProviderType, config.LLMProviderType),
		Model:              model,
		InputTokens:        result.Usage.InputTokens,
		OutputTokens:       result.Usage.OutputTokens,
		TotalTokens:        result.Usage.TotalTokens,
		CachedInputTokens:  result.Usage.CachedInputTokens,
		ReasoningTokens:    result.Usage.ReasoningTokens,
		LatencyMillis:      latency.Milliseconds(),
		Status:             "completed",
	}
}

func (e AgentRuntimeTurnExecutor) failedUsageRecord(request TurnRequest, config managedagents.AgentRuntimeConfig, result agentruntime.TurnResult, latency time.Duration, err error) *managedagents.RecordLLMUsageInput {
	usage := e.usageRecord(request, config, result, latency)
	if usage == nil {
		return nil
	}
	usage.Status = "failed"
	if err != nil {
		usage.ErrorMessage = err.Error()
	}
	return usage
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
