package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const observabilityExporterRunColumns = `
	id, workspace_id, exporter, status, session_id, turn_id, trace_id, destination,
	message, attempt_count, next_retry_at, started_at, finished_at
`

type observabilityRowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func auditWorkspaceScope(ctx context.Context, requestedWorkspaceID string) (AccessScope, error) {
	requestedWorkspaceID = strings.TrimSpace(requestedWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if requestedWorkspaceID != "" && requestedWorkspaceID != scope.WorkspaceID {
			return AccessScope{}, fmt.Errorf("%w: audit workspace scope mismatch", ErrForbidden)
		}
		return scope, nil
	}
	if requestedWorkspaceID == "" {
		requestedWorkspaceID = DefaultWorkspaceID
	}
	return ValidateAccessScope(AccessScope{WorkspaceID: requestedWorkspaceID})
}

func (s *PostgresStore) beginAuditScopeTx(ctx context.Context, workspaceID string) (*sql.Tx, AccessScope, error) {
	scope, err := auditWorkspaceScope(ctx, workspaceID)
	if err != nil {
		return nil, AccessScope{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, AccessScope{}, err
	}
	if _, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID); err != nil {
		tx.Rollback()
		return nil, AccessScope{}, err
	}
	return tx, scope, nil
}

func requireAuditSessionWorkspace(ctx context.Context, tx *sql.Tx, sessionID, workspaceID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	var sessionWorkspaceID string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id = $1`, sessionID).Scan(&sessionWorkspaceID); err != nil {
		if err == sql.ErrNoRows {
			return ErrForbidden
		}
		return err
	}
	if sessionWorkspaceID != workspaceID {
		return ErrForbidden
	}
	return nil
}

func normalizeOperatorAuditInput(input RecordOperatorAuditInput) (RecordOperatorAuditInput, error) {
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.Action = strings.TrimSpace(input.Action)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.Outcome = strings.TrimSpace(input.Outcome)
	if input.PrincipalID == "" || input.Action == "" || input.ResourceType == "" {
		return RecordOperatorAuditInput{}, fmt.Errorf("%w: principal_id, action, and resource_type are required", ErrInvalid)
	}
	if input.Outcome != "succeeded" && input.Outcome != "failed" {
		return RecordOperatorAuditInput{}, fmt.Errorf("%w: audit outcome must be succeeded or failed", ErrInvalid)
	}
	input.Role = defaultString(strings.TrimSpace(input.Role), "admin")
	if len(input.Details) == 0 {
		input.Details = json.RawMessage(`{}`)
	}
	if !json.Valid(input.Details) {
		return RecordOperatorAuditInput{}, fmt.Errorf("%w: audit details must be valid JSON", ErrInvalid)
	}
	return input, nil
}

func recordOperatorAuditTx(ctx context.Context, tx *sql.Tx, scope AccessScope, input RecordOperatorAuditInput) (OperatorAuditRecord, error) {
	if err := requireAuditSessionWorkspace(ctx, tx, input.SessionID, scope.WorkspaceID); err != nil {
		return OperatorAuditRecord{}, err
	}
	id, err := nextSequenceID(ctx, tx, "oaud", "tma_operator_audit_id_seq")
	if err != nil {
		return OperatorAuditRecord{}, err
	}
	record, err := scanOperatorAuditRecord(tx.QueryRowContext(ctx, `
		INSERT INTO operator_audit_log (
			id, workspace_id, session_id, principal_id, operator_label, role, action,
			resource_type, resource_id, outcome, error_message, details_json
		) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, workspace_id, COALESCE(session_id, ''), principal_id, operator_label, role,
			action, resource_type, resource_id, outcome, error_message, details_json, created_at
	`, id, scope.WorkspaceID, strings.TrimSpace(input.SessionID), input.PrincipalID,
		strings.TrimSpace(input.OperatorLabel), input.Role, input.Action, input.ResourceType,
		strings.TrimSpace(input.ResourceID), input.Outcome, strings.TrimSpace(input.ErrorMessage), input.Details))
	if err != nil {
		return OperatorAuditRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) RecordOperatorAuditContext(ctx context.Context, input RecordOperatorAuditInput) (OperatorAuditRecord, error) {
	input, err := normalizeOperatorAuditInput(input)
	if err != nil {
		return OperatorAuditRecord{}, err
	}
	tx, scope, err := s.beginAuditScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return OperatorAuditRecord{}, err
	}
	defer tx.Rollback()
	record, err := recordOperatorAuditTx(ctx, tx, scope, input)
	if err != nil {
		return OperatorAuditRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return OperatorAuditRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) ListOperatorAuditContext(ctx context.Context, input ListOperatorAuditInput) ([]OperatorAuditRecord, error) {
	tx, scope, err := s.beginAuditScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, COALESCE(session_id, ''), principal_id, operator_label, role,
			action, resource_type, resource_id, outcome, error_message, details_json, created_at
		FROM operator_audit_log
		WHERE workspace_id = $1
			AND ($2 = '' OR session_id = $2)
			AND ($3 = '' OR principal_id = $3)
			AND ($4 = '' OR action = $4)
		ORDER BY created_at DESC, id DESC LIMIT $5
	`, scope.WorkspaceID, strings.TrimSpace(input.SessionID), strings.TrimSpace(input.PrincipalID), strings.TrimSpace(input.Action), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []OperatorAuditRecord{}
	for rows.Next() {
		record, err := scanOperatorAuditRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *PostgresStore) RecordObservabilityExporterRunContext(ctx context.Context, input RecordObservabilityExporterRunInput) (ObservabilityExporterRun, error) {
	if input.Exporter == "" || input.Status == "" || input.SessionID == "" || input.TurnID == "" {
		return ObservabilityExporterRun{}, fmt.Errorf("%w: exporter, status, session_id, and turn_id are required", ErrInvalid)
	}
	tx, scope, err := s.beginAuditScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return ObservabilityExporterRun{}, err
	}
	defer tx.Rollback()
	if err := requireAuditSessionWorkspace(ctx, tx, input.SessionID, scope.WorkspaceID); err != nil {
		return ObservabilityExporterRun{}, err
	}
	id, err := nextSequenceID(ctx, tx, "oexp", "tma_observability_exporter_run_id_seq")
	if err != nil {
		return ObservabilityExporterRun{}, err
	}
	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	finishedAt := input.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = startedAt
	}
	attemptCount := input.AttemptCount
	if attemptCount <= 0 {
		attemptCount = 1
	}
	run, err := scanObservabilityExporterRun(tx.QueryRowContext(ctx, `
		INSERT INTO observability_exporter_runs (
			id, workspace_id, exporter, status, session_id, turn_id, trace_id, destination,
			message, attempt_count, next_retry_at, started_at, finished_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING `+observabilityExporterRunColumns,
		id, scope.WorkspaceID, input.Exporter, input.Status, input.SessionID, input.TurnID,
		input.TraceID, input.Destination, input.Message, attemptCount, input.NextRetryAt, startedAt, finishedAt))
	if err != nil {
		return ObservabilityExporterRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return ObservabilityExporterRun{}, err
	}
	return run, nil
}

