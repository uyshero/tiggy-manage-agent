package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
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

type mockStore struct {
	mu              sync.Mutex
	completed       int
	failed          int
	reason          string
	payload         json.RawMessage
	summaries       map[string]managedagents.SessionSummary
	interventions   []managedagents.SaveSessionInterventionInput
	usageRecords    []managedagents.RecordLLMUsageInput
	runtimeEvents   []string
	history         []managedagents.ConversationMessage
	runtimeSettings json.RawMessage
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

func (s *mockStore) savedInterventions() []managedagents.SaveSessionInterventionInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]managedagents.SaveSessionInterventionInput(nil), s.interventions...)
}

func (s *mockStore) CreateAgent(managedagents.CreateAgentInput) (managedagents.Agent, error) {
	return managedagents.Agent{}, nil
}

func (s *mockStore) GetAgent(string) (managedagents.Agent, error) {
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

func (s *mockStore) GetLLMProvider(string) (managedagents.LLMProvider, error) {
	return managedagents.LLMProvider{}, nil
}

func (s *mockStore) ListLLMProviders() ([]managedagents.LLMProvider, error) {
	return nil, nil
}

func (s *mockStore) SetLLMProviderEnabled(string, bool) (managedagents.LLMProvider, error) {
	return managedagents.LLMProvider{}, nil
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

func (s *mockStore) ListLLMModels(string) ([]managedagents.LLMModel, error) {
	return nil, nil
}

func (s *mockStore) CreateEnvironment(managedagents.CreateEnvironmentInput) (managedagents.Environment, error) {
	return managedagents.Environment{}, nil
}

func (s *mockStore) CreateSession(managedagents.CreateSessionInput) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) GetSession(string) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) UpdateSessionRuntimeSettings(string, managedagents.UpdateSessionRuntimeSettingsInput) (managedagents.Session, error) {
	return managedagents.Session{}, nil
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

func (s *mockStore) ResolveAgentRuntimeConfig(sessionID string) (managedagents.AgentRuntimeConfig, error) {
	return managedagents.AgentRuntimeConfig{
		SessionID:             sessionID,
		WorkspaceID:           "wksp_default",
		AgentID:               "agt_000001",
		AgentConfigVersion:    1,
		LLMProvider:           "fake",
		LLMProviderType:       "fake",
		LLMModel:              "fake-demo",
		ContextWindowTokens:   managedagents.DefaultContextWindowTokens,
		SummaryText:           "summary from mock store",
		SummarySourceUntilSeq: 2,
		RuntimeSettings:       append(json.RawMessage(nil), s.runtimeSettings...),
	}, nil
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

func (s *mockStore) DeleteSession(string) error {
	return nil
}

func (s *mockStore) AppendEvents(string, []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	return nil, nil
}

func (s *mockStore) AppendRuntimeEvent(sessionID string, turnID string, input managedagents.AppendEventInput) ([]managedagents.Event, error) {
	s.mu.Lock()
	s.runtimeEvents = append(s.runtimeEvents, input.Type)
	s.mu.Unlock()

	return []managedagents.Event{{
		ID:        "evt_runtime",
		SessionID: sessionID,
		Seq:       1,
		Type:      input.Type,
		Payload:   json.RawMessage(`{"turn_id":"` + turnID + `"}`),
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

func (s *mockStore) ListEvents(string, int64) ([]managedagents.Event, error) {
	return nil, nil
}

func (s *mockStore) ListConversationMessages(string, int64) ([]managedagents.ConversationMessage, error) {
	return append([]managedagents.ConversationMessage(nil), s.history...), nil
}

func (s *mockStore) SubscribeEvents(string) (<-chan managedagents.Event, func(), error) {
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
