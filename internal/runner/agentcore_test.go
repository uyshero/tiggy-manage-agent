package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/modeltest"
)

func TestAgentCoreRolloutPolicy(t *testing.T) {
	t.Parallel()

	config := managedagents.AgentRuntimeConfig{WorkspaceID: "wksp_a", AgentID: "agent_a"}
	tests := []struct {
		name   string
		policy AgentCoreRolloutPolicy
		want   bool
	}{
		{name: "disabled", policy: AgentCoreRolloutPolicy{Enabled: false, Percent: 100}, want: false},
		{name: "zero percent", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 0}, want: false},
		{name: "full rollout", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 100}, want: true},
		{name: "workspace allowed", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 100, WorkspaceIDs: []string{"wksp_a"}}, want: true},
		{name: "workspace denied", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 100, WorkspaceIDs: []string{"wksp_b"}}, want: false},
		{name: "agent allowed", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 100, AgentIDs: []string{"agent_a"}}, want: true},
		{name: "agent denied", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 100, AgentIDs: []string{"agent_b"}}, want: false},
		{name: "allowlists use and", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 100, WorkspaceIDs: []string{"wksp_a"}, AgentIDs: []string{"agent_b"}}, want: false},
		{name: "invalid percent fails closed", policy: AgentCoreRolloutPolicy{Enabled: true, Percent: 101}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.policy.Allows(config, "session_1"); got != test.want {
				t.Fatalf("Allows() = %t, want %t", got, test.want)
			}
		})
	}

	policy := AgentCoreRolloutPolicy{Enabled: true, Percent: 50}
	stable := policy.Allows(config, "session_stable")
	for range 10 {
		if got := policy.Allows(config, "session_stable"); got != stable {
			t.Fatalf("session bucket changed: got %t, want %t", got, stable)
		}
	}
	var allowed, denied bool
	for index := range 100 {
		if policy.Allows(config, fmt.Sprintf("session_%d", index)) {
			allowed = true
		} else {
			denied = true
		}
	}
	if !allowed || !denied {
		t.Fatalf("50 percent rollout did not split sample: allowed=%t denied=%t", allowed, denied)
	}
}

func TestAgentRuntimeTurnExecutorRunsDurableCore(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{agentLoopRepo: repository}
	legacy := &forbiddenRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime:     legacy,
		CoreRollout: AgentCoreRolloutPolicy{Enabled: true, Percent: 100},
		CoreClient:  llm.FakeClient{},
		Store:       store,
		LiveEvents:  NewLiveEventBroker(8),
		Timeout:     time.Minute,
	}
	result, err := executor.RunTurn(context.Background(), TurnRequest{
		SessionID: "session_1", TurnID: "turn_1", UserEventSeq: 10,
		UserPayload: []byte(`{"content":[{"type":"text","text":"hello durable core"}]}`),
		LeaseOwner:  "worker_1", Attempt: 1,
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if !result.DurableFinalized || result.DurableStatus != "completed" || result.Usage == nil {
		t.Fatalf("RunTurn() result = %+v", result)
	}
	var payload struct {
		ProtocolVersion string `json:"protocol_version"`
	}
	if err := json.Unmarshal(result.AgentPayload, &payload); err != nil || payload.ProtocolVersion != managedagents.AgentLoopMessageProtocolVersion {
		t.Fatalf("agent payload = %s err = %v", result.AgentPayload, err)
	}
	if legacy.calls != 0 {
		t.Fatalf("legacy runtime calls = %d", legacy.calls)
	}
	state, err := repository.Load(context.Background(), "session_1", "turn_1")
	if err != nil || state.Phase != agentcore.PhaseCompleted {
		t.Fatalf("durable state = %+v err = %v", state, err)
	}
}

func TestAgentRuntimeTurnExecutorConsumesSessionFollowUp(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{
		agentLoopRepo: repository,
		controlEvents: []managedagents.Event{{
			ID: "evt_follow_up", SessionID: "session_1", TurnID: "turn_1", Seq: 20,
			Type:    managedagents.EventUserFollowUp,
			Payload: []byte(`{"content":[{"type":"text","text":"also include verification"}],"turn_id":"turn_1"}`),
		}},
	}
	legacy := &forbiddenRuntime{}
	client := &sequenceLLMClient{responses: []llm.Response{
		{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "initial answer"}}}},
		{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "answer with verification"}}}},
	}}
	executor := AgentRuntimeTurnExecutor{
		Runtime: legacy, CoreRollout: AgentCoreRolloutPolicy{Enabled: true, Percent: 100}, CoreClient: client, Store: store,
		LiveEvents: NewLiveEventBroker(8), Timeout: time.Minute,
	}
	result, err := executor.RunTurn(context.Background(), TurnRequest{
		SessionID: "session_1", TurnID: "turn_1", UserEventSeq: 10,
		UserPayload: []byte(`{"content":[{"type":"text","text":"hello durable core"}]}`),
		LeaseOwner:  "worker_1", Attempt: 1,
	})
	if err != nil || !result.DurableFinalized || result.DurableStatus != "completed" {
		t.Fatalf("RunTurn() result = %+v err = %v", result, err)
	}
	state, loadErr := repository.Load(context.Background(), "session_1", "turn_1")
	if loadErr != nil || state.ControlCursor != 20 || client.Calls() != 2 || legacy.calls != 0 {
		t.Fatalf("state = %+v loadErr = %v modelCalls=%d legacyCalls=%d", state, loadErr, client.Calls(), legacy.calls)
	}
}