func (s *PostgresStore) ListObservabilityExporterRunsContext(ctx context.Context, input ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error) {
	tx, scope, err := s.beginAuditScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	runs, err := listObservabilityExporterRunsQuery(ctx, tx, scope.WorkspaceID, input)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return runs, nil
}

func listObservabilityExporterRunsQuery(ctx context.Context, q observabilityRowsQueryer, workspaceID string, input ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error) {
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := q.QueryContext(ctx, `
		SELECT `+observabilityExporterRunColumns+`
		FROM observability_exporter_runs
		WHERE workspace_id = $1
			AND ($2 = '' OR exporter = $2)
			AND ($3 = '' OR status = $3)
			AND ($4 = '' OR session_id = $4)
			AND ($5 = '' OR turn_id = $5)
			AND ($6::timestamptz IS NULL OR (status = 'failed' AND next_retry_at IS NOT NULL AND next_retry_at <= $6))
			AND ($7 = 0 OR attempt_count < $7)
		ORDER BY finished_at DESC, id DESC LIMIT $8
	`, workspaceID, input.Exporter, input.Status, input.SessionID, input.TurnID,
		nullableTime(input.RetryDueBefore), input.MaxAttemptCount, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []ObservabilityExporterRun{}
	for rows.Next() {
		run, err := scanObservabilityExporterRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func scanObservabilityExporterRun(scanner rowScanner) (ObservabilityExporterRun, error) {
	var run ObservabilityExporterRun
	err := scanner.Scan(
		&run.ID, &run.WorkspaceID, &run.Exporter, &run.Status, &run.SessionID, &run.TurnID,
		&run.TraceID, &run.Destination, &run.Message, &run.AttemptCount, &run.NextRetryAt,
		&run.StartedAt, &run.FinishedAt,
	)
	return run, err
}

func (s *PostgresStore) listObservabilityExporterRunsAllWorkspaces(ctx context.Context, input ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error) {
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	runs := make([]ObservabilityExporterRun, 0, limit)
	for _, workspaceID := range workspaceIDs {
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		workspaceInput := input
		workspaceInput.WorkspaceID = workspaceID
		workspaceInput.Limit = limit
		items, err := s.ListObservabilityExporterRunsContext(workspaceCtx, workspaceInput)
		if err != nil {
			return nil, err
		}
		runs = append(runs, items...)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].FinishedAt.Equal(runs[j].FinishedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].FinishedAt.After(runs[j].FinishedAt)
	})
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

var _ OperatorAuditContextStore = (*PostgresStore)(nil)
var _ ObservabilityExporterRunContextStore = (*PostgresStore)(nil)
