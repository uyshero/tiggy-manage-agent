package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modeltest"
)

func TestPostgresAgentLoopCompletesAndLoadsDurableState(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"run core"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		t.Fatalf("scope context: %v", err)
	}
	fence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-complete")
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: model.Response{
		Message:    model.Message{ID: "answer_1", Content: []model.Content{{Type: model.ContentText, Text: "done"}}},
		StopReason: model.StopReasonComplete,
		Usage:      model.Usage{OutputTokens: 2, TotalTokens: 2, Source: model.UsageSourceProvider},
	}})
	engine := newPostgresAgentLoopEngine(t, store, fence, modelPort, nil)

	outcome, err := engine.Run(ctx, state)
	if err != nil {
		t.Fatalf("run agent loop: %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("agent loop status = %q", outcome.Status)
	}
	loaded, err := store.LoadAgentLoopStateContext(ctx, session.ID, started.Run.ID)
	if err != nil {
		t.Fatalf("load agent loop state: %v", err)
	}
	if loaded.Phase != agentcore.PhaseCompleted || loaded.Revision != outcome.State.Revision || loaded.Usage.OutputTokens != 2 {
		t.Fatalf("loaded state = %+v", loaded)
	}
	if _, err := store.LoadAgentLoopStateContext(context.Background(), session.ID, started.Run.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unscoped load error = %v, want forbidden", err)
	}
	turnStatus, _ := postgresTurnState(t, store, session.ID, started.Run.ID)
	if turnStatus != TurnStatusCompleted {
		t.Fatalf("turn status = %q", turnStatus)
	}
	assertPostgresSessionStatus(t, store, session.ID, SessionStatusIdle)
}

func TestPostgresAgentLoopParksAndResumesThroughIntervention(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"write"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		t.Fatalf("scope context: %v", err)
	}
	fence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-park")
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	call := model.ToolCall{ID: "call_write", Name: "default.write_file", Arguments: json.RawMessage(`{"path":"report.txt"}`)}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: model.Response{
			Message:    model.Message{ID: "tool_request", Content: []model.Content{{Type: model.ContentToolCall, ToolCall: &call}}},
			StopReason: model.StopReasonToolCall,
		}},
		modeltest.ModelStep{Response: model.Response{
			Message:    model.Message{ID: "answer_2", Content: []model.Content{{Type: model.ContentText, Text: "approved write complete"}}},
			StopReason: model.StopReasonComplete,
		}},
	)
	toolPort := &modeltest.ScriptedTools{PreflightFunc: func(_ context.Context, _ agentcore.State, calls []model.ToolCall) (agentcore.ToolBatchPlan, error) {
		return agentcore.ToolBatchPlan{
			Calls:        []agentcore.PlannedToolCall{{Call: calls[0], ExecutionMode: "sequential", SideEffect: "write", Idempotency: "unknown"}},
			Interactions: []agentcore.RequiredInteraction{{ID: "approval_1", Kind: "tool_approval", CallID: calls[0].ID, Request: json.RawMessage(`{"risk":"write"}`)}},
		}, nil
	}}
	engine := newPostgresAgentLoopEngine(t, store, fence, modelPort, toolPort)

	paused, err := engine.Run(ctx, state)
	if err != nil {
		t.Fatalf("run to pause: %v", err)
	}
	if paused.Status != agentcore.OutcomePaused {
		t.Fatalf("pause status = %q", paused.Status)
	}
	turnStatus, _ := postgresTurnState(t, store, session.ID, started.Run.ID)
	if turnStatus != TurnStatusWaitingApproval {
		t.Fatalf("parked turn status = %q", turnStatus)
	}
	interventions, err := store.ListSessionInterventionsContext(ctx, session.ID, InterventionStatusPending)
	if err != nil || len(interventions) != 1 || interventions[0].CallID != call.ID {
		t.Fatalf("pending interventions = %+v err = %v", interventions, err)
	}
	if _, err := store.DecideSessionInterventionContext(ctx, session.ID, DecideSessionInterventionInput{
		TurnID: started.Run.ID, CallID: call.ID, Status: InterventionStatusApproved, DecisionReason: "approved by integration test",
	}); err != nil {
		t.Fatalf("approve intervention: %v", err)
	}
	resumeFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-resume")
	engine = newPostgresAgentLoopEngine(t, store, resumeFence, modelPort, toolPort)
	resumed, err := engine.Resume(ctx, paused.State, []agentcore.InteractionDecision{{InteractionID: "approval_1", Status: "approved"}})
	if err != nil {
		t.Fatalf("resume agent loop: %v", err)
	}
	completed, err := engine.Run(ctx, resumed)
	if err != nil {
		t.Fatalf("run resumed agent loop: %v", err)
	}
	if completed.Status != agentcore.OutcomeCompleted {
		t.Fatalf("completed status = %q", completed.Status)
	}
	if _, execute := toolPort.Counts(); execute != 1 {
		t.Fatalf("tool execute count = %d", execute)
	}
}

