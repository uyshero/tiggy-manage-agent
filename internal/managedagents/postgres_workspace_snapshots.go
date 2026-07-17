package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type WorkspaceSnapshotStore interface {
	CreateWorkspaceSnapshot(ctx context.Context, input CreateWorkspaceSnapshotInput) (WorkspaceSnapshot, error)
	GetLatestWorkspaceSnapshot(ctx context.Context, sessionID string) (WorkspaceSnapshot, error)
}

func (s *PostgresStore) CreateWorkspaceSnapshot(ctx context.Context, input CreateWorkspaceSnapshotInput) (WorkspaceSnapshot, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.ObjectRefID = strings.TrimSpace(input.ObjectRefID)
	if input.SessionID == "" || input.ObjectRefID == "" || input.ChecksumSHA256 == "" {
		return WorkspaceSnapshot{}, fmt.Errorf("%w: snapshot session, object ref, and checksum are required", ErrInvalid)
	}
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return WorkspaceSnapshot{}, fmt.Errorf("%w: snapshot database scope is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID); err != nil {
		return WorkspaceSnapshot{}, err
	}
	var workspaceID string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id = $1`, input.SessionID).Scan(&workspaceID); err == sql.ErrNoRows {
		return WorkspaceSnapshot{}, ErrNotFound
	} else if err != nil {
		return WorkspaceSnapshot{}, err
	}
	if workspaceID != scope.WorkspaceID {
		return WorkspaceSnapshot{}, ErrForbidden
	}
	var objectWorkspace string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM object_refs WHERE id = $1`, input.ObjectRefID).Scan(&objectWorkspace); err != nil {
		return WorkspaceSnapshot{}, err
	}
	if objectWorkspace != workspaceID {
		return WorkspaceSnapshot{}, ErrForbidden
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "workspace-snapshot:"+input.SessionID); err != nil {
		return WorkspaceSnapshot{}, err
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM workspace_snapshots WHERE session_id = $1`, input.SessionID).Scan(&sequence); err != nil {
		return WorkspaceSnapshot{}, err
	}
	id, err := nextSequenceID(ctx, tx, "wsnp", "tma_workspace_snapshot_id_seq")
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	now := time.Now().UTC()
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	snapshot, err := scanWorkspaceSnapshot(tx.QueryRowContext(ctx, `
		INSERT INTO workspace_snapshots (id, workspace_id, session_id, sequence, object_ref_id, checksum_sha256, size_bytes, file_count, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, workspace_id, session_id, sequence, object_ref_id, checksum_sha256, size_bytes, file_count, created_by, created_at
	`, id, workspaceID, input.SessionID, sequence, input.ObjectRefID, input.ChecksumSHA256, input.SizeBytes, input.FileCount, input.CreatedBy, now))
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceSnapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) GetLatestWorkspaceSnapshot(ctx context.Context, sessionID string) (WorkspaceSnapshot, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return WorkspaceSnapshot{}, fmt.Errorf("%w: snapshot database scope is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID); err != nil {
		return WorkspaceSnapshot{}, err
	}
	var workspaceID string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id = $1`, strings.TrimSpace(sessionID)).Scan(&workspaceID); err == sql.ErrNoRows {
		return WorkspaceSnapshot{}, ErrNotFound
	} else if err != nil {
		return WorkspaceSnapshot{}, err
	}
	if workspaceID != scope.WorkspaceID {
		return WorkspaceSnapshot{}, ErrForbidden
	}
	return scanWorkspaceSnapshot(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, session_id, sequence, object_ref_id, checksum_sha256, size_bytes, file_count, created_by, created_at
		FROM workspace_snapshots WHERE session_id = $1 ORDER BY sequence DESC LIMIT 1
	`, sessionID))
}

type workspaceSnapshotScanner interface{ Scan(dest ...any) error }

func scanWorkspaceSnapshot(scanner workspaceSnapshotScanner) (WorkspaceSnapshot, error) {
	var value WorkspaceSnapshot
	err := scanner.Scan(&value.ID, &value.WorkspaceID, &value.SessionID, &value.Sequence, &value.ObjectRefID,
		&value.ChecksumSHA256, &value.SizeBytes, &value.FileCount, &value.CreatedBy, &value.CreatedAt)
	if err == sql.ErrNoRows {
		return WorkspaceSnapshot{}, ErrNotFound
	}
	return value, err
}
