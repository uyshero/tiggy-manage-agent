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
	Calls              []PlannedToolCall     `json:"calls"`
	Interactions       []RequiredInteraction `json:"interactions,omitempty"`
	RegistryRevision   string                `json:"registry_revision,omitempty"`
	PolicyRevision     string                `json:"policy_revision,omitempty"`
	MiddlewareRevision string                `json:"middleware_revision,omitempty"`
}

type PlannedToolCall struct {
	Call            model.ToolCall          `json:"call"`
	ExecutionMode   string                  `json:"execution_mode"`
	SideEffect      string                  `json:"side_effect"`
	Idempotency     string                  `json:"idempotency"`
	IdempotencyKey  string                  `json:"idempotency_key"`
	LockKey         string                  `json:"lock_key,omitempty"`
	Disposition     ToolCallDisposition     `json:"disposition"`
	ValidationState ToolValidationState     `json:"validation_state"`
	ApprovalState   ToolApprovalState       `json:"approval_state"`
	ApprovalSource  ToolApprovalSource      `json:"approval_source,omitempty"`
	Permission      *ToolPermissionDecision `json:"permission,omitempty"`
}

type ToolCallDisposition string

const (
	ToolDispositionExecute     ToolCallDisposition = "execute"
	ToolDispositionReturnError ToolCallDisposition = "return_error"
	ToolDispositionDenied      ToolCallDisposition = "denied"
)

type ToolValidationState string

const (
	ToolValidationValid              ToolValidationState = "valid"
	ToolValidationInvalidArguments   ToolValidationState = "invalid_arguments"
	ToolValidationUnsupportedTool    ToolValidationState = "unsupported_tool"
	ToolValidationUnsupportedToolAPI ToolValidationState = "unsupported_tool_api"
)

type ToolApprovalState string

const (
	ToolApprovalNotRequired ToolApprovalState = "not_required"
	ToolApprovalPending     ToolApprovalState = "pending"
	ToolApprovalAuto        ToolApprovalState = "auto_approved"
	ToolApprovalApproved    ToolApprovalState = "approved"
	ToolApprovalRejected    ToolApprovalState = "rejected"
)

type ToolApprovalSource string

const (
	ToolApprovalSourcePolicy ToolApprovalSource = "policy"
	ToolApprovalSourceHuman  ToolApprovalSource = "human"
)

