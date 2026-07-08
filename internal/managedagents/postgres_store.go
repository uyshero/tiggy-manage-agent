package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db  *sql.DB
	hub *eventHub
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

	return &PostgresStore{db: db, hub: newEventHub()}, nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
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

	now := time.Now().UTC()

	var provider LLMProvider
	err := s.db.QueryRowContext(context.Background(), `
		INSERT INTO llm_providers (id, provider_type, base_url, api_key_env, enabled, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			provider_type = EXCLUDED.provider_type,
			base_url = EXCLUDED.base_url,
			api_key_env = EXCLUDED.api_key_env,
			enabled = EXCLUDED.enabled
		RETURNING id, provider_type, base_url, api_key_env, enabled, created_at
	`, input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled, now).Scan(
		&provider.ID,
		&provider.ProviderType,
		&provider.BaseURL,
		&provider.APIKeyEnv,
		&provider.Enabled,
		&provider.CreatedAt,
	)
	if err != nil {
		return LLMProvider{}, err
	}
	return provider, nil
}

func (s *PostgresStore) GetLLMProvider(id string) (LLMProvider, error) {
	if id == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}

	var provider LLMProvider
	err := s.db.QueryRowContext(context.Background(), `
		SELECT id, provider_type, base_url, api_key_env, enabled, created_at
		FROM llm_providers
		WHERE id = $1
	`, id).Scan(
		&provider.ID,
		&provider.ProviderType,
		&provider.BaseURL,
		&provider.APIKeyEnv,
		&provider.Enabled,
		&provider.CreatedAt,
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
		SELECT id, provider_type, base_url, api_key_env, enabled, created_at
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
			&provider.CreatedAt,
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

	var provider LLMProvider
	err := s.db.QueryRowContext(context.Background(), `
		UPDATE llm_providers
		SET enabled = $2
		WHERE id = $1
		RETURNING id, provider_type, base_url, api_key_env, enabled, created_at
	`, id, enabled).Scan(
		&provider.ID,
		&provider.ProviderType,
		&provider.BaseURL,
		&provider.APIKeyEnv,
		&provider.Enabled,
		&provider.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return LLMProvider{}, ErrNotFound
	}
	if err != nil {
		return LLMProvider{}, err
	}
	return provider, nil
}

func (s *PostgresStore) UpsertLLMModel(input UpsertLLMModelInput) (LLMModel, error) {
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

	now := time.Now().UTC()
	var model LLMModel
	err := s.db.QueryRowContext(context.Background(), `
		INSERT INTO llm_models (provider_id, model, context_window_tokens, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (provider_id, model) DO UPDATE SET
			context_window_tokens = EXCLUDED.context_window_tokens,
			updated_at = EXCLUDED.updated_at
		RETURNING provider_id, model, context_window_tokens, created_at, updated_at
	`, input.ProviderID, input.Model, input.ContextWindowTokens, now).Scan(
		&model.ProviderID,
		&model.Model,
		&model.ContextWindowTokens,
		&model.CreatedAt,
		&model.UpdatedAt,
	)
	if err != nil {
		return LLMModel{}, err
	}
	return model, nil
}

