package managedagents

import (
	"context"
	"database/sql"
)

func (s *PostgresStore) CreateEnvironmentContext(ctx context.Context, input CreateEnvironmentInput) (Environment, error) {
	return s.createEnvironmentContext(ctx, input)
}

func (s *PostgresStore) GetEnvironment(id string) (Environment, error) {
	return queryEnvironment(context.Background(), s.db, id, "")
}

func (s *PostgresStore) GetEnvironmentScoped(id string, scope AccessScope) (Environment, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return Environment{}, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return Environment{}, err
	}
	return s.GetEnvironmentContext(ctx, id)
}

func (s *PostgresStore) GetEnvironmentContext(ctx context.Context, id string) (Environment, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
		if err != nil {
			return Environment{}, err
		}
		defer tx.Rollback()
		environment, err := queryEnvironment(ctx, tx, id, scope.WorkspaceID)
		if err != nil {
			return Environment{}, err
		}
		if err := tx.Commit(); err != nil {
			return Environment{}, err
		}
		return environment, nil
	}
	return s.GetEnvironment(id)
}

func (s *PostgresStore) ListEnvironments() ([]Environment, error) {
	return queryEnvironments(context.Background(), s.db, "")
}

func (s *PostgresStore) ListEnvironmentsContext(ctx context.Context) ([]Environment, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		environments, err := queryEnvironments(ctx, tx, scope.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return environments, nil
	}
	return s.ListEnvironments()
}

func queryEnvironment(ctx context.Context, q queryer, id string, workspaceID string) (Environment, error) {
	var environment Environment
	var config []byte
	var archivedAt sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, config_json, archived_at, created_at
		FROM environments
		WHERE id = $1 AND ($2 = '' OR workspace_id = $2)
	`, id, workspaceID).Scan(&environment.ID, &environment.WorkspaceID, &environment.Name, &config, &archivedAt, &environment.CreatedAt)
	if err == sql.ErrNoRows {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, err
	}
	environment.Config = cloneRaw(config)
	if archivedAt.Valid {
		environment.ArchivedAt = &archivedAt.Time
	}
	return environment, nil
}

func queryEnvironments(ctx context.Context, q rowsQueryer, workspaceID string) ([]Environment, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, workspace_id, name, config_json, created_at
		FROM environments
		WHERE archived_at IS NULL AND ($1 = '' OR workspace_id = $1)
		ORDER BY name, id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	environments := []Environment{}
	for rows.Next() {
		var environment Environment
		var config []byte
		if err := rows.Scan(&environment.ID, &environment.WorkspaceID, &environment.Name, &config, &environment.CreatedAt); err != nil {
			return nil, err
		}
		environment.Config = cloneRaw(config)
		environments = append(environments, environment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return environments, nil
}
