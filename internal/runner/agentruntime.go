package runner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tokenestimate"
	"tiggy-manage-agent/internal/tools"
)

const maxVisionImageBytes = 20 << 20

// AgentRuntimeTurnExecutor 把 WorkerRunner 的 TurnExecutor 接口适配到 AgentRuntime。
type AgentRuntimeTurnExecutor struct {
	Runtime          agentruntime.Runtime
	Store            managedagents.Store
	ObjectStore      objectstore.Client
	ArtifactBucket   string
	Timeout          time.Duration
	ProviderResolver execution.ProviderResolver
	MCPHost          *mcp.StdioHost
	MCPHTTPHost      *mcp.StreamableHTTPHost
	MCPRuntimeGuard  *mcp.RuntimeGuard
}

func (e AgentRuntimeTurnExecutor) MCPHostStats() mcp.StdioHostStats {
	if e.MCPHost == nil {
		return mcp.StdioHostStats{}
	}
	return e.MCPHost.Stats()
}

func (e AgentRuntimeTurnExecutor) MCPHTTPHostStats() mcp.StreamableHTTPHostStats {
	if e.MCPHTTPHost == nil {
		return mcp.StreamableHTTPHostStats{}
	}
	return e.MCPHTTPHost.Stats()
}

func (e AgentRuntimeTurnExecutor) MCPHTTPEgressPolicy() *mcp.EgressPolicy {
	if e.MCPHTTPHost == nil {
		return nil
	}
	return e.MCPHTTPHost.EgressPolicy()
}

func (e AgentRuntimeTurnExecutor) MCPRuntimeGuardStats() mcp.RuntimeGuardStats {
	if e.MCPRuntimeGuard == nil {
		return mcp.RuntimeGuardStats{}
	}
	return e.MCPRuntimeGuard.Stats()
}

