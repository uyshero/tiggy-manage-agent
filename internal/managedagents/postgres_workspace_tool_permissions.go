package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

var emptyWorkspaceToolPermissionPolicy = []byte(`{"permission_rules":[]}`)

func (s *PostgresStore) GetWorkspaceToolPermissionPolicyContext(ctx context.Context, workspaceID string) (WorkspaceToolPermissionPolicy, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return WorkspaceToolPermissionPolicy{}, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	defer tx.Rollback()

	var policy WorkspaceToolPermissionPolicy
	var raw []byte
	err = tx.QueryRowContext(ctx, `
		SELECT w.id, COALESCE(p.policy_json, '{"permission_rules":[]}'::jsonb), COALESCE(p.revision, 1),
			COALESCE(p.updated_by, 'system'), COALESCE(p.updated_at, w.created_at)
		FROM workspaces w
		LEFT JOIN workspace_tool_permission_policies p ON p.workspace_id = w.id
		WHERE w.id = $1
	`, workspaceID).Scan(&policy.WorkspaceID, &raw, &policy.Revision, &policy.UpdatedBy, &policy.UpdatedAt)
	if err == sql.ErrNoRows {
		return WorkspaceToolPermissionPolicy{}, ErrNotFound
	}
	if err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	policy.Policy = cloneRaw(raw)
	if err := tx.Commit(); err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) UpdateWorkspaceToolPermissionPolicyContext(ctx context.Context, input UpdateWorkspaceToolPermissionPolicyInput) (WorkspaceToolPermissionPolicy, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		return WorkspaceToolPermissionPolicy{}, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	if input.ExpectedRevision <= 0 {
		return WorkspaceToolPermissionPolicy{}, fmt.Errorf("%w: expected revision must be positive", ErrInvalid)
	}
	if len(input.Policy) == 0 || string(input.Policy) == "null" {
		input.Policy = cloneRaw(emptyWorkspaceToolPermissionPolicy)
	}
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.UpdatedBy == "" {
		input.UpdatedBy = "system"
	}
	now := time.Now().UTC()
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	defer tx.Rollback()

	var workspaceExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1)`, input.WorkspaceID).Scan(&workspaceExists); err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	if !workspaceExists {
		return WorkspaceToolPermissionPolicy{}, ErrNotFound
	}
	var policy WorkspaceToolPermissionPolicy
	var raw []byte
	err = tx.QueryRowContext(ctx, `
		INSERT INTO workspace_tool_permission_policies (workspace_id, policy_json, revision, updated_by, updated_at)
		SELECT $1, $2, 2, $3, $4
		WHERE $5 = 1
		ON CONFLICT (workspace_id) DO UPDATE
		SET policy_json = EXCLUDED.policy_json,
			revision = workspace_tool_permission_policies.revision + 1,
			updated_by = EXCLUDED.updated_by,
			updated_at = EXCLUDED.updated_at
		WHERE workspace_tool_permission_policies.revision = $5
		RETURNING workspace_id, policy_json, revision, updated_by, updated_at
	`, input.WorkspaceID, input.Policy, input.UpdatedBy, now, input.ExpectedRevision).Scan(
		&policy.WorkspaceID, &raw, &policy.Revision, &policy.UpdatedBy, &policy.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return WorkspaceToolPermissionPolicy{}, fmt.Errorf("%w: workspace tool permission policy revision changed", ErrRevisionConflict)
	}
	if err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceToolPermissionPolicy{}, err
	}
	policy.Policy = cloneRaw(raw)
	return policy, nil
}
