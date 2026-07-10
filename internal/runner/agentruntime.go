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
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/tools"
)

// AgentRuntimeTurnExecutor 把 WorkerRunner 的 TurnExecutor 接口适配到 AgentRuntime。
type AgentRuntimeTurnExecutor struct {
	Runtime          agentruntime.Runtime
	Store            managedagents.Store
	ObjectStore      objectstore.Client
	ArtifactBucket   string
	Timeout          time.Duration
	ProviderResolver execution.ProviderResolver
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
	toolExecution := execution.ResolveToolExecution(execution.ToolExecutionRequest{
		Config:           config,
		SessionID:        config.SessionID,
		TurnID:           request.TurnID,
		ProviderResolver: e.ProviderResolver,
		Store:            e.Store,
		ArtifactRecorder: ToolArtifactRecorder{Store: e.Store, ObjectStore: e.ObjectStore, Bucket: e.ArtifactBucket},
	})
	result, err := e.Runtime.RunTurn(ctx, agentruntime.TurnRequest{
		SessionID:   request.SessionID,
		TurnID:      request.TurnID,
		UserPayload: request.UserPayload,
		History:     history,
		Config: agentruntime.Config{
			WorkspaceID:           config.WorkspaceID,
			EnvironmentID:         config.EnvironmentID,
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
			Tools:                 toolExecution.Registry.ModelContext(),
			ModelTools:            toolExecution.Registry.ModelTools(),
			Skills:                config.Skills,
			InterventionMode:      tools.ParseInterventionMode(config.RuntimeSettings),
			ToolRegistry:          toolExecution.Registry,
			ToolExecutor:          tools.RegistryExecutor{Registry: toolExecution.Registry},
			ToolExecutionContext:  toolExecution.Context,
		},
		EmitStep: e.emitStep(request),
	})
	if err != nil {
		if errors.Is(err, agentruntime.ErrPendingIntervention) {
			return TurnResult{}, ErrTurnWaitingApproval
		}
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
	traceState := newRuntimeTraceState(request.SessionID, request.TurnID)
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
		if eventType == managedagents.EventRuntimeToolInterventionRequired {
			if err := e.savePendingIntervention(request, step); err != nil {
				return err
			}
		}
		if step.Data == nil {
			step.Data = map[string]any{}
		}
		traceState.decorate(eventType, step.Data)
		payload, err := json.Marshal(map[string]any{
			"trace_id":       step.Data["trace_id"],
			"span_id":        step.Data["span_id"],
			"parent_span_id": step.Data["parent_span_id"],
			"span_name":      step.Data["span_name"],
			"span_kind":      step.Data["span_kind"],
			"span_status":    step.Data["span_status"],
			"duration_ms":    step.Data["duration_ms"],
			"message":        step.Message,
			"data":           step.Data,
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

type runtimeTraceState struct {
	sessionID string
	turnID    string
	llmStart  map[string]time.Time
	toolStart map[string]time.Time
}

func newRuntimeTraceState(sessionID string, turnID string) *runtimeTraceState {
	return &runtimeTraceState{
		sessionID: sessionID,
		turnID:    turnID,
		llmStart:  map[string]time.Time{},
		toolStart: map[string]time.Time{},
	}
}

func (s *runtimeTraceState) decorate(eventType string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	now := time.Now()
	callID, _ := data["id"].(string)
	identifier, _ := data["identifier"].(string)
	apiName, _ := data["api_name"].(string)
	roundKey := fmt.Sprintf("%v", data["tool_round"])
	if roundKey == "<nil>" {
		roundKey = "0"
	}
	status := spanStatusForRuntimeEvent(eventType, data)
	duration := time.Duration(0)
	switch eventType {
	case managedagents.EventRuntimeLLMRequest:
		s.llmStart[roundKey] = now
	case managedagents.EventRuntimeLLMResponse:
		if startedAt, ok := s.llmStart[roundKey]; ok {
			duration = now.Sub(startedAt)
		}
	case managedagents.EventRuntimeToolCall:
		if callID != "" {
			s.toolStart[callID] = now
		}
	case managedagents.EventRuntimeToolResult:
		if startedAt, ok := s.toolStart[callID]; ok {
			duration = now.Sub(startedAt)
		}
	}
	fields := observability.EventTraceFields(observability.EventTraceFieldsInput{
		SessionID:       s.sessionID,
		TurnID:          s.turnID,
		EventType:       eventType,
		CallID:          defaultString(callID, roundKey),
		Identifier:      identifier,
		APIName:         apiName,
		Status:          status,
		Duration:        duration,
		ParentSpanID:    parentSpanForRuntimeEvent(s.turnID, eventType, callID),
		InteractionRoot: eventType == managedagents.EventRuntimeStarted,
	})
	for key, value := range fields {
		data[key] = value
	}
}

func spanStatusForRuntimeEvent(eventType string, data map[string]any) string {
	switch eventType {
	case managedagents.EventRuntimeCompleted, managedagents.EventRuntimeLLMResponse:
		return "ok"
	case managedagents.EventRuntimeFailed, managedagents.EventRuntimeContextCompactionFailed:
		return "error"
	case managedagents.EventRuntimeToolResult:
		if success, ok := data["success"].(bool); ok && success {
			return "ok"
		}
		return "error"
	case managedagents.EventRuntimeToolInterventionApproved:
		return "approved"
	case managedagents.EventRuntimeToolInterventionRejected:
		return "rejected"
	case managedagents.EventRuntimeToolInterventionRequired:
		return "waiting"
	default:
		return "point"
	}
}

func parentSpanForRuntimeEvent(turnID string, eventType string, callID string) string {
	switch eventType {
	case managedagents.EventRuntimeStarted:
		return ""
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
		return observability.ToolSpanID(turnID, callID, 0)
	default:
		return observability.InteractionSpanID(turnID)
	}
}

func (e AgentRuntimeTurnExecutor) savePendingIntervention(request TurnRequest, step agentruntime.Step) error {
	if e.Store == nil {
		return nil
	}

	callID, _ := step.Data["id"].(string)
	identifier, _ := step.Data["identifier"].(string)
	apiName, _ := step.Data["api_name"].(string)
	mode, _ := step.Data["intervention_mode"].(string)
	reason, _ := step.Data["reason"].(string)
	if callID == "" || identifier == "" || apiName == "" {
		return nil
	}

	var arguments json.RawMessage
	if value, ok := step.Data["arguments"]; ok && value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode intervention arguments: %w", err)
		}
		arguments = encoded
	}

	var continuation json.RawMessage
	if value, ok := step.Private["continuation_messages"]; ok && value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode intervention continuation: %w", err)
		}
		continuation = encoded
	}
	continuationRound := 0
	if value, ok := step.Private["continuation_round"].(int); ok {
		continuationRound = value
	}

	if _, err := e.Store.SaveSessionIntervention(request.SessionID, managedagents.SaveSessionInterventionInput{
		TurnID:            request.TurnID,
		CallID:            callID,
		ToolIdentifier:    identifier,
		APIName:           apiName,
		Arguments:         arguments,
		InterventionMode:  mode,
		Reason:            reason,
		Continuation:      continuation,
		ContinuationRound: continuationRound,
	}); err != nil {
		return err
	}
	return e.Store.MarkSessionTurnWaitingApproval(request.SessionID, request.TurnID)
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
