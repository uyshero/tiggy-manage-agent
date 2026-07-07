package runner

import (
	"context"
	"encoding/json"
	"errors"
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
	mu            sync.Mutex
	completed     int
	failed        int
	reason        string
	payload       json.RawMessage
	runtimeEvents []string
	history       []managedagents.ConversationMessage
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

func (s *mockStore) runtimeEventTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.runtimeEvents...)
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

func (s *mockStore) CreateEnvironment(managedagents.CreateEnvironmentInput) (managedagents.Environment, error) {
	return managedagents.Environment{}, nil
}

func (s *mockStore) CreateSession(managedagents.CreateSessionInput) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) GetSession(string) (managedagents.Session, error) {
	return managedagents.Session{}, nil
}

func (s *mockStore) ResolveAgentRuntimeConfig(sessionID string) (managedagents.AgentRuntimeConfig, error) {
	return managedagents.AgentRuntimeConfig{
		SessionID:       sessionID,
		LLMProvider:     "fake",
		LLMProviderType: "fake",
		LLMModel:        "fake-demo",
	}, nil
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
	err     error
}

func (e staticExecutor) RunTurn(context.Context, TurnRequest) (json.RawMessage, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.payload, nil
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

func (e *blockingExecutor) RunTurn(ctx context.Context, request TurnRequest) (json.RawMessage, error) {
	e.once.Do(func() { close(e.started) })
	<-ctx.Done()
	close(e.canceled)
	return nil, ctx.Err()
}
