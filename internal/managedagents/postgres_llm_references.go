package managedagents

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

func normalizeLLMDeleteReferenceError(err error, resource string) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23503" {
		return fmt.Errorf("%w: %s is referenced by an agent configuration or session", ErrConflict, resource)
	}
	return err
}

func normalizeLLMReferenceWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23503" {
		switch postgresError.ConstraintName {
		case "agent_config_versions_llm_model_fkey", "sessions_effective_llm_model_fkey":
			return fmt.Errorf("%w: llm provider/model selection changed during write; retry with the current catalog", ErrConflict)
		}
	}
	return err
}

func (s *PostgresStore) llmProviderReferencedAcrossWorkspaces(ctx context.Context, providerID string) (bool, error) {
	return s.llmReferenceAcrossWorkspaces(ctx, func(ctx context.Context, workspaceID string) (bool, error) {
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return false, err
		}
		defer tx.Rollback()
		var referenced bool
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM agent_config_versions config
				JOIN agents agent ON agent.id = config.agent_id
				WHERE agent.workspace_id = $2 AND config.llm_provider = $1
				UNION ALL
				SELECT 1
				FROM sessions session
				WHERE session.workspace_id = $2
					AND session.effective_llm_provider = $1
			)
		`, providerID, workspaceID).Scan(&referenced)
		if err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return referenced, nil
	})
}

func (s *PostgresStore) llmModelReferencedAcrossWorkspaces(ctx context.Context, providerID string, model string) (bool, error) {
	return s.llmReferenceAcrossWorkspaces(ctx, func(ctx context.Context, workspaceID string) (bool, error) {
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return false, err
		}
		defer tx.Rollback()
		var referenced bool
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM agent_config_versions config
				JOIN agents agent ON agent.id = config.agent_id
				WHERE agent.workspace_id = $3
					AND config.llm_provider = $1
					AND config.llm_model = $2
				UNION ALL
				SELECT 1
				FROM sessions session
				WHERE session.workspace_id = $3
					AND session.effective_llm_provider = $1
					AND session.effective_llm_model = $2
			)
		`, providerID, model, workspaceID).Scan(&referenced)
		if err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return referenced, nil
	})
}

func (s *PostgresStore) llmReferenceAcrossWorkspaces(ctx context.Context, check func(context.Context, string) (bool, error)) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM tma_list_workspace_ids()`)
	if err != nil {
		return false, err
	}
	workspaceIDs := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			rows.Close()
			return false, err
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, workspaceID := range workspaceIDs {
		referenced, err := check(ctx, workspaceID)
		if err != nil {
			return false, err
		}
		if referenced {
			return true, nil
		}
	}
	return false, nil
}
