package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentcore"
)

func (s *PostgresStore) projectToolPermissionAuditEventTx(ctx context.Context, tx *sql.Tx, event Event) error {
	turnID, data := toolPermissionAuditEventData(event)
	switch event.Type {
	case string(agentcore.EventToolBatchPlanned):
		var plan agentcore.ToolBatchPlan
		if err := json.Unmarshal(data, &plan); err != nil {
			return fmt.Errorf("decode tool permission audit plan: %w", err)
		}
		for _, planned := range plan.Calls {
			if planned.Permission == nil || strings.TrimSpace(planned.Call.ID) == "" {
				continue
			}
			approvalStatus := "not_required"
			if planned.Permission.Required && planned.Permission.Allowed {
				approvalStatus = "auto_approved"
			} else if planned.Permission.Required {
				approvalStatus = "pending"
			}
			executionStatus := "planned"
			if planned.Permission.Decision == "deny" {
				executionStatus = "denied"
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO tool_permission_audit_records (
					workspace_id, session_id, turn_id, call_id, tool, path,
					decision, allowed, required, intervention_mode, approval_policy,
					approval_status, execution_status, reason, risk, matched_rule_id,
					rule_source, created_at, updated_at
				)
				SELECT
					s.workspace_id, $1, $2, $3, $4, $5,
					$6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $17
				FROM sessions s
				WHERE s.id = $1
				ON CONFLICT (session_id, turn_id, call_id) DO NOTHING
			`, event.SessionID, turnID, planned.Call.ID, planned.Call.Name,
				ToolPermissionAuditCallPath(planned.Call.Arguments), planned.Permission.Decision,
				planned.Permission.Allowed, planned.Permission.Required, planned.Permission.Mode,
				planned.Permission.ApprovalPolicy, approvalStatus, executionStatus,
				planned.Permission.Reason, planned.Permission.Risk, planned.Permission.MatchedRuleID,
				planned.Permission.RuleSource, event.CreatedAt)
			if err != nil {
				return fmt.Errorf("insert tool permission audit record: %w", err)
			}
		}
	case string(agentcore.EventToolCallStarted), string(agentcore.EventToolCallResult):
		var entry agentcore.ToolCallJournalEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("decode tool permission audit journal: %w", err)
		}
		if entry.Status != agentcore.ToolCallStarted && entry.Status != agentcore.ToolCallSucceeded && entry.Status != agentcore.ToolCallFailed && entry.Status != agentcore.ToolCallIndeterminate {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE tool_permission_audit_records
			SET execution_status = $4, updated_at = $5
			WHERE session_id = $1 AND turn_id = $2 AND call_id = $3 AND decision <> 'deny'
		`, event.SessionID, turnID, entry.CallID, entry.Status, event.CreatedAt)
		if err != nil {
			return fmt.Errorf("update tool permission audit execution: %w", err)
		}
	case string(agentcore.EventInterventionResolved):
		var decisions []agentcore.InteractionDecision
		if err := json.Unmarshal(data, &decisions); err != nil {
			return fmt.Errorf("decode tool permission audit decisions: %w", err)
		}
		for _, decision := range decisions {
			if decision.Status != "approved" && decision.Status != "rejected" {
				continue
			}
			callID := strings.TrimPrefix(decision.InteractionID, "tool_approval:")
			if _, err := tx.ExecContext(ctx, `
				UPDATE tool_permission_audit_records
				SET approval_status = $4, updated_at = $5
				WHERE session_id = $1 AND turn_id = $2 AND call_id = $3
			`, event.SessionID, turnID, callID, decision.Status, event.CreatedAt); err != nil {
				return fmt.Errorf("update tool permission audit approval: %w", err)
			}
		}
	case EventRuntimeToolInterventionApproved, EventRuntimeToolInterventionRejected:
		var decision struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(data, &decision); err != nil {
			return fmt.Errorf("decode managed tool permission audit decision: %w", err)
		}
		if decision.Status != "approved" && decision.Status != "rejected" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tool_permission_audit_records
			SET approval_status = $4, updated_at = $5
			WHERE session_id = $1 AND turn_id = $2 AND call_id = $3
		`, event.SessionID, turnID, decision.ID, decision.Status, event.CreatedAt); err != nil {
			return fmt.Errorf("update managed tool permission audit approval: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) ListToolPermissionAuditContext(ctx context.Context, input ListToolPermissionAuditInput) ([]ToolPermissionAuditRecord, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.Tool = strings.TrimSpace(input.Tool)
	if input.SessionID == "" || input.Limit < 1 || input.Limit > 201 {
		return nil, fmt.Errorf("%w: session_id and a limit between 1 and 201 are required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	session, err := getSessionTx(ctx, tx, input.SessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	hasCursor := input.Before != nil
	before := time.Time{}
	if hasCursor {
		before = input.Before.UTC()
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT
			session_id, turn_id, call_id, tool, path, decision, allowed, required,
			intervention_mode, approval_policy, approval_status, execution_status,
			reason, risk, matched_rule_id, rule_source, created_at
		FROM tool_permission_audit_records
		WHERE session_id = $1
			AND ($2 = '' OR decision = $2)
			AND ($3 = '' OR tool = $3)
			AND (NOT $4 OR (created_at, turn_id, call_id) < ($5, $6, $7))
		ORDER BY created_at DESC, turn_id DESC, call_id DESC
		LIMIT $8
	`, input.SessionID, input.Decision, input.Tool, hasCursor, before, input.BeforeTurnID, input.BeforeCallID, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]ToolPermissionAuditRecord, 0, input.Limit)
	for rows.Next() {
		var record ToolPermissionAuditRecord
		if err := rows.Scan(
			&record.SessionID, &record.TurnID, &record.CallID, &record.Tool, &record.Path,
			&record.Decision, &record.Allowed, &record.Required, &record.InterventionMode,
			&record.ApprovalPolicy, &record.ApprovalStatus, &record.ExecutionStatus,
			&record.Reason, &record.Risk, &record.MatchedRuleID, &record.RuleSource,
			&record.CreatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return records, nil
}
