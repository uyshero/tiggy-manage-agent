package httpapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type testStore struct {
	mu sync.Mutex

	nextAgentID       int64
	nextEnvironmentID int64
	nextSessionID     int64
	nextEventID       int64
	nextObjectID      int64
	nextArtifactID    int64
	nextWorkerID      int64
	nextWorkID        int64
	nextExporterRunID int64

	agents              map[string]managedagents.Agent
	agentConfigVersions map[string][]managedagents.AgentConfigVersion
	providers           map[string]managedagents.LLMProvider
	models              map[string]managedagents.LLMModel
	environments        map[string]managedagents.Environment
	sessions            map[string]managedagents.Session
	summaries           map[string]managedagents.SessionSummary
	interventions       map[string]managedagents.SessionIntervention
	events              map[string][]managedagents.Event
	usageRecords        []managedagents.RecordLLMUsageInput
	exporterRuns        []managedagents.ObservabilityExporterRun
	objectRefs          map[string]managedagents.ObjectRef
	sessionArtifacts    map[string][]managedagents.SessionArtifact
	workers             map[string]managedagents.Worker
	workerWork          map[string]managedagents.WorkerWork
	subscribers         map[string]map[chan managedagents.Event]struct{}
}

func newTestStore() *testStore {
	store := &testStore{
		agents:              make(map[string]managedagents.Agent),
		agentConfigVersions: make(map[string][]managedagents.AgentConfigVersion),
		providers:           make(map[string]managedagents.LLMProvider),
		models:              make(map[string]managedagents.LLMModel),
		environments:        make(map[string]managedagents.Environment),
		sessions:            make(map[string]managedagents.Session),
		summaries:           make(map[string]managedagents.SessionSummary),
		interventions:       make(map[string]managedagents.SessionIntervention),
		events:              make(map[string][]managedagents.Event),
		objectRefs:          make(map[string]managedagents.ObjectRef),
		sessionArtifacts:    make(map[string][]managedagents.SessionArtifact),
		workers:             make(map[string]managedagents.Worker),
		workerWork:          make(map[string]managedagents.WorkerWork),
		subscribers:         make(map[string]map[chan managedagents.Event]struct{}),
	}
	store.providers["fake"] = managedagents.LLMProvider{
		ID:           "fake",
		ProviderType: "fake",
		Enabled:      true,
		CreatedAt:    time.Now().UTC(),
	}
	store.models[llmModelKey("fake", "fake-demo")] = managedagents.LLMModel{
		ProviderID:          "fake",
		Model:               "fake-demo",
		ContextWindowTokens: managedagents.DefaultContextWindowTokens,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	return store
}

func (s *testStore) EnsureLLMProvider(input managedagents.EnsureLLMProviderInput) (managedagents.LLMProvider, error) {
	return s.UpsertLLMProvider(managedagents.UpsertLLMProviderInput{
		ID:           input.ID,
		ProviderType: input.ProviderType,
		BaseURL:      input.BaseURL,
		APIKeyEnv:    input.APIKeyEnv,
		Enabled:      true,
	})
}

func (s *testStore) UpsertLLMProvider(input managedagents.UpsertLLMProviderInput) (managedagents.LLMProvider, error) {
	if input.ID == "" {
		return managedagents.LLMProvider{}, fmt.Errorf("%w: llm provider id is required", managedagents.ErrInvalid)
	}
	if input.ProviderType == "" {
		return managedagents.LLMProvider{}, fmt.Errorf("%w: llm provider type is required", managedagents.ErrInvalid)
	}

	provider := managedagents.LLMProvider{
		ID:           input.ID,
		ProviderType: input.ProviderType,
		BaseURL:      input.BaseURL,
		APIKeyEnv:    input.APIKeyEnv,
		Enabled:      input.Enabled,
		CreatedAt:    time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[provider.ID] = provider
	return provider, nil
}

func (s *testStore) GetLLMProvider(id string) (managedagents.LLMProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider, ok := s.providers[id]
	if !ok {
		return managedagents.LLMProvider{}, managedagents.ErrNotFound
	}
	return provider, nil
}

func (s *testStore) ListLLMProviders() ([]managedagents.LLMProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	providers := make([]managedagents.LLMProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].ID < providers[j].ID
	})
	return providers, nil
}

func (s *testStore) SetLLMProviderEnabled(id string, enabled bool) (managedagents.LLMProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider, ok := s.providers[id]
	if !ok {
		return managedagents.LLMProvider{}, managedagents.ErrNotFound
	}
	provider.Enabled = enabled
	s.providers[id] = provider
	return provider, nil
}

func (s *testStore) UpsertLLMModel(input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error) {
	if input.ProviderID == "" || input.Model == "" {
		return managedagents.LLMModel{}, managedagents.ErrInvalid
	}
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = managedagents.DefaultContextWindowTokens
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[input.ProviderID]; !ok {
		return managedagents.LLMModel{}, managedagents.ErrNotFound
	}
	now := time.Now().UTC()
	model := managedagents.LLMModel{
		ProviderID:          input.ProviderID,
		Model:               input.Model,
		ContextWindowTokens: input.ContextWindowTokens,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	s.models[llmModelKey(input.ProviderID, input.Model)] = model
	return model, nil
}

func (s *testStore) ListLLMModels(providerID string) ([]managedagents.LLMModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	models := make([]managedagents.LLMModel, 0, len(s.models))
	for _, model := range s.models {
		if providerID != "" && model.ProviderID != providerID {
			continue
		}
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].ProviderID == models[j].ProviderID {
			return models[i].Model < models[j].Model
		}
		return models[i].ProviderID < models[j].ProviderID
	})
	return models, nil
}

func llmModelKey(providerID string, model string) string {
	return providerID + "\x00" + model
}

func (s *testStore) CreateAgent(input managedagents.CreateAgentInput) (managedagents.Agent, error) {
	if input.Name == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent name is required", managedagents.ErrInvalid)
	}
	if input.LLMProvider == "" {
		input.LLMProvider = "fake"
	}
	if input.LLMModel == "" {
		input.LLMModel = input.Model
	}
	if input.LLMModel == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent llm_model is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[input.LLMProvider]
	if !ok {
		return managedagents.Agent{}, fmt.Errorf("%w: llm provider %s", managedagents.ErrNotFound, input.LLMProvider)
	}
	if !provider.Enabled {
		return managedagents.Agent{}, fmt.Errorf("%w: llm provider %s is disabled", managedagents.ErrInvalid, input.LLMProvider)
	}

	now := time.Now().UTC()
	id := s.nextID("agt", &s.nextAgentID)
	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	agent := managedagents.Agent{
		ID:                   id,
		WorkspaceID:          workspaceID,
		Name:                 input.Name,
		CurrentConfigVersion: 1,
		ConfigVersion: managedagents.AgentConfigVersion{
			Version:     1,
			LLMProvider: input.LLMProvider,
			LLMModel:    input.LLMModel,
			System:      input.System,
			Tools:       cloneRaw(input.Tools),
			Skills:      cloneRaw(input.Skills),
			CreatedAt:   now,
		},
		CreatedAt: now,
	}
	s.agents[id] = agent
	s.agentConfigVersions[id] = []managedagents.AgentConfigVersion{agent.ConfigVersion}
	return agent, nil
}

