package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	mcppkg "tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skills"
)

type testStore struct {
	mu sync.Mutex

	nextAgentID         int64
	nextEnvironmentID   int64
	nextSessionID       int64
	nextEventID         int64
	nextObjectID        int64
	nextArtifactID      int64
	nextWorkerID        int64
	nextWorkID          int64
	nextExporterRunID   int64
	nextStartRequestID  int64
	nextOperatorAuditID int64
	nextDeliberationID  int64
	nextSkillID         int64
	nextSkillVersionID  int64
	nextPolicyID        int64
	nextPolicyVersionID int64
	nextMCPRegistryID   int64
	nextScheduleID      int64
	nextScheduleRunID   int64

	agents                    map[string]managedagents.Agent
	agentConfigVersions       map[string][]managedagents.AgentConfigVersion
	providers                 map[string]managedagents.LLMProvider
	models                    map[string]managedagents.LLMModel
	environments              map[string]managedagents.Environment
	sessions                  map[string]managedagents.Session
	summaries                 map[string]managedagents.SessionSummary
	taskPlans                 map[string][]managedagents.SessionTaskPlan
	interventions             map[string]managedagents.SessionIntervention
	events                    map[string][]managedagents.Event
	usageRecords              []managedagents.RecordLLMUsageInput
	exporterRuns              []managedagents.ObservabilityExporterRun
	traceIndexes              map[string]managedagents.TraceIndexEntry
	traceSpanIndexes          map[string][]managedagents.TraceSpanIndexEntry
	objectRefs                map[string]managedagents.ObjectRef
	sessionArtifacts          map[string][]managedagents.SessionArtifact
	workers                   map[string]managedagents.Worker
	workerWork                map[string]managedagents.WorkerWork
	subscribers               map[string]map[chan struct{}]struct{}
	startRequests             map[string]managedagents.SubagentStartRequest
	taskGroups                map[string]managedagents.SubagentTaskGroup
	taskGroupItems            map[string][]managedagents.SubagentTaskGroupItem
	operatorAudits            []managedagents.OperatorAuditRecord
	deliberations             map[string]managedagents.AgentDeliberation
	deliberationParticipants  map[string][]managedagents.AgentDeliberationParticipant
	deliberationRounds        map[string]map[int]managedagents.AgentDeliberationRound
	deliberationContributions map[string]map[int]map[int]managedagents.AgentDeliberationContribution
	skillRecords              map[string]skills.Skill
	skillVersions             map[string][]skills.Version
	skillDrafts               map[string]skills.Draft
	skillUsages               []skills.Usage
	marketplacePolicies       map[string]skillmarketplace.PolicyRecord
	marketplacePolicyVersions map[string][]skillmarketplace.PolicyVersion
	environmentVariables      map[string]map[string]envvars.EncryptedVariable
	mcpRegistryServers        map[string]mcpregistry.Server
	mcpRegistryVersions       map[string][]mcpregistry.Version
	runIdempotency            map[string]map[string]testRunIdempotency
	agentSchedules            map[string]managedagents.AgentSchedule
	workspaceToolPolicies     map[string]managedagents.WorkspaceToolPermissionPolicy
}

type testRunIdempotency struct {
	RunID       string
	RequestHash string
}

func newTestStore() *testStore {
	store := &testStore{
		agents:                    make(map[string]managedagents.Agent),
		agentConfigVersions:       make(map[string][]managedagents.AgentConfigVersion),
		providers:                 make(map[string]managedagents.LLMProvider),
		models:                    make(map[string]managedagents.LLMModel),
		environments:              make(map[string]managedagents.Environment),
		sessions:                  make(map[string]managedagents.Session),
		summaries:                 make(map[string]managedagents.SessionSummary),
		taskPlans:                 make(map[string][]managedagents.SessionTaskPlan),
		interventions:             make(map[string]managedagents.SessionIntervention),
		events:                    make(map[string][]managedagents.Event),
		traceIndexes:              make(map[string]managedagents.TraceIndexEntry),
		traceSpanIndexes:          make(map[string][]managedagents.TraceSpanIndexEntry),
		objectRefs:                make(map[string]managedagents.ObjectRef),
		sessionArtifacts:          make(map[string][]managedagents.SessionArtifact),
		workers:                   make(map[string]managedagents.Worker),
		workerWork:                make(map[string]managedagents.WorkerWork),
		subscribers:               make(map[string]map[chan struct{}]struct{}),
		startRequests:             make(map[string]managedagents.SubagentStartRequest),
		taskGroups:                make(map[string]managedagents.SubagentTaskGroup),
		taskGroupItems:            make(map[string][]managedagents.SubagentTaskGroupItem),
		deliberations:             make(map[string]managedagents.AgentDeliberation),
		deliberationParticipants:  make(map[string][]managedagents.AgentDeliberationParticipant),
		deliberationRounds:        make(map[string]map[int]managedagents.AgentDeliberationRound),
		deliberationContributions: make(map[string]map[int]map[int]managedagents.AgentDeliberationContribution),
		skillRecords:              make(map[string]skills.Skill),
		skillVersions:             make(map[string][]skills.Version),
		skillDrafts:               make(map[string]skills.Draft),
		marketplacePolicies:       make(map[string]skillmarketplace.PolicyRecord),
		marketplacePolicyVersions: make(map[string][]skillmarketplace.PolicyVersion),
		environmentVariables:      make(map[string]map[string]envvars.EncryptedVariable),
		mcpRegistryServers:        make(map[string]mcpregistry.Server),
		mcpRegistryVersions:       make(map[string][]mcpregistry.Version),
		runIdempotency:            make(map[string]map[string]testRunIdempotency),
		agentSchedules:            make(map[string]managedagents.AgentSchedule),
		workspaceToolPolicies:     make(map[string]managedagents.WorkspaceToolPermissionPolicy),
	}
	now := time.Now().UTC()
	store.providers["fake"] = managedagents.LLMProvider{
		ID:           "fake",
		ProviderType: "fake",
		Enabled:      true,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	store.models[llmModelKey("fake", "fake-demo")] = managedagents.LLMModel{
		ProviderID:          "fake",
		Model:               "fake-demo",
		ContextWindowTokens: managedagents.DefaultContextWindowTokens,
		CapabilityType:      managedagents.LLMModelCapabilityText,
		Revision:            1,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	return store
}

func (s *testStore) ListEncryptedEnvironmentVariables(ctx context.Context, workspaceID string) ([]envvars.EncryptedVariable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.environmentVariables[workspaceID]
	result := make([]envvars.EncryptedVariable, 0, len(items))
	for _, item := range items {
		if scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx); ok {
			if scope.WorkspaceID != workspaceID {
				return nil, managedagents.ErrForbidden
			}
			if item.OwnerID != "" && item.OwnerID != scope.OwnerID {
				continue
			}
		} else if item.OwnerID != "" {
			continue
		}
		item.Ciphertext = append([]byte(nil), item.Ciphertext...)
		result = append(result, item)
	}
	return result, nil
}

func (s *testStore) UpsertEncryptedEnvironmentVariable(ctx context.Context, input envvars.EncryptedVariable) (envvars.EncryptedVariable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ownerID := strings.TrimSpace(input.OwnerID)
	if scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx); ok {
		if scope.WorkspaceID != input.WorkspaceID {
			return envvars.EncryptedVariable{}, managedagents.ErrForbidden
		}
		if ownerID == "" {
			ownerID = scope.OwnerID
		}
		if ownerID != scope.OwnerID {
			return envvars.EncryptedVariable{}, managedagents.ErrForbidden
		}
	}
	if s.environmentVariables[input.WorkspaceID] == nil {
		s.environmentVariables[input.WorkspaceID] = make(map[string]envvars.EncryptedVariable)
	}
	now := time.Now().UTC()
	key := environmentVariableStoreKey(ownerID, input.Name)
	existing := s.environmentVariables[input.WorkspaceID][key]
	if existing.CreatedAt.IsZero() {
		existing.CreatedAt = now
	}
	existing.WorkspaceID = input.WorkspaceID
	existing.OwnerID = ownerID
	existing.Name = input.Name
	existing.Ciphertext = append([]byte(nil), input.Ciphertext...)
	existing.UpdatedAt = now
	s.environmentVariables[input.WorkspaceID][key] = existing
	return existing, nil
}

func (s *testStore) DeleteEncryptedEnvironmentVariable(ctx context.Context, workspaceID string, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ownerID := ""
	if scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx); ok {
		if scope.WorkspaceID != workspaceID {
			return managedagents.ErrForbidden
		}
		ownerID = scope.OwnerID
	}
	key := environmentVariableStoreKey(ownerID, name)
	if _, ok := s.environmentVariables[workspaceID][key]; !ok {
		return managedagents.ErrNotFound
	}
	delete(s.environmentVariables[workspaceID], key)
	return nil
}

