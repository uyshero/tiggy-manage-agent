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
		CreatedBy:          defaultString(input.CreatedBy, "system"),
		CreatedAt:          now,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, workspace_id, agent_id, agent_config_version, environment_id, status, title, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, session.ID, session.WorkspaceID, session.AgentID, session.AgentConfigVersion, session.EnvironmentID, session.Status, nullableString(session.Title), session.CreatedBy, session.CreatedAt)
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
	var providerType sql.NullString
	var baseURL sql.NullString
	var apiKeyEnv sql.NullString
	var enabled sql.NullBool

	err := s.db.QueryRowContext(context.Background(), `
		SELECT
			s.id,
			s.workspace_id,
			s.agent_id,
			s.agent_config_version,
			av.llm_provider,
			av.llm_model,
			av.system,
			av.tools_json,
			av.skills_json,
			lp.provider_type,
			lp.base_url,
			lp.api_key_env,
			lp.enabled
		FROM sessions s
		JOIN agent_config_versions av
			ON av.agent_id = s.agent_id
			AND av.version = s.agent_config_version
		LEFT JOIN llm_providers lp
			ON lp.id = av.llm_provider
		WHERE s.id = $1
	`, sessionID).Scan(
		&config.SessionID,
		&config.WorkspaceID,
		&config.AgentID,
		&config.AgentConfigVersion,
		&config.LLMProvider,
		&config.LLMModel,
		&config.System,
		&tools,
		&skills,
		&providerType,
		&baseURL,
		&apiKeyEnv,
		&enabled,
	)
	if err == sql.ErrNoRows {
		return AgentRuntimeConfig{}, ErrNotFound
	}
	if err != nil {
		return AgentRuntimeConfig{}, err
	}

	config.Tools = cloneRaw(tools)
	config.Skills = cloneRaw(skills)
	config.LLMProviderType = providerType.String
	config.LLMBaseURL = baseURL.String
	config.LLMAPIKeyEnv = apiKeyEnv.String
	if enabled.Valid && !enabled.Bool {
		return AgentRuntimeConfig{}, fmt.Errorf("%w: llm provider %s is disabled", ErrInvalid, config.LLMProvider)
	}
	return config, nil
}

func (s *PostgresStore) GetSession(id string) (Session, error) {
	var session Session
	var title sql.NullString
	var sandboxID sql.NullString
	var archivedAt sql.NullTime

	err := s.db.QueryRowContext(context.Background(), `
		SELECT id, workspace_id, agent_id, agent_config_version, environment_id, status, title, sandbox_id, created_by, created_at, archived_at
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
	if archivedAt.Valid {
		session.ArchivedAt = &archivedAt.Time
	}

	return session, nil
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
		WHERE session_id = $1 AND status = 'running'
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
		WHERE session_id = $1 AND id = $2 AND status = 'running'
	`, sessionID, turnID, now)
	return err
}

func interruptTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'interrupted', interrupt_requested_at = $3, ended_at = $3
		WHERE session_id = $1 AND id = $2 AND status = 'running'
	`, sessionID, turnID, now)
	return err
}

func failTurnTx(ctx context.Context, tx *sql.Tx, sessionID, turnID, reason string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_turns
		SET status = 'failed', error_message = $3, ended_at = $4
		WHERE session_id = $1 AND id = $2 AND status = 'running'
	`, sessionID, turnID, nullableString(reason), now)
	return err
}

func getSessionTx(ctx context.Context, tx *sql.Tx, id string) (Session, error) {
	return scanSession(ctx, tx, `
		SELECT id, workspace_id, agent_id, agent_config_version, environment_id, status, title, sandbox_id, created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
	`, id)
}

func getSessionForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (Session, error) {
	// 涉及状态迁移的事务都通过 FOR UPDATE 锁住 Session，保护状态机一致性。
	return scanSession(ctx, tx, `
		SELECT id, workspace_id, agent_id, agent_config_version, environment_id, status, title, sandbox_id, created_by, created_at, archived_at
		FROM sessions
		WHERE id = $1
		FOR UPDATE
	`, id)
}

func scanSession(ctx context.Context, tx *sql.Tx, query string, id string) (Session, error) {
	var session Session
	var title sql.NullString
	var sandboxID sql.NullString
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
