package managedagents

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	mcppkg "tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/skillpackage"
)

const (
	eventNotificationChannel       = "tma_session_events"
	eventCatchUpInterval           = time.Second
	eventListenerReconnectInterval = time.Second
)

type PostgresStore struct {
	db               *sql.DB
	hub              *eventHub
	claimMu          sync.Mutex
	claimCursor      int
	auditClaimMu     sync.Mutex
	auditClaimCursor int
	skillPackageMu   sync.RWMutex
	skillPackages    *skillpackage.Repository
	listenerCancel   context.CancelFunc
	listenerDone     chan struct{}
	closeOnce        sync.Once
	closeErr         error
}

func NewPostgresStore(databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	store := &PostgresStore{db: db, hub: newEventHub()}
	store.startEventListener(databaseURL)
	return store, nil
}

func (s *PostgresStore) Close() error {
	s.closeOnce.Do(func() {
		if s.listenerCancel != nil {
			s.listenerCancel()
		}
		if s.listenerDone != nil {
			<-s.listenerDone
		}
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

func (s *PostgresStore) EnsureLLMProvider(input EnsureLLMProviderInput) (LLMProvider, error) {
	// Ensure 用于服务启动时保证默认 Provider 存在，因此总是写成启用状态。
	return s.UpsertLLMProvider(UpsertLLMProviderInput{
		ID:           input.ID,
		ProviderType: input.ProviderType,
		BaseURL:      input.BaseURL,
		APIKeyEnv:    input.APIKeyEnv,
		Enabled:      true,
	})
}

func (s *PostgresStore) UpsertLLMProvider(input UpsertLLMProviderInput) (LLMProvider, error) {
	if input.ID == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if input.ProviderType == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider type is required", ErrInvalid)
	}

	if _, err := s.db.ExecContext(context.Background(), `
		SELECT tma_control_upsert_llm_provider($1, $2, $3, $4, $5)
	`, input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled); err != nil {
		return LLMProvider{}, err
	}
	return s.GetLLMProvider(input.ID)
}

func (s *PostgresStore) CreateLLMProvider(input UpsertLLMProviderInput) (LLMProvider, error) {
	if input.ID == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if input.ProviderType == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider type is required", ErrInvalid)
	}
	var created bool
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT tma_control_create_llm_provider($1, $2, $3, $4, $5)
	`, input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled).Scan(&created); err != nil {
		return LLMProvider{}, err
	}
	if !created {
		return LLMProvider{}, fmt.Errorf("%w: llm provider %s already exists", ErrConflict, input.ID)
	}
	return s.GetLLMProvider(input.ID)
}

func (s *PostgresStore) UpdateLLMProvider(input UpdateLLMProviderInput) (LLMProvider, error) {
	if input.ID == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if input.ProviderType == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider type is required", ErrInvalid)
	}
	if input.ExpectedRevision <= 0 {
		return LLMProvider{}, fmt.Errorf("%w: expected llm provider revision must be positive", ErrInvalid)
	}
	var updated bool
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT tma_control_update_llm_provider($1, $2, $3, $4, $5, $6)
	`, input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled, input.ExpectedRevision).Scan(&updated); err != nil {
		return LLMProvider{}, err
	}
	if !updated {
		current, err := s.GetLLMProvider(input.ID)
		if err != nil {
			return LLMProvider{}, err
		}
		return LLMProvider{}, fmt.Errorf("%w: llm provider %s revision changed from %d to %d", ErrRevisionConflict, input.ID, input.ExpectedRevision, current.Revision)
	}
	return s.GetLLMProvider(input.ID)
}

func (s *PostgresStore) GetLLMProvider(id string) (LLMProvider, error) {
	if id == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}

	var provider LLMProvider
	err := s.db.QueryRowContext(context.Background(), `
		SELECT id, provider_type, base_url, api_key_env, enabled, revision, created_at, updated_at
		FROM llm_providers
		WHERE id = $1
	`, id).Scan(
		&provider.ID,
		&provider.ProviderType,
		&provider.BaseURL,
		&provider.APIKeyEnv,
		&provider.Enabled,
		&provider.Revision,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return LLMProvider{}, ErrNotFound
	}
	if err != nil {
		return LLMProvider{}, err
	}
	return provider, nil
}

func (s *PostgresStore) ListLLMProviders() ([]LLMProvider, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, provider_type, base_url, api_key_env, enabled, revision, created_at, updated_at
		FROM llm_providers
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []LLMProvider
	for rows.Next() {
		var provider LLMProvider
		if err := rows.Scan(
			&provider.ID,
			&provider.ProviderType,
			&provider.BaseURL,
			&provider.APIKeyEnv,
			&provider.Enabled,
			&provider.Revision,
			&provider.CreatedAt,
			&provider.UpdatedAt,
		); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return providers, nil
}

func (s *PostgresStore) SetLLMProviderEnabled(id string, enabled bool) (LLMProvider, error) {
	if id == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}

	var updated bool
	if err := s.db.QueryRowContext(context.Background(), `SELECT tma_control_set_llm_provider_enabled($1, $2)`, id, enabled).Scan(&updated); err != nil {
		return LLMProvider{}, err
	}
	if !updated {
		return LLMProvider{}, ErrNotFound
	}
	return s.GetLLMProvider(id)
}

func (s *PostgresStore) SetLLMProviderEnabledIfRevision(id string, enabled bool, expectedRevision int64) (LLMProvider, error) {
	id = strings.TrimSpace(id)
	if id == "" || expectedRevision <= 0 {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id and positive expected revision are required", ErrInvalid)
	}
	var updated bool
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT tma_control_set_llm_provider_enabled($1, $2, $3)
	`, id, enabled, expectedRevision).Scan(&updated); err != nil {
		return LLMProvider{}, err
	}
	if !updated {
		current, err := s.GetLLMProvider(id)
		if err != nil {
			return LLMProvider{}, err
		}
		return LLMProvider{}, fmt.Errorf("%w: llm provider %s revision changed from %d to %d", ErrRevisionConflict, id, expectedRevision, current.Revision)
	}
	return s.GetLLMProvider(id)
}

func (s *PostgresStore) DeleteLLMProvider(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}

	ctx := context.Background()
	referenced, err := s.llmProviderReferencedAcrossWorkspaces(ctx, id)
	if err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("%w: llm provider %s is referenced by an agent configuration or session", ErrConflict, id)
	}
	var deleted bool
	if err := s.db.QueryRowContext(ctx, `SELECT tma_control_delete_llm_provider($1)`, id).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm provider "+id)
	}
	if deleted {
		return nil
	}
	return ErrNotFound
}

func (s *PostgresStore) DeleteLLMProviderIfRevision(id string, expectedRevision int64) error {
	id = strings.TrimSpace(id)
	if id == "" || expectedRevision <= 0 {
		return fmt.Errorf("%w: llm provider id and positive expected revision are required", ErrInvalid)
	}
	ctx := context.Background()
	referenced, err := s.llmProviderReferencedAcrossWorkspaces(ctx, id)
	if err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("%w: llm provider %s is referenced by an agent configuration or session", ErrConflict, id)
	}
	var deleted bool
	if err := s.db.QueryRowContext(ctx, `SELECT tma_control_delete_llm_provider($1, $2)`, id, expectedRevision).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm provider "+id)
	}
	if deleted {
		return nil
	}
	current, err := s.GetLLMProvider(id)
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: llm provider %s revision changed from %d to %d", ErrRevisionConflict, id, expectedRevision, current.Revision)
}

func (s *PostgresStore) UpsertLLMModel(input UpsertLLMModelInput) (LLMModel, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.ProviderID == "" {
		return LLMModel{}, fmt.Errorf("%w: llm model provider_id is required", ErrInvalid)
	}
	if input.Model == "" {
		return LLMModel{}, fmt.Errorf("%w: llm model is required", ErrInvalid)
	}
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = DefaultContextWindowTokens
	}
	if _, err := s.GetLLMProvider(input.ProviderID); err != nil {
		return LLMModel{}, err
	}
	var existing *LLMModel
	if current, err := queryLLMModel(context.Background(), s.db, input.ProviderID, input.Model); err == nil {
		existing = &current
	} else if !errors.Is(err, ErrNotFound) {
		return LLMModel{}, err
	}
	input, defaults, err := normalizeLLMModelMutationInput(input, existing)
	if err != nil {
		return LLMModel{}, err
	}
	capabilitiesJSON, err := llmModelCapabilitiesJSON(*input.Capabilities)
	if err != nil {
		return LLMModel{}, err
	}

	if _, err := s.db.ExecContext(context.Background(), `
		SELECT tma_control_upsert_llm_model($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
	`, input.ProviderID, input.Model, input.ContextWindowTokens, input.CapabilityType, capabilitiesJSON,
		defaults.Vision, defaults.Embedding, defaults.Reranker); err != nil {
		return LLMModel{}, err
	}
	return queryLLMModel(context.Background(), s.db, input.ProviderID, input.Model)
}

func normalizeLLMModelMutationInput(input UpsertLLMModelInput, existing *LLMModel) (UpsertLLMModelInput, llmModelDefaults, error) {
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = DefaultContextWindowTokens
	}
	capabilityType := strings.TrimSpace(input.CapabilityType)
	if capabilityType == "" && existing != nil {
		capabilityType = existing.CapabilityType
	}
	var valid bool
	capabilityType, valid = NormalizeLLMModelCapability(capabilityType)
	if !valid {
		return UpsertLLMModelInput{}, llmModelDefaults{}, fmt.Errorf("%w: unsupported llm model capability_type %q", ErrInvalid, input.CapabilityType)
	}
	input.CapabilityType = capabilityType
	capabilities, err := normalizeLLMModelCapabilities(capabilityType, input.Capabilities, existing)
	if err != nil {
		return UpsertLLMModelInput{}, llmModelDefaults{}, err
	}
	input.Capabilities = &capabilities
	defaults, err := normalizeLLMModelDefaults(input, capabilityType, existing)
	if err != nil {
		return UpsertLLMModelInput{}, llmModelDefaults{}, err
	}
	return input, defaults, nil
}

func (s *PostgresStore) CreateLLMModel(input UpsertLLMModelInput) (LLMModel, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.ProviderID == "" || input.Model == "" {
		return LLMModel{}, fmt.Errorf("%w: llm model provider_id and model are required", ErrInvalid)
	}
	if _, err := s.GetLLMProvider(input.ProviderID); err != nil {
		return LLMModel{}, err
	}
	input, defaults, err := normalizeLLMModelMutationInput(input, nil)
	if err != nil {
		return LLMModel{}, err
	}
	capabilitiesJSON, err := llmModelCapabilitiesJSON(*input.Capabilities)
	if err != nil {
		return LLMModel{}, err
	}
	var created bool
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT tma_control_create_llm_model($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
	`, input.ProviderID, input.Model, input.ContextWindowTokens, input.CapabilityType, capabilitiesJSON,
		defaults.Vision, defaults.Embedding, defaults.Reranker).Scan(&created); err != nil {
		return LLMModel{}, err
	}
	if !created {
		return LLMModel{}, fmt.Errorf("%w: llm model %s/%s already exists", ErrConflict, input.ProviderID, input.Model)
	}
	return queryLLMModel(context.Background(), s.db, input.ProviderID, input.Model)
}

func (s *PostgresStore) UpdateLLMModel(input UpdateLLMModelInput) (LLMModel, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.ProviderID == "" || input.Model == "" || input.ExpectedRevision <= 0 {
		return LLMModel{}, fmt.Errorf("%w: llm model identity and positive expected revision are required", ErrInvalid)
	}
	if _, err := s.GetLLMProvider(input.ProviderID); err != nil {
		return LLMModel{}, err
	}
	existing, err := queryLLMModel(context.Background(), s.db, input.ProviderID, input.Model)
	if err != nil {
		return LLMModel{}, err
	}
	normalized, defaults, err := normalizeLLMModelMutationInput(input.UpsertLLMModelInput, &existing)
	if err != nil {
		return LLMModel{}, err
	}
	capabilitiesJSON, err := llmModelCapabilitiesJSON(*normalized.Capabilities)
	if err != nil {
		return LLMModel{}, err
	}
	var updated bool
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT tma_control_update_llm_model($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)
	`, normalized.ProviderID, normalized.Model, normalized.ContextWindowTokens, normalized.CapabilityType, capabilitiesJSON,
		defaults.Vision, defaults.Embedding, defaults.Reranker, input.ExpectedRevision).Scan(&updated); err != nil {
		return LLMModel{}, err
	}
	if !updated {
		current, err := queryLLMModel(context.Background(), s.db, input.ProviderID, input.Model)
		if err != nil {
			return LLMModel{}, err
		}
		return LLMModel{}, fmt.Errorf("%w: llm model %s/%s revision changed from %d to %d", ErrRevisionConflict, input.ProviderID, input.Model, input.ExpectedRevision, current.Revision)
	}
	return queryLLMModel(context.Background(), s.db, input.ProviderID, input.Model)
}

func (s *PostgresStore) ListLLMModels(providerID string) ([]LLMModel, error) {
	query := `
		SELECT provider_id, model, context_window_tokens, capability_type, capabilities_json,
		       is_default_vision, is_default_embedding, is_default_reranker, revision, created_at, updated_at
		FROM llm_models
		WHERE ($1 = '' OR provider_id = $1)
		ORDER BY provider_id, model
	`
	rows, err := s.db.QueryContext(context.Background(), query, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []LLMModel
	for rows.Next() {
		var model LLMModel
		var capabilitiesJSON []byte
		if err := rows.Scan(
			&model.ProviderID,
			&model.Model,
			&model.ContextWindowTokens,
			&model.CapabilityType,
			&capabilitiesJSON,
			&model.IsDefaultVision,
			&model.IsDefaultEmbedding,
			&model.IsDefaultReranker,
			&model.Revision,
			&model.CreatedAt,
			&model.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := scanLLMModelCapabilities(capabilitiesJSON, &model); err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return models, nil
}

func (s *PostgresStore) DeleteLLMModel(providerID string, model string) error {
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if providerID == "" || model == "" {
		return fmt.Errorf("%w: llm model provider_id and model are required", ErrInvalid)
	}

	ctx := context.Background()
	referenced, err := s.llmModelReferencedAcrossWorkspaces(ctx, providerID, model)
	if err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("%w: llm model %s/%s is referenced by an agent configuration or session", ErrConflict, providerID, model)
	}
	var deleted bool
	if err := s.db.QueryRowContext(ctx, `SELECT tma_control_delete_llm_model($1, $2)`, providerID, model).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm model "+providerID+"/"+model)
	}
	if deleted {
		return nil
	}

	return ErrNotFound
}

func (s *PostgresStore) DeleteLLMModelIfRevision(providerID string, model string, expectedRevision int64) error {
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if providerID == "" || model == "" || expectedRevision <= 0 {
		return fmt.Errorf("%w: llm model identity and positive expected revision are required", ErrInvalid)
	}
	ctx := context.Background()
	referenced, err := s.llmModelReferencedAcrossWorkspaces(ctx, providerID, model)
	if err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("%w: llm model %s/%s is referenced by an agent configuration or session", ErrConflict, providerID, model)
	}
	var deleted bool
	if err := s.db.QueryRowContext(ctx, `SELECT tma_control_delete_llm_model($1, $2, $3)`, providerID, model, expectedRevision).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm model "+providerID+"/"+model)
	}
	if deleted {
		return nil
	}
	current, err := queryLLMModel(ctx, s.db, providerID, model)
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: llm model %s/%s revision changed from %d to %d", ErrRevisionConflict, providerID, model, expectedRevision, current.Revision)
}

func (s *PostgresStore) EnsureAgent(input EnsureAgentInput) (Agent, error) {
	return s.ensureAgentContext(context.Background(), input)
}

func (s *PostgresStore) ensureAgentContext(ctx context.Context, input EnsureAgentInput) (Agent, error) {
	if input.ID == "" {
		return Agent{}, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}
	if input.Name == "" {
		return Agent{}, fmt.Errorf("%w: agent name is required", ErrInvalid)
	}
	if input.LLMProvider == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_provider is required", ErrInvalid)
	}
	if input.LLMModel == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_model is required", ErrInvalid)
	}
	if err := s.validateLLMSelection(input.LLMProvider, input.LLMModel); err != nil {
		return Agent{}, err
	}

	workspaceID := defaultString(input.WorkspaceID, DefaultWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if input.WorkspaceID != "" && input.WorkspaceID != scope.WorkspaceID {
			return Agent{}, fmt.Errorf("%w: agent workspace scope mismatch", ErrForbidden)
		}
		workspaceID = scope.WorkspaceID
	}
	ownership, err := NormalizeAgentOwnership(workspaceID, AgentOwnership{
		OwnerType: input.OwnerType, OwnerID: input.OwnerID, Visibility: input.Visibility, AgentKind: input.AgentKind,
	})
	if err != nil {
		return Agent{}, err
	}
	normalizedSkills, err := s.normalizeAgentSkills(agentSkillContext(ctx, Agent{WorkspaceID: workspaceID, OwnerType: ownership.OwnerType, OwnerID: ownership.OwnerID}), workspaceID, input.Skills)
	if err != nil {
		return Agent{}, err
	}
	input.Skills = normalizedSkills
	normalizedMCP, err := normalizeAgentMCP(input.MCP)
	if err != nil {
		return Agent{}, err
	}
	input.MCP = normalizedMCP
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
		return Agent{}, err
	}

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
			INSERT INTO agents (
				id, workspace_id, owner_type, owner_id, visibility, agent_kind,
				name, current_config_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8)
			ON CONFLICT (id) DO NOTHING
		`, input.ID, workspaceID, ownership.OwnerType, ownership.OwnerID, ownership.Visibility, ownership.AgentKind,
		input.Name, now)
	if err != nil {
		return Agent{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Agent{}, err
	}
	if inserted == 1 {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model, system, tools_json, mcp_json, skills_json, created_at)
			VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8)
			`, input.ID, input.LLMProvider, input.LLMModel, input.System, nullableRaw(input.Tools), nullableRaw(input.MCP), nullableRaw(input.Skills), now)
		if err != nil {
			return Agent{}, normalizeLLMReferenceWriteError(err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE agents SET archived_at = NULL WHERE id = $1`, input.ID); err != nil {
			return Agent{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}
	resultCtx := ctx
	if _, ok := DatabaseAccessScopeFromContext(resultCtx); !ok {
		resultCtx, err = ContextWithDatabaseAccessScope(resultCtx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return Agent{}, err
		}
	}
	return s.GetAgentContext(resultCtx, input.ID)
}

func (s *PostgresStore) CreateAgent(input CreateAgentInput) (Agent, error) {
	return s.createAgentContext(context.Background(), input)
}

func (s *PostgresStore) createAgentContext(ctx context.Context, input CreateAgentInput) (Agent, error) {
	if input.Name == "" {
		return Agent{}, fmt.Errorf("%w: agent name is required", ErrInvalid)
	}
	llmProvider := agentLLMProvider(input)
	llmModel := agentLLMModel(input)
	if llmModel == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_model is required", ErrInvalid)
	}
	if err := s.validateLLMSelection(llmProvider, llmModel); err != nil {
		return Agent{}, err
	}

	workspaceID := defaultString(input.WorkspaceID, DefaultWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if input.WorkspaceID != "" && input.WorkspaceID != scope.WorkspaceID {
			return Agent{}, fmt.Errorf("%w: agent workspace scope mismatch", ErrForbidden)
		}
		workspaceID = scope.WorkspaceID
	}
	ownership, err := NormalizeAgentOwnership(workspaceID, AgentOwnership{
		OwnerType: input.OwnerType, OwnerID: input.OwnerID, Visibility: input.Visibility, AgentKind: input.AgentKind,
	})
	if err != nil {
		return Agent{}, err
	}
	normalizedSkills, err := s.normalizeAgentSkills(agentSkillContext(ctx, Agent{WorkspaceID: workspaceID, OwnerType: ownership.OwnerType, OwnerID: ownership.OwnerID}), workspaceID, input.Skills)
	if err != nil {
		return Agent{}, err
	}
	input.Skills = normalizedSkills
	normalizedMCP, err := normalizeAgentMCP(input.MCP)
	if err != nil {
		return Agent{}, err
	}
	input.MCP = normalizedMCP
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
		return Agent{}, err
	}

	id, err := nextSequenceID(ctx, tx, "agt", "tma_agent_id_seq")
	if err != nil {
		return Agent{}, err
	}

	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agents (
			id, workspace_id, owner_type, owner_id, visibility, agent_kind,
			name, current_config_version, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, workspaceID, ownership.OwnerType, ownership.OwnerID, ownership.Visibility, ownership.AgentKind,
		input.Name, 1, now)
	if err != nil {
		return Agent{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model, system, tools_json, mcp_json, skills_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, 1, llmProvider, llmModel, input.System, nullableRaw(input.Tools), nullableRaw(input.MCP), nullableRaw(input.Skills), now)
	if err != nil {
		return Agent{}, normalizeLLMReferenceWriteError(err)
	}

	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}

	return Agent{
		ID:                   id,
		WorkspaceID:          workspaceID,
		OwnerType:            ownership.OwnerType,
		OwnerID:              ownership.OwnerID,
		Visibility:           ownership.Visibility,
		AgentKind:            ownership.AgentKind,
		Name:                 input.Name,
		CurrentConfigVersion: 1,
		ConfigVersion: AgentConfigVersion{
			Version:     1,
			LLMProvider: llmProvider,
			LLMModel:    llmModel,
			System:      input.System,
			Tools:       cloneRaw(input.Tools),
			MCP:         cloneRaw(input.MCP),
			Skills:      cloneRaw(input.Skills),
			CreatedAt:   now,
		},
		CreatedAt: now,
	}, nil
}

func (s *PostgresStore) GetAgent(id string) (Agent, error) {
	return s.getAgent(id, "")
}

