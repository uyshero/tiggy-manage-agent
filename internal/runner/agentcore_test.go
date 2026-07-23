package runner

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/modeltest"
)

func TestAgentRuntimeTurnExecutorRunsDurableCore(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{agentLoopRepo: repository}
	executor := AgentRuntimeTurnExecutor{
		CoreClient: llm.FakeClient{}, Store: store,
		LiveEvents: NewLiveEventBroker(8), Timeout: time.Minute,
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
	state, err := repository.Load(context.Background(), "session_1", "turn_1")
	if err != nil || state.Phase != agentcore.PhaseCompleted {
		t.Fatalf("durable state = %+v err = %v", state, err)
	}
}

func TestAgentCoreVisionFallbackProducesDurableSupplement(t *testing.T) {
	t.Parallel()

	client := &sequenceLLMClient{responses: []llm.Response{{
		Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "invoice total is 42"}}},
		Usage:   llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}}}
	executor := AgentRuntimeTurnExecutor{CoreClient: client}
	runtimeRequest := agentruntime.TurnRequest{
		ImageParts: []llm.ContentPart{{Type: "image_url", ImageURL: &llm.ImageURL{URL: "data:image/png;base64,AA=="}}},
		Config:     agentruntime.Config{VisionLLMAPIKey: "vision-secret"},
	}
	prepared, usage, err := executor.prepareAgentCoreVision(context.Background(), runtimeRequest, managedagents.AgentRuntimeConfig{
		LLMCapabilityType:     managedagents.LLMModelCapabilityText,
		VisionLLMProvider:     "vision-provider",
		VisionLLMProviderType: llm.ProviderFake,
		VisionLLMModel:        "vision-model",
	})
	if err != nil {
		t.Fatalf("prepareAgentCoreVision() error = %v", err)
	}
	if len(prepared.ImageParts) != 0 || prepared.CurrentUserSupplement != "Vision model analysis of the uploaded image(s):\ninvoice total is 42" {
		t.Fatalf("prepared request = %+v", prepared)
	}
	if usage.TotalTokens != 15 || usage.Source != "provider" {
		t.Fatalf("usage = %+v", usage)
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
	client := &sequenceLLMClient{responses: []llm.Response{
		{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "initial answer"}}}},
		{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "answer with verification"}}}},
	}}
	executor := AgentRuntimeTurnExecutor{
		CoreClient: client, Store: store,
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
	if loadErr != nil || state.ControlCursor != 20 || client.Calls() != 2 {
		t.Fatalf("state = %+v loadErr = %v modelCalls=%d", state, loadErr, client.Calls())
	}
}

func TestAgentRuntimeTurnExecutorResumesRejectedCoreTool(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{agentLoopRepo: repository}
	client := &sequenceLLMClient{responses: []llm.Response{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "requesting write"}}, ToolCalls: []llm.ToolCall{{
				ID: "call_write", Type: "function", Function: llm.ToolCallFunction{Name: "default_write_file", Arguments: []byte(`{"path":"report.txt","content":"blocked"}`)},
			}}},
		},
		{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "write was rejected"}}}},
	}}
	executor := AgentRuntimeTurnExecutor{
		CoreClient: client, Store: store,
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
	result, err := executor.RunTurn(context.Background(), request)
	if err != nil {
		t.Fatalf("resumed RunTurn() error = %v", err)
	}
	if !result.DurableFinalized || result.DurableStatus != "completed" || client.Calls() != 2 {
		t.Fatalf("resumed result = %+v model calls = %d", result, client.Calls())
	}
}

func TestAgentCoreInteractionDecisionStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		kind         string
		status       string
		wantStatus   string
		wantResolved bool
	}{
		{name: "tool approved", kind: managedagents.InterventionKindToolApproval, status: managedagents.InterventionStatusApproved, wantStatus: managedagents.InterventionStatusApproved, wantResolved: true},
		{name: "tool rejected", kind: managedagents.InterventionKindToolApproval, status: managedagents.InterventionStatusRejected, wantStatus: managedagents.InterventionStatusRejected, wantResolved: true},
		{name: "plan approved", kind: managedagents.InterventionKindPlanApproval, status: managedagents.InterventionStatusApproved, wantStatus: managedagents.InterventionStatusApproved, wantResolved: true},
		{name: "clarification answered", kind: managedagents.InterventionKindClarification, status: managedagents.InterventionStatusAnswered, wantStatus: managedagents.InterventionStatusApproved, wantResolved: true},
		{name: "clarification skipped", kind: managedagents.InterventionKindClarification, status: managedagents.InterventionStatusSkipped, wantStatus: managedagents.InterventionStatusRejected, wantResolved: true},
		{name: "upload canceled", kind: managedagents.InterventionKindUploadRequest, status: managedagents.InterventionStatusCanceled, wantStatus: managedagents.InterventionStatusRejected, wantResolved: true},
		{name: "upload expired", kind: managedagents.InterventionKindUploadRequest, status: managedagents.InterventionStatusExpired, wantStatus: managedagents.InterventionStatusRejected, wantResolved: true},
		{name: "pending", kind: managedagents.InterventionKindClarification, status: managedagents.InterventionStatusPending},
		{name: "invalid cross-kind status", kind: managedagents.InterventionKindToolApproval, status: managedagents.InterventionStatusAnswered},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotStatus, gotResolved := agentCoreInteractionDecisionStatus(managedagents.SessionIntervention{Kind: test.kind, Status: test.status})
			if gotStatus != test.wantStatus || gotResolved != test.wantResolved {
				t.Fatalf("agentCoreInteractionDecisionStatus() = (%q, %t), want (%q, %t)", gotStatus, gotResolved, test.wantStatus, test.wantResolved)
			}
		})
	}
}

func TestAgentCoreResumeDecisionsUsesReconciliationInteractionID(t *testing.T) {
	t.Parallel()

	interactionID := agentcore.ToolReconciliationRequestPurpose + ":call_write"
	response := json.RawMessage(`{"outcome":"executed","summary":"external transaction exists"}`)
	store := &mockStore{listedInterventions: []managedagents.SessionIntervention{{
		SessionID: "session_1", TurnID: "turn_1", CallID: interactionID,
		Kind: managedagents.InterventionKindClarification, Status: managedagents.InterventionStatusAnswered,
		Response: response,
	}}}
	executor := AgentRuntimeTurnExecutor{Store: store}
	state := agentcore.State{Pause: &agentcore.PauseState{Interactions: []agentcore.RequiredInteraction{{
		ID: interactionID, Kind: managedagents.InterventionKindClarification, CallID: "call_write",
		Request: json.RawMessage(`{"purpose":"tool_reconciliation"}`),
	}}}}
	decisions, err := executor.agentCoreResumeDecisions(context.Background(), TurnRequest{SessionID: "session_1", TurnID: "turn_1"}, state)
	if err != nil || len(decisions) != 1 || decisions[0].InteractionID != interactionID || decisions[0].Status != managedagents.InterventionStatusApproved || string(decisions[0].Response) != string(response) {
		t.Fatalf("agentCoreResumeDecisions() = %+v err=%v", decisions, err)
	}
}

func TestAgentRuntimeTurnExecutorDurablyFailsChangedCoreBinding(t *testing.T) {
	t.Parallel()

	repository := &lazyStateRepository{}
	store := &mockStore{agentLoopRepo: repository}
	executor := AgentRuntimeTurnExecutor{
		CoreClient: llm.FakeClient{}, Store: store,
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
