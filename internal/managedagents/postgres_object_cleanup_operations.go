package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectcleanup"
)

func (s *PostgresStore) ListObjectCleanup(ctx context.Context, input objectcleanup.ListInput) ([]objectcleanup.Job, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.WorkspaceID == "" || (input.Status != "" && !validObjectCleanupStatus(input.Status)) || (!input.CreatedFrom.IsZero() && !input.CreatedTo.IsZero() && input.CreatedFrom.After(input.CreatedTo)) {
		return nil, fmt.Errorf("%w: invalid object cleanup filters", objectcleanup.ErrInvalid)
	}
	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 200 {
		return nil, fmt.Errorf("%w: object cleanup limit must not exceed 200", objectcleanup.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT `+objectCleanupColumns+`
		FROM object_cleanup_journal
		WHERE workspace_id = $1
			AND ($2 = '' OR status = $2)
			AND ($3 = '' OR reason = $3)
			AND ($4::timestamptz IS NULL OR created_at >= $4)
			AND ($5::timestamptz IS NULL OR created_at <= $5)
		ORDER BY created_at DESC, id DESC
		LIMIT $6
	`, scope.WorkspaceID, input.Status, input.Reason, nullableTime(input.CreatedFrom), nullableTime(input.CreatedTo), input.Limit)
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

func (s *PostgresStore) GetObjectCleanupStats(ctx context.Context, workspaceID string, now time.Time) (objectcleanup.Stats, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || now.IsZero() {
		return objectcleanup.Stats{}, fmt.Errorf("%w: workspace ID and current time are required", objectcleanup.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return objectcleanup.Stats{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT status, COUNT(*), COALESCE(SUM(size_bytes), 0), COALESCE(SUM(attempt_count), 0),
			COUNT(*) FILTER (WHERE attempt_count > 1),
			COUNT(*) FILTER (WHERE object_was_missing),
			COALESCE(SUM(size_bytes) FILTER (WHERE status = 'completed' AND NOT object_was_missing), 0)
		FROM object_cleanup_journal
		WHERE workspace_id = $1
		GROUP BY status
		ORDER BY status
	`, scope.WorkspaceID)
	if err != nil {
		return objectcleanup.Stats{}, err
	}
	stats := objectcleanup.Stats{WorkspaceID: scope.WorkspaceID, Statuses: []objectcleanup.StatusStats{}}
	for rows.Next() {
		var item objectcleanup.StatusStats
		if err := rows.Scan(&item.Status, &item.Jobs, &item.Bytes, &item.Attempts, &item.RetriedJobs, &item.MissingObjects, &item.DeletedBytes); err != nil {
			rows.Close()
			return objectcleanup.Stats{}, err
		}
		stats.Statuses = append(stats.Statuses, item)
		stats.TotalAttempts += item.Attempts
		stats.TotalRetriedJobs += item.RetriedJobs
		stats.TotalDeletedBytes += item.DeletedBytes
	}
	if err := rows.Close(); err != nil {
		return objectcleanup.Stats{}, err
	}
	if err := rows.Err(); err != nil {
		return objectcleanup.Stats{}, err
	}
	var oldest sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT MIN(created_at) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE reason = $2)
		FROM object_cleanup_journal WHERE workspace_id = $1
	`, scope.WorkspaceID, objectcleanup.ReasonManagedObjectOrphaned).Scan(&oldest, &stats.OrphansStaged); err != nil {
		return objectcleanup.Stats{}, err
	}
	if oldest.Valid {
		value := oldest.Time.UTC()
		stats.OldestPendingAt = &value
		if now.After(value) {
			stats.OldestPendingAge = int64(now.Sub(value).Seconds())
		}
	}
	if err := tx.Commit(); err != nil {
		return objectcleanup.Stats{}, err
	}
	return stats, nil
}

func (s *PostgresStore) RetryObjectCleanup(ctx context.Context, input objectcleanup.RetryInput) (objectcleanup.Job, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.JobID = strings.TrimSpace(input.JobID)
	if input.WorkspaceID == "" || input.JobID == "" || input.Now.IsZero() {
		return objectcleanup.Job{}, fmt.Errorf("%w: invalid object cleanup retry", objectcleanup.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return objectcleanup.Job{}, err
	}
	defer tx.Rollback()
	current, err := getObjectCleanupJobForUpdate(ctx, tx, scope.WorkspaceID, input.JobID)
	if err != nil {
		return objectcleanup.Job{}, err
	}
	if current.Status != objectcleanup.StatusDeadLetter || !current.SafeToDelete {
		return objectcleanup.Job{}, fmt.Errorf("%w: only safe dead-letter cleanup jobs can be retried", ErrConflict)
	}
	job, err := scanObjectCleanupJob(tx.QueryRowContext(ctx, `
		UPDATE object_cleanup_journal
		SET status = 'pending', next_attempt_at = $3,
			lease_owner = '', lease_expires_at = NULL, completed_at = NULL, updated_at = $3
		WHERE id = $1 AND workspace_id = $2
		RETURNING `+objectCleanupColumns,
		input.JobID, scope.WorkspaceID, input.Now))
	if err != nil {
		return objectcleanup.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return objectcleanup.Job{}, err
	}
	return job, nil
}

func (s *PostgresStore) ApproveBlockedObjectCleanup(ctx context.Context, input objectcleanup.ApproveInput) (objectcleanup.Job, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.JobID = strings.TrimSpace(input.JobID)
	if input.WorkspaceID == "" || input.JobID == "" || input.Now.IsZero() {
		return objectcleanup.Job{}, fmt.Errorf("%w: invalid blocked cleanup approval", objectcleanup.ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return objectcleanup.Job{}, err
	}
	defer tx.Rollback()
	current, err := getObjectCleanupJobForUpdate(ctx, tx, scope.WorkspaceID, input.JobID)
	if err != nil {
		return objectcleanup.Job{}, err
	}
	if current.Status != objectcleanup.StatusBlocked || current.SafeToDelete {
		return objectcleanup.Job{}, fmt.Errorf("%w: only blocked cleanup jobs can be approved", ErrConflict)
	}
	if err := removeApprovedCleanupObjectRef(ctx, tx, scope.WorkspaceID, current); err != nil {
		return objectcleanup.Job{}, err
	}
	job, err := scanObjectCleanupJob(tx.QueryRowContext(ctx, `
		UPDATE object_cleanup_journal
		SET status = 'pending', safe_to_delete = TRUE, attempt_count = 0, next_attempt_at = $3,
			lease_owner = '', lease_expires_at = NULL, completed_at = NULL, updated_at = $3
		WHERE id = $1 AND workspace_id = $2
		RETURNING `+objectCleanupColumns,
		input.JobID, scope.WorkspaceID, input.Now))
	if err != nil {
		return objectcleanup.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return objectcleanup.Job{}, err
	}
	return job, nil
}

func getObjectCleanupJobForUpdate(ctx context.Context, tx *sql.Tx, workspaceID, jobID string) (objectcleanup.Job, error) {
	return scanObjectCleanupJob(tx.QueryRowContext(ctx, `
		SELECT `+objectCleanupColumns+` FROM object_cleanup_journal
		WHERE id = $1 AND workspace_id = $2 FOR UPDATE
	`, jobID, workspaceID))
}

func removeApprovedCleanupObjectRef(ctx context.Context, tx *sql.Tx, workspaceID string, job objectcleanup.Job) error {
	if strings.TrimSpace(job.ObjectRefID) == "" {
		return nil
	}
	var provider, bucket, key, version, lifecycle string
	var linked bool
	err := tx.QueryRowContext(ctx, `
		SELECT storage_provider, bucket, object_key, object_version,
			COALESCE(metadata_json #>> '{object_lifecycle,class}', ''),
			EXISTS (SELECT 1 FROM object_ref_links link WHERE link.object_ref_id = object_refs.id AND link.workspace_id = object_refs.workspace_id)
		FROM object_refs WHERE id = $1 AND workspace_id = $2 FOR UPDATE
	`, job.ObjectRefID, workspaceID).Scan(&provider, &bucket, &key, &version, &lifecycle, &linked)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if linked {
		return fmt.Errorf("%w: object ref %s still has links", ErrConflict, job.ObjectRefID)
	}
	if lifecycle != objectLifecycleManaged {
		return fmt.Errorf("%w: object ref %s is not managed", ErrConflict, job.ObjectRefID)
	}
	if provider != job.StorageProvider || bucket != job.Bucket || key != job.ObjectKey || version != job.ObjectVersion {
		return fmt.Errorf("%w: cleanup job storage identity no longer matches object ref", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM object_refs WHERE id = $1 AND workspace_id = $2`, job.ObjectRefID, workspaceID)
	return requireObjectCleanupStageDelete(result, err)
}

func validObjectCleanupStatus(status string) bool {
	switch status {
	case objectcleanup.StatusPending, objectcleanup.StatusProcessing, objectcleanup.StatusCompleted, objectcleanup.StatusBlocked, objectcleanup.StatusDeadLetter:
		return true
	default:
		return false
	}
}
