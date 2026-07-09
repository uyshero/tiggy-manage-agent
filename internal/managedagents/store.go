package managedagents

import (
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrInvalid    = errors.New("invalid input")
	ErrForbidden  = errors.New("forbidden")
	ErrConflict   = errors.New("conflict")
	ErrTerminated = errors.New("session terminated")
)

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
	case "", InterventionStatusPending:
		return InterventionStatusPending
	case InterventionStatusApproved:
		return InterventionStatusApproved
	case InterventionStatusRejected:
		return InterventionStatusRejected
	default:
		return ""
	}
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

func normalizeWorkerWorkStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case WorkerWorkStatusPending:
		return WorkerWorkStatusPending
	case WorkerWorkStatusLeased:
		return WorkerWorkStatusLeased
	case WorkerWorkStatusRunning:
		return WorkerWorkStatusRunning
	case WorkerWorkStatusCompleted:
		return WorkerWorkStatusCompleted
	case WorkerWorkStatusFailed:
		return WorkerWorkStatusFailed
	case WorkerWorkStatusCanceled:
		return WorkerWorkStatusCanceled
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