type ToolPermissionDecision struct {
	Decision       string `json:"decision"`
	Allowed        bool   `json:"allowed"`
	Required       bool   `json:"required"`
	Mode           string `json:"mode"`
	ApprovalPolicy string `json:"approval_policy,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Risk           string `json:"risk,omitempty"`
	MatchedRuleID  string `json:"matched_rule_id,omitempty"`
	RuleSource     string `json:"rule_source,omitempty"`
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

	ToolReconciliationRequestPurpose = "tool_reconciliation"
	ToolReconciliationExecuted       = "executed"
	ToolReconciliationNotExecuted    = "not_executed"
	ToolReconciliationCompensated    = "compensated"
)

type ToolReconciliation struct {
	Outcome    string    `json:"outcome"`
	Summary    string    `json:"summary"`
	Evidence   string    `json:"evidence,omitempty"`
	ResolvedAt time.Time `json:"resolved_at"`
}

type ToolCallJournalEntry struct {
	CallID         string              `json:"call_id"`
	Name           string              `json:"name"`
	Idempotency    string              `json:"idempotency"`
	IdempotencyKey string              `json:"idempotency_key"`
	Status         ToolCallStatus      `json:"status"`
	Attempt        int                 `json:"attempt"`
	StartedAt      time.Time           `json:"started_at"`
	CompletedAt    *time.Time          `json:"completed_at,omitempty"`
	Result         *model.ToolResult   `json:"result,omitempty"`
	Reconciliation *ToolReconciliation `json:"reconciliation,omitempty"`
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
		if planned.Permission != nil {
			if err := planned.Permission.validate(); err != nil {
				return fmt.Errorf("planned call %q permission: %w", planned.Call.ID, err)
			}
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
	for _, planned := range p.Calls {
		if err := planned.validateLifecycle(p.Interactions); err != nil {
			return fmt.Errorf("planned call %q: %w", planned.Call.ID, err)
		}
	}
	return nil
}

func (p PlannedToolCall) validateLifecycle(interactions []RequiredInteraction) error {
	disposition := p.Disposition
	validation := p.ValidationState
	approval := p.ApprovalState
	switch disposition {
	case ToolDispositionExecute, ToolDispositionReturnError, ToolDispositionDenied:
	default:
		return fmt.Errorf("unsupported disposition %q", disposition)
	}
	switch validation {
	case ToolValidationValid, ToolValidationInvalidArguments, ToolValidationUnsupportedTool, ToolValidationUnsupportedToolAPI:
	default:
		return fmt.Errorf("unsupported validation state %q", validation)
	}
	switch approval {
	case ToolApprovalNotRequired, ToolApprovalPending, ToolApprovalAuto, ToolApprovalApproved, ToolApprovalRejected:
	default:
		return fmt.Errorf("unsupported approval state %q", approval)
	}
	if validation != ToolValidationValid && disposition != ToolDispositionReturnError {
		return fmt.Errorf("validation state %q requires return_error disposition", validation)
	}
	if disposition == ToolDispositionDenied && (validation != ToolValidationValid || approval != ToolApprovalNotRequired) {
		return fmt.Errorf("denied disposition requires valid input without approval")
	}
	if p.Permission != nil {
		if disposition == ToolDispositionDenied && p.Permission.Decision != "deny" {
			return fmt.Errorf("denied disposition requires deny permission")
		}
		if p.Permission.Decision == "deny" && disposition != ToolDispositionDenied {
			return fmt.Errorf("deny permission requires denied disposition")
		}
		if approval == ToolApprovalAuto && (!p.Permission.Allowed || !p.Permission.Required) {
			return fmt.Errorf("auto_approved approval requires an allowed required permission")
		}
		if (approval == ToolApprovalPending || approval == ToolApprovalApproved || approval == ToolApprovalRejected) &&
			(p.Permission.Allowed || !p.Permission.Required) {
			return fmt.Errorf("human approval requires a non-allowed required permission")
		}
	}
	switch approval {
	case ToolApprovalNotRequired:
		if p.ApprovalSource != "" {
			return fmt.Errorf("not_required approval cannot have source %q", p.ApprovalSource)
		}
	case ToolApprovalAuto:
		if p.ApprovalSource != ToolApprovalSourcePolicy {
			return fmt.Errorf("auto_approved approval requires policy source")
		}
	case ToolApprovalPending, ToolApprovalApproved, ToolApprovalRejected:
		if p.ApprovalSource != ToolApprovalSourceHuman {
			return fmt.Errorf("%s approval requires human source", approval)
		}
		interaction, ok := toolApprovalInteraction(interactions, p.Call.ID)
		if !ok {
			return fmt.Errorf("%s approval requires a tool_approval interaction", approval)
		}
		if approval == ToolApprovalPending && interaction.Decision != nil {
			return fmt.Errorf("pending approval cannot contain a decision")
		}
		if approval != ToolApprovalPending && (interaction.Decision == nil || interaction.Decision.Status != string(approval)) {
			return fmt.Errorf("approval state does not match interaction decision")
		}
	}
	return nil
}

func (d ToolPermissionDecision) validate() error {
	switch d.Decision {
	case "allow":
		if !d.Allowed {
			return fmt.Errorf("allow decision must be allowed")
		}
	case "ask":
		if d.Allowed || !d.Required {
			return fmt.Errorf("ask decision must be non-allowed and required")
		}
	case "deny":
		if d.Allowed || d.Required {
			return fmt.Errorf("deny decision must be non-allowed and non-required")
		}
	default:
		return fmt.Errorf("unsupported decision %q", d.Decision)
	}
	return nil
}

func toolApprovalInteraction(interactions []RequiredInteraction, callID string) (RequiredInteraction, bool) {
	for _, interaction := range interactions {
		if interaction.CallID == callID && (interaction.Kind == "tool_approval" || interaction.Kind == "approval") {
			return interaction, true
		}
	}
	return RequiredInteraction{}, false
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
		if entry.CompletedAt != nil || entry.Result != nil || entry.Reconciliation != nil {
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
		if entry.Reconciliation != nil {
			if err := entry.Reconciliation.Validate(); err != nil {
				return err
			}
			if entry.Status == ToolCallIndeterminate {
				return fmt.Errorf("reconciled tool journal entry cannot remain indeterminate")
			}
			if entry.Reconciliation.Outcome == ToolReconciliationExecuted && entry.Status != ToolCallSucceeded {
				return fmt.Errorf("executed reconciliation requires succeeded status")
			}
			if entry.Reconciliation.Outcome != ToolReconciliationExecuted && entry.Status != ToolCallFailed {
				return fmt.Errorf("non-executed reconciliation requires failed status")
			}
		}
	default:
		return fmt.Errorf("unsupported tool journal status %q", entry.Status)
	}
	return nil
}

func (r ToolReconciliation) Validate() error {
	switch r.Outcome {
	case ToolReconciliationExecuted, ToolReconciliationNotExecuted, ToolReconciliationCompensated:
	default:
		return fmt.Errorf("unsupported tool reconciliation outcome %q", r.Outcome)
	}
	if strings.TrimSpace(r.Summary) == "" || r.ResolvedAt.IsZero() {
		return fmt.Errorf("tool reconciliation summary and resolved time are required")
	}
	if len([]rune(r.Summary)) > 2000 || len([]rune(r.Evidence)) > 2000 {
		return fmt.Errorf("tool reconciliation text exceeds 2000 characters")
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
	if len(pause.Interactions) > len(plan.Interactions) {
		return fmt.Errorf("pause interactions do not match pending tool plan")
	}
	plannedByID := make(map[string]RequiredInteraction, len(plan.Interactions))
	for _, interaction := range plan.Interactions {
		plannedByID[interaction.ID] = interaction
	}
	for index, paused := range pause.Interactions {
		planned, ok := plannedByID[paused.ID]
		if !ok || paused.Kind != planned.Kind || paused.CallID != planned.CallID || string(paused.Request) != string(planned.Request) {
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
		if planned.Permission != nil {
			permission := *planned.Permission
			cloned.Calls[index].Permission = &permission
		}
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
		if entry.Reconciliation != nil {
			reconciliation := *entry.Reconciliation
			cloned[index].Reconciliation = &reconciliation
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