func (s *testStore) GetAgent(id string) (managedagents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[id]
	if !ok {
		return managedagents.Agent{}, managedagents.ErrNotFound
	}
	return agent, nil
}

func (s *testStore) ListAgentConfigVersions(agentID string) ([]managedagents.AgentConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agents[agentID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	versions := s.agentConfigVersions[agentID]
	return append([]managedagents.AgentConfigVersion(nil), versions...), nil
}

func (s *testStore) CreateAgentConfigVersion(input managedagents.CreateAgentConfigVersionInput) (managedagents.Agent, error) {
	if input.AgentID == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent id is required", managedagents.ErrInvalid)
	}
	if input.LLMProvider == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent llm_provider is required", managedagents.ErrInvalid)
	}
	if input.LLMModel == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent llm_model is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[input.AgentID]
	if !ok {
		return managedagents.Agent{}, managedagents.ErrNotFound
	}
	provider, ok := s.providers[input.LLMProvider]
	if !ok {
		return managedagents.Agent{}, fmt.Errorf("%w: llm provider %s", managedagents.ErrNotFound, input.LLMProvider)
	}
	if !provider.Enabled {
		return managedagents.Agent{}, fmt.Errorf("%w: llm provider %s is disabled", managedagents.ErrInvalid, input.LLMProvider)
	}

	nextVersion := agent.CurrentConfigVersion + 1
	configVersion := managedagents.AgentConfigVersion{
		Version:     nextVersion,
		LLMProvider: input.LLMProvider,
		LLMModel:    input.LLMModel,
		System:      input.System,
		Tools:       cloneRaw(input.Tools),
		Skills:      cloneRaw(input.Skills),
		CreatedAt:   time.Now().UTC(),
	}
	agent.CurrentConfigVersion = nextVersion
	agent.ConfigVersion = configVersion
	s.agents[input.AgentID] = agent
	s.agentConfigVersions[input.AgentID] = append(s.agentConfigVersions[input.AgentID], configVersion)
	return agent, nil
}

func (s *testStore) CreateEnvironment(input managedagents.CreateEnvironmentInput) (managedagents.Environment, error) {
	if input.Name == "" {
		return managedagents.Environment{}, fmt.Errorf("%w: environment name is required", managedagents.ErrInvalid)
	}
	if len(input.Config) == 0 {
		input.Config = json.RawMessage(`{}`)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	id := s.nextID("env", &s.nextEnvironmentID)
	environment := managedagents.Environment{
		ID:          id,
		WorkspaceID: defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID),
		Name:        input.Name,
		Config:      cloneRaw(input.Config),
		CreatedAt:   now,
	}
	s.environments[id] = environment
	return environment, nil
}

func (s *testStore) CreateSession(input managedagents.CreateSessionInput) (managedagents.Session, error) {
	agentID := input.AgentID
	if agentID == "" {
		agentID = input.Agent
	}
	if agentID == "" {
		return managedagents.Session{}, fmt.Errorf("%w: agent_id is required", managedagents.ErrInvalid)
	}
	if input.EnvironmentID == "" {
		return managedagents.Session{}, fmt.Errorf("%w: environment_id is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[agentID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: agent %s", managedagents.ErrNotFound, agentID)
	}
	environment, ok := s.environments[input.EnvironmentID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: environment %s", managedagents.ErrNotFound, input.EnvironmentID)
	}

	workspaceID := defaultString(input.WorkspaceID, agent.WorkspaceID)
	if workspaceID != agent.WorkspaceID || workspaceID != environment.WorkspaceID {
		return managedagents.Session{}, fmt.Errorf("%w: workspace mismatch", managedagents.ErrInvalid)
	}

	now := time.Now().UTC()
	id := s.nextID("sesn", &s.nextSessionID)
	session := managedagents.Session{
		ID:                 id,
		WorkspaceID:        workspaceID,
		AgentID:            agent.ID,
		AgentConfigVersion: agent.CurrentConfigVersion,
		EnvironmentID:      environment.ID,
		Status:             managedagents.SessionStatusIdle,
		Title:              input.Title,
		RuntimeSettings:    json.RawMessage(`{}`),
		CreatedBy:          defaultString(input.CreatedBy, "system"),
		CreatedAt:          now,
	}
	s.sessions[id] = session
	s.appendEventLocked(id, managedagents.EventSessionStatusProvisioning, json.RawMessage(`{"status":"provisioning"}`), now)
	s.appendEventLocked(id, managedagents.EventSessionStatusIdle, json.RawMessage(`{"status":"idle"}`), now)
	return session, nil
}

func (s *testStore) GetSession(id string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	return session, nil
}

