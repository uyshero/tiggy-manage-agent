package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/skillmarketplace"
)

func (s *PostgresStore) CreateMarketplacePolicy(ctx context.Context, input skillmarketplace.CreatePolicyInput) (skillmarketplace.PolicyRecord, skillmarketplace.PolicyVersion, error) {
	input.ScopeType = strings.ToLower(strings.TrimSpace(input.ScopeType))
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok && input.ScopeType == skillmarketplace.PolicyScopeWorkspace {
		input.WorkspaceID = scope.WorkspaceID
		input.OrganizationID = ""
	}
	if input.ScopeType != skillmarketplace.PolicyScopeOrganization && input.ScopeType != skillmarketplace.PolicyScopeWorkspace {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: unsupported marketplace policy scope_type", ErrInvalid)
	}
	if input.ScopeType == skillmarketplace.PolicyScopeOrganization && (input.OrganizationID == "" || input.WorkspaceID != "") {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: organization policy requires only organization_id", ErrInvalid)
	}
	if input.ScopeType == skillmarketplace.PolicyScopeWorkspace && (input.WorkspaceID == "" || input.OrganizationID != "") {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: workspace policy requires only workspace_id", ErrInvalid)
	}
	normalized, revision, encoded, err := normalizeMarketplacePolicy(input.Config)
	if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	requestedWorkspaceID := ""
	if input.ScopeType == skillmarketplace.PolicyScopeWorkspace {
		requestedWorkspaceID = input.WorkspaceID
	}
	tx, err := s.beginSkillScopeTx(ctx, requestedWorkspaceID)
	if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	defer tx.Rollback()
	if input.ScopeType == skillmarketplace.PolicyScopeOrganization {
		if err := authorizeMarketplaceOrganizationScope(ctx, tx, input.OrganizationID); err != nil {
			return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
		}
	}
	if err := ensureMarketplacePolicyScopeExists(ctx, tx, input); err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	policyID, err := nextSequenceID(ctx, tx, "smpol", "tma_skill_marketplace_policy_id_seq")
	if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	versionID, err := nextSequenceID(ctx, tx, "smpv", "tma_skill_marketplace_policy_version_id_seq")
	if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO skill_marketplace_policies (
			id, scope_type, organization_id, workspace_id, status, current_version, created_by, created_at
		) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), 'active', 1, $5, $6)
		ON CONFLICT DO NOTHING
	`, policyID, input.ScopeType, input.OrganizationID, input.WorkspaceID, input.CreatedBy, now)
	if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: active marketplace policy already exists for scope", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_marketplace_policy_versions (
			id, policy_id, version, config_json, checksum_sha256, created_by, created_at
		) VALUES ($1, $2, 1, $3, $4, $5, $6)
	`, versionID, policyID, encoded, revision, input.CreatedBy, now); err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	record := skillmarketplace.PolicyRecord{
		ID: policyID, ScopeType: input.ScopeType, OrganizationID: input.OrganizationID, WorkspaceID: input.WorkspaceID,
		Status: skillmarketplace.PolicyStatusActive, CurrentVersion: 1, CreatedBy: input.CreatedBy, CreatedAt: now,
	}
	version := skillmarketplace.PolicyVersion{
		ID: versionID, PolicyID: policyID, Version: 1, Config: normalized, Checksum: revision,
		CreatedBy: input.CreatedBy, CreatedAt: now,
	}
	return record, version, nil
}

