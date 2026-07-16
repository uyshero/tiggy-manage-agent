package runner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

func TestMockRunnerCompletesTurn(t *testing.T) {
	store := &mockStore{}
	runner := NewMockRunner(store, 10*time.Millisecond, nil)

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	waitFor(t, func() bool {
		return store.completeCalls() == 1
	})
	if got := payloadText(store.lastPayload()); got != "Mock Agent received your message." {
		t.Fatalf("expected mock agent payload, got %q", got)
	}
}

func TestMockRunnerInterruptCancelsTurn(t *testing.T) {
	store := &mockStore{}
	runner := NewMockRunner(store, 80*time.Millisecond, nil)

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if err := runner.InterruptTurn(t.Context(), InterruptRequest{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
	}); err != nil {
		t.Fatalf("interrupt turn: %v", err)
	}

	time.Sleep(120 * time.Millisecond)
	if got := store.completeCalls(); got != 0 {
		t.Fatalf("expected interrupted turn not to complete, got %d completion calls", got)
	}
}

func TestMockRunnerRejectsDuplicateTurn(t *testing.T) {
	runner := NewMockRunner(&mockStore{}, time.Second, nil)
	request := TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}

	if err := runner.StartTurn(t.Context(), request); err != nil {
		t.Fatalf("first start turn: %v", err)
	}
	defer runner.InterruptTurn(t.Context(), InterruptRequest{SessionID: request.SessionID, TurnID: request.TurnID})

	if err := runner.StartTurn(t.Context(), request); err != ErrTurnAlreadyRunning {
		t.Fatalf("expected ErrTurnAlreadyRunning, got %v", err)
	}
}

func TestWorkerRunnerCompletesTurn(t *testing.T) {
	store := &mockStore{}
	executor := staticExecutor{payload: json.RawMessage(`{"content":[{"type":"text","text":"worker ok"}]}`)}
	runner := NewWorkerRunner(store, executor, 1, nil)
	defer runner.Close()

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	waitFor(t, func() bool {
		return store.completeCalls() == 1
	})
	if got := payloadText(store.lastPayload()); got != "worker ok" {
		t.Fatalf("expected worker payload, got %q", got)
	}
}

func TestWorkerRunnerRecordsLLMUsageAfterSuccessfulTurn(t *testing.T) {
	store := &mockStore{}
	executor := staticExecutor{
		payload: json.RawMessage(`{"content":[{"type":"text","text":"worker ok"}]}`),
		usage: &managedagents.RecordLLMUsageInput{
			WorkspaceID:        "wksp_default",
			AgentID:            "agt_000001",
			AgentConfigVersion: 2,
			SessionID:          "sesn_000001",
			TurnID:             "turn_000001",
			ProviderID:         "volcengine-agent-plan",
			ProviderType:       "openai",
			Model:              "doubao-test",
			InputTokens:        11,
			OutputTokens:       7,
			TotalTokens:        18,
			Status:             "completed",
		},
	}
	runner := NewWorkerRunner(store, executor, 1, nil)
	defer runner.Close()

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	waitFor(t, func() bool {
		return store.usageCalls() == 1
	})
	if got := store.usageRecords[0]; got.ProviderID != "volcengine-agent-plan" || got.Model != "doubao-test" || got.TotalTokens != 18 || got.AgentConfigVersion != 2 {
		t.Fatalf("unexpected usage record: %#v", got)
	}
}

func TestWorkerRunnerFailsTurnWhenExecutorFails(t *testing.T) {
	store := &mockStore{}
	runner := NewWorkerRunner(store, staticExecutor{err: errors.New("executor boom")}, 1, nil)
	defer runner.Close()

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	waitFor(t, func() bool {
		return store.failCalls() == 1
	})
	if got := store.failReason(); got != "executor boom" {
		t.Fatalf("expected failure reason %q, got %q", "executor boom", got)
	}
	if got := store.usageCalls(); got != 0 {
		t.Fatalf("expected failed turn not to record usage, got %d", got)
	}
}

func TestWorkerRunnerLeavesTurnOpenWhenWaitingForApproval(t *testing.T) {
	store := &mockStore{}
	runner := NewWorkerRunner(store, staticExecutor{err: ErrTurnWaitingApproval}, 1, nil)
	defer runner.Close()

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if got := store.completeCalls(); got != 0 {
		t.Fatalf("expected waiting turn not to complete, got %d completions", got)
	}
	if got := store.failCalls(); got != 0 {
		t.Fatalf("expected waiting turn not to fail, got %d failures", got)
	}
}

func TestWorkerRunnerRecordsFailedLLMUsageWhenExecutorFailsAfterModelCall(t *testing.T) {
	store := &mockStore{}
	runner := NewWorkerRunner(store, staticExecutor{
		usage: &managedagents.RecordLLMUsageInput{
			WorkspaceID:        "wksp_default",
			AgentID:            "agt_000001",
			AgentConfigVersion: 2,
			SessionID:          "sesn_000001",
			TurnID:             "turn_000001",
			ProviderID:         "volcengine-agent-plan",
			ProviderType:       "openai",
			Model:              "doubao-test",
			InputTokens:        21,
			OutputTokens:       9,
			TotalTokens:        30,
			Status:             "completed",
		},
		err: errors.New("post llm step failed"),
	}, 1, nil)
	defer runner.Close()

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}

	waitFor(t, func() bool {
		return store.failCalls() == 1 && store.usageCalls() == 1
	})
	got := store.usageRecords[0]
	if got.Status != "failed" || got.ErrorMessage != "post llm step failed" {
		t.Fatalf("expected failed usage status and error, got %#v", got)
	}
	if got.ProviderID != "volcengine-agent-plan" || got.Model != "doubao-test" || got.TotalTokens != 30 {
		t.Fatalf("unexpected failed usage record: %#v", got)
	}
}

