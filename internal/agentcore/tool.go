package agentcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/model"
)

type ToolBatchPlan struct {
	Calls        []PlannedToolCall     `json:"calls"`
	Interactions []RequiredInteraction `json:"interactions,omitempty"`
}

type PlannedToolCall struct {
	Call           model.ToolCall `json:"call"`
	ExecutionMode  string         `json:"execution_mode"`
	SideEffect     string         `json:"side_effect"`
	Idempotency    string         `json:"idempotency"`
	IdempotencyKey string         `json:"idempotency_key"`
	LockKey        string         `json:"lock_key,omitempty"`
	ApprovalStatus string         `json:"approval_status,omitempty"`
}

type RequiredInteraction struct {
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	CallID   string               `json:"call_id"`
	Request  json.RawMessage      `json:"request"`
	Decision *InteractionDecision `json:"decision,omitempty"`
}

type InteractionDecision struct {
	InteractionID string          `json:"interaction_id"`
	Status        string          `json:"status"`
	Response      json.RawMessage `json:"response,omitempty"`
	Reason        string          `json:"reason,omitempty"`
}

type PauseState struct {
	Reason       string                `json:"reason"`
	Interactions []RequiredInteraction `json:"interactions"`
}

type ToolBatchResult struct {
	Results []model.ToolResult `json:"results"`
}

type ToolCallStatus string

const (
	ToolCallStarted       ToolCallStatus = "started"
	ToolCallSucceeded     ToolCallStatus = "succeeded"
	ToolCallFailed        ToolCallStatus = "failed"
	ToolCallIndeterminate ToolCallStatus = "indeterminate"
)