func (s *testStore) ListSessions(input managedagents.ListSessionsInput) ([]managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	sessions := make([]managedagents.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if input.WorkspaceID != "" && session.WorkspaceID != input.WorkspaceID {
			continue
		}
		if input.Status != "" && session.Status != input.Status {
			continue
		}
		if !input.IncludeArchived && session.ArchivedAt != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i int, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

func (s *testStore) UpdateSessionRuntimeSettings(id string, input managedagents.UpdateSessionRuntimeSettingsInput) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	session.RuntimeSettings = cloneRaw(input.RuntimeSettings)
	s.sessions[id] = session
	return session, nil
}

func (s *testStore) UpgradeSessionAgentConfig(id string, input managedagents.UpgradeSessionAgentConfigInput) (managedagents.UpgradeSessionAgentConfigResult, error) {
	if id == "" {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrInvalid
	}
	if !input.ToCurrent {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusIdle {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrConflict
	}
	agent, ok := s.agents[session.AgentID]
	if !ok {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrNotFound
	}
	result := managedagents.UpgradeSessionAgentConfigResult{
		Session:                  session,
		OldAgentConfigVersion:    session.AgentConfigVersion,
		NewAgentConfigVersion:    session.AgentConfigVersion,
		LatestAgentConfigVersion: agent.CurrentConfigVersion,
	}
	if agent.CurrentConfigVersion == session.AgentConfigVersion {
		return result, nil
	}
	if agent.CurrentConfigVersion < session.AgentConfigVersion {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrConflict
	}

	oldVersion := session.AgentConfigVersion
	session.AgentConfigVersion = agent.CurrentConfigVersion
	s.sessions[id] = session
	payload := json.RawMessage(fmt.Sprintf(`{"old_agent_config_version":%d,"new_agent_config_version":%d,"updated_by":"%s"}`,
		oldVersion, session.AgentConfigVersion, defaultString(input.UpdatedBy, "system")))
	event := s.appendEventLocked(session.ID, managedagents.EventSessionConfigUpdated, payload, time.Now().UTC())
	s.publishLocked(event)

	result.Session = session
	result.Event = event
	result.NewAgentConfigVersion = session.AgentConfigVersion
	result.Changed = true
	return result, nil
}

func (s *testStore) SaveSessionIntervention(sessionID string, input managedagents.SaveSessionInterventionInput) (managedagents.SessionIntervention, error) {
	if input.TurnID == "" || input.CallID == "" || input.ToolIdentifier == "" || input.APIName == "" || input.InterventionMode == "" {
		return managedagents.SessionIntervention{}, managedagents.ErrInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.SessionIntervention{}, managedagents.ErrNotFound
	}

	intervention := managedagents.SessionIntervention{
		SessionID:         sessionID,
		TurnID:            input.TurnID,
		CallID:            input.CallID,
		ToolIdentifier:    input.ToolIdentifier,
		APIName:           input.APIName,
		Arguments:         cloneRaw(input.Arguments),
		InterventionMode:  input.InterventionMode,
		Reason:            input.Reason,
		Status:            managedagents.InterventionStatusPending,
		RequestedAt:       time.Now().UTC(),
		Continuation:      cloneRaw(input.Continuation),
		ContinuationRound: input.ContinuationRound,
	}
	s.interventions[interventionKey(sessionID, input.TurnID, input.CallID)] = intervention
	return intervention, nil
}

func (s *testStore) ListSessionInterventions(sessionID string, status string) ([]managedagents.SessionIntervention, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	interventions := make([]managedagents.SessionIntervention, 0)
	for _, intervention := range s.interventions {
		if intervention.SessionID != sessionID {
			continue
		}
		if status != "" && intervention.Status != status {
			continue
		}
		interventions = append(interventions, intervention)
	}
	sort.Slice(interventions, func(i, j int) bool {
		if interventions[i].RequestedAt.Equal(interventions[j].RequestedAt) {
			if interventions[i].TurnID == interventions[j].TurnID {
				return interventions[i].CallID < interventions[j].CallID
			}
			return interventions[i].TurnID < interventions[j].TurnID
		}
		return interventions[i].RequestedAt.Before(interventions[j].RequestedAt)
	})
	return interventions, nil
}

func (s *testStore) DecideSessionIntervention(sessionID string, input managedagents.DecideSessionInterventionInput) (managedagents.DecideSessionInterventionResult, error) {
	if input.TurnID == "" || input.CallID == "" {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}
	if input.Status != managedagents.InterventionStatusApproved && input.Status != managedagents.InterventionStatusRejected {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrNotFound
	}
	key := interventionKey(sessionID, input.TurnID, input.CallID)
	intervention, ok := s.interventions[key]
	if !ok {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrNotFound
	}
	if intervention.Status != managedagents.InterventionStatusPending {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}

	now := time.Now().UTC()
	intervention.Status = input.Status
	intervention.DecisionReason = input.DecisionReason
	intervention.DecidedAt = &now
	s.interventions[key] = intervention

	eventType := managedagents.EventRuntimeToolInterventionApproved
	message := "Tool call approved by user."
	if input.Status == managedagents.InterventionStatusRejected {
		eventType = managedagents.EventRuntimeToolInterventionRejected
		message = "Tool call rejected by user."
	}
	payload, err := json.Marshal(map[string]any{
		"turn_id": input.TurnID,
		"message": message,
		"data": map[string]any{
			"id":                intervention.CallID,
			"identifier":        intervention.ToolIdentifier,
			"api_name":          intervention.APIName,
			"arguments":         rawJSONObject(intervention.Arguments),
			"intervention_mode": intervention.InterventionMode,
			"reason":            intervention.Reason,
			"decision_reason":   intervention.DecisionReason,
			"approval_source":   "user",
		},
	})
	if err != nil {
		return managedagents.DecideSessionInterventionResult{}, err
	}
	event := s.appendEventLocked(sessionID, eventType, payload, now)
	s.publishLocked(event)
	return managedagents.DecideSessionInterventionResult{
		Intervention: intervention,
		Events:       []managedagents.Event{event},
	}, nil
}

func (s *testStore) MarkSessionTurnWaitingApproval(sessionID string, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.ErrNotFound
	}
	if turnID == "" {
		return managedagents.ErrInvalid
	}
	return nil
}

func (s *testStore) ResolveAgentRuntimeConfig(sessionID string) (managedagents.AgentRuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return managedagents.AgentRuntimeConfig{}, managedagents.ErrNotFound
	}
	agent, ok := s.agents[session.AgentID]
	if !ok {
		return managedagents.AgentRuntimeConfig{}, managedagents.ErrNotFound
	}
	configVersion, ok := s.agentConfigVersionLocked(session.AgentID, session.AgentConfigVersion)
	if !ok {
		return managedagents.AgentRuntimeConfig{}, managedagents.ErrNotFound
	}
	provider := s.providers[configVersion.LLMProvider]
	contextWindowTokens := managedagents.DefaultContextWindowTokens
	if model, ok := s.models[llmModelKey(configVersion.LLMProvider, configVersion.LLMModel)]; ok {
		contextWindowTokens = model.ContextWindowTokens
	}
	summary := s.summaries[sessionID]

	return managedagents.AgentRuntimeConfig{
		SessionID:             sessionID,
		WorkspaceID:           session.WorkspaceID,
		AgentID:               agent.ID,
		AgentConfigVersion:    session.AgentConfigVersion,
		EnvironmentID:         session.EnvironmentID,
		LLMProvider:           configVersion.LLMProvider,
		LLMProviderType:       defaultString(provider.ProviderType, "fake"),
		LLMModel:              configVersion.LLMModel,
		LLMBaseURL:            provider.BaseURL,
		LLMAPIKeyEnv:          provider.APIKeyEnv,
		ContextWindowTokens:   contextWindowTokens,
		SummaryText:           summary.SummaryText,
		SummarySourceUntilSeq: summary.SourceUntilSeq,
		System:                configVersion.System,
		RuntimeSettings:       cloneRaw(session.RuntimeSettings),
		Tools:                 cloneRaw(configVersion.Tools),
		Skills:                cloneRaw(configVersion.Skills),
	}, nil
}

func (s *testStore) GetSessionSummary(sessionID string) (managedagents.SessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	summary, ok := s.summaries[sessionID]
	if !ok {
		return managedagents.SessionSummary{}, managedagents.ErrNotFound
	}
	return summary, nil
}

func (s *testStore) SaveSessionSummary(sessionID string, input managedagents.UpsertSessionSummaryInput) (managedagents.SessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.SessionSummary{}, managedagents.ErrNotFound
	}
	now := time.Now().UTC()
	summary := managedagents.SessionSummary{
		SessionID:      sessionID,
		SummaryText:    input.SummaryText,
		SourceUntilSeq: input.SourceUntilSeq,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.summaries[sessionID] = summary
	return summary, nil
}