func TestWorkerRunnerInterruptCancelsExecutor(t *testing.T) {
	store := &mockStore{}
	executor := newBlockingExecutor()
	runner := NewWorkerRunner(store, executor, 1, nil)
	defer runner.Close()

	if err := runner.StartTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	}); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	<-executor.started

	if err := runner.InterruptTurn(t.Context(), InterruptRequest{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
	}); err != nil {
		t.Fatalf("interrupt turn: %v", err)
	}
	<-executor.canceled

	time.Sleep(20 * time.Millisecond)
	if got := store.completeCalls(); got != 0 {
		t.Fatalf("expected canceled turn not to complete, got %d completion calls", got)
	}
	if got := store.failCalls(); got != 0 {
		t.Fatalf("expected canceled turn not to fail, got %d failure calls", got)
	}
}

func TestWorkerRunnerRecoversAndRunsPersistentTurnsConcurrently(t *testing.T) {
	store := newPersistentMockStore(
		managedagents.SessionTurnWork{SessionID: "sesn_000001", TurnID: "turn_000001", UserEventSeq: 4, UserPayload: json.RawMessage(`{"content":[]}`)},
		managedagents.SessionTurnWork{SessionID: "sesn_000002", TurnID: "turn_000001", UserEventSeq: 4, UserPayload: json.RawMessage(`{"content":[]}`)},
	)
	executor := &concurrentExecutor{started: make(chan TurnRequest, 2), release: make(chan struct{})}
	runner := NewWorkerRunnerWithConfig(store, executor, persistentRunnerTestConfig(2), nil)
	defer runner.Close()

	first := <-executor.started
	second := <-executor.started
	if first.SessionID == second.SessionID {
		t.Fatalf("expected different sessions to run concurrently, got %s twice", first.SessionID)
	}
	close(executor.release)
	waitFor(t, func() bool { return store.completeCalls() == 2 })
}

func TestWorkerRunnerPersistentLeaseDetectsRemoteInterrupt(t *testing.T) {
	store := newPersistentMockStore(managedagents.SessionTurnWork{
		SessionID: "sesn_000001", TurnID: "turn_000001", UserEventSeq: 4, UserPayload: json.RawMessage(`{"content":[]}`),
	})
	executor := newBlockingExecutor()
	runner := NewWorkerRunnerWithConfig(store, executor, persistentRunnerTestConfig(1), nil)
	defer runner.Close()

	<-executor.started
	store.setRenewable(false)
	<-executor.canceled
	if got := store.completeCalls(); got != 0 {
		t.Fatalf("expected remotely interrupted turn not to complete, got %d", got)
	}
}

func TestWorkerRunnerPersistentQueueRestoresInterventionResume(t *testing.T) {
	store := newPersistentMockStore(managedagents.SessionTurnWork{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		ResumeIntervention: &managedagents.SessionIntervention{
			SessionID: "sesn_000001",
			TurnID:    "turn_000001",
			CallID:    "call_approved",
			Status:    managedagents.InterventionStatusApproved,
		},
	})
	executed := make(chan TurnRequest, 1)
	runner := NewWorkerRunnerWithConfig(store, recordingExecutor{executed: executed}, persistentRunnerTestConfig(1), nil)
	defer runner.Close()

	request := <-executed
	if request.ResumeIntervention == nil || request.ResumeIntervention.CallID != "call_approved" || request.ResumeIntervention.Status != managedagents.InterventionStatusApproved {
		t.Fatalf("expected persisted intervention resume, got %+v", request.ResumeIntervention)
	}
}

func TestWorkerRunnerPersistentQueueReapsOrphansBeforeClaimingWork(t *testing.T) {
	store := newPersistentMockStore(managedagents.SessionTurnWork{
		SessionID: "sesn_000001", TurnID: "turn_000001", UserEventSeq: 4, UserPayload: json.RawMessage(`{"content":[]}`),
	})
	store.setReaped(managedagents.ReapedSubagent{
		Session: managedagents.Session{ID: "sesn_orphan", Status: managedagents.SessionStatusTerminated},
		Reason:  "orphaned_parent_terminated",
	})
	executed := make(chan TurnRequest, 1)
	runner := NewWorkerRunnerWithConfig(store, recordingExecutor{executed: executed}, persistentRunnerTestConfig(1), nil)
	defer runner.Close()

	<-executed
	waitFor(t, func() bool { return store.orphanReapCalls() > 0 })
}

func TestWorkerRunnerPostProcessingDoesNotBlockTurnConsumer(t *testing.T) {
	store := newPersistentMockStore(
		managedagents.SessionTurnWork{SessionID: "sesn_000001", TurnID: "turn_000001", UserEventSeq: 4, UserPayload: json.RawMessage(`{"content":[]}`)},
		managedagents.SessionTurnWork{SessionID: "sesn_000002", TurnID: "turn_000001", UserEventSeq: 4, UserPayload: json.RawMessage(`{"content":[]}`)},
	)
	executed := make(chan TurnRequest, 2)
	postRelease := make(chan struct{})
	config := persistentRunnerTestConfig(1)
	config.PostProcess = func(string, string) { <-postRelease }
	runner := NewWorkerRunnerWithConfig(store, recordingExecutor{executed: executed}, config, nil)

	<-executed
	<-executed
	waitFor(t, func() bool { return store.completeCalls() == 2 })
	close(postRelease)
	runner.Close()
}

