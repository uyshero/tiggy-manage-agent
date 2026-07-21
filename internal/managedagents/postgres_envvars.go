package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/envvars"
)

func (s *PostgresStore) ListEncryptedEnvironmentVariables(ctx context.Context, workspaceID string) ([]envvars.EncryptedVariable, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT workspace_id, owner_id, name, ciphertext, created_at, updated_at
		FROM managed_environment_variables
		WHERE workspace_id = $1
		ORDER BY name, owner_id
	`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	var records []envvars.EncryptedVariable
	for rows.Next() {
		var record envvars.EncryptedVariable
		if err := rows.Scan(&record.WorkspaceID, &record.OwnerID, &record.Name, &record.Ciphertext, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
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
	return records, nil
}

func (s *PostgresStore) UpsertEncryptedEnvironmentVariable(ctx context.Context, input envvars.EncryptedVariable) (envvars.EncryptedVariable, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return envvars.EncryptedVariable{}, err
	}
	defer tx.Rollback()
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = scope.OwnerID
	}
	if ownerID != scope.OwnerID {
		return envvars.EncryptedVariable{}, fmt.Errorf("%w: environment variable owner scope mismatch", ErrForbidden)
	}
	now := time.Now().UTC()
	var record envvars.EncryptedVariable
	err = tx.QueryRowContext(ctx, `
		INSERT INTO managed_environment_variables (workspace_id, owner_id, name, ciphertext, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (workspace_id, owner_id, name) DO UPDATE SET ciphertext = EXCLUDED.ciphertext, updated_at = EXCLUDED.updated_at
		RETURNING workspace_id, owner_id, name, ciphertext, created_at, updated_at
	`, scope.WorkspaceID, ownerID, strings.TrimSpace(input.Name), input.Ciphertext, now).Scan(
		&record.WorkspaceID, &record.OwnerID, &record.Name, &record.Ciphertext, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return envvars.EncryptedVariable{}, err
	}
	if err := tx.Commit(); err != nil {
		return envvars.EncryptedVariable{}, err
	}
	return record, nil
}

func (s *PostgresStore) DeleteEncryptedEnvironmentVariable(ctx context.Context, workspaceID string, name string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		DELETE FROM managed_environment_variables
		WHERE workspace_id = $1 AND owner_id = $2 AND name = $3
	`, scope.WorkspaceID, scope.OwnerID, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.Join(sql.ErrNoRows, ErrNotFound)
	}
	return tx.Commit()
}

func (s *PostgresStore) beginDatabaseAccessScope(ctx context.Context, requestedWorkspaceID string) (*sql.Tx, AccessScope, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, AccessScope{}, err
	}
	scope, err := setDatabaseAccessScope(ctx, tx, requestedWorkspaceID)
	if err != nil {
		tx.Rollback()
		return nil, AccessScope{}, err
	}
	return tx, scope, nil
}

func setDatabaseAccessScope(ctx context.Context, tx *sql.Tx, requestedWorkspaceID string) (AccessScope, error) {
	requestedWorkspaceID = strings.TrimSpace(requestedWorkspaceID)
	if requestedWorkspaceID == "" {
		return AccessScope{}, fmt.Errorf("%w: database workspace scope is required", ErrInvalid)
	}
	scope := AccessScope{WorkspaceID: requestedWorkspaceID}
	if contextScope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if contextScope.WorkspaceID != requestedWorkspaceID {
			return AccessScope{}, fmt.Errorf("%w: database workspace scope mismatch", ErrForbidden)
		}
		scope = contextScope
	}
	if _, err := tx.ExecContext(ctx, `
		SELECT
			set_config('tma.workspace_id', $1, true),
			set_config('tma.owner_id', $2, true)
	`, scope.WorkspaceID, scope.OwnerID); err != nil {
		return AccessScope{}, err
	}
	return scope, nil
}