func (e AgentRuntimeTurnExecutor) MCPRegistryRuntimeStates(workspaceID string) []mcp.RegistryRuntimeState {
	if e.MCPRuntimeGuard == nil {
		return nil
	}
	return e.MCPRuntimeGuard.RegistryStates(workspaceID)
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
	ctx, err := databaseContextForTurn(ctx, request)
	if err != nil {
		return TurnResult{}, err
	}
	emit := e.emitStep(request)

	config, err := e.resolveRuntimeConfig(ctx, request.SessionID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	var history []managedagents.ConversationMessage
	if request.ResumeIntervention == nil {
		history, err = e.resolveConversationHistory(ctx, request.SessionID, request.UserEventSeq)
		if err != nil {
			_ = e.recordRuntimeFailed(ctx, err, emit)
			return TurnResult{}, err
		}
		history = conversationHistoryAfterSeq(history, config.SummarySourceUntilSeq)
	}
	resume, err := runtimeInterventionResume(request.ResumeIntervention)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	resolvedSkills, err := e.resolveSkills(ctx, request, config, emit)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	imageParts, err := e.loadUserImageParts(ctx, request.UserPayload, config.WorkspaceID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}

	startedAt := time.Now()
	managedEnvironment, environmentCipher, err := envvars.ResolveWorkspace(ctx, e.Store, config.WorkspaceID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, fmt.Errorf("resolve managed environment: %w", err)
	}
	toolExecution := execution.ResolveToolExecution(execution.ToolExecutionRequest{
		Context:           ctx,
		Config:            config,
		SessionID:         config.SessionID,
		TurnID:            request.TurnID,
		ProviderResolver:  e.ProviderResolver,
		Store:             e.Store,
		ArtifactRecorder:  ToolArtifactRecorder{Store: e.Store, ObjectStore: e.ObjectStore, Bucket: e.ArtifactBucket},
		Environment:       managedEnvironment,
		EnvironmentCipher: environmentCipher,
		MCPHost:           e.MCPHost,
		MCPHTTPHost:       e.MCPHTTPHost,
		MCPRuntimeGuard:   e.MCPRuntimeGuard,
	})
	result, err := e.Runtime.RunTurn(ctx, agentruntime.TurnRequest{
		SessionID:          request.SessionID,
		TurnID:             request.TurnID,
		UserPayload:        request.UserPayload,
		History:            history,
		ImageParts:         imageParts,
		ResumeIntervention: resume,
		Config: agentruntime.Config{
			WorkspaceID:           config.WorkspaceID,
			EnvironmentID:         config.EnvironmentID,
			LLMProvider:           config.LLMProvider,
			LLMProviderType:       config.LLMProviderType,
			LLMModel:              config.LLMModel,
			LLMBaseURL:            config.LLMBaseURL,
			LLMAPIKey:             llmAPIKey(config.LLMAPIKeyEnv),
			LLMCapabilityType:     config.LLMCapabilityType,
			VisionLLMProvider:     config.VisionLLMProvider,
			VisionLLMProviderType: config.VisionLLMProviderType,
			VisionLLMModel:        config.VisionLLMModel,
			VisionLLMBaseURL:      config.VisionLLMBaseURL,
			VisionLLMAPIKey:       llmAPIKey(config.VisionLLMAPIKeyEnv),
			ContextWindowTokens:   config.ContextWindowTokens,
			SummaryText:           config.SummaryText,
			SummarySourceUntilSeq: config.SummarySourceUntilSeq,
			System:                config.System,
			RuntimeSettings:       config.RuntimeSettings,
			Tools:                 toolExecution.Registry.ModelContext(),
			ModelTools:            toolExecution.Registry.ModelTools(),
			Skills:                resolvedSkills.Rendered,
			SkillsResolved:        true,
			InterventionMode:      tools.ParseInterventionMode(config.RuntimeSettings),
			ToolRegistry:          toolExecution.Registry,
			ToolExecutor:          tools.RegistryExecutor{Registry: toolExecution.Registry},
			ToolExecutionContext:  toolExecution.Context,
		},
		EmitStep: emit,
	})
	if err != nil {
		if errors.Is(err, agentruntime.ErrPendingIntervention) {
			return TurnResult{}, ErrTurnWaitingApproval
		}
		_ = e.recordRuntimeFailed(ctx, err, emit)
		if ctx.Err() != nil {
			return TurnResult{}, ctx.Err()
		}
		return TurnResult{
			Usage: e.failedUsageRecord(request, config, result, time.Since(startedAt), err),
		}, err
	}
	if result.SummaryText != "" && e.Store != nil {
		if _, saveErr := managedagents.SaveSessionSummaryWithContext(ctx, e.Store, request.SessionID, managedagents.UpsertSessionSummaryInput{
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

func conversationHistoryAfterSeq(history []managedagents.ConversationMessage, seq int64) []managedagents.ConversationMessage {
	filtered := make([]managedagents.ConversationMessage, 0, len(history))
	for _, message := range history {
		if message.Seq > seq {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func (e AgentRuntimeTurnExecutor) loadUserImageParts(ctx context.Context, payload json.RawMessage, workspaceID string) ([]llm.ContentPart, error) {
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	var message struct {
		Attachments []struct {
			ObjectRefID string `json:"object_ref_id"`
			ContentType string `json:"content_type"`
			Name        string `json:"name"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, nil
	}
	parts := make([]llm.ContentPart, 0, len(message.Attachments))
	totalBytes := 0
	for _, attachment := range message.Attachments {
		contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
		if !strings.HasPrefix(contentType, "image/") {
			continue
		}
		if !supportedVisionImageType(contentType) {
			return nil, fmt.Errorf("unsupported vision image type %s for %s", contentType, attachment.Name)
		}
		if e.Store == nil || e.ObjectStore == nil || strings.TrimSpace(attachment.ObjectRefID) == "" {
			return nil, errors.New("image attachment storage is unavailable")
		}
		objectRef, err := managedagents.GetObjectRefWithContext(databaseCtx, e.Store, attachment.ObjectRefID)
		if err != nil {
			return nil, fmt.Errorf("load image object ref %s: %w", attachment.ObjectRefID, err)
		}
		if objectRef.WorkspaceID != "" && workspaceID != "" && objectRef.WorkspaceID != workspaceID {
			return nil, errors.New("image attachment workspace mismatch")
		}
		if supportedVisionImageType(strings.ToLower(strings.TrimSpace(objectRef.ContentType))) {
			contentType = strings.ToLower(strings.TrimSpace(objectRef.ContentType))
		}
		object, err := e.ObjectStore.GetObject(ctx, objectstore.GetObjectInput{Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion})
		if err != nil {
			return nil, fmt.Errorf("download image attachment %s: %w", attachment.Name, err)
		}
		content, readErr := io.ReadAll(io.LimitReader(object.Body, maxVisionImageBytes+1))
		_ = object.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read image attachment %s: %w", attachment.Name, readErr)
		}
		if len(content) > maxVisionImageBytes {
			return nil, fmt.Errorf("image attachment %s exceeds vision limit of 20 MB", attachment.Name)
		}
		totalBytes += len(content)
		if totalBytes > 40<<20 {
			return nil, errors.New("image attachments exceed total vision limit of 40 MB")
		}
		detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(content)))
		if !supportedVisionImageType(detectedType) {
			return nil, fmt.Errorf("image attachment %s content does not match a supported image format", attachment.Name)
		}
		contentType = detectedType
		parts = append(parts, llm.ContentPart{Type: "image_url", ImageURL: &llm.ImageURL{
			URL: "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(content), Detail: "auto",
		}})
	}
	return parts, nil
}

func supportedVisionImageType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func (e AgentRuntimeTurnExecutor) resolveSkills(ctx context.Context, request TurnRequest, config managedagents.AgentRuntimeConfig, emit func(context.Context, agentruntime.Step) error) (skills.ResolveResult, error) {
	if len(config.Skills) == 0 || string(config.Skills) == "null" {
		return skills.ResolveResult{}, nil
	}
	if err := emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsResolving, Message: "Resolving agent skills."}); err != nil {
		return skills.ResolveResult{}, err
	}
	registry, ok := e.Store.(skills.Registry)
	if !ok {
		err := errors.New("skill registry is unavailable")
		_ = emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsFailed, Message: err.Error()})
		return skills.ResolveResult{}, err
	}
	maxTokens := skillContextBudget(config.ContextWindowTokens)
	result, err := skills.ResolveRegistry(ctx, registry, config.WorkspaceID, config.Skills, maxTokens)
	if err != nil {
		legacy, legacyErr := skills.Resolve(config.Skills)
		if legacyErr == nil && isLegacySkillsResult(legacy) {
			legacy.EstimatedTokens = estimateSkillTokens(legacy.Rendered)
			if emitErr := emit(ctx, agentruntime.Step{
				Type: managedagents.EventRuntimeSkillsResolved, Message: "Using legacy skills context.",
				Data: map[string]any{"legacy_passthrough": true, "estimated_tokens": legacy.EstimatedTokens},
			}); emitErr != nil {
				return skills.ResolveResult{}, emitErr
			}
			return legacy, nil
		}
		wrapped := fmt.Errorf("resolve runtime skills: %w", err)
		_ = emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsFailed, Message: wrapped.Error()})
		return skills.ResolveResult{}, wrapped
	}
	usages := make([]skills.Usage, 0, len(result.Skills))
	for _, resolved := range result.Skills {
		usages = append(usages, skills.Usage{
			WorkspaceID: config.WorkspaceID, SessionID: request.SessionID, TurnID: request.TurnID,
			AgentID: config.AgentID, AgentConfigVersion: config.AgentConfigVersion,
			SkillID: resolved.Skill.ID, SkillIdentifier: resolved.Skill.Identifier, SkillVersion: resolved.Version.Version,
			RequestedMode: resolved.RequestedMode, RenderedMode: resolved.RenderedMode, Priority: resolved.Priority,
			EstimatedTokens: resolved.EstimatedTokens, Status: resolved.Status, FailureReason: resolved.FailureReason,
		})
	}
	if recorder, ok := e.Store.(skills.UsageRecorder); ok {
		if err := recorder.RecordSkillUsages(ctx, usages); err != nil {
			wrapped := fmt.Errorf("record skill usages: %w", err)
			_ = emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsFailed, Message: wrapped.Error()})
			return skills.ResolveResult{}, wrapped
		}
	}
	eventType := managedagents.EventRuntimeSkillsResolved
	message := "Agent skills resolved."
	if result.Truncated {
		eventType = managedagents.EventRuntimeSkillsTruncated
		message = "Agent skills resolved with budget degradation."
	}
	if err := emit(ctx, agentruntime.Step{
		Type: eventType, Message: message,
		Data: map[string]any{"skills": skillResolutionEventItems(result.Skills), "estimated_tokens": result.EstimatedTokens, "max_tokens": maxTokens},
	}); err != nil {
		return skills.ResolveResult{}, err
	}
	return result, nil
}

func skillResolutionEventItems(resolved []skills.ResolvedSkill) []map[string]any {
	items := make([]map[string]any, 0, len(resolved))
	for _, item := range resolved {
		items = append(items, map[string]any{
			"skill_id":         item.Skill.ID,
			"identifier":       item.Skill.Identifier,
			"version_id":       item.Version.ID,
			"version":          item.Version.Version,
			"requested_mode":   item.RequestedMode,
			"rendered_mode":    item.RenderedMode,
			"priority":         item.Priority,
			"estimated_tokens": item.EstimatedTokens,
			"status":           item.Status,
			"failure_reason":   item.FailureReason,
		})
	}
	return items
}

func isLegacySkillsResult(result skills.ResolveResult) bool {
	if result.LegacyPassthrough {
		return true
	}
	for _, enabled := range result.Config.Enabled {
		if enabled.Version <= 0 {
			return true
		}
	}
	return false
}

func skillContextBudget(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		contextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	budget := contextWindowTokens / 10
	if budget > 16000 {
		return 16000
	}
	if budget < 512 {
		return 512
	}
	return budget
}

func estimateSkillTokens(raw json.RawMessage) int {
	return tokenestimate.Text(string(raw))
}

func runtimeInterventionResume(intervention *managedagents.SessionIntervention) (*agentruntime.InterventionResume, error) {
	if intervention == nil {
		return nil, nil
	}
	var continuation []llm.Message
	var continuationState json.RawMessage
	if len(intervention.Continuation) > 0 {
		if err := json.Unmarshal(intervention.Continuation, &continuation); err != nil {
			var envelope interventionContinuationEnvelope
			if envelopeErr := json.Unmarshal(intervention.Continuation, &envelope); envelopeErr != nil || envelope.ProtocolVersion != interventionContinuationProtocolVersion {
				return nil, fmt.Errorf("decode intervention continuation: %w", err)
			}
			continuation = envelope.Messages
			continuationState = append(json.RawMessage(nil), envelope.State...)
		}
	}
	return &agentruntime.InterventionResume{
		Call: tools.Call{
			ID:         intervention.CallID,
			Identifier: intervention.ToolIdentifier,
			APIName:    intervention.APIName,
			Arguments:  append(json.RawMessage(nil), intervention.Arguments...),
		},
		Status:            intervention.Status,
		DecisionReason:    intervention.DecisionReason,
		Continuation:      continuation,
		ContinuationRound: intervention.ContinuationRound,
		ContinuationState: continuationState,
	}, nil
}

const interventionContinuationProtocolVersion = "tma.intervention_continuation.v2"

type interventionContinuationEnvelope struct {
	ProtocolVersion string          `json:"protocol_version"`
	Messages        []llm.Message   `json:"messages"`
	State           json.RawMessage `json:"state,omitempty"`
}

func (e AgentRuntimeTurnExecutor) resolveRuntimeConfig(ctx context.Context, sessionID string) (managedagents.AgentRuntimeConfig, error) {
	if e.Store == nil {
		return managedagents.AgentRuntimeConfig{}, nil
	}
	config, err := managedagents.ResolveAgentRuntimeConfigWithContext(ctx, e.Store, sessionID)
	if err != nil {
		return managedagents.AgentRuntimeConfig{}, err
	}
	if registry, ok := e.Store.(mcpregistry.Store); ok {
		_, resolved, resolveErr := mcpregistry.PinAndResolve(ctx, registry, config.WorkspaceID, config.MCP)
		if resolveErr != nil {
			return managedagents.AgentRuntimeConfig{}, fmt.Errorf("resolve MCP registry bindings: %w", resolveErr)
		}
		config.MCP = resolved
	}
	return config, nil
}

func (e AgentRuntimeTurnExecutor) resolveConversationHistory(ctx context.Context, sessionID string, beforeSeq int64) ([]managedagents.ConversationMessage, error) {
	if e.Store == nil || beforeSeq <= 0 {
		return nil, nil
	}
	return managedagents.ListConversationMessagesWithContext(ctx, e.Store, sessionID, beforeSeq)
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
			if err := e.savePendingIntervention(ctx, request, step); err != nil {
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
		_, err = managedagents.AppendRuntimeEventWithContext(ctx, e.Store, request.SessionID, request.TurnID, managedagents.AppendEventInput{
			Type:    eventType,
			Payload: payload,
		})
		return err
	}
}

type runtimeTraceState struct {
	sessionID        string
	turnID           string
	interactionStart time.Time
	llmStart         map[string]time.Time
	toolStart        map[string]time.Time
	contextStart     time.Time
	approvalStart    map[string]time.Time
}

func newRuntimeTraceState(sessionID string, turnID string) *runtimeTraceState {
	return &runtimeTraceState{
		sessionID:     sessionID,
		turnID:        turnID,
		llmStart:      map[string]time.Time{},
		toolStart:     map[string]time.Time{},
		approvalStart: map[string]time.Time{},
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
	case managedagents.EventRuntimeStarted:
		s.interactionStart = now
	case managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		if !s.interactionStart.IsZero() {
			duration = now.Sub(s.interactionStart)
		}
	case managedagents.EventRuntimeLLMRequest:
		s.llmStart[roundKey] = now
	case managedagents.EventRuntimeLLMResponse:
		if startedAt, ok := s.llmStart[roundKey]; ok {
			duration = now.Sub(startedAt)
			delete(s.llmStart, roundKey)
		}
	case managedagents.EventRuntimeToolCall:
		if callID != "" {
			s.toolStart[callID] = now
		}
	case managedagents.EventRuntimeToolResult:
		if startedAt, ok := s.toolStart[callID]; ok {
			duration = now.Sub(startedAt)
			delete(s.toolStart, callID)
		}
	case managedagents.EventRuntimeContextCompacting:
		s.contextStart = now
	case managedagents.EventRuntimeContextCompacted, managedagents.EventRuntimeContextCompactionFailed:
		if !s.contextStart.IsZero() {
			duration = now.Sub(s.contextStart)
			s.contextStart = time.Time{}
		}
	case managedagents.EventRuntimeToolInterventionRequired:
		if callID != "" {
			s.approvalStart[callID] = now
		}
	case managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
		if startedAt, ok := s.approvalStart[callID]; ok {
			duration = now.Sub(startedAt)
			delete(s.approvalStart, callID)
		}
	}
	interactionEvent := eventType == managedagents.EventRuntimeStarted || eventType == managedagents.EventRuntimeCompleted || eventType == managedagents.EventRuntimeFailed
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
		InteractionRoot: interactionEvent,
	})
	for key, value := range fields {
		data[key] = value
	}
}

func spanStatusForRuntimeEvent(eventType string, data map[string]any) string {
	switch eventType {
	case managedagents.EventRuntimeStarted:
		return "running"
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
	case managedagents.EventRuntimeStarted, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		return ""
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
		return observability.ToolSpanID(turnID, callID, 0)
	default:
		return observability.InteractionSpanID(turnID)
	}
}

func (e AgentRuntimeTurnExecutor) savePendingIntervention(ctx context.Context, request TurnRequest, step agentruntime.Step) error {
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
	if value, ok := step.Private["arguments"].(json.RawMessage); ok && len(value) > 0 {
		arguments = append(json.RawMessage(nil), value...)
	} else if value, ok := step.Data["arguments"]; ok && value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode intervention arguments: %w", err)
		}
		arguments = encoded
	}

	var continuation json.RawMessage
	if value, ok := step.Private["continuation_messages"]; ok && value != nil {
		encoded, err := marshalInterventionContinuation(value)
		if err != nil {
			return fmt.Errorf("encode intervention continuation: %w", err)
		}
		continuation = encoded
	}
	if state, ok := step.Private["continuation_state"].(json.RawMessage); ok && len(state) > 0 && len(continuation) > 0 {
		var messages []llm.Message
		if err := json.Unmarshal(continuation, &messages); err != nil {
			return fmt.Errorf("encode intervention continuation state: %w", err)
		}
		encoded, err := json.Marshal(interventionContinuationEnvelope{
			ProtocolVersion: interventionContinuationProtocolVersion,
			Messages:        messages,
			State:           append(json.RawMessage(nil), state...),
		})
		if err != nil {
			return fmt.Errorf("encode intervention continuation state: %w", err)
		}
		continuation = encoded
	}
	continuationRound := 0
	if value, ok := step.Private["continuation_round"].(int); ok {
		continuationRound = value
	}

	if _, err := managedagents.SaveSessionInterventionWithContext(ctx, e.Store, request.SessionID, managedagents.SaveSessionInterventionInput{
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
	return managedagents.MarkSessionTurnWaitingApprovalWithContext(ctx, e.Store, request.SessionID, request.TurnID)
}

func marshalInterventionContinuation(value any) ([]byte, error) {
	messages, ok := value.([]llm.Message)
	if !ok {
		return json.Marshal(value)
	}
	normalized := append([]llm.Message(nil), messages...)
	for messageIndex := range normalized {
		normalized[messageIndex].ToolCalls = append([]llm.ToolCall(nil), normalized[messageIndex].ToolCalls...)
		for callIndex := range normalized[messageIndex].ToolCalls {
			raw := normalized[messageIndex].ToolCalls[callIndex].Function.Arguments
			trimmed := strings.TrimSpace(string(raw))
			var object map[string]json.RawMessage
			if trimmed == "" || !json.Valid([]byte(trimmed)) || json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
				normalized[messageIndex].ToolCalls[callIndex].Function.Arguments = json.RawMessage(`{}`)
			}
		}
	}
	return json.Marshal(normalized)
}

func (e AgentRuntimeTurnExecutor) recordRuntimeFailed(ctx context.Context, err error, emit func(context.Context, agentruntime.Step) error) error {
	if e.Store == nil || err == nil || ctx.Err() != nil {
		return nil
	}
	data := map[string]any{}
	var providerError *llm.ProviderError
	if errors.As(err, &providerError) {
		data["provider_error"] = map[string]any{
			"class":          providerError.Class,
			"status_code":    providerError.StatusCode,
			"retryable":      providerError.Retryable,
			"retry_after_ms": providerError.RetryAfter.Milliseconds(),
			"attempts":       providerError.Attempts,
			"message":        providerError.Message,
		}
	}
	return emit(ctx, agentruntime.Step{
		Type:    managedagents.EventRuntimeFailed,
		Message: err.Error(),
		Data:    data,
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
