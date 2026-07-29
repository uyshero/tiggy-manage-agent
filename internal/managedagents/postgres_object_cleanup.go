package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectcleanup"
)

const objectCleanupColumns = `
	id, workspace_id, object_ref_id, storage_provider, bucket, object_key, object_version,
	reason, safe_to_delete, status, attempt_count, next_attempt_at, lease_owner,
	lease_expires_at, last_error, object_was_missing, created_at, updated_at, completed_at
`

const objectCleanupReturningColumns = `
	journal.id, journal.workspace_id, journal.object_ref_id, journal.storage_provider,
	journal.bucket, journal.object_key, journal.object_version, journal.reason,
	journal.safe_to_delete, journal.status, journal.attempt_count, journal.next_attempt_at,
	journal.lease_owner, journal.lease_expires_at, journal.last_error,
	journal.object_was_missing, journal.created_at, journal.updated_at, journal.completed_at
`

func (s *PostgresStore) ObjectCleanupWorkspaceContext(ctx context.Context, workspaceID string) (context.Context, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok && scope.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("%w: database workspace scope mismatch", ErrForbidden)
	}
	return ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
}

func (s *PostgresStore) EnqueueObjectCleanup(ctx context.Context, input objectcleanup.EnqueueInput) (objectcleanup.Job, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ObjectRefID = strings.TrimSpace(input.ObjectRefID)
	input.StorageProvider = strings.TrimSpace(input.StorageProvider)
	input.Bucket = strings.TrimSpace(input.Bucket)
	input.ObjectKey = strings.TrimSpace(input.ObjectKey)
	input.ObjectVersion = strings.TrimSpace(input.ObjectVersion)
	input.Reason = strings.TrimSpace(input.Reason)
	input.LastError = truncateObjectCleanupError(input.LastError)
	if input.WorkspaceID == "" || input.StorageProvider == "" || input.Bucket == "" || input.ObjectKey == "" || input.Reason == "" {
		return objectcleanup.Job{}, fmt.Errorf("%w: incomplete object cleanup identity", objectcleanup.ErrInvalid)
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return objectcleanup.Job{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "ocj", "tma_object_cleanup_journal_id_seq")
	if err != nil {
		return objectcleanup.Job{}, err
	}
	status := objectcleanup.StatusBlocked
	if input.SafeToDelete {
		status = objectcleanup.StatusPending
	}
	job, err := scanObjectCleanupJob(tx.QueryRowContext(ctx, `
		INSERT INTO object_cleanup_journal (
			id, workspace_id, object_ref_id, storage_provider, bucket, object_key, object_version,
			reason, safe_to_delete, status, next_attempt_at, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $11, $11)
		ON CONFLICT (workspace_id, storage_provider, bucket, object_key, object_version)
			WHERE status IN ('pending', 'processing', 'blocked', 'dead_letter')
		DO UPDATE SET
			object_ref_id = CASE WHEN EXCLUDED.object_ref_id <> '' THEN EXCLUDED.object_ref_id ELSE object_cleanup_journal.object_ref_id END,
			reason = EXCLUDED.reason,
			safe_to_delete = object_cleanup_journal.safe_to_delete OR EXCLUDED.safe_to_delete,
			attempt_count = CASE WHEN object_cleanup_journal.status = 'processing' THEN object_cleanup_journal.attempt_count ELSE 0 END,
			status = CASE
				WHEN object_cleanup_journal.status = 'processing' THEN 'processing'
				WHEN object_cleanup_journal.safe_to_delete OR EXCLUDED.safe_to_delete THEN 'pending'
				ELSE 'blocked'
			END,
			next_attempt_at = LEAST(object_cleanup_journal.next_attempt_at, EXCLUDED.next_attempt_at),
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at
		RETURNING `+objectCleanupColumns,
		id, scope.WorkspaceID, input.ObjectRefID, input.StorageProvider, input.Bucket, input.ObjectKey,
		input.ObjectVersion, input.Reason, input.SafeToDelete, status, input.CreatedAt, input.LastError))
	if err != nil {
		return objectcleanup.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return objectcleanup.Job{}, err
	}
	return job, nil
}

func (s *PostgresStore) StageOrphanObjectCleanup(ctx context.Context, input objectcleanup.StageInput) ([]objectcleanup.Job, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" || input.Cutoff.IsZero() || input.Now.IsZero() || input.Cutoff.After(input.Now) || input.Limit < 1 || input.Limit > 1000 {
		return nil, fmt.Errorf("%w: invalid orphan cleanup staging input", objectcleanup.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT object_ref.id, object_ref.storage_provider, object_ref.bucket,
			object_ref.object_key, object_ref.object_version
		FROM object_refs object_ref
		WHERE object_ref.workspace_id = $1
			AND object_ref.created_at <= $2
			AND object_ref.metadata_json #>> '{object_lifecycle,class}' = 'managed'
			AND NOT EXISTS (
				SELECT 1 FROM object_ref_links link
				WHERE link.object_ref_id = object_ref.id AND link.workspace_id = object_ref.workspace_id
			)
		ORDER BY object_ref.created_at, object_ref.id
		FOR UPDATE OF object_ref SKIP LOCKED
		LIMIT $3
	`, scope.WorkspaceID, input.Cutoff, input.Limit)
	if err != nil {
		return nil, err
	}
	type orphanObject struct {
		id, provider, bucket, key, version string
	}
	orphans := []orphanObject{}
	for rows.Next() {
		var orphan orphanObject
		if err := rows.Scan(&orphan.id, &orphan.provider, &orphan.bucket, &orphan.key, &orphan.version); err != nil {
			rows.Close()
			return nil, err
		}
		orphans = append(orphans, orphan)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	jobs := make([]objectcleanup.Job, 0, len(orphans))
	for _, orphan := range orphans {
		id, err := nextSequenceID(ctx, tx, "ocj", "tma_object_cleanup_journal_id_seq")
		if err != nil {
			return nil, err
		}
		job, err := scanObjectCleanupJob(tx.QueryRowContext(ctx, `
			INSERT INTO object_cleanup_journal (
				id, workspace_id, object_ref_id, storage_provider, bucket, object_key, object_version,
				reason, safe_to_delete, status, next_attempt_at, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE, 'pending', $9, $9, $9)
			ON CONFLICT (workspace_id, storage_provider, bucket, object_key, object_version)
				WHERE status IN ('pending', 'processing', 'blocked', 'dead_letter')
			DO UPDATE SET
				object_ref_id = EXCLUDED.object_ref_id,
				reason = EXCLUDED.reason,
				safe_to_delete = TRUE,
				attempt_count = CASE WHEN object_cleanup_journal.status = 'processing' THEN object_cleanup_journal.attempt_count ELSE 0 END,
				status = CASE WHEN object_cleanup_journal.status = 'processing' THEN 'processing' ELSE 'pending' END,
				next_attempt_at = LEAST(object_cleanup_journal.next_attempt_at, EXCLUDED.next_attempt_at),
				last_error = '',
				updated_at = EXCLUDED.updated_at
			RETURNING `+objectCleanupColumns,
			id, scope.WorkspaceID, orphan.id, orphan.provider, orphan.bucket, orphan.key,
			orphan.version, objectcleanup.ReasonManagedObjectOrphaned, input.Now))
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM object_refs object_ref
			WHERE object_ref.id = $1 AND object_ref.workspace_id = $2
				AND object_ref.metadata_json #>> '{object_lifecycle,class}' = 'managed'
				AND NOT EXISTS (
					SELECT 1 FROM object_ref_links link
					WHERE link.object_ref_id = object_ref.id AND link.workspace_id = object_ref.workspace_id
				)
		`, orphan.id, scope.WorkspaceID)
		if err := requireObjectCleanupStageDelete(result, err); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *PostgresStore) ClaimObjectCleanup(ctx context.Context, input objectcleanup.ClaimInput) ([]objectcleanup.Job, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.WorkerID = strings.TrimSpace(input.WorkerID)
	if input.WorkspaceID == "" || input.WorkerID == "" || input.Limit < 1 || input.Limit > 1000 || input.Now.IsZero() || !input.LeaseExpiresAt.After(input.Now) {
		return nil, fmt.Errorf("%w: invalid object cleanup claim", objectcleanup.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE object_cleanup_journal
		SET status = 'pending', lease_owner = '', lease_expires_at = NULL, updated_at = $2
		WHERE workspace_id = $1 AND status = 'processing' AND lease_expires_at <= $2
	`, scope.WorkspaceID, input.Now); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		WITH picked AS (
			SELECT id FROM object_cleanup_journal
			WHERE workspace_id = $1 AND status = 'pending' AND safe_to_delete AND next_attempt_at <= $2
			ORDER BY next_attempt_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		UPDATE object_cleanup_journal journal
		SET status = 'processing', attempt_count = journal.attempt_count + 1,
			lease_owner = $4, lease_expires_at = $5, updated_at = $2
		FROM picked
		WHERE journal.id = picked.id
		RETURNING `+objectCleanupReturningColumns,
		scope.WorkspaceID, input.Now, input.Limit, input.WorkerID, input.LeaseExpiresAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []objectcleanup.Job{}
	for rows.Next() {
		job, err := scanObjectCleanupJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *PostgresStore) CompleteObjectCleanup(ctx context.Context, input objectcleanup.CompleteInput) error {
	if input.CompletedAt.IsZero() {
		input.CompletedAt = time.Now().UTC()
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE object_cleanup_journal
		SET status = 'completed', lease_owner = '', lease_expires_at = NULL,
			object_was_missing = $4, completed_at = $5, updated_at = $5
		WHERE id = $1 AND workspace_id = $2 AND status = 'processing' AND lease_owner = $3
	`, strings.TrimSpace(input.JobID), scope.WorkspaceID, strings.TrimSpace(input.WorkerID), input.ObjectWasMissing, input.CompletedAt)
	if err := requireObjectCleanupAffected(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) FailObjectCleanup(ctx context.Context, input objectcleanup.FailInput) error {
	if input.FailedAt.IsZero() {
		input.FailedAt = time.Now().UTC()
	}
	status := objectcleanup.StatusPending
	if input.DeadLetter {
		status = objectcleanup.StatusDeadLetter
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE object_cleanup_journal
		SET status = $4, lease_owner = '', lease_expires_at = NULL, last_error = $5,
			next_attempt_at = $6, updated_at = $7
		WHERE id = $1 AND workspace_id = $2 AND status = 'processing' AND lease_owner = $3
	`, strings.TrimSpace(input.JobID), scope.WorkspaceID, strings.TrimSpace(input.WorkerID), status,
		truncateObjectCleanupError(input.ErrorMessage), input.NextAttemptAt, input.FailedAt)
	if err := requireObjectCleanupAffected(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListObjectCleanupWorkspaceIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM tma_list_workspace_ids()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

func scanObjectCleanupJob(scanner rowScanner) (objectcleanup.Job, error) {
	var job objectcleanup.Job
	if err := scanner.Scan(
		&job.ID, &job.WorkspaceID, &job.ObjectRefID, &job.StorageProvider, &job.Bucket,
		&job.ObjectKey, &job.ObjectVersion, &job.Reason, &job.SafeToDelete, &job.Status,
		&job.AttemptCount, &job.NextAttemptAt, &job.LeaseOwner, &job.LeaseExpiresAt,
		&job.LastError, &job.ObjectWasMissing, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt,
	); err == sql.ErrNoRows {
		return objectcleanup.Job{}, ErrNotFound
	} else if err != nil {
		return objectcleanup.Job{}, err
	}
	return job, nil
}

func requireObjectCleanupAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrLeaseLost
	}
	return nil
}

func requireObjectCleanupStageDelete(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("stage orphan cleanup: expected one object ref delete, got %d", affected)
	}
	return nil
}

func truncateObjectCleanupError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2000 {
		return value[:2000]
	}
	return value
}