type mockStore struct {
	mu               sync.Mutex
	completed        int
	failed           int
	reason           string
	payload          json.RawMessage
	summaries        map[string]managedagents.SessionSummary
	interventions    []managedagents.SaveSessionInterventionInput
	usageRecords     []managedagents.RecordLLMUsageInput
	exporterRuns     []managedagents.ObservabilityExporterRun
	runtimeEvents    []string
	runtimePayloads  []json.RawMessage
	history          []managedagents.ConversationMessage
	runtimeSettings  json.RawMessage
	toolsConfig      json.RawMessage
	skillsConfig     json.RawMessage
	skillRecord      skills.Skill
	skillVersion     skills.Version
	skillUsages      []skills.Usage
	workers          []managedagents.Worker
	workerWork       map[string]managedagents.WorkerWork
	enqueuedWork     []managedagents.EnqueueWorkerWorkInput
	sessions         map[string]managedagents.Session
	createdObjects   []managedagents.CreateObjectRefInput
	createdArtifacts []managedagents.CreateSessionArtifactInput
}

func (s *mockStore) completeCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completed
}

func (s *mockStore) lastPayload() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.payload...)
}

func (s *mockStore) failCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func (s *mockStore) failReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

func (s *mockStore) usageCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.usageRecords)
}

func (s *mockStore) runtimeEventTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.runtimeEvents...)
}

func (s *mockStore) enqueuedWorkerWork() []managedagents.EnqueueWorkerWorkInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]managedagents.EnqueueWorkerWorkInput(nil), s.enqueuedWork...)
}

func (s *mockStore) savedInterventions() []managedagents.SaveSessionInterventionInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]managedagents.SaveSessionInterventionInput(nil), s.interventions...)
}

func (s *mockStore) CreateAgent(managedagents.CreateAgentInput) (managedagents.Agent, error) {
	return managedagents.Agent{}, nil
}

func (s *mockStore) EnsureAgent(input managedagents.EnsureAgentInput) (managedagents.Agent, error) {
	return managedagents.Agent{ID: input.ID, Name: input.Name}, nil
}

func (s *mockStore) GetAgent(string) (managedagents.Agent, error) {
	return managedagents.Agent{}, nil
}

func (s *mockStore) GetAgentScoped(id string, scope managedagents.AccessScope) (managedagents.Agent, error) {
	if _, err := managedagents.ValidateAccessScope(scope); err != nil {
		return managedagents.Agent{}, err
	}
	return s.GetAgent(id)
}

func (s *mockStore) ListAgents() ([]managedagents.Agent, error) {
	return nil, nil
}

func (s *mockStore) ListAgentsScoped(scope managedagents.AccessScope) ([]managedagents.Agent, error) {
	if _, err := managedagents.ValidateAccessScope(scope); err != nil {
		return nil, err
	}
	return s.ListAgents()
}

func (s *mockStore) UpdateAgent(managedagents.UpdateAgentInput) (managedagents.Agent, error) {
	return managedagents.Agent{}, nil
}

func (s *mockStore) ListAgentConfigVersions(string) ([]managedagents.AgentConfigVersion, error) {
	return nil, nil
}

func (s *mockStore) CreateAgentConfigVersion(managedagents.CreateAgentConfigVersionInput) (managedagents.Agent, error) {
	return managedagents.Agent{}, nil
}

func (s *mockStore) EnsureLLMProvider(input managedagents.EnsureLLMProviderInput) (managedagents.LLMProvider, error) {
	return s.UpsertLLMProvider(managedagents.UpsertLLMProviderInput{
		ID:           input.ID,
		ProviderType: input.ProviderType,
		BaseURL:      input.BaseURL,
		APIKeyEnv:    input.APIKeyEnv,
		Enabled:      true,
	})
}

func (s *mockStore) UpsertLLMProvider(input managedagents.UpsertLLMProviderInput) (managedagents.LLMProvider, error) {
	return managedagents.LLMProvider{
		ID:           input.ID,
		ProviderType: input.ProviderType,
		BaseURL:      input.BaseURL,
		APIKeyEnv:    input.APIKeyEnv,
		Enabled:      input.Enabled,
	}, nil
}

func (s *mockStore) CreateLLMProvider(input managedagents.UpsertLLMProviderInput) (managedagents.LLMProvider, error) {
	return s.UpsertLLMProvider(input)
}

func (s *mockStore) UpdateLLMProvider(input managedagents.UpdateLLMProviderInput) (managedagents.LLMProvider, error) {
	return s.UpsertLLMProvider(input.UpsertLLMProviderInput)
}

func (s *mockStore) GetLLMProvider(string) (managedagents.LLMProvider, error) {
	return managedagents.LLMProvider{}, nil
}

func (s *mockStore) ListLLMProviders() ([]managedagents.LLMProvider, error) {
	return nil, nil
}

func (s *mockStore) SetLLMProviderEnabled(string, bool) (managedagents.LLMProvider, error) {
	return managedagents.LLMProvider{}, nil
}

func (s *mockStore) SetLLMProviderEnabledIfRevision(string, bool, int64) (managedagents.LLMProvider, error) {
	return managedagents.LLMProvider{}, nil
}

func (s *mockStore) DeleteLLMProvider(string) error {
	return nil
}

func (s *mockStore) DeleteLLMProviderIfRevision(string, int64) error {
	return nil
}

func (s *mockStore) UpsertLLMModel(input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error) {
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	return managedagents.LLMModel{
		ProviderID:          input.ProviderID,
		Model:               input.Model,
		ContextWindowTokens: input.ContextWindowTokens,
	}, nil
}

func (s *mockStore) CreateLLMModel(input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error) {
	return s.UpsertLLMModel(input)
}

func (s *mockStore) UpdateLLMModel(input managedagents.UpdateLLMModelInput) (managedagents.LLMModel, error) {
	return s.UpsertLLMModel(input.UpsertLLMModelInput)
}

func (s *mockStore) ListLLMModels(string) ([]managedagents.LLMModel, error) {
	return nil, nil
}

func (s *mockStore) DeleteLLMModel(string, string) error {
	return nil
}

