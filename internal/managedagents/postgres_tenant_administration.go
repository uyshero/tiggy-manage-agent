package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *PostgresStore) GetWorkspaceMembership(ctx context.Context, workspaceID string, subject string) (WorkspaceMembership, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return WorkspaceMembership{}, fmt.Errorf("%w: member subject is required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return WorkspaceMembership{}, err
	}
	defer tx.Rollback()
	var item WorkspaceMembership
	err = tx.QueryRowContext(ctx, `
		SELECT workspace_id, subject, display_name, email, role, status, created_at, updated_at
		FROM workspace_memberships
		WHERE workspace_id = $1 AND subject = $2
	`, scope.WorkspaceID, subject).Scan(
		&item.WorkspaceID, &item.Subject, &item.DisplayName, &item.Email, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return WorkspaceMembership{}, ErrNotFound
	}
	if err != nil {
		return WorkspaceMembership{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceMembership{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListWorkspaceMemberships(ctx context.Context, workspaceID string) ([]WorkspaceMembership, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT workspace_id, subject, display_name, email, role, status, created_at, updated_at
		FROM workspace_memberships
		WHERE workspace_id = $1
		ORDER BY CASE role WHEN 'admin' THEN 1 WHEN 'operator' THEN 2 WHEN 'member' THEN 3 ELSE 4 END,
			display_name, subject
	`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkspaceMembership{}
	for rows.Next() {
		var item WorkspaceMembership
		if err := rows.Scan(&item.WorkspaceID, &item.Subject, &item.DisplayName, &item.Email, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) UpsertWorkspaceMembership(ctx context.Context, input UpsertWorkspaceMembershipInput) (WorkspaceMembership, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Subject == "" {
		return WorkspaceMembership{}, fmt.Errorf("%w: member subject is required", ErrInvalid)
	}
	if !validWorkspaceMembershipRole(input.Role) {
		return WorkspaceMembership{}, fmt.Errorf("%w: unsupported workspace role %q", ErrInvalid, input.Role)
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if !validWorkspaceMembershipStatus(input.Status) {
		return WorkspaceMembership{}, fmt.Errorf("%w: unsupported workspace member status %q", ErrInvalid, input.Status)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return WorkspaceMembership{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, scope.WorkspaceID); err != nil {
		return WorkspaceMembership{}, err
	}
	var currentRole, currentStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT role, status FROM workspace_memberships WHERE workspace_id = $1 AND subject = $2
	`, scope.WorkspaceID, input.Subject).Scan(&currentRole, &currentStatus)
	if err != nil && err != sql.ErrNoRows {
		return WorkspaceMembership{}, err
	}
	if currentRole == WorkspaceRoleAdmin && currentStatus == "active" && (input.Role != WorkspaceRoleAdmin || input.Status != "active") {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id = $1 AND role = 'admin' AND status = 'active'`, scope.WorkspaceID).Scan(&admins); err != nil {
			return WorkspaceMembership{}, err
		}
		if admins <= 1 {
			return WorkspaceMembership{}, fmt.Errorf("%w: workspace must retain an active administrator", ErrConflict)
		}
	}
	var item WorkspaceMembership
	err = tx.QueryRowContext(ctx, `
		INSERT INTO workspace_memberships (workspace_id, subject, display_name, email, role, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, subject) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			email = EXCLUDED.email,
			role = EXCLUDED.role,
			status = EXCLUDED.status,
			updated_at = now()
		RETURNING workspace_id, subject, display_name, email, role, status, created_at, updated_at
	`, scope.WorkspaceID, input.Subject, input.DisplayName, input.Email, input.Role, input.Status).Scan(
		&item.WorkspaceID, &item.Subject, &item.DisplayName, &item.Email, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return WorkspaceMembership{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceMembership{}, err
	}
	return item, nil
}

func (s *PostgresStore) DeleteWorkspaceMembership(ctx context.Context, workspaceID string, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("%w: member subject is required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, scope.WorkspaceID); err != nil {
		return err
	}
	var role, status string
	err = tx.QueryRowContext(ctx, `SELECT role, status FROM workspace_memberships WHERE workspace_id = $1 AND subject = $2`, scope.WorkspaceID, subject).Scan(&role, &status)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role == WorkspaceRoleAdmin && status == "active" {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id = $1 AND role = 'admin' AND status = 'active'`, scope.WorkspaceID).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("%w: workspace must retain an active administrator", ErrConflict)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id = $1 AND subject = $2`, scope.WorkspaceID, subject)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) IsPlatformAdmin(ctx context.Context, subject string) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT tma_is_platform_admin($1)`, strings.TrimSpace(subject)).Scan(&allowed)
	return allowed, err
}

func (s *PostgresStore) ListPlatformAdmins(ctx context.Context, callerSubject string) ([]PlatformRoleAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject, display_name, email, role, created_at, updated_at FROM tma_list_platform_admins($1)`, strings.TrimSpace(callerSubject))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PlatformRoleAssignment{}
	for rows.Next() {
		var item PlatformRoleAssignment
		if err := rows.Scan(&item.Subject, &item.DisplayName, &item.Email, &item.Role, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpsertPlatformAdmin(ctx context.Context, callerSubject string, input PlatformRoleAssignment) (PlatformRoleAssignment, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	if input.Subject == "" {
		return PlatformRoleAssignment{}, fmt.Errorf("%w: platform administrator subject is required", ErrInvalid)
	}
	var item PlatformRoleAssignment
	err := s.db.QueryRowContext(ctx, `
		SELECT subject, display_name, email, role, created_at, updated_at
		FROM tma_upsert_platform_admin($1, $2, $3, $4)
	`, strings.TrimSpace(callerSubject), strings.TrimSpace(input.Subject), strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Email)).Scan(
		&item.Subject, &item.DisplayName, &item.Email, &item.Role, &item.CreatedAt, &item.UpdatedAt,
	)
	return item, err
}

func (s *PostgresStore) DeletePlatformAdmin(ctx context.Context, callerSubject string, subject string) error {
	callerSubject = strings.TrimSpace(callerSubject)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("%w: platform administrator subject is required", ErrInvalid)
	}
	if callerSubject == subject {
		return fmt.Errorf("%w: platform administrator cannot remove itself", ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `SELECT tma_delete_platform_admin($1, $2)`, callerSubject, subject)
	return err
}

func (s *PostgresStore) ListTenantWorkspaces(ctx context.Context, callerSubject string) ([]TenantWorkspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, member_count FROM tma_list_platform_workspaces($1)`, strings.TrimSpace(callerSubject))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TenantWorkspace{}
	for rows.Next() {
		var item TenantWorkspace
		if err := rows.Scan(&item.ID, &item.Name, &item.CreatedAt, &item.MemberCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateTenantWorkspace(ctx context.Context, callerSubject string, name string) (TenantWorkspace, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return TenantWorkspace{}, fmt.Errorf("%w: workspace name is required and must not exceed 200 characters", ErrInvalid)
	}
	var item TenantWorkspace
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, created_at, member_count
		FROM tma_create_platform_workspace($1, $2)
	`, strings.TrimSpace(callerSubject), name).Scan(&item.ID, &item.Name, &item.CreatedAt, &item.MemberCount)
	return item, err
}

func validWorkspaceMembershipRole(role string) bool {
	switch role {
	case WorkspaceRoleViewer, WorkspaceRoleMember, WorkspaceRoleOperator, WorkspaceRoleAdmin:
		return true
	default:
		return false
	}
}

func validWorkspaceMembershipStatus(status string) bool {
	return status == "invited" || status == "active" || status == "suspended"
}