func (s *PostgresStore) GetAgentScoped(id string, scope AccessScope) (Agent, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return Agent{}, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return Agent{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	agent, err := getAgentQuery(ctx, tx, id, scope.WorkspaceID)
	if errors.Is(err, ErrNotFound) {
		return Agent{}, ErrForbidden
	}
	if err != nil {
		return Agent{}, err
	}
	if scope.OwnerID != "" && agent.OwnerType == AgentOwnerUser && agent.OwnerID != scope.OwnerID {
		return Agent{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func (s *PostgresStore) getAgent(id string, workspaceID string) (Agent, error) {
	return getAgentQuery(context.Background(), s.db, id, workspaceID)
}

func getAgentQuery(ctx context.Context, q queryer, id string, workspaceID string) (Agent, error) {
	if id == "" {
		return Agent{}, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}

	var agent Agent
	var tools []byte
	var mcp []byte
	var skills []byte
	var archivedAt sql.NullTime
	err := q.QueryRowContext(ctx, `
			SELECT
				a.id,
				a.workspace_id,
				a.owner_type,
				a.owner_id,
				a.visibility,
				a.agent_kind,
				a.name,
			a.current_config_version,
			a.archived_at,
			a.created_at,
			av.version,
			av.llm_provider,
			av.llm_model,
			av.system,
			av.tools_json,
			av.mcp_json,
			av.skills_json,
			av.created_at
		FROM agents a
		JOIN agent_config_versions av
			ON av.agent_id = a.id
			AND av.version = a.current_config_version
		WHERE a.id = $1
			AND ($2 = '' OR a.workspace_id = $2)
	`, id, workspaceID).Scan(
		&agent.ID,
		&agent.WorkspaceID,
		&agent.OwnerType,
		&agent.OwnerID,
		&agent.Visibility,
		&agent.AgentKind,
		&agent.Name,
		&agent.CurrentConfigVersion,
		&archivedAt,
		&agent.CreatedAt,
		&agent.ConfigVersion.Version,
		&agent.ConfigVersion.LLMProvider,
		&agent.ConfigVersion.LLMModel,
		&agent.ConfigVersion.System,
		&tools,
		&mcp,
		&skills,
		&agent.ConfigVersion.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if archivedAt.Valid {
		agent.ArchivedAt = &archivedAt.Time
	}
	agent.ConfigVersion.Tools = cloneRaw(tools)
	agent.ConfigVersion.MCP = cloneRaw(mcp)
	agent.ConfigVersion.Skills = cloneRaw(skills)
	return agent, nil
}

func (s *PostgresStore) ListAgents() ([]Agent, error) {
	return listAgentsQuery(context.Background(), s.db, "")
}

func (s *PostgresStore) ListAgentsScoped(scope AccessScope) ([]Agent, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return nil, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	agents, err := listAgentsQuery(ctx, tx, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if scope.OwnerID != "" {
		visible := agents[:0]
		for _, agent := range agents {
			if agent.OwnerType != AgentOwnerUser || agent.OwnerID == scope.OwnerID {
				visible = append(visible, agent)
			}
		}
		agents = visible
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return agents, nil
}

type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func listAgentsQuery(ctx context.Context, q rowsQueryer, workspaceID string) ([]Agent, error) {
	rows, err := q.QueryContext(ctx, `
			SELECT
				a.id,
				a.workspace_id,
				a.owner_type,
				a.owner_id,
				a.visibility,
				a.agent_kind,
				a.name,
			a.current_config_version,
			a.created_at,
			av.version,
			av.llm_provider,
			av.llm_model,
			av.system,
			av.tools_json,
			av.mcp_json,
			av.skills_json,
			av.created_at
		FROM agents a
		JOIN agent_config_versions av
			ON av.agent_id = a.id
			AND av.version = a.current_config_version
		WHERE a.archived_at IS NULL
			AND ($1 = '' OR a.workspace_id = $1)
		ORDER BY a.created_at DESC, a.id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var agent Agent
		var tools []byte
		var mcp []byte
		var skills []byte
		if err := rows.Scan(
			&agent.ID,
			&agent.WorkspaceID,
			&agent.OwnerType,
			&agent.OwnerID,
			&agent.Visibility,
			&agent.AgentKind,
			&agent.Name,
			&agent.CurrentConfigVersion,
			&agent.CreatedAt,
			&agent.ConfigVersion.Version,
			&agent.ConfigVersion.LLMProvider,
			&agent.ConfigVersion.LLMModel,
			&agent.ConfigVersion.System,
			&tools,
			&mcp,
			&skills,
			&agent.ConfigVersion.CreatedAt,
		); err != nil {
			return nil, err
		}
		agent.ConfigVersion.Tools = cloneRaw(tools)
		agent.ConfigVersion.MCP = cloneRaw(mcp)
		agent.ConfigVersion.Skills = cloneRaw(skills)
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return agents, nil
}

func (s *PostgresStore) UpdateAgent(input UpdateAgentInput) (Agent, error) {
	return s.updateAgentContext(context.Background(), input)
}

func (s *PostgresStore) updateAgentContext(ctx context.Context, input UpdateAgentInput) (Agent, error) {
	if input.AgentID == "" {
		return Agent{}, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}

	current, err := s.GetAgentContext(ctx, input.AgentID)
	if err != nil {
		return Agent{}, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = current.Name
	}

	nextConfig := current.ConfigVersion
	configChanged := false
	if strings.TrimSpace(input.LLMProvider) != "" && strings.TrimSpace(input.LLMProvider) != nextConfig.LLMProvider {
		nextConfig.LLMProvider = strings.TrimSpace(input.LLMProvider)
		configChanged = true
	}
	if strings.TrimSpace(input.LLMModel) != "" && strings.TrimSpace(input.LLMModel) != nextConfig.LLMModel {
		nextConfig.LLMModel = strings.TrimSpace(input.LLMModel)
		configChanged = true
	}
	if input.System != "" && input.System != nextConfig.System {
		nextConfig.System = input.System
		configChanged = true
	}
	if input.Tools != nil {
		nextConfig.Tools = cloneRaw(input.Tools)
		configChanged = true
	}
	if input.MCP != nil {
		normalizedMCP, normalizeErr := normalizeAgentMCP(input.MCP)
		if normalizeErr != nil {
			return Agent{}, normalizeErr
		}
		nextConfig.MCP = normalizedMCP
		configChanged = true
	}
	if input.Skills != nil {
		normalizedSkills, normalizeErr := s.normalizeAgentSkills(agentSkillContext(ctx, current), current.WorkspaceID, input.Skills)
		if normalizeErr != nil {
			return Agent{}, normalizeErr
		}
		nextConfig.Skills = normalizedSkills
		configChanged = true
	}
	if configChanged {
		if nextConfig.LLMModel == "" {
			return Agent{}, fmt.Errorf("%w: agent llm_model is required", ErrInvalid)
		}
		if err := s.validateLLMSelection(nextConfig.LLMProvider, nextConfig.LLMModel); err != nil {
			return Agent{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, current.WorkspaceID); err != nil {
		return Agent{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE agents
		SET name = $2
		WHERE id = $1 AND archived_at IS NULL
	`, input.AgentID, name); err != nil {
		return Agent{}, err
	}

	if configChanged {
		nextVersion := current.CurrentConfigVersion + 1
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model, system, tools_json, mcp_json, skills_json, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			`, input.AgentID, nextVersion, nextConfig.LLMProvider, nextConfig.LLMModel, nextConfig.System, nullableRaw(nextConfig.Tools), nullableRaw(nextConfig.MCP), nullableRaw(nextConfig.Skills), now); err != nil {
			return Agent{}, normalizeLLMReferenceWriteError(err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE agents
			SET current_config_version = $2
			WHERE id = $1
		`, input.AgentID, nextVersion); err != nil {
			return Agent{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}
	return s.GetAgentContext(ctx, input.AgentID)
}

func (s *PostgresStore) ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error) {
	return s.listAgentConfigVersionsContext(context.Background(), agentID)
}

func (s *PostgresStore) listAgentConfigVersionsContext(ctx context.Context, agentID string) ([]AgentConfigVersion, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}

	agent, err := s.GetAgentContext(ctx, agentID)
	if err != nil {
		return nil, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, agent.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT version, llm_provider, llm_model, system, tools_json, mcp_json, skills_json, created_at
		FROM agent_config_versions
		WHERE agent_id = $1
		ORDER BY version
	`, agentID)
	if err != nil {
		return nil, err
	}
	var versions []AgentConfigVersion
	for rows.Next() {
		var version AgentConfigVersion
		var tools []byte
		var mcp []byte
		var skills []byte
		if err := rows.Scan(
			&version.Version,
			&version.LLMProvider,
			&version.LLMModel,
			&version.System,
			&tools,
			&mcp,
			&skills,
			&version.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		version.Tools = cloneRaw(tools)
		version.MCP = cloneRaw(mcp)
		version.Skills = cloneRaw(skills)
		versions = append(versions, version)
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
	return versions, nil
}

func (s *PostgresStore) CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error) {
	return s.createAgentConfigVersionContext(context.Background(), input)
}

func (s *PostgresStore) createAgentConfigVersionContext(ctx context.Context, input CreateAgentConfigVersionInput) (Agent, error) {
	if input.AgentID == "" {
		return Agent{}, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}
	if input.LLMProvider == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_provider is required", ErrInvalid)
	}
	if input.LLMModel == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_model is required", ErrInvalid)
	}
	if err := s.validateLLMSelection(input.LLMProvider, input.LLMModel); err != nil {
		return Agent{}, err
	}
	normalizedMCP, err := normalizeAgentMCP(input.MCP)
	if err != nil {
		return Agent{}, err
	}
	input.MCP = normalizedMCP
	current, err := s.GetAgentContext(ctx, input.AgentID)
	if err != nil {
		return Agent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, current.WorkspaceID); err != nil {
		return Agent{}, err
	}

	var agent Agent
	var currentVersion int
	var currentSkills []byte
	var archivedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
			SELECT a.id, a.workspace_id, a.owner_type, a.owner_id, a.visibility, a.agent_kind,
				a.name, a.current_config_version, a.archived_at, a.created_at, av.skills_json
		FROM agents a
		JOIN agent_config_versions av ON av.agent_id = a.id AND av.version = a.current_config_version
		WHERE a.id = $1
		FOR UPDATE
	`, input.AgentID).Scan(
		&agent.ID,
		&agent.WorkspaceID,
		&agent.OwnerType,
		&agent.OwnerID,
		&agent.Visibility,
		&agent.AgentKind,
		&agent.Name,
		&currentVersion,
		&archivedAt,
		&agent.CreatedAt,
		&currentSkills,
	)
	if err == sql.ErrNoRows {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if archivedAt.Valid {
		return Agent{}, fmt.Errorf("%w: agent %s is archived", ErrInvalid, input.AgentID)
	}
	if input.ExpectedCurrentVersion > 0 && input.ExpectedCurrentVersion != currentVersion {
		return Agent{}, fmt.Errorf("%w: Agent config changed from expected version %d to %d; retry against the latest config", ErrRevisionConflict, input.ExpectedCurrentVersion, currentVersion)
	}
	if !bytes.Equal(input.Skills, currentSkills) {
		normalizedSkills, normalizeErr := s.normalizeAgentSkills(agentSkillContext(ctx, agent), agent.WorkspaceID, input.Skills)
		if normalizeErr != nil {
			return Agent{}, normalizeErr
		}
		input.Skills = normalizedSkills
	}

	nextVersion := currentVersion + 1
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model, system, tools_json, mcp_json, skills_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, input.AgentID, nextVersion, input.LLMProvider, input.LLMModel, input.System, nullableRaw(input.Tools), nullableRaw(input.MCP), nullableRaw(input.Skills), now)
	if err != nil {
		return Agent{}, normalizeLLMReferenceWriteError(err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE agents
		SET current_config_version = $2
		WHERE id = $1
	`, input.AgentID, nextVersion)
	if err != nil {
		return Agent{}, err
	}

	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}

	agent.CurrentConfigVersion = nextVersion
	agent.ConfigVersion = AgentConfigVersion{
		Version:     nextVersion,
		LLMProvider: input.LLMProvider,
		LLMModel:    input.LLMModel,
		System:      input.System,
		Tools:       cloneRaw(input.Tools),
		MCP:         cloneRaw(input.MCP),
		Skills:      cloneRaw(input.Skills),
		CreatedAt:   now,
	}
	return agent, nil
}

func (s *PostgresStore) validateLLMProvider(id string) error {
	var enabled bool
	err := s.db.QueryRowContext(context.Background(), `
		SELECT enabled FROM llm_providers WHERE id = $1
	`, id).Scan(&enabled)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: llm provider %s", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w: llm provider %s is disabled", ErrInvalid, id)
	}
	return nil
}

func (s *PostgresStore) validateLLMSelection(providerID string, modelName string) error {
	if err := s.validateLLMProvider(providerID); err != nil {
		return err
	}
	var capabilityType string
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT capability_type FROM llm_models
		WHERE provider_id = $1 AND model = $2
	`, providerID, modelName).Scan(&capabilityType); err == sql.ErrNoRows {
		return fmt.Errorf("%w: llm model %s/%s", ErrNotFound, providerID, modelName)
	} else if err != nil {
		return err
	}
	if !LLMModelSupportsAgentRuntime(capabilityType) {
		return fmt.Errorf("%w: llm model %s/%s with capability_type %s cannot run an Agent", ErrInvalid, providerID, modelName, capabilityType)
	}
	return nil
}

func normalizeAgentMCP(raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := mcppkg.CanonicalJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return normalized, nil
}

func (s *PostgresStore) CreateEnvironment(input CreateEnvironmentInput) (Environment, error) {
	return s.createEnvironmentContext(context.Background(), input)
}

func (s *PostgresStore) createEnvironmentContext(ctx context.Context, input CreateEnvironmentInput) (Environment, error) {
	if input.Name == "" {
		return Environment{}, fmt.Errorf("%w: environment name is required", ErrInvalid)
	}

	workspaceID := defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if strings.TrimSpace(input.WorkspaceID) != "" && strings.TrimSpace(input.WorkspaceID) != scope.WorkspaceID {
			return Environment{}, fmt.Errorf("%w: environment workspace scope mismatch", ErrForbidden)
		}
		workspaceID = scope.WorkspaceID
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return Environment{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "env", "tma_environment_id_seq")
	if err != nil {
		return Environment{}, err
	}

	config := input.Config
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO environments (id, workspace_id, name, config_json, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, id, scope.WorkspaceID, input.Name, config, now)
	if err != nil {
		return Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Environment{}, err
	}

	return Environment{
		ID:          id,
		WorkspaceID: scope.WorkspaceID,
		Name:        input.Name,
		Config:      cloneRaw(config),
		CreatedAt:   now,
	}, nil
}

func (s *PostgresStore) CreateSession(input CreateSessionInput) (Session, error) {
	return s.createSessionContext(context.Background(), input)
}

func (s *PostgresStore) createSessionContext(ctx context.Context, input CreateSessionInput) (Session, error) {
	workspaceID := defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	ownerID := strings.TrimSpace(input.OwnerID)
	scope, hasScope := DatabaseAccessScopeFromContext(ctx)
	if hasScope {
		if strings.TrimSpace(input.WorkspaceID) != "" && strings.TrimSpace(input.WorkspaceID) != scope.WorkspaceID {
			return Session{}, fmt.Errorf("%w: session workspace scope mismatch", ErrForbidden)
		}
		if scope.OwnerID != "" && ownerID != "" && ownerID != scope.OwnerID {
			return Session{}, fmt.Errorf("%w: session owner scope mismatch", ErrForbidden)
		}
		workspaceID = scope.WorkspaceID
		if scope.OwnerID != "" {
			ownerID = scope.OwnerID
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
		return Session{}, err
	}
	input.WorkspaceID = workspaceID
	input.OwnerID = ownerID

	session, err := s.createSessionTx(ctx, tx, input)
	if err != nil {
		return Session{}, err
	}
	if hasScope && (session.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && session.OwnerID != scope.OwnerID)) {
		return Session{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *PostgresStore) CreateSubagentSession(input CreateSubagentSessionInput) (Session, error) {
	return s.createSubagentSessionContext(context.Background(), input)
}

func (s *PostgresStore) createSubagentSessionContext(ctx context.Context, input CreateSubagentSessionInput) (Session, error) {
	parentSessionID := strings.TrimSpace(input.Session.ParentSessionID)
	if parentSessionID == "" {
		return Session{}, fmt.Errorf("%w: parent_session_id is required", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()

	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return Session{}, err
	}
	workspaceID := scope.WorkspaceID
	if !scoped {
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id = $1`, parentSessionID).Scan(&workspaceID); err == sql.ErrNoRows {
			return Session{}, fmt.Errorf("%w: parent session %s", ErrNotFound, parentSessionID)
		} else if err != nil {
			return Session{}, err
		}
		if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
			return Session{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, workspaceID); err != nil {
		return Session{}, err
	}

	var parent Session
	if err := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, spawn_depth
		FROM sessions
		WHERE id = $1
		FOR UPDATE
	`, parentSessionID).Scan(&parent.ID, &parent.WorkspaceID, &parent.OwnerID, &parent.SpawnDepth); err == sql.ErrNoRows {
		return Session{}, fmt.Errorf("%w: parent session %s", ErrNotFound, parentSessionID)
	} else if err != nil {
		return Session{}, err
	}
	if err := authorizeSessionAccessScope(parent, scope, scoped); err != nil {
		return Session{}, err
	}
	if err := enforceSubagentLimitsTx(ctx, tx, parent, strings.TrimSpace(input.Session.ParentTurnID), input.Limits); err != nil {
		return Session{}, err
	}

	input.Session.WorkspaceID = parent.WorkspaceID
	input.Session.OwnerID = parent.OwnerID
	input.Session.ParentSessionID = parent.ID
	input.Session.SpawnDepth = parent.SpawnDepth + 1
	session, err := s.createSessionTx(ctx, tx, input.Session)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *PostgresStore) StartSubagentTurn(input StartSubagentTurnInput) ([]Event, error) {
	return s.startSubagentTurnContext(context.Background(), input)
}

func (s *PostgresStore) startSubagentTurnContext(ctx context.Context, input StartSubagentTurnInput) ([]Event, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session_id is required", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	workspaceID := scope.WorkspaceID
	if !scoped {
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id = $1`, sessionID).Scan(&workspaceID); err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: session %s", ErrNotFound, sessionID)
		} else if err != nil {
			return nil, err
		}
		if _, err := setDatabaseAccessScope(ctx, tx, workspaceID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, workspaceID); err != nil {
		return nil, err
	}

	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	if session.ParentSessionID == "" {
		return nil, fmt.Errorf("%w: session %s is not a subagent", ErrInvalid, sessionID)
	}
	if parentSessionID := strings.TrimSpace(input.ParentSessionID); parentSessionID != "" && session.ParentSessionID != parentSessionID {
		return nil, fmt.Errorf("%w: session %s is not a child of parent session %s", ErrInvalid, sessionID, parentSessionID)
	}
	if session.Status == SessionStatusTerminated {
		return nil, ErrTerminated
	}
	if scoped && scope.OwnerID != "" {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('tma.owner_id', '', true)`); err != nil {
			return nil, err
		}
	}
	if err := enforceSubagentActiveLimitsTx(ctx, tx, session, input.Limits); err != nil {
		return nil, err
	}

	events, err := s.applyEventTx(ctx, tx, &session, AppendEventInput{Type: EventUserMessage, Payload: cloneRaw(input.Payload)}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, session.Status); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, event := range events {
		s.hub.publish(event)
	}
	return events, nil
}

func (s *PostgresStore) EnqueueSubagentStart(input EnqueueSubagentStartInput) (SubagentStartRequest, error) {
	return s.EnqueueSubagentStartContext(context.Background(), input)
}

func (s *PostgresStore) EnqueueSubagentStartContext(ctx context.Context, input EnqueueSubagentStartInput) (SubagentStartRequest, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return SubagentStartRequest{}, fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	if err := s.expireSubagentStarts(context.Background(), 100); err != nil {
		return SubagentStartRequest{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	defer tx.Rollback()

	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SubagentStartRequest{}, err
	}
	if session.ParentSessionID == "" || session.Status != SessionStatusIdle || session.ArchivedAt != nil {
		return SubagentStartRequest{}, fmt.Errorf("%w: subagent session must be idle", ErrInvalid)
	}
	if parentID := strings.TrimSpace(input.ParentSessionID); parentID != "" && parentID != session.ParentSessionID {
		return SubagentStartRequest{}, fmt.Errorf("%w: session is not a child of the requested parent", ErrInvalid)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, session.WorkspaceID); err != nil {
		return SubagentStartRequest{}, err
	}
	now := time.Now().UTC()
	if existing, err := getPendingSubagentStartTx(ctx, tx, session.ID); err == nil {
		if err := tx.Commit(); err != nil {
			return SubagentStartRequest{}, err
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return SubagentStartRequest{}, err
	}
	if scoped && scope.OwnerID != "" {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('tma.owner_id', '', true)`); err != nil {
			return SubagentStartRequest{}, err
		}
	}
	if err := enforceSubagentQueueLimitsTx(ctx, tx, session, input.Limits); err != nil {
		return SubagentStartRequest{}, err
	}
	id, err := nextSequenceID(ctx, tx, "sreq", "tma_subagent_start_request_id_seq")
	if err != nil {
		return SubagentStartRequest{}, err
	}
	timeout := time.Duration(input.Limits.QueueTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 100 * 365 * 24 * time.Hour
	}
	request := SubagentStartRequest{
		ID: id, WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID, SessionID: session.ID,
		ParentSessionID: session.ParentSessionID, ParentTurnID: strings.TrimSpace(input.ParentTurnID),
		Payload: cloneRaw(input.Payload), Status: "pending", Priority: input.Priority, QueuedAt: now, ExpiresAt: now.Add(timeout),
		WaitSeconds: 0,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO subagent_start_requests (
			id, workspace_id, owner_id, session_id, parent_session_id, parent_turn_id, payload_json, status, priority,
			workspace_active_limit, user_active_limit, queued_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9, $10, $11, $12)
	`, request.ID, request.WorkspaceID, request.OwnerID, request.SessionID, request.ParentSessionID, request.ParentTurnID,
		request.Payload, request.Priority, input.Limits.WorkspaceActiveLimit, input.Limits.UserActiveLimit, request.QueuedAt, request.ExpiresAt)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentStartRequest{}, err
	}
	return request, nil
}

func (s *PostgresStore) GetPendingSubagentStart(sessionID string) (SubagentStartRequest, error) {
	return s.GetPendingSubagentStartContext(context.Background(), sessionID)
}

func (s *PostgresStore) GetPendingSubagentStartContext(ctx context.Context, sessionID string) (SubagentStartRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return SubagentStartRequest{}, err
	}
	row := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, session_id, parent_session_id, parent_turn_id, payload_json,
			status, priority, queued_at, expires_at, started_at, COALESCE(turn_id, '')
		FROM subagent_start_requests
		WHERE session_id = $1 AND status = 'pending' AND expires_at > CURRENT_TIMESTAMP
	`, strings.TrimSpace(sessionID))
	request, err := scanSubagentStartRequest(row)
	if err == sql.ErrNoRows {
		return SubagentStartRequest{}, ErrNotFound
	}
	if err != nil {
		return SubagentStartRequest{}, err
	}
	request.WaitSeconds = subagentStartWaitSeconds(request, time.Now().UTC())
	if err := tx.Commit(); err != nil {
		return SubagentStartRequest{}, err
	}
	return request, nil
}

func (s *PostgresStore) CancelSubagentStart(input CancelSubagentStartInput) (SubagentStartRequest, error) {
	return s.CancelSubagentStartContext(context.Background(), input)
}

func (s *PostgresStore) CancelSubagentStartContext(ctx context.Context, input CancelSubagentStartInput) (SubagentStartRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return SubagentStartRequest{}, err
	}
	request, err := getPendingSubagentStartTx(ctx, tx, strings.TrimSpace(input.SessionID))
	if err != nil {
		return SubagentStartRequest{}, err
	}
	if scope, scoped := DatabaseAccessScopeFromContext(ctx); scoped {
		if request.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && request.OwnerID != scope.OwnerID) {
			return SubagentStartRequest{}, ErrForbidden
		}
	}
	requestCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: request.WorkspaceID, OwnerID: request.OwnerID})
	if err != nil {
		return SubagentStartRequest{}, err
	}
	if _, err := setDatabaseAccessScope(requestCtx, tx, request.WorkspaceID); err != nil {
		return SubagentStartRequest{}, err
	}
	if parentID := strings.TrimSpace(input.ParentSessionID); parentID != "" && request.ParentSessionID != parentID {
		return SubagentStartRequest{}, fmt.Errorf("%w: queued start does not belong to the requested parent", ErrInvalid)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, request.WorkspaceID); err != nil {
		return SubagentStartRequest{}, err
	}
	if _, err := getSessionForUpdateTx(ctx, tx, request.SessionID); err != nil {
		return SubagentStartRequest{}, err
	}
	now := time.Now().UTC()
	reason := defaultString(strings.TrimSpace(input.Reason), "canceled by parent agent")
	if _, err := tx.ExecContext(ctx, `UPDATE subagent_start_requests SET status = 'canceled', canceled_at = $2, cancel_reason = $3 WHERE id = $1 AND status = 'pending'`, request.ID, now, reason); err != nil {
		return SubagentStartRequest{}, err
	}
	waitSeconds := subagentStartWaitSeconds(request, now)
	payload, _ := json.Marshal(map[string]any{
		"request_id":        request.ID,
		"session_id":        request.SessionID,
		"parent_session_id": request.ParentSessionID,
		"reason":            reason,
		"canceled_at":       now,
		"wait_seconds":      waitSeconds,
	})
	event, err := s.appendEventTx(ctx, tx, request.SessionID, EventRuntimeSubagentStartCanceled, payload, now)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentStartRequest{}, err
	}
	request.Status, request.CanceledAt, request.CancelReason, request.WaitSeconds = "canceled", &now, reason, waitSeconds
	s.hub.publish(event)
	return request, nil
}

func (s *PostgresStore) PromoteSubagentStarts(input PromoteSubagentStartsInput) ([]SubagentStartPromotion, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	ctx := context.Background()
	if err := s.expireSubagentStarts(ctx, limit*20); err != nil {
		return nil, err
	}
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, "")
	if err != nil {
		return nil, err
	}
	workspaceIDs = s.rotateSessionTurnClaimWorkspaces(workspaceIDs)
	promotions := make([]SubagentStartPromotion, 0, limit)
	for _, workspaceID := range workspaceIDs {
		remaining := limit - len(promotions)
		if remaining <= 0 {
			break
		}
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		items, err := s.promoteSubagentStartsWorkspace(workspaceCtx, remaining)
		if err != nil {
			return nil, err
		}
		promotions = append(promotions, items...)
	}
	return promotions, nil
}

func (s *PostgresStore) promoteSubagentStartsWorkspace(ctx context.Context, limit int) ([]SubagentStartPromotion, error) {
	candidateLimit := limit * 50
	if candidateLimit < 20 {
		candidateLimit = 20
	}
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: subagent promotion workspace scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, owner_id, priority, queued_at FROM subagent_start_requests
		WHERE status = 'pending'
		ORDER BY priority DESC, queued_at ASC
		LIMIT $1
	`, candidateLimit)
	if err != nil {
		return nil, err
	}
	candidates := make([]subagentPromotionCandidate, 0, candidateLimit)
	for rows.Next() {
		var candidate subagentPromotionCandidate
		if err := rows.Scan(&candidate.ID, &candidate.WorkspaceID, &candidate.OwnerID, &candidate.Priority, &candidate.QueuedAt); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	ids := fairSubagentPromotionOrder(candidates, candidateLimit)
	promotions := make([]SubagentStartPromotion, 0, limit)
	for _, id := range ids {
		promotion, promoted, err := s.promoteSubagentStart(ctx, id)
		if err != nil {
			return nil, err
		}
		if promoted {
			promotions = append(promotions, promotion)
		}
		if len(promotions) >= limit {
			break
		}
	}
	return promotions, nil
}

func (s *PostgresStore) expireSubagentStarts(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, "")
	if err != nil {
		return err
	}
	for _, workspaceID := range workspaceIDs {
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return err
		}
		if err := s.expireSubagentStartsWorkspace(workspaceCtx, limit); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) expireSubagentStartsWorkspace(ctx context.Context, limit int) error {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: subagent expiration workspace scope is required", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM subagent_start_requests WHERE status = 'pending' AND expires_at <= CURRENT_TIMESTAMP ORDER BY expires_at ASC LIMIT $1`, limit)
	if err != nil {
		return err
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
			tx.Rollback()
			return err
		}
		request, _, _, err := getSubagentStartForUpdateTx(ctx, tx, id)
		if errors.Is(err, ErrNotFound) {
			tx.Rollback()
			continue
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if request.Status != "pending" || request.ExpiresAt.After(time.Now().UTC()) {
			tx.Rollback()
			continue
		}
		requestCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: request.WorkspaceID, OwnerID: request.OwnerID})
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := setDatabaseAccessScope(requestCtx, tx, request.WorkspaceID); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := getSessionForUpdateTx(ctx, tx, request.SessionID); err != nil {
			tx.Rollback()
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `UPDATE subagent_start_requests SET status = 'expired' WHERE id = $1 AND status = 'pending'`, request.ID); err != nil {
			tx.Rollback()
			return err
		}
		waitSeconds := subagentStartWaitSeconds(request, now)
		payload, _ := json.Marshal(map[string]any{
			"request_id":        request.ID,
			"session_id":        request.SessionID,
			"parent_session_id": request.ParentSessionID,
			"queued_at":         request.QueuedAt,
			"expired_at":        now,
			"wait_seconds":      waitSeconds,
		})
		event, err := s.appendEventTx(ctx, tx, request.SessionID, EventRuntimeSubagentStartExpired, payload, now)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.hub.publish(event)
	}
	return nil
}

func (s *PostgresStore) promoteSubagentStart(ctx context.Context, id string) (SubagentStartPromotion, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubagentStartPromotion{}, false, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	request, workspaceLimit, userLimit, err := getSubagentStartForUpdateTx(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return SubagentStartPromotion{}, false, nil
	}
	if err != nil {
		return SubagentStartPromotion{}, false, err
	}
	if request.Status != "pending" {
		return SubagentStartPromotion{}, false, nil
	}
	requestCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: request.WorkspaceID, OwnerID: request.OwnerID})
	if err != nil {
		return SubagentStartPromotion{}, false, err
	}
	if _, err := setDatabaseAccessScope(requestCtx, tx, request.WorkspaceID); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, request.WorkspaceID); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, request.SessionID)
	if err != nil || session.Status != SessionStatusIdle || session.ArchivedAt != nil {
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE subagent_start_requests SET status = 'canceled' WHERE id = $1`, request.ID)
		}
		if err != nil {
			return SubagentStartPromotion{}, false, err
		}
		return SubagentStartPromotion{}, false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('tma.owner_id', '', true)`); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	if err := enforceSubagentActiveLimitsTx(ctx, tx, session, SubagentLimits{WorkspaceActiveLimit: workspaceLimit, UserActiveLimit: userLimit}); err != nil {
		var violation SubagentQuotaViolation
		if errors.As(err, &violation) {
			return SubagentStartPromotion{}, false, nil
		}
		return SubagentStartPromotion{}, false, err
	}
	events, err := s.applyEventTx(ctx, tx, &session, AppendEventInput{Type: EventUserMessage, Payload: request.Payload}, time.Now().UTC())
	if err != nil {
		return SubagentStartPromotion{}, false, err
	}
	turnID := ""
	for _, event := range events {
		if event.Type == EventUserMessage {
			turnID = payloadString(event.Payload, "turn_id")
		}
	}
	waitSeconds := subagentStartWaitSeconds(request, time.Now().UTC())
	dequeuedPayload, _ := json.Marshal(map[string]any{
		"request_id":        request.ID,
		"session_id":        request.SessionID,
		"parent_session_id": request.ParentSessionID,
		"queued_at":         request.QueuedAt,
		"turn_id":           turnID,
		"wait_seconds":      waitSeconds,
	})
	dequeued, err := s.appendEventTx(ctx, tx, session.ID, EventRuntimeSubagentStartDequeued, dequeuedPayload, time.Now().UTC())
	if err != nil {
		return SubagentStartPromotion{}, false, err
	}
	events = append(events, dequeued)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, session.Status); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subagent_start_requests SET status = 'started', started_at = $2, turn_id = $3 WHERE id = $1`, request.ID, now, turnID); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentStartPromotion{}, false, err
	}
	request.Status, request.StartedAt, request.TurnID, request.WaitSeconds = "started", &now, turnID, waitSeconds
	for _, event := range events {
		s.hub.publish(event)
	}
	return SubagentStartPromotion{Request: request, Events: events}, true, nil
}

func (s *PostgresStore) GetSubagentMetrics(input GetSubagentMetricsInput) (SubagentMetrics, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	metrics := SubagentMetrics{WorkspaceID: workspaceID}
	ctx := context.Background()
	queryer := interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}(s.db)
	var tx *sql.Tx
	if workspaceID != "" {
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return SubagentMetrics{}, err
		}
		tx, _, err = s.beginDatabaseAccessScope(workspaceCtx, workspaceID)
		if err != nil {
			return SubagentMetrics{}, err
		}
		defer tx.Rollback()
		ctx = workspaceCtx
		queryer = tx
	}

	pendingQuery := `SELECT COUNT(*), COALESCE(EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - MIN(queued_at))), 0) FROM subagent_start_requests WHERE status = 'pending'`
	pendingArgs := []any{}
	if workspaceID != "" {
		pendingQuery += ` AND workspace_id = $1`
		pendingArgs = append(pendingArgs, workspaceID)
	}
	if err := queryer.QueryRowContext(ctx, pendingQuery, pendingArgs...).Scan(&metrics.Queued, &metrics.WaitSeconds); err != nil {
		return SubagentMetrics{}, err
	}

	runningQuery := `SELECT COUNT(*) FROM sessions WHERE parent_session_id IS NOT NULL AND status = $1 AND archived_at IS NULL`
	runningArgs := []any{SessionStatusRunning}
	if workspaceID != "" {
		runningQuery += ` AND workspace_id = $2`
		runningArgs = append(runningArgs, workspaceID)
	}
	if err := queryer.QueryRowContext(ctx, runningQuery, runningArgs...).Scan(&metrics.Running); err != nil {
		return SubagentMetrics{}, err
	}

	rejectedQuery := `SELECT COUNT(*) FROM session_events e JOIN sessions s ON s.id = e.session_id WHERE e.type = $1`
	rejectedArgs := []any{EventRuntimeSubagentStartRejected}
	if workspaceID != "" {
		rejectedQuery += ` AND s.workspace_id = $2`
		rejectedArgs = append(rejectedArgs, workspaceID)
	}
	if err := queryer.QueryRowContext(ctx, rejectedQuery, rejectedArgs...).Scan(&metrics.Rejected); err != nil {
		return SubagentMetrics{}, err
	}

	if metrics.WaitSeconds < 0 {
		metrics.WaitSeconds = 0
	}
	return metrics, nil
}

func (s *PostgresStore) CreateSubagentTaskGroup(input CreateSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	strategy := normalizeSubagentTaskGroupStrategy(input.Strategy)
	if strategy == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: unsupported task group strategy %q", ErrInvalid, input.Strategy)
	}
	reducer := normalizeSubagentTaskGroupReducer(input.ResultReducer)
	if reducer == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: unsupported task group reducer %q", ErrInvalid, input.ResultReducer)
	}
	if strings.TrimSpace(input.ParentSessionID) == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: parent_session_id is required", ErrInvalid)
	}
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.OwnerID) == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: workspace_id and owner_id are required", ErrInvalid)
	}
	if input.PlannedCount <= 0 {
		return SubagentTaskGroup{}, fmt.Errorf("%w: planned_count must be positive", ErrInvalid)
	}
	if strategy == SubagentTaskGroupStrategyQuorum && (input.Quorum <= 0 || input.Quorum > input.PlannedCount) {
		return SubagentTaskGroup{}, fmt.Errorf("%w: quorum must be between 1 and planned_count", ErrInvalid)
	}

	ctx := context.Background()
	id, err := nextSequenceID(ctx, s.db, "sgrp", "tma_subagent_task_group_id_seq")
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	group := SubagentTaskGroup{
		ID:              id,
		WorkspaceID:     strings.TrimSpace(input.WorkspaceID),
		OwnerID:         strings.TrimSpace(input.OwnerID),
		ParentSessionID: strings.TrimSpace(input.ParentSessionID),
		ParentTurnID:    strings.TrimSpace(input.ParentTurnID),
		ParentGroupID:   strings.TrimSpace(input.ParentGroupID),
		ParentItemIndex: input.ParentItemIndex,
		Strategy:        strategy,
		ResultReducer:   reducer,
		Quorum:          input.Quorum,
		FailFast:        input.FailFast,
		PlannedCount:    input.PlannedCount,
		CreatedAt:       time.Now().UTC(),
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO subagent_task_groups (
			id, workspace_id, owner_id, parent_session_id, parent_turn_id, parent_group_id, parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, cancel_reason
		) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, '')
	`, group.ID, group.WorkspaceID, group.OwnerID, group.ParentSessionID, group.ParentTurnID, group.ParentGroupID, group.ParentItemIndex, group.Strategy, group.ResultReducer, group.Quorum, group.FailFast, group.PlannedCount, group.CreatedAt); err != nil {
		return SubagentTaskGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) AppendSubagentTaskGroupItem(groupID string, input AppendSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: group_id is required", ErrInvalid)
	}
	if input.ItemIndex < 0 {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: item_index must be non-negative", ErrInvalid)
	}
	if strings.TrimSpace(input.AgentID) == "" || strings.TrimSpace(input.EnvironmentID) == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: agent_id and environment_id are required", ErrInvalid)
	}
	state := normalizeSubagentTaskGroupItemState(input.InitialState)
	if state == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: unsupported initial_state %q", ErrInvalid, input.InitialState)
	}
	item := SubagentTaskGroupItem{
		GroupID:              groupID,
		ItemIndex:            input.ItemIndex,
		AgentID:              strings.TrimSpace(input.AgentID),
		EnvironmentID:        strings.TrimSpace(input.EnvironmentID),
		SessionID:            strings.TrimSpace(input.SessionID),
		Title:                strings.TrimSpace(input.Title),
		Message:              strings.TrimSpace(input.Message),
		Priority:             input.Priority,
		InitialState:         state,
		ErrorType:            strings.TrimSpace(input.ErrorType),
		ErrorMessage:         strings.TrimSpace(input.ErrorMessage),
		ExpectedResultSchema: cloneRaw(input.ExpectedResultSchema),
		RetryCount:           0,
		CreatedAt:            time.Now().UTC(),
	}
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO subagent_task_group_items (
			group_id, item_index, agent_id, environment_id, session_id, title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, item.GroupID, item.ItemIndex, item.AgentID, item.EnvironmentID, item.SessionID, item.Title, item.Message, item.Priority, item.InitialState, item.ErrorType, item.ErrorMessage, metadataJSON(item.ExpectedResultSchema), item.RetryCount, item.CreatedAt); err != nil {
		return SubagentTaskGroupItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateSubagentTaskGroupItem(groupID string, itemIndex int, input UpdateSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: group_id is required", ErrInvalid)
	}
	if itemIndex < 0 {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: item_index must be non-negative", ErrInvalid)
	}
	state := normalizeSubagentTaskGroupItemState(input.InitialState)
	if state == "" {
		return SubagentTaskGroupItem{}, fmt.Errorf("%w: unsupported initial_state %q", ErrInvalid, input.InitialState)
	}
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE subagent_task_group_items
		SET
			session_id = NULLIF($3, ''),
			title = $4,
			message = $5,
			priority = $6,
			initial_state = $7,
			error_type = $8,
			error_message = $9,
			expected_result_schema = $10,
			retry_count = retry_count + CASE WHEN $11 THEN 1 ELSE 0 END,
			created_at = $12
		WHERE group_id = $1 AND item_index = $2
		RETURNING group_id, item_index, agent_id, environment_id, COALESCE(session_id, ''), title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
	`, groupID, itemIndex, strings.TrimSpace(input.SessionID), strings.TrimSpace(input.Title), strings.TrimSpace(input.Message), input.Priority, state, strings.TrimSpace(input.ErrorType), strings.TrimSpace(input.ErrorMessage), metadataJSON(input.ExpectedResultSchema), input.IncrementRetry, time.Now().UTC())
	item, err := scanSubagentTaskGroupItem(row)
	if err == sql.ErrNoRows {
		return SubagentTaskGroupItem{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) GetSubagentTaskGroup(id string) (SubagentTaskGroup, error) {
	row := s.db.QueryRowContext(context.Background(), `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups
		WHERE id = $1
	`, strings.TrimSpace(id))
	group, err := scanSubagentTaskGroup(row)
	if err == sql.ErrNoRows {
		return SubagentTaskGroup{}, ErrNotFound
	}
	return group, err
}

func (s *PostgresStore) ListSubagentTaskGroupsByParentSession(parentSessionID string) ([]SubagentTaskGroup, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups
		WHERE parent_session_id = $1
		ORDER BY created_at DESC, id DESC
	`, strings.TrimSpace(parentSessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]SubagentTaskGroup, 0)
	for rows.Next() {
		group, err := scanSubagentTaskGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (s *PostgresStore) GetSubagentTaskGroupItemBySession(sessionID string) (SubagentTaskGroupItem, error) {
	row := s.db.QueryRowContext(context.Background(), `
		SELECT group_id, item_index, agent_id, environment_id, COALESCE(session_id, ''), title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
		FROM subagent_task_group_items
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, strings.TrimSpace(sessionID))
	item, err := scanSubagentTaskGroupItem(row)
	if err == sql.ErrNoRows {
		return SubagentTaskGroupItem{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) ListSubagentTaskGroupItems(groupID string) ([]SubagentTaskGroupItem, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT group_id, item_index, agent_id, environment_id, COALESCE(session_id, ''), title, message, priority, initial_state, error_type, error_message, expected_result_schema, retry_count, created_at
		FROM subagent_task_group_items
		WHERE group_id = $1
		ORDER BY item_index ASC
	`, strings.TrimSpace(groupID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]SubagentTaskGroupItem, 0)
	for rows.Next() {
		item, err := scanSubagentTaskGroupItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) ListChildSubagentTaskGroups(parentGroupID string, parentItemIndex int) ([]SubagentTaskGroup, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups
		WHERE parent_group_id = $1 AND parent_item_index = $2
		ORDER BY created_at ASC, id ASC
	`, strings.TrimSpace(parentGroupID), parentItemIndex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]SubagentTaskGroup, 0)
	for rows.Next() {
		group, err := scanSubagentTaskGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (s *PostgresStore) CancelSubagentTaskGroup(input CancelSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	return s.CancelSubagentTaskGroupContext(context.Background(), input)
}

func (s *PostgresStore) CancelSubagentTaskGroupContext(ctx context.Context, input CancelSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	groupID := strings.TrimSpace(input.GroupID)
	if groupID == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: group_id is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return SubagentTaskGroup{}, err
	}

	groupRow := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups
		WHERE id = $1
		FOR UPDATE
	`, groupID)
	group, err := scanSubagentTaskGroup(groupRow)
	if err == sql.ErrNoRows {
		return SubagentTaskGroup{}, ErrNotFound
	}
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	if parentID := strings.TrimSpace(input.ParentSessionID); parentID != "" && group.ParentSessionID != parentID {
		return SubagentTaskGroup{}, fmt.Errorf("%w: task group does not belong to the requested parent session", ErrInvalid)
	}
	if group.CanceledAt != nil {
		return group, nil
	}

	now := time.Now().UTC()
	reason := defaultString(strings.TrimSpace(input.Reason), "canceled by parent agent")
	if _, err := tx.ExecContext(ctx, `
		UPDATE subagent_task_groups
		SET canceled_at = $2, cancel_reason = $3
		WHERE id = $1
	`, group.ID, now, reason); err != nil {
		return SubagentTaskGroup{}, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT COALESCE(session_id, '')
		FROM subagent_task_group_items
		WHERE group_id = $1
		ORDER BY item_index ASC
	`, group.ID)
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	sessionIDs := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return SubagentTaskGroup{}, err
		}
		if sessionID != "" {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SubagentTaskGroup{}, err
	}
	if err := rows.Close(); err != nil {
		return SubagentTaskGroup{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentTaskGroup{}, err
	}

	for _, sessionID := range sessionIDs {
		if _, err := ArchiveSessionWithContext(ctx, s, sessionID); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrTerminated) {
			return SubagentTaskGroup{}, err
		}
	}
	group.CanceledAt = &now
	group.CancelReason = reason
	return group, nil
}

func (s *PostgresStore) ReactivateSubagentTaskGroup(input ReactivateSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	return s.ReactivateSubagentTaskGroupContext(context.Background(), input)
}

func (s *PostgresStore) ReactivateSubagentTaskGroupContext(ctx context.Context, input ReactivateSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	groupID := strings.TrimSpace(input.GroupID)
	if groupID == "" {
		return SubagentTaskGroup{}, fmt.Errorf("%w: group_id is required", ErrInvalid)
	}
	tx, err := s.beginTaskGroupScopeTx(ctx)
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		UPDATE subagent_task_groups
		SET canceled_at = NULL, cancel_reason = ''
		WHERE id = $1 AND ($2 = '' OR parent_session_id = $2)
		RETURNING id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
	`, groupID, strings.TrimSpace(input.ParentSessionID))
	group, err := scanSubagentTaskGroup(row)
	if err == sql.ErrNoRows {
		return SubagentTaskGroup{}, ErrNotFound
	}
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubagentTaskGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) GetSubagentTaskGroupMetrics(input GetSubagentTaskGroupMetricsInput) (SubagentTaskGroupMetrics, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	metrics := SubagentTaskGroupMetrics{WorkspaceID: workspaceID}
	groups, err := s.listSubagentTaskGroups(workspaceID)
	if err != nil {
		return SubagentTaskGroupMetrics{}, err
	}
	for _, group := range groups {
		groupCtx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: group.WorkspaceID})
		if err != nil {
			return SubagentTaskGroupMetrics{}, err
		}
		items, err := ListSubagentTaskGroupItemsWithContext(groupCtx, s, group.ID)
		if err != nil {
			return SubagentTaskGroupMetrics{}, err
		}
		itemStatuses := make([]string, 0, len(items))
		for _, item := range items {
			switch item.InitialState {
			case SubagentTaskGroupItemStateCreated:
				metrics.ItemCreated++
			case SubagentTaskGroupItemStateStarted:
				metrics.ItemStarted++
			case SubagentTaskGroupItemStateQueued:
				metrics.ItemQueued++
			case SubagentTaskGroupItemStateRejected:
				metrics.ItemRejected++
			}
			itemStatuses = append(itemStatuses, s.taskGroupItemStatusForMetrics(groupCtx, item))
		}
		switch s.taskGroupStatusForMetrics(group, itemStatuses) {
		case "pending":
			metrics.Pending++
		case "running":
			metrics.Running++
		case "completed":
			metrics.Completed++
		case "failed":
			metrics.Failed++
		case "canceled":
			metrics.Canceled++
		}
	}
	return metrics, nil
}