func TestPostgresAgentLoopRejectsStaleRevision(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"revision"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	fence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-revision")
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	next := state.Clone()
	next.Phase = agentcore.PhaseAwaitingModel
	transition := agentcore.Transition{ExpectedRevision: 0, Next: next}
	durability := store.AgentLoopDurability(fence)
	committed, err := durability.Commit(ctx, transition)
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	if _, err := durability.Commit(ctx, transition); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale commit error = %v, want revision conflict", err)
	}
	leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-new-worker")
	next = committed.Clone()
	next.ControlCursor = 1
	if _, err := durability.Commit(ctx, agentcore.Transition{ExpectedRevision: committed.Revision, Next: next}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired worker commit error = %v, want lease lost", err)
	}
}

func TestPostgresAgentLoopDoesNotReplayStartedNonIdempotentTool(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"write once"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	oldFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-crashed-worker")
	durability := store.AgentLoopDurability(oldFence)
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)

	state.Phase = agentcore.PhaseAwaitingModel
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: state})
	if err != nil {
		t.Fatalf("commit awaiting model: %v", err)
	}
	call := model.ToolCall{ID: "call_once", Name: "integration.write_once", Arguments: json.RawMessage(`{"value":"once"}`)}
	planned := agentcore.PlannedToolCall{
		Call: call, ExecutionMode: "sequential", SideEffect: "write", Idempotency: "unknown",
		IdempotencyKey: agentcore.StableToolIdempotencyKey(session.ID, started.Run.ID, call),
	}
	next := state.Clone()
	next.Messages = append(next.Messages, model.Message{
		ID: "assistant_tools", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
		Content: []model.Content{{Type: model.ContentToolCall, ToolCall: &call}},
	})
	next.PendingToolBatch = &agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{planned}}
	next.Phase = agentcore.PhasePreflightingTools
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: state.Revision, Next: next})
	if err != nil {
		t.Fatalf("commit preflight state: %v", err)
	}
	next = state.Clone()
	next.Phase = agentcore.PhaseExecutingTools
	next.ToolCalls = 1
	next.Budget.ToolCalls = 1
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: state.Revision, Next: next})
	if err != nil {
		t.Fatalf("commit executing state: %v", err)
	}
	next = state.Clone()
	next.ToolJournal = []agentcore.ToolCallJournalEntry{{
		CallID: call.ID, Name: call.Name, Idempotency: planned.Idempotency, IdempotencyKey: planned.IdempotencyKey,
		Status: agentcore.ToolCallStarted, Attempt: 1, StartedAt: time.Now().UTC(),
	}}
	state, err = durability.Commit(ctx, agentcore.Transition{
		ExpectedRevision: state.Revision, Next: next,
		Events: []agentcore.RuntimeEvent{{Type: agentcore.EventToolCallStarted, Message: "Tool call started before worker crash."}},
	})
	if err != nil {
		t.Fatalf("commit started journal: %v", err)
	}

	resumeFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-recovery-worker")
	toolPort := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		return agentcore.ToolBatchResult{}, errors.New("non-idempotent tool must not be replayed")
	}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: model.Response{
		Message:    model.Message{ID: "answer_recovered", Content: []model.Content{{Type: model.ContentText, Text: "manual verification required"}}},
		StopReason: model.StopReasonComplete,
	}})
	engine := newPostgresAgentLoopEngine(t, store, resumeFence, modelPort, toolPort)
	outcome, err := engine.Run(ctx, state)
	if err != nil {
		t.Fatalf("recover agent loop: %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || len(outcome.State.ToolJournal) != 1 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallIndeterminate {
		t.Fatalf("recovered outcome = %+v", outcome)
	}
	if _, execute := toolPort.Counts(); execute != 0 {
		t.Fatalf("non-idempotent replay count = %d", execute)
	}
}