func (s *testStore) UpsertSessionSummary(sessionID string, input managedagents.UpsertSessionSummaryInput) (managedagents.UpsertSessionSummaryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return managedagents.UpsertSessionSummaryResult{}, managedagents.ErrNotFound
	}
	if session.Status != managedagents.SessionStatusIdle {
		return managedagents.UpsertSessionSummaryResult{}, managedagents.ErrInvalid
	}
	now := time.Now().UTC()
	summary := managedagents.SessionSummary{
		SessionID:      sessionID,
		SummaryText:    input.SummaryText,
		SourceUntilSeq: input.SourceUntilSeq,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.summaries[sessionID] = summary
	compacting := s.appendEventLocked(sessionID, managedagents.EventSessionStatusCompacting, json.RawMessage(`{"status":"compacting"}`), now)
	idle := s.appendEventLocked(sessionID, managedagents.EventSessionStatusIdle, json.RawMessage(`{"status":"idle"}`), now)
	return managedagents.UpsertSessionSummaryResult{
		Summary: summary,
		Events:  []managedagents.Event{compacting, idle},
	}, nil
}

func (s *testStore) agentConfigVersionLocked(agentID string, version int) (managedagents.AgentConfigVersion, bool) {
	for _, configVersion := range s.agentConfigVersions[agentID] {
		if configVersion.Version == version {
			return configVersion, true
		}
	}
	return managedagents.AgentConfigVersion{}, false
}

func (s *testStore) RecordLLMUsage(input managedagents.RecordLLMUsageInput) (managedagents.LLMUsageRecord, error) {
	s.mu.Lock()
	s.usageRecords = append(s.usageRecords, input)
	id := fmt.Sprintf("llmu_%06d", len(s.usageRecords))
	s.mu.Unlock()

	return managedagents.LLMUsageRecord{
		ID:                 id,
		WorkspaceID:        input.WorkspaceID,
		AgentID:            input.AgentID,
		AgentConfigVersion: input.AgentConfigVersion,
		SessionID:          input.SessionID,
		TurnID:             input.TurnID,
		ProviderID:         input.ProviderID,
		ProviderType:       input.ProviderType,
		Model:              input.Model,
		InputTokens:        input.InputTokens,
		OutputTokens:       input.OutputTokens,
		TotalTokens:        input.TotalTokens,
		CachedInputTokens:  input.CachedInputTokens,
		ReasoningTokens:    input.ReasoningTokens,
		LatencyMillis:      input.LatencyMillis,
		Status:             input.Status,
		ErrorMessage:       input.ErrorMessage,
	}, nil
}