func environmentVariableStoreKey(ownerID string, name string) string {
	return strings.TrimSpace(ownerID) + "\x00" + strings.TrimSpace(name)
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

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	provider := managedagents.LLMProvider{
		ID:           input.ID,
		ProviderType: input.ProviderType,
		BaseURL:      input.BaseURL,
		APIKeyEnv:    input.APIKeyEnv,
		Enabled:      input.Enabled,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if existing, ok := s.providers[provider.ID]; ok {
		provider.Revision = existing.Revision + 1
		provider.CreatedAt = existing.CreatedAt
	}
	s.providers[provider.ID] = provider
	return provider, nil
}

func (s *testStore) CreateLLMProvider(input managedagents.UpsertLLMProviderInput) (managedagents.LLMProvider, error) {
	if input.ID == "" || input.ProviderType == "" {
		return managedagents.LLMProvider{}, managedagents.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.providers[input.ID]; exists {
		return managedagents.LLMProvider{}, fmt.Errorf("%w: llm provider %s already exists", managedagents.ErrConflict, input.ID)
	}
	now := time.Now().UTC()
	provider := managedagents.LLMProvider{
		ID: input.ID, ProviderType: input.ProviderType, BaseURL: input.BaseURL, APIKeyEnv: input.APIKeyEnv,
		Enabled: input.Enabled, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	s.providers[provider.ID] = provider
	return provider, nil
}

func (s *testStore) UpdateLLMProvider(input managedagents.UpdateLLMProviderInput) (managedagents.LLMProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, exists := s.providers[input.ID]
	if !exists {
		return managedagents.LLMProvider{}, managedagents.ErrNotFound
	}
	if input.ExpectedRevision != provider.Revision {
		return managedagents.LLMProvider{}, fmt.Errorf("%w: llm provider revision changed", managedagents.ErrRevisionConflict)
	}
	provider.ProviderType = input.ProviderType
	provider.BaseURL = input.BaseURL
	provider.APIKeyEnv = input.APIKeyEnv
	provider.Enabled = input.Enabled
	provider.Revision++
	provider.UpdatedAt = time.Now().UTC()
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
	if provider.Enabled != enabled {
		provider.Enabled = enabled
		provider.Revision++
		provider.UpdatedAt = time.Now().UTC()
	}
	s.providers[id] = provider
	return provider, nil
}

func (s *testStore) SetLLMProviderEnabledIfRevision(id string, enabled bool, expectedRevision int64) (managedagents.LLMProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[id]
	if !ok {
		return managedagents.LLMProvider{}, managedagents.ErrNotFound
	}
	if provider.Revision != expectedRevision {
		return managedagents.LLMProvider{}, fmt.Errorf("%w: llm provider revision changed", managedagents.ErrRevisionConflict)
	}
	if provider.Enabled != enabled {
		provider.Enabled = enabled
		provider.Revision++
		provider.UpdatedAt = time.Now().UTC()
	}
	s.providers[id] = provider
	return provider, nil
}

func (s *testStore) DeleteLLMProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return managedagents.ErrNotFound
	}
	for _, versions := range s.agentConfigVersions {
		for _, version := range versions {
			if version.LLMProvider == id {
				return fmt.Errorf("%w: llm provider %s is referenced by an agent configuration or session", managedagents.ErrConflict, id)
			}
		}
	}
	delete(s.providers, id)
	for key, model := range s.models {
		if model.ProviderID == id {
			delete(s.models, key)
		}
	}
	return nil
}

func (s *testStore) DeleteLLMProviderIfRevision(id string, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[id]
	if !ok {
		return managedagents.ErrNotFound
	}
	if provider.Revision != expectedRevision {
		return fmt.Errorf("%w: llm provider revision changed", managedagents.ErrRevisionConflict)
	}
	for _, versions := range s.agentConfigVersions {
		for _, version := range versions {
			if version.LLMProvider == id {
				return fmt.Errorf("%w: llm provider %s is referenced by an agent configuration or session", managedagents.ErrConflict, id)
			}
		}
	}
	delete(s.providers, id)
	for key, model := range s.models {
		if model.ProviderID == id {
			delete(s.models, key)
		}
	}
	return nil
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
	key := llmModelKey(input.ProviderID, input.Model)
	existing, exists := s.models[key]
	var existingModel *managedagents.LLMModel
	if exists {
		existingModel = &existing
	}
	normalized, err := managedagents.NormalizeLLMModelInput(input, existingModel)
	if err != nil {
		return managedagents.LLMModel{}, err
	}
	now := time.Now().UTC()
	model := managedagents.LLMModel{
		ProviderID:          normalized.ProviderID,
		Model:               normalized.Model,
		ContextWindowTokens: normalized.ContextWindowTokens,
		CapabilityType:      normalized.CapabilityType,
		Capabilities:        *normalized.Capabilities,
		IsDefaultVision:     *normalized.IsDefaultVision,
		IsDefaultEmbedding:  *normalized.IsDefaultEmbedding,
		IsDefaultReranker:   *normalized.IsDefaultReranker,
		Revision:            1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if exists {
		model.Revision = existing.Revision
		model.CreatedAt = existing.CreatedAt
		if existing.ContextWindowTokens != model.ContextWindowTokens || existing.CapabilityType != model.CapabilityType ||
			existing.Capabilities != model.Capabilities || existing.IsDefaultVision != model.IsDefaultVision ||
			existing.IsDefaultEmbedding != model.IsDefaultEmbedding || existing.IsDefaultReranker != model.IsDefaultReranker {
			model.Revision++
		} else {
			model.UpdatedAt = existing.UpdatedAt
		}
	}
	if model.IsDefaultVision || model.IsDefaultEmbedding || model.IsDefaultReranker {
		for existingKey, candidate := range s.models {
			changed := false
			if existingKey != key && model.IsDefaultVision && candidate.IsDefaultVision {
				candidate.IsDefaultVision, changed = false, true
			}
			if existingKey != key && model.IsDefaultEmbedding && candidate.IsDefaultEmbedding {
				candidate.IsDefaultEmbedding, changed = false, true
			}
			if existingKey != key && model.IsDefaultReranker && candidate.IsDefaultReranker {
				candidate.IsDefaultReranker, changed = false, true
			}
			if changed {
				candidate.Revision++
				candidate.UpdatedAt = now
				s.models[existingKey] = candidate
			}
		}
	}
	s.models[key] = model
	return model, nil
}

func (s *testStore) CreateLLMModel(input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := llmModelKey(input.ProviderID, input.Model)
	if input.ProviderID == "" || input.Model == "" {
		return managedagents.LLMModel{}, managedagents.ErrInvalid
	}
	if _, ok := s.providers[input.ProviderID]; !ok {
		return managedagents.LLMModel{}, managedagents.ErrNotFound
	}
	if _, exists := s.models[key]; exists {
		return managedagents.LLMModel{}, fmt.Errorf("%w: llm model already exists", managedagents.ErrConflict)
	}
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	normalized, err := managedagents.NormalizeLLMModelInput(input, nil)
	if err != nil {
		return managedagents.LLMModel{}, err
	}
	now := time.Now().UTC()
	if *normalized.IsDefaultVision || *normalized.IsDefaultEmbedding || *normalized.IsDefaultReranker {
		for existingKey, candidate := range s.models {
			changed := false
			if *normalized.IsDefaultVision && candidate.IsDefaultVision {
				candidate.IsDefaultVision, changed = false, true
			}
			if *normalized.IsDefaultEmbedding && candidate.IsDefaultEmbedding {
				candidate.IsDefaultEmbedding, changed = false, true
			}
			if *normalized.IsDefaultReranker && candidate.IsDefaultReranker {
				candidate.IsDefaultReranker, changed = false, true
			}
			if changed {
				candidate.Revision++
				candidate.UpdatedAt = now
				s.models[existingKey] = candidate
			}
		}
	}
	model := managedagents.LLMModel{
		ProviderID: normalized.ProviderID, Model: normalized.Model, ContextWindowTokens: normalized.ContextWindowTokens,
		CapabilityType: normalized.CapabilityType, Capabilities: *normalized.Capabilities,
		IsDefaultVision: *normalized.IsDefaultVision, IsDefaultEmbedding: *normalized.IsDefaultEmbedding,
		IsDefaultReranker: *normalized.IsDefaultReranker, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	s.models[key] = model
	return model, nil
}

func (s *testStore) UpdateLLMModel(input managedagents.UpdateLLMModelInput) (managedagents.LLMModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := llmModelKey(input.ProviderID, input.Model)
	existing, ok := s.models[key]
	if !ok {
		return managedagents.LLMModel{}, managedagents.ErrNotFound
	}
	if input.ExpectedRevision != existing.Revision {
		return managedagents.LLMModel{}, fmt.Errorf("%w: llm model revision changed", managedagents.ErrRevisionConflict)
	}
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	normalized, err := managedagents.NormalizeLLMModelInput(input.UpsertLLMModelInput, &existing)
	if err != nil {
		return managedagents.LLMModel{}, err
	}
	now := time.Now().UTC()
	if *normalized.IsDefaultVision || *normalized.IsDefaultEmbedding || *normalized.IsDefaultReranker {
		for existingKey, candidate := range s.models {
			changed := false
			if existingKey != key && *normalized.IsDefaultVision && candidate.IsDefaultVision {
				candidate.IsDefaultVision, changed = false, true
			}
			if existingKey != key && *normalized.IsDefaultEmbedding && candidate.IsDefaultEmbedding {
				candidate.IsDefaultEmbedding, changed = false, true
			}
			if existingKey != key && *normalized.IsDefaultReranker && candidate.IsDefaultReranker {
				candidate.IsDefaultReranker, changed = false, true
			}
			if changed {
				candidate.Revision++
				candidate.UpdatedAt = now
				s.models[existingKey] = candidate
			}
		}
	}
	existing.ContextWindowTokens = normalized.ContextWindowTokens
	existing.CapabilityType = normalized.CapabilityType
	existing.Capabilities = *normalized.Capabilities
	existing.IsDefaultVision = *normalized.IsDefaultVision
	existing.IsDefaultEmbedding = *normalized.IsDefaultEmbedding
	existing.IsDefaultReranker = *normalized.IsDefaultReranker
	existing.Revision++
	existing.UpdatedAt = now
	s.models[key] = existing
	return existing, nil
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

func (s *testStore) DeleteLLMModel(providerID string, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := llmModelKey(providerID, model)
	if _, ok := s.models[key]; !ok {
		return managedagents.ErrNotFound
	}
	for _, versions := range s.agentConfigVersions {
		for _, version := range versions {
			if version.LLMProvider == providerID && version.LLMModel == model {
				return fmt.Errorf("%w: llm model %s/%s is referenced by an agent configuration or session", managedagents.ErrConflict, providerID, model)
			}
		}
	}
	delete(s.models, key)
	return nil
}

func (s *testStore) DeleteLLMModelIfRevision(providerID string, model string, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := llmModelKey(providerID, model)
	existing, ok := s.models[key]
	if !ok {
		return managedagents.ErrNotFound
	}
	if existing.Revision != expectedRevision {
		return fmt.Errorf("%w: llm model revision changed", managedagents.ErrRevisionConflict)
	}
	for _, versions := range s.agentConfigVersions {
		for _, version := range versions {
			if version.LLMProvider == providerID && version.LLMModel == model {
				return fmt.Errorf("%w: llm model %s/%s is referenced by an agent configuration or session", managedagents.ErrConflict, providerID, model)
			}
		}
	}
	delete(s.models, key)
	return nil
}

func llmModelKey(providerID string, model string) string {
	return providerID + "\x00" + model
}

func (s *testStore) CreateSkill(ctx context.Context, input skills.CreateSkillInput) (skills.Skill, error) {
	if err := skills.ValidateIdentifier(input.Identifier); err != nil {
		return skills.Skill{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if strings.TrimSpace(input.Title) == "" {
		return skills.Skill{}, fmt.Errorf("%w: skill title is required", managedagents.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	ownerType := defaultString(input.OwnerType, skills.OwnerTypeWorkspace)
	ownerID := input.OwnerID
	visibility := input.Visibility
	if ownerType == skills.OwnerTypeUser {
		if scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx); ok {
			if ownerID == "" {
				ownerID = scope.OwnerID
			}
			if scope.OwnerID == "" || ownerID != scope.OwnerID {
				return skills.Skill{}, managedagents.ErrForbidden
			}
		}
		visibility = defaultString(visibility, skills.VisibilityPrivate)
	} else {
		ownerID = workspaceID
		visibility = defaultString(visibility, skills.VisibilityWorkspace)
	}
	for _, existing := range s.skillRecords {
		if existing.WorkspaceID == workspaceID && existing.Identifier == input.Identifier &&
			((existing.OwnerType == skills.OwnerTypeUser && ownerType == skills.OwnerTypeUser && existing.OwnerID == ownerID) ||
				(existing.OwnerType != skills.OwnerTypeUser && ownerType != skills.OwnerTypeUser)) {
			return skills.Skill{}, fmt.Errorf("%w: skill already exists", managedagents.ErrConflict)
		}
	}
	s.nextSkillID++
	item := skills.Skill{
		ID: fmt.Sprintf("skl_%d", s.nextSkillID), WorkspaceID: workspaceID, Identifier: input.Identifier,
		Title: input.Title, Description: input.Description, OwnerType: ownerType, OwnerID: ownerID, Visibility: visibility,
		ForkedFromSkillID: input.ForkedFromSkillID, ForkedFromVersion: input.ForkedFromVersion,
		SourcePluginID: input.SourcePluginID, SourceType: defaultString(input.SourceType, skills.SourceTypeInline),
		SourceLocator: input.SourceLocator, SourcePath: input.SourcePath,
		Status: skills.StatusActive, CreatedBy: defaultString(input.CreatedBy, "system"), CreatedAt: time.Now().UTC(),
	}
	s.skillRecords[item.ID] = item
	return item, nil
}

func (s *testStore) GetSkill(_ context.Context, id string) (skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.skillRecords[id]
	if !ok {
		return skills.Skill{}, managedagents.ErrNotFound
	}
	return item, nil
}

func (s *testStore) GetSkillByIdentifier(ctx context.Context, workspaceID string, identifier string) (skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaceID = defaultString(workspaceID, managedagents.DefaultWorkspaceID)
	var shared *skills.Skill
	for _, item := range s.skillRecords {
		if item.WorkspaceID == workspaceID && item.Identifier == identifier {
			if item.OwnerType == skills.OwnerTypeUser {
				if scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx); ok && item.OwnerID == scope.OwnerID {
					return item, nil
				}
				continue
			}
			copy := item
			shared = &copy
		}
	}
	if shared != nil {
		return *shared, nil
	}
	return skills.Skill{}, managedagents.ErrNotFound
}

func (s *testStore) ListSkills(ctx context.Context, input skills.ListSkillsInput) ([]skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	items := make([]skills.Skill, 0)
	for _, item := range s.skillRecords {
		visible := true
		if scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx); ok && item.OwnerType == skills.OwnerTypeUser && scope.OwnerID != "" && item.OwnerID != scope.OwnerID {
			visible = false
		}
		if item.WorkspaceID == workspaceID && visible && (input.IncludeArchived || item.Status == skills.StatusActive) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Identifier < items[right].Identifier })
	return items, nil
}

func (s *testStore) ArchiveSkill(_ context.Context, id string) (skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.skillRecords[id]
	if !ok {
		return skills.Skill{}, managedagents.ErrNotFound
	}
	if item.Status == skills.StatusArchived {
		return item, nil
	}
	for _, agent := range s.agents {
		if agent.ArchivedAt != nil || agent.WorkspaceID != item.WorkspaceID {
			continue
		}
		config, normalized := skills.NormalizeConfig(agent.ConfigVersion.Skills)
		if !normalized {
			return skills.Skill{}, fmt.Errorf("%w: cannot archive skill %q while Agent %s has an unreadable current skills config", managedagents.ErrConflict, item.Identifier, agent.ID)
		}
		for _, binding := range config.Enabled {
			if binding.SkillID == item.ID || (binding.SkillID == "" && binding.Skill == item.Identifier) {
				return skills.Skill{}, fmt.Errorf("%w: cannot archive skill %q while Agent %s currently enables it; disable it first", managedagents.ErrConflict, item.Identifier, agent.ID)
			}
		}
	}
	now := time.Now().UTC()
	item.Status = skills.StatusArchived
	item.ArchivedAt = &now
	s.skillRecords[id] = item
	return item, nil
}

func (s *testStore) CreateSkillVersion(_ context.Context, input skills.CreateVersionInput) (skills.Version, error) {
	if err := skills.ValidateManifest(input.Manifest); err != nil {
		return skills.Version{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.skillRecords[input.SkillID]
	if !ok {
		return skills.Version{}, managedagents.ErrNotFound
	}
	if item.Status != skills.StatusActive {
		return skills.Version{}, managedagents.ErrConflict
	}
	manifest := cloneRaw(input.Manifest)
	if len(manifest) == 0 || string(manifest) == "null" {
		manifest = json.RawMessage(`{}`)
	}
	assets := cloneRaw(input.Assets)
	if len(assets) == 0 || string(assets) == "null" {
		assets = json.RawMessage(`[]`)
	}
	s.nextSkillVersionID++
	versionNumber := len(s.skillVersions[input.SkillID]) + 1
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(string(manifest)+"\x00"+input.ContentText+"\x00"+string(assets))))
	version := skills.Version{
		ID: fmt.Sprintf("sklv_%d", s.nextSkillVersionID), SkillID: input.SkillID, Version: versionNumber,
		ContentFormat: defaultString(input.ContentFormat, "hybrid"), Manifest: manifest, ContentText: input.ContentText,
		Assets: assets, Checksum: checksum, SourceRef: input.SourceRef, SourceRevision: input.SourceRevision,
		SourceURL: input.SourceURL, CreatedBy: defaultString(input.CreatedBy, "system"), CreatedAt: time.Now().UTC(),
	}
	s.skillVersions[input.SkillID] = append(s.skillVersions[input.SkillID], version)
	return version, nil
}

func (s *testStore) GetSkillDraft(_ context.Context, skillID string) (skills.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.skillDrafts[skillID]
	if !ok {
		return skills.Draft{}, managedagents.ErrNotFound
	}
	return draft, nil
}

func (s *testStore) PutSkillDraft(_ context.Context, input skills.PutDraftInput) (skills.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	skill, ok := s.skillRecords[input.SkillID]
	if !ok {
		return skills.Draft{}, managedagents.ErrNotFound
	}
	if skill.OwnerType != skills.OwnerTypeUser {
		return skills.Draft{}, managedagents.ErrForbidden
	}
	current := s.skillDrafts[input.SkillID]
	if input.ExpectedRevision > 0 && current.Revision != input.ExpectedRevision {
		return skills.Draft{}, managedagents.ErrRevisionConflict
	}
	revision := current.Revision + 1
	if revision == 0 {
		revision = 1
	}
	draft := skills.Draft{SkillID: input.SkillID, Revision: revision, ContentFormat: defaultString(input.ContentFormat, "hybrid"),
		Manifest: cloneRaw(input.Manifest), ContentText: input.ContentText, Assets: cloneRaw(input.Assets),
		UpdatedBy: defaultString(input.UpdatedBy, "system"), UpdatedAt: time.Now().UTC()}
	if len(draft.Manifest) == 0 {
		draft.Manifest = json.RawMessage(`{}`)
	}
	if len(draft.Assets) == 0 {
		draft.Assets = json.RawMessage(`[]`)
	}
	s.skillDrafts[input.SkillID] = draft
	return draft, nil
}

func (s *testStore) PublishSkillDraft(ctx context.Context, skillID string, expectedRevision int64, createdBy string) (skills.Version, error) {
	draft, err := s.GetSkillDraft(ctx, skillID)
	if err != nil {
		return skills.Version{}, err
	}
	if expectedRevision > 0 && draft.Revision != expectedRevision {
		return skills.Version{}, managedagents.ErrRevisionConflict
	}
	version, err := s.CreateSkillVersion(ctx, skills.CreateVersionInput{SkillID: skillID, ContentFormat: draft.ContentFormat, Manifest: draft.Manifest, ContentText: draft.ContentText, Assets: draft.Assets, CreatedBy: createdBy})
	if err != nil {
		return skills.Version{}, err
	}
	s.mu.Lock()
	delete(s.skillDrafts, skillID)
	s.mu.Unlock()
	return version, nil
}

func (s *testStore) GetSkillVersion(_ context.Context, skillID string, versionNumber int) (skills.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, version := range s.skillVersions[skillID] {
		if version.Version == versionNumber {
			return version, nil
		}
	}
	return skills.Version{}, managedagents.ErrNotFound
}

func (s *testStore) ListSkillVersions(_ context.Context, skillID string) ([]skills.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.skillRecords[skillID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	versions := append([]skills.Version(nil), s.skillVersions[skillID]...)
	slices.Reverse(versions)
	return versions, nil
}

func (s *testStore) RecordSkillUsages(_ context.Context, usages []skills.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillUsages = append(s.skillUsages, usages...)
	return nil
}

func (s *testStore) ListSkillUsages(_ context.Context, sessionID string, turnID string) ([]skills.Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]skills.Usage, 0)
	for _, usage := range s.skillUsages {
		if usage.SessionID == sessionID && (turnID == "" || usage.TurnID == turnID) {
			result = append(result, usage)
		}
	}
	return result, nil
}

func (s *testStore) CreateMarketplacePolicy(_ context.Context, input skillmarketplace.CreatePolicyInput) (skillmarketplace.PolicyRecord, skillmarketplace.PolicyVersion, error) {
	normalized, err := skillmarketplace.NormalizePolicy(input.Config)
	if err != nil {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if input.ScopeType != skillmarketplace.PolicyScopeOrganization && input.ScopeType != skillmarketplace.PolicyScopeWorkspace {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, managedagents.ErrInvalid
	}
	if input.ScopeType == skillmarketplace.PolicyScopeOrganization && (input.OrganizationID == "" || input.WorkspaceID != "") {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, managedagents.ErrInvalid
	}
	if input.ScopeType == skillmarketplace.PolicyScopeWorkspace && (input.WorkspaceID == "" || input.OrganizationID != "") {
		return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, managedagents.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.marketplacePolicies {
		if existing.Status != skillmarketplace.PolicyStatusActive {
			continue
		}
		if existing.ScopeType == input.ScopeType && existing.OrganizationID == input.OrganizationID && existing.WorkspaceID == input.WorkspaceID {
			return skillmarketplace.PolicyRecord{}, skillmarketplace.PolicyVersion{}, managedagents.ErrConflict
		}
	}
	s.nextPolicyID++
	s.nextPolicyVersionID++
	now := time.Now().UTC()
	revision, _ := skillmarketplace.PolicyRevision(normalized)
	record := skillmarketplace.PolicyRecord{
		ID: fmt.Sprintf("smpol_%06d", s.nextPolicyID), ScopeType: input.ScopeType,
		OrganizationID: input.OrganizationID, WorkspaceID: input.WorkspaceID,
		Status: skillmarketplace.PolicyStatusActive, CurrentVersion: 1,
		CreatedBy: defaultString(input.CreatedBy, "system"), CreatedAt: now,
	}
	version := skillmarketplace.PolicyVersion{
		ID: fmt.Sprintf("smpv_%06d", s.nextPolicyVersionID), PolicyID: record.ID, Version: 1,
		Config: normalized, Checksum: revision, CreatedBy: record.CreatedBy, CreatedAt: now,
	}
	s.marketplacePolicies[record.ID] = record
	s.marketplacePolicyVersions[record.ID] = []skillmarketplace.PolicyVersion{version}
	return record, version, nil
}

func (s *testStore) GetMarketplacePolicy(_ context.Context, id string) (skillmarketplace.PolicyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.marketplacePolicies[id]
	if !ok {
		return skillmarketplace.PolicyRecord{}, managedagents.ErrNotFound
	}
	return item, nil
}

func (s *testStore) ListMarketplacePolicies(_ context.Context, input skillmarketplace.ListPoliciesInput) ([]skillmarketplace.PolicyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []skillmarketplace.PolicyRecord{}
	for _, item := range s.marketplacePolicies {
		if input.OrganizationID != "" && item.OrganizationID != input.OrganizationID {
			continue
		}
		if input.WorkspaceID != "" && item.WorkspaceID != input.WorkspaceID {
			continue
		}
		if !input.IncludeArchived && item.Status != skillmarketplace.PolicyStatusActive {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *testStore) PublishMarketplacePolicyVersion(_ context.Context, policyID string, config skillmarketplace.Policy, createdBy string) (skillmarketplace.PolicyVersion, error) {
	normalized, err := skillmarketplace.NormalizePolicy(config)
	if err != nil {
		return skillmarketplace.PolicyVersion{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.marketplacePolicies[policyID]
	if !ok {
		return skillmarketplace.PolicyVersion{}, managedagents.ErrNotFound
	}
	if record.Status != skillmarketplace.PolicyStatusActive {
		return skillmarketplace.PolicyVersion{}, managedagents.ErrConflict
	}
	s.nextPolicyVersionID++
	revision, _ := skillmarketplace.PolicyRevision(normalized)
	version := skillmarketplace.PolicyVersion{
		ID: fmt.Sprintf("smpv_%06d", s.nextPolicyVersionID), PolicyID: policyID,
		Version: record.CurrentVersion + 1, Config: normalized, Checksum: revision,
		CreatedBy: defaultString(createdBy, "system"), CreatedAt: time.Now().UTC(),
	}
	record.CurrentVersion = version.Version
	s.marketplacePolicies[policyID] = record
	s.marketplacePolicyVersions[policyID] = append(s.marketplacePolicyVersions[policyID], version)
	return version, nil
}

func (s *testStore) GetMarketplacePolicyVersion(_ context.Context, policyID string, versionNumber int) (skillmarketplace.PolicyVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, version := range s.marketplacePolicyVersions[policyID] {
		if version.Version == versionNumber {
			return version, nil
		}
	}
	return skillmarketplace.PolicyVersion{}, managedagents.ErrNotFound
}

func (s *testStore) ArchiveMarketplacePolicy(_ context.Context, id string) (skillmarketplace.PolicyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.marketplacePolicies[id]
	if !ok {
		return skillmarketplace.PolicyRecord{}, managedagents.ErrNotFound
	}
	now := time.Now().UTC()
	record.Status = skillmarketplace.PolicyStatusArchived
	record.ArchivedAt = &now
	s.marketplacePolicies[id] = record
	return record, nil
}

func (s *testStore) ResolveMarketplacePolicy(_ context.Context, workspaceID string) (skillmarketplace.EffectivePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaceID = defaultString(workspaceID, managedagents.DefaultWorkspaceID)
	var selected *skillmarketplace.PolicyRecord
	for _, item := range s.marketplacePolicies {
		if item.Status != skillmarketplace.PolicyStatusActive {
			continue
		}
		if item.ScopeType == skillmarketplace.PolicyScopeWorkspace && item.WorkspaceID == workspaceID {
			copy := item
			selected = &copy
			break
		}
		if selected == nil && item.ScopeType == skillmarketplace.PolicyScopeOrganization && item.OrganizationID == "org_default" {
			copy := item
			selected = &copy
		}
	}
	if selected == nil {
		return skillmarketplace.EffectivePolicy{}, managedagents.ErrNotFound
	}
	versions := s.marketplacePolicyVersions[selected.ID]
	for _, version := range versions {
		if version.Version == selected.CurrentVersion {
			return skillmarketplace.EffectivePolicy{
				Source: selected.ScopeType, Policy: *selected, Version: version, Config: version.Config, Revision: version.Checksum,
			}, nil
		}
	}
	return skillmarketplace.EffectivePolicy{}, managedagents.ErrNotFound
}

func (s *testStore) EnsureAgent(input managedagents.EnsureAgentInput) (managedagents.Agent, error) {
	if input.ID == "" || input.Name == "" || input.LLMProvider == "" || input.LLMModel == "" {
		return managedagents.Agent{}, managedagents.ErrInvalid
	}
	normalizedMCP, err := normalizeTestStoreMCP(input.MCP)
	if err != nil {
		return managedagents.Agent{}, err
	}
	input.MCP = normalizedMCP

	s.mu.Lock()
	defer s.mu.Unlock()
	if agent, ok := s.agents[input.ID]; ok {
		agent.ArchivedAt = nil
		s.agents[input.ID] = agent
		return agent, nil
	}
	provider, ok := s.providers[input.LLMProvider]
	if !ok {
		return managedagents.Agent{}, managedagents.ErrNotFound
	}
	if !provider.Enabled {
		return managedagents.Agent{}, managedagents.ErrInvalid
	}

	now := time.Now().UTC()
	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	ownership, err := managedagents.NormalizeAgentOwnership(workspaceID, managedagents.AgentOwnership{
		OwnerType: input.OwnerType, OwnerID: input.OwnerID, Visibility: input.Visibility, AgentKind: input.AgentKind,
	})
	if err != nil {
		return managedagents.Agent{}, err
	}
	agent := managedagents.Agent{
		ID:                   input.ID,
		WorkspaceID:          workspaceID,
		OwnerType:            ownership.OwnerType,
		OwnerID:              ownership.OwnerID,
		Visibility:           ownership.Visibility,
		AgentKind:            ownership.AgentKind,
		Name:                 input.Name,
		CurrentConfigVersion: 1,
		ConfigVersion: managedagents.AgentConfigVersion{
			Version:     1,
			LLMProvider: input.LLMProvider,
			LLMModel:    input.LLMModel,
			System:      input.System,
			Tools:       cloneRaw(input.Tools),
			MCP:         cloneRaw(input.MCP),
			Skills:      cloneRaw(input.Skills),
			CreatedAt:   now,
		},
		CreatedAt: now,
	}
	s.agents[input.ID] = agent
	s.agentConfigVersions[input.ID] = []managedagents.AgentConfigVersion{agent.ConfigVersion}
	return agent, nil
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
	normalizedMCP, err := normalizeTestStoreMCP(input.MCP)
	if err != nil {
		return managedagents.Agent{}, err
	}
	input.MCP = normalizedMCP

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
	ownership, err := managedagents.NormalizeAgentOwnership(workspaceID, managedagents.AgentOwnership{
		OwnerType: input.OwnerType, OwnerID: input.OwnerID, Visibility: input.Visibility, AgentKind: input.AgentKind,
	})
	if err != nil {
		return managedagents.Agent{}, err
	}
	agent := managedagents.Agent{
		ID:                   id,
		WorkspaceID:          workspaceID,
		OwnerType:            ownership.OwnerType,
		OwnerID:              ownership.OwnerID,
		Visibility:           ownership.Visibility,
		AgentKind:            ownership.AgentKind,
		Name:                 input.Name,
		CurrentConfigVersion: 1,
		ConfigVersion: managedagents.AgentConfigVersion{
			Version:     1,
			LLMProvider: input.LLMProvider,
			LLMModel:    input.LLMModel,
			System:      input.System,
			Tools:       cloneRaw(input.Tools),
			MCP:         cloneRaw(input.MCP),
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

func (s *testStore) GetAgentScoped(id string, scope managedagents.AccessScope) (managedagents.Agent, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.Agent{}, err
	}
	agent, err := s.GetAgent(id)
	if err != nil {
		return managedagents.Agent{}, managedagents.ErrNotFound
	}
	if agent.WorkspaceID != scope.WorkspaceID {
		return managedagents.Agent{}, managedagents.ErrForbidden
	}
	if scope.OwnerID != "" && agent.OwnerType == managedagents.AgentOwnerUser && agent.OwnerID != scope.OwnerID {
		return managedagents.Agent{}, managedagents.ErrForbidden
	}
	return agent, nil
}

func (s *testStore) ListAgents() ([]managedagents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agents := make([]managedagents.Agent, 0, len(s.agents))
	for _, agent := range s.agents {
		if agent.ArchivedAt != nil {
			continue
		}
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].CreatedAt.Equal(agents[j].CreatedAt) {
			return agents[i].ID > agents[j].ID
		}
		return agents[i].CreatedAt.After(agents[j].CreatedAt)
	})
	return agents, nil
}

func (s *testStore) ListAgentsScoped(scope managedagents.AccessScope) ([]managedagents.Agent, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	agents, err := s.ListAgents()
	if err != nil {
		return nil, err
	}
	filtered := agents[:0]
	for _, agent := range agents {
		if agent.WorkspaceID == scope.WorkspaceID &&
			(agent.OwnerType != managedagents.AgentOwnerUser || scope.OwnerID == "" || agent.OwnerID == scope.OwnerID) {
			filtered = append(filtered, agent)
		}
	}
	return filtered, nil
}

func (s *testStore) UpdateAgent(input managedagents.UpdateAgentInput) (managedagents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[input.AgentID]
	if !ok {
		return managedagents.Agent{}, managedagents.ErrNotFound
	}
	if strings.TrimSpace(input.Name) != "" {
		agent.Name = strings.TrimSpace(input.Name)
	}
	if strings.TrimSpace(input.LLMProvider) != "" || strings.TrimSpace(input.LLMModel) != "" || input.System != "" || input.Tools != nil || input.MCP != nil || input.Skills != nil {
		next := agent.ConfigVersion
		if strings.TrimSpace(input.LLMProvider) != "" {
			next.LLMProvider = strings.TrimSpace(input.LLMProvider)
		}
		if strings.TrimSpace(input.LLMModel) != "" {
			next.LLMModel = strings.TrimSpace(input.LLMModel)
		}
		if input.System != "" {
			next.System = input.System
		}
		if input.Tools != nil {
			next.Tools = cloneRaw(input.Tools)
		}
		if input.MCP != nil {
			normalizedMCP, normalizeErr := normalizeTestStoreMCP(input.MCP)
			if normalizeErr != nil {
				return managedagents.Agent{}, normalizeErr
			}
			next.MCP = normalizedMCP
		}
		if input.Skills != nil {
			next.Skills = cloneRaw(input.Skills)
		}
		agent.CurrentConfigVersion += 1
		next.Version = agent.CurrentConfigVersion
		next.CreatedAt = time.Now().UTC()
		agent.ConfigVersion = next
		s.agentConfigVersions[input.AgentID] = append(s.agentConfigVersions[input.AgentID], next)
	}
	s.agents[input.AgentID] = agent
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
	normalizedMCP, err := normalizeTestStoreMCP(input.MCP)
	if err != nil {
		return managedagents.Agent{}, err
	}
	input.MCP = normalizedMCP

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[input.AgentID]
	if !ok {
		return managedagents.Agent{}, managedagents.ErrNotFound
	}
	if input.ExpectedCurrentVersion > 0 && input.ExpectedCurrentVersion != agent.CurrentConfigVersion {
		return managedagents.Agent{}, fmt.Errorf("%w: Agent config changed from expected version %d to %d; retry against the latest config", managedagents.ErrRevisionConflict, input.ExpectedCurrentVersion, agent.CurrentConfigVersion)
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
		MCP:         cloneRaw(input.MCP),
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createSessionLocked(input)
}

func (s *testStore) CreateSubagentSession(input managedagents.CreateSubagentSessionInput) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parentSessionID := strings.TrimSpace(input.Session.ParentSessionID)
	parent, ok := s.sessions[parentSessionID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: parent session %s", managedagents.ErrNotFound, parentSessionID)
	}
	if err := enforceTestStoreSubagentLimits(s.sessions, parent, strings.TrimSpace(input.Session.ParentTurnID), input.Limits); err != nil {
		return managedagents.Session{}, err
	}
	input.Session.WorkspaceID = parent.WorkspaceID
	input.Session.OwnerID = parent.OwnerID
	input.Session.ParentSessionID = parent.ID
	input.Session.SpawnDepth = parent.SpawnDepth + 1
	return s.createSessionLocked(input.Session)
}

func (s *testStore) StartSubagentTurn(input managedagents.StartSubagentTurnInput) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[strings.TrimSpace(input.SessionID)]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.ParentSessionID == "" {
		return nil, fmt.Errorf("%w: session %s is not a subagent", managedagents.ErrInvalid, session.ID)
	}
	if parentSessionID := strings.TrimSpace(input.ParentSessionID); parentSessionID != "" && session.ParentSessionID != parentSessionID {
		return nil, fmt.Errorf("%w: session %s is not a child of parent session %s", managedagents.ErrInvalid, session.ID, parentSessionID)
	}
	if err := enforceTestStoreSubagentActiveLimits(s.sessions, session, input.Limits); err != nil {
		return nil, err
	}
	events, err := s.applyEventLocked(&session, managedagents.AppendEventInput{Type: managedagents.EventUserMessage, Payload: cloneRaw(input.Payload)}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.sessions[session.ID] = session
	return events, nil
}

func (s *testStore) EnqueueSubagentStart(input managedagents.EnqueueSubagentStartInput) (managedagents.SubagentStartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[strings.TrimSpace(input.SessionID)]
	if !ok {
		return managedagents.SubagentStartRequest{}, managedagents.ErrNotFound
	}
	for id, request := range s.startRequests {
		if request.Status == "pending" && !request.ExpiresAt.After(time.Now().UTC()) {
			request.Status = "expired"
			s.startRequests[id] = request
		}
		if request.Status == "pending" && request.SessionID == session.ID {
			return request, nil
		}
	}
	workspaceQueued := 0
	userQueued := 0
	for _, request := range s.startRequests {
		if request.Status != "pending" || request.WorkspaceID != session.WorkspaceID {
			continue
		}
		workspaceQueued++
		if request.OwnerID == session.OwnerID {
			userQueued++
		}
	}
	checks := []struct {
		current, limit           int
		errorType, policy, scope string
	}{
		{workspaceQueued, input.Limits.WorkspaceQueuedLimit, "subagent_workspace_queue_limit", "workspace_queue_limit", "workspace"},
		{userQueued, input.Limits.UserQueuedLimit, "subagent_user_queue_limit", "user_queue_limit", "owner"},
	}
	for _, check := range checks {
		if check.limit > 0 && check.current >= check.limit {
			return managedagents.SubagentStartRequest{}, testStoreSubagentViolation(check.errorType, "subagent queue limit reached", map[string]any{
				"scope": check.scope, "policy": check.policy, "current_queued": check.current, "limit": check.limit, "subagent_session_id": session.ID,
			})
		}
	}
	now := time.Now().UTC()
	timeout := time.Duration(input.Limits.QueueTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 100 * 365 * 24 * time.Hour
	}
	request := managedagents.SubagentStartRequest{
		ID: s.nextID("sreq", &s.nextStartRequestID), WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID,
		SessionID: session.ID, ParentSessionID: session.ParentSessionID, ParentTurnID: input.ParentTurnID,
		Payload: cloneRaw(input.Payload), Status: "pending", Priority: input.Priority, QueuedAt: now, ExpiresAt: now.Add(timeout), WaitSeconds: 0,
	}
	s.startRequests[request.ID] = request
	return request, nil
}

func (s *testStore) GetPendingSubagentStart(sessionID string) (managedagents.SubagentStartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, request := range s.startRequests {
		if request.SessionID == sessionID && request.Status == "pending" && request.ExpiresAt.After(time.Now().UTC()) {
			request.WaitSeconds = subagentStartWaitSeconds(request, time.Now().UTC())
			return request, nil
		}
	}
	return managedagents.SubagentStartRequest{}, managedagents.ErrNotFound
}

func (s *testStore) CancelSubagentStart(input managedagents.CancelSubagentStartInput) (managedagents.SubagentStartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, request := range s.startRequests {
		if request.SessionID != input.SessionID || request.Status != "pending" {
			continue
		}
		if input.ParentSessionID != "" && request.ParentSessionID != input.ParentSessionID {
			return managedagents.SubagentStartRequest{}, managedagents.ErrInvalid
		}
		now := time.Now().UTC()
		request.Status = "canceled"
		request.CanceledAt = &now
		request.CancelReason = defaultString(strings.TrimSpace(input.Reason), "canceled by parent agent")
		request.WaitSeconds = subagentStartWaitSeconds(request, now)
		s.startRequests[id] = request
		payload, _ := json.Marshal(map[string]any{
			"request_id":        request.ID,
			"session_id":        request.SessionID,
			"parent_session_id": request.ParentSessionID,
			"reason":            request.CancelReason,
			"canceled_at":       now,
			"wait_seconds":      request.WaitSeconds,
		})
		event := s.appendEventLocked(request.SessionID, managedagents.EventRuntimeSubagentStartCanceled, payload, now)
		s.publishLocked(event)
		return request, nil
	}
	return managedagents.SubagentStartRequest{}, managedagents.ErrNotFound
}

func (s *testStore) CreateSubagentTaskGroup(input managedagents.CreateSubagentTaskGroupInput) (managedagents.SubagentTaskGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	strategy := normalizeTestSubagentTaskGroupStrategy(input.Strategy)
	if strategy == "" {
		return managedagents.SubagentTaskGroup{}, managedagents.ErrInvalid
	}
	if input.PlannedCount <= 0 {
		return managedagents.SubagentTaskGroup{}, managedagents.ErrInvalid
	}
	group := managedagents.SubagentTaskGroup{
		ID:              s.nextID("sgrp", &s.nextStartRequestID),
		WorkspaceID:     input.WorkspaceID,
		OwnerID:         input.OwnerID,
		ParentSessionID: input.ParentSessionID,
		ParentTurnID:    input.ParentTurnID,
		ParentGroupID:   input.ParentGroupID,
		ParentItemIndex: input.ParentItemIndex,
		Strategy:        strategy,
		ResultReducer:   normalizeTestSubagentTaskGroupReducer(input.ResultReducer),
		Quorum:          input.Quorum,
		FailFast:        input.FailFast,
		PlannedCount:    input.PlannedCount,
		CreatedAt:       time.Now().UTC(),
	}
	s.taskGroups[group.ID] = group
	return group, nil
}

func (s *testStore) AppendSubagentTaskGroupItem(groupID string, input managedagents.AppendSubagentTaskGroupItemInput) (managedagents.SubagentTaskGroupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.taskGroups[groupID]; !ok {
		return managedagents.SubagentTaskGroupItem{}, managedagents.ErrNotFound
	}
	item := managedagents.SubagentTaskGroupItem{
		GroupID:              groupID,
		ItemIndex:            input.ItemIndex,
		AgentID:              input.AgentID,
		EnvironmentID:        input.EnvironmentID,
		SessionID:            input.SessionID,
		Title:                input.Title,
		Message:              input.Message,
		Priority:             input.Priority,
		InitialState:         normalizeTestSubagentTaskGroupItemState(input.InitialState),
		ErrorType:            input.ErrorType,
		ErrorMessage:         input.ErrorMessage,
		ExpectedResultSchema: cloneRaw(input.ExpectedResultSchema),
		RetryCount:           0,
		CreatedAt:            time.Now().UTC(),
	}
	s.taskGroupItems[groupID] = append(s.taskGroupItems[groupID], item)
	sort.Slice(s.taskGroupItems[groupID], func(i, j int) bool {
		return s.taskGroupItems[groupID][i].ItemIndex < s.taskGroupItems[groupID][j].ItemIndex
	})
	return item, nil
}

func (s *testStore) UpdateSubagentTaskGroupItem(groupID string, itemIndex int, input managedagents.UpdateSubagentTaskGroupItemInput) (managedagents.SubagentTaskGroupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.taskGroupItems[groupID]
	for index, item := range items {
		if item.ItemIndex != itemIndex {
			continue
		}
		item.SessionID = strings.TrimSpace(input.SessionID)
		item.Title = strings.TrimSpace(input.Title)
		item.Message = strings.TrimSpace(input.Message)
		item.Priority = input.Priority
		item.InitialState = normalizeTestSubagentTaskGroupItemState(input.InitialState)
		item.ErrorType = strings.TrimSpace(input.ErrorType)
		item.ErrorMessage = strings.TrimSpace(input.ErrorMessage)
		item.ExpectedResultSchema = cloneRaw(input.ExpectedResultSchema)
		if input.IncrementRetry {
			item.RetryCount++
		}
		item.CreatedAt = time.Now().UTC()
		items[index] = item
		s.taskGroupItems[groupID] = items
		return item, nil
	}
	return managedagents.SubagentTaskGroupItem{}, managedagents.ErrNotFound
}

func (s *testStore) GetSubagentTaskGroup(id string) (managedagents.SubagentTaskGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.taskGroups[id]
	if !ok {
		return managedagents.SubagentTaskGroup{}, managedagents.ErrNotFound
	}
	return group, nil
}

func (s *testStore) ListSubagentTaskGroupsByParentSession(parentSessionID string) ([]managedagents.SubagentTaskGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := make([]managedagents.SubagentTaskGroup, 0)
	for _, group := range s.taskGroups {
		if group.ParentSessionID == strings.TrimSpace(parentSessionID) {
			groups = append(groups, group)
		}
	}
	slices.SortFunc(groups, func(a, b managedagents.SubagentTaskGroup) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(b.ID, a.ID)
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		return 1
	})
	return groups, nil
}

func (s *testStore) GetSubagentTaskGroupItemBySession(sessionID string) (managedagents.SubagentTaskGroupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, items := range s.taskGroupItems {
		for _, item := range items {
			if item.SessionID == sessionID {
				return item, nil
			}
		}
	}
	return managedagents.SubagentTaskGroupItem{}, managedagents.ErrNotFound
}

func (s *testStore) ListSubagentTaskGroupItems(groupID string) ([]managedagents.SubagentTaskGroupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append([]managedagents.SubagentTaskGroupItem(nil), s.taskGroupItems[groupID]...)
	return items, nil
}

func (s *testStore) ListChildSubagentTaskGroups(parentGroupID string, parentItemIndex int) ([]managedagents.SubagentTaskGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := make([]managedagents.SubagentTaskGroup, 0)
	for _, group := range s.taskGroups {
		if group.ParentGroupID == parentGroupID && group.ParentItemIndex == parentItemIndex {
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].CreatedAt.Equal(groups[j].CreatedAt) {
			return groups[i].ID < groups[j].ID
		}
		return groups[i].CreatedAt.Before(groups[j].CreatedAt)
	})
	return groups, nil
}

func (s *testStore) CancelSubagentTaskGroup(input managedagents.CancelSubagentTaskGroupInput) (managedagents.SubagentTaskGroup, error) {
	s.mu.Lock()
	group, ok := s.taskGroups[input.GroupID]
	if !ok {
		s.mu.Unlock()
		return managedagents.SubagentTaskGroup{}, managedagents.ErrNotFound
	}
	if input.ParentSessionID != "" && group.ParentSessionID != input.ParentSessionID {
		s.mu.Unlock()
		return managedagents.SubagentTaskGroup{}, managedagents.ErrInvalid
	}
	if group.CanceledAt != nil {
		s.mu.Unlock()
		return group, nil
	}
	now := time.Now().UTC()
	reason := defaultString(strings.TrimSpace(input.Reason), "canceled by parent agent")
	group.CanceledAt = &now
	group.CancelReason = reason
	s.taskGroups[group.ID] = group
	sessionIDs := make([]string, 0, len(s.taskGroupItems[group.ID]))
	for _, item := range s.taskGroupItems[group.ID] {
		if item.SessionID != "" {
			sessionIDs = append(sessionIDs, item.SessionID)
		}
	}
	s.mu.Unlock()

	for _, sessionID := range sessionIDs {
		if _, err := s.ArchiveSession(sessionID); err != nil && !errors.Is(err, managedagents.ErrNotFound) {
			return managedagents.SubagentTaskGroup{}, err
		}
	}
	return group, nil
}

func (s *testStore) ReactivateSubagentTaskGroup(input managedagents.ReactivateSubagentTaskGroupInput) (managedagents.SubagentTaskGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.taskGroups[input.GroupID]
	if !ok {
		return managedagents.SubagentTaskGroup{}, managedagents.ErrNotFound
	}
	if input.ParentSessionID != "" && group.ParentSessionID != input.ParentSessionID {
		return managedagents.SubagentTaskGroup{}, managedagents.ErrInvalid
	}
	group.CanceledAt = nil
	group.CancelReason = ""
	s.taskGroups[group.ID] = group
	return group, nil
}

func (s *testStore) GetSubagentTaskGroupMetrics(input managedagents.GetSubagentTaskGroupMetricsInput) (managedagents.SubagentTaskGroupMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspaceID := strings.TrimSpace(input.WorkspaceID)
	metrics := managedagents.SubagentTaskGroupMetrics{WorkspaceID: workspaceID}
	for _, group := range s.taskGroups {
		if workspaceID != "" && group.WorkspaceID != workspaceID {
			continue
		}
		items := s.taskGroupItems[group.ID]
		itemStatuses := make([]string, 0, len(items))
		for _, item := range items {
			switch item.InitialState {
			case managedagents.SubagentTaskGroupItemStateCreated:
				metrics.ItemCreated++
			case managedagents.SubagentTaskGroupItemStateStarted:
				metrics.ItemStarted++
			case managedagents.SubagentTaskGroupItemStateQueued:
				metrics.ItemQueued++
			case managedagents.SubagentTaskGroupItemStateRejected:
				metrics.ItemRejected++
			}
			itemStatuses = append(itemStatuses, taskGroupItemStatusFromTestStoreLocked(s, item))
		}
		switch taskGroupStatusFromTestStore(group, itemStatuses) {
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

func (s *testStore) GetSubagentMetrics(input managedagents.GetSubagentMetricsInput) (managedagents.SubagentMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspaceID := strings.TrimSpace(input.WorkspaceID)
	metrics := managedagents.SubagentMetrics{WorkspaceID: workspaceID}
	now := time.Now().UTC()
	for _, request := range s.startRequests {
		if workspaceID != "" && request.WorkspaceID != workspaceID {
			continue
		}
		if request.Status == "pending" {
			metrics.Queued++
			waitSeconds := subagentStartWaitSeconds(request, now)
			if waitSeconds > metrics.WaitSeconds {
				metrics.WaitSeconds = waitSeconds
			}
		}
	}
	for _, session := range s.sessions {
		if workspaceID != "" && session.WorkspaceID != workspaceID {
			continue
		}
		if session.ParentSessionID != "" && session.Status == managedagents.SessionStatusRunning && session.ArchivedAt == nil {
			metrics.Running++
		}
	}
	for sessionID, events := range s.events {
		session, ok := s.sessions[sessionID]
		if !ok {
			continue
		}
		if workspaceID != "" && session.WorkspaceID != workspaceID {
			continue
		}
		for _, event := range events {
			if event.Type == managedagents.EventRuntimeSubagentStartRejected {
				metrics.Rejected++
			}
		}
	}
	return metrics, nil
}

func (s *testStore) createSessionLocked(input managedagents.CreateSessionInput) (managedagents.Session, error) {
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

	agent, ok := s.agents[agentID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: agent %s", managedagents.ErrNotFound, agentID)
	}
	agentConfigVersion := agent.CurrentConfigVersion
	if input.AgentConfigVersion > 0 {
		found := false
		for _, version := range s.agentConfigVersions[agentID] {
			if version.Version == input.AgentConfigVersion {
				found = true
				break
			}
		}
		if !found {
			return managedagents.Session{}, fmt.Errorf("%w: agent config version %s#%d", managedagents.ErrNotFound, agentID, input.AgentConfigVersion)
		}
		agentConfigVersion = input.AgentConfigVersion
	}
	environment, ok := s.environments[input.EnvironmentID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: environment %s", managedagents.ErrNotFound, input.EnvironmentID)
	}

	workspaceID := defaultString(input.WorkspaceID, agent.WorkspaceID)
	if workspaceID != agent.WorkspaceID || workspaceID != environment.WorkspaceID {
		return managedagents.Session{}, fmt.Errorf("%w: workspace mismatch", managedagents.ErrInvalid)
	}
	if input.SpawnDepth < 0 {
		return managedagents.Session{}, fmt.Errorf("%w: spawn_depth must be non-negative", managedagents.ErrInvalid)
	}
	if input.ParentSessionID != "" {
		parentSession, ok := s.sessions[input.ParentSessionID]
		if !ok {
			return managedagents.Session{}, fmt.Errorf("%w: parent session %s", managedagents.ErrNotFound, input.ParentSessionID)
		}
		if parentSession.WorkspaceID != workspaceID {
			return managedagents.Session{}, fmt.Errorf("%w: parent session workspace mismatch", managedagents.ErrInvalid)
		}
	}

	now := time.Now().UTC()
	id := s.nextID("sesn", &s.nextSessionID)
	session := managedagents.Session{
		ID:                      id,
		WorkspaceID:             workspaceID,
		OwnerID:                 defaultString(input.OwnerID, defaultString(input.CreatedBy, "system")),
		AgentID:                 agent.ID,
		AgentConfigVersion:      agentConfigVersion,
		EnvironmentID:           environment.ID,
		ParentSessionID:         input.ParentSessionID,
		ParentTurnID:            input.ParentTurnID,
		SpawnDepth:              input.SpawnDepth,
		Status:                  managedagents.SessionStatusIdle,
		Title:                   input.Title,
		RuntimeSettings:         json.RawMessage(`{}`),
		RuntimeSettingsRevision: 1,
		Tags:                    []string{},
		CreatedBy:               defaultString(input.CreatedBy, "system"),
		CreatedAt:               now,
	}
	s.sessions[id] = session
	s.appendEventLocked(id, managedagents.EventSessionStatusProvisioning, json.RawMessage(`{"status":"provisioning"}`), now)
	s.appendEventLocked(id, managedagents.EventSessionStatusIdle, json.RawMessage(`{"status":"idle"}`), now)
	return session, nil
}

func enforceTestStoreSubagentLimits(sessions map[string]managedagents.Session, parent managedagents.Session, parentTurnID string, limits managedagents.SubagentLimits) error {
	if limits.MaxDepth > 0 && parent.SpawnDepth >= limits.MaxDepth {
		return testStoreSubagentViolation("subagent_depth_limit", "subagent spawn depth limit reached", map[string]any{
			"scope": "session_tree", "policy": "max_depth", "current_depth": parent.SpawnDepth + 1, "limit": limits.MaxDepth, "session_id": parent.ID,
		})
	}
	childrenForTurn := 0
	childrenForSession := 0
	for _, session := range sessions {
		if session.ParentSessionID == parent.ID {
			childrenForSession++
			if session.ParentTurnID == parentTurnID {
				childrenForTurn++
			}
		}
	}
	checks := []struct {
		current   int
		limit     int
		errorType string
		message   string
		state     map[string]any
		counter   string
	}{
		{childrenForTurn, limits.MaxChildrenPerTurn, "subagent_turn_fanout_limit", "subagent spawn limit reached for parent turn", map[string]any{"scope": "parent_turn", "policy": "max_children_per_turn", "parent_session_id": parent.ID, "parent_turn_id": parentTurnID}, "current_children"},
		{childrenForSession, limits.MaxChildrenPerSession, "subagent_session_children_limit", "subagent session child limit reached", map[string]any{"scope": "parent_session", "policy": "max_children_per_session", "parent_session_id": parent.ID}, "current_children"},
	}
	for _, check := range checks {
		if check.limit > 0 && check.current >= check.limit {
			check.state[check.counter] = check.current
			check.state["limit"] = check.limit
			return testStoreSubagentViolation(check.errorType, check.message, check.state)
		}
	}
	return nil
}

func enforceTestStoreSubagentActiveLimits(sessions map[string]managedagents.Session, target managedagents.Session, limits managedagents.SubagentLimits) error {
	workspaceActive := 0
	userActive := 0
	for _, session := range sessions {
		if session.ID == target.ID || session.ParentSessionID == "" || session.Status != managedagents.SessionStatusRunning || session.ArchivedAt != nil || session.WorkspaceID != target.WorkspaceID {
			continue
		}
		workspaceActive++
		if session.OwnerID == target.OwnerID {
			userActive++
		}
	}
	checks := []struct {
		current   int
		limit     int
		errorType string
		message   string
		state     map[string]any
	}{
		{workspaceActive, limits.WorkspaceActiveLimit, "subagent_workspace_active_limit", "workspace subagent active limit reached", map[string]any{"scope": "workspace", "policy": "workspace_active_limit", "workspace_id": target.WorkspaceID}},
		{userActive, limits.UserActiveLimit, "subagent_user_active_limit", "user subagent active limit reached", map[string]any{"scope": "owner", "policy": "user_active_limit", "workspace_id": target.WorkspaceID, "owner_id": target.OwnerID}},
	}
	for _, check := range checks {
		if check.limit > 0 && check.current >= check.limit {
			check.state["current_active"] = check.current
			check.state["limit"] = check.limit
			check.state["subagent_session_id"] = target.ID
			return testStoreSubagentViolation(check.errorType, check.message, check.state)
		}
	}
	return nil
}

func testStoreSubagentViolation(errorType string, message string, state map[string]any) error {
	state["category"] = "quota"
	state["conflict"] = true
	return managedagents.SubagentQuotaViolation{Type: errorType, Message: message, State: state}
}

func (s *testStore) GetSession(id string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	session.Tags = append([]string(nil), session.Tags...)
	session.SummaryText = s.summaries[id].SummaryText
	if session.SummaryText == "" {
		session.SummaryText = latestTestStoreAgentMessage(s.events[id])
	}
	return session, nil
}

func (s *testStore) GetSessionScoped(id string, scope managedagents.AccessScope) (managedagents.Session, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.Session{}, err
	}
	session, err := s.GetSession(id)
	if err != nil {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if session.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && session.OwnerID != scope.OwnerID) {
		return managedagents.Session{}, managedagents.ErrForbidden
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
		if input.OwnerID != "" && session.OwnerID != input.OwnerID {
			continue
		}
		if input.ParentSessionID != "" && session.ParentSessionID != input.ParentSessionID {
			continue
		}
		if input.ParentTurnID != "" && session.ParentTurnID != input.ParentTurnID {
			continue
		}
		if input.ParentedOnly && session.ParentSessionID == "" {
			continue
		}
		if input.Status != "" && session.Status != input.Status {
			continue
		}
		if !input.IncludeArchived && session.ArchivedAt != nil {
			continue
		}
		session.Tags = append([]string(nil), session.Tags...)
		session.SummaryText = s.summaries[session.ID].SummaryText
		if session.SummaryText == "" {
			session.SummaryText = latestTestStoreAgentMessage(s.events[session.ID])
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i int, j int) bool {
		if (sessions[i].PinnedAt != nil) != (sessions[j].PinnedAt != nil) {
			return sessions[i].PinnedAt != nil
		}
		if sessions[i].PinnedAt != nil && sessions[j].PinnedAt != nil && !sessions[i].PinnedAt.Equal(*sessions[j].PinnedAt) {
			return sessions[i].PinnedAt.After(*sessions[j].PinnedAt)
		}
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

func (s *testStore) ListSessionsScoped(input managedagents.ListSessionsInput, scope managedagents.AccessScope) ([]managedagents.Session, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = scope.WorkspaceID
	input.OwnerID = scope.OwnerID
	return s.ListSessions(input)
}

func latestTestStoreAgentMessage(events []managedagents.Event) string {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == managedagents.EventAgentMessage {
			return messagePayloadText(events[index].Payload)
		}
	}
	return ""
}

func (s *testStore) ReapOrphanSubagents(managedagents.ReapOrphanSubagentsInput) ([]managedagents.ReapedSubagent, error) {
	return []managedagents.ReapedSubagent{}, nil
}

func (s *testStore) RecordOperatorAudit(input managedagents.RecordOperatorAuditInput) (managedagents.OperatorAuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextOperatorAuditID++
	details := cloneRaw(input.Details)
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	record := managedagents.OperatorAuditRecord{
		ID:            fmt.Sprintf("oaud_%06d", s.nextOperatorAuditID),
		WorkspaceID:   input.WorkspaceID,
		SessionID:     input.SessionID,
		PrincipalID:   input.PrincipalID,
		OperatorLabel: input.OperatorLabel,
		Role:          input.Role,
		Action:        input.Action,
		ResourceType:  input.ResourceType,
		ResourceID:    input.ResourceID,
		Outcome:       input.Outcome,
		ErrorMessage:  input.ErrorMessage,
		Details:       details,
		CreatedAt:     time.Now().UTC(),
	}
	s.operatorAudits = append(s.operatorAudits, record)
	return record, nil
}

func (s *testStore) ListOperatorAudit(input managedagents.ListOperatorAuditInput) ([]managedagents.OperatorAuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	records := make([]managedagents.OperatorAuditRecord, 0, len(s.operatorAudits))
	for index := len(s.operatorAudits) - 1; index >= 0; index-- {
		record := s.operatorAudits[index]
		if input.WorkspaceID != "" && record.WorkspaceID != input.WorkspaceID {
			continue
		}
		if input.SessionID != "" && record.SessionID != input.SessionID {
			continue
		}
		if input.PrincipalID != "" && record.PrincipalID != input.PrincipalID {
			continue
		}
		if input.Action != "" && record.Action != input.Action {
			continue
		}
		records = append(records, record)
		if len(records) == limit {
			break
		}
	}
	return records, nil
}

func (s *testStore) CreateAgentDeliberation(input managedagents.CreateAgentDeliberationInput) (managedagents.AgentDeliberation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.deliberations {
		if input.Deliberation.IdempotencyKey != "" && existing.ParentSessionID == input.Deliberation.ParentSessionID && existing.IdempotencyKey == input.Deliberation.IdempotencyKey {
			return managedagents.AgentDeliberation{}, managedagents.ErrConflict
		}
	}
	s.nextDeliberationID++
	now := time.Now().UTC()
	item := input.Deliberation
	item.ID = fmt.Sprintf("dlib_%06d", s.nextDeliberationID)
	item.Status = managedagents.AgentDeliberationStatusRunning
	item.Phase = managedagents.AgentDeliberationPhaseRound1Running
	item.MaxParticipants = len(input.Participants)
	item.MaxRounds = 2
	item.Plan = cloneRaw(item.Plan)
	item.CreatedAt = now
	item.UpdatedAt = now
	s.deliberations[item.ID] = item
	participants := append([]managedagents.AgentDeliberationParticipant(nil), input.Participants...)
	for index := range participants {
		participants[index].DeliberationID = item.ID
		participants[index].ParticipantIndex = index
		participants[index].CreatedAt = now
	}
	s.deliberationParticipants[item.ID] = participants
	return item, nil
}

func (s *testStore) GetAgentDeliberation(id string) (managedagents.AgentDeliberation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.deliberations[id]
	if !ok {
		return managedagents.AgentDeliberation{}, managedagents.ErrNotFound
	}
	return item, nil
}

func (s *testStore) GetAgentDeliberationByIdempotency(parentSessionID string, idempotencyKey string) (managedagents.AgentDeliberation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.deliberations {
		if item.ParentSessionID == parentSessionID && item.IdempotencyKey == idempotencyKey && idempotencyKey != "" {
			return item, nil
		}
	}
	return managedagents.AgentDeliberation{}, managedagents.ErrNotFound
}

func (s *testStore) ListAgentDeliberationsByParentSession(parentSessionID string) ([]managedagents.AgentDeliberation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []managedagents.AgentDeliberation{}
	for _, item := range s.deliberations {
		if item.ParentSessionID == parentSessionID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *testStore) UpdateAgentDeliberation(id string, input managedagents.UpdateAgentDeliberationInput) (managedagents.AgentDeliberation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.deliberations[id]
	if !ok {
		return managedagents.AgentDeliberation{}, managedagents.ErrNotFound
	}
	item.Status = input.Status
	item.Phase = input.Phase
	item.FinalGroupID = input.FinalGroupID
	item.FinalResult = cloneRaw(input.FinalResult)
	item.CancelReason = input.CancelReason
	item.UpdatedAt = time.Now().UTC()
	if input.Status == managedagents.AgentDeliberationStatusCanceled && item.CanceledAt == nil {
		now := item.UpdatedAt
		item.CanceledAt = &now
	}
	s.deliberations[id] = item
	return item, nil
}

func (s *testStore) ListAgentDeliberationParticipants(deliberationID string) ([]managedagents.AgentDeliberationParticipant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]managedagents.AgentDeliberationParticipant(nil), s.deliberationParticipants[deliberationID]...), nil
}

func (s *testStore) CreateAgentDeliberationRound(round managedagents.AgentDeliberationRound) (managedagents.AgentDeliberationRound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deliberationRounds[round.DeliberationID] == nil {
		s.deliberationRounds[round.DeliberationID] = map[int]managedagents.AgentDeliberationRound{}
	}
	if _, exists := s.deliberationRounds[round.DeliberationID][round.RoundNumber]; exists {
		return managedagents.AgentDeliberationRound{}, managedagents.ErrConflict
	}
	round.CreatedAt = time.Now().UTC()
	s.deliberationRounds[round.DeliberationID][round.RoundNumber] = round
	return round, nil
}

func (s *testStore) GetAgentDeliberationRound(deliberationID string, roundNumber int) (managedagents.AgentDeliberationRound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	round, ok := s.deliberationRounds[deliberationID][roundNumber]
	if !ok {
		return managedagents.AgentDeliberationRound{}, managedagents.ErrNotFound
	}
	return round, nil
}

func (s *testStore) ListAgentDeliberationRounds(deliberationID string) ([]managedagents.AgentDeliberationRound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []managedagents.AgentDeliberationRound{}
	for _, round := range s.deliberationRounds[deliberationID] {
		items = append(items, round)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RoundNumber < items[j].RoundNumber })
	return items, nil
}

func (s *testStore) UpdateAgentDeliberationRound(deliberationID string, roundNumber int, input managedagents.UpdateAgentDeliberationRoundInput) (managedagents.AgentDeliberationRound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	round, ok := s.deliberationRounds[deliberationID][roundNumber]
	if !ok {
		return managedagents.AgentDeliberationRound{}, managedagents.ErrNotFound
	}
	round.Status = input.Status
	round.ModeratorGroupID = input.ModeratorGroupID
	round.Summary = cloneRaw(input.Summary)
	round.Questions = cloneRaw(input.Questions)
	if input.Complete && round.CompletedAt == nil {
		now := time.Now().UTC()
		round.CompletedAt = &now
	}
	s.deliberationRounds[deliberationID][roundNumber] = round
	return round, nil
}

func (s *testStore) UpsertAgentDeliberationContribution(contribution managedagents.AgentDeliberationContribution) (managedagents.AgentDeliberationContribution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deliberationContributions[contribution.DeliberationID] == nil {
		s.deliberationContributions[contribution.DeliberationID] = map[int]map[int]managedagents.AgentDeliberationContribution{}
	}
	if s.deliberationContributions[contribution.DeliberationID][contribution.RoundNumber] == nil {
		s.deliberationContributions[contribution.DeliberationID][contribution.RoundNumber] = map[int]managedagents.AgentDeliberationContribution{}
	}
	now := time.Now().UTC()
	if existing, ok := s.deliberationContributions[contribution.DeliberationID][contribution.RoundNumber][contribution.ParticipantIndex]; ok {
		contribution.CreatedAt = existing.CreatedAt
	} else {
		contribution.CreatedAt = now
	}
	contribution.ContributionJSON = cloneRaw(contribution.ContributionJSON)
	contribution.UpdatedAt = now
	s.deliberationContributions[contribution.DeliberationID][contribution.RoundNumber][contribution.ParticipantIndex] = contribution
	return contribution, nil
}

func (s *testStore) ListAgentDeliberationContributions(deliberationID string, roundNumber int) ([]managedagents.AgentDeliberationContribution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []managedagents.AgentDeliberationContribution{}
	for candidateRound, entries := range s.deliberationContributions[deliberationID] {
		if roundNumber != 0 && candidateRound != roundNumber {
			continue
		}
		for _, contribution := range entries {
			items = append(items, contribution)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RoundNumber == items[j].RoundNumber {
			return items[i].ParticipantIndex < items[j].ParticipantIndex
		}
		return items[i].RoundNumber < items[j].RoundNumber
	})
	return items, nil
}

func (s *testStore) UpdateSessionRuntimeSettings(id string, input managedagents.UpdateSessionRuntimeSettingsInput) (managedagents.Session, error) {
	if _, err := managedagents.AgentConfigUpdatePolicy(input.RuntimeSettings); err != nil {
		return managedagents.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if input.ExpectedRevision <= 0 {
		return managedagents.Session{}, managedagents.ErrInvalid
	}
	if input.ExpectedRevision != session.RuntimeSettingsRevision {
		return managedagents.Session{}, managedagents.ErrRevisionConflict
	}
	session.RuntimeSettings = cloneRaw(input.RuntimeSettings)
	session.RuntimeSettingsRevision++
	s.sessions[id] = session
	return session, nil
}

func (s *testStore) UpdateSessionMetadata(id string, input managedagents.UpdateSessionMetadataInput) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if input.Pinned == nil && input.Tags == nil {
		return managedagents.Session{}, managedagents.ErrInvalid
	}
	if input.Pinned != nil {
		if *input.Pinned {
			now := time.Now().UTC()
			if session.PinnedAt == nil {
				session.PinnedAt = &now
			}
		} else {
			session.PinnedAt = nil
		}
	}
	if input.Tags != nil {
		if len(*input.Tags) > 8 {
			return managedagents.Session{}, managedagents.ErrInvalid
		}
		seen := map[string]struct{}{}
		tags := make([]string, 0, len(*input.Tags))
		for _, value := range *input.Tags {
			tag := strings.TrimSpace(value)
			if tag == "" {
				continue
			}
			if len([]rune(tag)) > 32 {
				return managedagents.Session{}, managedagents.ErrInvalid
			}
			key := strings.ToLower(tag)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			tags = append(tags, tag)
		}
		session.Tags = tags
	}
	s.sessions[id] = session
	session.SummaryText = s.summaries[id].SummaryText
	return session, nil
}

func (s *testStore) UpgradeSessionAgentConfig(id string, input managedagents.UpgradeSessionAgentConfigInput) (managedagents.UpgradeSessionAgentConfigResult, error) {
	if id == "" {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrInvalid
	}
	if input.TargetVersion < 0 || input.ToCurrent == (input.TargetVersion > 0) {
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
	targetVersion := agent.CurrentConfigVersion
	if input.TargetVersion > 0 {
		if _, ok := s.agentConfigVersionLocked(session.AgentID, input.TargetVersion); !ok {
			return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrNotFound
		}
		targetVersion = input.TargetVersion
	}
	if targetVersion == session.AgentConfigVersion {
		return result, nil
	}
	if targetVersion < session.AgentConfigVersion {
		return managedagents.UpgradeSessionAgentConfigResult{}, managedagents.ErrConflict
	}

	oldVersion := session.AgentConfigVersion
	session.AgentConfigVersion = targetVersion
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

	kind := input.Kind
	if kind == "" {
		kind = managedagents.InterventionKindToolApproval
	}
	intervention := managedagents.SessionIntervention{
		SessionID:         sessionID,
		TurnID:            input.TurnID,
		CallID:            input.CallID,
		ToolIdentifier:    input.ToolIdentifier,
		APIName:           input.APIName,
		Arguments:         cloneRaw(input.Arguments),
		Kind:              kind,
		Request:           cloneRaw(input.Request),
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
		if intervention.Status == input.Status {
			return managedagents.DecideSessionInterventionResult{Intervention: intervention}, nil
		}
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}
	approvalResult := input.Status == managedagents.InterventionStatusApproved || input.Status == managedagents.InterventionStatusRejected
	humanResult := input.Status == managedagents.InterventionStatusAnswered || input.Status == managedagents.InterventionStatusSkipped || input.Status == managedagents.InterventionStatusCanceled || input.Status == managedagents.InterventionStatusExpired
	if (intervention.Kind == managedagents.InterventionKindToolApproval || intervention.Kind == managedagents.InterventionKindPlanApproval) && !approvalResult {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}
	if (intervention.Kind == managedagents.InterventionKindClarification || intervention.Kind == managedagents.InterventionKindUploadRequest) && !humanResult {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}
	if input.Status == managedagents.InterventionStatusAnswered && len(input.Response) == 0 {
		return managedagents.DecideSessionInterventionResult{}, managedagents.ErrInvalid
	}

	now := time.Now().UTC()
	intervention.Status = input.Status
	intervention.DecisionReason = input.DecisionReason
	intervention.Response = cloneRaw(input.Response)
	intervention.DecidedAt = &now
	intervention.RespondedAt = &now
	s.interventions[key] = intervention

	eventType := managedagents.EventRuntimeToolInterventionApproved
	message := "Tool call approved by user."
	if input.Status == managedagents.InterventionStatusRejected {
		eventType = managedagents.EventRuntimeToolInterventionRejected
		message = "Tool call rejected by user."
	}
	if intervention.Kind == managedagents.InterventionKindClarification || intervention.Kind == managedagents.InterventionKindUploadRequest {
		switch input.Status {
		case managedagents.InterventionStatusAnswered:
			eventType = managedagents.EventRuntimeHumanInputSubmitted
			message = "User submitted requested information."
		case managedagents.InterventionStatusSkipped:
			eventType = managedagents.EventRuntimeHumanInputSkipped
			message = "User skipped the requested information."
		default:
			eventType = managedagents.EventRuntimeHumanInputCanceled
			message = "User input request was canceled."
		}
	} else if intervention.Kind == managedagents.InterventionKindPlanApproval {
		if input.Status == managedagents.InterventionStatusRejected {
			eventType = managedagents.EventRuntimePlanApprovalRejected
			message = "Task plan rejected by user."
		} else {
			eventType = managedagents.EventRuntimePlanApprovalApproved
			message = "Task plan approved by user."
		}
	}
	payload, err := json.Marshal(map[string]any{
		"turn_id": input.TurnID,
		"message": message,
		"data": map[string]any{
			"id":                intervention.CallID,
			"identifier":        intervention.ToolIdentifier,
			"api_name":          intervention.APIName,
			"arguments":         rawJSONObject(intervention.Arguments),
			"kind":              intervention.Kind,
			"request":           rawJSONObject(intervention.Request),
			"response":          rawJSONObject(intervention.Response),
			"status":            intervention.Status,
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

func (s *testStore) MarkSessionTurnWaitingHuman(sessionID string, turnID string) error {
	return s.MarkSessionTurnWaitingApproval(sessionID, turnID)
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
	providerID := configVersion.LLMProvider
	modelName := configVersion.LLMModel
	var overrides struct {
		LLMProvider *string `json:"llm_provider"`
		LLMModel    *string `json:"llm_model"`
	}
	if len(session.RuntimeSettings) > 0 && json.Unmarshal(session.RuntimeSettings, &overrides) == nil {
		if overrides.LLMProvider != nil {
			providerID = strings.TrimSpace(*overrides.LLMProvider)
		}
		if overrides.LLMModel != nil {
			modelName = strings.TrimSpace(*overrides.LLMModel)
		}
	}
	provider, ok := s.providers[providerID]
	if !ok || !provider.Enabled {
		return managedagents.AgentRuntimeConfig{}, managedagents.ErrInvalid
	}
	contextWindowTokens := managedagents.DefaultContextWindowTokens
	capabilityType := managedagents.LLMModelCapabilityText
	if model, ok := s.models[llmModelKey(providerID, modelName)]; ok {
		contextWindowTokens = model.ContextWindowTokens
		capabilityType = model.CapabilityType
	} else {
		return managedagents.AgentRuntimeConfig{}, managedagents.ErrInvalid
	}
	summary := s.summaries[sessionID]
	workspaceToolPolicy := s.workspaceToolPolicies[session.WorkspaceID]
	var visionModel managedagents.LLMModel
	var visionProvider managedagents.LLMProvider
	for _, candidate := range s.models {
		if !candidate.IsDefaultVision {
			continue
		}
		candidateProvider, ok := s.providers[candidate.ProviderID]
		if ok && candidateProvider.Enabled {
			visionModel = candidate
			visionProvider = candidateProvider
		}
		break
	}

	return managedagents.AgentRuntimeConfig{
		SessionID:             sessionID,
		ParentSessionID:       session.ParentSessionID,
		SpawnDepth:            session.SpawnDepth,
		WorkspaceID:           session.WorkspaceID,
		AgentID:               agent.ID,
		AgentConfigVersion:    session.AgentConfigVersion,
		EnvironmentID:         session.EnvironmentID,
		LLMProvider:           providerID,
		LLMProviderType:       defaultString(provider.ProviderType, "fake"),
		LLMModel:              modelName,
		LLMBaseURL:            provider.BaseURL,
		LLMAPIKeyEnv:          provider.APIKeyEnv,
		ContextWindowTokens:   contextWindowTokens,
		LLMCapabilityType:     capabilityType,
		VisionLLMProvider:     visionProvider.ID,
		VisionLLMProviderType: visionProvider.ProviderType,
		VisionLLMModel:        visionModel.Model,
		VisionLLMBaseURL:      visionProvider.BaseURL,
		VisionLLMAPIKeyEnv:    visionProvider.APIKeyEnv,
		SummaryText:           summary.SummaryText,
		SummarySourceUntilSeq: summary.SourceUntilSeq,
		System:                configVersion.System,
		RuntimeSettings:       cloneRaw(session.RuntimeSettings),
		Tools:                 cloneRaw(configVersion.Tools),
		WorkspaceToolPolicy:   cloneRaw(workspaceToolPolicy.Policy),
		MCP:                   cloneRaw(configVersion.MCP),
		Skills:                cloneRaw(configVersion.Skills),
	}, nil
}

func (s *testStore) GetWorkspaceToolPermissionPolicyContext(_ context.Context, workspaceID string) (managedagents.WorkspaceToolPermissionPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if policy, ok := s.workspaceToolPolicies[workspaceID]; ok {
		policy.Policy = cloneRaw(policy.Policy)
		return policy, nil
	}
	return managedagents.WorkspaceToolPermissionPolicy{
		WorkspaceID: workspaceID,
		Policy:      json.RawMessage(`{"permission_rules":[]}`),
		Revision:    1,
		UpdatedBy:   "system",
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func (s *testStore) UpdateWorkspaceToolPermissionPolicyContext(_ context.Context, input managedagents.UpdateWorkspaceToolPermissionPolicyInput) (managedagents.WorkspaceToolPermissionPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	currentRevision := int64(1)
	if current, ok := s.workspaceToolPolicies[input.WorkspaceID]; ok {
		currentRevision = current.Revision
	}
	if input.ExpectedRevision != currentRevision {
		return managedagents.WorkspaceToolPermissionPolicy{}, managedagents.ErrRevisionConflict
	}
	policy := managedagents.WorkspaceToolPermissionPolicy{
		WorkspaceID: input.WorkspaceID,
		Policy:      cloneRaw(input.Policy),
		Revision:    currentRevision + 1,
		UpdatedBy:   input.UpdatedBy,
		UpdatedAt:   time.Now().UTC(),
	}
	s.workspaceToolPolicies[input.WorkspaceID] = policy
	return policy, nil
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

func (s *testStore) GetCurrentSessionTaskPlanContext(_ context.Context, sessionID string) (managedagents.SessionTaskPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.SessionTaskPlan{}, managedagents.ErrNotFound
	}
	for _, plan := range s.taskPlans[sessionID] {
		if plan.Status == managedagents.TaskPlanStatusActive {
			return plan, nil
		}
	}
	return managedagents.SessionTaskPlan{}, managedagents.ErrNotFound
}

func (s *testStore) ListSessionTaskPlansContext(_ context.Context, sessionID string) ([]managedagents.SessionTaskPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	return append([]managedagents.SessionTaskPlan(nil), s.taskPlans[sessionID]...), nil
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

func normalizeTestStoreMCP(raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := mcppkg.CanonicalJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	return normalized, nil
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

func (s *testStore) UpsertTraceIndex(input managedagents.UpsertTraceIndexInput) error {
	if input.Trace.TraceID == "" || input.Trace.SessionID == "" || input.Trace.TurnID == "" {
		return managedagents.ErrInvalid
	}
	trace := input.Trace
	if trace.WorkspaceID == "" {
		trace.WorkspaceID = managedagents.DefaultWorkspaceID
	}
	now := time.Now().UTC()
	if trace.StartedAt.IsZero() {
		trace.StartedAt = now
	}
	if trace.EndedAt.IsZero() {
		trace.EndedAt = trace.StartedAt
	}
	trace.UpdatedAt = now
	spans := make([]managedagents.TraceSpanIndexEntry, 0, len(input.Spans))
	for _, span := range input.Spans {
		if span.SpanID == "" {
			continue
		}
		if span.TraceID == "" {
			span.TraceID = trace.TraceID
		}
		if span.WorkspaceID == "" {
			span.WorkspaceID = trace.WorkspaceID
		}
		if span.SessionID == "" {
			span.SessionID = trace.SessionID
		}
		if span.TurnID == "" {
			span.TurnID = trace.TurnID
		}
		if span.SessionTitle == "" {
			span.SessionTitle = trace.SessionTitle
		}
		if span.StartTime.IsZero() {
			span.StartTime = trace.StartedAt
		}
		span.UpdatedAt = now
		spans = append(spans, span)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traceIndexes[trace.TraceID] = trace
	s.traceSpanIndexes[trace.TraceID] = spans
	return nil
}

func (s *testStore) GetTraceIndex(traceID string) (managedagents.TraceIndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trace, ok := s.traceIndexes[traceID]
	if !ok {
		return managedagents.TraceIndexEntry{}, managedagents.ErrNotFound
	}
	return trace, nil
}

func (s *testStore) ListTraceIndexes(input managedagents.ListTraceIndexInput) ([]managedagents.TraceIndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	entries := []managedagents.TraceIndexEntry{}
	for _, entry := range s.traceIndexes {
		if input.WorkspaceID != "" && entry.WorkspaceID != input.WorkspaceID {
			continue
		}
		if input.SessionID != "" && entry.SessionID != input.SessionID {
			continue
		}
		if input.TurnID != "" && entry.TurnID != input.TurnID {
			continue
		}
		if input.TraceID != "" && entry.TraceID != input.TraceID {
			continue
		}
		if input.SessionStatus != "" && entry.SessionStatus != input.SessionStatus {
			continue
		}
		if !input.IncludeArchived {
			if session, ok := s.sessions[entry.SessionID]; ok && session.ArchivedAt != nil {
				continue
			}
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].StartedAt.Equal(entries[j].StartedAt) {
			return entries[i].TraceID > entries[j].TraceID
		}
		return entries[i].StartedAt.After(entries[j].StartedAt)
	})
	if offset >= len(entries) {
		return []managedagents.TraceIndexEntry{}, nil
	}
	entries = entries[offset:]
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *testStore) ListTraceSpanIndexes(input managedagents.ListTraceSpanIndexInput) ([]managedagents.TraceSpanIndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	query := strings.TrimSpace(strings.ToLower(input.Query))
	entries := []managedagents.TraceSpanIndexEntry{}
	for _, spans := range s.traceSpanIndexes {
		for _, entry := range spans {
			if input.WorkspaceID != "" && entry.WorkspaceID != input.WorkspaceID {
				continue
			}
			if input.TraceID != "" && entry.TraceID != input.TraceID {
				continue
			}
			if input.SessionID != "" && entry.SessionID != input.SessionID {
				continue
			}
			if input.TurnID != "" && entry.TurnID != input.TurnID {
				continue
			}
			if input.Kind != "" && !strings.EqualFold(entry.Kind, input.Kind) {
				continue
			}
			if input.Status != "" && !strings.EqualFold(entry.Status, input.Status) {
				continue
			}
			if input.Critical != nil && entry.Critical != *input.Critical {
				continue
			}
			if input.MinDurationMillis > 0 && entry.DurationMillis < input.MinDurationMillis {
				continue
			}
			if input.MaxDurationMillis > 0 && entry.DurationMillis > input.MaxDurationMillis {
				continue
			}
			if input.MinSelfDurationMillis > 0 && entry.SelfDurationMillis < input.MinSelfDurationMillis {
				continue
			}
			if !input.IncludeArchived {
				if session, ok := s.sessions[entry.SessionID]; ok && session.ArchivedAt != nil {
					continue
				}
			}
			if query != "" {
				values := []string{entry.TraceID, entry.SessionID, entry.TurnID, entry.SessionTitle, entry.SpanID, entry.ParentSpanID, entry.Name, entry.Kind, entry.Status}
				for key, value := range entry.Attributes {
					values = append(values, key, value)
				}
				if !strings.Contains(strings.ToLower(strings.Join(values, " ")), query) {
					continue
				}
			}
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].StartTime.Equal(entries[j].StartTime) {
			return entries[i].SpanID < entries[j].SpanID
		}
		return entries[i].StartTime.After(entries[j].StartTime)
	})
	if offset >= len(entries) {
		return []managedagents.TraceSpanIndexEntry{}, nil
	}
	entries = entries[offset:]
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *testStore) PruneTraceIndexes(input managedagents.PruneTraceIndexInput) (int, error) {
	if input.Before.IsZero() {
		return 0, managedagents.ErrInvalid
	}
	limit := input.Limit
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	traceIDs := make([]string, 0, len(s.traceIndexes))
	for traceID, entry := range s.traceIndexes {
		if entry.EndedAt.Before(input.Before) {
			traceIDs = append(traceIDs, traceID)
		}
	}
	sort.Slice(traceIDs, func(i, j int) bool {
		return s.traceIndexes[traceIDs[i]].EndedAt.Before(s.traceIndexes[traceIDs[j]].EndedAt)
	})
	if len(traceIDs) > limit {
		traceIDs = traceIDs[:limit]
	}
	for _, traceID := range traceIDs {
		delete(s.traceIndexes, traceID)
		delete(s.traceSpanIndexes, traceID)
	}
	return len(traceIDs), nil
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

func (s *testStore) GetObjectRefScoped(id string, scope managedagents.AccessScope) (managedagents.ObjectRef, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.ObjectRef{}, err
	}
	object, err := s.GetObjectRef(id)
	if err != nil {
		return managedagents.ObjectRef{}, managedagents.ErrNotFound
	}
	if object.WorkspaceID != scope.WorkspaceID {
		return managedagents.ObjectRef{}, managedagents.ErrForbidden
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

func (s *testStore) GetWorkerScoped(id string, scope managedagents.AccessScope) (managedagents.Worker, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.Worker{}, err
	}
	worker, err := s.GetWorker(id)
	if err != nil {
		return managedagents.Worker{}, managedagents.ErrNotFound
	}
	if worker.WorkspaceID != scope.WorkspaceID {
		return managedagents.Worker{}, managedagents.ErrForbidden
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

func (s *testStore) ListWorkersScoped(input managedagents.ListWorkersInput, scope managedagents.AccessScope) ([]managedagents.Worker, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = scope.WorkspaceID
	return s.ListWorkers(input)
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

func (s *testStore) GetWorkerWorkScoped(id string, scope managedagents.AccessScope) (managedagents.WorkerWork, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.WorkerWork{}, err
	}
	work, err := s.GetWorkerWork(id)
	if err != nil {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	if work.WorkspaceID != scope.WorkspaceID {
		return managedagents.WorkerWork{}, managedagents.ErrForbidden
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

func (s *testStore) RequeueWorkerWork(workID string, input managedagents.RequeueWorkerWorkInput) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	original, ok := s.workerWork[workID]
	if !ok {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	if original.Status != managedagents.WorkerWorkStatusFailed && original.Status != managedagents.WorkerWorkStatusCanceled {
		return managedagents.WorkerWork{}, fmt.Errorf("%w: only failed or canceled worker work can be requeued", managedagents.ErrConflict)
	}
	workerID := strings.TrimSpace(input.WorkerID)
	if input.ClearWorker && workerID != "" {
		return managedagents.WorkerWork{}, fmt.Errorf("%w: requeue accepts either worker_id or clear_worker, not both", managedagents.ErrInvalid)
	}
	if !input.ClearWorker && workerID == "" {
		workerID = original.WorkerID
	}

	now := time.Now().UTC()
	id := s.nextID("work", &s.nextWorkID)
	work := managedagents.WorkerWork{
		ID:            id,
		WorkspaceID:   original.WorkspaceID,
		WorkerID:      workerID,
		EnvironmentID: original.EnvironmentID,
		SessionID:     original.SessionID,
		TurnID:        original.TurnID,
		WorkType:      original.WorkType,
		Status:        managedagents.WorkerWorkStatusPending,
		Payload:       metadataJSON(original.Payload),
		Result:        json.RawMessage(`{}`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.workerWork[id] = work
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
	if session.ArchivedAt != nil {
		return session, nil
	}

	now := time.Now().UTC()
	affected := make([]managedagents.SubagentStartRequest, 0)
	for requestID, request := range s.startRequests {
		if request.Status != "pending" || (request.SessionID != id && request.ParentSessionID != id) {
			continue
		}
		request.Status, request.CanceledAt, request.CancelReason = "canceled", &now, "session archived"
		request.WaitSeconds = subagentStartWaitSeconds(request, now)
		s.startRequests[requestID] = request
		affected = append(affected, request)
	}
	session.Status = managedagents.SessionStatusTerminated
	session.ArchivedAt = &now
	s.sessions[id] = session

	for _, request := range affected {
		payload, _ := json.Marshal(map[string]any{
			"request_id":        request.ID,
			"session_id":        request.SessionID,
			"parent_session_id": request.ParentSessionID,
			"reason":            "session archived",
			"canceled_at":       now,
			"wait_seconds":      request.WaitSeconds,
		})
		s.publishLocked(s.appendEventLocked(request.SessionID, managedagents.EventRuntimeSubagentStartCanceled, payload, now))
	}
	if len(affected) > 0 {
		needsAggregate := false
		for _, request := range affected {
			if request.SessionID != id {
				needsAggregate = true
				break
			}
		}
		if needsAggregate {
			payload, _ := json.Marshal(map[string]any{"reason": "session archived", "canceled_requests": len(affected)})
			s.publishLocked(s.appendEventLocked(id, managedagents.EventRuntimeSubagentStartCanceled, payload, now))
		}
	}
	event := s.appendEventLocked(id, managedagents.EventSessionStatusTerminated, json.RawMessage(`{"status":"terminated"}`), now)
	s.publishLocked(event)
	return session, nil
}

func (s *testStore) RestoreSession(id string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if session.ArchivedAt == nil {
		return session, nil
	}

	now := time.Now().UTC()
	session.Status = managedagents.SessionStatusIdle
	session.ArchivedAt = nil
	s.sessions[id] = session
	event := s.appendEventLocked(id, managedagents.EventSessionStatusIdle, json.RawMessage(`{"status":"idle","reason":"session restored"}`), now)
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

func TestArchiveSessionCancelsQueuedChildStartsOnChildSessions(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	childSession := mustCreateSessionForSubagentTest(t, store, childAgent.ID, environment.ID, "child-session")
	childSession.ParentSessionID = parentSession.ID
	childSession.ParentTurnID = "turn_parent"
	store.sessions[childSession.ID] = childSession

	queuedAt := time.Now().UTC().Add(-5 * time.Second)
	request := managedagents.SubagentStartRequest{
		ID:              "sreq_000001",
		WorkspaceID:     childSession.WorkspaceID,
		OwnerID:         childSession.OwnerID,
		SessionID:       childSession.ID,
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_parent",
		Status:          "pending",
		QueuedAt:        queuedAt,
		ExpiresAt:       queuedAt.Add(time.Hour),
	}
	store.startRequests[request.ID] = request

	archived, err := store.ArchiveSession(parentSession.ID)
	if err != nil {
		t.Fatalf("archive parent session: %v", err)
	}
	if archived.Status != managedagents.SessionStatusTerminated || archived.ArchivedAt == nil {
		t.Fatalf("expected terminated parent session, got %+v", archived)
	}

	events, err := store.ListEvents(childSession.ID, 0)
	if err != nil {
		t.Fatalf("list child events: %v", err)
	}
	foundCanceled := false
	for _, event := range events {
		if event.Type != managedagents.EventRuntimeSubagentStartCanceled {
			continue
		}
		foundCanceled = true
		if payloadString(event.Payload, "request_id") != request.ID {
			t.Fatalf("expected canceled request id %q, got payload %s", request.ID, string(event.Payload))
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode canceled payload: %v", err)
		}
		waitSeconds, ok := payload["wait_seconds"].(float64)
		if !ok || waitSeconds < 4 {
			t.Fatalf("expected wait_seconds on canceled payload, got %v", payload["wait_seconds"])
		}
	}
	if !foundCanceled {
		t.Fatalf("expected child canceled event, got %#v", events)
	}
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

func (s *testStore) StartSessionRunContext(_ context.Context, sessionID string, input managedagents.StartSessionRunInput) (managedagents.StartSessionRunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return managedagents.StartSessionRunResult{}, managedagents.ErrNotFound
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key != "" {
		if existing, found := s.runIdempotency[sessionID][key]; found {
			if existing.RequestHash != input.RequestHash {
				return managedagents.StartSessionRunResult{}, fmt.Errorf("%w: idempotency_conflict", managedagents.ErrConflict)
			}
			run, found := s.sessionRunLocked(sessionID, existing.RunID)
			if !found {
				return managedagents.StartSessionRunResult{}, managedagents.ErrNotFound
			}
			run.IdempotencyKey = key
			run.RequestHash = existing.RequestHash
			return managedagents.StartSessionRunResult{Run: run, Created: false}, nil
		}
	}
	if session.Status != managedagents.SessionStatusIdle {
		return managedagents.StartSessionRunResult{}, fmt.Errorf("%w: user.message requires idle session", managedagents.ErrSessionBusy)
	}
	now := time.Now().UTC()
	events, err := s.applyEventLocked(&session, managedagents.AppendEventInput{Type: managedagents.EventUserMessage, Payload: cloneRaw(input.Payload)}, now)
	if err != nil {
		return managedagents.StartSessionRunResult{}, err
	}
	s.sessions[sessionID] = session
	runID := payloadString(events[len(events)-1].Payload, "turn_id")
	if key != "" {
		if s.runIdempotency[sessionID] == nil {
			s.runIdempotency[sessionID] = make(map[string]testRunIdempotency)
		}
		s.runIdempotency[sessionID][key] = testRunIdempotency{RunID: runID, RequestHash: input.RequestHash}
	}
	run, _ := s.sessionRunLocked(sessionID, runID)
	run.IdempotencyKey = key
	run.RequestHash = input.RequestHash
	return managedagents.StartSessionRunResult{Run: run, Events: events, Created: true}, nil
}

func (s *testStore) GetSessionRunContext(_ context.Context, sessionID string, runID string) (managedagents.SessionRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return managedagents.SessionRun{}, managedagents.ErrNotFound
	}
	run, ok := s.sessionRunLocked(sessionID, runID)
	if !ok {
		return managedagents.SessionRun{}, managedagents.ErrNotFound
	}
	return run, nil
}

func (s *testStore) ListSessionRunsContext(_ context.Context, sessionID string) ([]managedagents.SessionRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	seen := map[string]bool{}
	runs := []managedagents.SessionRun{}
	for _, event := range s.events[sessionID] {
		turnID := payloadString(event.Payload, "turn_id")
		if turnID == "" || seen[turnID] {
			continue
		}
		if run, ok := s.sessionRunLocked(sessionID, turnID); ok {
			seen[turnID] = true
			runs = append(runs, run)
		}
	}
	return runs, nil
}

func (s *testStore) ListSessionRunEventsContext(_ context.Context, sessionID string, runID string, afterSeq int64) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	if _, ok := s.sessionRunLocked(sessionID, runID); !ok {
		return nil, managedagents.ErrNotFound
	}
	events := []managedagents.Event{}
	for _, event := range s.events[sessionID] {
		if event.Seq > afterSeq && payloadString(event.Payload, "turn_id") == runID {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *testStore) ListSessionTurnControlEventsContext(_ context.Context, sessionID string, turnID string, afterSeq int64) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	events := []managedagents.Event{}
	for _, event := range s.events[sessionID] {
		if event.Seq <= afterSeq || payloadString(event.Payload, "turn_id") != turnID {
			continue
		}
		switch event.Type {
		case managedagents.EventUserSteer, managedagents.EventUserFollowUp, managedagents.EventUserInterrupt:
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *testStore) sessionRunLocked(sessionID string, runID string) (managedagents.SessionRun, bool) {
	run := managedagents.SessionRun{ID: runID, SessionID: sessionID, Status: managedagents.TurnStatusRunning}
	found := false
	for _, event := range s.events[sessionID] {
		if payloadString(event.Payload, "turn_id") != runID {
			continue
		}
		if !found {
			run.StartedAt = event.CreatedAt
			found = true
		}
		if event.Type == managedagents.EventSessionStatusRunning {
			run.AgentID = payloadString(event.Payload, "agent_id")
			run.AgentConfigVersion = payloadInt(event.Payload, "agent_config_version")
		}
		switch event.Type {
		case managedagents.EventUserMessage:
			run.UserEventID = event.ID
			run.UserEventSeq = event.Seq
		case managedagents.EventUserInterrupt:
			run.Status = managedagents.TurnStatusInterrupted
			ended := event.CreatedAt
			run.InterruptRequestedAt = &ended
			run.EndedAt = &ended
		case managedagents.EventAgentMessage:
			run.Status = managedagents.TurnStatusCompleted
			ended := event.CreatedAt
			run.EndedAt = &ended
		case managedagents.EventSessionStatusIdle:
			if payloadString(event.Payload, "last_turn_status") == managedagents.TurnStatusFailed {
				run.Status = managedagents.TurnStatusFailed
				run.ErrorMessage = payloadString(event.Payload, "reason")
				ended := event.CreatedAt
				run.EndedAt = &ended
			}
		}
	}
	if run.Status == managedagents.TurnStatusRunning {
		for _, intervention := range s.interventions {
			if intervention.SessionID == sessionID && intervention.TurnID == runID && intervention.Status == managedagents.InterventionStatusPending {
				if intervention.Kind == managedagents.InterventionKindClarification || intervention.Kind == managedagents.InterventionKindUploadRequest {
					run.Status = managedagents.TurnStatusWaitingHuman
				} else {
					run.Status = managedagents.TurnStatusWaitingApproval
				}
				break
			}
		}
	}
	for key, values := range s.runIdempotency[sessionID] {
		if values.RunID == runID {
			run.IdempotencyKey = key
			run.RequestHash = values.RequestHash
			break
		}
	}
	return run, found
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

func (s *testStore) ListToolPermissionAuditContext(_ context.Context, input managedagents.ListToolPermissionAuditInput) ([]managedagents.ToolPermissionAuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[input.SessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}
	records := managedagents.ProjectToolPermissionAudit(s.events[input.SessionID])
	filtered := make([]managedagents.ToolPermissionAuditRecord, 0, len(records))
	for _, record := range records {
		if input.Decision != "" && record.Decision != input.Decision {
			continue
		}
		if input.Tool != "" && record.Tool != input.Tool {
			continue
		}
		if input.Before != nil {
			before := input.Before.UTC()
			if record.CreatedAt.After(before) {
				continue
			}
			if record.CreatedAt.Equal(before) && (record.TurnID > input.BeforeTurnID || (record.TurnID == input.BeforeTurnID && record.CallID >= input.BeforeCallID)) {
				continue
			}
		}
		filtered = append(filtered, record)
		if len(filtered) == input.Limit {
			break
		}
	}
	return filtered, nil
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

func (s *testStore) SubscribeEvents(sessionID string, afterSeq int64) (<-chan managedagents.Event, func(), error) {
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; !ok {
		s.mu.Unlock()
		return nil, nil, managedagents.ErrNotFound
	}

	wake := make(chan struct{}, 1)
	if s.subscribers[sessionID] == nil {
		s.subscribers[sessionID] = make(map[chan struct{}]struct{})
	}
	s.subscribers[sessionID][wake] = struct{}{}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan managedagents.Event)
	go s.streamEvents(ctx, sessionID, afterSeq, wake, events)
	return events, cancel, nil
}

func (s *testStore) streamEvents(ctx context.Context, sessionID string, afterSeq int64, wake chan struct{}, events chan<- managedagents.Event) {
	defer close(events)
	defer s.cancelSubscriber(sessionID, wake)
	cursor := afterSeq

	for {
		s.mu.Lock()
		if _, ok := s.sessions[sessionID]; !ok {
			s.mu.Unlock()
			return
		}
		persisted := make([]managedagents.Event, 0, len(s.events[sessionID]))
		for _, event := range s.events[sessionID] {
			if event.Seq > cursor {
				persisted = append(persisted, event)
			}
		}
		s.mu.Unlock()

		for _, event := range persisted {
			select {
			case <-ctx.Done():
				return
			case events <- event:
				cursor = event.Seq
			}
		}

		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				return
			}
		}
	}
}

func (s *testStore) cancelSubscriber(sessionID string, wake chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subscribers[sessionID][wake]; !ok {
		return
	}
	delete(s.subscribers[sessionID], wake)
	if len(s.subscribers[sessionID]) == 0 {
		delete(s.subscribers, sessionID)
	}
	close(wake)
}

func (s *testStore) applyEventLocked(session *managedagents.Session, input managedagents.AppendEventInput, now time.Time) ([]managedagents.Event, error) {
	switch input.Type {
	case managedagents.EventUserMessage:
		if session.Status != managedagents.SessionStatusIdle {
			return nil, fmt.Errorf("%w: user.message requires idle session", managedagents.ErrInvalid)
		}
		configEvents, err := s.followLatestSessionAgentConfigLocked(session, now)
		if err != nil {
			return nil, err
		}
		turnID := s.nextTurnIDLocked(session.ID)
		session.Status = managedagents.SessionStatusRunning
		statusEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusRunning, runningStatusPayload(*session, turnID), now)
		userEvent := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		for _, event := range configEvents {
			s.publishLocked(event)
		}
		s.publishLocked(statusEvent)
		s.publishLocked(userEvent)
		return append(configEvents, statusEvent, userEvent), nil

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
		rejectionEvents := make([]managedagents.Event, 0)
		for key, intervention := range s.interventions {
			if intervention.SessionID != session.ID || intervention.TurnID != turnID || intervention.Status != managedagents.InterventionStatusPending {
				continue
			}
			intervention.Status = managedagents.InterventionStatusRejected
			intervention.DecisionReason = "turn interrupted by user"
			intervention.DecidedAt = &now
			s.interventions[key] = intervention
			payload, _ := json.Marshal(map[string]any{
				"turn_id": turnID,
				"message": "Tool call rejected because the turn was interrupted.",
				"data": map[string]any{
					"id":                intervention.CallID,
					"identifier":        intervention.ToolIdentifier,
					"api_name":          intervention.APIName,
					"arguments":         rawJSONObject(intervention.Arguments),
					"intervention_mode": intervention.InterventionMode,
					"reason":            intervention.Reason,
					"decision_reason":   intervention.DecisionReason,
					"approval_source":   "user_interrupt",
				},
			})
			rejectionEvents = append(rejectionEvents, s.appendEventLocked(session.ID, managedagents.EventRuntimeToolInterventionRejected, payload, now))
		}
		session.Status = managedagents.SessionStatusIdle
		idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, statusPayload("idle", turnID), now)
		s.publishLocked(userEvent)
		s.publishLocked(interruptingEvent)
		for _, event := range rejectionEvents {
			s.publishLocked(event)
		}
		s.publishLocked(idleEvent)
		events := []managedagents.Event{userEvent, interruptingEvent}
		events = append(events, rejectionEvents...)
		events = append(events, idleEvent)
		return events, nil

	case managedagents.EventUserSteer, managedagents.EventUserFollowUp:
		if session.Status != managedagents.SessionStatusRunning {
			return nil, fmt.Errorf("%w: %s requires running session", managedagents.ErrInvalid, input.Type)
		}
		turnID := s.currentTurnIDLocked(session.ID)
		if turnID == "" {
			return nil, fmt.Errorf("%w: running session has no active turn", managedagents.ErrInvalid)
		}
		event := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		s.publishLocked(event)
		return []managedagents.Event{event}, nil

	default:
		event := s.appendEventLocked(session.ID, input.Type, cloneRaw(input.Payload), now)
		s.publishLocked(event)
		return []managedagents.Event{event}, nil
	}
}

func (s *testStore) followLatestSessionAgentConfigLocked(session *managedagents.Session, now time.Time) ([]managedagents.Event, error) {
	policy, err := managedagents.AgentConfigUpdatePolicy(session.RuntimeSettings)
	if err != nil {
		return nil, err
	}
	if policy == managedagents.AgentConfigUpdatePinned {
		return nil, nil
	}
	agent, ok := s.agents[session.AgentID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if agent.CurrentConfigVersion <= session.AgentConfigVersion {
		return nil, nil
	}
	oldVersion := session.AgentConfigVersion
	session.AgentConfigVersion = agent.CurrentConfigVersion
	payload, _ := json.Marshal(map[string]any{
		"old_agent_config_version": oldVersion, "new_agent_config_version": session.AgentConfigVersion,
		"latest_agent_config_version": agent.CurrentConfigVersion, "updated_by": "system:auto-follow",
		"automatic": true, "policy": policy, "trigger": "new_turn",
	})
	return []managedagents.Event{s.appendEventLocked(session.ID, managedagents.EventSessionConfigUpdated, payload, now)}, nil
}

func runningStatusPayload(session managedagents.Session, turnID string) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"status": "running", "turn_id": turnID, "agent_id": session.AgentID,
		"agent_config_version": session.AgentConfigVersion,
	})
	return payload
}

func (s *testStore) appendEventLocked(sessionID, eventType string, payload json.RawMessage, now time.Time) managedagents.Event {
	seq := int64(len(s.events[sessionID]) + 1)
	event := managedagents.Event{
		ID:        s.nextID("evt", &s.nextEventID),
		SessionID: sessionID,
		TurnID:    payloadString(payload, "turn_id"),
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
		case ch <- struct{}{}:
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

func subagentStartWaitSeconds(request managedagents.SubagentStartRequest, now time.Time) int64 {
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

func normalizeTestSubagentTaskGroupStrategy(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", managedagents.SubagentTaskGroupStrategyAllCompleted:
		return managedagents.SubagentTaskGroupStrategyAllCompleted
	case managedagents.SubagentTaskGroupStrategyAnyCompleted:
		return managedagents.SubagentTaskGroupStrategyAnyCompleted
	case managedagents.SubagentTaskGroupStrategyQuorum:
		return managedagents.SubagentTaskGroupStrategyQuorum
	default:
		return ""
	}
}

func normalizeTestSubagentTaskGroupReducer(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return managedagents.SubagentTaskGroupReducerConcatText
	case managedagents.SubagentTaskGroupReducerNone:
		return managedagents.SubagentTaskGroupReducerNone
	case managedagents.SubagentTaskGroupReducerConcatText:
		return managedagents.SubagentTaskGroupReducerConcatText
	case managedagents.SubagentTaskGroupReducerJSONList:
		return managedagents.SubagentTaskGroupReducerJSONList
	case managedagents.SubagentTaskGroupReducerJSONObject:
		return managedagents.SubagentTaskGroupReducerJSONObject
	case managedagents.SubagentTaskGroupReducerFirstSuccess:
		return managedagents.SubagentTaskGroupReducerFirstSuccess
	case managedagents.SubagentTaskGroupReducerMajorityText:
		return managedagents.SubagentTaskGroupReducerMajorityText
	case managedagents.SubagentTaskGroupReducerJSONValues:
		return managedagents.SubagentTaskGroupReducerJSONValues
	case managedagents.SubagentTaskGroupReducerMergeObjects:
		return managedagents.SubagentTaskGroupReducerMergeObjects
	case managedagents.SubagentTaskGroupReducerFirstValue:
		return managedagents.SubagentTaskGroupReducerFirstValue
	case managedagents.SubagentTaskGroupReducerMajorityValue:
		return managedagents.SubagentTaskGroupReducerMajorityValue
	default:
		return ""
	}
}

func normalizeTestSubagentTaskGroupItemState(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", managedagents.SubagentTaskGroupItemStateCreated:
		return managedagents.SubagentTaskGroupItemStateCreated
	case managedagents.SubagentTaskGroupItemStateStarted:
		return managedagents.SubagentTaskGroupItemStateStarted
	case managedagents.SubagentTaskGroupItemStateQueued:
		return managedagents.SubagentTaskGroupItemStateQueued
	case managedagents.SubagentTaskGroupItemStateRejected:
		return managedagents.SubagentTaskGroupItemStateRejected
	default:
		return ""
	}
}

func taskGroupItemStatusFromTestStoreLocked(s *testStore, item managedagents.SubagentTaskGroupItem) string {
	if item.InitialState == managedagents.SubagentTaskGroupItemStateRejected || item.SessionID == "" {
		return managedagents.SubagentTaskGroupItemStateRejected
	}
	for _, request := range s.startRequests {
		if request.SessionID == item.SessionID && request.Status == "pending" {
			return managedagents.SubagentTaskGroupItemStateQueued
		}
	}
	session, ok := s.sessions[item.SessionID]
	if !ok {
		return managedagents.SessionStatusTerminated
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return managedagents.SessionStatusTerminated
	}
	if session.Status == managedagents.SessionStatusRunning {
		if status := managedagents.PendingInterventionTurnStatus(s.interventionsBySessionLocked(item.SessionID)); status != "" {
			return status
		}
		return managedagents.SessionStatusRunning
	}
	if session.Status == managedagents.SessionStatusIdle {
		lastTurnStatus, _, hasAgentText := latestTaskGroupTurnOutcomeFromTestStore(s.events[item.SessionID])
		switch {
		case lastTurnStatus == managedagents.TurnStatusCompleted:
			return managedagents.TurnStatusCompleted
		case lastTurnStatus == managedagents.TurnStatusFailed:
			return managedagents.TurnStatusFailed
		case lastTurnStatus == managedagents.TurnStatusInterrupted:
			return managedagents.SessionStatusTerminated
		case hasAgentText:
			return managedagents.TurnStatusCompleted
		default:
			return managedagents.SubagentTaskGroupItemStateCreated
		}
	}
	return session.Status
}

func taskGroupStatusFromTestStore(group managedagents.SubagentTaskGroup, itemStatuses []string) string {
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
		case managedagents.TurnStatusCompleted:
			completed++
			terminal++
			pendingOnly = false
		case managedagents.TurnStatusFailed, managedagents.SessionStatusTerminated, managedagents.SubagentTaskGroupItemStateRejected:
			failed++
			terminal++
			pendingOnly = false
		case managedagents.SubagentTaskGroupItemStateQueued, managedagents.SubagentTaskGroupItemStateCreated:
		default:
			pendingOnly = false
		}
	}
	if pendingOnly {
		return "pending"
	}
	remaining := total - terminal
	switch group.Strategy {
	case managedagents.SubagentTaskGroupStrategyAnyCompleted:
		if completed > 0 {
			return "completed"
		}
		if group.FailFast && failed > 0 {
			return "failed"
		}
		if terminal == total {
			return "failed"
		}
	case managedagents.SubagentTaskGroupStrategyQuorum:
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

func latestTaskGroupTurnOutcomeFromTestStore(events []managedagents.Event) (string, string, bool) {
	lastTurnStatus := ""
	reason := ""
	hasAgentText := false
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type == managedagents.EventAgentMessage {
			hasAgentText = true
		}
		if event.Type != managedagents.EventSessionStatusIdle {
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

func (s *testStore) interventionsBySessionLocked(sessionID string) []managedagents.SessionIntervention {
	items := make([]managedagents.SessionIntervention, 0)
	for _, intervention := range s.interventions {
		if intervention.SessionID == sessionID {
			items = append(items, intervention)
		}
	}
	return items
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

func payloadInt(payload json.RawMessage, key string) int {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return 0
	}
	number, _ := value[key].(float64)
	return int(number)
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