func (s *mockStore) DeleteLLMModelIfRevision(string, string, int64) error {
	return nil
}

func (s *mockStore) CreateEnvironment(managedagents.CreateEnvironmentInput) (managedagents.Environment, error) {
	return managedagents.Environment{}, nil
}

func (s *mockStore) CreateSession(managedagents.CreateSessionInput) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) CreateSubagentSession(managedagents.CreateSubagentSessionInput) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) StartSubagentTurn(managedagents.StartSubagentTurnInput) ([]managedagents.Event, error) {
	return nil, nil
}

func (s *mockStore) EnqueueSubagentStart(managedagents.EnqueueSubagentStartInput) (managedagents.SubagentStartRequest, error) {
	return managedagents.SubagentStartRequest{}, nil
}

func (s *mockStore) GetPendingSubagentStart(string) (managedagents.SubagentStartRequest, error) {
	return managedagents.SubagentStartRequest{}, managedagents.ErrNotFound
}

func (s *mockStore) CancelSubagentStart(managedagents.CancelSubagentStartInput) (managedagents.SubagentStartRequest, error) {
	return managedagents.SubagentStartRequest{}, managedagents.ErrNotFound
}

func (s *mockStore) CreateSubagentTaskGroup(managedagents.CreateSubagentTaskGroupInput) (managedagents.SubagentTaskGroup, error) {
	return managedagents.SubagentTaskGroup{}, nil
}

func (s *mockStore) AppendSubagentTaskGroupItem(string, managedagents.AppendSubagentTaskGroupItemInput) (managedagents.SubagentTaskGroupItem, error) {
	return managedagents.SubagentTaskGroupItem{}, nil
}

func (s *mockStore) UpdateSubagentTaskGroupItem(string, int, managedagents.UpdateSubagentTaskGroupItemInput) (managedagents.SubagentTaskGroupItem, error) {
	return managedagents.SubagentTaskGroupItem{}, nil
}

func (s *mockStore) GetSubagentTaskGroup(string) (managedagents.SubagentTaskGroup, error) {
	return managedagents.SubagentTaskGroup{}, managedagents.ErrNotFound
}

func (s *mockStore) ListSubagentTaskGroupsByParentSession(string) ([]managedagents.SubagentTaskGroup, error) {
	return nil, nil
}

func (s *mockStore) GetSubagentTaskGroupItemBySession(string) (managedagents.SubagentTaskGroupItem, error) {
	return managedagents.SubagentTaskGroupItem{}, managedagents.ErrNotFound
}

func (s *mockStore) ListSubagentTaskGroupItems(string) ([]managedagents.SubagentTaskGroupItem, error) {
	return nil, nil
}

func (s *mockStore) ListChildSubagentTaskGroups(string, int) ([]managedagents.SubagentTaskGroup, error) {
	return nil, nil
}

func (s *mockStore) CancelSubagentTaskGroup(managedagents.CancelSubagentTaskGroupInput) (managedagents.SubagentTaskGroup, error) {
	return managedagents.SubagentTaskGroup{}, nil
}

func (s *mockStore) ReactivateSubagentTaskGroup(managedagents.ReactivateSubagentTaskGroupInput) (managedagents.SubagentTaskGroup, error) {
	return managedagents.SubagentTaskGroup{}, nil
}

func (s *mockStore) GetSubagentTaskGroupMetrics(managedagents.GetSubagentTaskGroupMetricsInput) (managedagents.SubagentTaskGroupMetrics, error) {
	return managedagents.SubagentTaskGroupMetrics{}, nil
}

func (s *mockStore) GetSubagentMetrics(managedagents.GetSubagentMetricsInput) (managedagents.SubagentMetrics, error) {
	return managedagents.SubagentMetrics{}, nil
}

func (s *mockStore) GetSession(sessionID string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if session, ok := s.sessions[sessionID]; ok {
		return session, nil
	}
	return managedagents.Session{}, managedagents.ErrNotFound
}

func (s *mockStore) GetSessionScoped(sessionID string, scope managedagents.AccessScope) (managedagents.Session, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.Session{}, err
	}
	session, err := s.GetSession(sessionID)
	if err != nil {
		return managedagents.Session{}, err
	}
	if session.WorkspaceID != "" && session.WorkspaceID != scope.WorkspaceID {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if scope.OwnerID != "" && session.OwnerID != "" && session.OwnerID != scope.OwnerID {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	return session, nil
}

func (s *mockStore) ListSessions(managedagents.ListSessionsInput) ([]managedagents.Session, error) {
	return nil, nil
}

func (s *mockStore) ListSessionsScoped(input managedagents.ListSessionsInput, scope managedagents.AccessScope) ([]managedagents.Session, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = scope.WorkspaceID
	input.OwnerID = scope.OwnerID
	return s.ListSessions(input)
}

func (s *mockStore) UpdateSessionRuntimeSettings(string, managedagents.UpdateSessionRuntimeSettingsInput) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) UpdateSessionMetadata(id string, input managedagents.UpdateSessionMetadataInput) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		return managedagents.Session{}, nil
	}
	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if input.Pinned != nil {
		if *input.Pinned {
			now := time.Now().UTC()
			session.PinnedAt = &now
		} else {
			session.PinnedAt = nil
		}
	}
	if input.Tags != nil {
		session.Tags = append([]string(nil), (*input.Tags)...)
	}
	s.sessions[id] = session
	return session, nil
}

func (s *mockStore) UpgradeSessionAgentConfig(string, managedagents.UpgradeSessionAgentConfigInput) (managedagents.UpgradeSessionAgentConfigResult, error) {
	return managedagents.UpgradeSessionAgentConfigResult{}, nil
}