func (s *PostgresStore) ReapOrphanSubagents(input ReapOrphanSubagentsInput) ([]ReapedSubagent, error) {
	limit := reapLimit(input.Limit)
	workspaceIDs, err := s.listTenantWorkspaceIDs(context.Background(), strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return nil, err
	}
	reaped := make([]ReapedSubagent, 0, limit)
	for _, workspaceID := range workspaceIDs {
		remaining := limit - len(reaped)
		if remaining <= 0 {
			break
		}
		workspaceCtx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		ids, err := s.listOrphanSubagentIDs(workspaceCtx, workspaceID, remaining)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			item, ok, err := s.reapOrphanSubagent(workspaceCtx, id)
			if err != nil {
				return nil, err
			}
			if ok {
				reaped = append(reaped, item)
			}
		}
	}
	return reaped, nil
}

func (s *PostgresStore) listTenantWorkspaceIDs(ctx context.Context, requestedWorkspaceID string) ([]string, error) {
	if requestedWorkspaceID != "" {
		return []string{requestedWorkspaceID}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM tma_list_workspace_ids()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workspaceIDs := make([]string, 0)
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	return workspaceIDs, rows.Err()
}

func (s *PostgresStore) listOrphanSubagentIDs(ctx context.Context, workspaceID string, limit int) ([]string, error) {
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT child.id
		FROM sessions AS child
		LEFT JOIN sessions AS parent ON parent.id = child.parent_session_id
		WHERE child.archived_at IS NULL
			AND child.workspace_id = $1
			AND child.spawn_depth > 0
			AND child.status <> $2
			AND (
				child.parent_session_id IS NULL
				OR parent.id IS NULL
				OR parent.archived_at IS NOT NULL
				OR parent.status = $2
			)
		ORDER BY child.created_at ASC, child.id ASC
		LIMIT $3
	`, workspaceID, SessionStatusTerminated, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) createSessionTx(ctx context.Context, tx *sql.Tx, input CreateSessionInput) (Session, error) {
	agentID := input.AgentID
	if agentID == "" {
		agentID = input.Agent
	}
	if agentID == "" {
		return Session{}, fmt.Errorf("%w: agent_id is required", ErrInvalid)
	}
	if input.EnvironmentID == "" {
		return Session{}, fmt.Errorf("%w: environment_id is required", ErrInvalid)
	}
	if input.SpawnDepth < 0 {
		return Session{}, fmt.Errorf("%w: spawn_depth must be non-negative", ErrInvalid)
	}

	var agentWorkspaceID string
	var agentConfigVersion int
	err := tx.QueryRowContext(ctx, `
		SELECT workspace_id, current_config_version FROM agents WHERE id = $1 AND archived_at IS NULL
	`, agentID).Scan(&agentWorkspaceID, &agentConfigVersion)
	if err == sql.ErrNoRows {
		return Session{}, fmt.Errorf("%w: agent %s", ErrNotFound, agentID)
	}
	if err != nil {
		return Session{}, err
	}
	if input.AgentConfigVersion > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM agent_config_versions WHERE agent_id = $1 AND version = $2
			)
		`, agentID, input.AgentConfigVersion).Scan(&exists); err != nil {
			return Session{}, err
		}
		if !exists {
			return Session{}, fmt.Errorf("%w: agent config version %s#%d", ErrNotFound, agentID, input.AgentConfigVersion)
		}
		agentConfigVersion = input.AgentConfigVersion
	}

	var environmentWorkspaceID string
	err = tx.QueryRowContext(ctx, `
		SELECT workspace_id FROM environments WHERE id = $1 AND archived_at IS NULL
	`, input.EnvironmentID).Scan(&environmentWorkspaceID)
	if err == sql.ErrNoRows {
		return Session{}, fmt.Errorf("%w: environment %s", ErrNotFound, input.EnvironmentID)
	}
	if err != nil {
		return Session{}, err
	}

	workspaceID := defaultString(input.WorkspaceID, agentWorkspaceID)
	if workspaceID != agentWorkspaceID || workspaceID != environmentWorkspaceID {
		return Session{}, fmt.Errorf("%w: workspace mismatch", ErrInvalid)
	}
	if parentSessionID := strings.TrimSpace(input.ParentSessionID); parentSessionID != "" {
		var parentWorkspaceID string
		err = tx.QueryRowContext(ctx, `
			SELECT workspace_id FROM sessions WHERE id = $1
		`, parentSessionID).Scan(&parentWorkspaceID)
		if err == sql.ErrNoRows {
			return Session{}, fmt.Errorf("%w: parent session %s", ErrNotFound, parentSessionID)
		}
		if err != nil {
			return Session{}, err
		}
		if parentWorkspaceID != workspaceID {
			return Session{}, fmt.Errorf("%w: parent session workspace mismatch", ErrInvalid)
		}
	}

	id, err := nextSequenceID(ctx, tx, "sesn", "tma_session_id_seq")
	if err != nil {
		return Session{}, err
	}

	now := time.Now().UTC()
	session := Session{
		ID:                      id,
		WorkspaceID:             workspaceID,
		OwnerID:                 defaultString(input.OwnerID, defaultString(input.CreatedBy, "system")),
		AgentID:                 agentID,
		AgentConfigVersion:      agentConfigVersion,
		EnvironmentID:           input.EnvironmentID,
		ParentSessionID:         strings.TrimSpace(input.ParentSessionID),
		ParentTurnID:            strings.TrimSpace(input.ParentTurnID),
		SpawnDepth:              input.SpawnDepth,
		Status:                  SessionStatusIdle,
		Title:                   input.Title,
		RuntimeSettings:         json.RawMessage(`{}`),
		RuntimeSettingsRevision: 1,
		Tags:                    []string{},
		CreatedBy:               defaultString(input.CreatedBy, "system"),
		CreatedAt:               now,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, parent_session_id, parent_turn_id, spawn_depth, status, title, runtime_settings_json, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, session.ID, session.WorkspaceID, session.OwnerID, session.AgentID, session.AgentConfigVersion, session.EnvironmentID, nullableString(session.ParentSessionID), nullableString(session.ParentTurnID), session.SpawnDepth, session.Status, nullableString(session.Title), session.RuntimeSettings, session.CreatedBy, session.CreatedAt)
	if err != nil {
		return Session{}, normalizeLLMReferenceWriteError(err)
	}

	if _, err := s.appendEventTx(ctx, tx, id, EventSessionStatusProvisioning, mustRaw(`{"status":"provisioning"}`), now); err != nil {
		return Session{}, err
	}
	if _, err := s.appendEventTx(ctx, tx, id, EventSessionStatusIdle, mustRaw(`{"status":"idle"}`), now); err != nil {
		return Session{}, err
	}

	return session, nil
}

func enforceSubagentLimitsTx(ctx context.Context, tx *sql.Tx, parent Session, parentTurnID string, limits SubagentLimits) error {
	if limits.MaxDepth > 0 && parent.SpawnDepth >= limits.MaxDepth {
		return newSubagentQuotaViolation("subagent_depth_limit", "subagent spawn depth limit reached", map[string]any{
			"scope": "session_tree", "policy": "max_depth", "current_depth": parent.SpawnDepth + 1, "limit": limits.MaxDepth, "session_id": parent.ID,
		})
	}
	checks := []struct {
		limit     int
		errorType string
		message   string
		state     map[string]any
		query     string
		args      []any
		counter   string
	}{
		{limits.MaxChildrenPerTurn, "subagent_turn_fanout_limit", "subagent spawn limit reached for parent turn", map[string]any{"scope": "parent_turn", "policy": "max_children_per_turn", "parent_session_id": parent.ID, "parent_turn_id": parentTurnID}, `SELECT COUNT(*) FROM sessions WHERE parent_session_id = $1 AND parent_turn_id = $2`, []any{parent.ID, parentTurnID}, "current_children"},
		{limits.MaxChildrenPerSession, "subagent_session_children_limit", "subagent session child limit reached", map[string]any{"scope": "parent_session", "policy": "max_children_per_session", "parent_session_id": parent.ID}, `SELECT COUNT(*) FROM sessions WHERE parent_session_id = $1`, []any{parent.ID}, "current_children"},
	}
	for _, check := range checks {
		if check.limit <= 0 {
			continue
		}
		var current int
		if err := tx.QueryRowContext(ctx, check.query, check.args...).Scan(&current); err != nil {
			return err
		}
		if current >= check.limit {
			check.state[check.counter] = current
			check.state["limit"] = check.limit
			return newSubagentQuotaViolation(check.errorType, check.message, check.state)
		}
	}
	return nil
}

func enforceSubagentActiveLimitsTx(ctx context.Context, tx *sql.Tx, session Session, limits SubagentLimits) error {
	checks := []struct {
		limit     int
		errorType string
		message   string
		state     map[string]any
		query     string
		args      []any
	}{
		{limits.WorkspaceActiveLimit, "subagent_workspace_active_limit", "workspace subagent active limit reached", map[string]any{"scope": "workspace", "policy": "workspace_active_limit", "workspace_id": session.WorkspaceID}, `SELECT COUNT(*) FROM sessions WHERE workspace_id = $1 AND parent_session_id IS NOT NULL AND status = $2 AND archived_at IS NULL AND id <> $3`, []any{session.WorkspaceID, SessionStatusRunning, session.ID}},
		{limits.UserActiveLimit, "subagent_user_active_limit", "user subagent active limit reached", map[string]any{"scope": "owner", "policy": "user_active_limit", "workspace_id": session.WorkspaceID, "owner_id": session.OwnerID}, `SELECT COUNT(*) FROM sessions WHERE workspace_id = $1 AND owner_id = $2 AND parent_session_id IS NOT NULL AND status = $3 AND archived_at IS NULL AND id <> $4`, []any{session.WorkspaceID, session.OwnerID, SessionStatusRunning, session.ID}},
	}
	for _, check := range checks {
		if check.limit <= 0 {
			continue
		}
		var current int
		if err := tx.QueryRowContext(ctx, check.query, check.args...).Scan(&current); err != nil {
			return err
		}
		if current >= check.limit {
			check.state["current_active"] = current
			check.state["limit"] = check.limit
			check.state["subagent_session_id"] = session.ID
			return newSubagentQuotaViolation(check.errorType, check.message, check.state)
		}
	}
	return nil
}

func enforceSubagentQueueLimitsTx(ctx context.Context, tx *sql.Tx, session Session, limits SubagentLimits) error {
	checks := []struct {
		limit     int
		errorType string
		message   string
		state     map[string]any
		query     string
		args      []any
	}{
		{limits.WorkspaceQueuedLimit, "subagent_workspace_queue_limit", "workspace subagent queue limit reached", map[string]any{"scope": "workspace", "policy": "workspace_queue_limit", "workspace_id": session.WorkspaceID}, `SELECT COUNT(*) FROM subagent_start_requests WHERE workspace_id = $1 AND status = 'pending'`, []any{session.WorkspaceID}},
		{limits.UserQueuedLimit, "subagent_user_queue_limit", "user subagent queue limit reached", map[string]any{"scope": "owner", "policy": "user_queue_limit", "workspace_id": session.WorkspaceID, "owner_id": session.OwnerID}, `SELECT COUNT(*) FROM subagent_start_requests WHERE workspace_id = $1 AND owner_id = $2 AND status = 'pending'`, []any{session.WorkspaceID, session.OwnerID}},
	}
	for _, check := range checks {
		if check.limit <= 0 {
			continue
		}
		var current int
		if err := tx.QueryRowContext(ctx, check.query, check.args...).Scan(&current); err != nil {
			return err
		}
		if current >= check.limit {
			check.state["current_queued"] = current
			check.state["limit"] = check.limit
			check.state["subagent_session_id"] = session.ID
			return newSubagentQuotaViolation(check.errorType, check.message, check.state)
		}
	}
	return nil
}

func getPendingSubagentStartTx(ctx context.Context, tx *sql.Tx, sessionID string) (SubagentStartRequest, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, session_id, parent_session_id, parent_turn_id, payload_json,
			status, priority, queued_at, expires_at, started_at, COALESCE(turn_id, '')
		FROM subagent_start_requests
		WHERE session_id = $1 AND status = 'pending'
		FOR UPDATE
	`, sessionID)
	request, err := scanSubagentStartRequest(row)
	if err == sql.ErrNoRows {
		return SubagentStartRequest{}, ErrNotFound
	}
	return request, err
}

func listPendingSubagentStartsForArchiveTx(ctx context.Context, tx *sql.Tx, sessionID string) ([]SubagentStartRequest, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, owner_id, session_id, parent_session_id, parent_turn_id, payload_json,
			status, priority, queued_at, expires_at, started_at, COALESCE(turn_id, '')
		FROM subagent_start_requests
		WHERE status = 'pending' AND (session_id = $1 OR parent_session_id = $1)
		FOR UPDATE
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]SubagentStartRequest, 0)
	for rows.Next() {
		request, err := scanSubagentStartRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return requests, nil
}

func getSubagentStartForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (SubagentStartRequest, int, int, error) {
	var request SubagentStartRequest
	var payload []byte
	var startedAt sql.NullTime
	var turnID sql.NullString
	var workspaceLimit int
	var userLimit int
	err := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, owner_id, session_id, parent_session_id, parent_turn_id, payload_json,
			status, priority, queued_at, expires_at, started_at, turn_id, workspace_active_limit, user_active_limit
		FROM subagent_start_requests
		WHERE id = $1
		FOR UPDATE SKIP LOCKED
	`, id).Scan(&request.ID, &request.WorkspaceID, &request.OwnerID, &request.SessionID, &request.ParentSessionID,
		&request.ParentTurnID, &payload, &request.Status, &request.Priority, &request.QueuedAt, &request.ExpiresAt,
		&startedAt, &turnID, &workspaceLimit, &userLimit)
	if err == sql.ErrNoRows {
		return SubagentStartRequest{}, 0, 0, ErrNotFound
	}
	if err != nil {
		return SubagentStartRequest{}, 0, 0, err
	}
	request.Payload = cloneRaw(payload)
	if startedAt.Valid {
		request.StartedAt = &startedAt.Time
	}
	request.TurnID = turnID.String
	return request, workspaceLimit, userLimit, nil
}

func scanSubagentStartRequest(row rowScanner) (SubagentStartRequest, error) {
	var request SubagentStartRequest
	var payload []byte
	var startedAt sql.NullTime
	err := row.Scan(&request.ID, &request.WorkspaceID, &request.OwnerID, &request.SessionID, &request.ParentSessionID,
		&request.ParentTurnID, &payload, &request.Status, &request.Priority, &request.QueuedAt, &request.ExpiresAt,
		&startedAt, &request.TurnID)
	if err != nil {
		return SubagentStartRequest{}, err
	}
	request.Payload = cloneRaw(payload)
	if startedAt.Valid {
		request.StartedAt = &startedAt.Time
	}
	request.WaitSeconds = subagentStartWaitSeconds(request, time.Now().UTC())
	return request, nil
}

func scanSubagentTaskGroup(row rowScanner) (SubagentTaskGroup, error) {
	var group SubagentTaskGroup
	var canceledAt sql.NullTime
	err := row.Scan(&group.ID, &group.WorkspaceID, &group.OwnerID, &group.ParentSessionID, &group.ParentTurnID, &group.ParentGroupID, &group.ParentItemIndex, &group.Strategy, &group.ResultReducer, &group.Quorum, &group.FailFast, &group.PlannedCount, &group.CreatedAt, &canceledAt, &group.CancelReason)
	if err != nil {
		return SubagentTaskGroup{}, err
	}
	if canceledAt.Valid {
		group.CanceledAt = &canceledAt.Time
	}
	return group, nil
}

func scanSubagentTaskGroupItem(row rowScanner) (SubagentTaskGroupItem, error) {
	var item SubagentTaskGroupItem
	var expectedSchema []byte
	err := row.Scan(&item.GroupID, &item.ItemIndex, &item.AgentID, &item.EnvironmentID, &item.SessionID, &item.Title, &item.Message, &item.Priority, &item.InitialState, &item.ErrorType, &item.ErrorMessage, &expectedSchema, &item.RetryCount, &item.CreatedAt)
	if err != nil {
		return SubagentTaskGroupItem{}, err
	}
	item.ExpectedResultSchema = cloneRaw(expectedSchema)
	return item, nil
}

func (s *PostgresStore) listSubagentTaskGroups(workspaceID string) ([]SubagentTaskGroup, error) {
	ctx := context.Background()
	query := `
		SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, COALESCE(parent_group_id, ''), parent_item_index, strategy, result_reducer, quorum, fail_fast, planned_count, created_at, canceled_at, cancel_reason
		FROM subagent_task_groups
	`
	args := []any{}
	if workspaceID != "" {
		query += ` WHERE workspace_id = $1`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	queryer := interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}(s.db)
	var tx *sql.Tx
	if strings.TrimSpace(workspaceID) != "" {
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		tx, _, err = s.beginDatabaseAccessScope(workspaceCtx, workspaceID)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		ctx = workspaceCtx
		queryer = tx
	}
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]SubagentTaskGroup, 0)
	for rows.Next() {
		group, err := scanSubagentTaskGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (s *PostgresStore) taskGroupItemStatusForMetrics(ctx context.Context, item SubagentTaskGroupItem) string {
	if item.InitialState == SubagentTaskGroupItemStateRejected || item.SessionID == "" {
		return SubagentTaskGroupItemStateRejected
	}
	if queued, err := GetPendingSubagentStartWithContext(ctx, s, item.SessionID); err == nil && queued.Status == "pending" {
		return SubagentTaskGroupItemStateQueued
	}
	session, err := GetSessionWithContext(ctx, s, item.SessionID)
	if err != nil {
		return SessionStatusTerminated
	}
	if session.Status == SessionStatusTerminated {
		return SessionStatusTerminated
	}
	if session.Status == SessionStatusRunning {
		pending, err := ListSessionInterventionsWithContext(ctx, s, item.SessionID, InterventionStatusPending)
		if err == nil && len(pending) > 0 {
			return PendingInterventionTurnStatus(pending)
		}
		return SessionStatusRunning
	}
	if session.Status == SessionStatusIdle {
		events, err := ListEventsWithContext(ctx, s, item.SessionID, 0)
		if err == nil {
			lastTurnStatus, _, hasAgentText := latestTaskGroupTurnOutcome(events)
			switch {
			case lastTurnStatus == TurnStatusCompleted:
				return TurnStatusCompleted
			case lastTurnStatus == TurnStatusFailed:
				return TurnStatusFailed
			case lastTurnStatus == TurnStatusInterrupted:
				return SessionStatusTerminated
			case hasAgentText:
				return TurnStatusCompleted
			}
		}
		return SubagentTaskGroupItemStateCreated
	}
	return session.Status
}

func (s *PostgresStore) taskGroupStatusForMetrics(group SubagentTaskGroup, itemStatuses []string) string {
	if group.CanceledAt != nil {
		return "canceled"
	}
	total := len(itemStatuses)
	completed := 0
	failed := 0
	terminal := 0
	pendingOnly := true
	for _, status := range itemStatuses {
		switch status {
		case TurnStatusCompleted:
			completed++
			terminal++
			pendingOnly = false
		case TurnStatusFailed, SessionStatusTerminated, SubagentTaskGroupItemStateRejected:
			failed++
			terminal++
			pendingOnly = false
		case SubagentTaskGroupItemStateQueued, SubagentTaskGroupItemStateCreated:
		default:
			pendingOnly = false
		}
	}
	if pendingOnly {
		return "pending"
	}
	remaining := total - terminal
	switch group.Strategy {
	case SubagentTaskGroupStrategyAnyCompleted:
		if completed > 0 {
			return "completed"
		}
		if group.FailFast && failed > 0 {
			return "failed"
		}
		if terminal == total {
			return "failed"
		}
	case SubagentTaskGroupStrategyQuorum:
		if completed >= group.Quorum {
			return "completed"
		}
		if group.FailFast && failed > 0 {
			return "failed"
		}
		if completed+remaining < group.Quorum {
			return "failed"
		}
	default:
		if group.FailFast && failed > 0 {
			return "failed"
		}
		if terminal == total {
			if failed > 0 {
				return "failed"
			}
			return "completed"
		}
	}
	return "running"
}

func latestTaskGroupTurnOutcome(events []Event) (string, string, bool) {
	lastTurnStatus := ""
	reason := ""
	hasAgentText := false
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type == EventAgentMessage {
			hasAgentText = true
		}
		if event.Type != EventSessionStatusIdle {
			continue
		}
		var payload struct {
			LastTurnStatus string `json:"last_turn_status"`
			Reason         string `json:"reason"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		lastTurnStatus = strings.TrimSpace(payload.LastTurnStatus)
		reason = strings.TrimSpace(payload.Reason)
		break
	}
	return lastTurnStatus, reason, hasAgentText
}

type subagentPromotionCandidate struct {
	ID          string
	WorkspaceID string
	OwnerID     string
	Priority    int
	QueuedAt    time.Time
}

func fairSubagentPromotionOrder(candidates []subagentPromotionCandidate, limit int) []string {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}

	type ownerBucket struct {
		key   string
		ids   []string
		times []time.Time
	}

	priorities := make(map[int]map[string]*ownerBucket)
	priorityOrder := make([]int, 0)
	for _, candidate := range candidates {
		buckets := priorities[candidate.Priority]
		if buckets == nil {
			buckets = make(map[string]*ownerBucket)
			priorities[candidate.Priority] = buckets
			priorityOrder = append(priorityOrder, candidate.Priority)
		}
		key := candidate.WorkspaceID + "\x00" + candidate.OwnerID
		bucket := buckets[key]
		if bucket == nil {
			bucket = &ownerBucket{key: key}
			buckets[key] = bucket
		}
		bucket.ids = append(bucket.ids, candidate.ID)
		bucket.times = append(bucket.times, candidate.QueuedAt)
	}

	sort.Slice(priorityOrder, func(i int, j int) bool { return priorityOrder[i] > priorityOrder[j] })
	capacity := len(candidates)
	if limit < capacity {
		capacity = limit
	}
	ordered := make([]string, 0, capacity)
	for _, priority := range priorityOrder {
		buckets := priorities[priority]
		for len(buckets) > 0 && len(ordered) < limit {
			round := make([]*ownerBucket, 0, len(buckets))
			for _, bucket := range buckets {
				if len(bucket.ids) == 0 {
					continue
				}
				round = append(round, bucket)
			}
			sort.Slice(round, func(i int, j int) bool {
				if round[i].times[0].Equal(round[j].times[0]) {
					return round[i].key < round[j].key
				}
				return round[i].times[0].Before(round[j].times[0])
			})
			for _, bucket := range round {
				if len(bucket.ids) == 0 {
					delete(buckets, bucket.key)
					continue
				}
				ordered = append(ordered, bucket.ids[0])
				bucket.ids = bucket.ids[1:]
				bucket.times = bucket.times[1:]
				if len(bucket.ids) == 0 {
					delete(buckets, bucket.key)
				}
				if len(ordered) >= limit {
					break
				}
			}
		}
		if len(ordered) >= limit {
			break
		}
	}
	return ordered
}

func subagentStartWaitSeconds(request SubagentStartRequest, now time.Time) int64 {
	end := now
	switch {
	case request.StartedAt != nil:
		end = request.StartedAt.UTC()
	case request.CanceledAt != nil:
		end = request.CanceledAt.UTC()
	}
	if end.Before(request.QueuedAt) {
		return 0
	}
	return int64(end.Sub(request.QueuedAt).Seconds())
}

func (s *PostgresStore) reapOrphanSubagent(ctx context.Context, sessionID string) (ReapedSubagent, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReapedSubagent{}, false, err
	}
	defer tx.Rollback()
	if _, scoped, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return ReapedSubagent{}, false, err
	} else if !scoped {
		return ReapedSubagent{}, false, fmt.Errorf("%w: orphan reap workspace scope is required", ErrInvalid)
	}

	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if errors.Is(err, ErrNotFound) {
		return ReapedSubagent{}, false, nil
	}
	if err != nil {
		return ReapedSubagent{}, false, err
	}
	if session.ArchivedAt != nil || session.Status == SessionStatusTerminated || session.SpawnDepth <= 0 {
		return ReapedSubagent{}, false, nil
	}

	reason := "orphaned_parent_deleted"
	parentID := strings.TrimSpace(session.ParentSessionID)
	if parentID != "" {
		parent, err := getSessionForUpdateTx(ctx, tx, parentID)
		switch {
		case errors.Is(err, ErrNotFound):
			reason = "orphaned_parent_deleted"
		case err != nil:
			return ReapedSubagent{}, false, err
		case parent.ArchivedAt != nil || parent.Status == SessionStatusTerminated:
			reason = "orphaned_parent_terminated"
		default:
			return ReapedSubagent{}, false, nil
		}
	}

	now := time.Now().UTC()
	events := make([]Event, 0, 4)
	if canceled, err := s.cancelPendingSubagentStartsTx(ctx, tx, session.ID, now, "orphan subagent reaped"); err != nil {
		return ReapedSubagent{}, false, err
	} else {
		events = append(events, canceled...)
	}
	if err := terminateOpenTurnsTx(ctx, tx, session.ID, now); err != nil {
		return ReapedSubagent{}, false, err
	}

	session.Status = SessionStatusTerminated
	session.ArchivedAt = &now
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET status = $2, archived_at = $3 WHERE id = $1
	`, session.ID, session.Status, now); err != nil {
		return ReapedSubagent{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{
		"status":            "terminated",
		"reason":            reason,
		"parent_session_id": parentID,
	})
	event, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusTerminated, payload, now)
	if err != nil {
		return ReapedSubagent{}, false, err
	}
	events = append(events, event)

	if err := tx.Commit(); err != nil {
		return ReapedSubagent{}, false, err
	}
	for _, event := range events {
		s.hub.publish(event)
	}
	return ReapedSubagent{Session: session, ParentSessionID: parentID, Reason: reason}, true, nil
}

func (s *PostgresStore) cancelPendingSubagentStartsTx(ctx context.Context, tx *sql.Tx, sessionID string, now time.Time, reason string) ([]Event, error) {
	requests, err := listPendingSubagentStartsForArchiveTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, nil
	}

	requestIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		requestIDs = append(requestIDs, request.ID)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subagent_start_requests
		SET status = 'canceled', canceled_at = $2, cancel_reason = $3
		WHERE status = 'pending' AND id = ANY($1)
	`, requestIDs, now, reason); err != nil {
		return nil, err
	}

	events := make([]Event, 0, len(requests))
	for _, request := range requests {
		waitSeconds := subagentStartWaitSeconds(request, now)
		payload, _ := json.Marshal(map[string]any{
			"request_id":        request.ID,
			"session_id":        request.SessionID,
			"parent_session_id": request.ParentSessionID,
			"reason":            reason,
			"canceled_at":       now,
			"wait_seconds":      waitSeconds,
		})
		event, err := s.appendEventTx(ctx, tx, request.SessionID, EventRuntimeSubagentStartCanceled, payload, now)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func terminateOpenTurnsTx(ctx context.Context, tx *sql.Tx, sessionID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'interrupted',
			interrupt_requested_at = COALESCE(interrupt_requested_at, $2),
			ended_at = COALESCE(ended_at, $2),
			lease_owner = NULL,
			lease_expires_at = NULL,
			last_heartbeat_at = NULL
		WHERE session_id = $1 AND status IN ('running', 'waiting_approval', 'waiting_human')
	`, sessionID, now)
	return err
}

