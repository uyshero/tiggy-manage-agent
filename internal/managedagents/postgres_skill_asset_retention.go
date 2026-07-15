package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/skillretention"
)

func (s *PostgresStore) SkillAssetGCWorkspaceContext(ctx context.Context, workspaceID string) (context.Context, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok && scope.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("%w: database workspace scope mismatch", ErrForbidden)
	}
	return ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
}

func (s *PostgresStore) CreateSkillAssetRetentionPolicy(ctx context.Context, input skillretention.CreatePolicyInput) (skillretention.PolicyRecord, skillretention.PolicyVersion, error) {
	input.ScopeType = strings.ToLower(strings.TrimSpace(input.ScopeType))
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.CreatedBy = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok && input.ScopeType == skillretention.ScopeWorkspace {
		input.WorkspaceID = scope.WorkspaceID
		input.OrganizationID = ""
	}
	if input.ScopeType != skillretention.ScopeOrganization && input.ScopeType != skillretention.ScopeWorkspace {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("%w: unsupported retention policy scope_type", ErrInvalid)
	}
	if input.ScopeType == skillretention.ScopeOrganization && (input.OrganizationID == "" || input.WorkspaceID != "") {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("%w: organization policy requires only organization_id", ErrInvalid)
	}
	if input.ScopeType == skillretention.ScopeWorkspace && (input.WorkspaceID == "" || input.OrganizationID != "") {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("%w: workspace policy requires only workspace_id", ErrInvalid)
	}
	normalized, revision, encoded, err := normalizeSkillAssetRetentionPolicy(input.Config)
	if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	requestedWorkspaceID := ""
	if input.ScopeType == skillretention.ScopeWorkspace {
		requestedWorkspaceID = input.WorkspaceID
	}
	tx, err := s.beginSkillScopeTx(ctx, requestedWorkspaceID)
	if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	defer tx.Rollback()
	if input.ScopeType == skillretention.ScopeOrganization {
		if err := authorizeSkillAssetRetentionOrganizationScope(ctx, tx, input.OrganizationID); err != nil {
			return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
		}
	}
	if err := ensureSkillAssetRetentionPolicyScopeExists(ctx, tx, input); err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	policyID, err := nextSequenceID(ctx, tx, "sarp", "tma_skill_asset_retention_policy_id_seq")
	if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	versionID, err := nextSequenceID(ctx, tx, "sarpv", "tma_skill_asset_retention_policy_version_id_seq")
	if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO skill_asset_retention_policies (
			id, scope_type, organization_id, workspace_id, status, current_version, created_by, created_at
		) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), 'active', 1, $5, $6)
		ON CONFLICT DO NOTHING
	`, policyID, input.ScopeType, input.OrganizationID, input.WorkspaceID, input.CreatedBy, now)
	if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("%w: active retention policy already exists for scope", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_asset_retention_policy_versions (
			id, policy_id, version, config_json, checksum_sha256, created_by, created_at
		) VALUES ($1, $2, 1, $3, $4, $5, $6)
	`, versionID, policyID, encoded, revision, input.CreatedBy, now); err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	record := skillretention.PolicyRecord{
		ID: policyID, ScopeType: input.ScopeType, OrganizationID: input.OrganizationID, WorkspaceID: input.WorkspaceID,
		Status: skillretention.PolicyStatusActive, CurrentVersion: 1, CreatedBy: input.CreatedBy, CreatedAt: now,
	}
	version := skillretention.PolicyVersion{
		ID: versionID, PolicyID: policyID, Version: 1, Config: normalized, Checksum: revision,
		CreatedBy: input.CreatedBy, CreatedAt: now,
	}
	return record, version, nil
}