func (s *testStore) GetSessionLLMUsage(sessionID string) (managedagents.LLMUsageReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.LLMUsageReport{}, managedagents.ErrNotFound
	}

	report := managedagents.LLMUsageReport{
		SessionID: sessionID,
		Records:   []managedagents.LLMUsageRecord{},
	}
	for index, input := range s.usageRecords {
		if input.SessionID != sessionID {
			continue
		}
		record := managedagents.LLMUsageRecord{
			ID:                 fmt.Sprintf("llmu_%06d", index+1),
			WorkspaceID:        input.WorkspaceID,
			AgentID:            input.AgentID,
			AgentConfigVersion: input.AgentConfigVersion,
			SessionID:          input.SessionID,
			TurnID:             input.TurnID,
			ProviderID:         input.ProviderID,
			ProviderType:       input.ProviderType,
			Model:              input.Model,
			InputTokens:        input.InputTokens,
			OutputTokens:       input.OutputTokens,
			TotalTokens:        input.TotalTokens,
			CachedInputTokens:  input.CachedInputTokens,
			ReasoningTokens:    input.ReasoningTokens,
			LatencyMillis:      input.LatencyMillis,
			Status:             input.Status,
			ErrorMessage:       input.ErrorMessage,
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
	return report, nil
}

func (s *testStore) ListLLMUsage(input managedagents.ListLLMUsageInput) (managedagents.LLMUsageAggregateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	groupBy := testUsageGroupBy(input.GroupBy)
	if groupBy == "" {
		return managedagents.LLMUsageAggregateReport{}, managedagents.ErrInvalid
	}
	input.GroupBy = groupBy

	report := managedagents.LLMUsageAggregateReport{
		GroupBy: groupBy,
		Filters: input,
		Groups:  []managedagents.LLMUsageAggregate{},
	}
	indexByKey := map[string]int{}
	for _, record := range usageRecordsFromInputs(s.usageRecords) {
		if !matchesUsageInput(record, input) {
			continue
		}
		providerID := ""
		model := ""
		if groupBy == managedagents.LLMUsageGroupByProvider || groupBy == managedagents.LLMUsageGroupByProviderModel {
			providerID = record.ProviderID
		}
		if groupBy == managedagents.LLMUsageGroupByModel || groupBy == managedagents.LLMUsageGroupByProviderModel {
			model = record.Model
		}
		key := providerID + "\x00" + model
		groupIndex, ok := indexByKey[key]
		if !ok {
			groupIndex = len(report.Groups)
			indexByKey[key] = groupIndex
			report.Groups = append(report.Groups, managedagents.LLMUsageAggregate{
				ProviderID: providerID,
				Model:      model,
			})
		}
		addUsageSummary(&report.Groups[groupIndex].Summary, record)
		addUsageSummary(&report.Summary, record)
	}
	return report, nil
}

func (s *testStore) RecordObservabilityExporterRun(input managedagents.RecordObservabilityExporterRunInput) (managedagents.ObservabilityExporterRun, error) {
	if input.Exporter == "" || input.Status == "" || input.SessionID == "" || input.TurnID == "" {
		return managedagents.ObservabilityExporterRun{}, managedagents.ErrInvalid
	}
	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	finishedAt := input.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = startedAt
	}
	attemptCount := input.AttemptCount
	if attemptCount <= 0 {
		attemptCount = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextExporterRunID++
	run := managedagents.ObservabilityExporterRun{
		ID:           fmt.Sprintf("oexp_%06d", s.nextExporterRunID),
		Exporter:     input.Exporter,
		Status:       input.Status,
		SessionID:    input.SessionID,
		TurnID:       input.TurnID,
		TraceID:      input.TraceID,
		Destination:  input.Destination,
		Message:      input.Message,
		AttemptCount: attemptCount,
		NextRetryAt:  input.NextRetryAt,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
	}
	s.exporterRuns = append(s.exporterRuns, run)
	return run, nil
}

func (s *testStore) ListObservabilityExporterRuns(input managedagents.ListObservabilityExporterRunsInput) ([]managedagents.ObservabilityExporterRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	runs := make([]managedagents.ObservabilityExporterRun, 0, limit)
	for index := len(s.exporterRuns) - 1; index >= 0; index-- {
		run := s.exporterRuns[index]
		if input.Exporter != "" && run.Exporter != input.Exporter {
			continue
		}
		if input.Status != "" && run.Status != input.Status {
			continue
		}
		if input.SessionID != "" && run.SessionID != input.SessionID {
			continue
		}
		if input.TurnID != "" && run.TurnID != input.TurnID {
			continue
		}
		if !input.RetryDueBefore.IsZero() {
			if run.Status != managedagents.ObservabilityExporterRunFailed || run.NextRetryAt == nil || run.NextRetryAt.After(input.RetryDueBefore) {
				continue
			}
		}
		if input.MaxAttemptCount > 0 && run.AttemptCount >= input.MaxAttemptCount {
			continue
		}
		runs = append(runs, run)
		if len(runs) >= limit {
			break
		}
	}
	return runs, nil
}

func (s *testStore) CreateObjectRef(input managedagents.CreateObjectRefInput) (managedagents.ObjectRef, error) {
	if input.Bucket == "" {
		return managedagents.ObjectRef{}, fmt.Errorf("%w: object bucket is required", managedagents.ErrInvalid)
	}
	if input.ObjectKey == "" {
		return managedagents.ObjectRef{}, fmt.Errorf("%w: object_key is required", managedagents.ErrInvalid)
	}
	if input.SizeBytes < 0 {
		return managedagents.ObjectRef{}, fmt.Errorf("%w: object size_bytes must be non-negative", managedagents.ErrInvalid)
	}
	visibility := defaultString(input.Visibility, managedagents.ObjectVisibilityWorkspace)
	if visibility != managedagents.ObjectVisibilityWorkspace && visibility != managedagents.ObjectVisibilitySession {
		return managedagents.ObjectRef{}, fmt.Errorf("%w: unsupported object visibility %q", managedagents.ErrInvalid, input.Visibility)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextObjectID++
	now := time.Now().UTC()
	object := managedagents.ObjectRef{
		ID:              fmt.Sprintf("obj_%06d", s.nextObjectID),
		WorkspaceID:     defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID),
		StorageProvider: defaultString(input.StorageProvider, managedagents.ObjectStorageProviderS3),
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
		CreatedAt:       now,
	}
	s.objectRefs[object.ID] = object
	return object, nil
}

func (s *testStore) GetObjectRef(id string) (managedagents.ObjectRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	object, ok := s.objectRefs[id]
	if !ok {
		return managedagents.ObjectRef{}, managedagents.ErrNotFound
	}
	return object, nil
}

func (s *testStore) CountSessionArtifactsByObjectRef(objectRefID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, artifacts := range s.sessionArtifacts {
		for _, artifact := range artifacts {
			if artifact.ObjectRefID == objectRefID {
				count++
			}
		}
	}
	return count, nil
}

func (s *testStore) DeleteObjectRef(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.objectRefs[id]; !ok {
		return managedagents.ErrNotFound
	}
	delete(s.objectRefs, id)
	return nil
}

func (s *testStore) CreateSessionArtifact(input managedagents.CreateSessionArtifactInput) (managedagents.SessionArtifact, error) {
	if input.SessionID == "" {
		return managedagents.SessionArtifact{}, fmt.Errorf("%w: artifact session_id is required", managedagents.ErrInvalid)
	}
	if input.ObjectRefID == "" {
		return managedagents.SessionArtifact{}, fmt.Errorf("%w: artifact object_ref_id is required", managedagents.ErrInvalid)
	}
	artifactType := defaultString(input.ArtifactType, managedagents.ArtifactTypeFile)
	if artifactType != managedagents.ArtifactTypeFile && artifactType != managedagents.ArtifactTypeSnapshot && artifactType != managedagents.ArtifactTypeAsset {
		return managedagents.SessionArtifact{}, fmt.Errorf("%w: unsupported artifact_type %q", managedagents.ErrInvalid, input.ArtifactType)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[input.SessionID]
	if !ok {
		return managedagents.SessionArtifact{}, managedagents.ErrNotFound
	}
	object, ok := s.objectRefs[input.ObjectRefID]
	if !ok {
		return managedagents.SessionArtifact{}, managedagents.ErrNotFound
	}
	workspaceID := defaultString(input.WorkspaceID, session.WorkspaceID)
	if workspaceID != session.WorkspaceID || workspaceID != object.WorkspaceID {
		return managedagents.SessionArtifact{}, fmt.Errorf("%w: artifact workspace mismatch", managedagents.ErrInvalid)
	}

	s.nextArtifactID++
	now := time.Now().UTC()
	name := input.Name
	if name == "" {
		name = object.ObjectKey
	}
	artifact := managedagents.SessionArtifact{
		ID:            fmt.Sprintf("art_%06d", s.nextArtifactID),
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
		CreatedAt:     now,
	}
	s.sessionArtifacts[artifact.SessionID] = append(s.sessionArtifacts[artifact.SessionID], artifact)
	return artifact, nil
}

func (s *testStore) GetSessionArtifact(sessionID string, artifactID string) (managedagents.SessionArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.SessionArtifact{}, managedagents.ErrNotFound
	}
	for _, artifact := range s.sessionArtifacts[sessionID] {
		if artifact.ID == artifactID {
			return artifact, nil
		}
	}
	return managedagents.SessionArtifact{}, managedagents.ErrNotFound
}

func (s *testStore) DeleteSessionArtifact(sessionID string, artifactID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	artifacts := s.sessionArtifacts[sessionID]
	filtered := artifacts[:0]
	removed := false
	for _, artifact := range artifacts {
		if artifact.ID == artifactID {
			removed = true
			continue
		}
		filtered = append(filtered, artifact)
	}
	if !removed {
		return managedagents.ErrNotFound
	}
	s.sessionArtifacts[sessionID] = append([]managedagents.SessionArtifact(nil), filtered...)
	return nil
}

func (s *testStore) ListSessionArtifacts(sessionID string) ([]managedagents.SessionArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	artifacts := append([]managedagents.SessionArtifact(nil), s.sessionArtifacts[sessionID]...)
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].CreatedAt.Equal(artifacts[j].CreatedAt) {
			return artifacts[i].ID < artifacts[j].ID
		}
		return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt)
	})
	return artifacts, nil
}

func (s *testStore) RegisterWorker(input managedagents.RegisterWorkerInput) (managedagents.Worker, error) {
	if input.Name == "" {
		return managedagents.Worker{}, fmt.Errorf("%w: worker name is required", managedagents.ErrInvalid)
	}
	workerType := defaultString(input.WorkerType, managedagents.WorkerTypeLocal)
	if workerType != managedagents.WorkerTypeLocal && workerType != managedagents.WorkerTypeShared && workerType != managedagents.WorkerTypeCloud {
		return managedagents.Worker{}, fmt.Errorf("%w: unsupported worker_type %q", managedagents.ErrInvalid, input.WorkerType)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextWorkerID++
	now := time.Now().UTC()
	leaseExpiresAt := now.Add(workerLeaseDuration(input.LeaseSeconds))
	worker := managedagents.Worker{
		ID:             fmt.Sprintf("wrk_%06d", s.nextWorkerID),
		WorkspaceID:    defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID),
		Name:           input.Name,
		WorkerType:     workerType,
		Status:         managedagents.WorkerStatusOnline,
		Capabilities:   metadataJSON(input.Capabilities),
		Metadata:       metadataJSON(input.Metadata),
		RegisteredBy:   defaultString(input.RegisteredBy, "system"),
		RegisteredAt:   now,
		LastSeenAt:     &now,
		LeaseExpiresAt: &leaseExpiresAt,
	}
	s.workers[worker.ID] = worker
	return worker, nil
}

