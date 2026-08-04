package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) ListModelRuntimeQuotaPoliciesContext(ctx context.Context, workspaceID string, includeArchived bool) ([]ModelRuntimeQuotaPolicy, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: quota policy workspace is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, scope, COALESCE(app_id, ''), plan, config_json, status, revision,
			created_by, updated_by, created_at, updated_at, archived_at
		FROM model_runtime_quota_policies
		WHERE workspace_id = $1 AND ($2 OR status = 'active')
		ORDER BY scope, app_id NULLS FIRST, id
	`, workspaceID, includeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelRuntimeQuotaPolicy{}
	for rows.Next() {
		item, scanErr := scanModelRuntimeQuotaPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
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

func (s *PostgresStore) GetModelRuntimeQuotaPolicyContext(ctx context.Context, workspaceID, scope, appID string) (ModelRuntimeQuotaPolicy, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	scope = strings.ToLower(strings.TrimSpace(scope))
	appID = strings.TrimSpace(appID)
	if workspaceID == "" || (scope != ModelRuntimeQuotaScopeWorkspace && scope != ModelRuntimeQuotaScopeApplication) {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: valid quota policy workspace and scope are required", ErrInvalid)
	}
	if (scope == ModelRuntimeQuotaScopeWorkspace && appID != "") || (scope == ModelRuntimeQuotaScopeApplication && appID == "") {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: quota policy target does not match scope", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	defer tx.Rollback()
	item, err := scanModelRuntimeQuotaPolicy(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, scope, COALESCE(app_id, ''), plan, config_json, status, revision,
			created_by, updated_by, created_at, updated_at, archived_at
		FROM model_runtime_quota_policies
		WHERE workspace_id = $1 AND scope = $2 AND (($2 = 'workspace' AND app_id IS NULL) OR app_id = $3)
	`, workspaceID, scope, appID))
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpsertModelRuntimeQuotaPolicyContext(ctx context.Context, input UpsertModelRuntimeQuotaPolicyInput) (ModelRuntimeQuotaPolicy, error) {
	input, err := NormalizeUpsertModelRuntimeQuotaPolicyInput(input)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	configJSON, err := json.Marshal(input.Config)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("marshal quota policy config: %w", err)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	defer tx.Rollback()
	if input.Scope == ModelRuntimeQuotaScopeApplication {
		var kind, status string
		if err := tx.QueryRowContext(ctx, `SELECT kind, status FROM service_identities WHERE workspace_id = $1 AND id = $2`, input.WorkspaceID, input.AppID).Scan(&kind, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: application %s", ErrNotFound, input.AppID)
			}
			return ModelRuntimeQuotaPolicy{}, err
		}
		if kind != ServiceIdentityKindApplication || status != ServiceIdentityStatusActive {
			return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: quota policy target must be an active application identity", ErrInvalid)
		}
	}

	current, found, err := getModelRuntimeQuotaPolicyForUpdate(ctx, tx, input.WorkspaceID, input.Scope, input.AppID)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	var item ModelRuntimeQuotaPolicy
	if !found {
		if input.ExpectedRevision != 0 {
			return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: quota policy does not exist", ErrRevisionConflict)
		}
		id, err := nextSequenceID(ctx, tx, "mrqp", "tma_model_runtime_quota_policy_id_seq")
		if err != nil {
			return ModelRuntimeQuotaPolicy{}, err
		}
		item, err = scanModelRuntimeQuotaPolicy(tx.QueryRowContext(ctx, `
			INSERT INTO model_runtime_quota_policies (
				id, workspace_id, scope, app_id, plan, config_json, status, revision, created_by, updated_by
			) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, 'active', 1, $7, $7)
			RETURNING id, workspace_id, scope, COALESCE(app_id, ''), plan, config_json, status, revision,
				created_by, updated_by, created_at, updated_at, archived_at
		`, id, input.WorkspaceID, input.Scope, input.AppID, input.Plan, configJSON, input.UpdatedBy))
		if err != nil {
			return ModelRuntimeQuotaPolicy{}, err
		}
	} else {
		if input.ExpectedRevision != current.Revision {
			return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: expected=%d actual=%d", ErrRevisionConflict, input.ExpectedRevision, current.Revision)
		}
		item, err = scanModelRuntimeQuotaPolicy(tx.QueryRowContext(ctx, `
			UPDATE model_runtime_quota_policies
			SET plan = $2, config_json = $3, status = 'active', revision = revision + 1,
				updated_by = $4, updated_at = now(), archived_at = NULL
			WHERE id = $1
			RETURNING id, workspace_id, scope, COALESCE(app_id, ''), plan, config_json, status, revision,
				created_by, updated_by, created_at, updated_at, archived_at
		`, current.ID, input.Plan, configJSON, input.UpdatedBy))
		if err != nil {
			return ModelRuntimeQuotaPolicy{}, err
		}
	}
	if err := insertModelRuntimeQuotaPolicyVersion(ctx, tx, item, input.UpdatedBy); err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	return item, nil
}