func newSubagentQuotaViolation(errorType string, message string, state map[string]any) error {
	state["category"] = "quota"
	state["conflict"] = true
	return SubagentQuotaViolation{Type: errorType, Message: message, State: state}
}

func (s *PostgresStore) ResolveAgentRuntimeConfig(sessionID string) (AgentRuntimeConfig, error) {
	return s.resolveAgentRuntimeConfigContext(context.Background(), sessionID)
}

func (s *PostgresStore) resolveAgentRuntimeConfigContext(ctx context.Context, sessionID string) (AgentRuntimeConfig, error) {
	if strings.TrimSpace(sessionID) == "" {
		return AgentRuntimeConfig{}, fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	var config AgentRuntimeConfig
	var tools []byte
	var workspaceToolPolicy []byte
	var mcp []byte
	var skills []byte
	var runtimeSettings []byte
	var providerType sql.NullString
	var baseURL sql.NullString
	var apiKeyEnv sql.NullString
	var enabled sql.NullBool
	var summaryText sql.NullString
	var summarySourceUntilSeq sql.NullInt64
	var visionProviderID sql.NullString
	var visionProviderType sql.NullString
	var visionModel sql.NullString
	var visionBaseURL sql.NullString
	var visionAPIKeyEnv sql.NullString

	scope, scoped := DatabaseAccessScopeFromContext(ctx)
	workspaceID := scope.WorkspaceID
	if !scoped {
		if err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id = $1`, sessionID).Scan(&workspaceID); err == sql.ErrNoRows {
			return AgentRuntimeConfig{}, ErrNotFound
		} else if err != nil {
			return AgentRuntimeConfig{}, err
		}
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	defer tx.Rollback()
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return AgentRuntimeConfig{}, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT
			s.id,
			s.workspace_id,
			s.owner_id,
			s.agent_id,
			s.agent_config_version,
			s.environment_id,
			av.llm_provider,
			av.llm_model,
			av.system,
			s.runtime_settings_json,
			av.tools_json,
			COALESCE(wtp.policy_json, '{"permission_rules":[]}'::jsonb),
			av.mcp_json,
			av.skills_json,
			lp.provider_type,
			lp.base_url,
			lp.api_key_env,
			lp.enabled,
			COALESCE(lp.revision, 0),
			COALESCE(lm.revision, 0),
			COALESCE(lm.context_window_tokens, $2),
			COALESCE(lm.capability_type, 'text'),
			vlp.id,
			vlp.provider_type,
			vlm.model,
			vlp.base_url,
			vlp.api_key_env,
			ss.summary_text,
			ss.source_until_seq
		FROM sessions s
		JOIN workspaces w
			ON w.id = s.workspace_id
		LEFT JOIN workspace_tool_permission_policies wtp
			ON wtp.workspace_id = w.id
		JOIN agent_config_versions av
			ON av.agent_id = s.agent_id
			AND av.version = s.agent_config_version
		LEFT JOIN llm_providers lp
			ON lp.id = av.llm_provider
		LEFT JOIN llm_models lm
			ON lm.provider_id = av.llm_provider
			AND lm.model = av.llm_model
		LEFT JOIN llm_models vlm
			ON vlm.is_default_vision = TRUE
		LEFT JOIN llm_providers vlp
			ON vlp.id = vlm.provider_id
			AND vlp.enabled = TRUE
		LEFT JOIN session_summaries ss
			ON ss.session_id = s.id
		WHERE s.id = $1
	`, sessionID, DefaultContextWindowTokens).Scan(
		&config.SessionID,
		&config.WorkspaceID,
		&config.OwnerID,
		&config.AgentID,
		&config.AgentConfigVersion,
		&config.EnvironmentID,
		&config.LLMProvider,
		&config.LLMModel,
		&config.System,
		&runtimeSettings,
		&tools,
		&workspaceToolPolicy,
		&mcp,
		&skills,
		&providerType,
		&baseURL,
		&apiKeyEnv,
		&enabled,
		&config.LLMProviderRevision,
		&config.LLMModelRevision,
		&config.ContextWindowTokens,
		&config.LLMCapabilityType,
		&visionProviderID,
		&visionProviderType,
		&visionModel,
		&visionBaseURL,
		&visionAPIKeyEnv,
		&summaryText,
		&summarySourceUntilSeq,
	)
	if err == sql.ErrNoRows {
		return AgentRuntimeConfig{}, ErrNotFound
	}
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRuntimeConfig{}, err
	}

	config.RuntimeSettings = cloneRaw(runtimeSettings)
	config.ParentSessionID = session.ParentSessionID
	config.SpawnDepth = session.SpawnDepth
	config.Tools = cloneRaw(tools)
	config.WorkspaceToolPolicy = cloneRaw(workspaceToolPolicy)
	config.MCP = cloneRaw(mcp)
	config.Skills = cloneRaw(skills)
	config.LLMProviderType = providerType.String
	config.LLMBaseURL = baseURL.String
	config.LLMAPIKeyEnv = apiKeyEnv.String
	config.SummaryText = summaryText.String
	config.SummarySourceUntilSeq = summarySourceUntilSeq.Int64
	config.VisionLLMProvider = visionProviderID.String
	config.VisionLLMProviderType = visionProviderType.String
	config.VisionLLMModel = visionModel.String
	config.VisionLLMBaseURL = visionBaseURL.String
	config.VisionLLMAPIKeyEnv = visionAPIKeyEnv.String
	overridden, err := s.applyRuntimeLLMOverrides(&config, runtimeSettings)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	if !overridden && enabled.Valid && !enabled.Bool {
		return AgentRuntimeConfig{}, fmt.Errorf("%w: llm provider %s is disabled", ErrInvalid, config.LLMProvider)
	}
	return config, nil
}

func (s *PostgresStore) applyRuntimeLLMOverrides(config *AgentRuntimeConfig, runtimeSettings []byte) (bool, error) {
	if config == nil || len(runtimeSettings) == 0 || string(runtimeSettings) == "null" {
		return false, nil
	}
	var overrides struct {
		LLMProvider *string `json:"llm_provider"`
		LLMModel    *string `json:"llm_model"`
	}
	if err := json.Unmarshal(runtimeSettings, &overrides); err != nil {
		return false, nil
	}
	providerID := strings.TrimSpace(config.LLMProvider)
	modelName := strings.TrimSpace(config.LLMModel)
	if overrides.LLMProvider != nil {
		providerID = strings.TrimSpace(*overrides.LLMProvider)
	}
	if overrides.LLMModel != nil {
		modelName = strings.TrimSpace(*overrides.LLMModel)
	}
	if providerID == "" || modelName == "" || (providerID == config.LLMProvider && modelName == config.LLMModel) {
		return false, nil
	}
	var providerType sql.NullString
	var baseURL sql.NullString
	var apiKeyEnv sql.NullString
	var enabled sql.NullBool
	err := s.db.QueryRowContext(context.Background(), `
		SELECT
			lp.provider_type,
			lp.base_url,
			lp.api_key_env,
			lp.enabled,
			COALESCE(lp.revision, 0),
			COALESCE(lm.revision, 0),
			COALESCE(lm.context_window_tokens, $3),
			COALESCE(lm.capability_type, 'text')
		FROM llm_providers lp
		LEFT JOIN llm_models lm
			ON lm.provider_id = lp.id
			AND lm.model = $2
		WHERE lp.id = $1
	`, providerID, modelName, DefaultContextWindowTokens).Scan(
		&providerType,
		&baseURL,
		&apiKeyEnv,
		&enabled,
		&config.LLMProviderRevision,
		&config.LLMModelRevision,
		&config.ContextWindowTokens,
		&config.LLMCapabilityType,
	)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("%w: runtime override provider %s not found", ErrNotFound, providerID)
	}
	if err != nil {
		return false, err
	}
	if enabled.Valid && !enabled.Bool {
		return false, fmt.Errorf("%w: llm provider %s is disabled", ErrInvalid, providerID)
	}
	config.LLMProvider = providerID
	config.LLMModel = modelName
	config.LLMProviderType = providerType.String
	config.LLMBaseURL = baseURL.String
	config.LLMAPIKeyEnv = apiKeyEnv.String
	return true, nil
}

func (s *PostgresStore) GetSessionSummary(sessionID string) (SessionSummary, error) {
	return s.getSessionSummaryContext(context.Background(), sessionID)
}

func (s *PostgresStore) getSessionSummaryContext(ctx context.Context, sessionID string) (SessionSummary, error) {
	if sessionID == "" {
		return SessionSummary{}, fmt.Errorf("%w: summary session_id is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionSummary{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionSummary{}, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return SessionSummary{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionSummary{}, err
	}
	var summary SessionSummary
	err = tx.QueryRowContext(ctx, `
		SELECT session_id, summary_text, source_until_seq, created_at, updated_at
		FROM session_summaries
		WHERE session_id = $1
	`, sessionID).Scan(
		&summary.SessionID,
		&summary.SummaryText,
		&summary.SourceUntilSeq,
		&summary.CreatedAt,
		&summary.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return SessionSummary{}, ErrNotFound
	}
	if err != nil {
		return SessionSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionSummary{}, err
	}
	return summary, nil
}

func (s *PostgresStore) SaveSessionSummary(sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error) {
	return s.saveSessionSummaryContext(context.Background(), sessionID, input)
}

func (s *PostgresStore) saveSessionSummaryContext(ctx context.Context, sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error) {
	if sessionID == "" {
		return SessionSummary{}, fmt.Errorf("%w: summary session_id is required", ErrInvalid)
	}
	if input.SummaryText == "" {
		return SessionSummary{}, fmt.Errorf("%w: summary_text is required", ErrInvalid)
	}
	if input.SourceUntilSeq < 0 {
		return SessionSummary{}, fmt.Errorf("%w: source_until_seq must be non-negative", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionSummary{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionSummary{}, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return SessionSummary{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionSummary{}, err
	}

	now := time.Now().UTC()
	var summary SessionSummary
	err = tx.QueryRowContext(ctx, `
		INSERT INTO session_summaries (session_id, summary_text, source_until_seq, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (session_id) DO UPDATE SET
			summary_text = EXCLUDED.summary_text,
			source_until_seq = EXCLUDED.source_until_seq,
			updated_at = EXCLUDED.updated_at
		RETURNING session_id, summary_text, source_until_seq, created_at, updated_at
	`, sessionID, input.SummaryText, input.SourceUntilSeq, now).Scan(
		&summary.SessionID,
		&summary.SummaryText,
		&summary.SourceUntilSeq,
		&summary.CreatedAt,
		&summary.UpdatedAt,
	)
	if err != nil {
		return SessionSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionSummary{}, err
	}
	return summary, nil
}

func (s *PostgresStore) UpsertSessionSummary(sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error) {
	return s.upsertSessionSummaryContext(context.Background(), sessionID, input)
}

func (s *PostgresStore) upsertSessionSummaryContext(ctx context.Context, sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error) {
	if sessionID == "" {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: summary session_id is required", ErrInvalid)
	}
	if input.SummaryText == "" {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: summary_text is required", ErrInvalid)
	}
	if input.SourceUntilSeq < 0 {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: source_until_seq must be non-negative", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}

	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return UpsertSessionSummaryResult{}, err
	}
	if session.Status != SessionStatusIdle {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: summary update requires idle session", ErrInvalid)
	}

	now := time.Now().UTC()
	compactingEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusCompacting, mustRaw(`{"status":"compacting"}`), now)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, SessionStatusCompacting); err != nil {
		return UpsertSessionSummaryResult{}, err
	}

	var summary SessionSummary
	err = tx.QueryRowContext(ctx, `
		INSERT INTO session_summaries (session_id, summary_text, source_until_seq, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (session_id) DO UPDATE SET
			summary_text = EXCLUDED.summary_text,
			source_until_seq = EXCLUDED.source_until_seq,
			updated_at = EXCLUDED.updated_at
		RETURNING session_id, summary_text, source_until_seq, created_at, updated_at
	`, session.ID, input.SummaryText, input.SourceUntilSeq, now).Scan(
		&summary.SessionID,
		&summary.SummaryText,
		&summary.SourceUntilSeq,
		&summary.CreatedAt,
		&summary.UpdatedAt,
	)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}

	idleEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusIdle, mustRaw(`{"status":"idle"}`), now)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, SessionStatusIdle); err != nil {
		return UpsertSessionSummaryResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return UpsertSessionSummaryResult{}, err
	}

	events := []Event{compactingEvent, idleEvent}
	for _, event := range events {
		s.hub.publish(event)
	}
	return UpsertSessionSummaryResult{Summary: summary, Events: events}, nil
}

func (s *PostgresStore) GetSession(id string) (Session, error) {
	return s.getSession(id, AccessScope{})
}

func (s *PostgresStore) GetSessionScoped(id string, scope AccessScope) (Session, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return Session{}, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return Session{}, err
	}
	return s.getSessionScopedContext(ctx, id, scope)
}