func (s *PostgresStore) ListLLMModels(providerID string) ([]LLMModel, error) {
	query := `
		SELECT provider_id, model, context_window_tokens, created_at, updated_at
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
		if err := rows.Scan(
			&model.ProviderID,
			&model.Model,
			&model.ContextWindowTokens,
			&model.CreatedAt,
			&model.UpdatedAt,
		); err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return models, nil
}

func (s *PostgresStore) CreateAgent(input CreateAgentInput) (Agent, error) {
	if input.Name == "" {
		return Agent{}, fmt.Errorf("%w: agent name is required", ErrInvalid)
	}
	llmProvider := agentLLMProvider(input)
	llmModel := agentLLMModel(input)
	if llmModel == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_model is required", ErrInvalid)
	}
	if err := s.validateLLMProvider(llmProvider); err != nil {
		return Agent{}, err
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()

	id, err := nextSequenceID(ctx, tx, "agt", "tma_agent_id_seq")
	if err != nil {
		return Agent{}, err
	}

	workspaceID := defaultString(input.WorkspaceID, DefaultWorkspaceID)
	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agents (id, workspace_id, name, current_config_version, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, id, workspaceID, input.Name, 1, now)
	if err != nil {
		return Agent{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model, system, tools_json, skills_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, id, 1, llmProvider, llmModel, input.System, nullableRaw(input.Tools), nullableRaw(input.Skills), now)
	if err != nil {
		return Agent{}, err
	}

	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}

	return Agent{
		ID:                   id,
		WorkspaceID:          workspaceID,
		Name:                 input.Name,
		CurrentConfigVersion: 1,
		ConfigVersion: AgentConfigVersion{
			Version:     1,
			LLMProvider: llmProvider,
			LLMModel:    llmModel,
			System:      input.System,
			Tools:       cloneRaw(input.Tools),
			Skills:      cloneRaw(input.Skills),
			CreatedAt:   now,
		},
		CreatedAt: now,
	}, nil
}

func (s *PostgresStore) GetAgent(id string) (Agent, error) {
	if id == "" {
		return Agent{}, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}

	var agent Agent
	var tools []byte
	var skills []byte
	var archivedAt sql.NullTime
	err := s.db.QueryRowContext(context.Background(), `
		SELECT
			a.id,
			a.workspace_id,
			a.name,
			a.current_config_version,
			a.archived_at,
			a.created_at,
			av.version,
			av.llm_provider,
			av.llm_model,
			av.system,
			av.tools_json,
			av.skills_json,
			av.created_at
		FROM agents a
		JOIN agent_config_versions av
			ON av.agent_id = a.id
			AND av.version = a.current_config_version
		WHERE a.id = $1
	`, id).Scan(
		&agent.ID,
		&agent.WorkspaceID,
		&agent.Name,
		&agent.CurrentConfigVersion,
		&archivedAt,
		&agent.CreatedAt,
		&agent.ConfigVersion.Version,
		&agent.ConfigVersion.LLMProvider,
		&agent.ConfigVersion.LLMModel,
		&agent.ConfigVersion.System,
		&tools,
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
	agent.ConfigVersion.Skills = cloneRaw(skills)
	return agent, nil
}

func (s *PostgresStore) ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}

	if _, err := s.GetAgent(agentID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(context.Background(), `
		SELECT version, llm_provider, llm_model, system, tools_json, skills_json, created_at
		FROM agent_config_versions
		WHERE agent_id = $1
		ORDER BY version
	`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []AgentConfigVersion
	for rows.Next() {
		var version AgentConfigVersion
		var tools []byte
		var skills []byte
		if err := rows.Scan(
			&version.Version,
			&version.LLMProvider,
			&version.LLMModel,
			&version.System,
			&tools,
			&skills,
			&version.CreatedAt,
		); err != nil {
			return nil, err
		}
		version.Tools = cloneRaw(tools)
		version.Skills = cloneRaw(skills)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}

func (s *PostgresStore) CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error) {
	if input.AgentID == "" {
		return Agent{}, fmt.Errorf("%w: agent id is required", ErrInvalid)
	}
	if input.LLMProvider == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_provider is required", ErrInvalid)
	}
	if input.LLMModel == "" {
		return Agent{}, fmt.Errorf("%w: agent llm_model is required", ErrInvalid)
	}
	if err := s.validateLLMProvider(input.LLMProvider); err != nil {
		return Agent{}, err
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()

	var agent Agent
	var currentVersion int
	var archivedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, current_config_version, archived_at, created_at
		FROM agents
		WHERE id = $1
		FOR UPDATE
	`, input.AgentID).Scan(
		&agent.ID,
		&agent.WorkspaceID,
		&agent.Name,
		&currentVersion,
		&archivedAt,
		&agent.CreatedAt,
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

	nextVersion := currentVersion + 1
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_config_versions (agent_id, version, llm_provider, llm_model, system, tools_json, skills_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, input.AgentID, nextVersion, input.LLMProvider, input.LLMModel, input.System, nullableRaw(input.Tools), nullableRaw(input.Skills), now)
	if err != nil {
		return Agent{}, err
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

func (s *PostgresStore) CreateEnvironment(input CreateEnvironmentInput) (Environment, error) {
	if input.Name == "" {
		return Environment{}, fmt.Errorf("%w: environment name is required", ErrInvalid)
	}

	ctx := context.Background()
	id, err := nextSequenceID(ctx, s.db, "env", "tma_environment_id_seq")
	if err != nil {
		return Environment{}, err
	}

	workspaceID := defaultString(input.WorkspaceID, DefaultWorkspaceID)
	config := input.Config
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO environments (id, workspace_id, name, config_json, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, id, workspaceID, input.Name, config, now)
	if err != nil {
		return Environment{}, err
	}

	return Environment{
		ID:          id,
		WorkspaceID: workspaceID,
		Name:        input.Name,
		Config:      cloneRaw(config),
		CreatedAt:   now,
	}, nil
}

func (s *PostgresStore) CreateSession(input CreateSessionInput) (Session, error) {
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

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()

	var agentWorkspaceID string
	var agentConfigVersion int
	err = tx.QueryRowContext(ctx, `
		SELECT workspace_id, current_config_version FROM agents WHERE id = $1 AND archived_at IS NULL
	`, agentID).Scan(&agentWorkspaceID, &agentConfigVersion)
	if err == sql.ErrNoRows {
		return Session{}, fmt.Errorf("%w: agent %s", ErrNotFound, agentID)
	}
	if err != nil {
		return Session{}, err
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

	id, err := nextSequenceID(ctx, tx, "sesn", "tma_session_id_seq")
	if err != nil {
		return Session{}, err
	}

	now := time.Now().UTC()
	session := Session{
		ID:                 id,
		WorkspaceID:        workspaceID,
		AgentID:            agentID,
		AgentConfigVersion: agentConfigVersion,
		EnvironmentID:      input.EnvironmentID,
		Status:             SessionStatusIdle,
		Title:              input.Title,
		RuntimeSettings:    json.RawMessage(`{}`),
		CreatedBy:          defaultString(input.CreatedBy, "system"),
		CreatedAt:          now,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, workspace_id, agent_id, agent_config_version, environment_id, status, title, runtime_settings_json, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, session.ID, session.WorkspaceID, session.AgentID, session.AgentConfigVersion, session.EnvironmentID, session.Status, nullableString(session.Title), session.RuntimeSettings, session.CreatedBy, session.CreatedAt)
	if err != nil {
		return Session{}, err
	}

	if _, err := s.appendEventTx(ctx, tx, id, EventSessionStatusProvisioning, mustRaw(`{"status":"provisioning"}`), now); err != nil {
		return Session{}, err
	}
	if _, err := s.appendEventTx(ctx, tx, id, EventSessionStatusIdle, mustRaw(`{"status":"idle"}`), now); err != nil {
		return Session{}, err
	}

	if err := tx.Commit(); err != nil {
		return Session{}, err
	}

	return session, nil
}

func (s *PostgresStore) ResolveAgentRuntimeConfig(sessionID string) (AgentRuntimeConfig, error) {
	var config AgentRuntimeConfig
	var tools []byte
	var skills []byte
	var runtimeSettings []byte
	var providerType sql.NullString
	var baseURL sql.NullString
	var apiKeyEnv sql.NullString
	var enabled sql.NullBool
	var summaryText sql.NullString
	var summarySourceUntilSeq sql.NullInt64

	err := s.db.QueryRowContext(context.Background(), `
		SELECT
			s.id,
			s.workspace_id,
			s.agent_id,
			s.agent_config_version,
			av.llm_provider,
			av.llm_model,
			av.system,
			s.runtime_settings_json,
			av.tools_json,
			av.skills_json,
			lp.provider_type,
			lp.base_url,
			lp.api_key_env,
			lp.enabled,
			COALESCE(lm.context_window_tokens, $2),
			ss.summary_text,
			ss.source_until_seq
		FROM sessions s
		JOIN agent_config_versions av
			ON av.agent_id = s.agent_id
			AND av.version = s.agent_config_version
		LEFT JOIN llm_providers lp
			ON lp.id = av.llm_provider
		LEFT JOIN llm_models lm
			ON lm.provider_id = av.llm_provider
			AND lm.model = av.llm_model
		LEFT JOIN session_summaries ss
			ON ss.session_id = s.id
		WHERE s.id = $1
	`, sessionID, DefaultContextWindowTokens).Scan(
		&config.SessionID,
		&config.WorkspaceID,
		&config.AgentID,
		&config.AgentConfigVersion,
		&config.LLMProvider,
		&config.LLMModel,
		&config.System,
		&runtimeSettings,
		&tools,
		&skills,
		&providerType,
		&baseURL,
		&apiKeyEnv,
		&enabled,
		&config.ContextWindowTokens,
		&summaryText,
		&summarySourceUntilSeq,
	)
	if err == sql.ErrNoRows {
		return AgentRuntimeConfig{}, ErrNotFound
	}
	if err != nil {
		return AgentRuntimeConfig{}, err
	}

	config.RuntimeSettings = cloneRaw(runtimeSettings)
	config.Tools = cloneRaw(tools)
	config.Skills = cloneRaw(skills)
	config.LLMProviderType = providerType.String
	config.LLMBaseURL = baseURL.String
	config.LLMAPIKeyEnv = apiKeyEnv.String
	config.SummaryText = summaryText.String
	config.SummarySourceUntilSeq = summarySourceUntilSeq.Int64
	if enabled.Valid && !enabled.Bool {
		return AgentRuntimeConfig{}, fmt.Errorf("%w: llm provider %s is disabled", ErrInvalid, config.LLMProvider)
	}
	return config, nil
}

func (s *PostgresStore) GetSessionSummary(sessionID string) (SessionSummary, error) {
	if sessionID == "" {
		return SessionSummary{}, fmt.Errorf("%w: summary session_id is required", ErrInvalid)
	}
	var summary SessionSummary
	err := s.db.QueryRowContext(context.Background(), `
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
	return summary, nil
}

func (s *PostgresStore) SaveSessionSummary(sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error) {
	if sessionID == "" {
		return SessionSummary{}, fmt.Errorf("%w: summary session_id is required", ErrInvalid)
	}
	if input.SummaryText == "" {
		return SessionSummary{}, fmt.Errorf("%w: summary_text is required", ErrInvalid)
	}
	if input.SourceUntilSeq < 0 {
		return SessionSummary{}, fmt.Errorf("%w: source_until_seq must be non-negative", ErrInvalid)
	}
	if _, err := s.GetSession(sessionID); err != nil {
		return SessionSummary{}, err
	}

	now := time.Now().UTC()
	var summary SessionSummary
	err := s.db.QueryRowContext(context.Background(), `
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
	return summary, nil
}

func (s *PostgresStore) UpsertSessionSummary(sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error) {
	if sessionID == "" {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: summary session_id is required", ErrInvalid)
	}
	if input.SummaryText == "" {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: summary_text is required", ErrInvalid)
	}
	if input.SourceUntilSeq < 0 {
		return UpsertSessionSummaryResult{}, fmt.Errorf("%w: source_until_seq must be non-negative", ErrInvalid)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpsertSessionSummaryResult{}, err
	}
	defer tx.Rollback()

	session, err := getSessionTx(ctx, tx, sessionID)
	if err != nil {
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
	var session Session
	var title sql.NullString
	var sandboxID sql.NullString
	var runtimeSettings []byte
	var archivedAt sql.NullTime

	err := s.db.QueryRowContext(context.Background(), `
		SELECT id, workspace_id, agent_id, agent_config_version, environment_id, status, title, sandbox_id, runtime_settings_json, created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
	`, id).Scan(
		&session.ID,
		&session.WorkspaceID,
		&session.AgentID,
		&session.AgentConfigVersion,
		&session.EnvironmentID,
		&session.Status,
		&title,
		&sandboxID,
		&runtimeSettings,
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
	session.SandboxID = sandboxID.String
	session.RuntimeSettings = cloneRaw(runtimeSettings)
	if archivedAt.Valid {
		session.ArchivedAt = &archivedAt.Time
	}

	return session, nil
}

func (s *PostgresStore) UpdateSessionRuntimeSettings(id string, input UpdateSessionRuntimeSettingsInput) (Session, error) {
	if len(input.RuntimeSettings) == 0 {
		input.RuntimeSettings = json.RawMessage(`{}`)
	}
	if !json.Valid(input.RuntimeSettings) {
		return Session{}, fmt.Errorf("%w: runtime_settings must be valid JSON", ErrInvalid)
	}
	if _, err := s.GetSession(id); err != nil {
		return Session{}, err
	}
	if _, err := s.db.ExecContext(context.Background(), `
		UPDATE sessions
		SET runtime_settings_json = $2
		WHERE id = $1
	`, id, input.RuntimeSettings); err != nil {
		return Session{}, err
	}
	return s.GetSession(id)
}

func (s *PostgresStore) SaveSessionIntervention(sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error) {
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

	now := time.Now().UTC()
	expiresAt := now.Add(30 * time.Minute)
	row := s.db.QueryRowContext(context.Background(), `
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
			expires_at,
			decided_at,
			decision_reason,
			continuation_messages_json,
			continuation_round
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULL, NULL, $12, $13)
		ON CONFLICT (session_id, turn_id, call_id) DO UPDATE
		SET
			tool_identifier = EXCLUDED.tool_identifier,
			api_name = EXCLUDED.api_name,
			arguments_json = EXCLUDED.arguments_json,
			intervention_mode = EXCLUDED.intervention_mode,
			reason = EXCLUDED.reason,
			status = EXCLUDED.status,
			requested_at = EXCLUDED.requested_at,
			expires_at = EXCLUDED.expires_at,
			decided_at = NULL,
			decision_reason = NULL,
			continuation_messages_json = EXCLUDED.continuation_messages_json,
			continuation_round = EXCLUDED.continuation_round
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
			expires_at,
			decided_at,
			continuation_messages_json,
			continuation_round
	`, sessionID, input.TurnID, input.CallID, input.ToolIdentifier, input.APIName, nullableRaw(input.Arguments), input.InterventionMode, input.Reason, InterventionStatusPending, now, expiresAt, nullableRaw(input.Continuation), input.ContinuationRound)
	intervention, err := scanSessionIntervention(row)
	if err != nil {
		return SessionIntervention{}, err
	}
	return intervention, nil
}

func (s *PostgresStore) ListSessionInterventions(sessionID string, status string) ([]SessionIntervention, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: intervention session_id is required", ErrInvalid)
	}
	if _, err := s.GetSession(sessionID); err != nil {
		return nil, err
	}
	normalizedStatus := normalizeInterventionStatus(status)
	if status != "" && normalizedStatus == "" {
		return nil, fmt.Errorf("%w: unsupported intervention status %q", ErrInvalid, status)
	}

	rows, err := s.db.QueryContext(context.Background(), `
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
			expires_at,
			decided_at,
			continuation_messages_json,
			continuation_round
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
	return interventions, rows.Err()
}

func (s *PostgresStore) DecideSessionIntervention(sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error) {
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
	if status != InterventionStatusApproved && status != InterventionStatusRejected {
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention decision must be approved or rejected", ErrInvalid)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	defer tx.Rollback()

	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
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
		return DecideSessionInterventionResult{}, fmt.Errorf("%w: intervention %s is already %s", ErrInvalid, input.CallID, current.Status)
	}

	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx, `
		UPDATE session_interventions
		SET status = $4, decision_reason = $5, decided_at = $6
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
			expires_at,
			decided_at,
			continuation_messages_json,
			continuation_round
	`, sessionID, input.TurnID, input.CallID, status, input.DecisionReason, now)
	decided, err := scanSessionIntervention(row)
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}

	eventType := EventRuntimeToolInterventionApproved
	message := "Tool call approved by user."
	if status == InterventionStatusRejected {
		eventType = EventRuntimeToolInterventionRejected
		message = "Tool call rejected by user."
	}
	payload, err := json.Marshal(map[string]any{
		"turn_id": decided.TurnID,
		"message": message,
		"data": map[string]any{
			"id":                decided.CallID,
			"identifier":        decided.ToolIdentifier,
			"api_name":          decided.APIName,
			"arguments":         rawJSONObject(decided.Arguments),
			"intervention_mode": decided.InterventionMode,
			"reason":            decided.Reason,
			"decision_reason":   decided.DecisionReason,
			"approval_source":   "user",
		},
	})
	if err != nil {
		return DecideSessionInterventionResult{}, err
	}
	event, err := s.appendEventTx(ctx, tx, sessionID, eventType, payload, now)
	if err != nil {
		return DecideSessionInterventionResult{}, err
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
	if sessionID == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalid)
	}
	if turnID == "" {
		return fmt.Errorf("%w: turn_id is required", ErrInvalid)
	}

	result, err := s.db.ExecContext(context.Background(), `
		UPDATE session_turns
		SET status = $3
		WHERE session_id = $1 AND id = $2 AND status IN ($3, $4)
	`, sessionID, turnID, TurnStatusWaitingApproval, TurnStatusRunning)
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

func (s *PostgresStore) ArchiveSession(id string) (Session, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()

	session, err := getSessionForUpdateTx(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if session.Status == SessionStatusTerminated {
		return session, nil
	}

	now := time.Now().UTC()
	session.Status = SessionStatusTerminated
	session.ArchivedAt = &now

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions SET status = $2, archived_at = $3 WHERE id = $1
	`, id, session.Status, now)
	if err != nil {
		return Session{}, err
	}

	event, err := s.appendEventTx(ctx, tx, id, EventSessionStatusTerminated, mustRaw(`{"status":"terminated"}`), now)
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
	result, err := s.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE id = $1`, id)
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

	s.hub.closeSession(id)
	return nil
}

func (s *PostgresStore) AppendEvents(sessionID string, inputs []AppendEventInput) ([]Event, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: at least one event is required", ErrInvalid)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 锁住 Session 行，串行化同一 Session 下的 seq / turn_id / status 更新。
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
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
	if input.Type == "" {
		return nil, fmt.Errorf("%w: event type is required", ErrInvalid)
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
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
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// completion 是异步到达的，必须重新锁 Session 并确认它仍在运行。
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
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
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// failure 也可能异步到达，必须确认失败的是当前 running turn。
	session, err := getSessionForUpdateTx(ctx, tx, sessionID)
	if err != nil {
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

	ctx := context.Background()
	id, err := nextSequenceID(ctx, s.db, "llmu", "tma_llm_usage_id_seq")
	if err != nil {
		return LLMUsageRecord{}, err
	}
	now := time.Now().UTC()

	var record LLMUsageRecord
	err = s.db.QueryRowContext(ctx, `
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
	return record, nil
}

func (s *PostgresStore) GetSessionLLMUsage(sessionID string) (LLMUsageReport, error) {
	if sessionID == "" {
		return LLMUsageReport{}, fmt.Errorf("%w: usage session_id is required", ErrInvalid)
	}
	if _, err := s.GetSession(sessionID); err != nil {
		return LLMUsageReport{}, err
	}

	rows, err := s.db.QueryContext(context.Background(), `
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
	return report, nil
}

func (s *PostgresStore) ListLLMUsage(input ListLLMUsageInput) (LLMUsageAggregateReport, error) {
	groupBy := normalizeLLMUsageGroupBy(input.GroupBy)
	if groupBy == "" {
		return LLMUsageAggregateReport{}, fmt.Errorf("%w: unsupported usage group_by %q", ErrInvalid, input.GroupBy)
	}
	input.GroupBy = groupBy

	rows, err := s.db.QueryContext(context.Background(), `
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

func (s *PostgresStore) ListEvents(sessionID string, afterSeq int64) ([]Event, error) {
	if _, err := s.GetSession(sessionID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, session_id, seq, type, payload_json, created_at
		FROM session_events
		WHERE session_id = $1 AND seq > $2
		ORDER BY seq ASC
	`, sessionID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.SessionID, &event.Seq, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) ListConversationMessages(sessionID string, beforeSeq int64) ([]ConversationMessage, error) {
	if _, err := s.GetSession(sessionID); err != nil {
		return nil, err
	}
	if beforeSeq <= 0 {
		return []ConversationMessage{}, nil
	}

	rows, err := s.db.QueryContext(context.Background(), `
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
	return messages, rows.Err()
}

func (s *PostgresStore) SubscribeEvents(sessionID string) (<-chan Event, func(), error) {
	if _, err := s.GetSession(sessionID); err != nil {
		return nil, nil, err
	}

	ch, cancel := s.hub.subscribe(sessionID)
	return ch, cancel, nil
}

func (s *PostgresStore) applyEventTx(ctx context.Context, tx *sql.Tx, session *Session, input AppendEventInput, now time.Time) ([]Event, error) {
	switch input.Type {
	case EventUserMessage:
		if session.Status != SessionStatusIdle {
			return nil, fmt.Errorf("%w: user.message requires idle session", ErrInvalid)
		}

		// user.message 开启一个新的 turn，并立刻把 Session 切到 running。
		turnID, err := nextTurnID(ctx, tx, session.ID)
		if err != nil {
			return nil, err
		}
		if err := createTurnTx(ctx, tx, session.ID, turnID, now); err != nil {
			return nil, err
		}
		session.Status = SessionStatusRunning
		statusEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusRunning, statusPayload("running", turnID), now)
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

		return []Event{statusEvent, userEvent}, nil

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
		session.Status = SessionStatusIdle
		idleEvent, err := s.appendEventTx(ctx, tx, session.ID, EventSessionStatusIdle, statusPayload("idle", turnID), now)
		if err != nil {
			return nil, err
		}
		if err := interruptTurnTx(ctx, tx, session.ID, turnID, now); err != nil {
			return nil, err
		}

		return []Event{userEvent, interruptingEvent, idleEvent}, nil

	default:
		event, err := s.appendEventTx(ctx, tx, session.ID, input.Type, cloneRaw(input.Payload), now)
		if err != nil {
			return nil, err
		}
		return []Event{event}, nil
	}
}

func (s *PostgresStore) appendEventTx(ctx context.Context, tx *sql.Tx, sessionID, eventType string, payload json.RawMessage, now time.Time) (Event, error) {
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
		Seq:       seq,
		Type:      eventType,
		Payload:   cloneRaw(payload),
		CreatedAt: now,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, seq, type, payload_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.ID, event.SessionID, event.Seq, event.Type, nullableRaw(event.Payload), event.CreatedAt)
	if err != nil {
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
		WHERE session_id = $1 AND status IN ('running', 'waiting_approval')
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

func createTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO session_turns (session_id, id, status, started_at)
		VALUES ($1, $2, 'running', $3)
	`, sessionID, turnID, now)
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
		SET status = 'completed', ended_at = $3
		WHERE session_id = $1 AND id = $2 AND status IN ('running', 'waiting_approval')
	`, sessionID, turnID, now)
	return err
}

func interruptTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'interrupted', interrupt_requested_at = $3, ended_at = $3
		WHERE session_id = $1 AND id = $2 AND status IN ('running', 'waiting_approval')
	`, sessionID, turnID, now)
	return err
}

func failTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID, reason string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'failed', error_message = $3, ended_at = $4
		WHERE session_id = $1 AND id = $2 AND status IN ('running', 'waiting_approval')
	`, sessionID, turnID, nullableString(reason), now)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
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
			expires_at,
			decided_at,
			continuation_messages_json,
			continuation_round
		FROM session_interventions
		WHERE session_id = $1 AND turn_id = $2 AND call_id = $3
		FOR UPDATE
	`, sessionID, turnID, callID)
	return scanSessionIntervention(row)
}

func scanSessionIntervention(scanner rowScanner) (SessionIntervention, error) {
	var intervention SessionIntervention
	var arguments []byte
	var continuation []byte
	var reason sql.NullString
	var decisionReason sql.NullString
	var expiresAt sql.NullTime
	var decidedAt sql.NullTime

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
		&expiresAt,
		&decidedAt,
		&continuation,
		&intervention.ContinuationRound,
	)
	if err == sql.ErrNoRows {
		return SessionIntervention{}, ErrNotFound
	}
	if err != nil {
		return SessionIntervention{}, err
	}

	intervention.Arguments = cloneRaw(arguments)
	intervention.Continuation = cloneRaw(continuation)
	intervention.Reason = reason.String
	intervention.DecisionReason = decisionReason.String
	if expiresAt.Valid {
		intervention.ExpiresAt = &expiresAt.Time
	}
	if decidedAt.Valid {
		intervention.DecidedAt = &decidedAt.Time
	}
	return intervention, nil
}

func getSessionTx(ctx context.Context, tx *sql.Tx, id string) (Session, error) {
	return scanSession(ctx, tx, `
		SELECT id, workspace_id, agent_id, agent_config_version, environment_id, status, title, sandbox_id, runtime_settings_json, created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
	`, id)
}

func getSessionForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (Session, error) {
	// 涉及状态迁移的事务都通过 FOR UPDATE 锁住 Session，保护状态机一致性。
	return scanSession(ctx, tx, `
		SELECT id, workspace_id, agent_id, agent_config_version, environment_id, status, title, sandbox_id, runtime_settings_json, created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
		FOR UPDATE
	`, id)
}

func scanSession(ctx context.Context, tx *sql.Tx, query string, id string) (Session, error) {
	var session Session
	var title sql.NullString
	var sandboxID sql.NullString
	var runtimeSettings []byte
	var archivedAt sql.NullTime

	err := tx.QueryRowContext(ctx, query, id).Scan(
		&session.ID,
		&session.WorkspaceID,
		&session.AgentID,
		&session.AgentConfigVersion,
		&session.EnvironmentID,
		&session.Status,
		&title,
		&sandboxID,
		&runtimeSettings,
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
	session.SandboxID = sandboxID.String
	session.RuntimeSettings = cloneRaw(runtimeSettings)
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