func TestAgentRuntimeTurnExecutorResumesRejectedCoreTool(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{agentLoopRepo: repository}
	legacy := &forbiddenRuntime{}
	client := &sequenceLLMClient{responses: []llm.Response{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "requesting write"}}, ToolCalls: []llm.ToolCall{{
				ID: "call_write", Type: "function", Function: llm.ToolCallFunction{Name: "default.write_file", Arguments: []byte(`{"path":"report.txt","content":"blocked"}`)},
			}}},
		},
		{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "write was rejected"}}}},
	}}
	executor := AgentRuntimeTurnExecutor{
		Runtime: legacy, CoreRollout: AgentCoreRolloutPolicy{Enabled: true, Percent: 100}, CoreClient: client, Store: store,
		LiveEvents: NewLiveEventBroker(8), Timeout: time.Minute,
	}
	request := TurnRequest{
		SessionID: "session_1", TurnID: "turn_1", UserEventSeq: 10,
		UserPayload: []byte(`{"content":[{"type":"text","text":"write a report"}]}`),
		LeaseOwner:  "worker_1", Attempt: 1,
	}
	if _, err := executor.RunTurn(context.Background(), request); !errors.Is(err, ErrTurnWaitingApproval) {
		t.Fatalf("first RunTurn() error = %v, want waiting approval", err)
	}
	paused, err := repository.Load(context.Background(), request.SessionID, request.TurnID)
	if err != nil || paused.Phase != agentcore.PhasePaused {
		t.Fatalf("paused state = %+v err = %v", paused, err)
	}
	store.listedInterventions = []managedagents.SessionIntervention{{
		SessionID: request.SessionID, TurnID: request.TurnID, CallID: "call_write",
		ToolIdentifier: "default", APIName: "write_file", Arguments: []byte(`{"path":"report.txt","content":"blocked"}`),
		Kind: managedagents.InterventionKindToolApproval, Status: managedagents.InterventionStatusRejected,
		DecisionReason: "not allowed",
	}}
	request.Attempt = 2
	request.LeaseOwner = "worker_2"
	request.ResumeIntervention = &store.listedInterventions[0]
	executor.CoreRollout.Enabled = false
	executor.Runtime = nil
	result, err := executor.RunTurn(context.Background(), request)
	if err != nil {
		t.Fatalf("resumed RunTurn() error = %v", err)
	}
	if !result.DurableFinalized || result.DurableStatus != "completed" || client.Calls() != 2 || legacy.calls != 0 {
		t.Fatalf("resumed result = %+v model calls = %d legacy calls = %d", result, client.Calls(), legacy.calls)
	}
}