func (s *mockStore) SaveSessionIntervention(sessionID string, input managedagents.SaveSessionInterventionInput) (managedagents.SessionIntervention, error) {
	s.mu.Lock()
	s.interventions = append(s.interventions, input)
	s.mu.Unlock()

	now := time.Now().UTC()
	return managedagents.SessionIntervention{
		SessionID:         sessionID,
		TurnID:            input.TurnID,
		CallID:            input.CallID,
		ToolIdentifier:    input.ToolIdentifier,
		APIName:           input.APIName,
		Arguments:         input.Arguments,
		Kind:              input.Kind,
		Request:           input.Request,
		InterventionMode:  input.InterventionMode,
		Reason:            input.Reason,
		Status:            managedagents.InterventionStatusPending,
		RequestedAt:       now,
		Continuation:      input.Continuation,
		ContinuationRound: input.ContinuationRound,
	}, nil
}

func (s *mockStore) ListSessionInterventions(string, string) ([]managedagents.SessionIntervention, error) {
	return nil, nil
}

func (s *mockStore) DecideSessionIntervention(string, managedagents.DecideSessionInterventionInput) (managedagents.DecideSessionInterventionResult, error) {
	return managedagents.DecideSessionInterventionResult{}, nil
}

func (s *mockStore) MarkSessionTurnWaitingApproval(string, string) error {
	return nil
}

func (s *mockStore) MarkSessionTurnWaitingHuman(string, string) error {
	return nil
}

func (s *mockStore) ResolveAgentRuntimeConfig(sessionID string) (managedagents.AgentRuntimeConfig, error) {
	return managedagents.AgentRuntimeConfig{
		SessionID:             sessionID,
		WorkspaceID:           "wksp_default",
		AgentID:               "agt_000001",
		AgentConfigVersion:    1,
		EnvironmentID:         "env_000001",
		LLMProvider:           "fake",
		LLMProviderType:       "fake",
		LLMModel:              "fake-demo",
		ContextWindowTokens:   managedagents.DefaultContextWindowTokens,
		SummaryText:           "summary from mock store",
		SummarySourceUntilSeq: 2,
		RuntimeSettings:       append(json.RawMessage(nil), s.runtimeSettings...),
		Tools:                 append(json.RawMessage(nil), s.toolsConfig...),
		Skills:                append(json.RawMessage(nil), s.skillsConfig...),
	}, nil
}

func (s *mockStore) CreateSkill(context.Context, skills.CreateSkillInput) (skills.Skill, error) {
	return skills.Skill{}, errors.New("unsupported")
}

func (s *mockStore) GetSkill(context.Context, string) (skills.Skill, error) {
	return s.skillRecord, nil
}

func (s *mockStore) GetSkillByIdentifier(_ context.Context, _ string, identifier string) (skills.Skill, error) {
	if identifier != s.skillRecord.Identifier {
		return skills.Skill{}, managedagents.ErrNotFound
	}
	return s.skillRecord, nil
}

func (s *mockStore) ListSkills(context.Context, skills.ListSkillsInput) ([]skills.Skill, error) {
	return []skills.Skill{s.skillRecord}, nil
}

func (s *mockStore) ArchiveSkill(context.Context, string) (skills.Skill, error) {
	return skills.Skill{}, errors.New("unsupported")
}

func (s *mockStore) CreateSkillVersion(context.Context, skills.CreateVersionInput) (skills.Version, error) {
	return skills.Version{}, errors.New("unsupported")
}

func (s *mockStore) GetSkillVersion(_ context.Context, skillID string, version int) (skills.Version, error) {
	if skillID != s.skillVersion.SkillID || version != s.skillVersion.Version {
		return skills.Version{}, managedagents.ErrNotFound
	}
	return s.skillVersion, nil
}

func (s *mockStore) ListSkillVersions(context.Context, string) ([]skills.Version, error) {
	return []skills.Version{s.skillVersion}, nil
}

func (s *mockStore) RecordSkillUsages(_ context.Context, usages []skills.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillUsages = append(s.skillUsages, usages...)
	return nil
}

func (s *mockStore) GetSessionSummary(sessionID string) (managedagents.SessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.summaries == nil {
		s.summaries = make(map[string]managedagents.SessionSummary)
	}
	summary, ok := s.summaries[sessionID]
	if !ok {
		return managedagents.SessionSummary{}, managedagents.ErrNotFound
	}
	return summary, nil
}

func (s *mockStore) SaveSessionSummary(sessionID string, input managedagents.UpsertSessionSummaryInput) (managedagents.SessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.summaries == nil {
		s.summaries = make(map[string]managedagents.SessionSummary)
	}
	summary := managedagents.SessionSummary{
		SessionID:      sessionID,
		SummaryText:    input.SummaryText,
		SourceUntilSeq: input.SourceUntilSeq,
	}
	s.summaries[sessionID] = summary
	return summary, nil
}

func (s *mockStore) UpsertSessionSummary(string, managedagents.UpsertSessionSummaryInput) (managedagents.UpsertSessionSummaryResult, error) {
	return managedagents.UpsertSessionSummaryResult{}, nil
}

func (s *mockStore) ArchiveSession(string) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) RestoreSession(string) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) DeleteSession(string) error {
	return nil
}

func (s *mockStore) AppendEvents(string, []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	return nil, nil
}

func (s *mockStore) AppendRuntimeEvent(sessionID string, turnID string, input managedagents.AppendEventInput) ([]managedagents.Event, error) {
	s.mu.Lock()
	s.runtimeEvents = append(s.runtimeEvents, input.Type)
	s.runtimePayloads = append(s.runtimePayloads, append(json.RawMessage(nil), input.Payload...))
	s.mu.Unlock()

	return []managedagents.Event{{
		ID:        "evt_runtime",
		SessionID: sessionID,
		Seq:       1,
		Type:      input.Type,
		Payload:   append(json.RawMessage(nil), input.Payload...),
	}}, nil
}