func (s *testStore) GetWorker(id string) (managedagents.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[id]
	if !ok {
		return managedagents.Worker{}, managedagents.ErrNotFound
	}
	return worker, nil
}

func (s *testStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	workers := []managedagents.Worker{}
	for _, worker := range s.workers {
		if worker.WorkspaceID != workspaceID {
			continue
		}
		if input.Status != "" && worker.Status != input.Status {
			continue
		}
		workers = append(workers, worker)
	}
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].RegisteredAt.Equal(workers[j].RegisteredAt) {
			return workers[i].ID < workers[j].ID
		}
		return workers[i].RegisteredAt.After(workers[j].RegisteredAt)
	})
	return workers, nil
}

func (s *testStore) HeartbeatWorker(id string, input managedagents.WorkerHeartbeatInput) (managedagents.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[id]
	if !ok || worker.ArchivedAt != nil {
		return managedagents.Worker{}, managedagents.ErrNotFound
	}
	status := defaultString(input.Status, managedagents.WorkerStatusOnline)
	if status == managedagents.WorkerStatusArchived {
		return managedagents.Worker{}, fmt.Errorf("%w: archived status requires archive endpoint", managedagents.ErrInvalid)
	}
	now := time.Now().UTC()
	leaseExpiresAt := now.Add(workerLeaseDuration(input.LeaseSeconds))
	worker.Status = status
	worker.LastSeenAt = &now
	worker.LeaseExpiresAt = &leaseExpiresAt
	if len(input.Capabilities) > 0 {
		worker.Capabilities = metadataJSON(input.Capabilities)
	}
	if len(input.Metadata) > 0 {
		worker.Metadata = metadataJSON(input.Metadata)
	}
	s.workers[id] = worker
	return worker, nil
}

func (s *testStore) ArchiveWorker(id string) (managedagents.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[id]
	if !ok || worker.ArchivedAt != nil {
		return managedagents.Worker{}, managedagents.ErrNotFound
	}
	now := time.Now().UTC()
	worker.Status = managedagents.WorkerStatusArchived
	worker.ArchivedAt = &now
	s.workers[id] = worker
	return worker, nil
}