func (s *PostgresStore) ArchiveModelRuntimeQuotaPolicyContext(ctx context.Context, input ArchiveModelRuntimeQuotaPolicyInput) (ModelRuntimeQuotaPolicy, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Scope = strings.ToLower(strings.TrimSpace(input.Scope))
	input.AppID = strings.TrimSpace(input.AppID)
	input.ArchivedBy = strings.TrimSpace(input.ArchivedBy)
	if input.WorkspaceID == "" || input.ArchivedBy == "" || input.ExpectedRevision <= 0 || input.Scope != ModelRuntimeQuotaScopeApplication || input.AppID == "" {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: application quota policy target, positive revision, and actor are required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	defer tx.Rollback()
	current, found, err := getModelRuntimeQuotaPolicyForUpdate(ctx, tx, input.WorkspaceID, input.Scope, input.AppID)
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	if !found {
		return ModelRuntimeQuotaPolicy{}, ErrNotFound
	}
	if current.Revision != input.ExpectedRevision {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: expected=%d actual=%d", ErrRevisionConflict, input.ExpectedRevision, current.Revision)
	}
	if current.Status == ModelRuntimeQuotaPolicyStatusArchived {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("%w: quota policy is already archived", ErrConflict)
	}
	item, err := scanModelRuntimeQuotaPolicy(tx.QueryRowContext(ctx, `
		UPDATE model_runtime_quota_policies
		SET status = 'archived', revision = revision + 1, updated_by = $2, updated_at = now(), archived_at = now()
		WHERE id = $1
		RETURNING id, workspace_id, scope, COALESCE(app_id, ''), plan, config_json, status, revision,
			created_by, updated_by, created_at, updated_at, archived_at
	`, current.ID, input.ArchivedBy))
	if err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	if err := insertModelRuntimeQuotaPolicyVersion(ctx, tx, item, input.ArchivedBy); err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelRuntimeQuotaPolicy{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetModelRuntimeQuotaUsageContext(ctx context.Context, workspaceID, appID string, from, to time.Time) (ModelRuntimeQuotaUsage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	appID = strings.TrimSpace(appID)
	if workspaceID == "" || from.IsZero() || to.IsZero() || !from.Before(to) {
		return ModelRuntimeQuotaUsage{}, fmt.Errorf("%w: quota usage requires workspace and valid period", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return ModelRuntimeQuotaUsage{}, err
	}
	defer tx.Rollback()
	usage := ModelRuntimeQuotaUsage{PeriodStartedAt: from.UTC(), PeriodEndsAt: to.UTC()}
	err = tx.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE capability IN ('generate', 'embedding', 'rerank')),
			count(*) FILTER (WHERE capability IN ('speech_to_text', 'text_to_speech', 'multimodal_realtime'))
		FROM model_invocations
		WHERE workspace_id = $1
			AND ($2 = '' OR service_identity_id = $2)
			AND started_at >= $3 AND started_at < $4
			AND NOT (status = 'failed' AND error_code IN (
				'model_quota_exceeded', 'model_capacity_exceeded', 'speech_quota_exceeded', 'speech_capacity_exceeded'
			))
	`, workspaceID, appID, from.UTC(), to.UTC()).Scan(&usage.ModelRequests, &usage.SpeechSessions)
	if err != nil {
		return ModelRuntimeQuotaUsage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelRuntimeQuotaUsage{}, err
	}
	return usage, nil
}

func getModelRuntimeQuotaPolicyForUpdate(ctx context.Context, tx *sql.Tx, workspaceID, scope, appID string) (ModelRuntimeQuotaPolicy, bool, error) {
	item, err := scanModelRuntimeQuotaPolicy(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, scope, COALESCE(app_id, ''), plan, config_json, status, revision,
			created_by, updated_by, created_at, updated_at, archived_at
		FROM model_runtime_quota_policies
		WHERE workspace_id = $1 AND scope = $2 AND (($2 = 'workspace' AND app_id IS NULL) OR app_id = $3)
		FOR UPDATE
	`, workspaceID, scope, appID))
	if errors.Is(err, ErrNotFound) {
		return ModelRuntimeQuotaPolicy{}, false, nil
	}
	return item, err == nil, err
}

func insertModelRuntimeQuotaPolicyVersion(ctx context.Context, tx *sql.Tx, item ModelRuntimeQuotaPolicy, actor string) error {
	id, err := nextSequenceID(ctx, tx, "mrqpv", "tma_model_runtime_quota_policy_version_id_seq")
	if err != nil {
		return err
	}
	configJSON, err := json.Marshal(item.Config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO model_runtime_quota_policy_versions (
			id, policy_id, workspace_id, scope, app_id, plan, config_json, status, revision, changed_by
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10)
	`, id, item.ID, item.WorkspaceID, item.Scope, item.AppID, item.Plan, configJSON, item.Status, item.Revision, actor)
	return err
}

type modelRuntimeQuotaPolicyScanner interface {
	Scan(...any) error
}

func scanModelRuntimeQuotaPolicy(scanner modelRuntimeQuotaPolicyScanner) (ModelRuntimeQuotaPolicy, error) {
	var item ModelRuntimeQuotaPolicy
	var configJSON []byte
	if err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.Scope, &item.AppID, &item.Plan, &configJSON, &item.Status, &item.Revision,
		&item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.ArchivedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ModelRuntimeQuotaPolicy{}, ErrNotFound
		}
		return ModelRuntimeQuotaPolicy{}, err
	}
	if err := json.Unmarshal(configJSON, &item.Config); err != nil {
		return ModelRuntimeQuotaPolicy{}, fmt.Errorf("decode quota policy config: %w", err)
	}
	return item, nil
}

var _ ModelRuntimeQuotaPolicyStore = (*PostgresStore)(nil)
