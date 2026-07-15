package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const workerReturningColumns = `
	id, workspace_id, name, worker_type, status, capabilities_json, metadata_json,
	registered_by, registered_at, last_seen_at, lease_expires_at, archived_at
`

const workerWorkReturningColumns = `
	id, workspace_id, worker_id, environment_id, session_id, turn_id, work_type,
	status, payload_json, result_json, error_message, lease_expires_at, created_at,
	updated_at, started_at, completed_at
`

func workerWorkspaceScope(ctx context.Context, requestedWorkspaceID string) (AccessScope, error) {
	requestedWorkspaceID = strings.TrimSpace(requestedWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if requestedWorkspaceID != "" && requestedWorkspaceID != scope.WorkspaceID {
			return AccessScope{}, fmt.Errorf("%w: worker workspace scope mismatch", ErrForbidden)
		}
		return scope, nil
	}
	if requestedWorkspaceID == "" {
		requestedWorkspaceID = DefaultWorkspaceID
	}
	return ValidateAccessScope(AccessScope{WorkspaceID: requestedWorkspaceID})
}

func (s *PostgresStore) beginWorkerScopeTx(ctx context.Context, requestedWorkspaceID string) (*sql.Tx, AccessScope, error) {
	scope, err := workerWorkspaceScope(ctx, requestedWorkspaceID)
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

func (s *PostgresStore) RegisterWorkerContext(ctx context.Context, input RegisterWorkerInput) (Worker, error) {
	if strings.TrimSpace(input.Name) == "" {
		return Worker{}, fmt.Errorf("%w: worker name is required", ErrInvalid)
	}
	workerType := normalizeWorkerType(input.WorkerType)
	if workerType == "" {
		return Worker{}, fmt.Errorf("%w: unsupported worker_type %q", ErrInvalid, input.WorkerType)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return Worker{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "wrk", "tma_worker_id_seq")
	if err != nil {
		return Worker{}, err
	}
	now := time.Now().UTC()
	leaseExpiresAt := workerLeaseExpiresAt(now, input.LeaseSeconds)
	worker, err := scanWorker(tx.QueryRowContext(ctx, `
		INSERT INTO workers (
			id, workspace_id, name, worker_type, status, capabilities_json, metadata_json,
			registered_by, registered_at, last_seen_at, lease_expires_at
		) VALUES ($1, $2, $3, $4, 'online', $5, $6, $7, $8, $8, $9)
		RETURNING `+workerReturningColumns,
		id, scope.WorkspaceID, strings.TrimSpace(input.Name), workerType,
		metadataJSON(input.Capabilities), metadataJSON(input.Metadata),
		defaultString(strings.TrimSpace(input.RegisteredBy), "system"), now, leaseExpiresAt,
	))
	if err != nil {
		return Worker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Worker{}, err
	}
	return worker, nil
}

func (s *PostgresStore) GetWorkerContext(ctx context.Context, id string) (Worker, error) {
	if strings.TrimSpace(id) == "" {
		return Worker{}, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return Worker{}, err
	}
	defer tx.Rollback()
	worker, err := scanWorker(tx.QueryRowContext(ctx, `SELECT `+workerReturningColumns+` FROM workers WHERE id = $1 AND workspace_id = $2`, id, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	if err != nil {
		return Worker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Worker{}, err
	}
	return worker, nil
}

func (s *PostgresStore) ListWorkersContext(ctx context.Context, input ListWorkersInput) ([]Worker, error) {
	status := strings.TrimSpace(input.Status)
	if status != "" && normalizeWorkerStatus(status) == "" {
		return nil, fmt.Errorf("%w: unsupported worker status %q", ErrInvalid, input.Status)
	}
	normalizedStatus := ""
	if status != "" {
		normalizedStatus = normalizeWorkerStatus(status)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT `+workerReturningColumns+` FROM workers
		WHERE workspace_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY registered_at DESC, id DESC
	`, scope.WorkspaceID, normalizedStatus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workers := []Worker{}
	for rows.Next() {
		worker, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker)
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
	return workers, nil
}

func (s *PostgresStore) HeartbeatWorkerContext(ctx context.Context, id string, input WorkerHeartbeatInput) (Worker, error) {
	if strings.TrimSpace(id) == "" {
		return Worker{}, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	status := normalizeWorkerStatus(input.Status)
	if status == "" || status == WorkerStatusArchived {
		return Worker{}, fmt.Errorf("%w: unsupported worker status %q", ErrInvalid, input.Status)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return Worker{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	worker, err := scanWorker(tx.QueryRowContext(ctx, `
		UPDATE workers SET status = $2,
			capabilities_json = CASE WHEN $3::jsonb IS NULL THEN capabilities_json ELSE $3::jsonb END,
			metadata_json = CASE WHEN $4::jsonb IS NULL THEN metadata_json ELSE $4::jsonb END,
			last_seen_at = $5, lease_expires_at = $6
		WHERE id = $1 AND workspace_id = $7 AND archived_at IS NULL RETURNING `+workerReturningColumns,
		id, status, nullableJSON(input.Capabilities), nullableJSON(input.Metadata), now,
		workerLeaseExpiresAt(now, input.LeaseSeconds), scope.WorkspaceID,
	))
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	if err != nil {
		return Worker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Worker{}, err
	}
	return worker, nil
}

func (s *PostgresStore) ArchiveWorkerContext(ctx context.Context, id string) (Worker, error) {
	if strings.TrimSpace(id) == "" {
		return Worker{}, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return Worker{}, err
	}
	defer tx.Rollback()
	worker, err := scanWorker(tx.QueryRowContext(ctx, `
		UPDATE workers SET status = 'archived', archived_at = $2
		WHERE id = $1 AND workspace_id = $3 AND archived_at IS NULL RETURNING `+workerReturningColumns,
		id, time.Now().UTC(), scope.WorkspaceID,
	))
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	if err != nil {
		return Worker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Worker{}, err
	}
	return worker, nil
}

func (s *PostgresStore) ReapExpiredWorkersContext(ctx context.Context, input ReapExpiredWorkersInput) ([]Worker, error) {
	tx, scope, err := s.beginWorkerScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		WITH expired AS (
			SELECT id FROM workers WHERE workspace_id = $2 AND status = 'online'
				AND archived_at IS NULL AND lease_expires_at IS NOT NULL AND lease_expires_at < $1
			ORDER BY lease_expires_at, id LIMIT $3 FOR UPDATE SKIP LOCKED
		)
		UPDATE workers AS worker SET status = 'offline' FROM expired
		WHERE worker.id = expired.id RETURNING
			worker.id, worker.workspace_id, worker.name, worker.worker_type, worker.status,
			worker.capabilities_json, worker.metadata_json, worker.registered_by,
			worker.registered_at, worker.last_seen_at, worker.lease_expires_at, worker.archived_at
	`, time.Now().UTC(), scope.WorkspaceID, reapLimit(input.Limit))
	if err != nil {
		return nil, err
	}
	workers := []Worker{}
	for rows.Next() {
		worker, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return workers, nil
}

func requireWorkerReferenceWorkspace(ctx context.Context, tx *sql.Tx, table, id, workspaceID string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	var foundWorkspaceID string
	query := "SELECT workspace_id FROM " + table + " WHERE id = $1"
	if err := tx.QueryRowContext(ctx, query, id).Scan(&foundWorkspaceID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: referenced %s is not available in this workspace", ErrForbidden, table)
		}
		return err
	}
	if foundWorkspaceID != workspaceID {
		return fmt.Errorf("%w: referenced %s belongs to another workspace", ErrForbidden, table)
	}
	return nil
}

func (s *PostgresStore) EnqueueWorkerWorkContext(ctx context.Context, input EnqueueWorkerWorkInput) (WorkerWork, error) {
	workType := normalizeWorkerWorkType(input.WorkType)
	if workType == "" {
		return WorkerWork{}, fmt.Errorf("%w: unsupported worker work_type %q", ErrInvalid, input.WorkType)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return WorkerWork{}, err
	}
	defer tx.Rollback()
	for _, ref := range []struct{ table, id string }{
		{"workers", input.WorkerID}, {"environments", input.EnvironmentID}, {"sessions", input.SessionID},
	} {
		if err := requireWorkerReferenceWorkspace(ctx, tx, ref.table, ref.id, scope.WorkspaceID); err != nil {
			return WorkerWork{}, err
		}
	}
	id, err := nextSequenceID(ctx, tx, "work", "tma_worker_work_id_seq")
	if err != nil {
		return WorkerWork{}, err
	}
	now := time.Now().UTC()
	work, err := scanWorkerWork(tx.QueryRowContext(ctx, `
		INSERT INTO worker_work (
			id, workspace_id, worker_id, environment_id, session_id, turn_id,
			work_type, status, payload_json, result_json, created_at, updated_at
		) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7, 'pending', $8, '{}'::jsonb, $9, $9)
		RETURNING `+workerWorkReturningColumns,
		id, scope.WorkspaceID, strings.TrimSpace(input.WorkerID), strings.TrimSpace(input.EnvironmentID),
		strings.TrimSpace(input.SessionID), strings.TrimSpace(input.TurnID), workType,
		metadataJSON(input.Payload), now,
	))
	if err != nil {
		return WorkerWork{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerWork{}, err
	}
	return work, nil
}

func (s *PostgresStore) GetWorkerWorkContext(ctx context.Context, id string) (WorkerWork, error) {
	if strings.TrimSpace(id) == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker work id is required", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return WorkerWork{}, err
	}
	defer tx.Rollback()
	work, err := scanWorkerWork(tx.QueryRowContext(ctx, `SELECT `+workerWorkReturningColumns+` FROM worker_work WHERE id = $1 AND workspace_id = $2`, id, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return WorkerWork{}, ErrNotFound
	}
	if err != nil {
		return WorkerWork{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerWork{}, err
	}
	return work, nil
}

func getWorkerTx(ctx context.Context, tx *sql.Tx, id string) (Worker, error) {
	worker, err := scanWorker(tx.QueryRowContext(ctx, `SELECT `+workerReturningColumns+` FROM workers WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	return worker, err
}

func getWorkerWorkTx(ctx context.Context, tx *sql.Tx, id string) (WorkerWork, error) {
	work, err := scanWorkerWork(tx.QueryRowContext(ctx, `SELECT `+workerWorkReturningColumns+` FROM worker_work WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return WorkerWork{}, ErrNotFound
	}
	return work, err
}

func (s *PostgresStore) PollWorkerWorkContext(ctx context.Context, workerID string, input PollWorkerWorkInput) (*WorkerWork, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	worker, err := getWorkerTx(ctx, tx, workerID)
	if err != nil {
		return nil, err
	}
	if worker.WorkspaceID != scope.WorkspaceID || worker.Status == WorkerStatusArchived {
		return nil, ErrNotFound
	}
	var workID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM worker_work WHERE workspace_id = $1 AND status = 'pending'
			AND (worker_id IS NULL OR worker_id = $2)
		ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	`, scope.WorkspaceID, workerID).Scan(&workID); err != nil {
		if err == sql.ErrNoRows {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	work, err := scanWorkerWork(tx.QueryRowContext(ctx, `
		UPDATE worker_work SET worker_id = $2, status = 'leased', lease_expires_at = $3, updated_at = $4
		WHERE id = $1 RETURNING `+workerWorkReturningColumns,
		workID, workerID, workerLeaseExpiresAt(now, input.LeaseSeconds), now,
	))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &work, nil
}

func (s *PostgresStore) updateWorkerWorkForWorkerContext(ctx context.Context, workerID, workID, query string, args ...any) (WorkerWork, error) {
	if strings.TrimSpace(workerID) == "" || strings.TrimSpace(workID) == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker id and work id are required", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return WorkerWork{}, err
	}
	defer tx.Rollback()
	worker, err := getWorkerTx(ctx, tx, workerID)
	if err != nil || worker.WorkspaceID != scope.WorkspaceID {
		if err == nil {
			err = ErrNotFound
		}
		return WorkerWork{}, err
	}
	work, err := scanWorkerWork(tx.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		current, getErr := getWorkerWorkTx(ctx, tx, workID)
		if getErr == nil && current.WorkerID == workerID && current.Status == WorkerWorkStatusCanceled {
			if err := tx.Commit(); err != nil {
				return WorkerWork{}, err
			}
			return current, nil
		}
		return WorkerWork{}, ErrNotFound
	}
	if err != nil {
		return WorkerWork{}, err
	}
	if work.WorkspaceID != worker.WorkspaceID {
		return WorkerWork{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return WorkerWork{}, err
	}
	return work, nil
}

func (s *PostgresStore) AckWorkerWorkContext(ctx context.Context, workerID, workID string) (WorkerWork, error) {
	now := time.Now().UTC()
	return s.updateWorkerWorkForWorkerContext(ctx, workerID, workID, `
		UPDATE worker_work SET status = 'running', started_at = COALESCE(started_at, $3), updated_at = $3
		WHERE id = $2 AND worker_id = $1 AND status IN ('leased', 'running')
		RETURNING `+workerWorkReturningColumns, workerID, workID, now)
}

func (s *PostgresStore) HeartbeatWorkerWorkContext(ctx context.Context, workerID, workID string, input WorkerWorkHeartbeatInput) (WorkerWork, error) {
	now := time.Now().UTC()
	return s.updateWorkerWorkForWorkerContext(ctx, workerID, workID, `
		UPDATE worker_work SET lease_expires_at = $3, updated_at = $4
		WHERE id = $2 AND worker_id = $1 AND status IN ('leased', 'running')
		RETURNING `+workerWorkReturningColumns,
		workerID, workID, workerLeaseExpiresAt(now, input.LeaseSeconds), now)
}

func (s *PostgresStore) CompleteWorkerWorkContext(ctx context.Context, workerID, workID string, input CompleteWorkerWorkInput) (WorkerWork, error) {
	status := WorkerWorkStatusFailed
	if input.Success {
		status = WorkerWorkStatusCompleted
	}
	now := time.Now().UTC()
	return s.updateWorkerWorkForWorkerContext(ctx, workerID, workID, `
		UPDATE worker_work SET status = $3, result_json = $4, error_message = $5, updated_at = $6, completed_at = $6
		WHERE id = $2 AND worker_id = $1 AND status IN ('leased', 'running')
		RETURNING `+workerWorkReturningColumns,
		workerID, workID, status, metadataJSON(input.Result), input.ErrorMessage, now)
}

func (s *PostgresStore) CancelWorkerWorkContext(ctx context.Context, workID string, input CancelWorkerWorkInput) (WorkerWork, error) {
	if strings.TrimSpace(workID) == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker work id is required", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return WorkerWork{}, err
	}
	defer tx.Rollback()
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "worker work canceled"
	}
	now := time.Now().UTC()
	work, err := scanWorkerWork(tx.QueryRowContext(ctx, `
		UPDATE worker_work SET status = 'canceled', error_message = $2, updated_at = $3, completed_at = $3
		WHERE id = $1 AND workspace_id = $4 AND status IN ('pending', 'leased', 'running') RETURNING `+workerWorkReturningColumns,
		workID, reason, now, scope.WorkspaceID,
	))
	if err == sql.ErrNoRows {
		work, err = getWorkerWorkTx(ctx, tx, workID)
		if err == nil && work.WorkspaceID != scope.WorkspaceID {
			err = ErrNotFound
		}
	}
	if err != nil {
		return WorkerWork{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerWork{}, err
	}
	return work, nil
}

func (s *PostgresStore) RequeueWorkerWorkContext(ctx context.Context, workID string, input RequeueWorkerWorkInput) (WorkerWork, error) {
	if strings.TrimSpace(workID) == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker work id is required", ErrInvalid)
	}
	workerID := strings.TrimSpace(input.WorkerID)
	if input.ClearWorker && workerID != "" {
		return WorkerWork{}, fmt.Errorf("%w: requeue accepts either worker_id or clear_worker, not both", ErrInvalid)
	}
	tx, scope, err := s.beginWorkerScopeTx(ctx, "")
	if err != nil {
		return WorkerWork{}, err
	}
	defer tx.Rollback()
	original, err := scanWorkerWork(tx.QueryRowContext(ctx, `SELECT `+workerWorkReturningColumns+` FROM worker_work WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, workID, scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return WorkerWork{}, ErrNotFound
	}
	if err != nil {
		return WorkerWork{}, err
	}
	if original.Status != WorkerWorkStatusFailed && original.Status != WorkerWorkStatusCanceled {
		return WorkerWork{}, fmt.Errorf("%w: only failed or canceled worker work can be requeued", ErrConflict)
	}
	if !input.ClearWorker && workerID == "" {
		workerID = original.WorkerID
	}
	if err := requireWorkerReferenceWorkspace(ctx, tx, "workers", workerID, scope.WorkspaceID); err != nil {
		return WorkerWork{}, err
	}
	newID, err := nextSequenceID(ctx, tx, "work", "tma_worker_work_id_seq")
	if err != nil {
		return WorkerWork{}, err
	}
	now := time.Now().UTC()
	requeued, err := scanWorkerWork(tx.QueryRowContext(ctx, `
		INSERT INTO worker_work (
			id, workspace_id, worker_id, environment_id, session_id, turn_id,
			work_type, status, payload_json, result_json, created_at, updated_at
		) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7, 'pending', $8, '{}'::jsonb, $9, $9)
		RETURNING `+workerWorkReturningColumns,
		newID, original.WorkspaceID, workerID, original.EnvironmentID, original.SessionID,
		original.TurnID, original.WorkType, metadataJSON(original.Payload), now,
	))
	if err != nil {
		return WorkerWork{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerWork{}, err
	}
	return requeued, nil
}

func (s *PostgresStore) ReapExpiredWorkerWorkContext(ctx context.Context, input ReapExpiredWorkerWorkInput) ([]WorkerWork, error) {
	tx, scope, err := s.beginWorkerScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `
		WITH expired AS (
			SELECT id FROM worker_work WHERE workspace_id = $2 AND status IN ('leased', 'running')
				AND lease_expires_at IS NOT NULL AND lease_expires_at < $1
			ORDER BY lease_expires_at, id LIMIT $3 FOR UPDATE SKIP LOCKED
		)
		UPDATE worker_work AS work SET status = 'failed',
			error_message = COALESCE(NULLIF(work.error_message, ''), 'worker work lease expired at ' || work.lease_expires_at::text),
			updated_at = $1, completed_at = $1 FROM expired WHERE work.id = expired.id
		RETURNING work.id, work.workspace_id, work.worker_id, work.environment_id, work.session_id,
			work.turn_id, work.work_type, work.status, work.payload_json, work.result_json,
			work.error_message, work.lease_expires_at, work.created_at, work.updated_at,
			work.started_at, work.completed_at
	`, now, scope.WorkspaceID, reapLimit(input.Limit))
	if err != nil {
		return nil, err
	}
	works := []WorkerWork{}
	for rows.Next() {
		work, err := scanWorkerWork(rows)
		if err != nil {
			return nil, err
		}
		works = append(works, work)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return works, nil
}

// The background reapers enumerate the tenant directory and bind one
// transaction-local scope per workspace. They never use an RLS bypass flag.
func (s *PostgresStore) reapExpiredWorkersAllWorkspaces(ctx context.Context, input ReapExpiredWorkersInput) ([]Worker, error) {
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return nil, err
	}
	limit := reapLimit(input.Limit)
	items := make([]Worker, 0, limit)
	for _, workspaceID := range workspaceIDs {
		if len(items) >= limit {
			break
		}
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		expired, err := s.ReapExpiredWorkersContext(workspaceCtx, ReapExpiredWorkersInput{WorkspaceID: workspaceID, Limit: limit - len(items)})
		if err != nil {
			return nil, err
		}
		items = append(items, expired...)
	}
	return items, nil
}

func (s *PostgresStore) reapExpiredWorkerWorkAllWorkspaces(ctx context.Context, input ReapExpiredWorkerWorkInput) ([]WorkerWork, error) {
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return nil, err
	}
	limit := reapLimit(input.Limit)
	items := make([]WorkerWork, 0, limit)
	for _, workspaceID := range workspaceIDs {
		if len(items) >= limit {
			break
		}
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		expired, err := s.ReapExpiredWorkerWorkContext(workspaceCtx, ReapExpiredWorkerWorkInput{WorkspaceID: workspaceID, Limit: limit - len(items)})
		if err != nil {
			return nil, err
		}
		items = append(items, expired...)
	}
	return items, nil
}

var _ WorkerContextStore = (*PostgresStore)(nil)