type ToolCallJournalEntry struct {
	CallID         string            `json:"call_id"`
	Name           string            `json:"name"`
	Idempotency    string            `json:"idempotency"`
	IdempotencyKey string            `json:"idempotency_key"`
	Status         ToolCallStatus    `json:"status"`
	Attempt        int               `json:"attempt"`
	StartedAt      time.Time         `json:"started_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
	Result         *model.ToolResult `json:"result,omitempty"`
}

func StableToolIdempotencyKey(sessionID, turnID string, call model.ToolCall) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(turnID) + "\x00" + call.ID + "\x00" + call.Name + "\x00" + string(call.Arguments)))
	return "tma_tool_" + hex.EncodeToString(sum[:16])
}

func (p ToolBatchPlan) Validate() error {
	if len(p.Calls) == 0 {
		return fmt.Errorf("tool batch calls are required")
	}
	callIDs := make(map[string]struct{}, len(p.Calls))
	for index, planned := range p.Calls {
		if err := planned.Call.Validate(); err != nil {
			return fmt.Errorf("planned call %d: %w", index, err)
		}
		if _, exists := callIDs[planned.Call.ID]; exists {
			return fmt.Errorf("duplicate planned call id %q", planned.Call.ID)
		}
		callIDs[planned.Call.ID] = struct{}{}
		if planned.ExecutionMode != "sequential" && planned.ExecutionMode != "parallel" {
			return fmt.Errorf("planned call %q has invalid execution mode", planned.Call.ID)
		}
		if strings.TrimSpace(planned.Idempotency) == "" || strings.TrimSpace(planned.IdempotencyKey) == "" {
			return fmt.Errorf("planned call %q requires idempotency metadata", planned.Call.ID)
		}
	}
	interactionIDs := make(map[string]struct{}, len(p.Interactions))
	for index, interaction := range p.Interactions {
		if strings.TrimSpace(interaction.ID) == "" || strings.TrimSpace(interaction.Kind) == "" || strings.TrimSpace(interaction.CallID) == "" {
			return fmt.Errorf("interaction %d is incomplete", index)
		}
		if _, exists := callIDs[interaction.CallID]; !exists {
			return fmt.Errorf("interaction %q references unknown call %q", interaction.ID, interaction.CallID)
		}
		if _, exists := interactionIDs[interaction.ID]; exists {
			return fmt.Errorf("duplicate interaction id %q", interaction.ID)
		}
		interactionIDs[interaction.ID] = struct{}{}
		if len(interaction.Request) == 0 || !json.Valid(interaction.Request) {
			return fmt.Errorf("interaction %q request must be valid JSON", interaction.ID)
		}
		if interaction.Decision != nil {
			if interaction.Decision.InteractionID != interaction.ID || (interaction.Decision.Status != "approved" && interaction.Decision.Status != "rejected") {
				return fmt.Errorf("interaction %q decision is invalid", interaction.ID)
			}
			if len(interaction.Decision.Response) > 0 && !json.Valid(interaction.Decision.Response) {
				return fmt.Errorf("interaction %q decision response must be valid JSON", interaction.ID)
			}
		}
	}
	return nil
}

func (entry ToolCallJournalEntry) Validate() error {
	if strings.TrimSpace(entry.CallID) == "" || strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Idempotency) == "" || strings.TrimSpace(entry.IdempotencyKey) == "" {
		return fmt.Errorf("tool journal identity and idempotency metadata are required")
	}
	if entry.Attempt < 1 || entry.StartedAt.IsZero() {
		return fmt.Errorf("tool journal attempt and start time are required")
	}
	switch entry.Status {
	case ToolCallStarted:
		if entry.CompletedAt != nil || entry.Result != nil {
			return fmt.Errorf("started tool journal entry cannot contain a result")
		}
	case ToolCallSucceeded, ToolCallFailed, ToolCallIndeterminate:
		if entry.CompletedAt == nil || entry.CompletedAt.IsZero() || entry.Result == nil {
			return fmt.Errorf("terminal tool journal entry requires completion time and result")
		}
		if entry.Result.CallID != entry.CallID || entry.Result.Name != entry.Name {
			return fmt.Errorf("tool journal result does not match call identity")
		}
		message := model.Message{ID: "journal_validation", Role: model.RoleTool, Visibility: model.VisibilityInternal, Content: []model.Content{{Type: model.ContentToolResult, ToolResult: cloneToolResult(*entry.Result)}}}
		if err := message.Validate(); err != nil {
			return fmt.Errorf("tool journal result is invalid: %w", err)
		}
		if entry.Status == ToolCallSucceeded && entry.Result.IsError {
			return fmt.Errorf("succeeded tool journal entry contains an error result")
		}
		if entry.Status != ToolCallSucceeded && !entry.Result.IsError {
			return fmt.Errorf("failed tool journal entry requires an error result")
		}
	default:
		return fmt.Errorf("unsupported tool journal status %q", entry.Status)
	}
	return nil
}

func (p PauseState) Validate() error {
	if strings.TrimSpace(p.Reason) == "" || len(p.Interactions) == 0 {
		return fmt.Errorf("pause reason and interactions are required")
	}
	interactionIDs := make(map[string]struct{}, len(p.Interactions))
	for index, interaction := range p.Interactions {
		if strings.TrimSpace(interaction.ID) == "" || strings.TrimSpace(interaction.Kind) == "" || strings.TrimSpace(interaction.CallID) == "" {
			return fmt.Errorf("pause interaction %d is incomplete", index)
		}
		if _, exists := interactionIDs[interaction.ID]; exists {
			return fmt.Errorf("duplicate pause interaction id %q", interaction.ID)
		}
		interactionIDs[interaction.ID] = struct{}{}
		if len(interaction.Request) == 0 || !json.Valid(interaction.Request) {
			return fmt.Errorf("pause interaction %q request must be valid JSON", interaction.ID)
		}
	}
	return nil
}

func validatePauseMatchesPlan(pause PauseState, plan ToolBatchPlan) error {
	if len(pause.Interactions) != len(plan.Interactions) {
		return fmt.Errorf("pause interactions do not match pending tool plan")
	}
	for index := range pause.Interactions {
		paused := pause.Interactions[index]
		planned := plan.Interactions[index]
		if paused.ID != planned.ID || paused.Kind != planned.Kind || paused.CallID != planned.CallID || string(paused.Request) != string(planned.Request) {
			return fmt.Errorf("pause interaction %d does not match pending tool plan", index)
		}
	}
	return nil
}

func cloneToolBatchPlan(value *ToolBatchPlan) *ToolBatchPlan {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Calls = make([]PlannedToolCall, len(value.Calls))
	for index, planned := range value.Calls {
		cloned.Calls[index] = planned
		cloned.Calls[index].Call.Arguments = append(json.RawMessage(nil), planned.Call.Arguments...)
	}
	cloned.Interactions = cloneInteractions(value.Interactions)
	return &cloned
}

func cloneToolCallJournal(values []ToolCallJournalEntry) []ToolCallJournalEntry {
	if values == nil {
		return nil
	}
	cloned := make([]ToolCallJournalEntry, len(values))
	for index, entry := range values {
		cloned[index] = entry
		if entry.CompletedAt != nil {
			completedAt := *entry.CompletedAt
			cloned[index].CompletedAt = &completedAt
		}
		if entry.Result != nil {
			cloned[index].Result = cloneToolResult(*entry.Result)
		}
	}
	return cloned
}

func clonePauseState(value *PauseState) *PauseState {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Interactions = cloneInteractions(value.Interactions)
	return &cloned
}

func cloneInteractions(values []RequiredInteraction) []RequiredInteraction {
	if values == nil {
		return nil
	}
	cloned := make([]RequiredInteraction, len(values))
	for index, interaction := range values {
		cloned[index] = interaction
		cloned[index].Request = append(json.RawMessage(nil), interaction.Request...)
		if interaction.Decision != nil {
			decision := *interaction.Decision
			decision.Response = append(json.RawMessage(nil), interaction.Decision.Response...)
			cloned[index].Decision = &decision
		}
	}
	return cloned
}