func (s *mockStore) CompleteSessionTurn(sessionID string, turnID string, payload json.RawMessage) ([]managedagents.Event, error) {
	s.mu.Lock()
	s.completed++
	s.payload = append(json.RawMessage(nil), payload...)
	s.mu.Unlock()

	return []managedagents.Event{{
		ID:        "evt_000001",
		SessionID: sessionID,
		Seq:       1,
		Type:      managedagents.EventSessionStatusIdle,
		Payload:   json.RawMessage(`{"turn_id":"` + turnID + `"}`),
	}}, nil
}

func payloadText(payload json.RawMessage) string {
	var object struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &object); err != nil || len(object.Content) == 0 {
		return ""
	}
	return object.Content[0].Text
}

func (s *mockStore) FailSessionTurn(sessionID string, turnID string, reason string) ([]managedagents.Event, error) {
	s.mu.Lock()
	s.failed++
	s.reason = reason
	s.mu.Unlock()

	return []managedagents.Event{{
		ID:        "evt_000002",
		SessionID: sessionID,
		Seq:       1,
		Type:      managedagents.EventSessionStatusIdle,
		Payload:   json.RawMessage(`{"turn_id":"` + turnID + `","last_turn_status":"failed","reason":"` + reason + `"}`),
	}}, nil
}

func (s *mockStore) RecordLLMUsage(input managedagents.RecordLLMUsageInput) (managedagents.LLMUsageRecord, error) {
	s.mu.Lock()
	s.usageRecords = append(s.usageRecords, input)
	s.mu.Unlock()

	return managedagents.LLMUsageRecord{
		ID:                 "llmu_000001",
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

func (s *mockStore) GetSessionLLMUsage(sessionID string) (managedagents.LLMUsageReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *mockStore) ListLLMUsage(input managedagents.ListLLMUsageInput) (managedagents.LLMUsageAggregateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	report := managedagents.LLMUsageAggregateReport{
		GroupBy: managedagents.LLMUsageGroupByProviderModel,
		Filters: input,
		Groups:  []managedagents.LLMUsageAggregate{},
	}
	for _, usage := range s.usageRecords {
		if input.ProviderID != "" && usage.ProviderID != input.ProviderID {
			continue
		}
		if input.Model != "" && usage.Model != input.Model {
			continue
		}
		record := managedagents.LLMUsageRecord{
			ProviderID:        usage.ProviderID,
			Model:             usage.Model,
			InputTokens:       usage.InputTokens,
			OutputTokens:      usage.OutputTokens,
			TotalTokens:       usage.TotalTokens,
			CachedInputTokens: usage.CachedInputTokens,
			ReasoningTokens:   usage.ReasoningTokens,
			LatencyMillis:     usage.LatencyMillis,
		}
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

func (s *mockStore) RecordObservabilityExporterRun(input managedagents.RecordObservabilityExporterRunInput) (managedagents.ObservabilityExporterRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	finishedAt := input.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = startedAt
	}
	run := managedagents.ObservabilityExporterRun{
		ID:          fmt.Sprintf("oexp_%06d", len(s.exporterRuns)+1),
		Exporter:    input.Exporter,
		Status:      input.Status,
		SessionID:   input.SessionID,
		TurnID:      input.TurnID,
		TraceID:     input.TraceID,
		Destination: input.Destination,
		Message:     input.Message,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	}
	s.exporterRuns = append(s.exporterRuns, run)
	return run, nil
}

func (s *mockStore) ListObservabilityExporterRuns(input managedagents.ListObservabilityExporterRunsInput) ([]managedagents.ObservabilityExporterRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	runs := make([]managedagents.ObservabilityExporterRun, 0, len(s.exporterRuns))
	for i := len(s.exporterRuns) - 1; i >= 0 && len(runs) < limit; i-- {
		run := s.exporterRuns[i]
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
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *mockStore) CreateObjectRef(input managedagents.CreateObjectRefInput) (managedagents.ObjectRef, error) {
	s.mu.Lock()
	s.createdObjects = append(s.createdObjects, input)
	s.mu.Unlock()
	return managedagents.ObjectRef{
		ID:              "obj_000001",
		WorkspaceID:     input.WorkspaceID,
		StorageProvider: input.StorageProvider,
		Bucket:          input.Bucket,
		ObjectKey:       input.ObjectKey,
		ObjectVersion:   input.ObjectVersion,
		ContentType:     input.ContentType,
		SizeBytes:       input.SizeBytes,
		ChecksumSHA256:  input.ChecksumSHA256,
		ETag:            input.ETag,
		Visibility:      input.Visibility,
		Metadata:        input.Metadata,
		CreatedBy:       input.CreatedBy,
	}, nil
}

func (s *mockStore) GetObjectRef(string) (managedagents.ObjectRef, error) {
	return managedagents.ObjectRef{}, nil
}

func (s *mockStore) GetObjectRefScoped(id string, scope managedagents.AccessScope) (managedagents.ObjectRef, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.ObjectRef{}, err
	}
	object, err := s.GetObjectRef(id)
	if err != nil {
		return managedagents.ObjectRef{}, err
	}
	if object.WorkspaceID != "" && object.WorkspaceID != scope.WorkspaceID {
		return managedagents.ObjectRef{}, managedagents.ErrNotFound
	}
	return object, nil
}

func (s *mockStore) CreateSessionArtifact(input managedagents.CreateSessionArtifactInput) (managedagents.SessionArtifact, error) {
	s.mu.Lock()
	s.createdArtifacts = append(s.createdArtifacts, input)
	s.mu.Unlock()
	return managedagents.SessionArtifact{
		ID:            "art_000001",
		WorkspaceID:   input.WorkspaceID,
		SessionID:     input.SessionID,
		EnvironmentID: input.EnvironmentID,
		ObjectRefID:   input.ObjectRefID,
		TurnID:        input.TurnID,
		ToolCallID:    input.ToolCallID,
		Name:          input.Name,
		Description:   input.Description,
		ArtifactType:  input.ArtifactType,
		Metadata:      input.Metadata,
		CreatedBy:     input.CreatedBy,
	}, nil
}

func (s *mockStore) GetSessionArtifact(string, string) (managedagents.SessionArtifact, error) {
	return managedagents.SessionArtifact{}, nil
}

func (s *mockStore) CountSessionArtifactsByObjectRef(string) (int, error) {
	return 0, nil
}

func (s *mockStore) DeleteObjectRef(string) error {
	return nil
}

func (s *mockStore) DeleteSessionArtifact(string, string) error {
	return nil
}

func (s *mockStore) ListSessionArtifacts(string) ([]managedagents.SessionArtifact, error) {
	return nil, nil
}

func (s *mockStore) RegisterWorker(managedagents.RegisterWorkerInput) (managedagents.Worker, error) {
	return managedagents.Worker{}, nil
}

func (s *mockStore) GetWorker(string) (managedagents.Worker, error) {
	return managedagents.Worker{}, managedagents.ErrNotFound
}

func (s *mockStore) GetWorkerScoped(id string, scope managedagents.AccessScope) (managedagents.Worker, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.Worker{}, err
	}
	worker, err := s.GetWorker(id)
	if err != nil {
		return managedagents.Worker{}, err
	}
	if worker.WorkspaceID != "" && worker.WorkspaceID != scope.WorkspaceID {
		return managedagents.Worker{}, managedagents.ErrNotFound
	}
	return worker, nil
}

func (s *mockStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	workers := []managedagents.Worker{}
	for _, worker := range s.workers {
		if worker.WorkspaceID != "" && worker.WorkspaceID != workspaceID {
			continue
		}
		if input.Status != "" && worker.Status != input.Status {
			continue
		}
		workers = append(workers, worker)
	}
	return workers, nil
}

func (s *mockStore) ListWorkersScoped(input managedagents.ListWorkersInput, scope managedagents.AccessScope) ([]managedagents.Worker, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return nil, err
	}
	input.WorkspaceID = scope.WorkspaceID
	return s.ListWorkers(input)
}

func (s *mockStore) HeartbeatWorker(string, managedagents.WorkerHeartbeatInput) (managedagents.Worker, error) {
	return managedagents.Worker{}, managedagents.ErrNotFound
}

func (s *mockStore) ArchiveWorker(string) (managedagents.Worker, error) {
	return managedagents.Worker{}, managedagents.ErrNotFound
}

func (s *mockStore) EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.workerWork == nil {
		s.workerWork = make(map[string]managedagents.WorkerWork)
	}
	id := fmt.Sprintf("work_%06d", len(s.workerWork)+1)
	now := time.Now().UTC()
	work := managedagents.WorkerWork{
		ID:            id,
		WorkspaceID:   defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID),
		WorkerID:      input.WorkerID,
		EnvironmentID: input.EnvironmentID,
		SessionID:     input.SessionID,
		TurnID:        input.TurnID,
		WorkType:      defaultString(input.WorkType, managedagents.WorkerWorkTypeToolExecution),
		Status:        managedagents.WorkerWorkStatusCompleted,
		Payload:       append(json.RawMessage(nil), input.Payload...),
		Result:        mockCompletedWorkerToolResult(input.Payload),
		CreatedAt:     now,
		UpdatedAt:     now,
		CompletedAt:   &now,
	}
	s.workerWork[id] = work
	s.enqueuedWork = append(s.enqueuedWork, input)
	return work, nil
}