func (s *PostgresStore) GetMarketplacePolicy(ctx context.Context, id string) (skillmarketplace.PolicyRecord, error) {
	if strings.TrimSpace(id) == "" {
		return skillmarketplace.PolicyRecord{}, fmt.Errorf("%w: marketplace policy id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillmarketplace.PolicyRecord{}, err
	}
	defer tx.Rollback()
	return scanMarketplacePolicy(tx.QueryRowContext(ctx, `
		SELECT id, scope_type, COALESCE(organization_id, ''), COALESCE(workspace_id, ''), status,
			current_version, created_by, created_at, archived_at
		FROM skill_marketplace_policies WHERE id = $1
	`, strings.TrimSpace(id)))
}

func (s *PostgresStore) ListMarketplacePolicies(ctx context.Context, input skillmarketplace.ListPoliciesInput) ([]skillmarketplace.PolicyRecord, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if input.OrganizationID != "" {
		if err := authorizeMarketplaceOrganizationScope(ctx, tx, input.OrganizationID); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, scope_type, COALESCE(organization_id, ''), COALESCE(workspace_id, ''), status,
			current_version, created_by, created_at, archived_at
		FROM skill_marketplace_policies
		WHERE ($1 = '' OR organization_id = $1)
			AND ($2 = '' OR workspace_id = $2)
			AND ($3 OR status = 'active')
		ORDER BY created_at DESC, id DESC
	`, input.OrganizationID, input.WorkspaceID, input.IncludeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []skillmarketplace.PolicyRecord{}
	for rows.Next() {
		item, err := scanMarketplacePolicy(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) PublishMarketplacePolicyVersion(ctx context.Context, policyID string, config skillmarketplace.Policy, createdBy string) (skillmarketplace.PolicyVersion, error) {
	policyID = strings.TrimSpace(policyID)
	createdBy = defaultString(strings.TrimSpace(createdBy), "system")
	if policyID == "" {
		return skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: marketplace policy id is required", ErrInvalid)
	}
	normalized, revision, encoded, err := normalizeMarketplacePolicy(config)
	if err != nil {
		return skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	defer tx.Rollback()
	var status string
	var currentVersion int
	if err := tx.QueryRowContext(ctx, `SELECT status, current_version FROM skill_marketplace_policies WHERE id = $1 FOR UPDATE`, policyID).Scan(&status, &currentVersion); err == sql.ErrNoRows {
		return skillmarketplace.PolicyVersion{}, ErrNotFound
	} else if err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	if status != skillmarketplace.PolicyStatusActive {
		return skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: archived marketplace policy cannot publish versions", ErrConflict)
	}
	versionNumber := currentVersion + 1
	versionID, err := nextSequenceID(ctx, tx, "smpv", "tma_skill_marketplace_policy_version_id_seq")
	if err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_marketplace_policy_versions (
			id, policy_id, version, config_json, checksum_sha256, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, versionID, policyID, versionNumber, encoded, revision, createdBy, now); err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_marketplace_policies SET current_version = $2 WHERE id = $1`, policyID, versionNumber); err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	return skillmarketplace.PolicyVersion{
		ID: versionID, PolicyID: policyID, Version: versionNumber, Config: normalized,
		Checksum: revision, CreatedBy: createdBy, CreatedAt: now,
	}, nil
}

func (s *PostgresStore) GetMarketplacePolicyVersion(ctx context.Context, policyID string, version int) (skillmarketplace.PolicyVersion, error) {
	if strings.TrimSpace(policyID) == "" || version <= 0 {
		return skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: marketplace policy id and positive version are required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	defer tx.Rollback()
	return scanMarketplacePolicyVersion(tx.QueryRowContext(ctx, `
		SELECT id, policy_id, version, config_json, checksum_sha256, created_by, created_at
		FROM skill_marketplace_policy_versions WHERE policy_id = $1 AND version = $2
	`, strings.TrimSpace(policyID), version))
}

func (s *PostgresStore) ArchiveMarketplacePolicy(ctx context.Context, id string) (skillmarketplace.PolicyRecord, error) {
	if strings.TrimSpace(id) == "" {
		return skillmarketplace.PolicyRecord{}, fmt.Errorf("%w: marketplace policy id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillmarketplace.PolicyRecord{}, err
	}
	defer tx.Rollback()
	record, err := scanMarketplacePolicy(tx.QueryRowContext(ctx, `
		UPDATE skill_marketplace_policies
		SET status = 'archived', archived_at = COALESCE(archived_at, $2)
		WHERE id = $1
		RETURNING id, scope_type, COALESCE(organization_id, ''), COALESCE(workspace_id, ''), status,
			current_version, created_by, created_at, archived_at
	`, strings.TrimSpace(id), time.Now().UTC()))
	if err != nil {
		return skillmarketplace.PolicyRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillmarketplace.PolicyRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) ResolveMarketplacePolicy(ctx context.Context, workspaceID string) (skillmarketplace.EffectivePolicy, error) {
	workspaceID = defaultString(strings.TrimSpace(workspaceID), DefaultWorkspaceID)
	tx, err := s.beginSkillScopeTx(ctx, workspaceID)
	if err != nil {
		return skillmarketplace.EffectivePolicy{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		SELECT p.id, p.scope_type, COALESCE(p.organization_id, ''), COALESCE(p.workspace_id, ''), p.status,
			p.current_version, p.created_by, p.created_at, p.archived_at,
			v.id, v.policy_id, v.version, v.config_json, v.checksum_sha256, v.created_by, v.created_at
		FROM workspaces w
		JOIN skill_marketplace_policies p
			ON p.status = 'active' AND (p.workspace_id = w.id OR p.organization_id = w.org_id)
		JOIN skill_marketplace_policy_versions v ON v.policy_id = p.id AND v.version = p.current_version
		WHERE w.id = $1
		ORDER BY CASE WHEN p.scope_type = 'workspace' THEN 0 ELSE 1 END
		LIMIT 1
	`, workspaceID)
	record, version, err := scanEffectiveMarketplacePolicy(row)
	if err != nil {
		return skillmarketplace.EffectivePolicy{}, err
	}
	return skillmarketplace.EffectivePolicy{
		Source: record.ScopeType, Policy: record, Version: version, Config: version.Config, Revision: version.Checksum,
	}, nil
}

func authorizeMarketplaceOrganizationScope(ctx context.Context, tx *sql.Tx, organizationID string) error {
	scope, scoped := DatabaseAccessScopeFromContext(ctx)
	if !scoped {
		return nil
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(tma_workspace_organization_id($1) = $2, false)
	`, scope.WorkspaceID, strings.TrimSpace(organizationID)).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: marketplace policy belongs to another organization", ErrForbidden)
	}
	return nil
}

func normalizeMarketplacePolicy(policy skillmarketplace.Policy) (skillmarketplace.Policy, string, []byte, error) {
	normalized, err := skillmarketplace.NormalizePolicy(policy)
	if err != nil {
		return skillmarketplace.Policy{}, "", nil, err
	}
	revision, err := skillmarketplace.PolicyRevision(normalized)
	if err != nil {
		return skillmarketplace.Policy{}, "", nil, err
	}
	encoded, err := json.Marshal(normalized)
	return normalized, revision, encoded, err
}

func ensureMarketplacePolicyScopeExists(ctx context.Context, tx *sql.Tx, input skillmarketplace.CreatePolicyInput) error {
	var exists bool
	query := `SELECT tma_organization_exists($1)`
	id := input.OrganizationID
	if input.ScopeType == skillmarketplace.PolicyScopeWorkspace {
		query = `SELECT tma_workspace_exists($1)`
		id = input.WorkspaceID
	}
	if err := tx.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func scanMarketplacePolicy(scanner rowScanner) (skillmarketplace.PolicyRecord, error) {
	var item skillmarketplace.PolicyRecord
	if err := scanner.Scan(
		&item.ID, &item.ScopeType, &item.OrganizationID, &item.WorkspaceID, &item.Status,
		&item.CurrentVersion, &item.CreatedBy, &item.CreatedAt, &item.ArchivedAt,
	); err == sql.ErrNoRows {
		return skillmarketplace.PolicyRecord{}, ErrNotFound
	} else if err != nil {
		return skillmarketplace.PolicyRecord{}, err
	}
	return item, nil
}

func scanMarketplacePolicyVersion(scanner rowScanner) (skillmarketplace.PolicyVersion, error) {
	var item skillmarketplace.PolicyVersion
	var config []byte
	if err := scanner.Scan(&item.ID, &item.PolicyID, &item.Version, &config, &item.Checksum, &item.CreatedBy, &item.CreatedAt); err == sql.ErrNoRows {
		return skillmarketplace.PolicyVersion{}, ErrNotFound
	} else if err != nil {
		return skillmarketplace.PolicyVersion{}, err
	}
	if err := json.Unmarshal(config, &item.Config); err != nil {
		return skillmarketplace.PolicyVersion{}, fmt.Errorf("decode marketplace policy: %w", err)
	}
	return item, nil
}

func scanEffectiveMarketplacePolicy(scanner rowScanner) (skillmarketplace.PolicyRecord, skillmarketplace.PolicyVersion, error) {
	var record skillmarketplace.PolicyRecord
	var version skillmarketplace.PolicyVersion
	var config []byte
	if err := scanner.Scan(
		&record.ID, &record.ScopeType, &record.OrganizationID, &record.WorkspaceID, &record.Status,
		&record.CurrentVersion, &record.CreatedBy, &record.CreatedAt, &record.ArchivedAt,
		&version.ID, &version.PolicyID, &version.Version, &config, &version.Checksum, &version.CreatedBy, &version.CreatedAt,
	); err == sql.ErrNoRows {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, ErrNotFound
	} else if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, err
	}
	if err := json.Unmarshal(config, &version.Config); err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("decode marketplace policy: %w", err)
	}
	return record, version, nil
}