func TestAgentRuntimeTurnExecutorDurablyFailsChangedCoreBinding(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{agentLoopRepo: repository}
	legacy := &forbiddenRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: legacy, CoreRollout: AgentCoreRolloutPolicy{Enabled: true, Percent: 100}, CoreClient: llm.FakeClient{}, Store: store,
		LiveEvents: NewLiveEventBroker(8), Timeout: time.Minute,
	}
	request := TurnRequest{
		SessionID: "session_1", TurnID: "turn_1", UserEventSeq: 10,
		UserPayload: []byte(`{"content":[{"type":"text","text":"hello durable core"}]}`),
		LeaseOwner:  "worker_1", Attempt: 1,
	}
	result, err := executor.RunTurn(context.Background(), request)
	if err != nil || !result.DurableFinalized || result.DurableStatus != "completed" {
		t.Fatalf("initial RunTurn() result = %+v err = %v", result, err)
	}

	state, err := repository.Load(context.Background(), request.SessionID, request.TurnID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.Phase = agentcore.PhaseAwaitingModel
	state.Failure = nil
	state.FeatureState[agentCoreRouteStateKey] = []byte(`{"provider_instance_id":"changed-provider"}`)
	repository.durability = modeltest.NewMemoryDurability(state)
	request.Attempt = 2
	request.LeaseOwner = "worker_2"

	result, err = executor.RunTurn(context.Background(), request)
	if err == nil || !result.DurableFinalized || result.DurableStatus != "failed" {
		t.Fatalf("changed binding result = %+v err = %v", result, err)
	}
	failed, loadErr := repository.Load(context.Background(), request.SessionID, request.TurnID)
	if loadErr != nil || failed.Phase != agentcore.PhaseFailed || failed.Failure == nil || failed.Failure.Code != "runtime_binding_changed" {
		t.Fatalf("failed state = %+v err = %v", failed, loadErr)
	}
	if legacy.calls != 0 {
		t.Fatalf("legacy runtime calls = %d", legacy.calls)
	}
}