func (s *mockStore) GetWorkerWork(id string) (managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workerWork == nil {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	work, ok := s.workerWork[id]
	if !ok {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	return work, nil
}

func (s *mockStore) GetWorkerWorkScoped(id string, scope managedagents.AccessScope) (managedagents.WorkerWork, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return managedagents.WorkerWork{}, err
	}
	work, err := s.GetWorkerWork(id)
	if err != nil {
		return managedagents.WorkerWork{}, err
	}
	if work.WorkspaceID != "" && work.WorkspaceID != scope.WorkspaceID {
		return managedagents.WorkerWork{}, managedagents.ErrNotFound
	}
	return work, nil
}

func mockCompletedWorkerToolResult(payload json.RawMessage) json.RawMessage {
	var invocation tools.WorkInvocation
	_ = json.Unmarshal(payload, &invocation)
	var input struct {
		OutputPaths []string `json:"output_paths"`
	}
	_ = json.Unmarshal(invocation.Input, &input)

	stdout := "worker ok"
	inputText := string(invocation.Input)
	switch {
	case strings.Contains(inputText, "tma-worker-export-ok"):
		stdout = "tma-worker-export-ok"
	case strings.Contains(inputText, "tma-session-tool-ok"):
		stdout = "/worker\n tma-session-tool-ok"
	}
	state, _ := json.Marshal(capability.CommandResult{
		ExitCode: 0,
		Stdout:   stdout,
		Stderr:   "",
	})
	exportedFiles := make([]tools.ArtifactExport, 0, len(input.OutputPaths))
	for _, exportPath := range input.OutputPaths {
		exportPath = strings.TrimSpace(exportPath)
		if exportPath == "" {
			continue
		}
		exportedFiles = append(exportedFiles, tools.ArtifactExport{
			Path:          exportPath,
			Name:          filepath.Base(exportPath),
			ContentType:   "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString([]byte(stdout)),
		})
	}
	result, _ := json.Marshal(map[string]any{
		"status":       "executed",
		"work_type":    managedagents.WorkerWorkTypeToolExecution,
		"tool_runtime": tools.ToolRuntimeLocalSystem,
		"invocation":   invocation,
		"tool_result": tools.ExecutionResult{
			Identifier:    invocation.Namespace,
			APIName:       invocation.API,
			Content:       stdout,
			State:         state,
			ExportedFiles: exportedFiles,
		},
	})
	return result
}

func (s *mockStore) PollWorkerWork(string, managedagents.PollWorkerWorkInput) (*managedagents.WorkerWork, error) {
	return nil, nil
}

func (s *mockStore) AckWorkerWork(string, string) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *mockStore) HeartbeatWorkerWork(string, string, managedagents.WorkerWorkHeartbeatInput) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *mockStore) CancelWorkerWork(string, managedagents.CancelWorkerWorkInput) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *mockStore) RequeueWorkerWork(string, managedagents.RequeueWorkerWorkInput) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *mockStore) ReapExpiredWorkerWork(managedagents.ReapExpiredWorkerWorkInput) ([]managedagents.WorkerWork, error) {
	return nil, nil
}