func (s *testStore) ReapExpiredWorkers(input managedagents.ReapExpiredWorkersInput) ([]managedagents.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	limit := reapLimit(input.Limit)
	candidates := make([]managedagents.Worker, 0, len(s.workers))
	for _, worker := range s.workers {
		if worker.Status != managedagents.WorkerStatusOnline || worker.ArchivedAt != nil {
			continue
		}
		if worker.LeaseExpiresAt == nil || !worker.LeaseExpiresAt.Before(now) {
			continue
		}
		candidates = append(candidates, worker)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].LeaseExpiresAt
		right := candidates[j].LeaseExpiresAt
		if left != nil && right != nil && !left.Equal(*right) {
			return left.Before(*right)
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	expired := make([]managedagents.Worker, 0, len(candidates))
	for _, worker := range candidates {
		worker.Status = managedagents.WorkerStatusOffline
		s.workers[worker.ID] = worker
		expired = append(expired, worker)
	}
	return expired, nil
}

func (s *testStore) EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error) {
	workType := normalizeWorkerWorkType(input.WorkType)
	if workType == "" {
		return managedagents.WorkerWork{}, fmt.Errorf("%w: unsupported worker work_type %q", managedagents.ErrInvalid, input.WorkType)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	id := s.nextID("work", &s.nextWorkID)
	work := managedagents.WorkerWork{
		ID:            id,
		WorkspaceID:   defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID),
		WorkerID:      input.WorkerID,
		EnvironmentID: input.EnvironmentID,
		SessionID:     input.SessionID,
		TurnID:        input.TurnID,
		WorkType:      workType,
		Status:        managedagents.WorkerWorkStatusPending,
		Payload:       metadataJSON(input.Payload),
		Result:        json.RawMessage(`{}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.workerWork[id] = work
	return work, nil
}

func (s *testStore) GetWorkerWork(id string) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	work, ok := s.workerWork[id]
	if !ok {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	return work, nil
}

func (s *testStore) PollWorkerWork(workerID string, input managedagents.PollWorkerWorkInput) (*managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[workerID]
	if !ok || worker.ArchivedAt != nil {
		return nil, managedagents.ErrNotFound
	}

	workID := ""
	var selected managedagents.WorkerWork
	for _, work := range s.workerWork {
		if work.WorkspaceID != worker.WorkspaceID {
			continue
		}
		if work.Status != managedagents.WorkerWorkStatusPending {
			continue
		}
		if work.WorkerID != "" && work.WorkerID != workerID {
			continue
		}
		if workID == "" || work.CreatedAt.Before(selected.CreatedAt) || (work.CreatedAt.Equal(selected.CreatedAt) && work.ID < selected.ID) {
			workID = work.ID
			selected = work
		}
	}
	if workID == "" {
		return nil, nil
	}

	now := time.Now().UTC()
	leaseExpiresAt := now.Add(workerLeaseDuration(input.LeaseSeconds))
	selected.WorkerID = workerID
	selected.Status = managedagents.WorkerWorkStatusLeased
	selected.LeaseExpiresAt = &leaseExpiresAt
	selected.UpdatedAt = now
	s.workerWork[workID] = selected
	return &selected, nil
}

func (s *testStore) AckWorkerWork(workerID string, workID string) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	work, ok := s.workerWork[workID]
	if !ok || work.WorkerID != workerID {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	if work.Status != managedagents.WorkerWorkStatusLeased && work.Status != managedagents.WorkerWorkStatusRunning {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}

	now := time.Now().UTC()
	work.Status = managedagents.WorkerWorkStatusRunning
	if work.StartedAt == nil {
		work.StartedAt = &now
	}
	work.UpdatedAt = now
	s.workerWork[workID] = work
	return work, nil
}

func (s *testStore) HeartbeatWorkerWork(workerID string, workID string, input managedagents.WorkerWorkHeartbeatInput) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	work, ok := s.workerWork[workID]
	if !ok || work.WorkerID != workerID {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	if work.Status == managedagents.WorkerWorkStatusCanceled {
		return work, nil
	}
	if work.Status != managedagents.WorkerWorkStatusLeased && work.Status != managedagents.WorkerWorkStatusRunning {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}

	now := time.Now().UTC()
	leaseExpiresAt := now.Add(workerLeaseDuration(input.LeaseSeconds))
	work.LeaseExpiresAt = &leaseExpiresAt
	work.UpdatedAt = now
	s.workerWork[workID] = work
	return work, nil
}

func (s *testStore) CancelWorkerWork(workID string, input managedagents.CancelWorkerWorkInput) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	work, ok := s.workerWork[workID]
	if !ok {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	if work.Status != managedagents.WorkerWorkStatusPending &&
		work.Status != managedagents.WorkerWorkStatusLeased &&
		work.Status != managedagents.WorkerWorkStatusRunning {
		return work, nil
	}

	now := time.Now().UTC()
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "worker work canceled"
	}
	work.Status = managedagents.WorkerWorkStatusCanceled
	work.ErrorMessage = reason
	work.UpdatedAt = now
	work.CompletedAt = &now
	s.workerWork[workID] = work
	return work, nil
}

func (s *testStore) ReapExpiredWorkerWork(input managedagents.ReapExpiredWorkerWorkInput) ([]managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	limit := reapLimit(input.Limit)
	candidates := make([]managedagents.WorkerWork, 0, len(s.workerWork))
	for _, work := range s.workerWork {
		if work.Status != managedagents.WorkerWorkStatusLeased && work.Status != managedagents.WorkerWorkStatusRunning {
			continue
		}
		if work.LeaseExpiresAt == nil || !work.LeaseExpiresAt.Before(now) {
			continue
		}
		candidates = append(candidates, work)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].LeaseExpiresAt
		right := candidates[j].LeaseExpiresAt
		if left != nil && right != nil && !left.Equal(*right) {
			return left.Before(*right)
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	expired := make([]managedagents.WorkerWork, 0, len(candidates))
	for _, work := range candidates {
		work.Status = managedagents.WorkerWorkStatusFailed
		if strings.TrimSpace(work.ErrorMessage) == "" {
			work.ErrorMessage = "worker work lease expired at " + work.LeaseExpiresAt.UTC().Format(time.RFC3339)
		}
		work.UpdatedAt = now
		work.CompletedAt = &now
		s.workerWork[work.ID] = work
		expired = append(expired, work)
	}
	return expired, nil
}

func (s *testStore) CompleteWorkerWork(workerID string, workID string, input managedagents.CompleteWorkerWorkInput) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	work, ok := s.workerWork[workID]
	if !ok || work.WorkerID != workerID {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	if work.Status == managedagents.WorkerWorkStatusCanceled {
		return work, nil
	}
	if work.Status != managedagents.WorkerWorkStatusLeased && work.Status != managedagents.WorkerWorkStatusRunning {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}

	now := time.Now().UTC()
	work.Status = managedagents.WorkerWorkStatusFailed
	if input.Success {
		work.Status = managedagents.WorkerWorkStatusCompleted
	}
	work.Result = metadataJSON(input.Result)
	work.ErrorMessage = input.ErrorMessage
	work.CompletedAt = &now
	work.UpdatedAt = now
	s.workerWork[workID] = work
	return work, nil
}

func usageRecordsFromInputs(inputs []managedagents.RecordLLMUsageInput) []managedagents.LLMUsageRecord {
	records := make([]managedagents.LLMUsageRecord, 0, len(inputs))
	for index, input := range inputs {
		records = append(records, managedagents.LLMUsageRecord{
			ID:                 fmt.Sprintf("llmu_%06d", index+1),
			WorkspaceID:        input.WorkspaceID,
			AgentID:            input.AgentID,
			AgentConfigVersion: input.AgentConfigVersion,
			SessionID:          input.SessionID,
			TurnID:             input.TurnID,
			ProviderID:         input.ProviderID,
			ProviderType:       input.ProviderType,
			Model:              input.Model,
			InputTokens:        input.InputTokens,
			OutputTokens:       input.OutputTokens,
			TotalTokens:        input.TotalTokens,
			CachedInputTokens:  input.CachedInputTokens,
			ReasoningTokens:    input.ReasoningTokens,
			LatencyMillis:      input.LatencyMillis,
			Status:             input.Status,
			ErrorMessage:       input.ErrorMessage,
		})
	}
	return records
}

func matchesUsageInput(record managedagents.LLMUsageRecord, input managedagents.ListLLMUsageInput) bool {
	if input.WorkspaceID != "" && record.WorkspaceID != input.WorkspaceID {
		return false
	}
	if input.ProviderID != "" && record.ProviderID != input.ProviderID {
		return false
	}
	if input.Model != "" && record.Model != input.Model {
		return false
	}
	if input.Status != "" && record.Status != input.Status {
		return false
	}
	if input.From != nil && record.CreatedAt.Before(*input.From) {
		return false
	}
	if input.To != nil && !record.CreatedAt.Before(*input.To) {
		return false
	}
	return true
}

func addUsageSummary(summary *managedagents.LLMUsageSummary, record managedagents.LLMUsageRecord) {
	summary.RecordCount++
	summary.InputTokens += record.InputTokens
	summary.OutputTokens += record.OutputTokens
	summary.TotalTokens += record.TotalTokens
	summary.CachedInputTokens += record.CachedInputTokens
	summary.ReasoningTokens += record.ReasoningTokens
	summary.LatencyMillis += record.LatencyMillis
}

func testUsageGroupBy(value string) string {
	switch value {
	case "", managedagents.LLMUsageGroupByProviderModel, "provider-model":
		return managedagents.LLMUsageGroupByProviderModel
	case managedagents.LLMUsageGroupByProvider:
		return managedagents.LLMUsageGroupByProvider
	case managedagents.LLMUsageGroupByModel:
		return managedagents.LLMUsageGroupByModel
	default:
		return ""
	}
}

func (s *testStore) ArchiveSession(id string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return session, nil
	}

	now := time.Now().UTC()
	session.Status = managedagents.SessionStatusTerminated
	session.ArchivedAt = &now
	s.sessions[id] = session

	event := s.appendEventLocked(id, managedagents.EventSessionStatusTerminated, json.RawMessage(`{"status":"terminated"}`), now)
	s.publishLocked(event)
	return session, nil
}

func (s *testStore) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[id]; !ok {
		return managedagents.ErrNotFound
	}
	delete(s.sessions, id)
	delete(s.events, id)
	s.closeSessionLocked(id)
	return nil
}

func (s *testStore) AppendEvents(sessionID string, inputs []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: at least one event is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}

	now := time.Now().UTC()
	events := make([]managedagents.Event, 0, len(inputs))
	for _, input := range inputs {
		if input.Type == "" {
			return nil, fmt.Errorf("%w: event type is required", managedagents.ErrInvalid)
		}
		newEvents, err := s.applyEventLocked(&session, input, now)
		if err != nil {
			return nil, err
		}
		events = append(events, newEvents...)
	}

	s.sessions[sessionID] = session
	return events, nil
}

func (s *testStore) CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	currentTurnID := s.currentTurnIDLocked(sessionID)
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	now := time.Now().UTC()
	agentEvent := s.appendEventLocked(session.ID, managedagents.EventAgentMessage, payloadWithTurnID(agentPayload, turnID), now)
	session.Status = managedagents.SessionStatusIdle
	idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, statusPayload("idle", turnID), now)
	s.sessions[sessionID] = session

	s.publishLocked(agentEvent)
	s.publishLocked(idleEvent)
	return []managedagents.Event{agentEvent, idleEvent}, nil
}

func (s *testStore) AppendRuntimeEvent(sessionID string, turnID string, input managedagents.AppendEventInput) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if input.Type == "" {
		return nil, fmt.Errorf("%w: event type is required", managedagents.ErrInvalid)
	}
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	if turnID == "" || s.currentTurnIDLocked(sessionID) != turnID {
		return nil, nil
	}

	event := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), time.Now().UTC())
	s.publishLocked(event)
	return []managedagents.Event{event}, nil
}

func (s *testStore) FailSessionTurn(sessionID string, turnID string, reason string) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	currentTurnID := s.currentTurnIDLocked(sessionID)
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	now := time.Now().UTC()
	session.Status = managedagents.SessionStatusIdle
	idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, failedTurnIdlePayload(turnID, reason), now)
	s.sessions[sessionID] = session

	s.publishLocked(idleEvent)
	return []managedagents.Event{idleEvent}, nil
}

func (s *testStore) ListEvents(sessionID string, afterSeq int64) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}

	all := s.events[sessionID]
	events := make([]managedagents.Event, 0, len(all))
	for _, event := range all {
		if event.Seq > afterSeq {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *testStore) ListConversationMessages(sessionID string, beforeSeq int64) ([]managedagents.ConversationMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	if beforeSeq <= 0 {
		return []managedagents.ConversationMessage{}, nil
	}

	var messages []managedagents.ConversationMessage
	for _, event := range s.events[sessionID] {
		if event.Seq >= beforeSeq {
			continue
		}
		role := ""
		switch event.Type {
		case managedagents.EventUserMessage:
			role = "user"
		case managedagents.EventAgentMessage:
			role = "assistant"
		default:
			continue
		}
		messages = append(messages, managedagents.ConversationMessage{
			Seq:     event.Seq,
			Role:    role,
			Payload: cloneRaw(event.Payload),
		})
	}
	return messages, nil
}

func (s *testStore) SubscribeEvents(sessionID string) (<-chan managedagents.Event, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, nil, managedagents.ErrNotFound
	}

	ch := make(chan managedagents.Event, 16)
	if s.subscribers[sessionID] == nil {
		s.subscribers[sessionID] = make(map[chan managedagents.Event]struct{})
	}
	s.subscribers[sessionID][ch] = struct{}{}

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		delete(s.subscribers[sessionID], ch)
		if len(s.subscribers[sessionID]) == 0 {
			delete(s.subscribers, sessionID)
		}
		close(ch)
	}
	return ch, cancel, nil
}

func (s *testStore) applyEventLocked(session *managedagents.Session, input managedagents.AppendEventInput, now time.Time) ([]managedagents.Event, error) {
	switch input.Type {
	case managedagents.EventUserMessage:
		if session.Status != managedagents.SessionStatusIdle {
			return nil, fmt.Errorf("%w: user.message requires idle session", managedagents.ErrInvalid)
		}
		turnID := s.nextTurnIDLocked(session.ID)
		session.Status = managedagents.SessionStatusRunning
		statusEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusRunning, statusPayload("running", turnID), now)
		userEvent := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		s.publishLocked(statusEvent)
		s.publishLocked(userEvent)
		return []managedagents.Event{statusEvent, userEvent}, nil

	case managedagents.EventUserInterrupt:
		if session.Status != managedagents.SessionStatusRunning {
			return nil, fmt.Errorf("%w: user.interrupt requires running session", managedagents.ErrInvalid)
		}
		turnID := s.currentTurnIDLocked(session.ID)
		if turnID == "" {
			return nil, fmt.Errorf("%w: running session has no active turn", managedagents.ErrInvalid)
		}
		userEvent := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		interruptingEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusInterrupting, statusPayload("interrupting", turnID), now)
		session.Status = managedagents.SessionStatusIdle
		idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, statusPayload("idle", turnID), now)
		s.publishLocked(userEvent)
		s.publishLocked(interruptingEvent)
		s.publishLocked(idleEvent)
		return []managedagents.Event{userEvent, interruptingEvent, idleEvent}, nil

	default:
		event := s.appendEventLocked(session.ID, input.Type, cloneRaw(input.Payload), now)
		s.publishLocked(event)
		return []managedagents.Event{event}, nil
	}
}

func (s *testStore) appendEventLocked(sessionID, eventType string, payload json.RawMessage, now time.Time) managedagents.Event {
	seq := int64(len(s.events[sessionID]) + 1)
	event := managedagents.Event{
		ID:        s.nextID("evt", &s.nextEventID),
		SessionID: sessionID,
		Seq:       seq,
		Type:      eventType,
		Payload:   cloneRaw(payload),
		CreatedAt: now,
	}
	s.events[sessionID] = append(s.events[sessionID], event)
	return event
}

func (s *testStore) publishLocked(event managedagents.Event) {
	for ch := range s.subscribers[event.SessionID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *testStore) closeSessionLocked(sessionID string) {
	for ch := range s.subscribers[sessionID] {
		close(ch)
	}
	delete(s.subscribers, sessionID)
}

func (s *testStore) nextID(prefix string, counter *int64) string {
	*counter = *counter + 1
	return fmt.Sprintf("%s_%06d", prefix, *counter)
}

func (s *testStore) nextTurnIDLocked(sessionID string) string {
	var count int64
	for _, event := range s.events[sessionID] {
		if event.Type == managedagents.EventUserMessage {
			count++
		}
	}
	return fmt.Sprintf("turn_%06d", count+1)
}

func (s *testStore) currentTurnIDLocked(sessionID string) string {
	events := s.events[sessionID]
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == managedagents.EventUserMessage {
			return payloadString(events[i].Payload, "turn_id")
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func metadataJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return cloneRaw(value)
}

func workerLeaseDuration(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
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

func interventionKey(sessionID string, turnID string, callID string) string {
	return sessionID + "\x00" + turnID + "\x00" + callID
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

func statusPayload(status string, turnID string) json.RawMessage {
	payload := map[string]string{"status": status}
	if turnID != "" {
		payload["turn_id"] = turnID
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"status":"` + status + `"}`)
	}
	return encoded
}

func failedTurnIdlePayload(turnID string, reason string) json.RawMessage {
	payload := map[string]string{
		"status":           "idle",
		"last_turn_status": "failed",
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}
	if reason != "" {
		payload["reason"] = reason
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"status":"idle","last_turn_status":"failed"}`)
	}
	return encoded
}

func payloadWithTurnID(payload json.RawMessage, turnID string) json.RawMessage {
	var object map[string]any
	if len(payload) == 0 || string(payload) == "null" {
		object = make(map[string]any)
	} else if err := json.Unmarshal(payload, &object); err != nil {
		object = make(map[string]any)
	} else if object == nil {
		object = make(map[string]any)
	}

	object["turn_id"] = turnID
	encoded, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return encoded
}

func normalizeWorkerWorkType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", managedagents.WorkerWorkTypeToolExecution:
		return managedagents.WorkerWorkTypeToolExecution
	case managedagents.WorkerWorkTypeSandboxCommand:
		return managedagents.WorkerWorkTypeSandboxCommand
	case managedagents.WorkerWorkTypeArtifactSync:
		return managedagents.WorkerWorkTypeArtifactSync
	default:
		return ""
	}
}
