package managedagents

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentcore"
)

type ToolPermissionAuditRecord struct {
	SessionID        string    `json:"session_id"`
	TurnID           string    `json:"turn_id"`
	CallID           string    `json:"call_id"`
	Tool             string    `json:"tool"`
	Path             string    `json:"path,omitempty"`
	Decision         string    `json:"decision"`
	Allowed          bool      `json:"allowed"`
	Required         bool      `json:"required"`
	InterventionMode string    `json:"intervention_mode"`
	ApprovalPolicy   string    `json:"approval_policy,omitempty"`
	ApprovalStatus   string    `json:"approval_status"`
	ExecutionStatus  string    `json:"execution_status"`
	Reason           string    `json:"reason,omitempty"`
	Risk             string    `json:"risk,omitempty"`
	MatchedRuleID    string    `json:"matched_rule_id,omitempty"`
	RuleSource       string    `json:"rule_source,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type ListToolPermissionAuditInput struct {
	SessionID    string
	Decision     string
	Tool         string
	Limit        int
	Before       *time.Time
	BeforeTurnID string
	BeforeCallID string
}

type ToolPermissionAuditReader interface {
	ListToolPermissionAuditContext(context.Context, ListToolPermissionAuditInput) ([]ToolPermissionAuditRecord, error)
}

func ProjectToolPermissionAudit(events []Event) []ToolPermissionAuditRecord {
	records := make([]*ToolPermissionAuditRecord, 0)
	byCall := map[string]*ToolPermissionAuditRecord{}
	for _, event := range events {
		turnID, data := toolPermissionAuditEventData(event)
		key := func(callID string) string { return turnID + "\x00" + strings.TrimSpace(callID) }
		switch event.Type {
		case string(agentcore.EventToolBatchPlanned):
			var plan agentcore.ToolBatchPlan
			if json.Unmarshal(data, &plan) != nil {
				continue
			}
			for _, planned := range plan.Calls {
				if planned.Permission == nil || strings.TrimSpace(planned.Call.ID) == "" {
					continue
				}
				approvalStatus := string(planned.ApprovalState)
				executionStatus := "planned"
				if planned.Disposition == agentcore.ToolDispositionDenied || planned.Permission.Decision == "deny" {
					executionStatus = "denied"
				}
				record := &ToolPermissionAuditRecord{
					SessionID: event.SessionID, TurnID: turnID, CallID: planned.Call.ID,
					Tool: planned.Call.Name, Path: ToolPermissionAuditCallPath(planned.Call.Arguments),
					Decision: planned.Permission.Decision, Allowed: planned.Permission.Allowed,
					Required: planned.Permission.Required, InterventionMode: planned.Permission.Mode,
					ApprovalPolicy: planned.Permission.ApprovalPolicy, ApprovalStatus: approvalStatus,
					ExecutionStatus: executionStatus, Reason: planned.Permission.Reason,
					Risk: planned.Permission.Risk, MatchedRuleID: planned.Permission.MatchedRuleID,
					RuleSource: planned.Permission.RuleSource, CreatedAt: event.CreatedAt,
				}
				byCall[key(planned.Call.ID)] = record
				records = append(records, record)
			}
		case string(agentcore.EventToolCallStarted), string(agentcore.EventToolCallResult):
			var entry agentcore.ToolCallJournalEntry
			if json.Unmarshal(data, &entry) == nil {
				if record := byCall[key(entry.CallID)]; record != nil && record.Decision != "deny" {
					record.ExecutionStatus = string(entry.Status)
				}
			}
		case string(agentcore.EventInterventionResolved):
			var decisions []agentcore.InteractionDecision
			if json.Unmarshal(data, &decisions) != nil {
				continue
			}
			for _, decision := range decisions {
				callID := strings.TrimPrefix(decision.InteractionID, "tool_approval:")
				if record := byCall[key(callID)]; record != nil {
					record.ApprovalStatus = decision.Status
				}
			}
		case EventRuntimeToolInterventionApproved, EventRuntimeToolInterventionRejected:
			var decision struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if json.Unmarshal(data, &decision) == nil {
				if record := byCall[key(decision.ID)]; record != nil {
					record.ApprovalStatus = decision.Status
				}
			}
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			if records[i].TurnID == records[j].TurnID {
				return records[i].CallID > records[j].CallID
			}
			return records[i].TurnID > records[j].TurnID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	result := make([]ToolPermissionAuditRecord, len(records))
	for index := range records {
		result[index] = *records[index]
	}
	return result
}

func ToolPermissionAuditCallPath(arguments json.RawMessage) string {
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil {
		return ""
	}
	value, _ := object["path"].(string)
	return strings.TrimSpace(value)
}

func toolPermissionAuditEventData(event Event) (string, json.RawMessage) {
	var envelope struct {
		TurnID string          `json:"turn_id"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(event.Payload, &envelope) != nil {
		return event.TurnID, nil
	}
	turnID := strings.TrimSpace(event.TurnID)
	if turnID == "" {
		turnID = strings.TrimSpace(envelope.TurnID)
	}
	return turnID, envelope.Data
}