func TestWorkerRunnerSkipsLegacyFinalizationForDurableResult(t *testing.T) {
	t.Parallel()

	store := &mockStore{}
	postProcessed := make(chan struct{}, 1)
	runner := NewWorkerRunnerWithConfig(store, durableExecutor{result: TurnResult{
		DurableFinalized: true,
		DurableStatus:    "completed",
		Usage: &managedagents.RecordLLMUsageInput{
			WorkspaceID: "wksp_default", AgentID: "agent_1", AgentConfigVersion: 1,
			ProviderID: "fake", Model: "fake-demo", Status: "completed",
		},
	}}, WorkerRunnerConfig{WorkerCount: 1, WakeBuffer: 1, PostProcess: func(string, string) { postProcessed <- struct{}{} }}, nil)
	defer runner.Close()
	if err := runner.StartTurn(context.Background(), TurnRequest{SessionID: "session_1", TurnID: "turn_1"}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	select {
	case <-postProcessed:
	case <-time.After(time.Second):
		t.Fatal("durable turn was not post-processed")
	}
	if store.completeCalls() != 0 || store.failCalls() != 0 || store.usageCalls() != 1 {
		t.Fatalf("legacy finalization calls: complete=%d fail=%d usage=%d", store.completeCalls(), store.failCalls(), store.usageCalls())
	}
}

func TestWorkerRunnerSkipsLegacyFailureForDurableFailedResult(t *testing.T) {
	t.Parallel()

	store := &mockStore{}
	postProcessed := make(chan struct{}, 1)
	runner := NewWorkerRunnerWithConfig(store, durableExecutor{
		result: TurnResult{
			DurableFinalized: true,
			DurableStatus:    "failed",
			Usage: &managedagents.RecordLLMUsageInput{
				WorkspaceID: "wksp_default", AgentID: "agent_1", AgentConfigVersion: 1,
				ProviderID: "fake", Model: "fake-demo", Status: "failed",
			},
		},
		err: errors.New("durable failure"),
	}, WorkerRunnerConfig{WorkerCount: 1, WakeBuffer: 1, PostProcess: func(string, string) { postProcessed <- struct{}{} }}, nil)
	defer runner.Close()
	if err := runner.StartTurn(context.Background(), TurnRequest{SessionID: "session_1", TurnID: "turn_1"}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	select {
	case <-postProcessed:
	case <-time.After(time.Second):
		t.Fatal("durable failed turn was not post-processed")
	}
	if store.completeCalls() != 0 || store.failCalls() != 0 || store.usageCalls() != 1 {
		t.Fatalf("legacy finalization calls: complete=%d fail=%d usage=%d", store.completeCalls(), store.failCalls(), store.usageCalls())
	}
}

type forbiddenRuntime struct {
	calls int
}

func (r *forbiddenRuntime) RunTurn(context.Context, agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	r.calls++
	return agentruntime.TurnResult{}, errors.New("legacy runtime must not run")
}

type durableExecutor struct {
	result TurnResult
	err    error
}

type sequenceLLMClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     int
}

func (c *sequenceLLMClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) == 0 {
		return llm.Response{}, errors.New("no scripted llm response")
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	c.calls++
	return response, nil
}

func (c *sequenceLLMClient) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (e durableExecutor) RunTurn(context.Context, TurnRequest) (TurnResult, error) {
	return e.result, e.err
}

type lazyStateRepository struct {
	mu         sync.Mutex
	durability *modeltest.MemoryDurability
}

func (r *lazyStateRepository) Load(_ context.Context, sessionID, turnID string) (agentcore.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.durability == nil {
		return agentcore.State{}, managedagents.ErrNotFound
	}
	state := r.durability.Snapshot()
	if state.SessionID != sessionID || state.TurnID != turnID {
		return agentcore.State{}, managedagents.ErrNotFound
	}
	return state, nil
}

func (r *lazyStateRepository) Commit(ctx context.Context, transition agentcore.Transition) (agentcore.State, error) {
	durability, err := r.forTransition(transition)
	if err != nil {
		return agentcore.State{}, err
	}
	return durability.Commit(ctx, transition)
}

func (r *lazyStateRepository) Park(ctx context.Context, transition agentcore.ParkTransition) (agentcore.State, error) {
	durability, err := r.forTransition(transition.Transition)
	if err != nil {
		return agentcore.State{}, err
	}
	return durability.Park(ctx, transition)
}

func (r *lazyStateRepository) Complete(ctx context.Context, transition agentcore.CompleteTransition) (agentcore.State, error) {
	durability, err := r.forTransition(transition.Transition)
	if err != nil {
		return agentcore.State{}, err
	}
	return durability.Complete(ctx, transition)
}

func (r *lazyStateRepository) Fail(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	durability, err := r.forTransition(transition.Transition)
	if err != nil {
		return agentcore.State{}, err
	}
	return durability.Fail(ctx, transition)
}

func (r *lazyStateRepository) Cancel(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	durability, err := r.forTransition(transition.Transition)
	if err != nil {
		return agentcore.State{}, err
	}
	return durability.Cancel(ctx, transition)
}

func (r *lazyStateRepository) forTransition(transition agentcore.Transition) (*modeltest.MemoryDurability, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.durability == nil {
		if transition.ExpectedRevision != 0 || transition.Next.Phase != agentcore.PhaseAwaitingModel {
			return nil, errors.New("first transition must initialize awaiting_model")
		}
		initial := transition.Next.Clone()
		initial.Phase = agentcore.PhasePreparing
		initial.Revision = 0
		r.durability = modeltest.NewMemoryDurability(initial)
	}
	return r.durability, nil
}