func (s *PostgresStore) GetSkillAssetRetentionPolicy(ctx context.Context, id string) (skillretention.PolicyRecord, error) {
	if strings.TrimSpace(id) == "" {
		return skillretention.PolicyRecord{}, fmt.Errorf("%w: retention policy id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.PolicyRecord{}, err
	}
	defer tx.Rollback()
	return scanSkillAssetRetentionPolicy(tx.QueryRowContext(ctx, `
		SELECT id, scope_type, COALESCE(organization_id, ''), COALESCE(workspace_id, ''), status,
			current_version, created_by, created_at, archived_at
		FROM skill_asset_retention_policies WHERE id = $1
	`, strings.TrimSpace(id)))
}

func (s *PostgresStore) ListSkillAssetRetentionPolicies(ctx context.Context, input skillretention.ListPoliciesInput) ([]skillretention.PolicyRecord, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if input.OrganizationID != "" {
		if err := authorizeSkillAssetRetentionOrganizationScope(ctx, tx, input.OrganizationID); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, scope_type, COALESCE(organization_id, ''), COALESCE(workspace_id, ''), status,
			current_version, created_by, created_at, archived_at
		FROM skill_asset_retention_policies
		WHERE ($1 = '' OR organization_id = $1)
			AND ($2 = '' OR workspace_id = $2)
			AND ($3 OR status = 'active')
		ORDER BY created_at DESC, id DESC
	`, input.OrganizationID, input.WorkspaceID, input.IncludeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []skillretention.PolicyRecord{}
	for rows.Next() {
		item, err := scanSkillAssetRetentionPolicy(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) PublishSkillAssetRetentionPolicyVersion(ctx context.Context, policyID string, config skillretention.Policy, createdBy string) (skillretention.PolicyVersion, error) {
	policyID = strings.TrimSpace(policyID)
	createdBy = defaultString(strings.TrimSpace(createdBy), "system")
	if policyID == "" {
		return skillretention.PolicyVersion{}, fmt.Errorf("%w: retention policy id is required", ErrInvalid)
	}
	normalized, revision, encoded, err := normalizeSkillAssetRetentionPolicy(config)
	if err != nil {
		return skillretention.PolicyVersion{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.PolicyVersion{}, err
	}
	defer tx.Rollback()
	var status string
	var currentVersion int
	if err := tx.QueryRowContext(ctx, `SELECT status, current_version FROM skill_asset_retention_policies WHERE id = $1 FOR UPDATE`, policyID).Scan(&status, &currentVersion); err == sql.ErrNoRows {
		return skillretention.PolicyVersion{}, ErrNotFound
	} else if err != nil {
		return skillretention.PolicyVersion{}, err
	}
	if status != skillretention.PolicyStatusActive {
		return skillretention.PolicyVersion{}, fmt.Errorf("%w: archived retention policy cannot publish versions", ErrConflict)
	}
	versionNumber := currentVersion + 1
	versionID, err := nextSequenceID(ctx, tx, "sarpv", "tma_skill_asset_retention_policy_version_id_seq")
	if err != nil {
		return skillretention.PolicyVersion{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_asset_retention_policy_versions (
			id, policy_id, version, config_json, checksum_sha256, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, versionID, policyID, versionNumber, encoded, revision, createdBy, now); err != nil {
		return skillretention.PolicyVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_asset_retention_policies SET current_version = $2 WHERE id = $1`, policyID, versionNumber); err != nil {
		return skillretention.PolicyVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillretention.PolicyVersion{}, err
	}
	return skillretention.PolicyVersion{
		ID: versionID, PolicyID: policyID, Version: versionNumber, Config: normalized,
		Checksum: revision, CreatedBy: createdBy, CreatedAt: now,
	}, nil
}

func (s *PostgresStore) GetSkillAssetRetentionPolicyVersion(ctx context.Context, policyID string, version int) (skillretention.PolicyVersion, error) {
	if strings.TrimSpace(policyID) == "" || version <= 0 {
		return skillretention.PolicyVersion{}, fmt.Errorf("%w: retention policy id and positive version are required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.PolicyVersion{}, err
	}
	defer tx.Rollback()
	return scanSkillAssetRetentionPolicyVersion(tx.QueryRowContext(ctx, `
		SELECT id, policy_id, version, config_json, checksum_sha256, created_by, created_at
		FROM skill_asset_retention_policy_versions WHERE policy_id = $1 AND version = $2
	`, strings.TrimSpace(policyID), version))
}

func (s *PostgresStore) ArchiveSkillAssetRetentionPolicy(ctx context.Context, id string) (skillretention.PolicyRecord, error) {
	if strings.TrimSpace(id) == "" {
		return skillretention.PolicyRecord{}, fmt.Errorf("%w: retention policy id is required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.PolicyRecord{}, err
	}
	defer tx.Rollback()
	record, err := scanSkillAssetRetentionPolicy(tx.QueryRowContext(ctx, `
		UPDATE skill_asset_retention_policies
		SET status = 'archived', archived_at = COALESCE(archived_at, $2)
		WHERE id = $1
		RETURNING id, scope_type, COALESCE(organization_id, ''), COALESCE(workspace_id, ''), status,
			current_version, created_by, created_at, archived_at
	`, strings.TrimSpace(id), time.Now().UTC()))
	if err != nil {
		return skillretention.PolicyRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillretention.PolicyRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) ResolveSkillAssetRetentionPolicy(ctx context.Context, workspaceID string) (skillretention.EffectivePolicy, bool, error) {
	workspaceID = defaultString(strings.TrimSpace(workspaceID), DefaultWorkspaceID)
	tx, err := s.beginSkillScopeTx(ctx, workspaceID)
	if err != nil {
		return skillretention.EffectivePolicy{}, false, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_workspace_exists($1)`, workspaceID).Scan(&exists); err != nil {
		return skillretention.EffectivePolicy{}, false, err
	}
	if !exists {
		return skillretention.EffectivePolicy{}, false, ErrNotFound
	}
	record, version, err := scanEffectiveSkillAssetRetentionPolicy(tx.QueryRowContext(ctx, `
		SELECT p.id, p.scope_type, COALESCE(p.organization_id, ''), COALESCE(p.workspace_id, ''), p.status,
			p.current_version, p.created_by, p.created_at, p.archived_at,
			v.id, v.policy_id, v.version, v.config_json, v.checksum_sha256, v.created_by, v.created_at
		FROM workspaces w
		JOIN skill_asset_retention_policies p
			ON p.status = 'active' AND (p.workspace_id = w.id OR p.organization_id = w.org_id)
		JOIN skill_asset_retention_policy_versions v ON v.policy_id = p.id AND v.version = p.current_version
		WHERE w.id = $1
		ORDER BY CASE WHEN p.scope_type = 'workspace' THEN 0 ELSE 1 END
		LIMIT 1
	`, workspaceID))
	if err == ErrNotFound {
		return skillretention.EffectivePolicy{}, false, nil
	}
	if err != nil {
		return skillretention.EffectivePolicy{}, false, err
	}
	return skillretention.EffectivePolicy{
		Source: record.ScopeType, Policy: record, Version: version, Config: version.Config, Revision: version.Checksum,
	}, true, nil
}

const skillAssetCandidateQuery = `
	WITH asset_refs AS (
		SELECT s.id AS skill_id, s.identifier AS skill_identifier, s.status AS skill_status,
			s.archived_at, sv.id AS skill_version_id, sv.version AS skill_version,
			COALESCE(asset->>'path', '') AS asset_path,
			COALESCE(asset->>'object_ref_id', '') AS object_ref_id
		FROM skills s
		JOIN skill_versions sv ON sv.skill_id = s.id
		CROSS JOIN LATERAL jsonb_array_elements(
			CASE
				WHEN jsonb_typeof(sv.assets_json) = 'array' THEN sv.assets_json
				WHEN jsonb_typeof(sv.assets_json->'files') = 'array' THEN sv.assets_json->'files'
				ELSE '[]'::jsonb
			END
		) asset
		WHERE s.workspace_id = $1 AND COALESCE(asset->>'object_ref_id', '') <> ''
		UNION ALL
		SELECT s.id AS skill_id, s.identifier AS skill_identifier, s.status AS skill_status,
			s.archived_at, sv.id AS skill_version_id, sv.version AS skill_version,
			spf.path AS asset_path, spf.object_ref_id
		FROM skills s
		JOIN skill_versions sv ON sv.skill_id = s.id
		JOIN skill_version_package_files spf ON spf.skill_version_id = sv.id
		WHERE s.workspace_id = $1
	), candidates AS (
		SELECT o.*,
			COALESCE(picked.skill_id, '') AS skill_id,
			COALESCE(picked.skill_identifier, '') AS skill_identifier,
			COALESCE(picked.skill_version_id, '') AS skill_version_id,
			COALESCE(picked.skill_version, 0) AS skill_version,
			COALESCE(picked.asset_path, '') AS asset_path,
			CASE WHEN picked.object_ref_id IS NULL THEN 'orphaned_skill_asset' ELSE 'archived_retention_expired' END AS gc_reason,
			CASE WHEN picked.object_ref_id IS NULL THEN o.created_at ELSE latest_ref.latest_archived_at END AS eligible_at
		FROM object_refs o
		LEFT JOIN LATERAL (
			SELECT ar.* FROM asset_refs ar WHERE ar.object_ref_id = o.id
			ORDER BY ar.skill_id, ar.skill_version DESC, ar.asset_path LIMIT 1
		) picked ON TRUE
		LEFT JOIN LATERAL (
			SELECT MAX(ar.archived_at) AS latest_archived_at FROM asset_refs ar WHERE ar.object_ref_id = o.id
		) latest_ref ON TRUE
		WHERE o.workspace_id = $1
			AND ($4 = '' OR o.id = $4)
			AND NOT EXISTS (SELECT 1 FROM session_artifacts sa WHERE sa.object_ref_id = o.id)
			AND (
				(picked.object_ref_id IS NOT NULL AND NOT EXISTS (
					SELECT 1 FROM asset_refs protected
					WHERE protected.object_ref_id = o.id
						AND (protected.skill_status <> 'archived' OR protected.archived_at IS NULL OR protected.archived_at > $2)
				))
				OR
				(picked.object_ref_id IS NULL AND o.metadata_json->>'kind' = 'skill_asset' AND o.created_at <= $2)
			)
	)
	SELECT workspace_id, skill_id, skill_identifier, skill_version_id, skill_version, asset_path,
		id, storage_provider, bucket, object_key, object_version, content_type, size_bytes,
		checksum_sha256, metadata_json, COALESCE(metadata_json->>'scan_provider', ''),
		COALESCE(metadata_json->>'scan_version', ''), gc_reason, eligible_at, created_at
	FROM candidates
	ORDER BY eligible_at, id
	LIMIT $3
`

func (s *PostgresStore) ListSkillAssetGCCandidates(ctx context.Context, input skillretention.ListCandidatesInput) ([]skillretention.Candidate, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	if input.Cutoff.IsZero() {
		return nil, fmt.Errorf("%w: GC cutoff is required", ErrInvalid)
	}
	if input.Limit <= 0 || input.Limit > skillretention.MaxDeleteLimit {
		input.Limit = skillretention.DefaultDeleteLimit
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, skillAssetCandidateQuery, input.WorkspaceID, input.Cutoff.UTC(), input.Limit, strings.TrimSpace(input.ObjectRefID))
	if err != nil {
		return nil, err
	}
	items := []skillretention.Candidate{}
	for rows.Next() {
		candidate, err := scanSkillAssetCandidate(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, candidate)
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
	return items, nil
}

func (s *PostgresStore) AcquireSkillAssetGCLock(ctx context.Context, workspaceID string) (func() error, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok && scope.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("%w: database workspace scope mismatch", ErrForbidden)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+workspaceID).Scan(&acquired); err != nil {
		conn.Close()
		return nil, err
	}
	if !acquired {
		conn.Close()
		return nil, skillretention.ErrConflict
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			var unlocked bool
			if err := conn.QueryRowContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+workspaceID).Scan(&unlocked); err != nil {
				releaseErr = err
			} else if !unlocked {
				releaseErr = fmt.Errorf("skill asset GC advisory lock was not held")
			}
			if err := conn.Close(); releaseErr == nil && err != nil {
				releaseErr = err
			}
		})
		return releaseErr
	}, nil
}

func (s *PostgresStore) StartSkillAssetGCRun(ctx context.Context, input skillretention.StartRunInput) (skillretention.Run, []skillretention.Item, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.RequestedBy = defaultString(strings.TrimSpace(input.RequestedBy), "system")
	if input.WorkspaceID == "" || input.StartedAt.IsZero() {
		return skillretention.Run{}, nil, fmt.Errorf("%w: workspace_id and started_at are required", ErrInvalid)
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return skillretention.Run{}, nil, err
	}
	defer tx.Rollback()
	// A process crash releases the advisory lock but can leave a durable running row.
	if _, err := tx.ExecContext(ctx, `
		UPDATE skill_asset_gc_runs SET status = 'failed', finished_at = $2
		WHERE workspace_id = $1 AND status = 'running'
	`, input.WorkspaceID, input.StartedAt.UTC()); err != nil {
		return skillretention.Run{}, nil, err
	}
	runID, err := nextSequenceID(ctx, tx, "sagcr", "tma_skill_asset_gc_run_id_seq")
	if err != nil {
		return skillretention.Run{}, nil, err
	}
	status := skillretention.RunStatusRunning
	var finishedAt *time.Time
	if len(input.Candidates) == 0 {
		status = skillretention.RunStatusSucceeded
		finished := input.StartedAt.UTC()
		finishedAt = &finished
	}
	policyID := input.Effective.Policy.ID
	policyVersion := input.Effective.Version.Version
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO skill_asset_gc_runs (
			id, workspace_id, policy_source, policy_id, policy_version, policy_revision,
			retention_days, delete_limit, status, candidate_count, requested_by, started_at, finished_at
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, runID, input.WorkspaceID, input.Effective.Source, policyID, policyVersion, input.Effective.Revision,
		input.Effective.Config.RetentionDays, input.Effective.Config.DeleteLimit, status, len(input.Candidates),
		input.RequestedBy, input.StartedAt.UTC(), finishedAt); err != nil {
		return skillretention.Run{}, nil, err
	}
	items := make([]skillretention.Item, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidate.WorkspaceID = input.WorkspaceID
		itemID, err := nextSequenceID(ctx, tx, "sagci", "tma_skill_asset_gc_item_id_seq")
		if err != nil {
			return skillretention.Run{}, nil, err
		}
		if len(candidate.Metadata) == 0 {
			candidate.Metadata = json.RawMessage(`{}`)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skill_asset_gc_items (
				id, run_id, workspace_id, skill_id, skill_identifier, skill_version_id, skill_version,
				asset_path, object_ref_id, storage_provider, bucket, object_key, object_version,
				content_type, size_bytes, checksum_sha256, metadata_json, scan_provider, scan_version,
				status, reason, eligible_at, object_created_at, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
				$14, $15, $16, $17, $18, $19, 'candidate', $20, $21, $22, $23, $23)
		`, itemID, runID, candidate.WorkspaceID, candidate.SkillID, candidate.SkillIdentifier,
			candidate.SkillVersionID, candidate.SkillVersion, candidate.AssetPath, candidate.ObjectRefID,
			candidate.StorageProvider, candidate.Bucket, candidate.ObjectKey, candidate.ObjectVersion,
			candidate.ContentType, candidate.SizeBytes, candidate.ChecksumSHA256, candidate.Metadata,
			candidate.ScanProvider, candidate.ScanVersion, candidate.Reason, candidate.EligibleAt,
			candidate.ObjectCreatedAt, input.StartedAt.UTC()); err != nil {
			return skillretention.Run{}, nil, err
		}
		items = append(items, skillretention.Item{
			ID: itemID, RunID: runID, Candidate: candidate, Status: skillretention.ItemStatusCandidate,
			Reason: candidate.Reason, CreatedAt: input.StartedAt.UTC(), UpdatedAt: input.StartedAt.UTC(),
		})
	}
	if err := tx.Commit(); err != nil {
		return skillretention.Run{}, nil, err
	}
	run := skillretention.Run{
		ID: runID, WorkspaceID: input.WorkspaceID, PolicySource: input.Effective.Source,
		PolicyID: policyID, PolicyVersion: policyVersion, PolicyRevision: input.Effective.Revision,
		RetentionDays: input.Effective.Config.RetentionDays, DeleteLimit: input.Effective.Config.DeleteLimit,
		Status: status, CandidateCount: len(input.Candidates), RequestedBy: input.RequestedBy,
		StartedAt: input.StartedAt.UTC(), FinishedAt: finishedAt,
	}
	return run, items, nil
}

func (s *PostgresStore) ClaimSkillAssetGCItem(ctx context.Context, itemID string, cutoff time.Time) (skillretention.Candidate, bool, string, error) {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	defer tx.Rollback()
	var workspaceID, objectRefID, status string
	if err := tx.QueryRowContext(ctx, `
		SELECT workspace_id, object_ref_id, status FROM skill_asset_gc_items WHERE id = $1 FOR UPDATE
	`, strings.TrimSpace(itemID)).Scan(&workspaceID, &objectRefID, &status); err == sql.ErrNoRows {
		return skillretention.Candidate{}, false, "", ErrNotFound
	} else if err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	if status != skillretention.ItemStatusCandidate {
		return skillretention.Candidate{}, false, "item_is_not_candidate", nil
	}
	rows, err := tx.QueryContext(ctx, skillAssetCandidateQuery, workspaceID, cutoff.UTC(), 1, objectRefID)
	if err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return skillretention.Candidate{}, false, "", err
		}
		if err := rows.Close(); err != nil {
			return skillretention.Candidate{}, false, "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skill_asset_gc_items SET status = 'skipped', reason = 'reference_changed_or_protected', updated_at = now() WHERE id = $1`, itemID); err != nil {
			return skillretention.Candidate{}, false, "", err
		}
		if err := tx.Commit(); err != nil {
			return skillretention.Candidate{}, false, "", err
		}
		return skillretention.Candidate{}, false, "reference_changed_or_protected", nil
	}
	candidate, err := scanSkillAssetCandidate(rows)
	if err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	if err := rows.Close(); err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE skill_asset_gc_items SET status = 'deleting', attempts = attempts + 1,
			error_message = '', updated_at = now() WHERE id = $1
	`, itemID); err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	if err := tx.Commit(); err != nil {
		return skillretention.Candidate{}, false, "", err
	}
	return candidate, true, candidate.Reason, nil
}

func (s *PostgresStore) SkipSkillAssetGCItem(ctx context.Context, itemID string, reason string) error {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE skill_asset_gc_items SET status = 'skipped', reason = $2, updated_at = now()
		WHERE id = $1 AND status IN ('candidate', 'deleting')
	`, strings.TrimSpace(itemID), defaultString(strings.TrimSpace(reason), "not_eligible"))
	if err := requireAffected(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) FailSkillAssetGCItem(ctx context.Context, itemID string, message string) error {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE skill_asset_gc_items SET status = 'failed', error_message = $2, updated_at = now()
		WHERE id = $1 AND status IN ('candidate', 'deleting')
	`, strings.TrimSpace(itemID), truncateSkillAssetGCError(message))
	if err := requireAffected(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) FinalizeSkillAssetGCItem(ctx context.Context, itemID string, deletedBy string, objectWasMissing bool) (skillretention.Tombstone, error) {
	itemID = strings.TrimSpace(itemID)
	deletedBy = defaultString(strings.TrimSpace(deletedBy), "system")
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.Tombstone{}, err
	}
	defer tx.Rollback()
	item, err := scanSkillAssetGCItem(tx.QueryRowContext(ctx, skillAssetGCItemSelect+` WHERE i.id = $1 FOR UPDATE`, itemID))
	if err != nil {
		return skillretention.Tombstone{}, err
	}
	if item.Status != skillretention.ItemStatusDeleting {
		return skillretention.Tombstone{}, fmt.Errorf("%w: GC item is not deleting", ErrConflict)
	}
	// Package file references use real foreign keys. Once an archived package is
	// eligible, unlink its index rows before removing the shared object metadata.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM skill_version_package_files spf
		USING skill_versions sv, skills s
		WHERE spf.object_ref_id = $1 AND spf.skill_version_id = sv.id
			AND sv.skill_id = s.id AND s.status = 'archived'
	`, item.Candidate.ObjectRefID); err != nil {
		return skillretention.Tombstone{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE skill_versions sv
		SET package_object_ref_id = CASE WHEN package_object_ref_id = $1 THEN NULL ELSE package_object_ref_id END,
			skill_md_object_ref_id = CASE WHEN skill_md_object_ref_id = $1 THEN NULL ELSE skill_md_object_ref_id END
		FROM skills s
		WHERE sv.skill_id = s.id AND s.status = 'archived'
			AND (sv.package_object_ref_id = $1 OR sv.skill_md_object_ref_id = $1)
	`, item.Candidate.ObjectRefID); err != nil {
		return skillretention.Tombstone{}, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM object_refs o
		WHERE o.id = $1
			AND NOT EXISTS (SELECT 1 FROM session_artifacts sa WHERE sa.object_ref_id = o.id)
			AND NOT EXISTS (
				SELECT 1 FROM skills s
				JOIN skill_versions sv ON sv.skill_id = s.id
				CROSS JOIN LATERAL jsonb_array_elements(
					CASE
						WHEN jsonb_typeof(sv.assets_json) = 'array' THEN sv.assets_json
						WHEN jsonb_typeof(sv.assets_json->'files') = 'array' THEN sv.assets_json->'files'
						ELSE '[]'::jsonb
					END
				) asset
				WHERE COALESCE(asset->>'object_ref_id', '') = o.id AND s.status <> 'archived'
			)
			AND NOT EXISTS (
				SELECT 1 FROM skill_version_package_files spf
				JOIN skill_versions sv ON sv.id = spf.skill_version_id
				JOIN skills s ON s.id = sv.skill_id
				WHERE spf.object_ref_id = o.id AND s.status <> 'archived'
			)
	`, item.Candidate.ObjectRefID)
	if err != nil {
		return skillretention.Tombstone{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var stillExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM object_refs WHERE id = $1)`, item.Candidate.ObjectRefID).Scan(&stillExists); err != nil {
			return skillretention.Tombstone{}, err
		}
		if stillExists {
			return skillretention.Tombstone{}, fmt.Errorf("%w: object reference became protected during GC", ErrConflict)
		}
	}
	tombstoneID, err := nextSequenceID(ctx, tx, "sagct", "tma_skill_asset_gc_tombstone_id_seq")
	if err != nil {
		return skillretention.Tombstone{}, err
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO skill_asset_gc_tombstones (
			id, run_id, workspace_id, skill_id, skill_version_id, asset_path, object_ref_id,
			storage_provider, bucket, object_key, object_version, content_type, size_bytes,
			checksum_sha256, metadata_json, scan_provider, scan_version, reason,
			object_was_missing, deleted_by, deleted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $20, $21)
	`, tombstoneID, item.RunID, item.Candidate.WorkspaceID, item.Candidate.SkillID,
		item.Candidate.SkillVersionID, item.Candidate.AssetPath, item.Candidate.ObjectRefID,
		item.Candidate.StorageProvider, item.Candidate.Bucket, item.Candidate.ObjectKey,
		item.Candidate.ObjectVersion, item.Candidate.ContentType, item.Candidate.SizeBytes,
		item.Candidate.ChecksumSHA256, item.Candidate.Metadata, item.Candidate.ScanProvider,
		item.Candidate.ScanVersion, item.Reason, objectWasMissing, deletedBy, now)
	if err != nil {
		return skillretention.Tombstone{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE skill_asset_gc_items SET status = 'deleted', object_was_missing = $2,
			deleted_at = $3, updated_at = $3 WHERE id = $1
	`, itemID, objectWasMissing, now); err != nil {
		return skillretention.Tombstone{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillretention.Tombstone{}, err
	}
	return skillretention.Tombstone{
		ID: tombstoneID, RunID: item.RunID, WorkspaceID: item.Candidate.WorkspaceID,
		SkillID: item.Candidate.SkillID, SkillVersionID: item.Candidate.SkillVersionID,
		AssetPath: item.Candidate.AssetPath, ObjectRefID: item.Candidate.ObjectRefID,
		StorageProvider: item.Candidate.StorageProvider, Bucket: item.Candidate.Bucket,
		ObjectKey: item.Candidate.ObjectKey, ObjectVersion: item.Candidate.ObjectVersion,
		ContentType: item.Candidate.ContentType, SizeBytes: item.Candidate.SizeBytes,
		ChecksumSHA256: item.Candidate.ChecksumSHA256, Metadata: item.Candidate.Metadata,
		ScanProvider: item.Candidate.ScanProvider, ScanVersion: item.Candidate.ScanVersion,
		Reason: item.Reason, ObjectWasMissing: objectWasMissing, DeletedBy: deletedBy, DeletedAt: now,
	}, nil
}

func (s *PostgresStore) FinishSkillAssetGCRun(ctx context.Context, runID string) (skillretention.Run, error) {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.Run{}, err
	}
	defer tx.Rollback()
	run, err := scanSkillAssetGCRun(tx.QueryRowContext(ctx, `
		WITH counts AS (
			SELECT COUNT(*) FILTER (WHERE status = 'deleted')::INTEGER AS deleted_count,
				COUNT(*) FILTER (WHERE status = 'skipped')::INTEGER AS skipped_count,
				COUNT(*) FILTER (WHERE status = 'failed')::INTEGER AS failed_count,
				COALESCE(SUM(size_bytes) FILTER (WHERE status = 'deleted'), 0)::BIGINT AS bytes_deleted
			FROM skill_asset_gc_items WHERE run_id = $1
		), updated AS (
			UPDATE skill_asset_gc_runs r SET
				deleted_count = COALESCE(c.deleted_count, 0), skipped_count = COALESCE(c.skipped_count, 0),
				failed_count = COALESCE(c.failed_count, 0), bytes_deleted = COALESCE(c.bytes_deleted, 0),
				status = CASE
					WHEN COALESCE(c.failed_count, 0) = 0 THEN 'succeeded'
					WHEN COALESCE(c.deleted_count, 0) = 0 THEN 'failed'
					ELSE 'partial'
				END,
				finished_at = now()
			FROM counts c WHERE r.id = $1
			RETURNING r.*
		)
		SELECT id, workspace_id, policy_source, COALESCE(policy_id, ''), policy_version,
			policy_revision, retention_days, delete_limit, status, candidate_count, deleted_count,
			skipped_count, failed_count, bytes_deleted, requested_by, started_at, finished_at
		FROM updated
	`, strings.TrimSpace(runID)))
	if err != nil {
		return skillretention.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return skillretention.Run{}, err
	}
	return run, nil
}

func (s *PostgresStore) ListSkillAssetGCRuns(ctx context.Context, input skillretention.ListRunsInput) ([]skillretention.Run, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	if input.Limit <= 0 || input.Limit > 200 {
		input.Limit = 20
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, skillAssetGCRunSelect+` WHERE workspace_id = $1 ORDER BY started_at DESC, id DESC LIMIT $2`, input.WorkspaceID, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []skillretention.Run{}
	for rows.Next() {
		run, err := scanSkillAssetGCRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *PostgresStore) GetSkillAssetGCRun(ctx context.Context, runID string) (skillretention.Run, []skillretention.Item, error) {
	tx, err := s.beginSkillScopeTx(ctx, "")
	if err != nil {
		return skillretention.Run{}, nil, err
	}
	defer tx.Rollback()
	run, err := scanSkillAssetGCRun(tx.QueryRowContext(ctx, skillAssetGCRunSelect+` WHERE id = $1`, strings.TrimSpace(runID)))
	if err != nil {
		return skillretention.Run{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, skillAssetGCItemSelect+` WHERE i.run_id = $1 ORDER BY i.created_at, i.id`, run.ID)
	if err != nil {
		return skillretention.Run{}, nil, err
	}
	defer rows.Close()
	items := []skillretention.Item{}
	for rows.Next() {
		item, err := scanSkillAssetGCItem(rows)
		if err != nil {
			return skillretention.Run{}, nil, err
		}
		items = append(items, item)
	}
	return run, items, rows.Err()
}

func (s *PostgresStore) ListSkillAssetGCTombstones(ctx context.Context, input skillretention.ListTombstonesInput) ([]skillretention.Tombstone, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	if input.Limit <= 0 || input.Limit > 200 {
		input.Limit = 20
	}
	tx, err := s.beginSkillScopeTx(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, run_id, workspace_id, skill_id, skill_version_id, asset_path, object_ref_id,
			storage_provider, bucket, object_key, object_version, content_type, size_bytes,
			checksum_sha256, metadata_json, scan_provider, scan_version, reason,
			object_was_missing, deleted_by, deleted_at
		FROM skill_asset_gc_tombstones WHERE workspace_id = $1
		ORDER BY deleted_at DESC, id DESC LIMIT $2
	`, input.WorkspaceID, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []skillretention.Tombstone{}
	for rows.Next() {
		item, err := scanSkillAssetGCTombstone(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListSkillAssetGCWorkspaceIDs(ctx context.Context) ([]string, error) {
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

const skillAssetGCRunSelect = `
	SELECT id, workspace_id, policy_source, COALESCE(policy_id, ''), policy_version,
		policy_revision, retention_days, delete_limit, status, candidate_count, deleted_count,
		skipped_count, failed_count, bytes_deleted, requested_by, started_at, finished_at
	FROM skill_asset_gc_runs
`

const skillAssetGCItemSelect = `
	SELECT i.id, i.run_id, i.workspace_id, i.skill_id, i.skill_identifier, i.skill_version_id,
		i.skill_version, i.asset_path, i.object_ref_id, i.storage_provider, i.bucket, i.object_key,
		i.object_version, i.content_type, i.size_bytes, i.checksum_sha256, i.metadata_json,
		i.scan_provider, i.scan_version, i.reason, i.eligible_at, i.object_created_at,
		i.status, i.attempts, i.object_was_missing, i.error_message, i.created_at, i.updated_at, i.deleted_at
	FROM skill_asset_gc_items i
`

func normalizeSkillAssetRetentionPolicy(policy skillretention.Policy) (skillretention.Policy, string, []byte, error) {
	normalized, err := skillretention.NormalizePolicy(policy)
	if err != nil {
		return skillretention.Policy{}, "", nil, err
	}
	revision, err := skillretention.PolicyRevision(normalized)
	if err != nil {
		return skillretention.Policy{}, "", nil, err
	}
	encoded, err := json.Marshal(normalized)
	return normalized, revision, encoded, err
}

func ensureSkillAssetRetentionPolicyScopeExists(ctx context.Context, tx *sql.Tx, input skillretention.CreatePolicyInput) error {
	query := `SELECT tma_organization_exists($1)`
	id := input.OrganizationID
	if input.ScopeType == skillretention.ScopeWorkspace {
		query = `SELECT tma_workspace_exists($1)`
		id = input.WorkspaceID
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func authorizeSkillAssetRetentionOrganizationScope(ctx context.Context, tx *sql.Tx, organizationID string) error {
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
		return fmt.Errorf("%w: retention policy belongs to another organization", ErrForbidden)
	}
	return nil
}

func scanSkillAssetRetentionPolicy(scanner rowScanner) (skillretention.PolicyRecord, error) {
	var item skillretention.PolicyRecord
	if err := scanner.Scan(
		&item.ID, &item.ScopeType, &item.OrganizationID, &item.WorkspaceID, &item.Status,
		&item.CurrentVersion, &item.CreatedBy, &item.CreatedAt, &item.ArchivedAt,
	); err == sql.ErrNoRows {
		return skillretention.PolicyRecord{}, ErrNotFound
	} else if err != nil {
		return skillretention.PolicyRecord{}, err
	}
	return item, nil
}

func scanSkillAssetRetentionPolicyVersion(scanner rowScanner) (skillretention.PolicyVersion, error) {
	var item skillretention.PolicyVersion
	var config []byte
	if err := scanner.Scan(&item.ID, &item.PolicyID, &item.Version, &config, &item.Checksum, &item.CreatedBy, &item.CreatedAt); err == sql.ErrNoRows {
		return skillretention.PolicyVersion{}, ErrNotFound
	} else if err != nil {
		return skillretention.PolicyVersion{}, err
	}
	if err := json.Unmarshal(config, &item.Config); err != nil {
		return skillretention.PolicyVersion{}, fmt.Errorf("decode skill asset retention policy: %w", err)
	}
	return item, nil
}

func scanEffectiveSkillAssetRetentionPolicy(scanner rowScanner) (skillretention.PolicyRecord, skillretention.PolicyVersion, error) {
	var record skillretention.PolicyRecord
	var version skillretention.PolicyVersion
	var config []byte
	if err := scanner.Scan(
		&record.ID, &record.ScopeType, &record.OrganizationID, &record.WorkspaceID, &record.Status,
		&record.CurrentVersion, &record.CreatedBy, &record.CreatedAt, &record.ArchivedAt,
		&version.ID, &version.PolicyID, &version.Version, &config, &version.Checksum, &version.CreatedBy, &version.CreatedAt,
	); err == sql.ErrNoRows {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, ErrNotFound
	} else if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, err
	}
	if err := json.Unmarshal(config, &version.Config); err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("decode skill asset retention policy: %w", err)
	}
	return record, version, nil
}

func scanSkillAssetCandidate(scanner rowScanner) (skillretention.Candidate, error) {
	var item skillretention.Candidate
	if err := scanner.Scan(
		&item.WorkspaceID, &item.SkillID, &item.SkillIdentifier, &item.SkillVersionID,
		&item.SkillVersion, &item.AssetPath, &item.ObjectRefID, &item.StorageProvider,
		&item.Bucket, &item.ObjectKey, &item.ObjectVersion, &item.ContentType, &item.SizeBytes,
		&item.ChecksumSHA256, &item.Metadata, &item.ScanProvider, &item.ScanVersion,
		&item.Reason, &item.EligibleAt, &item.ObjectCreatedAt,
	); err != nil {
		return skillretention.Candidate{}, err
	}
	return item, nil
}

func scanSkillAssetGCRun(scanner rowScanner) (skillretention.Run, error) {
	var item skillretention.Run
	if err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.PolicySource, &item.PolicyID, &item.PolicyVersion,
		&item.PolicyRevision, &item.RetentionDays, &item.DeleteLimit, &item.Status,
		&item.CandidateCount, &item.DeletedCount, &item.SkippedCount, &item.FailedCount,
		&item.BytesDeleted, &item.RequestedBy, &item.StartedAt, &item.FinishedAt,
	); err == sql.ErrNoRows {
		return skillretention.Run{}, ErrNotFound
	} else if err != nil {
		return skillretention.Run{}, err
	}
	return item, nil
}

func scanSkillAssetGCItem(scanner rowScanner) (skillretention.Item, error) {
	var item skillretention.Item
	if err := scanner.Scan(
		&item.ID, &item.RunID, &item.Candidate.WorkspaceID, &item.Candidate.SkillID,
		&item.Candidate.SkillIdentifier, &item.Candidate.SkillVersionID, &item.Candidate.SkillVersion,
		&item.Candidate.AssetPath, &item.Candidate.ObjectRefID, &item.Candidate.StorageProvider,
		&item.Candidate.Bucket, &item.Candidate.ObjectKey, &item.Candidate.ObjectVersion,
		&item.Candidate.ContentType, &item.Candidate.SizeBytes, &item.Candidate.ChecksumSHA256,
		&item.Candidate.Metadata, &item.Candidate.ScanProvider, &item.Candidate.ScanVersion,
		&item.Reason, &item.Candidate.EligibleAt, &item.Candidate.ObjectCreatedAt, &item.Status,
		&item.Attempts, &item.ObjectWasMissing, &item.ErrorMessage, &item.CreatedAt,
		&item.UpdatedAt, &item.DeletedAt,
	); err == sql.ErrNoRows {
		return skillretention.Item{}, ErrNotFound
	} else if err != nil {
		return skillretention.Item{}, err
	}
	item.Candidate.Reason = item.Reason
	return item, nil
}

func scanSkillAssetGCTombstone(scanner rowScanner) (skillretention.Tombstone, error) {
	var item skillretention.Tombstone
	if err := scanner.Scan(
		&item.ID, &item.RunID, &item.WorkspaceID, &item.SkillID, &item.SkillVersionID,
		&item.AssetPath, &item.ObjectRefID, &item.StorageProvider, &item.Bucket, &item.ObjectKey,
		&item.ObjectVersion, &item.ContentType, &item.SizeBytes, &item.ChecksumSHA256, &item.Metadata,
		&item.ScanProvider, &item.ScanVersion, &item.Reason, &item.ObjectWasMissing,
		&item.DeletedBy, &item.DeletedAt,
	); err != nil {
		return skillretention.Tombstone{}, err
	}
	return item, nil
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func truncateSkillAssetGCError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2000 {
		return value[:2000]
	}
	return value
}