func (s *PostgresStore) getSessionScopedContext(ctx context.Context, id string, scope AccessScope) (Session, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return Session{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	session, err := getSessionTx(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return Session{}, ErrForbidden
	}
	if err != nil {
		return Session{}, err
	}
	if session.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && session.OwnerID != scope.OwnerID) {
		return Session{}, ErrForbidden
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *PostgresStore) getSession(id string, scope AccessScope) (Session, error) {
	var session Session
	var title sql.NullString
	var parentSessionID sql.NullString
	var parentTurnID sql.NullString
	var sandboxID sql.NullString
	var runtimeSettings []byte
	var pinnedAt sql.NullTime
	var tags []byte
	var archivedAt sql.NullTime

	err := s.db.QueryRowContext(context.Background(), `
		SELECT id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, parent_session_id, parent_turn_id, spawn_depth, status, title, sandbox_id, runtime_settings_json, runtime_settings_revision, pinned_at, tags_json,
			COALESCE(
				NULLIF((SELECT summary_text FROM session_summaries WHERE session_id = sessions.id), ''),
				(SELECT COALESCE(e.payload_json->'content'->0->>'text', e.payload_json->>'message', e.payload_json->>'summary', e.payload_json->>'text') FROM session_events e WHERE e.session_id = sessions.id AND e.type = 'agent.message' ORDER BY e.seq DESC LIMIT 1),
				''
			), created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
			AND ($2 = '' OR workspace_id = $2)
			AND ($2 = '' OR $3 = '' OR owner_id = $3)
	`, id, scope.WorkspaceID, scope.OwnerID).Scan(
		&session.ID,
		&session.WorkspaceID,
		&session.OwnerID,
		&session.AgentID,
		&session.AgentConfigVersion,
		&session.EnvironmentID,
		&parentSessionID,
		&parentTurnID,
		&session.SpawnDepth,
		&session.Status,
		&title,
		&sandboxID,
		&runtimeSettings,
		&session.RuntimeSettingsRevision,
		&pinnedAt,
		&tags,
		&session.SummaryText,
		&session.CreatedBy,
		&session.CreatedAt,
		&archivedAt,
	)
	if err == sql.ErrNoRows {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}

	session.Title = title.String
	session.ParentSessionID = parentSessionID.String
	session.ParentTurnID = parentTurnID.String
	session.SandboxID = sandboxID.String
	session.RuntimeSettings = cloneRaw(runtimeSettings)
	if pinnedAt.Valid {
		session.PinnedAt = &pinnedAt.Time
	}
	if err := json.Unmarshal(tags, &session.Tags); err != nil {
		return Session{}, fmt.Errorf("decode session tags: %w", err)
	}
	if archivedAt.Valid {
		session.ArchivedAt = &archivedAt.Time
	}

	return session, nil
}

func (s *PostgresStore) ListSessions(input ListSessionsInput) ([]Session, error) {
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT s.id, s.workspace_id, s.owner_id, s.agent_id, s.agent_config_version, s.environment_id, s.parent_session_id, s.parent_turn_id, s.spawn_depth, s.status, s.title, s.sandbox_id, s.runtime_settings_json, s.runtime_settings_revision, s.pinned_at, s.tags_json,
			COALESCE(
				NULLIF(ss.summary_text, ''),
				(SELECT COALESCE(e.payload_json->'content'->0->>'text', e.payload_json->>'message', e.payload_json->>'summary', e.payload_json->>'text') FROM session_events e WHERE e.session_id = s.id AND e.type = 'agent.message' ORDER BY e.seq DESC LIMIT 1),
				''
			), s.created_by, s.created_at, s.archived_at
		FROM sessions s
		LEFT JOIN session_summaries ss ON ss.session_id = s.id
		WHERE ($1 = '' OR s.workspace_id = $1)
			AND ($2 = '' OR s.owner_id = $2)
			AND ($3 = '' OR s.parent_session_id = $3)
			AND ($4 = '' OR s.parent_turn_id = $4)
			AND (NOT $5 OR s.parent_session_id IS NOT NULL)
			AND ($6 = '' OR s.status = $6)
			AND ($7 OR s.archived_at IS NULL)
		ORDER BY s.pinned_at DESC NULLS LAST, s.created_at DESC, s.id DESC
		LIMIT $8
	`, input.WorkspaceID, input.OwnerID, input.ParentSessionID, input.ParentTurnID, input.ParentedOnly, input.Status, input.IncludeArchived, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []Session{}
	for rows.Next() {
		session, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (s *PostgresStore) ListSessionsScoped(input ListSessionsInput, scope AccessScope) ([]Session, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return nil, err
	}
	return s.listSessionsScopedContext(ctx, input, scope)
}

func (s *PostgresStore) listSessionsScopedContext(ctx context.Context, input ListSessionsInput, scope AccessScope) ([]Session, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = scope.WorkspaceID
	input.OwnerID = scope.OwnerID
	tx, _, err := s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.workspace_id, s.owner_id, s.agent_id, s.agent_config_version, s.environment_id, s.parent_session_id, s.parent_turn_id, s.spawn_depth, s.status, s.title, s.sandbox_id, s.runtime_settings_json, s.runtime_settings_revision, s.pinned_at, s.tags_json,
			COALESCE(
				NULLIF(ss.summary_text, ''),
				(SELECT COALESCE(e.payload_json->'content'->0->>'text', e.payload_json->>'message', e.payload_json->>'summary', e.payload_json->>'text') FROM session_events e WHERE e.session_id = s.id AND e.type = 'agent.message' ORDER BY e.seq DESC LIMIT 1),
				''
			), s.created_by, s.created_at, s.archived_at
		FROM sessions s
		LEFT JOIN session_summaries ss ON ss.session_id = s.id
		WHERE s.workspace_id = $1
			AND ($2 = '' OR s.owner_id = $2)
			AND ($3 = '' OR s.parent_session_id = $3)
			AND ($4 = '' OR s.parent_turn_id = $4)
			AND (NOT $5 OR s.parent_session_id IS NOT NULL)
			AND ($6 = '' OR s.status = $6)
			AND ($7 OR s.archived_at IS NULL)
		ORDER BY s.pinned_at DESC NULLS LAST, s.created_at DESC, s.id DESC
		LIMIT $8
	`, input.WorkspaceID, input.OwnerID, input.ParentSessionID, input.ParentTurnID, input.ParentedOnly, input.Status, input.IncludeArchived, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []Session{}
	for rows.Next() {
		session, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (s *PostgresStore) UpdateSessionRuntimeSettings(id string, input UpdateSessionRuntimeSettingsInput) (Session, error) {
	return s.updateSessionRuntimeSettingsContext(context.Background(), id, input)
}

func (s *PostgresStore) updateSessionRuntimeSettingsContext(ctx context.Context, id string, input UpdateSessionRuntimeSettingsInput) (Session, error) {
	if input.ExpectedRevision <= 0 {
		return Session{}, fmt.Errorf("%w: expected runtime settings revision must be positive", ErrInvalid)
	}
	if len(input.RuntimeSettings) == 0 {
		input.RuntimeSettings = json.RawMessage(`{}`)
	}
	if !json.Valid(input.RuntimeSettings) {
		return Session{}, fmt.Errorf("%w: runtime_settings must be valid JSON", ErrInvalid)
	}
	if _, err := AgentConfigUpdatePolicy(input.RuntimeSettings); err != nil {
		return Session{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return Session{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return Session{}, err
	}
	if session.RuntimeSettingsRevision != input.ExpectedRevision {
		return Session{}, fmt.Errorf("%w: session runtime settings revision changed", ErrRevisionConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET runtime_settings_json = $2,
			runtime_settings_revision = runtime_settings_revision + 1
		WHERE id = $1 AND runtime_settings_revision = $3
	`, id, input.RuntimeSettings, input.ExpectedRevision)
	if err != nil {
		return Session{}, normalizeLLMReferenceWriteError(err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return Session{}, err
	} else if affected != 1 {
		return Session{}, fmt.Errorf("%w: session runtime settings revision changed", ErrRevisionConflict)
	}
	updated, err := getSessionTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return updated, nil
}

func (s *PostgresStore) followLatestSessionAgentConfigTx(ctx context.Context, tx *sql.Tx, session *Session, now time.Time) (*Event, error) {
	policy, err := AgentConfigUpdatePolicy(session.RuntimeSettings)
	if err != nil {
		return nil, err
	}
	if policy == AgentConfigUpdatePinned {
		return nil, nil
	}

	var latestVersion int
	if err := tx.QueryRowContext(ctx, `
		SELECT current_config_version
		FROM agents
		WHERE id = $1 AND archived_at IS NULL
	`, session.AgentID).Scan(&latestVersion); err == sql.ErrNoRows {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if latestVersion <= session.AgentConfigVersion {
		return nil, nil
	}

	oldVersion := session.AgentConfigVersion
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET agent_config_version = $2
		WHERE id = $1
	`, session.ID, latestVersion); err != nil {
		return nil, err
	}
	session.AgentConfigVersion = latestVersion
	payload, err := json.Marshal(map[string]any{
		"old_agent_config_version":    oldVersion,
		"new_agent_config_version":    latestVersion,
		"latest_agent_config_version": latestVersion,
		"updated_by":                  "system:auto-follow",
		"automatic":                   true,
		"policy":                      policy,
		"trigger":                     "new_turn",
	})
	if err != nil {
		return nil, err
	}
	event, err := s.appendEventTx(ctx, tx, session.ID, EventSessionConfigUpdated, payload, now)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *PostgresStore) UpdateSessionMetadata(id string, input UpdateSessionMetadataInput) (Session, error) {
	return s.updateSessionMetadataContext(context.Background(), id, input)
}

func (s *PostgresStore) updateSessionMetadataContext(ctx context.Context, id string, input UpdateSessionMetadataInput) (Session, error) {
	if input.Pinned == nil && input.Tags == nil {
		return Session{}, fmt.Errorf("%w: pinned or tags is required", ErrInvalid)
	}
	tags := []string{}
	if input.Tags != nil {
		var err error
		tags, err = normalizeSessionTags(*input.Tags)
		if err != nil {
			return Session{}, err
		}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return Session{}, err
	}
	pinned := false
	if input.Pinned != nil {
		pinned = *input.Pinned
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return Session{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return Session{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET pinned_at = CASE
				WHEN $2 THEN CASE WHEN $3 THEN COALESCE(pinned_at, CURRENT_TIMESTAMP) ELSE NULL END
				ELSE pinned_at
			END,
			tags_json = CASE WHEN $4 THEN $5::jsonb ELSE tags_json END
		WHERE id = $1
	`, id, input.Pinned != nil, pinned, input.Tags != nil, tagsJSON)
	if err != nil {
		return Session{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Session{}, err
	}
	if rows == 0 {
		return Session{}, ErrNotFound
	}
	updated, err := getSessionTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return updated, nil
}

func normalizeSessionTags(values []string) ([]string, error) {
	if len(values) > 8 {
		return nil, fmt.Errorf("%w: a session can have at most 8 tags", ErrInvalid)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag := strings.TrimSpace(value)
		if tag == "" {
			continue
		}
		if len([]rune(tag)) > 32 {
			return nil, fmt.Errorf("%w: session tags must be at most 32 characters", ErrInvalid)
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

func (s *PostgresStore) UpgradeSessionAgentConfig(id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error) {
	return s.upgradeSessionAgentConfigContext(context.Background(), id, input)
}

func (s *PostgresStore) upgradeSessionAgentConfigContext(ctx context.Context, id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error) {
	if id == "" {
		return UpgradeSessionAgentConfigResult{}, fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	if input.TargetVersion < 0 || input.ToCurrent == (input.TargetVersion > 0) {
		return UpgradeSessionAgentConfigResult{}, fmt.Errorf("%w: choose exactly one of to_current or to_version", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}

	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	if session.Status == SessionStatusTerminated {
		return UpgradeSessionAgentConfigResult{}, ErrTerminated
	}
	if session.Status != SessionStatusIdle {
		return UpgradeSessionAgentConfigResult{}, fmt.Errorf("%w: session config upgrade requires idle session", ErrConflict)
	}
	if !scoped {
		if _, err := setDatabaseAccessScope(ctx, tx, session.WorkspaceID); err != nil {
			return UpgradeSessionAgentConfigResult{}, err
		}
	}

	var latestVersion int
	err = tx.QueryRowContext(ctx, `
		SELECT current_config_version
		FROM agents
		WHERE id = $1 AND archived_at IS NULL
	`, session.AgentID).Scan(&latestVersion)
	if err == sql.ErrNoRows {
		return UpgradeSessionAgentConfigResult{}, ErrNotFound
	}
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	targetVersion := latestVersion
	if input.TargetVersion > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM agent_config_versions
				WHERE agent_id = $1 AND version = $2
			)
		`, session.AgentID, input.TargetVersion).Scan(&exists); err != nil {
			return UpgradeSessionAgentConfigResult{}, err
		}
		if !exists {
			return UpgradeSessionAgentConfigResult{}, fmt.Errorf("%w: agent config version %s#%d", ErrNotFound, session.AgentID, input.TargetVersion)
		}
		targetVersion = input.TargetVersion
	}

	result := UpgradeSessionAgentConfigResult{
		Session:                  session,
		OldAgentConfigVersion:    session.AgentConfigVersion,
		NewAgentConfigVersion:    session.AgentConfigVersion,
		LatestAgentConfigVersion: latestVersion,
		Changed:                  false,
	}
	if targetVersion == session.AgentConfigVersion {
		if err := tx.Commit(); err != nil {
			return UpgradeSessionAgentConfigResult{}, err
		}
		return result, nil
	}
	if targetVersion < session.AgentConfigVersion {
		return UpgradeSessionAgentConfigResult{}, fmt.Errorf("%w: target agent config version %d is older than session version %d", ErrConflict, targetVersion, session.AgentConfigVersion)
	}

	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET agent_config_version = $2
		WHERE id = $1
	`, session.ID, targetVersion)
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	session.AgentConfigVersion = targetVersion
	payload, err := json.Marshal(map[string]any{
		"old_agent_config_version":    result.OldAgentConfigVersion,
		"new_agent_config_version":    targetVersion,
		"latest_agent_config_version": latestVersion,
		"updated_by":                  defaultString(input.UpdatedBy, "system"),
	})
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	event, err := s.appendEventTx(ctx, tx, session.ID, EventSessionConfigUpdated, json.RawMessage(payload), now)
	if err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpgradeSessionAgentConfigResult{}, err
	}
	s.hub.publish(event)

	result.Session = session
	result.Event = event
	result.NewAgentConfigVersion = targetVersion
	result.Changed = true
	return result, nil
}

func (s *PostgresStore) SaveSessionIntervention(sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error) {
	return s.saveSessionInterventionContext(context.Background(), sessionID, input)
}

func (s *PostgresStore) saveSessionInterventionContext(ctx context.Context, sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error) {
	if sessionID == "" {
		return SessionIntervention{}, fmt.Errorf("%w: intervention session_id is required", ErrInvalid)
	}
	if input.TurnID == "" {
		return SessionIntervention{}, fmt.Errorf("%w: intervention turn_id is required", ErrInvalid)
	}
	if input.CallID == "" {
		return SessionIntervention{}, fmt.Errorf("%w: intervention call_id is required", ErrInvalid)
	}
	if input.ToolIdentifier == "" {
		return SessionIntervention{}, fmt.Errorf("%w: intervention tool_identifier is required", ErrInvalid)
	}
	if input.APIName == "" {
		return SessionIntervention{}, fmt.Errorf("%w: intervention api_name is required", ErrInvalid)
	}
	if input.InterventionMode == "" {
		return SessionIntervention{}, fmt.Errorf("%w: intervention intervention_mode is required", ErrInvalid)
	}
	kind := normalizeInterventionKind(input.Kind)
	if kind == "" {
		return SessionIntervention{}, fmt.Errorf("%w: unsupported intervention kind %q", ErrInvalid, input.Kind)
	}
	if len(input.Request) > 0 && !json.Valid(input.Request) {
		return SessionIntervention{}, fmt.Errorf("%w: intervention request must be valid JSON", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionIntervention{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionIntervention{}, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return SessionIntervention{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionIntervention{}, err
	}

	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx, `
		INSERT INTO session_interventions (
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			requested_at,
			decided_at,
			decision_reason,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL, NULL, $11, $12, $13, $14, NULL, NULL, $15)
		ON CONFLICT (session_id, turn_id, call_id) DO UPDATE
		SET
			tool_identifier = EXCLUDED.tool_identifier,
			api_name = EXCLUDED.api_name,
			arguments_json = EXCLUDED.arguments_json,
			intervention_mode = EXCLUDED.intervention_mode,
			reason = EXCLUDED.reason,
			status = EXCLUDED.status,
			requested_at = EXCLUDED.requested_at,
			decided_at = NULL,
			decision_reason = NULL,
			continuation_messages_json = EXCLUDED.continuation_messages_json,
			continuation_round = EXCLUDED.continuation_round,
			kind = EXCLUDED.kind,
			request_json = EXCLUDED.request_json,
			response_json = NULL,
			responded_at = NULL,
			expires_at = EXCLUDED.expires_at
		RETURNING
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			decision_reason,
			requested_at,
			decided_at,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
	`, sessionID, input.TurnID, input.CallID, input.ToolIdentifier, input.APIName, nullableRaw(input.Arguments), input.InterventionMode, input.Reason, InterventionStatusPending, now, nullableRaw(input.Continuation), input.ContinuationRound, kind, nullableRaw(input.Request), input.ExpiresAt)
	intervention, err := scanSessionIntervention(row)
	if err != nil {
		return SessionIntervention{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionIntervention{}, err
	}
	return intervention, nil
}

func (s *PostgresStore) ListSessionInterventions(sessionID string, status string) ([]SessionIntervention, error) {
	return s.listSessionInterventionsContext(context.Background(), sessionID, status)
}

func (s *PostgresStore) listSessionInterventionsContext(ctx context.Context, sessionID string, status string) ([]SessionIntervention, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: intervention session_id is required", ErrInvalid)
	}
	normalizedStatus := normalizeInterventionStatus(status)
	if status != "" && normalizedStatus == "" {
		return nil, fmt.Errorf("%w: unsupported intervention status %q", ErrInvalid, status)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			decision_reason,
			requested_at,
			decided_at,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
		FROM session_interventions
		WHERE session_id = $1
			AND ($2 = '' OR status = $2)
		ORDER BY requested_at ASC, turn_id ASC, call_id ASC
	`, sessionID, normalizedStatus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	interventions := make([]SessionIntervention, 0)
	for rows.Next() {
		intervention, err := scanSessionIntervention(rows)
		if err != nil {
			return nil, err
		}
		interventions = append(interventions, intervention)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return interventions, nil
}

func (s *PostgresStore) DecideSessionIntervention(sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error) {
	return s.decideSessionInterventionContext(context.Background(), sessionID, input)
}

func (s *PostgresStore) decideSessionInterventionContext(ctx context.Context, sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error) {
	if sessionID == "" {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention session_id is required", ErrInvalid)
	}
	if input.TurnID == "" {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention turn_id is required", ErrInvalid)
	}
	if input.CallID == "" {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention call_id is required", ErrInvalid)
	}
	status := normalizeInterventionStatus(input.Status)
	if status == "" || status == InterventionStatusPending {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: unsupported intervention result %q", ErrInvalid, input.Status)
	}
	if len(input.Response) > 0 && !json.Valid(input.Response) {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention response must be valid JSON", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}

	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return DecideSessionInterventionResult{}, err
	}
	if session.Status == SessionStatusTerminated {
		return DecideSessionInterventionResult{}, ErrTerminated
	}

	current, err := getSessionInterventionForUpdateTx(ctx, tx, sessionID, input.TurnID, input.CallID)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	if current.Status != InterventionStatusPending {
		if current.Status == status {
			return DecideSessionInterventionResult{Intervention: current}, nil
		}
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention %s is already %s", ErrInvalid, input.CallID, current.Status)
	}
	if !interventionStatusAllowed(current.Kind, status) {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention kind %s does not accept result %s", ErrInvalid, current.Kind, status)
	}
	if status == InterventionStatusAnswered && len(input.Response) == 0 {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: answered clarification requires response", ErrInvalid)
	}
	turnStatus, err := getSessionTurnStatusForUpdateTx(ctx, tx, sessionID, input.TurnID)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	turnTerminal := turnStatus == TurnStatusInterrupted || turnStatus == TurnStatusCompleted || turnStatus == TurnStatusFailed
	if turnTerminal && (status == InterventionStatusApproved || status == InterventionStatusAnswered) {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention turn is %s and cannot resume", ErrConflict, turnStatus)
	}

	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx, `
		UPDATE session_interventions
		SET status = $4, decision_reason = $5, decided_at = $6, response_json = $7, responded_at = $6
		WHERE session_id = $1 AND turn_id = $2 AND call_id = $3
		RETURNING
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			decision_reason,
			requested_at,
			decided_at,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
	`, sessionID, input.TurnID, input.CallID, status, input.DecisionReason, now, nullableRaw(input.Response))
	decided, err := scanSessionIntervention(row)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}

	eventType, message := interventionResultEvent(current.Kind, status)
	payload, err := interventionDecisionPayload(decided, message, "user")
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	event, err := s.appendEventTx(ctx, tx, sessionID, eventType, payload, now)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	if !turnTerminal {
		resumable, err := markSessionTurnResumableTx(ctx, tx, sessionID, input.TurnID, input.CallID)
		if err != nil {
			return DecideSessionInterventionResult{}, err
		}
		if !resumable {
			pending, err := hasPendingTurnInterventionsTx(ctx, tx, sessionID, input.TurnID)
			if err != nil {
				return DecideSessionInterventionResult{}, err
			}
			if !pending {
				return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention turn is not resumable", ErrConflict)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return DecideSessionInterventionResult{}, err
	}

	s.hub.publish(event)
	return DecideSessionInterventionResult{
		Intervention: decided,
		Events:       []Event{event},
	}, nil
}

func (s *PostgresStore) MarkSessionTurnWaitingApproval(sessionID string, turnID string) error {
	return s.markSessionTurnWaitingApprovalContext(context.Background(), sessionID, turnID)
}

func (s *PostgresStore) markSessionTurnWaitingApprovalContext(ctx context.Context, sessionID string, turnID string) error {
	return s.markSessionTurnWaitingStatusContext(ctx, sessionID, turnID, TurnStatusWaitingApproval)
}

func (s *PostgresStore) MarkSessionTurnWaitingHuman(sessionID string, turnID string) error {
	return s.markSessionTurnWaitingHumanContext(context.Background(), sessionID, turnID)
}

func (s *PostgresStore) markSessionTurnWaitingHumanContext(ctx context.Context, sessionID string, turnID string) error {
	return s.markSessionTurnWaitingStatusContext(ctx, sessionID, turnID, TurnStatusWaitingHuman)
}

func (s *PostgresStore) markSessionTurnWaitingStatusContext(ctx context.Context, sessionID string, turnID string, waitingStatus string) error {
	if sessionID == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	if turnID == "" {
		return fmt.Errorf("%w: turn_id is required", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = $3,
			resume_intervention_call_id = NULL,
			lease_owner = NULL,
			lease_expires_at = NULL,
			last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND status IN ($3, $4)
	`, sessionID, turnID, waitingStatus, TurnStatusRunning)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) ArchiveSession(id string) (Session, error) {
	return s.archiveSessionContext(context.Background(), id)
}

func (s *PostgresStore) archiveSessionContext(ctx context.Context, id string) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return Session{}, err
	}

	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return Session{}, err
	}
	if session.ArchivedAt != nil {
		return session, nil
	}

	now := time.Now().UTC()
	requests, err := listPendingSubagentStartsForArchiveTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := terminateOpenTurnsTx(ctx, tx, session.ID, now); err != nil {
		return Session{}, err
	}
	session.Status = SessionStatusTerminated
	session.ArchivedAt = &now

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions SET status = $2, archived_at = $3 WHERE id = $1
	`, id, session.Status, now)
	if err != nil {
		return Session{}, err
	}

	published := make([]Event, 0, len(requests)+2)
	if len(requests) > 0 {
		canceledEvents, err := s.cancelPendingSubagentStartsTx(ctx, tx, session.ID, now, "session archived")
		if err != nil {
			return Session{}, err
		}
		published = append(published, canceledEvents...)
		needsAggregate := false
		for _, request := range requests {
			if request.SessionID != id {
				needsAggregate = true
				break
			}
		}
		if needsAggregate {
			payload, _ := json.Marshal(map[string]any{"reason": "session archived", "canceled_requests": len(requests)})
			event, err := s.appendEventTx(ctx, tx, id, EventRuntimeSubagentStartCanceled, payload, now)
			if err != nil {
				return Session{}, err
			}
			published = append(published, event)
		}
	}
	event, err := s.appendEventTx(ctx, tx, id, EventSessionStatusTerminated, mustRaw(`{"status":"terminated"}`), now)
	if err != nil {
		return Session{}, err
	}
	published = append(published, event)

	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	for _, event := range published {
		s.hub.publish(event)
	}

	return session, nil
}

func (s *PostgresStore) RestoreSession(id string) (Session, error) {
	return s.restoreSessionContext(context.Background(), id)
}

func (s *PostgresStore) restoreSessionContext(ctx context.Context, id string) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return Session{}, err
	}

	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return Session{}, err
	}
	if session.ArchivedAt == nil {
		return session, nil
	}

	now := time.Now().UTC()
	session.Status = SessionStatusIdle
	session.ArchivedAt = nil
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET status = $2, archived_at = NULL WHERE id = $1
	`, id, session.Status); err != nil {
		return Session{}, err
	}
	event, err := s.appendEventTx(ctx, tx, id, EventSessionStatusIdle, mustRaw(`{"status":"idle","reason":"session restored"}`), now)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	s.hub.publish(event)
	return session, nil
}

func (s *PostgresStore) DeleteSession(id string) error {
	return s.deleteSessionContext(context.Background(), id)
}

func (s *PostgresStore) deleteSessionContext(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return err
	}
	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	s.hub.closeSession(id)
	return nil
}

func (s *PostgresStore) AppendEvents(sessionID string, inputs []AppendEventInput) ([]Event, error) {
	return s.appendEventsContext(context.Background(), sessionID, inputs)
}

func (s *PostgresStore) StartSessionRunContext(ctx context.Context, sessionID string, input StartSessionRunInput) (StartSessionRunResult, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.RequestHash = strings.TrimSpace(input.RequestHash)
	if sessionID == "" {
		return StartSessionRunResult{}, fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	if len(input.Payload) == 0 {
		return StartSessionRunResult{}, fmt.Errorf("%w: run input is required", ErrInvalid)
	}
	if len(input.IdempotencyKey) > 200 {
		return StartSessionRunResult{}, fmt.Errorf("%w: idempotency_key must not exceed 200 characters", ErrInvalid)
	}
	if input.IdempotencyKey != "" && input.RequestHash == "" {
		return StartSessionRunResult{}, fmt.Errorf("%w: request hash is required with idempotency_key", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return StartSessionRunResult{}, err
	}
	if session.Status == SessionStatusTerminated {
		return StartSessionRunResult{}, ErrTerminated
	}

	if input.IdempotencyKey != "" {
		existing, found, getErr := getSessionRunByIdempotencyTx(ctx, tx, sessionID, input.IdempotencyKey)
		if getErr != nil {
			return StartSessionRunResult{}, getErr
		}
		if found {
			if existing.RequestHash != input.RequestHash {
				return StartSessionRunResult{}, fmt.Errorf("%w: idempotency_conflict", ErrConflict)
			}
			if err := tx.Commit(); err != nil {
				return StartSessionRunResult{}, err
			}
			return StartSessionRunResult{Run: existing, Created: false}, nil
		}
	}
	if session.Status != SessionStatusIdle {
		return StartSessionRunResult{}, fmt.Errorf("%w: user.message requires idle session", ErrSessionBusy)
	}

	now := time.Now().UTC()
	configEvent, err := s.followLatestSessionAgentConfigTx(ctx, tx, &session, now)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	turnID, err := nextTurnID(ctx, tx, session.ID)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	if err := createTurnTx(ctx, tx, session, turnID, now, input.IdempotencyKey, input.RequestHash); err != nil {
		return StartSessionRunResult{}, err
	}
	session.Status = SessionStatusRunning
	statusEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusRunning, runningStatusPayload(session, turnID), now)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	userEvent, err := s.appendEventTx(ctx, tx, session.ID, EventUserMessage, payloadWithTurnID(input.Payload, turnID), now)
	if err != nil {
		return StartSessionRunResult{}, err
	}
	if err := setTurnUserEventTx(ctx, tx, session.ID, turnID, userEvent.ID); err != nil {
		return StartSessionRunResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, session.Status); err != nil {
		return StartSessionRunResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return StartSessionRunResult{}, err
	}
	events := make([]Event, 0, 3)
	if configEvent != nil {
		events = append(events, *configEvent)
	}
	events = append(events, statusEvent, userEvent)
	for _, event := range events {
		s.hub.publish(event)
	}
	return StartSessionRunResult{
		Run: SessionRun{
			ID: turnID, SessionID: session.ID, AgentID: session.AgentID,
			AgentConfigVersion: session.AgentConfigVersion, Status: TurnStatusRunning,
			UserEventID: userEvent.ID, UserEventSeq: userEvent.Seq, StartedAt: now,
			IdempotencyKey: input.IdempotencyKey, RequestHash: input.RequestHash,
		},
		Events: events, Created: true,
	}, nil
}

func (s *PostgresStore) GetSessionRunContext(ctx context.Context, sessionID string, runID string) (SessionRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionRun{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return SessionRun{}, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return SessionRun{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return SessionRun{}, err
	}
	run, found, err := getSessionRunTx(ctx, tx, sessionID, runID)
	if err != nil {
		return SessionRun{}, err
	}
	if !found {
		return SessionRun{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return SessionRun{}, err
	}
	return run, nil
}

func (s *PostgresStore) ListSessionRunsContext(ctx context.Context, sessionID string) ([]SessionRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, sessionRunSelect+`
		WHERE turn.session_id = $1
		ORDER BY turn.started_at ASC, turn.id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []SessionRun{}
	for rows.Next() {
		run, scanErr := scanSessionRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *PostgresStore) ListSessionRunEventsContext(ctx context.Context, sessionID string, runID string, afterSeq int64) ([]Event, error) {
	if afterSeq < 0 {
		return nil, fmt.Errorf("%w: after_seq must not be negative", ErrInvalid)
	}
	if _, err := s.GetSessionRunContext(ctx, sessionID, runID); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, COALESCE(turn_id, ''), seq, type, payload_json, created_at
		FROM session_events
		WHERE session_id = $1 AND turn_id = $2 AND seq > $3
		ORDER BY seq ASC
	`, sessionID, runID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.SessionID, &event.TurnID, &event.Seq, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *PostgresStore) ListSessionTurnControlEventsContext(ctx context.Context, sessionID string, turnID string, afterSeq int64) ([]Event, error) {
	if afterSeq < 0 {
		return nil, fmt.Errorf("%w: after_seq must not be negative", ErrInvalid)
	}
	if _, err := s.GetSessionRunContext(ctx, sessionID, turnID); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, COALESCE(turn_id, ''), seq, type, payload_json, created_at
		FROM session_events
		WHERE session_id = $1 AND turn_id = $2 AND seq > $3
			AND type IN ($4, $5, $6)
		ORDER BY seq ASC
	`, sessionID, turnID, afterSeq, EventUserSteer, EventUserFollowUp, EventUserInterrupt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.SessionID, &event.TurnID, &event.Seq, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

const sessionRunSelect = `
	SELECT turn.id, turn.session_id, turn.agent_id, turn.agent_config_version, turn.status,
		COALESCE(turn.user_event_id, ''), COALESCE(user_event.seq, 0),
		turn.attempt_count, turn.started_at, turn.ended_at, turn.interrupt_requested_at,
		COALESCE(turn.error_message, ''), COALESCE(turn.idempotency_key, ''), turn.request_hash
	FROM session_turns turn
	LEFT JOIN session_events user_event ON user_event.id = turn.user_event_id
`

type sessionRunScanner interface {
	Scan(dest ...any) error
}

func scanSessionRun(scanner sessionRunScanner) (SessionRun, error) {
	var run SessionRun
	if err := scanner.Scan(
		&run.ID, &run.SessionID, &run.AgentID, &run.AgentConfigVersion, &run.Status, &run.UserEventID, &run.UserEventSeq,
		&run.Attempt, &run.StartedAt, &run.EndedAt, &run.InterruptRequestedAt,
		&run.ErrorMessage, &run.IdempotencyKey, &run.RequestHash,
	); err != nil {
		return SessionRun{}, err
	}
	return run, nil
}

func getSessionRunTx(ctx context.Context, tx *sql.Tx, sessionID string, runID string) (SessionRun, bool, error) {
	run, err := scanSessionRun(tx.QueryRowContext(ctx, sessionRunSelect+`
		WHERE turn.session_id = $1 AND turn.id = $2
	`, sessionID, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRun{}, false, nil
	}
	return run, err == nil, err
}

func getSessionRunByIdempotencyTx(ctx context.Context, tx *sql.Tx, sessionID string, key string) (SessionRun, bool, error) {
	run, err := scanSessionRun(tx.QueryRowContext(ctx, sessionRunSelect+`
		WHERE turn.session_id = $1 AND turn.idempotency_key = $2
	`, sessionID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRun{}, false, nil
	}
	return run, err == nil, err
}

func (s *PostgresStore) appendEventsContext(ctx context.Context, sessionID string, inputs []AppendEventInput) ([]Event, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: at least one event is required", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}

	// 锁住 Session 行，串行化同一 Session 下的 seq / turn_id / status 更新。
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	if session.Status == SessionStatusTerminated {
		return nil, ErrTerminated
	}

	now := time.Now().UTC()
	events := make([]Event, 0, len(inputs))
	for _, input := range inputs {
		if input.Type == "" {
			return nil, fmt.Errorf("%w: event type is required", ErrInvalid)
		}
		newEvents, err := s.applyEventTx(ctx, tx, &session, input, now)
		if err != nil {
			return nil, err
		}
		events = append(events, newEvents...)
	}

	_, err = tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, session.Status)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	for _, event := range events {
		s.hub.publish(event)
	}

	return events, nil
}

func (s *PostgresStore) AppendRuntimeEvent(sessionID string, turnID string, input AppendEventInput) ([]Event, error) {
	return s.appendRuntimeEventContext(context.Background(), sessionID, turnID, input)
}

func (s *PostgresStore) appendRuntimeEventContext(ctx context.Context, sessionID string, turnID string, input AppendEventInput) ([]Event, error) {
	if input.Type == "" {
		return nil, fmt.Errorf("%w: event type is required", ErrInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}

	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	if session.Status == SessionStatusTerminated {
		return nil, ErrTerminated
	}
	if session.Status != SessionStatusRunning {
		return nil, nil
	}

	currentTurnID, err := currentTurnID(ctx, tx, session.ID)
	if err != nil {
		return nil, err
	}
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	event, err := s.appendEventTx(ctx, tx, session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), time.Now().UTC())
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	s.hub.publish(event)
	return []Event{event}, nil
}

func (s *PostgresStore) CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error) {
	return s.completeSessionTurnContext(context.Background(), sessionID, turnID, agentPayload)
}

func (s *PostgresStore) completeSessionTurnContext(ctx context.Context, sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}

	// completion 是异步到达的，必须重新锁 Session 并确认它仍在运行。
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	if session.Status == SessionStatusTerminated {
		return nil, ErrTerminated
	}
	if session.Status != SessionStatusRunning {
		return nil, nil
	}

	currentTurnID, err := currentTurnID(ctx, tx, session.ID)
	if err != nil {
		return nil, err
	}
	// 如果 turn 已被中断或新 turn 替换，旧后台任务不能再补 agent.message。
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	now := time.Now().UTC()
	agentEvent, err := s.appendEventTx(ctx, tx, session.ID, EventAgentMessage, payloadWithTurnID(agentPayload, turnID), now)
	if err != nil {
		return nil, err
	}
	session.Status = SessionStatusIdle
	idleEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusIdle, statusPayload("idle", turnID), now)
	if err != nil {
		return nil, err
	}
	if err := completeTurnTx(ctx, tx, session.ID, turnID, now); err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, session.Status)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	events := []Event{agentEvent, idleEvent}
	for _, event := range events {
		s.hub.publish(event)
	}

	return events, nil
}

func (s *PostgresStore) FailSessionTurn(sessionID string, turnID string, reason string) ([]Event, error) {
	return s.failSessionTurnContext(context.Background(), sessionID, turnID, reason)
}

func (s *PostgresStore) failSessionTurnContext(ctx context.Context, sessionID string, turnID string, reason string) ([]Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}

	// failure 也可能异步到达，必须确认失败的是当前 running turn。
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	if session.Status == SessionStatusTerminated {
		return nil, ErrTerminated
	}
	if session.Status != SessionStatusRunning {
		return nil, nil
	}

	currentTurnID, err := currentTurnID(ctx, tx, session.ID)
	if err != nil {
		return nil, err
	}
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	now := time.Now().UTC()
	session.Status = SessionStatusIdle
	idleEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusIdle, failedTurnIdlePayload(turnID, reason), now)
	if err != nil {
		return nil, err
	}
	if err := failTurnTx(ctx, tx, session.ID, turnID, reason, now); err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `UPDATE sessions SET status = $2 WHERE id = $1`, session.ID, session.Status)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	events := []Event{idleEvent}
	for _, event := range events {
		s.hub.publish(event)
	}
	return events, nil
}

func (s *PostgresStore) RecordLLMUsage(input RecordLLMUsageInput) (LLMUsageRecord, error) {
	return s.RecordLLMUsageContext(context.Background(), input)
}

func (s *PostgresStore) RecordLLMUsageContext(ctx context.Context, input RecordLLMUsageInput) (LLMUsageRecord, error) {
	if input.SessionID == "" {
		return LLMUsageRecord{}, fmt.Errorf("%w: usage session_id is required", ErrInvalid)
	}
	if input.TurnID == "" {
		return LLMUsageRecord{}, fmt.Errorf("%w: usage turn_id is required", ErrInvalid)
	}
	if input.ProviderID == "" {
		return LLMUsageRecord{}, fmt.Errorf("%w: usage provider_id is required", ErrInvalid)
	}
	if input.Model == "" {
		return LLMUsageRecord{}, fmt.Errorf("%w: usage model is required", ErrInvalid)
	}
	if input.Status == "" {
		input.Status = "completed"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LLMUsageRecord{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return LLMUsageRecord{}, err
	}
	session, err := getSessionTx(ctx, tx, input.SessionID)
	if err != nil {
		return LLMUsageRecord{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return LLMUsageRecord{}, err
	}
	input.WorkspaceID = session.WorkspaceID
	input.AgentID = session.AgentID
	input.AgentConfigVersion = session.AgentConfigVersion
	id, err := nextSequenceID(ctx, tx, "llmu", "tma_llm_usage_id_seq")
	if err != nil {
		return LLMUsageRecord{}, err
	}
	now := time.Now().UTC()

	var record LLMUsageRecord
	err = tx.QueryRowContext(ctx, `
		INSERT INTO llm_usage_records (
			id,
			workspace_id,
			agent_id,
			agent_config_version,
			session_id,
			turn_id,
			provider_id,
			provider_type,
			model,
			input_tokens,
			output_tokens,
			total_tokens,
			cached_input_tokens,
			reasoning_tokens,
			latency_ms,
			status,
			error_message,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING
			id,
			workspace_id,
			agent_id,
			agent_config_version,
			session_id,
			turn_id,
			provider_id,
			provider_type,
			model,
			input_tokens,
			output_tokens,
			total_tokens,
			cached_input_tokens,
			reasoning_tokens,
			latency_ms,
			status,
			error_message,
			created_at
	`, id,
		input.WorkspaceID,
		input.AgentID,
		input.AgentConfigVersion,
		input.SessionID,
		input.TurnID,
		input.ProviderID,
		input.ProviderType,
		input.Model,
		input.InputTokens,
		input.OutputTokens,
		input.TotalTokens,
		input.CachedInputTokens,
		input.ReasoningTokens,
		input.LatencyMillis,
		input.Status,
		input.ErrorMessage,
		now,
	).Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.AgentID,
		&record.AgentConfigVersion,
		&record.SessionID,
		&record.TurnID,
		&record.ProviderID,
		&record.ProviderType,
		&record.Model,
		&record.InputTokens,
		&record.OutputTokens,
		&record.TotalTokens,
		&record.CachedInputTokens,
		&record.ReasoningTokens,
		&record.LatencyMillis,
		&record.Status,
		&record.ErrorMessage,
		&record.CreatedAt,
	)
	if err != nil {
		return LLMUsageRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return LLMUsageRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) GetSessionLLMUsage(sessionID string) (LLMUsageReport, error) {
	return s.getSessionLLMUsageContext(context.Background(), sessionID)
}

func (s *PostgresStore) getSessionLLMUsageContext(ctx context.Context, sessionID string) (LLMUsageReport, error) {
	if sessionID == "" {
		return LLMUsageReport{}, fmt.Errorf("%w: usage session_id is required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LLMUsageReport{}, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return LLMUsageReport{}, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return LLMUsageReport{}, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return LLMUsageReport{}, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT
			id,
			workspace_id,
			agent_id,
			agent_config_version,
			session_id,
			turn_id,
			provider_id,
			provider_type,
			model,
			input_tokens,
			output_tokens,
			total_tokens,
			cached_input_tokens,
			reasoning_tokens,
			latency_ms,
			status,
			error_message,
			created_at
		FROM llm_usage_records
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return LLMUsageReport{}, err
	}
	defer rows.Close()

	report := LLMUsageReport{
		SessionID: sessionID,
		Records:   []LLMUsageRecord{},
	}
	for rows.Next() {
		var record LLMUsageRecord
		if err := rows.Scan(
			&record.ID,
			&record.WorkspaceID,
			&record.AgentID,
			&record.AgentConfigVersion,
			&record.SessionID,
			&record.TurnID,
			&record.ProviderID,
			&record.ProviderType,
			&record.Model,
			&record.InputTokens,
			&record.OutputTokens,
			&record.TotalTokens,
			&record.CachedInputTokens,
			&record.ReasoningTokens,
			&record.LatencyMillis,
			&record.Status,
			&record.ErrorMessage,
			&record.CreatedAt,
		); err != nil {
			return LLMUsageReport{}, err
		}
		report.Records = append(report.Records, record)
		report.Summary.RecordCount++
		report.Summary.InputTokens += record.InputTokens
		report.Summary.OutputTokens += record.OutputTokens
		report.Summary.TotalTokens += record.TotalTokens
		report.Summary.CachedInputTokens += record.CachedInputTokens
		report.Summary.ReasoningTokens += record.ReasoningTokens
		report.Summary.LatencyMillis += record.LatencyMillis
	}
	if err := rows.Err(); err != nil {
		return LLMUsageReport{}, err
	}
	if err := rows.Close(); err != nil {
		return LLMUsageReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return LLMUsageReport{}, err
	}
	return report, nil
}

func (s *PostgresStore) ListLLMUsage(input ListLLMUsageInput) (LLMUsageAggregateReport, error) {
	return s.ListLLMUsageContext(context.Background(), input)
}

func (s *PostgresStore) ListLLMUsageContext(ctx context.Context, input ListLLMUsageInput) (LLMUsageAggregateReport, error) {
	groupBy := normalizeLLMUsageGroupBy(input.GroupBy)
	if groupBy == "" {
		return LLMUsageAggregateReport{}, fmt.Errorf("%w: unsupported usage group_by %q", ErrInvalid, input.GroupBy)
	}
	input.GroupBy = groupBy

	queryer := interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}(s.db)
	var tx *sql.Tx
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if input.WorkspaceID != "" && strings.TrimSpace(input.WorkspaceID) != scope.WorkspaceID {
			return LLMUsageAggregateReport{}, ErrForbidden
		}
		input.WorkspaceID = scope.WorkspaceID
		var err error
		tx, _, err = s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
		if err != nil {
			return LLMUsageAggregateReport{}, err
		}
		defer tx.Rollback()
		queryer = tx
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT
			CASE WHEN $1 IN ('provider', 'provider_model') THEN provider_id ELSE '' END AS provider_id,
			CASE WHEN $1 IN ('model', 'provider_model') THEN model ELSE '' END AS model,
			COUNT(*) AS record_count,
			COALESCE(SUM(input_tokens), 0) AS input_tokens,
			COALESCE(SUM(output_tokens), 0) AS output_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(cached_input_tokens), 0) AS cached_input_tokens,
			COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
			COALESCE(SUM(latency_ms), 0) AS latency_ms
		FROM llm_usage_records
		WHERE ($2 = '' OR workspace_id = $2)
			AND ($3 = '' OR provider_id = $3)
			AND ($4 = '' OR model = $4)
			AND ($5 = '' OR status = $5)
			AND ($6::timestamptz IS NULL OR created_at >= $6::timestamptz)
			AND ($7::timestamptz IS NULL OR created_at < $7::timestamptz)
		GROUP BY 1, 2
		ORDER BY total_tokens DESC, provider_id ASC, model ASC
	`, groupBy, input.WorkspaceID, input.ProviderID, input.Model, input.Status, input.From, input.To)
	if err != nil {
		return LLMUsageAggregateReport{}, err
	}
	defer rows.Close()

	report := LLMUsageAggregateReport{
		GroupBy: groupBy,
		Filters: input,
		Groups:  []LLMUsageAggregate{},
	}
	for rows.Next() {
		var aggregate LLMUsageAggregate
		if err := rows.Scan(
			&aggregate.ProviderID,
			&aggregate.Model,
			&aggregate.Summary.RecordCount,
			&aggregate.Summary.InputTokens,
			&aggregate.Summary.OutputTokens,
			&aggregate.Summary.TotalTokens,
			&aggregate.Summary.CachedInputTokens,
			&aggregate.Summary.ReasoningTokens,
			&aggregate.Summary.LatencyMillis,
		); err != nil {
			return LLMUsageAggregateReport{}, err
		}
		report.Groups = append(report.Groups, aggregate)
		report.Summary.RecordCount += aggregate.Summary.RecordCount
		report.Summary.InputTokens += aggregate.Summary.InputTokens
		report.Summary.OutputTokens += aggregate.Summary.OutputTokens
		report.Summary.TotalTokens += aggregate.Summary.TotalTokens
		report.Summary.CachedInputTokens += aggregate.Summary.CachedInputTokens
		report.Summary.ReasoningTokens += aggregate.Summary.ReasoningTokens
		report.Summary.LatencyMillis += aggregate.Summary.LatencyMillis
	}
	if err := rows.Err(); err != nil {
		return LLMUsageAggregateReport{}, err
	}
	return report, nil
}

func (s *PostgresStore) RecordObservabilityExporterRun(input RecordObservabilityExporterRunInput) (ObservabilityExporterRun, error) {
	ctx := context.Background()
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return ObservabilityExporterRun{}, err
	}
	for _, workspaceID := range workspaceIDs {
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return ObservabilityExporterRun{}, err
		}
		workspaceInput := input
		workspaceInput.WorkspaceID = workspaceID
		run, err := s.RecordObservabilityExporterRunContext(workspaceCtx, workspaceInput)
		if errors.Is(err, ErrForbidden) {
			continue
		}
		return run, err
	}
	return ObservabilityExporterRun{}, ErrNotFound
}

func (s *PostgresStore) ListObservabilityExporterRuns(input ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error) {
	return s.listObservabilityExporterRunsAllWorkspaces(context.Background(), input)
}

func (s *PostgresStore) RecordOperatorAudit(input RecordOperatorAuditInput) (OperatorAuditRecord, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		workspaceID = DefaultWorkspaceID
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return OperatorAuditRecord{}, err
	}
	input.WorkspaceID = workspaceID
	return s.RecordOperatorAuditContext(ctx, input)
}

func (s *PostgresStore) ListOperatorAudit(input ListOperatorAuditInput) ([]OperatorAuditRecord, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		workspaceID = DefaultWorkspaceID
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = workspaceID
	return s.ListOperatorAuditContext(ctx, input)
}

func (s *PostgresStore) CreateAgentDeliberation(input CreateAgentDeliberationInput) (AgentDeliberation, error) {
	deliberation := input.Deliberation
	if strings.TrimSpace(deliberation.ParentSessionID) == "" || strings.TrimSpace(deliberation.Objective) == "" {
		return AgentDeliberation{}, fmt.Errorf("%w: parent_session_id and objective are required", ErrInvalid)
	}
	if len(input.Participants) < 2 || len(input.Participants) > 8 {
		return AgentDeliberation{}, fmt.Errorf("%w: deliberation participants must be between 2 and 8", ErrInvalid)
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentDeliberation{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "dlib", "tma_agent_deliberation_id_seq")
	if err != nil {
		return AgentDeliberation{}, err
	}
	now := time.Now().UTC()
	deliberation.ID = id
	deliberation.Status = AgentDeliberationStatusRunning
	deliberation.Phase = AgentDeliberationPhaseRound1Running
	deliberation.MaxParticipants = len(input.Participants)
	deliberation.MaxRounds = 2
	deliberation.CreatedAt = now
	deliberation.UpdatedAt = now
	if len(deliberation.Plan) == 0 {
		deliberation.Plan = json.RawMessage(`{}`)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_deliberations (
			id, workspace_id, owner_id, parent_session_id, parent_turn_id, idempotency_key,
			objective, strategy, status, phase, max_participants, max_rounds, max_tokens,
			max_seconds, moderator_agent_id, moderator_environment_id, plan_json,
			final_result_json, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,2,$12,$13,$14,$15,$16,'null'::jsonb,$17,$17)
	`, deliberation.ID, strings.TrimSpace(deliberation.WorkspaceID), strings.TrimSpace(deliberation.OwnerID),
		strings.TrimSpace(deliberation.ParentSessionID), strings.TrimSpace(deliberation.ParentTurnID), strings.TrimSpace(deliberation.IdempotencyKey),
		strings.TrimSpace(deliberation.Objective), strings.TrimSpace(deliberation.Strategy), deliberation.Status, deliberation.Phase,
		deliberation.MaxParticipants, deliberation.MaxTokens, deliberation.MaxSeconds, strings.TrimSpace(deliberation.ModeratorAgentID),
		strings.TrimSpace(deliberation.ModeratorEnvironmentID), deliberation.Plan, now); err != nil {
		return AgentDeliberation{}, err
	}
	for index, participant := range input.Participants {
		participant.DeliberationID = deliberation.ID
		participant.ParticipantIndex = index
		participant.CreatedAt = now
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_deliberation_participants (
				deliberation_id, participant_index, role_id, role_title, goal, agent_id, environment_id, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, participant.DeliberationID, participant.ParticipantIndex, strings.TrimSpace(participant.RoleID),
			strings.TrimSpace(participant.RoleTitle), strings.TrimSpace(participant.Goal), strings.TrimSpace(participant.AgentID),
			strings.TrimSpace(participant.EnvironmentID), now); err != nil {
			return AgentDeliberation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentDeliberation{}, err
	}
	return deliberation, nil
}

func (s *PostgresStore) GetAgentDeliberation(id string) (AgentDeliberation, error) {
	row := s.db.QueryRowContext(context.Background(), agentDeliberationSelect+` WHERE id = $1`, strings.TrimSpace(id))
	deliberation, err := scanAgentDeliberation(row)
	if err == sql.ErrNoRows {
		return AgentDeliberation{}, ErrNotFound
	}
	return deliberation, err
}

func (s *PostgresStore) GetAgentDeliberationByIdempotency(parentSessionID string, idempotencyKey string) (AgentDeliberation, error) {
	row := s.db.QueryRowContext(context.Background(), agentDeliberationSelect+` WHERE parent_session_id = $1 AND idempotency_key = $2`, strings.TrimSpace(parentSessionID), strings.TrimSpace(idempotencyKey))
	deliberation, err := scanAgentDeliberation(row)
	if err == sql.ErrNoRows {
		return AgentDeliberation{}, ErrNotFound
	}
	return deliberation, err
}

func (s *PostgresStore) ListAgentDeliberationsByParentSession(parentSessionID string) ([]AgentDeliberation, error) {
	rows, err := s.db.QueryContext(context.Background(), agentDeliberationSelect+` WHERE parent_session_id = $1 ORDER BY created_at DESC, id DESC`, strings.TrimSpace(parentSessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AgentDeliberation{}
	for rows.Next() {
		item, err := scanAgentDeliberation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateAgentDeliberation(id string, input UpdateAgentDeliberationInput) (AgentDeliberation, error) {
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE agent_deliberations SET status=$2, phase=$3, final_group_id=NULLIF($4,''),
			final_result_json=$5, cancel_reason=$6,
			canceled_at=CASE WHEN $2='canceled' THEN COALESCE(canceled_at, now()) ELSE canceled_at END,
			updated_at=now()
		WHERE id=$1
		RETURNING id, workspace_id, owner_id, parent_session_id, parent_turn_id, idempotency_key,
			objective, strategy, status, phase, max_participants, max_rounds, max_tokens, max_seconds,
			moderator_agent_id, moderator_environment_id, plan_json, COALESCE(final_group_id,''),
			final_result_json, created_at, updated_at, canceled_at, cancel_reason
	`, strings.TrimSpace(id), input.Status, input.Phase, strings.TrimSpace(input.FinalGroupID), nullableRaw(input.FinalResult), strings.TrimSpace(input.CancelReason))
	deliberation, err := scanAgentDeliberation(row)
	if err == sql.ErrNoRows {
		return AgentDeliberation{}, ErrNotFound
	}
	return deliberation, err
}

func (s *PostgresStore) ListAgentDeliberationParticipants(deliberationID string) ([]AgentDeliberationParticipant, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT deliberation_id, participant_index, role_id, role_title, goal, agent_id, environment_id, created_at
		FROM agent_deliberation_participants WHERE deliberation_id=$1 ORDER BY participant_index
	`, strings.TrimSpace(deliberationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AgentDeliberationParticipant{}
	for rows.Next() {
		var item AgentDeliberationParticipant
		if err := rows.Scan(&item.DeliberationID, &item.ParticipantIndex, &item.RoleID, &item.RoleTitle, &item.Goal, &item.AgentID, &item.EnvironmentID, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateAgentDeliberationRound(round AgentDeliberationRound) (AgentDeliberationRound, error) {
	row := s.db.QueryRowContext(context.Background(), `
		INSERT INTO agent_deliberation_rounds (deliberation_id, round_number, round_type, status, task_group_id, created_at)
		VALUES ($1,$2,$3,$4,$5,now())
		RETURNING deliberation_id, round_number, round_type, status, task_group_id, COALESCE(moderator_group_id,''), summary_json, questions_json, created_at, completed_at
	`, strings.TrimSpace(round.DeliberationID), round.RoundNumber, strings.TrimSpace(round.RoundType), strings.TrimSpace(round.Status), strings.TrimSpace(round.TaskGroupID))
	return scanAgentDeliberationRound(row)
}

func (s *PostgresStore) GetAgentDeliberationRound(deliberationID string, roundNumber int) (AgentDeliberationRound, error) {
	row := s.db.QueryRowContext(context.Background(), agentDeliberationRoundSelect+` WHERE deliberation_id=$1 AND round_number=$2`, strings.TrimSpace(deliberationID), roundNumber)
	round, err := scanAgentDeliberationRound(row)
	if err == sql.ErrNoRows {
		return AgentDeliberationRound{}, ErrNotFound
	}
	return round, err
}

func (s *PostgresStore) ListAgentDeliberationRounds(deliberationID string) ([]AgentDeliberationRound, error) {
	rows, err := s.db.QueryContext(context.Background(), agentDeliberationRoundSelect+` WHERE deliberation_id=$1 ORDER BY round_number`, strings.TrimSpace(deliberationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AgentDeliberationRound{}
	for rows.Next() {
		item, err := scanAgentDeliberationRound(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateAgentDeliberationRound(deliberationID string, roundNumber int, input UpdateAgentDeliberationRoundInput) (AgentDeliberationRound, error) {
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE agent_deliberation_rounds SET status=$3, moderator_group_id=NULLIF($4,''), summary_json=$5,
			questions_json=$6, completed_at=CASE WHEN $7 THEN COALESCE(completed_at,now()) ELSE completed_at END
		WHERE deliberation_id=$1 AND round_number=$2
		RETURNING deliberation_id, round_number, round_type, status, task_group_id, COALESCE(moderator_group_id,''), summary_json, questions_json, created_at, completed_at
	`, strings.TrimSpace(deliberationID), roundNumber, strings.TrimSpace(input.Status), strings.TrimSpace(input.ModeratorGroupID), nullableRaw(input.Summary), metadataJSON(input.Questions), input.Complete)
	round, err := scanAgentDeliberationRound(row)
	if err == sql.ErrNoRows {
		return AgentDeliberationRound{}, ErrNotFound
	}
	return round, err
}

func (s *PostgresStore) UpsertAgentDeliberationContribution(contribution AgentDeliberationContribution) (AgentDeliberationContribution, error) {
	row := s.db.QueryRowContext(context.Background(), `
		INSERT INTO agent_deliberation_contributions (
			deliberation_id, round_number, participant_index, task_group_id, item_index, session_id,
			status, contribution_text, contribution_json, retry_count, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10,now(),now())
		ON CONFLICT (deliberation_id, round_number, participant_index) DO UPDATE SET
			task_group_id=EXCLUDED.task_group_id, item_index=EXCLUDED.item_index, session_id=EXCLUDED.session_id,
			status=EXCLUDED.status, contribution_text=EXCLUDED.contribution_text,
			contribution_json=EXCLUDED.contribution_json, retry_count=EXCLUDED.retry_count, updated_at=now()
		RETURNING deliberation_id, round_number, participant_index, task_group_id, item_index,
			COALESCE(session_id,''), status, contribution_text, contribution_json, retry_count, created_at, updated_at
	`, contribution.DeliberationID, contribution.RoundNumber, contribution.ParticipantIndex, contribution.TaskGroupID,
		contribution.ItemIndex, contribution.SessionID, contribution.Status, contribution.ContributionText,
		nullableRaw(contribution.ContributionJSON), contribution.RetryCount)
	return scanAgentDeliberationContribution(row)
}

func (s *PostgresStore) ListAgentDeliberationContributions(deliberationID string, roundNumber int) ([]AgentDeliberationContribution, error) {
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT deliberation_id, round_number, participant_index, task_group_id, item_index,
			COALESCE(session_id,''), status, contribution_text, contribution_json, retry_count, created_at, updated_at
		FROM agent_deliberation_contributions
		WHERE deliberation_id=$1 AND ($2=0 OR round_number=$2)
		ORDER BY round_number, participant_index
	`, strings.TrimSpace(deliberationID), roundNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AgentDeliberationContribution{}
	for rows.Next() {
		item, err := scanAgentDeliberationContribution(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const agentDeliberationSelect = `SELECT id, workspace_id, owner_id, parent_session_id, parent_turn_id, idempotency_key,
	objective, strategy, status, phase, max_participants, max_rounds, max_tokens, max_seconds,
	moderator_agent_id, moderator_environment_id, plan_json, COALESCE(final_group_id,''), final_result_json,
	created_at, updated_at, canceled_at, cancel_reason FROM agent_deliberations`

const agentDeliberationRoundSelect = `SELECT deliberation_id, round_number, round_type, status, task_group_id,
	COALESCE(moderator_group_id,''), summary_json, questions_json, created_at, completed_at FROM agent_deliberation_rounds`

func (s *PostgresStore) UpsertTraceIndex(input UpsertTraceIndexInput) error {
	return s.UpsertTraceIndexContext(context.Background(), input)
}

func (s *PostgresStore) UpsertTraceIndexContext(ctx context.Context, input UpsertTraceIndexInput) error {
	trace := input.Trace
	if trace.TraceID == "" {
		return fmt.Errorf("%w: trace_id is required", ErrInvalid)
	}
	if trace.SessionID == "" {
		return fmt.Errorf("%w: trace session_id is required", ErrInvalid)
	}
	if trace.TurnID == "" {
		return fmt.Errorf("%w: trace turn_id is required", ErrInvalid)
	}
	now := time.Now().UTC()
	if trace.StartedAt.IsZero() {
		trace.StartedAt = now
	}
	if trace.EndedAt.IsZero() {
		trace.EndedAt = trace.StartedAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return err
	}
	session, err := getSessionTx(ctx, tx, trace.SessionID)
	if err != nil {
		return err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return err
	}
	trace.WorkspaceID = session.WorkspaceID
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO trace_indexes (
			trace_id,
			workspace_id,
			session_id,
			turn_id,
			session_title,
			session_status,
			turn_status,
			summary,
			started_at,
			ended_at,
			duration_ms,
			step_count,
			span_count,
			tool_calls,
			errors,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (trace_id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			session_id = EXCLUDED.session_id,
			turn_id = EXCLUDED.turn_id,
			session_title = EXCLUDED.session_title,
			session_status = EXCLUDED.session_status,
			turn_status = EXCLUDED.turn_status,
			summary = EXCLUDED.summary,
			started_at = EXCLUDED.started_at,
			ended_at = EXCLUDED.ended_at,
			duration_ms = EXCLUDED.duration_ms,
			step_count = EXCLUDED.step_count,
			span_count = EXCLUDED.span_count,
			tool_calls = EXCLUDED.tool_calls,
			errors = EXCLUDED.errors,
			updated_at = EXCLUDED.updated_at
	`, trace.TraceID, trace.WorkspaceID, trace.SessionID, trace.TurnID, trace.SessionTitle, trace.SessionStatus, trace.TurnStatus, trace.Summary, trace.StartedAt, trace.EndedAt, trace.DurationMillis, trace.StepCount, trace.SpanCount, trace.ToolCalls, trace.Errors, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM trace_span_indexes WHERE trace_id = $1`, trace.TraceID); err != nil {
		return err
	}
	for _, span := range input.Spans {
		if span.SpanID == "" {
			continue
		}
		span.WorkspaceID = trace.WorkspaceID
		span.SessionID = trace.SessionID
		span.TurnID = trace.TurnID
		if span.SessionTitle == "" {
			span.SessionTitle = trace.SessionTitle
		}
		if span.StartTime.IsZero() {
			span.StartTime = trace.StartedAt
		}
		attributes, err := json.Marshal(span.Attributes)
		if err != nil {
			return err
		}
		if len(attributes) == 0 || string(attributes) == "null" {
			attributes = []byte("{}")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trace_span_indexes (
				trace_id,
				span_id,
				workspace_id,
				session_id,
				turn_id,
				session_title,
				parent_span_id,
				name,
				kind,
				status,
				depth,
				start_time,
				start_offset_ms,
				duration_ms,
				self_duration_ms,
				critical,
				event_count,
				attributes_json,
				updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18::jsonb, $19)
		`, trace.TraceID, span.SpanID, span.WorkspaceID, span.SessionID, span.TurnID, span.SessionTitle, span.ParentSpanID, span.Name, span.Kind, span.Status, span.Depth, span.StartTime, span.StartOffsetMillis, span.DurationMillis, span.SelfDurationMillis, span.Critical, span.EventCount, string(attributes), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) GetTraceIndex(traceID string) (TraceIndexEntry, error) {
	if traceID == "" {
		return TraceIndexEntry{}, fmt.Errorf("%w: trace_id is required", ErrInvalid)
	}
	rows, err := s.ListTraceIndexes(ListTraceIndexInput{TraceID: traceID, IncludeArchived: true, Limit: 1})
	if err != nil {
		return TraceIndexEntry{}, err
	}
	if len(rows) == 0 {
		return TraceIndexEntry{}, ErrNotFound
	}
	return rows[0], nil
}

func (s *PostgresStore) ListTraceIndexes(input ListTraceIndexInput) ([]TraceIndexEntry, error) {
	return s.ListTraceIndexesContext(context.Background(), input)
}

func (s *PostgresStore) ListTraceIndexesContext(ctx context.Context, input ListTraceIndexInput) ([]TraceIndexEntry, error) {
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	queryer := interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}(s.db)
	var tx *sql.Tx
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if input.WorkspaceID != "" && strings.TrimSpace(input.WorkspaceID) != scope.WorkspaceID {
			return nil, ErrForbidden
		}
		input.WorkspaceID = scope.WorkspaceID
		var err error
		tx, _, err = s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		queryer = tx
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT t.trace_id, t.workspace_id, t.session_id, t.turn_id, t.session_title, t.session_status, t.turn_status, t.summary,
			t.started_at, t.ended_at, t.duration_ms, t.step_count, t.span_count, t.tool_calls, t.errors, t.updated_at
		FROM trace_indexes t
		JOIN sessions s ON s.id = t.session_id
		WHERE ($1 = '' OR t.workspace_id = $1)
			AND ($2 = '' OR t.session_id = $2)
			AND ($3 = '' OR t.turn_id = $3)
			AND ($4 = '' OR t.trace_id = $4)
			AND ($5 = '' OR t.session_status = $5)
			AND ($6 OR s.archived_at IS NULL)
		ORDER BY t.started_at DESC, t.trace_id DESC
		LIMIT $7
		OFFSET $8
	`, input.WorkspaceID, input.SessionID, input.TurnID, input.TraceID, input.SessionStatus, input.IncludeArchived, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []TraceIndexEntry{}
	for rows.Next() {
		var entry TraceIndexEntry
		if err := rows.Scan(
			&entry.TraceID,
			&entry.WorkspaceID,
			&entry.SessionID,
			&entry.TurnID,
			&entry.SessionTitle,
			&entry.SessionStatus,
			&entry.TurnStatus,
			&entry.Summary,
			&entry.StartedAt,
			&entry.EndedAt,
			&entry.DurationMillis,
			&entry.StepCount,
			&entry.SpanCount,
			&entry.ToolCalls,
			&entry.Errors,
			&entry.UpdatedAt,
		); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *PostgresStore) ListTraceSpanIndexes(input ListTraceSpanIndexInput) ([]TraceSpanIndexEntry, error) {
	return s.ListTraceSpanIndexesContext(context.Background(), input)
}

func (s *PostgresStore) ListTraceSpanIndexesContext(ctx context.Context, input ListTraceSpanIndexInput) ([]TraceSpanIndexEntry, error) {
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	query := strings.TrimSpace(strings.ToLower(input.Query))
	queryer := interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}(s.db)
	var tx *sql.Tx
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if input.WorkspaceID != "" && strings.TrimSpace(input.WorkspaceID) != scope.WorkspaceID {
			return nil, ErrForbidden
		}
		input.WorkspaceID = scope.WorkspaceID
		var err error
		tx, _, err = s.beginDatabaseAccessScope(ctx, scope.WorkspaceID)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		queryer = tx
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT sp.trace_id, sp.workspace_id, sp.session_id, sp.turn_id, sp.session_title, sp.span_id, sp.parent_span_id, sp.name,
			sp.kind, sp.status, sp.depth, sp.start_time, sp.start_offset_ms, sp.duration_ms, sp.self_duration_ms, sp.critical,
			sp.event_count, sp.attributes_json, sp.updated_at
		FROM trace_span_indexes sp
		JOIN sessions s ON s.id = sp.session_id
		WHERE ($1 = '' OR sp.workspace_id = $1)
			AND ($2 = '' OR sp.trace_id = $2)
			AND ($3 = '' OR sp.session_id = $3)
			AND ($4 = '' OR sp.turn_id = $4)
			AND ($5 = '' OR lower(sp.kind) = lower($5))
			AND ($6 = '' OR lower(sp.status) = lower($6))
			AND ($7::boolean IS NULL OR sp.critical = $7)
			AND ($8 = 0 OR sp.duration_ms >= $8)
			AND ($9 = 0 OR sp.duration_ms <= $9)
			AND ($10 = 0 OR sp.self_duration_ms >= $10)
			AND ($11 = '' OR lower(concat_ws(' ', sp.trace_id, sp.session_id, sp.turn_id, sp.session_title, sp.span_id, sp.parent_span_id, sp.name, sp.kind, sp.status, sp.attributes_json::text)) LIKE '%' || $11 || '%')
			AND ($12 OR s.archived_at IS NULL)
		ORDER BY sp.start_time DESC, sp.span_id
		LIMIT $13
		OFFSET $14
	`, input.WorkspaceID, input.TraceID, input.SessionID, input.TurnID, input.Kind, input.Status, input.Critical, input.MinDurationMillis, input.MaxDurationMillis, input.MinSelfDurationMillis, query, input.IncludeArchived, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []TraceSpanIndexEntry{}
	for rows.Next() {
		var entry TraceSpanIndexEntry
		var attributes []byte
		if err := rows.Scan(
			&entry.TraceID,
			&entry.WorkspaceID,
			&entry.SessionID,
			&entry.TurnID,
			&entry.SessionTitle,
			&entry.SpanID,
			&entry.ParentSpanID,
			&entry.Name,
			&entry.Kind,
			&entry.Status,
			&entry.Depth,
			&entry.StartTime,
			&entry.StartOffsetMillis,
			&entry.DurationMillis,
			&entry.SelfDurationMillis,
			&entry.Critical,
			&entry.EventCount,
			&attributes,
			&entry.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(attributes) > 0 {
			if err := json.Unmarshal(attributes, &entry.Attributes); err != nil {
				return nil, err
			}
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *PostgresStore) PruneTraceIndexes(input PruneTraceIndexInput) (int, error) {
	if input.Before.IsZero() {
		return 0, fmt.Errorf("%w: prune before is required", ErrInvalid)
	}
	limit := input.Limit
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	var count int
	err := s.db.QueryRowContext(context.Background(), `
		WITH deleted AS (
			DELETE FROM trace_indexes
			WHERE trace_id IN (
				SELECT trace_id
				FROM trace_indexes
				WHERE ended_at < $1
				ORDER BY ended_at
				LIMIT $2
			)
			RETURNING 1
		)
		SELECT count(*) FROM deleted
	`, input.Before, limit).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (s *PostgresStore) CreateObjectRef(input CreateObjectRefInput) (ObjectRef, error) {
	if input.Bucket == "" {
		return ObjectRef{}, fmt.Errorf("%w: object bucket is required", ErrInvalid)
	}
	if input.ObjectKey == "" {
		return ObjectRef{}, fmt.Errorf("%w: object_key is required", ErrInvalid)
	}
	if input.SizeBytes < 0 {
		return ObjectRef{}, fmt.Errorf("%w: object size_bytes must be non-negative", ErrInvalid)
	}
	visibility := normalizeObjectVisibility(input.Visibility)
	if visibility == "" {
		return ObjectRef{}, fmt.Errorf("%w: unsupported object visibility %q", ErrInvalid, input.Visibility)
	}

	ctx := context.Background()
	id, err := nextSequenceID(ctx, s.db, "obj", "tma_object_ref_id_seq")
	if err != nil {
		return ObjectRef{}, err
	}

	object := ObjectRef{
		ID:              id,
		WorkspaceID:     defaultString(input.WorkspaceID, DefaultWorkspaceID),
		StorageProvider: defaultString(input.StorageProvider, ObjectStorageProviderS3),
		Bucket:          input.Bucket,
		ObjectKey:       input.ObjectKey,
		ObjectVersion:   input.ObjectVersion,
		ContentType:     input.ContentType,
		SizeBytes:       input.SizeBytes,
		ChecksumSHA256:  input.ChecksumSHA256,
		ETag:            input.ETag,
		Visibility:      visibility,
		Metadata:        metadataJSON(input.Metadata),
		CreatedBy:       defaultString(input.CreatedBy, "system"),
		CreatedAt:       time.Now().UTC(),
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO object_refs (
			id,
			workspace_id,
			storage_provider,
			bucket,
			object_key,
			object_version,
			content_type,
			size_bytes,
			checksum_sha256,
			etag,
			visibility,
			metadata_json,
			created_by,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING
			id,
			workspace_id,
			storage_provider,
			bucket,
			object_key,
			object_version,
			content_type,
			size_bytes,
			checksum_sha256,
			etag,
			visibility,
			metadata_json,
			created_by,
			created_at
	`, object.ID,
		object.WorkspaceID,
		object.StorageProvider,
		object.Bucket,
		object.ObjectKey,
		object.ObjectVersion,
		object.ContentType,
		object.SizeBytes,
		object.ChecksumSHA256,
		object.ETag,
		object.Visibility,
		object.Metadata,
		object.CreatedBy,
		object.CreatedAt,
	)
	return scanObjectRef(row)
}

func (s *PostgresStore) GetObjectRef(id string) (ObjectRef, error) {
	return s.getObjectRef(id, "")
}

func (s *PostgresStore) GetObjectRefScoped(id string, scope AccessScope) (ObjectRef, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return ObjectRef{}, err
	}
	object, err := s.getObjectRef(id, scope.WorkspaceID)
	if errors.Is(err, ErrNotFound) {
		return ObjectRef{}, ErrForbidden
	}
	return object, err
}

func (s *PostgresStore) getObjectRef(id string, workspaceID string) (ObjectRef, error) {
	if id == "" {
		return ObjectRef{}, fmt.Errorf("%w: object ref id is required", ErrInvalid)
	}
	row := s.db.QueryRowContext(context.Background(), `
		SELECT
			id,
			workspace_id,
			storage_provider,
			bucket,
			object_key,
			object_version,
			content_type,
			size_bytes,
			checksum_sha256,
			etag,
			visibility,
			metadata_json,
			created_by,
			created_at
		FROM object_refs
		WHERE id = $1
			AND ($2 = '' OR workspace_id = $2)
	`, id, workspaceID)
	object, err := scanObjectRef(row)
	if err == sql.ErrNoRows {
		return ObjectRef{}, ErrNotFound
	}
	return object, err
}

func (s *PostgresStore) CountSessionArtifactsByObjectRef(objectRefID string) (int, error) {
	if objectRefID == "" {
		return 0, fmt.Errorf("%w: object ref id is required", ErrInvalid)
	}
	var count int
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		FROM session_artifacts
		WHERE object_ref_id = $1
	`, objectRefID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *PostgresStore) DeleteObjectRef(id string) error {
	if id == "" {
		return fmt.Errorf("%w: object ref id is required", ErrInvalid)
	}
	result, err := s.db.ExecContext(context.Background(), `DELETE FROM object_refs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateSessionArtifact(input CreateSessionArtifactInput) (SessionArtifact, error) {
	if input.SessionID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if input.ObjectRefID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact object_ref_id is required", ErrInvalid)
	}
	artifactType := normalizeArtifactType(input.ArtifactType)
	if artifactType == "" {
		return SessionArtifact{}, fmt.Errorf("%w: unsupported artifact_type %q", ErrInvalid, input.ArtifactType)
	}

	session, err := s.GetSession(input.SessionID)
	if err != nil {
		return SessionArtifact{}, err
	}
	object, err := s.GetObjectRef(input.ObjectRefID)
	if err != nil {
		return SessionArtifact{}, err
	}
	workspaceID := defaultString(input.WorkspaceID, session.WorkspaceID)
	if workspaceID != session.WorkspaceID || workspaceID != object.WorkspaceID {
		return SessionArtifact{}, fmt.Errorf("%w: artifact workspace mismatch", ErrInvalid)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionArtifact{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "tma-skill-asset-gc:"+workspaceID); err != nil {
		return SessionArtifact{}, err
	}
	var objectStillExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM object_refs WHERE id = $1)`, input.ObjectRefID).Scan(&objectStillExists); err != nil {
		return SessionArtifact{}, err
	}
	if !objectStillExists {
		return SessionArtifact{}, ErrNotFound
	}
	id, err := nextSequenceID(ctx, tx, "art", "tma_session_artifact_id_seq")
	if err != nil {
		return SessionArtifact{}, err
	}
	name := input.Name
	if name == "" {
		name = object.ObjectKey
	}
	artifact := SessionArtifact{
		ID:            id,
		WorkspaceID:   workspaceID,
		SessionID:     input.SessionID,
		EnvironmentID: defaultString(input.EnvironmentID, session.EnvironmentID),
		ObjectRefID:   input.ObjectRefID,
		TurnID:        input.TurnID,
		ToolCallID:    input.ToolCallID,
		Name:          name,
		Description:   input.Description,
		ArtifactType:  artifactType,
		Metadata:      metadataJSON(input.Metadata),
		CreatedBy:     defaultString(input.CreatedBy, "system"),
		CreatedAt:     time.Now().UTC(),
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO session_artifacts (
			id,
			workspace_id,
			session_id,
			environment_id,
			object_ref_id,
			turn_id,
			tool_call_id,
			name,
			description,
			artifact_type,
			metadata_json,
			created_by,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING
			id,
			workspace_id,
			session_id,
			environment_id,
			object_ref_id,
			turn_id,
			tool_call_id,
			name,
			description,
			artifact_type,
			metadata_json,
			created_by,
			created_at
	`, artifact.ID,
		artifact.WorkspaceID,
		artifact.SessionID,
		nullableString(artifact.EnvironmentID),
		artifact.ObjectRefID,
		artifact.TurnID,
		artifact.ToolCallID,
		artifact.Name,
		artifact.Description,
		artifact.ArtifactType,
		artifact.Metadata,
		artifact.CreatedBy,
		artifact.CreatedAt,
	)
	created, err := scanSessionArtifact(row)
	if err != nil {
		return SessionArtifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionArtifact{}, err
	}
	return created, nil
}

func (s *PostgresStore) GetSessionArtifact(sessionID string, artifactID string) (SessionArtifact, error) {
	if sessionID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if artifactID == "" {
		return SessionArtifact{}, fmt.Errorf("%w: artifact id is required", ErrInvalid)
	}

	row := s.db.QueryRowContext(context.Background(), `
		SELECT
			id,
			workspace_id,
			session_id,
			environment_id,
			object_ref_id,
			turn_id,
			tool_call_id,
			name,
			description,
			artifact_type,
			metadata_json,
			created_by,
			created_at
		FROM session_artifacts
		WHERE session_id = $1 AND id = $2
	`, sessionID, artifactID)
	artifact, err := scanSessionArtifact(row)
	if err == sql.ErrNoRows {
		return SessionArtifact{}, ErrNotFound
	}
	return artifact, err
}

func (s *PostgresStore) DeleteSessionArtifact(sessionID string, artifactID string) error {
	if sessionID == "" {
		return fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if artifactID == "" {
		return fmt.Errorf("%w: artifact id is required", ErrInvalid)
	}
	result, err := s.db.ExecContext(context.Background(), `DELETE FROM session_artifacts WHERE session_id = $1 AND id = $2`, sessionID, artifactID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListSessionArtifacts(sessionID string) ([]SessionArtifact, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: artifact session_id is required", ErrInvalid)
	}
	if _, err := s.GetSession(sessionID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(context.Background(), `
		SELECT
			id,
			workspace_id,
			session_id,
			environment_id,
			object_ref_id,
			turn_id,
			tool_call_id,
			name,
			description,
			artifact_type,
			metadata_json,
			created_by,
			created_at
		FROM session_artifacts
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	artifacts := []SessionArtifact{}
	for rows.Next() {
		artifact, err := scanSessionArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func (s *PostgresStore) RegisterWorker(input RegisterWorkerInput) (Worker, error) {
	if strings.TrimSpace(input.Name) == "" {
		return Worker{}, fmt.Errorf("%w: worker name is required", ErrInvalid)
	}
	workerType := normalizeWorkerType(input.WorkerType)
	if workerType == "" {
		return Worker{}, fmt.Errorf("%w: unsupported worker_type %q", ErrInvalid, input.WorkerType)
	}

	ctx := context.Background()
	id, err := nextSequenceID(ctx, s.db, "wrk", "tma_worker_id_seq")
	if err != nil {
		return Worker{}, err
	}
	now := time.Now().UTC()
	leaseExpiresAt := workerLeaseExpiresAt(now, input.LeaseSeconds)
	worker := Worker{
		ID:             id,
		WorkspaceID:    defaultString(input.WorkspaceID, DefaultWorkspaceID),
		Name:           strings.TrimSpace(input.Name),
		WorkerType:     workerType,
		Status:         WorkerStatusOnline,
		Capabilities:   metadataJSON(input.Capabilities),
		Metadata:       metadataJSON(input.Metadata),
		RegisteredBy:   defaultString(input.RegisteredBy, "system"),
		RegisteredAt:   now,
		LastSeenAt:     &now,
		LeaseExpiresAt: &leaseExpiresAt,
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO workers (
			id,
			workspace_id,
			name,
			worker_type,
			status,
			capabilities_json,
			metadata_json,
			registered_by,
			registered_at,
			last_seen_at,
			lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING
			id,
			workspace_id,
			name,
			worker_type,
			status,
			capabilities_json,
			metadata_json,
			registered_by,
			registered_at,
			last_seen_at,
			lease_expires_at,
			archived_at
	`, worker.ID,
		worker.WorkspaceID,
		worker.Name,
		worker.WorkerType,
		worker.Status,
		worker.Capabilities,
		worker.Metadata,
		worker.RegisteredBy,
		worker.RegisteredAt,
		worker.LastSeenAt,
		worker.LeaseExpiresAt,
	)
	return scanWorker(row)
}

func (s *PostgresStore) GetWorker(id string) (Worker, error) {
	return s.getWorker(id, "")
}

func (s *PostgresStore) GetWorkerScoped(id string, scope AccessScope) (Worker, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return Worker{}, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return Worker{}, err
	}
	worker, err := s.GetWorkerContext(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Worker{}, ErrForbidden
	}
	return worker, err
}

func (s *PostgresStore) getWorker(id string, workspaceID string) (Worker, error) {
	if id == "" {
		return Worker{}, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	row := s.db.QueryRowContext(context.Background(), `
		SELECT
			id,
			workspace_id,
			name,
			worker_type,
			status,
			capabilities_json,
			metadata_json,
			registered_by,
			registered_at,
			last_seen_at,
			lease_expires_at,
			archived_at
		FROM workers
		WHERE id = $1
			AND ($2 = '' OR workspace_id = $2)
	`, id, workspaceID)
	worker, err := scanWorker(row)
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	return worker, err
}

func (s *PostgresStore) ListWorkers(input ListWorkersInput) ([]Worker, error) {
	workspaceID := defaultString(input.WorkspaceID, DefaultWorkspaceID)
	status := strings.TrimSpace(input.Status)
	if status != "" && normalizeWorkerStatus(status) == "" {
		return nil, fmt.Errorf("%w: unsupported worker status %q", ErrInvalid, input.Status)
	}

	query := `
		SELECT
			id,
			workspace_id,
			name,
			worker_type,
			status,
			capabilities_json,
			metadata_json,
			registered_by,
			registered_at,
			last_seen_at,
			lease_expires_at,
			archived_at
		FROM workers
		WHERE workspace_id = $1
	`
	args := []any{workspaceID}
	if status != "" {
		query += ` AND status = $2`
		args = append(args, normalizeWorkerStatus(status))
	}
	query += ` ORDER BY registered_at DESC, id DESC`

	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	workers := []Worker{}
	for rows.Next() {
		worker, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return workers, nil
}

func (s *PostgresStore) ListWorkersScoped(input ListWorkersInput, scope AccessScope) ([]Worker, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = scope.WorkspaceID
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return nil, err
	}
	return s.ListWorkersContext(ctx, input)
}

func (s *PostgresStore) HeartbeatWorker(id string, input WorkerHeartbeatInput) (Worker, error) {
	if id == "" {
		return Worker{}, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	status := normalizeWorkerStatus(input.Status)
	if status == "" {
		return Worker{}, fmt.Errorf("%w: unsupported worker status %q", ErrInvalid, input.Status)
	}
	if status == WorkerStatusArchived {
		return Worker{}, fmt.Errorf("%w: archived status requires archive endpoint", ErrInvalid)
	}
	now := time.Now().UTC()
	leaseExpiresAt := workerLeaseExpiresAt(now, input.LeaseSeconds)

	row := s.db.QueryRowContext(context.Background(), `
		UPDATE workers
		SET
			status = $2,
			capabilities_json = CASE WHEN $3::jsonb IS NULL THEN capabilities_json ELSE $3::jsonb END,
			metadata_json = CASE WHEN $4::jsonb IS NULL THEN metadata_json ELSE $4::jsonb END,
			last_seen_at = $5,
			lease_expires_at = $6
		WHERE id = $1 AND archived_at IS NULL
		RETURNING
			id,
			workspace_id,
			name,
			worker_type,
			status,
			capabilities_json,
			metadata_json,
			registered_by,
			registered_at,
			last_seen_at,
			lease_expires_at,
			archived_at
	`, id,
		status,
		nullableJSON(input.Capabilities),
		nullableJSON(input.Metadata),
		now,
		leaseExpiresAt,
	)
	worker, err := scanWorker(row)
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	return worker, err
}

func (s *PostgresStore) ArchiveWorker(id string) (Worker, error) {
	if id == "" {
		return Worker{}, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	now := time.Now().UTC()
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE workers
		SET status = 'archived', archived_at = $2
		WHERE id = $1 AND archived_at IS NULL
		RETURNING
			id,
			workspace_id,
			name,
			worker_type,
			status,
			capabilities_json,
			metadata_json,
			registered_by,
			registered_at,
			last_seen_at,
			lease_expires_at,
			archived_at
	`, id, now)
	worker, err := scanWorker(row)
	if err == sql.ErrNoRows {
		return Worker{}, ErrNotFound
	}
	return worker, err
}

func (s *PostgresStore) ReapExpiredWorkers(input ReapExpiredWorkersInput) ([]Worker, error) {
	return s.reapExpiredWorkersAllWorkspaces(context.Background(), input)
}

func (s *PostgresStore) EnqueueWorkerWork(input EnqueueWorkerWorkInput) (WorkerWork, error) {
	workType := normalizeWorkerWorkType(input.WorkType)
	if workType == "" {
		return WorkerWork{}, fmt.Errorf("%w: unsupported worker work_type %q", ErrInvalid, input.WorkType)
	}

	ctx := context.Background()
	id, err := nextSequenceID(ctx, s.db, "work", "tma_worker_work_id_seq")
	if err != nil {
		return WorkerWork{}, err
	}
	now := time.Now().UTC()
	work := WorkerWork{
		ID:            id,
		WorkspaceID:   defaultString(input.WorkspaceID, DefaultWorkspaceID),
		WorkerID:      input.WorkerID,
		EnvironmentID: input.EnvironmentID,
		SessionID:     input.SessionID,
		TurnID:        input.TurnID,
		WorkType:      workType,
		Status:        WorkerWorkStatusPending,
		Payload:       metadataJSON(input.Payload),
		Result:        json.RawMessage(`{}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO worker_work (
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}'::jsonb, $10, $11)
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, work.ID,
		work.WorkspaceID,
		nullableString(work.WorkerID),
		nullableString(work.EnvironmentID),
		nullableString(work.SessionID),
		work.TurnID,
		work.WorkType,
		work.Status,
		work.Payload,
		work.CreatedAt,
		work.UpdatedAt,
	)
	return scanWorkerWork(row)
}

func (s *PostgresStore) GetWorkerWork(id string) (WorkerWork, error) {
	return s.getWorkerWork(id, "")
}

func (s *PostgresStore) GetWorkerWorkScoped(id string, scope AccessScope) (WorkerWork, error) {
	scope, err := ValidateAccessScope(scope)
	if err != nil {
		return WorkerWork{}, err
	}
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), scope)
	if err != nil {
		return WorkerWork{}, err
	}
	work, err := s.GetWorkerWorkContext(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return WorkerWork{}, ErrForbidden
	}
	return work, err
}

func (s *PostgresStore) getWorkerWork(id string, workspaceID string) (WorkerWork, error) {
	if id == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker work id is required", ErrInvalid)
	}
	row := s.db.QueryRowContext(context.Background(), `
		SELECT
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
		FROM worker_work
		WHERE id = $1
			AND ($2 = '' OR workspace_id = $2)
	`, id, workspaceID)
	work, err := scanWorkerWork(row)
	if err == sql.ErrNoRows {
		return WorkerWork{}, ErrNotFound
	}
	return work, err
}

func (s *PostgresStore) PollWorkerWork(workerID string, input PollWorkerWorkInput) (*WorkerWork, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker id is required", ErrInvalid)
	}
	worker, err := s.GetWorker(workerID)
	if err != nil {
		return nil, err
	}
	if worker.Status == WorkerStatusArchived {
		return nil, ErrNotFound
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var workID string
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM worker_work
		WHERE workspace_id = $1
			AND status = 'pending'
			AND (worker_id IS NULL OR worker_id = $2)
		ORDER BY created_at ASC, id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, worker.WorkspaceID, workerID).Scan(&workID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	leaseExpiresAt := workerLeaseExpiresAt(now, input.LeaseSeconds)
	row := tx.QueryRowContext(ctx, `
		UPDATE worker_work
		SET
			worker_id = $2,
			status = 'leased',
			lease_expires_at = $3,
			updated_at = $4
		WHERE id = $1
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, workID, workerID, leaseExpiresAt, now)
	work, err := scanWorkerWork(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &work, nil
}

func (s *PostgresStore) AckWorkerWork(workerID string, workID string) (WorkerWork, error) {
	if workerID == "" || workID == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker id and work id are required", ErrInvalid)
	}
	now := time.Now().UTC()
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE worker_work
		SET status = 'running', started_at = COALESCE(started_at, $3), updated_at = $3
		WHERE id = $2 AND worker_id = $1 AND status IN ('leased', 'running')
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, workerID, workID, now)
	work, err := scanWorkerWork(row)
	if err == sql.ErrNoRows {
		return WorkerWork{}, ErrNotFound
	}
	return work, err
}

func (s *PostgresStore) HeartbeatWorkerWork(workerID string, workID string, input WorkerWorkHeartbeatInput) (WorkerWork, error) {
	if workerID == "" || workID == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker id and work id are required", ErrInvalid)
	}
	now := time.Now().UTC()
	leaseExpiresAt := workerLeaseExpiresAt(now, input.LeaseSeconds)
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE worker_work
		SET lease_expires_at = $3, updated_at = $4
		WHERE id = $2 AND worker_id = $1 AND status IN ('leased', 'running')
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, workerID, workID, leaseExpiresAt, now)
	work, err := scanWorkerWork(row)
	if err == sql.ErrNoRows {
		canceled, getErr := s.getCanceledWorkerWorkForWorker(workerID, workID)
		if getErr == nil {
			return canceled, nil
		}
		return WorkerWork{}, ErrNotFound
	}
	return work, err
}

func (s *PostgresStore) CancelWorkerWork(workID string, input CancelWorkerWorkInput) (WorkerWork, error) {
	if workID == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker work id is required", ErrInvalid)
	}
	now := time.Now().UTC()
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "worker work canceled"
	}
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE worker_work
		SET
			status = 'canceled',
			error_message = $2,
			updated_at = $3,
			completed_at = $3
		WHERE id = $1 AND status IN ('pending', 'leased', 'running')
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, workID, reason, now)
	work, err := scanWorkerWork(row)
	if err == nil {
		return work, nil
	}
	if err != sql.ErrNoRows {
		return WorkerWork{}, err
	}
	return s.GetWorkerWork(workID)
}

func (s *PostgresStore) RequeueWorkerWork(workID string, input RequeueWorkerWorkInput) (WorkerWork, error) {
	if workID == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker work id is required", ErrInvalid)
	}
	workerID := strings.TrimSpace(input.WorkerID)
	if input.ClearWorker && workerID != "" {
		return WorkerWork{}, fmt.Errorf("%w: requeue accepts either worker_id or clear_worker, not both", ErrInvalid)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkerWork{}, err
	}
	defer tx.Rollback()

	original, err := scanWorkerWork(tx.QueryRowContext(ctx, `
		SELECT
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
		FROM worker_work
		WHERE id = $1
		FOR UPDATE
	`, workID))
	if err == sql.ErrNoRows {
		return WorkerWork{}, ErrNotFound
	}
	if err != nil {
		return WorkerWork{}, err
	}
	if original.Status != WorkerWorkStatusFailed && original.Status != WorkerWorkStatusCanceled {
		return WorkerWork{}, fmt.Errorf("%w: only failed or canceled worker work can be requeued", ErrConflict)
	}
	if !input.ClearWorker && workerID == "" {
		workerID = original.WorkerID
	}

	newID, err := nextSequenceID(ctx, tx, "work", "tma_worker_work_id_seq")
	if err != nil {
		return WorkerWork{}, err
	}
	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx, `
		INSERT INTO worker_work (
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, '{}'::jsonb, $9, $9)
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, newID,
		original.WorkspaceID,
		nullableString(workerID),
		nullableString(original.EnvironmentID),
		nullableString(original.SessionID),
		original.TurnID,
		original.WorkType,
		metadataJSON(original.Payload),
		now,
	)
	requeued, err := scanWorkerWork(row)
	if err != nil {
		return WorkerWork{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerWork{}, err
	}
	return requeued, nil
}

func (s *PostgresStore) ReapExpiredWorkerWork(input ReapExpiredWorkerWorkInput) ([]WorkerWork, error) {
	return s.reapExpiredWorkerWorkAllWorkspaces(context.Background(), input)
}

func (s *PostgresStore) CompleteWorkerWork(workerID string, workID string, input CompleteWorkerWorkInput) (WorkerWork, error) {
	if workerID == "" || workID == "" {
		return WorkerWork{}, fmt.Errorf("%w: worker id and work id are required", ErrInvalid)
	}
	status := WorkerWorkStatusFailed
	if input.Success {
		status = WorkerWorkStatusCompleted
	}
	now := time.Now().UTC()
	row := s.db.QueryRowContext(context.Background(), `
		UPDATE worker_work
		SET
			status = $3,
			result_json = $4,
			error_message = $5,
			updated_at = $6,
			completed_at = $6
		WHERE id = $2 AND worker_id = $1 AND status IN ('leased', 'running')
		RETURNING
			id,
			workspace_id,
			worker_id,
			environment_id,
			session_id,
			turn_id,
			work_type,
			status,
			payload_json,
			result_json,
			error_message,
			lease_expires_at,
			created_at,
			updated_at,
			started_at,
			completed_at
	`, workerID, workID, status, metadataJSON(input.Result), input.ErrorMessage, now)
	work, err := scanWorkerWork(row)
	if err == sql.ErrNoRows {
		canceled, getErr := s.getCanceledWorkerWorkForWorker(workerID, workID)
		if getErr == nil {
			return canceled, nil
		}
		return WorkerWork{}, ErrNotFound
	}
	return work, err
}

func (s *PostgresStore) getCanceledWorkerWorkForWorker(workerID string, workID string) (WorkerWork, error) {
	work, err := s.GetWorkerWork(workID)
	if err != nil {
		return WorkerWork{}, err
	}
	if work.WorkerID != workerID || work.Status != WorkerWorkStatusCanceled {
		return WorkerWork{}, ErrNotFound
	}
	return work, nil
}

func (s *PostgresStore) ListEvents(sessionID string, afterSeq int64) ([]Event, error) {
	return s.listEventsContext(context.Background(), sessionID, afterSeq)
}

func (s *PostgresStore) listEventsContext(ctx context.Context, sessionID string, afterSeq int64) ([]Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, COALESCE(turn_id, ''), seq, type, payload_json, created_at
		FROM session_events
		WHERE session_id = $1 AND seq > $2
		ORDER BY seq ASC
	`, sessionID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.SessionID, &event.TurnID, &event.Seq, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *PostgresStore) ListConversationMessages(sessionID string, beforeSeq int64) ([]ConversationMessage, error) {
	return s.listConversationMessagesContext(context.Background(), sessionID, beforeSeq)
}

func (s *PostgresStore) listConversationMessagesContext(ctx context.Context, sessionID string, beforeSeq int64) ([]ConversationMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	scope, scoped, err := setContextDatabaseAccessScope(ctx, tx)
	if err != nil {
		return nil, err
	}
	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := authorizeSessionAccessScope(session, scope, scoped); err != nil {
		return nil, err
	}
	if beforeSeq <= 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return []ConversationMessage{}, nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT seq, type, payload_json
		FROM session_events
		WHERE session_id = $1
			AND seq < $2
			AND type IN ($3, $4)
		ORDER BY seq ASC
	`, sessionID, beforeSeq, EventUserMessage, EventAgentMessage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ConversationMessage
	for rows.Next() {
		var eventType string
		var message ConversationMessage
		if err := rows.Scan(&message.Seq, &eventType, &message.Payload); err != nil {
			return nil, err
		}
		switch eventType {
		case EventUserMessage:
			message.Role = "user"
		case EventAgentMessage:
			message.Role = "assistant"
		default:
			continue
		}
		message.Payload = cloneRaw(message.Payload)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *PostgresStore) SubscribeEvents(sessionID string, afterSeq int64) (<-chan Event, func(), error) {
	return s.subscribeEventsContext(context.Background(), sessionID, afterSeq)
}

func (s *PostgresStore) subscribeEventsContext(ctx context.Context, sessionID string, afterSeq int64) (<-chan Event, func(), error) {
	if afterSeq < 0 {
		return nil, nil, fmt.Errorf("%w: after_seq must not be negative", ErrInvalid)
	}
	scope, scoped := DatabaseAccessScopeFromContext(ctx)
	if _, err := s.GetSessionContext(ctx, sessionID); err != nil {
		return nil, nil, err
	}

	wake, cancelWake := s.hub.subscribe(sessionID)
	var streamCtx context.Context = context.Background()
	if scoped {
		var err error
		streamCtx, err = ContextWithDatabaseAccessScope(streamCtx, scope)
		if err != nil {
			cancelWake()
			return nil, nil, err
		}
	}
	streamCtx, cancelContext := context.WithCancel(streamCtx)
	events := make(chan Event)
	go s.streamPersistedEvents(streamCtx, sessionID, afterSeq, wake, cancelWake, events)

	var cancelOnce sync.Once
	cancel := func() {
		cancelOnce.Do(cancelContext)
	}
	return events, cancel, nil
}

func (s *PostgresStore) streamPersistedEvents(ctx context.Context, sessionID string, afterSeq int64, wake <-chan struct{}, cancelWake func(), events chan<- Event) {
	defer close(events)
	defer cancelWake()

	ticker := time.NewTicker(eventCatchUpInterval)
	defer ticker.Stop()
	cursor := afterSeq

	for {
		persisted, err := s.listEventsContext(ctx, sessionID, cursor)
		if err != nil {
			if errors.Is(err, ErrNotFound) || ctx.Err() != nil {
				return
			}
		} else {
			for _, event := range persisted {
				select {
				case <-ctx.Done():
					return
				case events <- event:
					cursor = event.Seq
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				return
			}
		case <-ticker.C:
		}
	}
}

func (s *PostgresStore) startEventListener(databaseURL string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.listenerCancel = cancel
	s.listenerDone = make(chan struct{})
	go s.listenForEventNotifications(ctx, databaseURL)
}

func (s *PostgresStore) listenForEventNotifications(ctx context.Context, databaseURL string) {
	defer close(s.listenerDone)

	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			if !waitForEventListenerRetry(ctx) {
				return
			}
			continue
		}

		_, err = conn.Exec(ctx, "LISTEN "+eventNotificationChannel)
		if err == nil {
			for ctx.Err() == nil {
				notification, waitErr := conn.WaitForNotification(ctx)
				if waitErr != nil {
					break
				}
				s.hub.notify(notification.Payload)
			}
		}
		_ = conn.Close(context.Background())
		if !waitForEventListenerRetry(ctx) {
			return
		}
	}
}

func waitForEventListenerRetry(ctx context.Context) bool {
	timer := time.NewTimer(eventListenerReconnectInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *PostgresStore) ClaimSessionTurns(input ClaimSessionTurnsInput) ([]SessionTurnWork, error) {
	if strings.TrimSpace(input.LeaseOwner) == "" {
		return nil, fmt.Errorf("%w: lease_owner is required", ErrInvalid)
	}
	if input.LeaseDuration <= 0 {
		return nil, fmt.Errorf("%w: lease_duration must be positive", ErrInvalid)
	}
	if input.Limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", ErrInvalid)
	}

	ctx := context.Background()
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, "")
	if err != nil {
		return nil, err
	}
	workspaceIDs = s.rotateSessionTurnClaimWorkspaces(workspaceIDs)
	work := make([]SessionTurnWork, 0, input.Limit)
	for _, workspaceID := range workspaceIDs {
		remaining := input.Limit - len(work)
		if remaining <= 0 {
			break
		}
		workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		claimed, err := s.claimSessionTurnsWorkspace(workspaceCtx, input, remaining)
		if err != nil {
			return nil, err
		}
		work = append(work, claimed...)
	}
	return work, nil
}

func (s *PostgresStore) rotateSessionTurnClaimWorkspaces(workspaceIDs []string) []string {
	if len(workspaceIDs) < 2 {
		return workspaceIDs
	}
	s.claimMu.Lock()
	start := s.claimCursor % len(workspaceIDs)
	s.claimCursor = (start + 1) % len(workspaceIDs)
	s.claimMu.Unlock()
	rotated := make([]string, 0, len(workspaceIDs))
	rotated = append(rotated, workspaceIDs[start:]...)
	rotated = append(rotated, workspaceIDs[:start]...)
	return rotated
}

func (s *PostgresStore) claimSessionTurnsWorkspace(ctx context.Context, input ClaimSessionTurnsInput, limit int) ([]SessionTurnWork, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, scoped, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		return nil, err
	} else if !scoped {
		return nil, fmt.Errorf("%w: Session turn claim workspace scope is required", ErrInvalid)
	}

	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
			SELECT st.session_id, st.id, st.workspace_id, st.owner_id, event.seq, event.payload_json, st.resume_intervention_call_id
			FROM session_turns st
			JOIN session_events event ON event.id = st.user_event_id
			WHERE st.status = 'running'
				AND (st.lease_expires_at IS NULL OR st.lease_expires_at <= CURRENT_TIMESTAMP)
			ORDER BY st.started_at ASC, st.session_id ASC
			FOR UPDATE OF st SKIP LOCKED
			LIMIT $1
		)
		UPDATE session_turns st
		SET lease_owner = $2,
			lease_expires_at = CURRENT_TIMESTAMP + ($3 * interval '1 millisecond'),
			last_heartbeat_at = CURRENT_TIMESTAMP,
			attempt_count = st.attempt_count + 1
		FROM candidates candidate
		WHERE st.session_id = candidate.session_id AND st.id = candidate.id
		RETURNING st.session_id, st.id, candidate.workspace_id, candidate.owner_id, candidate.seq, candidate.payload_json, candidate.resume_intervention_call_id, st.attempt_count
	`, limit, input.LeaseOwner, input.LeaseDuration.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	work := make([]SessionTurnWork, 0, limit)
	resumeCallIDs := make([]string, 0, limit)
	for rows.Next() {
		var item SessionTurnWork
		var resumeCallID sql.NullString
		if err := rows.Scan(&item.SessionID, &item.TurnID, &item.Scope.WorkspaceID, &item.Scope.OwnerID, &item.UserEventSeq, &item.UserPayload, &resumeCallID, &item.Attempt); err != nil {
			return nil, err
		}
		item.UserPayload = cloneRaw(item.UserPayload)
		work = append(work, item)
		resumeCallIDs = append(resumeCallIDs, resumeCallID.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index, callID := range resumeCallIDs {
		if callID == "" {
			continue
		}
		intervention, err := getSessionInterventionTx(ctx, tx, work[index].SessionID, work[index].TurnID, callID)
		if err != nil {
			return nil, err
		}
		work[index].ResumeIntervention = &intervention
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return work, nil
}

func (s *PostgresStore) RenewSessionTurnLease(input RenewSessionTurnLeaseInput) (bool, error) {
	if input.SessionID == "" || input.TurnID == "" || strings.TrimSpace(input.LeaseOwner) == "" {
		return false, fmt.Errorf("%w: session_id, turn_id, and lease_owner are required", ErrInvalid)
	}
	if input.LeaseDuration <= 0 {
		return false, fmt.Errorf("%w: lease_duration must be positive", ErrInvalid)
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if strings.TrimSpace(input.Scope.WorkspaceID) != "" {
		ctx, err = ContextWithDatabaseAccessScope(ctx, input.Scope)
		if err != nil {
			return false, err
		}
		if _, err := setDatabaseAccessScope(ctx, tx, input.Scope.WorkspaceID); err != nil {
			return false, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET lease_expires_at = CURRENT_TIMESTAMP + ($4 * interval '1 millisecond'),
			last_heartbeat_at = CURRENT_TIMESTAMP
		WHERE session_id = $1 AND id = $2 AND lease_owner = $3 AND status = 'running'
	`, input.SessionID, input.TurnID, input.LeaseOwner, input.LeaseDuration.Milliseconds())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *PostgresStore) ReleaseSessionTurnLease(input ReleaseSessionTurnLeaseInput) error {
	if input.SessionID == "" || input.TurnID == "" || strings.TrimSpace(input.LeaseOwner) == "" {
		return fmt.Errorf("%w: session_id, turn_id, and lease_owner are required", ErrInvalid)
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if strings.TrimSpace(input.Scope.WorkspaceID) != "" {
		ctx, err = ContextWithDatabaseAccessScope(ctx, input.Scope)
		if err != nil {
			return err
		}
		if _, err := setDatabaseAccessScope(ctx, tx, input.Scope.WorkspaceID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE session_turns
		SET lease_owner = NULL, lease_expires_at = NULL, last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND lease_owner = $3 AND status = 'running'
	`, input.SessionID, input.TurnID, input.LeaseOwner)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) applyEventTx(ctx context.Context, tx *sql.Tx, session *Session, input AppendEventInput, now time.Time) ([]Event, error) {
	switch input.Type {
	case EventUserMessage:
		if session.Status != SessionStatusIdle {
			return nil, fmt.Errorf("%w: user.message requires idle session", ErrInvalid)
		}

		configEvent, err := s.followLatestSessionAgentConfigTx(ctx, tx, session, now)
		if err != nil {
			return nil, err
		}

		// user.message 开启一个新的 turn，并立刻把 Session 切到 running。
		turnID, err := nextTurnID(ctx, tx, session.ID)
		if err != nil {
			return nil, err
		}
		if err := createTurnTx(ctx, tx, *session, turnID, now); err != nil {
			return nil, err
		}
		session.Status = SessionStatusRunning
		statusEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusRunning, runningStatusPayload(*session, turnID), now)
		if err != nil {
			return nil, err
		}
		userEvent, err := s.appendEventTx(ctx, tx, session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		if err != nil {
			return nil, err
		}
		if err := setTurnUserEventTx(ctx, tx, session.ID, turnID, userEvent.ID); err != nil {
			return nil, err
		}

		events := make([]Event, 0, 3)
		if configEvent != nil {
			events = append(events, *configEvent)
		}
		return append(events, statusEvent, userEvent), nil

	case EventUserInterrupt:
		if session.Status != SessionStatusRunning {
			return nil, fmt.Errorf("%w: user.interrupt requires running session", ErrInvalid)
		}

		// interrupt 总是作用于当前 running turn，而不是客户端指定的任意 turn。
		turnID, err := currentTurnID(ctx, tx, session.ID)
		if err != nil {
			return nil, err
		}
		if turnID == "" {
			return nil, fmt.Errorf("%w: running session has no active turn", ErrInvalid)
		}

		userEvent, err := s.appendEventTx(ctx, tx, session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		if err != nil {
			return nil, err
		}
		interruptingEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusInterrupting, statusPayload("interrupting", turnID), now)
		if err != nil {
			return nil, err
		}
		rejectionEvents, err := s.rejectPendingTurnInterventionsTx(ctx, tx, session.ID, turnID, "turn interrupted by user", now)
		if err != nil {
			return nil, err
		}
		session.Status = SessionStatusIdle
		idleEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusIdle, statusPayload("idle", turnID), now)
		if err != nil {
			return nil, err
		}
		if err := interruptTurnTx(ctx, tx, session.ID, turnID, now); err != nil {
			return nil, err
		}

		events := []Event{userEvent, interruptingEvent}
		events = append(events, rejectionEvents...)
		events = append(events, idleEvent)
		return events, nil

	case EventUserSteer, EventUserFollowUp:
		if session.Status != SessionStatusRunning {
			return nil, fmt.Errorf("%w: %s requires running session", ErrInvalid, input.Type)
		}
		turnID, err := currentTurnID(ctx, tx, session.ID)
		if err != nil {
			return nil, err
		}
		if turnID == "" {
			return nil, fmt.Errorf("%w: running session has no active turn", ErrInvalid)
		}
		event, err := s.appendEventTx(ctx, tx, session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		if err != nil {
			return nil, err
		}
		return []Event{event}, nil

	default:
		event, err := s.appendEventTx(ctx, tx, session.ID, input.Type, cloneRaw(input.Payload), now)
		if err != nil {
			return nil, err
		}
		return []Event{event}, nil
	}
}

func (s *PostgresStore) appendEventTx(ctx context.Context, tx *sql.Tx, sessionID, eventType string, payload json.RawMessage, now time.Time) (Event, error) {
	var err error
	payload, err = postgresSafeJSON(payload)
	if err != nil {
		return Event{}, fmt.Errorf("sanitize event payload: %w", err)
	}
	// seq 是 Session 内递增序号；外层事务已锁 Session 行，避免并发重复 seq。
	seq, err := nextEventSeq(ctx, tx, sessionID)
	if err != nil {
		return Event{}, err
	}
	id, err := nextSequenceID(ctx, tx, "evt", "tma_event_id_seq")
	if err != nil {
		return Event{}, err
	}

	event := Event{
		ID:        id,
		SessionID: sessionID,
		TurnID:    payloadString(payload, "turn_id"),
		Seq:       seq,
		Type:      eventType,
		Payload:   cloneRaw(payload),
		CreatedAt: now,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, turn_id, seq, type, payload_json, created_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7)
	`, event.ID, event.SessionID, event.TurnID, event.Seq, event.Type, nullableRaw(event.Payload), event.CreatedAt)
	if err != nil {
		return Event{}, err
	}
	if err := s.projectToolPermissionAuditEventTx(ctx, tx, event); err != nil {
		return Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, eventNotificationChannel, event.SessionID); err != nil {
		return Event{}, err
	}

	return event, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func nextSequenceID(ctx context.Context, q queryer, prefix, sequence string) (string, error) {
	// 全局资源 ID 使用 Postgres sequence，避免 count(*) + 1 在并发下重复。
	var value int64
	if err := q.QueryRowContext(ctx, "SELECT nextval('"+sequence+"')").Scan(&value); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%06d", prefix, value), nil
}

func nextEventSeq(ctx context.Context, tx *sql.Tx, sessionID string) (int64, error) {
	var seq int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1 FROM session_events WHERE session_id = $1
	`, sessionID).Scan(&seq)
	return seq, err
}

func nextTurnID(ctx context.Context, tx *sql.Tx, sessionID string) (string, error) {
	// turn_id 是 Session 内编号；调用方必须先 FOR UPDATE 锁住 Session。
	var count int64
	err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM session_turns WHERE session_id = $1
	`, sessionID).Scan(&count)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("turn_%06d", count+1), nil
}

func currentTurnID(ctx context.Context, tx *sql.Tx, sessionID string) (string, error) {
	var turnID sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM session_turns
		WHERE session_id = $1 AND status IN ('running', 'waiting_approval', 'waiting_human')
		ORDER BY started_at DESC
		LIMIT 1
	`, sessionID).Scan(&turnID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return turnID.String, nil
}

func createTurnTx(ctx context.Context, tx *sql.Tx, session Session, turnID string, now time.Time, runMetadata ...string) error {
	idempotencyKey := ""
	requestHash := ""
	if len(runMetadata) > 0 {
		idempotencyKey = strings.TrimSpace(runMetadata[0])
	}
	if len(runMetadata) > 1 {
		requestHash = strings.TrimSpace(runMetadata[1])
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO session_turns (
			session_id, id, workspace_id, owner_id, agent_id, agent_config_version,
			status, started_at, idempotency_key, request_hash
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'running', $7, NULLIF($8, ''), $9)
	`, session.ID, turnID, session.WorkspaceID, session.OwnerID, session.AgentID,
		session.AgentConfigVersion, now, idempotencyKey, requestHash)
	return err
}

func setTurnUserEventTx(ctx context.Context, tx *sql.Tx, sessionID, turnID, userEventID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET user_event_id = $3
		WHERE session_id = $1 AND id = $2
	`, sessionID, turnID, userEventID)
	return err
}

func completeTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'completed', ended_at = $3,
			lease_owner = NULL, lease_expires_at = NULL, last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND status IN ('running', 'waiting_approval', 'waiting_human')
	`, sessionID, turnID, now)
	return err
}

func interruptTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'interrupted', interrupt_requested_at = $3, ended_at = $3,
			lease_owner = NULL, lease_expires_at = NULL, last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND status IN ('running', 'waiting_approval', 'waiting_human')
	`, sessionID, turnID, now)
	return err
}

func failTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID, reason string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'failed', error_message = $3, ended_at = $4,
			lease_owner = NULL, lease_expires_at = NULL, last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND status IN ('running', 'waiting_approval', 'waiting_human')
	`, sessionID, turnID, nullableString(reason), now)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanObjectRef(scanner rowScanner) (ObjectRef, error) {
	var object ObjectRef
	var metadata []byte
	err := scanner.Scan(
		&object.ID,
		&object.WorkspaceID,
		&object.StorageProvider,
		&object.Bucket,
		&object.ObjectKey,
		&object.ObjectVersion,
		&object.ContentType,
		&object.SizeBytes,
		&object.ChecksumSHA256,
		&object.ETag,
		&object.Visibility,
		&metadata,
		&object.CreatedBy,
		&object.CreatedAt,
	)
	if err != nil {
		return ObjectRef{}, err
	}
	object.Metadata = cloneRaw(metadata)
	return object, nil
}

func scanSessionArtifact(scanner rowScanner) (SessionArtifact, error) {
	var artifact SessionArtifact
	var environmentID sql.NullString
	var metadata []byte
	err := scanner.Scan(
		&artifact.ID,
		&artifact.WorkspaceID,
		&artifact.SessionID,
		&environmentID,
		&artifact.ObjectRefID,
		&artifact.TurnID,
		&artifact.ToolCallID,
		&artifact.Name,
		&artifact.Description,
		&artifact.ArtifactType,
		&metadata,
		&artifact.CreatedBy,
		&artifact.CreatedAt,
	)
	if err != nil {
		return SessionArtifact{}, err
	}
	artifact.EnvironmentID = environmentID.String
	artifact.Metadata = cloneRaw(metadata)
	return artifact, nil
}

func scanWorker(scanner rowScanner) (Worker, error) {
	var worker Worker
	var capabilities []byte
	var metadata []byte
	var lastSeenAt sql.NullTime
	var leaseExpiresAt sql.NullTime
	var archivedAt sql.NullTime
	err := scanner.Scan(
		&worker.ID,
		&worker.WorkspaceID,
		&worker.Name,
		&worker.WorkerType,
		&worker.Status,
		&capabilities,
		&metadata,
		&worker.RegisteredBy,
		&worker.RegisteredAt,
		&lastSeenAt,
		&leaseExpiresAt,
		&archivedAt,
	)
	if err != nil {
		return Worker{}, err
	}
	worker.Capabilities = cloneRaw(capabilities)
	worker.Metadata = cloneRaw(metadata)
	if lastSeenAt.Valid {
		worker.LastSeenAt = &lastSeenAt.Time
	}
	if leaseExpiresAt.Valid {
		worker.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if archivedAt.Valid {
		worker.ArchivedAt = &archivedAt.Time
	}
	return worker, nil
}

func scanOperatorAuditRecord(scanner rowScanner) (OperatorAuditRecord, error) {
	var record OperatorAuditRecord
	var details []byte
	if err := scanner.Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.SessionID,
		&record.PrincipalID,
		&record.OperatorLabel,
		&record.Role,
		&record.Action,
		&record.ResourceType,
		&record.ResourceID,
		&record.Outcome,
		&record.ErrorMessage,
		&details,
		&record.CreatedAt,
	); err != nil {
		return OperatorAuditRecord{}, err
	}
	record.Details = cloneRaw(details)
	return record, nil
}

func scanAgentDeliberation(scanner rowScanner) (AgentDeliberation, error) {
	var item AgentDeliberation
	var plan []byte
	var finalResult []byte
	var canceledAt sql.NullTime
	err := scanner.Scan(&item.ID, &item.WorkspaceID, &item.OwnerID, &item.ParentSessionID, &item.ParentTurnID,
		&item.IdempotencyKey, &item.Objective, &item.Strategy, &item.Status, &item.Phase, &item.MaxParticipants,
		&item.MaxRounds, &item.MaxTokens, &item.MaxSeconds, &item.ModeratorAgentID, &item.ModeratorEnvironmentID,
		&plan, &item.FinalGroupID, &finalResult, &item.CreatedAt, &item.UpdatedAt, &canceledAt, &item.CancelReason)
	if err != nil {
		return AgentDeliberation{}, err
	}
	item.Plan = cloneRaw(plan)
	item.FinalResult = cloneRaw(finalResult)
	if canceledAt.Valid {
		item.CanceledAt = &canceledAt.Time
	}
	return item, nil
}

func scanAgentDeliberationRound(scanner rowScanner) (AgentDeliberationRound, error) {
	var item AgentDeliberationRound
	var summary []byte
	var questions []byte
	var completedAt sql.NullTime
	err := scanner.Scan(&item.DeliberationID, &item.RoundNumber, &item.RoundType, &item.Status, &item.TaskGroupID,
		&item.ModeratorGroupID, &summary, &questions, &item.CreatedAt, &completedAt)
	if err != nil {
		return AgentDeliberationRound{}, err
	}
	item.Summary = cloneRaw(summary)
	item.Questions = cloneRaw(questions)
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	return item, nil
}

func scanAgentDeliberationContribution(scanner rowScanner) (AgentDeliberationContribution, error) {
	var item AgentDeliberationContribution
	var result []byte
	err := scanner.Scan(&item.DeliberationID, &item.RoundNumber, &item.ParticipantIndex, &item.TaskGroupID,
		&item.ItemIndex, &item.SessionID, &item.Status, &item.ContributionText, &result, &item.RetryCount,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return AgentDeliberationContribution{}, err
	}
	item.ContributionJSON = cloneRaw(result)
	return item, nil
}

func scanWorkerWork(scanner rowScanner) (WorkerWork, error) {
	var work WorkerWork
	var workerID sql.NullString
	var environmentID sql.NullString
	var sessionID sql.NullString
	var payload []byte
	var result []byte
	var leaseExpiresAt sql.NullTime
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	err := scanner.Scan(
		&work.ID,
		&work.WorkspaceID,
		&workerID,
		&environmentID,
		&sessionID,
		&work.TurnID,
		&work.WorkType,
		&work.Status,
		&payload,
		&result,
		&work.ErrorMessage,
		&leaseExpiresAt,
		&work.CreatedAt,
		&work.UpdatedAt,
		&startedAt,
		&completedAt,
	)
	if err != nil {
		return WorkerWork{}, err
	}
	work.WorkerID = workerID.String
	work.EnvironmentID = environmentID.String
	work.SessionID = sessionID.String
	work.Payload = cloneRaw(payload)
	work.Result = cloneRaw(result)
	if leaseExpiresAt.Valid {
		work.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if startedAt.Valid {
		work.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		work.CompletedAt = &completedAt.Time
	}
	return work, nil
}

func getSessionInterventionForUpdateTx(ctx context.Context, tx *sql.Tx, sessionID string, turnID string, callID string) (SessionIntervention, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			decision_reason,
			requested_at,
			decided_at,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
		FROM session_interventions
		WHERE session_id = $1 AND turn_id = $2 AND call_id = $3
		FOR UPDATE
	`, sessionID, turnID, callID)
	return scanSessionIntervention(row)
}

func getSessionInterventionTx(ctx context.Context, tx *sql.Tx, sessionID string, turnID string, callID string) (SessionIntervention, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			decision_reason,
			requested_at,
			decided_at,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
		FROM session_interventions
		WHERE session_id = $1 AND turn_id = $2 AND call_id = $3
	`, sessionID, turnID, callID)
	return scanSessionIntervention(row)
}

func getSessionTurnStatusForUpdateTx(ctx context.Context, tx *sql.Tx, sessionID string, turnID string) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM session_turns
		WHERE session_id = $1 AND id = $2
		FOR UPDATE
	`, sessionID, turnID).Scan(&status)
	return status, err
}

func interventionDecisionPayload(intervention SessionIntervention, message string, source string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"turn_id": intervention.TurnID,
		"message": message,
		"data": map[string]any{
			"id":                intervention.CallID,
			"identifier":        intervention.ToolIdentifier,
			"api_name":          intervention.APIName,
			"arguments":         rawJSONObject(intervention.Arguments),
			"kind":              intervention.Kind,
			"request":           rawJSONValue(intervention.Request),
			"response":          rawJSONValue(intervention.Response),
			"status":            intervention.Status,
			"intervention_mode": intervention.InterventionMode,
			"reason":            intervention.Reason,
			"decision_reason":   intervention.DecisionReason,
			"approval_source":   source,
		},
	})
}

func interventionResultEvent(kind, status string) (string, string) {
	if kind == InterventionKindClarification || kind == InterventionKindUploadRequest {
		switch status {
		case InterventionStatusAnswered:
			return EventRuntimeHumanInputSubmitted, "User submitted requested information."
		case InterventionStatusSkipped:
			return EventRuntimeHumanInputSkipped, "User skipped the requested information."
		default:
			return EventRuntimeHumanInputCanceled, "User input request was canceled."
		}
	}
	if kind == InterventionKindPlanApproval {
		if status == InterventionStatusRejected {
			return EventRuntimePlanApprovalRejected, "Task plan rejected by user."
		}
		return EventRuntimePlanApprovalApproved, "Task plan approved by user."
	}
	if status == InterventionStatusRejected {
		return EventRuntimeToolInterventionRejected, "Tool call rejected by user."
	}
	return EventRuntimeToolInterventionApproved, "Tool call approved by user."
}

func (s *PostgresStore) rejectPendingTurnInterventionsTx(ctx context.Context, tx *sql.Tx, sessionID string, turnID string, reason string, now time.Time) ([]Event, error) {
	rows, err := tx.QueryContext(ctx, `
		UPDATE session_interventions
		SET status = CASE
				WHEN kind IN ('clarification', 'upload_request') THEN 'canceled'
				ELSE $3
			END,
			decision_reason = $4,
			decided_at = $5,
			responded_at = CASE
				WHEN kind IN ('clarification', 'upload_request') THEN $5
				ELSE responded_at
			END
		WHERE session_id = $1 AND turn_id = $2 AND status = $6
		RETURNING
			session_id,
			turn_id,
			call_id,
			tool_identifier,
			api_name,
			arguments_json,
			intervention_mode,
			reason,
			status,
			decision_reason,
			requested_at,
			decided_at,
			continuation_messages_json,
			continuation_round,
			kind,
			request_json,
			response_json,
			responded_at,
			expires_at
	`, sessionID, turnID, InterventionStatusRejected, reason, now, InterventionStatusPending)
	if err != nil {
		return nil, err
	}
	interventions := make([]SessionIntervention, 0)
	for rows.Next() {
		intervention, scanErr := scanSessionIntervention(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		interventions = append(interventions, intervention)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	events := make([]Event, 0, len(interventions)*2)
	for _, intervention := range interventions {
		eventType, eventMessage := interventionResultEvent(intervention.Kind, intervention.Status)
		if intervention.Kind == InterventionKindToolApproval {
			eventMessage = "Tool call rejected because the turn was interrupted."
		} else if intervention.Kind == InterventionKindPlanApproval {
			eventMessage = "Task plan rejected because the turn was interrupted."
		} else {
			eventMessage = "User input request canceled because the turn was interrupted."
		}
		payload, err := interventionDecisionPayload(intervention, eventMessage, "user_interrupt")
		if err != nil {
			return nil, err
		}
		event, err := s.appendEventTx(ctx, tx, sessionID, eventType, payload, now)
		if err != nil {
			return nil, err
		}
		events = append(events, event)

		resultPayload, err := interruptedToolResultPayload(intervention)
		if err != nil {
			return nil, err
		}
		resultEvent, err := s.appendEventTx(ctx, tx, sessionID, EventRuntimeToolResult, resultPayload, now)
		if err != nil {
			return nil, err
		}
		events = append(events, resultEvent)
	}
	return events, nil
}

func interruptedToolResultPayload(intervention SessionIntervention) (json.RawMessage, error) {
	message := "Tool call canceled because the turn was interrupted by the user."
	errorType := "tool_canceled"
	if intervention.Kind == InterventionKindPlanApproval {
		message = "Task plan review canceled because the turn was interrupted by the user."
		errorType = "plan_review_canceled"
	} else if intervention.Kind == InterventionKindClarification || intervention.Kind == InterventionKindUploadRequest {
		message = "User input request canceled because the turn was interrupted by the user."
		errorType = "human_input_canceled"
	}
	return json.Marshal(map[string]any{
		"turn_id": intervention.TurnID,
		"message": message,
		"data": map[string]any{
			"protocol_version": "tma.tool_result.v1",
			"id":               intervention.CallID,
			"identifier":       intervention.ToolIdentifier,
			"api_name":         intervention.APIName,
			"status":           "canceled",
			"success":          false,
			"reason":           "user_interrupted",
			"retryable":        false,
			"content":          message,
			"approval_source":  "user_interrupt",
			"error": map[string]any{
				"type":    errorType,
				"message": message,
			},
		},
	})
}

func markSessionTurnResumableTx(ctx context.Context, tx *sql.Tx, sessionID string, turnID string, callID string) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = $4,
			resume_intervention_call_id = $3,
			lease_owner = NULL,
			lease_expires_at = NULL,
			last_heartbeat_at = NULL
		WHERE session_id = $1 AND id = $2 AND status IN ($4, $5, $6)
			AND NOT EXISTS (
				SELECT 1
				FROM session_interventions
				WHERE session_id = $1 AND turn_id = $2 AND status = $7
			)
	`, sessionID, turnID, callID, TurnStatusRunning, TurnStatusWaitingApproval, TurnStatusWaitingHuman, InterventionStatusPending)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func hasPendingTurnInterventionsTx(ctx context.Context, tx *sql.Tx, sessionID, turnID string) (bool, error) {
	var pending bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM session_interventions
			WHERE session_id = $1 AND turn_id = $2 AND status = $3
		)
	`, sessionID, turnID, InterventionStatusPending).Scan(&pending)
	return pending, err
}

func scanSessionIntervention(scanner rowScanner) (SessionIntervention, error) {
	var intervention SessionIntervention
	var arguments []byte
	var continuation []byte
	var request []byte
	var response []byte
	var reason sql.NullString
	var decisionReason sql.NullString
	var decidedAt sql.NullTime
	var respondedAt sql.NullTime
	var expiresAt sql.NullTime

	err := scanner.Scan(
		&intervention.SessionID,
		&intervention.TurnID,
		&intervention.CallID,
		&intervention.ToolIdentifier,
		&intervention.APIName,
		&arguments,
		&intervention.InterventionMode,
		&reason,
		&intervention.Status,
		&decisionReason,
		&intervention.RequestedAt,
		&decidedAt,
		&continuation,
		&intervention.ContinuationRound,
		&intervention.Kind,
		&request,
		&response,
		&respondedAt,
		&expiresAt,
	)
	if err == sql.ErrNoRows {
		return SessionIntervention{}, ErrNotFound
	}
	if err != nil {
		return SessionIntervention{}, err
	}

	intervention.Arguments = cloneRaw(arguments)
	intervention.Continuation = cloneRaw(continuation)
	intervention.Request = cloneRaw(request)
	intervention.Response = cloneRaw(response)
	intervention.Reason = reason.String
	intervention.DecisionReason = decisionReason.String
	if decidedAt.Valid {
		intervention.DecidedAt = &decidedAt.Time
	}
	if respondedAt.Valid {
		intervention.RespondedAt = &respondedAt.Time
	}
	if expiresAt.Valid {
		intervention.ExpiresAt = &expiresAt.Time
	}
	return intervention, nil
}

func getSessionTx(ctx context.Context, tx *sql.Tx, id string) (Session, error) {
	return scanSession(ctx, tx, `
		SELECT id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, parent_session_id, parent_turn_id, spawn_depth, status, title, sandbox_id, runtime_settings_json, runtime_settings_revision, pinned_at, tags_json,
			COALESCE(
				NULLIF((SELECT summary_text FROM session_summaries WHERE session_id = sessions.id), ''),
				(SELECT COALESCE(e.payload_json->'content'->0->>'text', e.payload_json->>'message', e.payload_json->>'summary', e.payload_json->>'text') FROM session_events e WHERE e.session_id = sessions.id AND e.type = 'agent.message' ORDER BY e.seq DESC LIMIT 1),
				''
			), created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
	`, id)
}

func getSessionForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (Session, error) {
	// 涉及状态迁移的事务都通过 FOR UPDATE 锁住 Session，保护状态机一致性。
	return scanSession(ctx, tx, `
		SELECT id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, parent_session_id, parent_turn_id, spawn_depth, status, title, sandbox_id, runtime_settings_json, runtime_settings_revision, pinned_at, tags_json,
			COALESCE(
				NULLIF((SELECT summary_text FROM session_summaries WHERE session_id = sessions.id), ''),
				(SELECT COALESCE(e.payload_json->'content'->0->>'text', e.payload_json->>'message', e.payload_json->>'summary', e.payload_json->>'text') FROM session_events e WHERE e.session_id = sessions.id AND e.type = 'agent.message' ORDER BY e.seq DESC LIMIT 1),
				''
			), created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
		FOR UPDATE
	`, id)
}

func scanSession(ctx context.Context, tx *sql.Tx, query string, id string) (Session, error) {
	var session Session
	var title sql.NullString
	var parentSessionID sql.NullString
	var parentTurnID sql.NullString
	var sandboxID sql.NullString
	var runtimeSettings []byte
	var pinnedAt sql.NullTime
	var tags []byte
	var archivedAt sql.NullTime

	err := tx.QueryRowContext(ctx, query, id).Scan(
		&session.ID,
		&session.WorkspaceID,
		&session.OwnerID,
		&session.AgentID,
		&session.AgentConfigVersion,
		&session.EnvironmentID,
		&parentSessionID,
		&parentTurnID,
		&session.SpawnDepth,
		&session.Status,
		&title,
		&sandboxID,
		&runtimeSettings,
		&session.RuntimeSettingsRevision,
		&pinnedAt,
		&tags,
		&session.SummaryText,
		&session.CreatedBy,
		&session.CreatedAt,
		&archivedAt,
	)
	if err == sql.ErrNoRows {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}

	session.Title = title.String
	session.ParentSessionID = parentSessionID.String
	session.ParentTurnID = parentTurnID.String
	session.SandboxID = sandboxID.String
	session.RuntimeSettings = cloneRaw(runtimeSettings)
	if pinnedAt.Valid {
		session.PinnedAt = &pinnedAt.Time
	}
	if err := json.Unmarshal(tags, &session.Tags); err != nil {
		return Session{}, fmt.Errorf("decode session tags: %w", err)
	}
	if archivedAt.Valid {
		session.ArchivedAt = &archivedAt.Time
	}

	return session, nil
}

func scanSessionRow(scanner interface {
	Scan(dest ...any) error
}) (Session, error) {
	var session Session
	var title sql.NullString
	var parentSessionID sql.NullString
	var parentTurnID sql.NullString
	var sandboxID sql.NullString
	var runtimeSettings []byte
	var pinnedAt sql.NullTime
	var tags []byte
	var archivedAt sql.NullTime
	if err := scanner.Scan(
		&session.ID,
		&session.WorkspaceID,
		&session.OwnerID,
		&session.AgentID,
		&session.AgentConfigVersion,
		&session.EnvironmentID,
		&parentSessionID,
		&parentTurnID,
		&session.SpawnDepth,
		&session.Status,
		&title,
		&sandboxID,
		&runtimeSettings,
		&session.RuntimeSettingsRevision,
		&pinnedAt,
		&tags,
		&session.SummaryText,
		&session.CreatedBy,
		&session.CreatedAt,
		&archivedAt,
	); err != nil {
		return Session{}, err
	}
	session.Title = title.String
	session.ParentSessionID = parentSessionID.String
	session.ParentTurnID = parentTurnID.String
	session.SandboxID = sandboxID.String
	session.RuntimeSettings = cloneRaw(runtimeSettings)
	if pinnedAt.Valid {
		session.PinnedAt = &pinnedAt.Time
	}
	if err := json.Unmarshal(tags, &session.Tags); err != nil {
		return Session{}, fmt.Errorf("decode session tags: %w", err)
	}
	if archivedAt.Valid {
		session.ArchivedAt = &archivedAt.Time
	}
	return session, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableRaw(value json.RawMessage) any {
	if len(value) == 0 {
		return json.RawMessage(`null`)
	}
	return value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return cloneRaw(value)
}

func metadataJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return cloneRaw(value)
}

func workerLeaseExpiresAt(now time.Time, leaseSeconds int) time.Time {
	if leaseSeconds <= 0 {
		leaseSeconds = 60
	}
	return now.Add(time.Duration(leaseSeconds) * time.Second)
}

func reapLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func rawJSONObject(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func rawJSONValue(raw json.RawMessage) any {
	return rawJSONObject(raw)
}