func TestPostgresSessionControlsBindToRunningTurn(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"start"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	events, err := AppendEventsWithContext(ctx, store, session.ID, []AppendEventInput{
		{Type: EventUserSteer, Payload: json.RawMessage(`{"content":[{"type":"text","text":"focus"}]}`)},
		{Type: EventUserFollowUp, Payload: json.RawMessage(`{"content":[{"type":"text","text":"include tests"}]}`)},
	})
	if err != nil {
		t.Fatalf("append controls: %v", err)
	}
	if len(events) != 2 || events[0].TurnID != started.Run.ID || events[1].TurnID != started.Run.ID {
		t.Fatalf("control events = %+v", events)
	}
	controls, err := store.ListSessionTurnControlEventsContext(ctx, session.ID, started.Run.ID, events[0].Seq-1)
	if err != nil {
		t.Fatalf("list controls: %v", err)
	}
	if len(controls) != 2 || controls[0].Type != EventUserSteer || controls[1].Type != EventUserFollowUp {
		t.Fatalf("listed controls = %+v", controls)
	}
}

func TestPostgresAgentLoopPersistsCancelAfterTurnInterrupt(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"cancel"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	fence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-cancel")
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	state.Phase = agentcore.PhaseAwaitingModel
	state, err = store.AgentLoopDurability(fence).Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: state})
	if err != nil {
		t.Fatalf("commit awaiting state: %v", err)
	}
	if _, err := AppendEventsWithContext(ctx, store, session.ID, []AppendEventInput{{Type: EventUserInterrupt}}); err != nil {
		t.Fatalf("interrupt turn: %v", err)
	}
	engine := newPostgresAgentLoopEngine(t, store, fence, modeltest.NewScriptedModel(), nil)
	canceledContext, cancel := context.WithCancel(ctx)
	cancel()
	outcome, err := engine.Run(canceledContext, state)
	if err != nil {
		t.Fatalf("cancel agent loop: %v", err)
	}
	if outcome.Status != agentcore.OutcomeCanceled || outcome.State.Phase != agentcore.PhaseCanceled {
		t.Fatalf("canceled outcome = %+v", outcome)
	}
	loaded, err := store.LoadAgentLoopStateContext(ctx, session.ID, started.Run.ID)
	if err != nil || loaded.Phase != agentcore.PhaseCanceled {
		t.Fatalf("loaded canceled state = %+v err = %v", loaded, err)
	}
}

func newPostgresAgentLoopIntegrationStore(t *testing.T) *PostgresStore {
	t.Helper()
	store := newPostgresIntegrationStore(t)
	var table sql.NullString
	if err := store.db.QueryRowContext(t.Context(), `SELECT to_regclass('public.agent_loop_states')`).Scan(&table); err != nil {
		t.Fatalf("check agent loop schema: %v", err)
	}
	if !table.Valid || table.String == "" {
		t.Fatal("agent_loop_states table missing; apply migration 000083")
	}
	return store
}

func postgresAgentLoopInitialState(sessionID, turnID string) agentcore.State {
	state := agentcore.NewState(sessionID, turnID, agentcore.Budget{
		MaxRounds: 8, MaxModelCalls: 8, MaxToolCalls: 8,
		MaxInputTokens: 10_000, MaxOutputTokens: 10_000, MaxReasoningTokens: 10_000, MaxCostMicros: 1_000_000,
		Deadline: time.Now().UTC().Add(time.Hour),
	})
	state.Messages = []model.Message{{
		ID: "user_1", Role: model.RoleUser, Visibility: model.VisibilityPublic,
		Content: []model.Content{{Type: model.ContentText, Text: "run integration"}},
	}}
	return state
}

func newPostgresAgentLoopEngine(t *testing.T, store *PostgresStore, fence AgentLoopFence, modelPort agentcore.ModelPort, toolPort agentcore.ToolPort) *agentcore.Engine {
	t.Helper()
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelPort,
		Context: modeltest.StaticContext{
			Route:           model.Route{ProviderInstanceID: "fake", ProviderConfigVersion: 1, ModelID: "test-model", CatalogRevision: "integration"},
			MaxOutputTokens: 128,
		},
		Tools: toolPort, Durability: store.AgentLoopDurability(fence), Clock: modeltest.FixedClock{Time: time.Now().UTC()}, IDs: modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("new agent loop engine: %v", err)
	}
	return engine
}

func leasePostgresAgentLoopTurn(t *testing.T, store *PostgresStore, sessionID, turnID, owner string) AgentLoopFence {
	t.Helper()
	var attempt int
	err := store.db.QueryRowContext(t.Context(), `
		UPDATE session_turns
		SET lease_owner = $3,
			lease_expires_at = CURRENT_TIMESTAMP + interval '1 hour',
			last_heartbeat_at = CURRENT_TIMESTAMP,
			attempt_count = attempt_count + 1
		WHERE session_id = $1 AND id = $2 AND status = 'running'
		RETURNING attempt_count
	`, sessionID, turnID, owner).Scan(&attempt)
	if err != nil {
		t.Fatalf("lease agent loop turn: %v", err)
	}
	return AgentLoopFence{LeaseOwner: owner, Attempt: attempt}
}
