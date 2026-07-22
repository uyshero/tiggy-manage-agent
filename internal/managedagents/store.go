package managedagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type databaseAccessScopeContextKey struct{}

var (
	ErrNotFound         = errors.New("not found")
	ErrInvalid          = errors.New("invalid input")
	ErrForbidden        = errors.New("forbidden")
	ErrConflict         = errors.New("conflict")
	ErrRevisionConflict = errors.New("revision conflict")
	ErrLeaseLost        = errors.New("lease lost")
	ErrSessionBusy      = errors.New("session busy")
	ErrTerminated       = errors.New("session terminated")
)

func ValidateAccessScope(scope AccessScope) (AccessScope, error) {
	scope.WorkspaceID = strings.TrimSpace(scope.WorkspaceID)
	scope.OwnerID = strings.TrimSpace(scope.OwnerID)
	if scope.WorkspaceID == "" {
		return AccessScope{}, fmt.Errorf("%w: access scope workspace_id is required", ErrInvalid)
	}
	return scope, nil
}

func ContextWithDatabaseAccessScope(ctx context.Context, scope AccessScope) (context.Context, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, databaseAccessScopeContextKey{}, scope), nil
}

func DatabaseAccessScopeFromContext(ctx context.Context) (AccessScope, bool) {
	if ctx == nil {
		return AccessScope{}, false
	}
	scope, ok := ctx.Value(databaseAccessScopeContextKey{}).(AccessScope)
	if !ok {
		return AccessScope{}, false
	}
	scope, err := ValidateAccessScope(scope)
	return scope, err == nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func normalizeLLMUsageGroupBy(value string) string {
	switch strings.TrimSpace(value) {
	case "", LLMUsageGroupByProviderModel, "provider-model":
		return LLMUsageGroupByProviderModel
	case LLMUsageGroupByProvider:
		return LLMUsageGroupByProvider
	case LLMUsageGroupByModel:
		return LLMUsageGroupByModel
	default:
		return ""
	}
}

func normalizeInterventionStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return ""
	case InterventionStatusPending:
		return InterventionStatusPending
	case InterventionStatusApproved:
		return InterventionStatusApproved
	case InterventionStatusRejected:
		return InterventionStatusRejected
	case InterventionStatusAnswered:
		return InterventionStatusAnswered
	case InterventionStatusSkipped:
		return InterventionStatusSkipped
	case InterventionStatusCanceled:
		return InterventionStatusCanceled
	case InterventionStatusExpired:
		return InterventionStatusExpired
	default:
		return ""
	}
}

func normalizeInterventionKind(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", InterventionKindToolApproval:
		return InterventionKindToolApproval
	case InterventionKindClarification:
		return InterventionKindClarification
	case InterventionKindPlanApproval:
		return InterventionKindPlanApproval
	case InterventionKindUploadRequest:
		return InterventionKindUploadRequest
	default:
		return ""
	}
}

func interventionStatusAllowed(kind, status string) bool {
	switch kind {
	case InterventionKindToolApproval, InterventionKindPlanApproval:
		return status == InterventionStatusApproved || status == InterventionStatusRejected
	case InterventionKindClarification, InterventionKindUploadRequest:
		return status == InterventionStatusAnswered || status == InterventionStatusSkipped || status == InterventionStatusCanceled || status == InterventionStatusExpired
	default:
		return false
	}
}

func PendingInterventionTurnStatus(interventions []SessionIntervention) string {
	hasPending := false
	for _, intervention := range interventions {
		if intervention.Status != InterventionStatusPending {
			continue
		}
		hasPending = true
		if intervention.Kind == InterventionKindClarification || intervention.Kind == InterventionKindUploadRequest {
			return TurnStatusWaitingHuman
		}
	}
	if hasPending {
		return TurnStatusWaitingApproval
	}
	return ""
}

func normalizeObjectVisibility(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", ObjectVisibilityWorkspace:
		return ObjectVisibilityWorkspace
	case ObjectVisibilitySession:
		return ObjectVisibilitySession
	default:
		return ""
	}
}

func normalizeArtifactType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", ArtifactTypeFile:
		return ArtifactTypeFile
	case ArtifactTypeSnapshot:
		return ArtifactTypeSnapshot
	case ArtifactTypeAsset:
		return ArtifactTypeAsset
	default:
		return ""
	}
}

func normalizeWorkerType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", WorkerTypeLocal:
		return WorkerTypeLocal
	case WorkerTypeShared:
		return WorkerTypeShared
	case WorkerTypeCloud:
		return WorkerTypeCloud
	default:
		return ""
	}
}

func normalizeWorkerStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", WorkerStatusOnline:
		return WorkerStatusOnline
	case WorkerStatusOffline:
		return WorkerStatusOffline
	case WorkerStatusDraining:
		return WorkerStatusDraining
	case WorkerStatusArchived:
		return WorkerStatusArchived
	default:
		return ""
	}
}

func normalizeWorkerWorkType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", WorkerWorkTypeToolExecution:
		return WorkerWorkTypeToolExecution
	case WorkerWorkTypeSandboxCommand:
		return WorkerWorkTypeSandboxCommand
	case WorkerWorkTypeArtifactSync:
		return WorkerWorkTypeArtifactSync
	default:
		return ""
	}
}

func normalizeSubagentTaskGroupStrategy(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", SubagentTaskGroupStrategyAllCompleted:
		return SubagentTaskGroupStrategyAllCompleted
	case SubagentTaskGroupStrategyAnyCompleted:
		return SubagentTaskGroupStrategyAnyCompleted
	case SubagentTaskGroupStrategyQuorum:
		return SubagentTaskGroupStrategyQuorum
	default:
		return ""
	}
}

func normalizeSubagentTaskGroupItemState(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", SubagentTaskGroupItemStateCreated:
		return SubagentTaskGroupItemStateCreated
	case SubagentTaskGroupItemStateStarted:
		return SubagentTaskGroupItemStateStarted
	case SubagentTaskGroupItemStateQueued:
		return SubagentTaskGroupItemStateQueued
	case SubagentTaskGroupItemStateRejected:
		return SubagentTaskGroupItemStateRejected
	default:
		return ""
	}
}

func normalizeSubagentTaskGroupReducer(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return SubagentTaskGroupReducerConcatText
	case SubagentTaskGroupReducerNone:
		return SubagentTaskGroupReducerNone
	case SubagentTaskGroupReducerConcatText:
		return SubagentTaskGroupReducerConcatText
	case SubagentTaskGroupReducerJSONList:
		return SubagentTaskGroupReducerJSONList
	case SubagentTaskGroupReducerJSONObject:
		return SubagentTaskGroupReducerJSONObject
	case SubagentTaskGroupReducerFirstSuccess:
		return SubagentTaskGroupReducerFirstSuccess
	case SubagentTaskGroupReducerMajorityText:
		return SubagentTaskGroupReducerMajorityText
	case SubagentTaskGroupReducerJSONValues:
		return SubagentTaskGroupReducerJSONValues
	case SubagentTaskGroupReducerMergeObjects:
		return SubagentTaskGroupReducerMergeObjects
	case SubagentTaskGroupReducerFirstValue:
		return SubagentTaskGroupReducerFirstValue
	case SubagentTaskGroupReducerMajorityValue:
		return SubagentTaskGroupReducerMajorityValue
	default:
		return ""
	}
}

func agentLLMProvider(input CreateAgentInput) string {
	return defaultString(input.LLMProvider, "fake")
}

func agentLLMModel(input CreateAgentInput) string {
	return defaultString(input.LLMModel, input.Model)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func mustRaw(value string) json.RawMessage {
	return json.RawMessage(value)
}

func statusPayload(status string, turnID string) json.RawMessage {
	payload := map[string]string{"status": status}
	if turnID != "" {
		payload["turn_id"] = turnID
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return mustRaw(`{"status":"` + status + `"}`)
	}
	return encoded
}

func runningStatusPayload(session Session, turnID string) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"status":               "running",
		"turn_id":              turnID,
		"agent_id":             session.AgentID,
		"agent_config_version": session.AgentConfigVersion,
	})
	return payload
}

func failedTurnIdlePayload(turnID string, reason string) json.RawMessage {
	payload := map[string]string{
		"status":           "idle",
		"last_turn_status": "failed",
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}
	if reason != "" {
		payload["reason"] = reason
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return mustRaw(`{"status":"idle","last_turn_status":"failed"}`)
	}
	return encoded
}

func payloadWithTurnID(payload json.RawMessage, turnID string) json.RawMessage {
	var object map[string]any
	if len(payload) == 0 || string(payload) == "null" {
		object = make(map[string]any)
	} else if err := json.Unmarshal(payload, &object); err != nil {
		object = make(map[string]any)
	} else if object == nil {
		object = make(map[string]any)
	}

	object["turn_id"] = turnID
	encoded, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return encoded
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}

	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}