func (s *mockStore) ReapExpiredWorkers(managedagents.ReapExpiredWorkersInput) ([]managedagents.Worker, error) {
	return nil, nil
}

func (s *mockStore) CompleteWorkerWork(string, string, managedagents.CompleteWorkerWorkInput) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *mockStore) ListEvents(string, int64) ([]managedagents.Event, error) {
	return nil, nil
}

func (s *mockStore) ListConversationMessages(string, int64) ([]managedagents.ConversationMessage, error) {
	return append([]managedagents.ConversationMessage(nil), s.history...), nil
}

func (s *mockStore) SubscribeEvents(string, int64) (<-chan managedagents.Event, func(), error) {
	events := make(chan managedagents.Event)
	return events, func() { close(events) }, nil
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

type staticExecutor struct {
	payload json.RawMessage
	usage   *managedagents.RecordLLMUsageInput
	err     error
}

func (e staticExecutor) RunTurn(context.Context, TurnRequest) (TurnResult, error) {
	if e.err != nil {
		return TurnResult{Usage: e.usage}, e.err
	}
	return TurnResult{AgentPayload: e.payload, Usage: e.usage}, nil
}

type blockingExecutor struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

type persistentMockStore struct {
	*mockStore
	queueMu   sync.Mutex
	pending   []managedagents.SessionTurnWork
	renewable bool
	reaped    []managedagents.ReapedSubagent
	reapCalls int
}

func newPersistentMockStore(work ...managedagents.SessionTurnWork) *persistentMockStore {
	return &persistentMockStore{
		mockStore: &mockStore{},
		pending:   append([]managedagents.SessionTurnWork(nil), work...),
		renewable: true,
	}
}

func (s *persistentMockStore) ClaimSessionTurns(input managedagents.ClaimSessionTurnsInput) ([]managedagents.SessionTurnWork, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if len(s.pending) == 0 {
		return nil, nil
	}
	limit := input.Limit
	if limit <= 0 || limit > len(s.pending) {
		limit = len(s.pending)
	}
	claimed := append([]managedagents.SessionTurnWork(nil), s.pending[:limit]...)
	s.pending = append([]managedagents.SessionTurnWork(nil), s.pending[limit:]...)
	for index := range claimed {
		claimed[index].Attempt++
	}
	return claimed, nil
}

func (s *persistentMockStore) ReapOrphanSubagents(managedagents.ReapOrphanSubagentsInput) ([]managedagents.ReapedSubagent, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	s.reapCalls++
	if len(s.reaped) == 0 {
		return nil, nil
	}
	out := append([]managedagents.ReapedSubagent(nil), s.reaped...)
	s.reaped = nil
	return out, nil
}

func (s *persistentMockStore) RenewSessionTurnLease(managedagents.RenewSessionTurnLeaseInput) (bool, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.renewable, nil
}

func (s *persistentMockStore) ReleaseSessionTurnLease(managedagents.ReleaseSessionTurnLeaseInput) error {
	return nil
}

func (s *persistentMockStore) setRenewable(value bool) {
	s.queueMu.Lock()
	s.renewable = value
	s.queueMu.Unlock()
}

func (s *persistentMockStore) setReaped(items ...managedagents.ReapedSubagent) {
	s.queueMu.Lock()
	s.reaped = append([]managedagents.ReapedSubagent(nil), items...)
	s.queueMu.Unlock()
}

func (s *persistentMockStore) orphanReapCalls() int {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.reapCalls
}

type concurrentExecutor struct {
	started chan TurnRequest
	release chan struct{}
}

func (e *concurrentExecutor) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	e.started <- request
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	case <-e.release:
		return TurnResult{AgentPayload: json.RawMessage(`{"content":[]}`)}, nil
	}
}

type recordingExecutor struct {
	executed chan TurnRequest
}

func (e recordingExecutor) RunTurn(_ context.Context, request TurnRequest) (TurnResult, error) {
	e.executed <- request
	return TurnResult{AgentPayload: json.RawMessage(`{"content":[]}`)}, nil
}

func persistentRunnerTestConfig(workerCount int) WorkerRunnerConfig {
	return WorkerRunnerConfig{
		WorkerCount:       workerCount,
		WakeBuffer:        workerCount,
		PollInterval:      5 * time.Millisecond,
		LeaseDuration:     100 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		InstanceID:        "test-runner",
		PostProcess:       func(string, string) {},
	}
}

func newBlockingExecutor() *blockingExecutor {
	return &blockingExecutor{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (e *blockingExecutor) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	e.once.Do(func() { close(e.started) })
	<-ctx.Done()
	close(e.canceled)
	return TurnResult{}, ctx.Err()
}
