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

func TestPostgresToolPermissionAuditProjectsAndPaginatesDurableEvents(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	var table sql.NullString
	if err := store.db.QueryRowContext(t.Context(), `SELECT to_regclass('public.tool_permission_audit_records')`).Scan(&table); err != nil || !table.Valid {
		t.Fatalf("tool permission audit table: table=%v err=%v; apply migration 000085", table, err)
	}
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"audit"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	fence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-permission-audit")
	durability := store.AgentLoopDurability(fence)
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	state.Phase = agentcore.PhaseAwaitingModel
	plan := agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{
		{
			Call:            model.ToolCall{ID: "call_ask", Name: "default_edit_file", Arguments: json.RawMessage(`{"path":"/workspace/src/main.go"}`)},
			Disposition:     agentcore.ToolDispositionExecute,
			ValidationState: agentcore.ToolValidationValid,
			ApprovalState:   agentcore.ToolApprovalPending,
			ApprovalSource:  agentcore.ToolApprovalSourceHuman,
			Permission: &agentcore.ToolPermissionDecision{
				Decision: "ask", Required: true, Mode: "request_approval", ApprovalPolicy: "conditional",
				MatchedRuleID: "ask-src", RuleSource: "session", Risk: "write",
			},
		},
		{
			Call:            model.ToolCall{ID: "call_deny", Name: "default_edit_file", Arguments: json.RawMessage(`{"path":"/workspace/secrets/token"}`)},
			Disposition:     agentcore.ToolDispositionDenied,
			ValidationState: agentcore.ToolValidationValid,
			ApprovalState:   agentcore.ToolApprovalNotRequired,
			Permission: &agentcore.ToolPermissionDecision{
				Decision: "deny", Mode: "full_access", ApprovalPolicy: "conditional",
				MatchedRuleID: "deny-secrets", RuleSource: "workspace", Risk: "write",
			},
		},
	}}
	state, err = durability.Commit(ctx, agentcore.Transition{
		ExpectedRevision: 0, Next: state,
		Events: []agentcore.RuntimeEvent{
			{Type: agentcore.EventToolBatchPlanned, Payload: plan},
			{Type: agentcore.EventToolCallStarted, Payload: agentcore.ToolCallJournalEntry{CallID: "call_ask", Status: agentcore.ToolCallStarted}},
			{Type: agentcore.EventToolCallStarted, Payload: agentcore.ToolCallJournalEntry{CallID: "call_deny", Status: agentcore.ToolCallStarted}},
		},
	})
	if err != nil {
		t.Fatalf("commit permission plan: %v", err)
	}
	next := state.Clone()
	state, err = durability.Commit(ctx, agentcore.Transition{
		ExpectedRevision: state.Revision, Next: next,
		Events: []agentcore.RuntimeEvent{
			{Type: agentcore.EventInterventionResolved, Payload: []agentcore.InteractionDecision{{InteractionID: "tool_approval:call_ask", Status: "approved"}}},
			{Type: agentcore.EventToolCallResult, Payload: agentcore.ToolCallJournalEntry{CallID: "call_ask", Status: agentcore.ToolCallSucceeded}},
			{Type: agentcore.EventToolCallResult, Payload: agentcore.ToolCallJournalEntry{CallID: "call_deny", Status: agentcore.ToolCallFailed}},
		},
	})
	if err != nil {
		t.Fatalf("commit permission outcomes: %v", err)
	}

	records, err := store.ListToolPermissionAuditContext(ctx, ListToolPermissionAuditInput{SessionID: session.ID, Limit: 2})
	if err != nil || len(records) != 2 {
		t.Fatalf("list permission audit: records=%+v err=%v", records, err)
	}
	byCall := map[string]ToolPermissionAuditRecord{records[0].CallID: records[0], records[1].CallID: records[1]}
	if ask := byCall["call_ask"]; ask.ApprovalStatus != "approved" || ask.ExecutionStatus != "succeeded" || ask.MatchedRuleID != "ask-src" {
		t.Fatalf("ask audit = %+v", ask)
	}
	if deny := byCall["call_deny"]; deny.ExecutionStatus != "denied" || deny.RuleSource != "workspace" {
		t.Fatalf("deny audit = %+v", deny)
	}
	first, err := store.ListToolPermissionAuditContext(ctx, ListToolPermissionAuditInput{SessionID: session.ID, Limit: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("first permission audit page: records=%+v err=%v", first, err)
	}
	second, err := store.ListToolPermissionAuditContext(ctx, ListToolPermissionAuditInput{
		SessionID: session.ID, Limit: 1, Before: &first[0].CreatedAt,
		BeforeTurnID: first[0].TurnID, BeforeCallID: first[0].CallID,
	})
	if err != nil || len(second) != 1 || second[0].CallID == first[0].CallID {
		t.Fatalf("second permission audit page: first=%+v second=%+v err=%v", first, second, err)
	}
	otherWorkspaceCtx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: "wksp_other", OwnerID: session.OwnerID})
	if _, err := store.ListToolPermissionAuditContext(otherWorkspaceCtx, ListToolPermissionAuditInput{SessionID: session.ID, Limit: 1}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-workspace permission audit error = %v, want forbidden", err)
	}
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
	call := model.ToolCall{ID: "call_write", Name: "default_write_file", Arguments: json.RawMessage(`{"path":"report.txt"}`)}
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
			Calls: []agentcore.PlannedToolCall{{
				Call: calls[0], ExecutionMode: "sequential", SideEffect: "write", Idempotency: "unknown",
				Disposition: agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
				ApprovalState: agentcore.ToolApprovalPending, ApprovalSource: agentcore.ToolApprovalSourceHuman,
			}},
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
		t.Fatalf("stale initial commit error = %v, want revision conflict", err)
	}
	next = committed.Clone()
	next.ControlCursor = 1
	advanced, err := durability.Commit(ctx, agentcore.Transition{ExpectedRevision: committed.Revision, Next: next})
	if err != nil {
		t.Fatalf("fast commit: %v", err)
	}
	if advanced.Revision != committed.Revision+1 || advanced.ControlCursor != 1 {
		t.Fatalf("fast committed state = %+v", advanced)
	}
	if _, err := durability.Commit(ctx, agentcore.Transition{ExpectedRevision: committed.Revision, Next: next}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale fast commit error = %v, want revision conflict", err)
	}
	invalid := advanced.Clone()
	invalid.Phase = agentcore.PhaseExecutingTools
	if _, err := durability.Commit(ctx, agentcore.Transition{ExpectedRevision: advanced.Revision, Next: invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid fast phase error = %v, want invalid", err)
	}
	leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-new-worker")
	next = advanced.Clone()
	next.ControlCursor = 2
	if _, err := durability.Commit(ctx, agentcore.Transition{ExpectedRevision: advanced.Revision, Next: next}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired worker commit error = %v, want lease lost", err)
	}
}

func TestPostgresAgentLoopDurabilityStateOwnershipContract(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"ownership"}]}`),
	})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		t.Fatalf("scope context: %v", err)
	}
	durability := store.AgentLoopDurability(leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "ownership-contract"))
	next := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	next.Phase = agentcore.PhaseAwaitingModel
	originalText := next.Messages[0].Content[0].Text
	committed, err := durability.Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: next})
	if err != nil {
		t.Fatalf("commit ownership state: %v", err)
	}
	if next.Revision != 0 {
		t.Fatalf("commit mutated input revision to %d", next.Revision)
	}

	committed.Messages[0].Content[0].Text = "mutated returned state"
	if next.Messages[0].Content[0].Text != originalText {
		t.Fatal("returned state aliases transition input")
	}
	next.Messages[0].Content[0].Text = "mutated transition input"
	loaded, err := durability.Load(ctx, session.ID, started.Run.ID)
	if err != nil {
		t.Fatalf("load ownership state: %v", err)
	}
	if loaded.Messages[0].Content[0].Text != originalText {
		t.Fatal("durable state aliases transition input or returned state")
	}
}

func TestPostgresAgentLoopFastCommitDoesNotWaitForSessionRowLock(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"avoid session lock"}]}`),
	})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	fence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-lock-free")
	durability := store.AgentLoopDurability(fence)
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	state.Phase = agentcore.PhaseAwaitingModel
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: state})
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	blocker, err := store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var lockedSessionID string
	if err := blocker.QueryRowContext(t.Context(), `SELECT id FROM sessions WHERE id = $1 FOR NO KEY UPDATE`, session.ID).Scan(&lockedSessionID); err != nil {
		t.Fatalf("lock session row: %v", err)
	}

	next := state.Clone()
	next.ControlCursor = 1
	commitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	committed, err := durability.Commit(commitCtx, agentcore.Transition{
		ExpectedRevision: state.Revision,
		Next:             next,
		Events: []agentcore.RuntimeEvent{{
			Type: agentcore.EventModelRequested, Message: "Fast commit while session metadata is locked.",
		}},
	})
	if err != nil {
		t.Fatalf("fast commit waited for sessions row lock: %v", err)
	}
	if committed.Revision != state.Revision+1 || committed.ControlCursor != 1 {
		t.Fatalf("fast committed state = %+v", committed)
	}

	var counterSeq, eventSeq int64
	if err := store.db.QueryRowContext(t.Context(), `SELECT last_seq FROM session_event_counters WHERE session_id = $1`, session.ID).Scan(&counterSeq); err != nil {
		t.Fatalf("load event counter: %v", err)
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT COALESCE(MAX(seq), 0) FROM session_events WHERE session_id = $1`, session.ID).Scan(&eventSeq); err != nil {
		t.Fatalf("load latest event seq: %v", err)
	}
	if counterSeq != eventSeq {
		t.Fatalf("event counter = %d, latest event seq = %d", counterSeq, eventSeq)
	}
}

func TestPostgresSessionEventBatchPreservesOrderAndRollbackLeavesNoGap(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		t.Fatalf("scope context: %v", err)
	}
	latestSeq := func() int64 {
		var seq int64
		if err := store.db.QueryRowContext(t.Context(), `SELECT COALESCE(MAX(seq), 0) FROM session_events WHERE session_id = $1`, session.ID).Scan(&seq); err != nil {
			t.Fatalf("latest session event seq: %v", err)
		}
		return seq
	}
	initialSeq := latestSeq()
	now := time.Now().UTC().Truncate(time.Microsecond)
	inputs := []sessionEventAppend{
		{Type: "batch.first", Payload: json.RawMessage(`{"position":1}`)},
		{Type: "batch.second", Payload: json.RawMessage(`{"position":2}`)},
		{Type: "batch.third", Payload: json.RawMessage(`{"position":3}`)},
	}

	rollbackTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setDatabaseAccessScope(ctx, rollbackTx, session.WorkspaceID); err != nil {
		rollbackTx.Rollback()
		t.Fatal(err)
	}
	rolledBack, err := store.appendEventsTx(ctx, rollbackTx, session.ID, inputs, now)
	if err != nil {
		rollbackTx.Rollback()
		t.Fatalf("append rollback batch: %v", err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("rollback batch: %v", err)
	}
	if latestSeq() != initialSeq {
		t.Fatalf("rolled back batch advanced latest seq from %d to %d", initialSeq, latestSeq())
	}

	commitTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer commitTx.Rollback()
	if _, err := setDatabaseAccessScope(ctx, commitTx, session.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	committed, err := store.appendEventsTx(ctx, commitTx, session.ID, inputs, now)
	if err != nil {
		t.Fatalf("append committed batch: %v", err)
	}
	if err := commitTx.Commit(); err != nil {
		t.Fatalf("commit batch: %v", err)
	}
	for index := range committed {
		expectedSeq := initialSeq + int64(index) + 1
		if committed[index].Seq != expectedSeq || committed[index].Type != inputs[index].Type {
			t.Fatalf("committed event %d = %+v, want seq=%d type=%s", index, committed[index], expectedSeq, inputs[index].Type)
		}
		if rolledBack[index].Seq != committed[index].Seq {
			t.Fatalf("rollback seq %d = %d, committed seq = %d", index, rolledBack[index].Seq, committed[index].Seq)
		}
	}
	if latestSeq() != initialSeq+int64(len(inputs)) {
		t.Fatalf("latest seq = %d, want %d", latestSeq(), initialSeq+int64(len(inputs)))
	}
	rows, err := store.db.QueryContext(t.Context(), `
		SELECT seq, type
		FROM session_events
		WHERE session_id = $1 AND seq > $2
		ORDER BY seq
	`, session.ID, initialSeq)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var seq int64
		var eventType string
		if err := rows.Scan(&seq, &eventType); err != nil {
			t.Fatal(err)
		}
		if index >= len(inputs) || seq != initialSeq+int64(index)+1 || eventType != inputs[index].Type {
			t.Fatalf("stored event %d has seq=%d type=%s", index, seq, eventType)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(inputs) {
		t.Fatalf("stored event count = %d, want %d", index, len(inputs))
	}
}

func TestPostgresAgentLoopRecoversStartedToolsByIdempotency(t *testing.T) {
	tests := []struct {
		idempotency string
		replayable  bool
	}{
		{idempotency: "safe", replayable: true},
		{idempotency: "keyed", replayable: true},
		{idempotency: "unsafe", replayable: false},
	}
	for _, test := range tests {
		t.Run(test.idempotency, func(t *testing.T) {
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
			call := model.ToolCall{ID: "call_once", Name: "integration_write_once", Arguments: json.RawMessage(`{"value":"once"}`)}
			planned := agentcore.PlannedToolCall{
				Call: call, ExecutionMode: "sequential", SideEffect: "write", Idempotency: test.idempotency,
				IdempotencyKey: agentcore.StableToolIdempotencyKey(session.ID, started.Run.ID, call),
				Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
				ApprovalState: agentcore.ToolApprovalNotRequired,
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
			toolPort := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
				if !test.replayable {
					return agentcore.ToolBatchResult{}, errors.New("non-idempotent tool must not be replayed")
				}
				if plan.Calls[0].IdempotencyKey != planned.IdempotencyKey {
					return agentcore.ToolBatchResult{}, errors.New("replayed tool changed its idempotency key")
				}
				return agentcore.ToolBatchResult{Results: []model.ToolResult{{
					CallID: call.ID, Name: call.Name, Content: []model.Content{{Type: model.ContentText, Text: "ok"}},
				}}}, nil
			}}
			modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: model.Response{
				Message:    model.Message{ID: "answer_recovered", Content: []model.Content{{Type: model.ContentText, Text: "recovery complete"}}},
				StopReason: model.StopReasonComplete,
			}})
			engine := newPostgresAgentLoopEngine(t, store, resumeFence, modelPort, toolPort)
			outcome, err := engine.Run(ctx, state)
			if err != nil {
				t.Fatalf("recover agent loop: %v", err)
			}
			_, execute := toolPort.Counts()
			if test.replayable {
				if outcome.Status != agentcore.OutcomeCompleted || len(outcome.State.ToolJournal) != 1 {
					t.Fatalf("recovered outcome = %+v", outcome)
				}
				journal := outcome.State.ToolJournal[0]
				if execute != 1 || journal.Status != agentcore.ToolCallSucceeded || journal.Attempt != 2 || journal.IdempotencyKey != planned.IdempotencyKey {
					t.Fatalf("replayed tool execute=%d journal=%+v", execute, journal)
				}
				return
			}
			if outcome.Status != agentcore.OutcomePaused || outcome.Pause == nil || len(outcome.Pause.Interactions) != 1 || execute != 0 {
				t.Fatalf("non-idempotent recovery outcome=%+v execute=%d", outcome, execute)
			}
			turnStatus, _ := postgresTurnState(t, store, session.ID, started.Run.ID)
			if turnStatus != TurnStatusWaitingHuman {
				t.Fatalf("reconciliation turn status = %q", turnStatus)
			}
			interventions, err := store.ListSessionInterventionsContext(ctx, session.ID, InterventionStatusPending)
			if err != nil || len(interventions) != 1 || interventions[0].CallID != agentcore.ToolReconciliationRequestPurpose+":"+call.ID || interventions[0].Kind != InterventionKindClarification {
				t.Fatalf("reconciliation interventions = %+v err=%v", interventions, err)
			}
			response := json.RawMessage(`{"mode":"form","fields":{"outcome":"executed","summary":"external transaction exists","evidence":"transaction:42"}}`)
			if _, err := store.DecideSessionInterventionContext(ctx, session.ID, DecideSessionInterventionInput{
				TurnID: started.Run.ID, CallID: interventions[0].CallID, Status: InterventionStatusAnswered, Response: response,
			}); err != nil {
				t.Fatalf("answer reconciliation: %v", err)
			}
			finalFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-reconciled-worker")
			engine = newPostgresAgentLoopEngine(t, store, finalFence, modelPort, toolPort)
			resumed, err := engine.Resume(ctx, outcome.State, []agentcore.InteractionDecision{{
				InteractionID: outcome.Pause.Interactions[0].ID, Status: "approved", Response: response,
			}})
			if err != nil {
				t.Fatalf("resume reconciliation: %v", err)
			}
			completed, err := engine.Run(ctx, resumed)
			if err != nil || completed.Status != agentcore.OutcomeCompleted {
				t.Fatalf("complete reconciled turn outcome=%+v err=%v", completed, err)
			}
			journal := completed.State.ToolJournal[0]
			if journal.Status != agentcore.ToolCallSucceeded || journal.Attempt != 1 || journal.Reconciliation == nil || journal.Reconciliation.Outcome != agentcore.ToolReconciliationExecuted {
				t.Fatalf("reconciled journal = %+v", journal)
			}
		})
	}
}

func TestPostgresAgentLoopRecoversPartiallyCompletedToolBatch(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"read twice"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	oldFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-partial-worker")
	durability := store.AgentLoopDurability(oldFence)
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	state.Phase = agentcore.PhaseAwaitingModel
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: state})
	if err != nil {
		t.Fatalf("commit awaiting model: %v", err)
	}

	calls := []model.ToolCall{
		{ID: "call_completed", Name: "integration_read_completed", Arguments: json.RawMessage(`{}`)},
		{ID: "call_interrupted", Name: "integration_read_interrupted", Arguments: json.RawMessage(`{}`)},
	}
	planned := make([]agentcore.PlannedToolCall, len(calls))
	for index, call := range calls {
		planned[index] = agentcore.PlannedToolCall{
			Call: call, ExecutionMode: "parallel", SideEffect: "none", Idempotency: "safe",
			IdempotencyKey: agentcore.StableToolIdempotencyKey(session.ID, started.Run.ID, call),
			Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
			ApprovalState: agentcore.ToolApprovalNotRequired,
		}
	}
	next := state.Clone()
	next.Messages = append(next.Messages, model.Message{
		ID: "assistant_tools", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
		Content: []model.Content{
			{Type: model.ContentToolCall, ToolCall: &calls[0]},
			{Type: model.ContentToolCall, ToolCall: &calls[1]},
		},
	})
	next.PendingToolBatch = &agentcore.ToolBatchPlan{Calls: planned}
	next.Phase = agentcore.PhasePreflightingTools
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: state.Revision, Next: next})
	if err != nil {
		t.Fatalf("commit partial batch preflight: %v", err)
	}
	next = state.Clone()
	next.Phase = agentcore.PhaseExecutingTools
	next.ToolCalls = len(calls)
	next.Budget.ToolCalls = len(calls)
	state, err = durability.Commit(ctx, agentcore.Transition{ExpectedRevision: state.Revision, Next: next})
	if err != nil {
		t.Fatalf("commit partial batch executing state: %v", err)
	}
	next = state.Clone()
	completedAt := time.Now().UTC()
	completedResult := model.ToolResult{
		CallID: calls[0].ID, Name: calls[0].Name,
		Content: []model.Content{{Type: model.ContentText, Text: "already completed"}},
	}
	next.ToolJournal = []agentcore.ToolCallJournalEntry{
		{
			CallID: calls[0].ID, Name: calls[0].Name, Idempotency: "safe", IdempotencyKey: planned[0].IdempotencyKey,
			Status: agentcore.ToolCallSucceeded, Attempt: 1, StartedAt: completedAt, CompletedAt: &completedAt, Result: &completedResult,
		},
		{
			CallID: calls[1].ID, Name: calls[1].Name, Idempotency: "safe", IdempotencyKey: planned[1].IdempotencyKey,
			Status: agentcore.ToolCallStarted, Attempt: 1, StartedAt: completedAt,
		},
	}
	state, err = durability.Commit(ctx, agentcore.Transition{
		ExpectedRevision: state.Revision, Next: next,
		Events: []agentcore.RuntimeEvent{
			{Type: agentcore.EventToolCallResult, Message: "First tool result committed before worker crash."},
			{Type: agentcore.EventToolCallStarted, Message: "Second tool call started before worker crash."},
		},
	})
	if err != nil {
		t.Fatalf("commit partial tool batch: %v", err)
	}

	resumeFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-partial-recovery")
	toolPort := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		if len(plan.Calls) != 1 || plan.Calls[0].Call.ID != calls[1].ID || plan.Calls[0].IdempotencyKey != planned[1].IdempotencyKey {
			return agentcore.ToolBatchResult{}, errors.New("recovery replayed the wrong tool call")
		}
		return agentcore.ToolBatchResult{Results: []model.ToolResult{{
			CallID: calls[1].ID, Name: calls[1].Name, Content: []model.Content{{Type: model.ContentText, Text: "recovered"}},
		}}}, nil
	}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{
		Assert: func(request model.Request) error {
			var resultIDs []string
			for _, message := range request.Messages {
				for _, content := range message.Content {
					if content.ToolResult != nil {
						resultIDs = append(resultIDs, content.ToolResult.CallID)
					}
				}
			}
			if len(resultIDs) != 2 || resultIDs[0] != calls[0].ID || resultIDs[1] != calls[1].ID {
				return errors.New("recovered tool results are not in source order")
			}
			return nil
		},
		Response: model.Response{
			Message:    model.Message{ID: "answer_recovered", Content: []model.Content{{Type: model.ContentText, Text: "partial batch recovered"}}},
			StopReason: model.StopReasonComplete,
		},
	})
	engine := newPostgresAgentLoopEngine(t, store, resumeFence, modelPort, toolPort)
	outcome, err := engine.Run(ctx, state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("recover partial batch outcome=%+v err=%v", outcome, err)
	}
	if _, execute := toolPort.Counts(); execute != 1 {
		t.Fatalf("replayed tool count = %d", execute)
	}
	if len(outcome.State.ToolJournal) != 2 || outcome.State.ToolJournal[0].Attempt != 1 || outcome.State.ToolJournal[1].Attempt != 2 || outcome.State.ToolJournal[1].Status != agentcore.ToolCallSucceeded {
		t.Fatalf("recovered journal = %+v", outcome.State.ToolJournal)
	}
}

func TestPostgresAgentLoopRejectsLateToolResultAfterLeaseLoss(t *testing.T) {
	store := newPostgresAgentLoopIntegrationStore(t)
	session := createPostgresIntegrationSession(t, store)
	started, err := store.StartSessionRunContext(t.Context(), session.ID, StartSessionRunInput{Payload: json.RawMessage(`{"content":[{"type":"text","text":"run once"}]}`)})
	if err != nil {
		t.Fatalf("start session run: %v", err)
	}
	ctx, _ := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	oldFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-stale-tool-worker")
	oldDurability := store.AgentLoopDurability(oldFence)
	state := postgresAgentLoopInitialState(session.ID, started.Run.ID)
	state.Phase = agentcore.PhaseAwaitingModel
	state, err = oldDurability.Commit(ctx, agentcore.Transition{ExpectedRevision: 0, Next: state})
	if err != nil {
		t.Fatalf("commit awaiting model: %v", err)
	}

	call := model.ToolCall{ID: "call_fenced", Name: "integration_fenced_read", Arguments: json.RawMessage(`{}`)}
	planned := agentcore.PlannedToolCall{
		Call: call, ExecutionMode: "parallel", SideEffect: "none", Idempotency: "safe",
		IdempotencyKey: agentcore.StableToolIdempotencyKey(session.ID, started.Run.ID, call),
		Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
		ApprovalState: agentcore.ToolApprovalNotRequired,
	}
	next := state.Clone()
	next.Messages = append(next.Messages, model.Message{
		ID: "assistant_tools", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
		Content: []model.Content{{Type: model.ContentToolCall, ToolCall: &call}},
	})
	next.PendingToolBatch = &agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{planned}}
	next.Phase = agentcore.PhasePreflightingTools
	state, err = oldDurability.Commit(ctx, agentcore.Transition{ExpectedRevision: state.Revision, Next: next})
	if err != nil {
		t.Fatalf("commit preflight state: %v", err)
	}
	next = state.Clone()
	next.Phase = agentcore.PhaseExecutingTools
	next.ToolCalls = 1
	next.Budget.ToolCalls = 1
	state, err = oldDurability.Commit(ctx, agentcore.Transition{ExpectedRevision: state.Revision, Next: next})
	if err != nil {
		t.Fatalf("commit executing state: %v", err)
	}
	next = state.Clone()
	next.ToolJournal = []agentcore.ToolCallJournalEntry{{
		CallID: call.ID, Name: call.Name, Idempotency: planned.Idempotency, IdempotencyKey: planned.IdempotencyKey,
		Status: agentcore.ToolCallStarted, Attempt: 1, StartedAt: time.Now().UTC(),
	}}
	state, err = oldDurability.Commit(ctx, agentcore.Transition{
		ExpectedRevision: state.Revision, Next: next,
		Events: []agentcore.RuntimeEvent{{Type: agentcore.EventToolCallStarted, Message: "Tool call started before lease loss."}},
	})
	if err != nil {
		t.Fatalf("commit started journal: %v", err)
	}

	newFence := leasePostgresAgentLoopTurn(t, store, session.ID, started.Run.ID, "agent-loop-current-tool-worker")
	late := state.Clone()
	lateCompletedAt := time.Now().UTC()
	lateResult := model.ToolResult{
		CallID: call.ID, Name: call.Name,
		Content: []model.Content{{Type: model.ContentText, Text: "stale worker result"}},
	}
	late.ToolJournal[0].Status = agentcore.ToolCallSucceeded
	late.ToolJournal[0].CompletedAt = &lateCompletedAt
	late.ToolJournal[0].Result = &lateResult
	if _, err := oldDurability.Commit(ctx, agentcore.Transition{
		ExpectedRevision: state.Revision, Next: late,
		Events: []agentcore.RuntimeEvent{{Type: agentcore.EventToolCallResult, Message: "Late stale worker result."}},
	}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late stale worker commit error = %v, want lease lost", err)
	}

	toolPort := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		if len(plan.Calls) != 1 || plan.Calls[0].IdempotencyKey != planned.IdempotencyKey {
			return agentcore.ToolBatchResult{}, errors.New("recovered tool changed its idempotency key")
		}
		return agentcore.ToolBatchResult{Results: []model.ToolResult{{
			CallID: call.ID, Name: call.Name,
			Content: []model.Content{{Type: model.ContentText, Text: "current worker result"}},
		}}}, nil
	}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: model.Response{
		Message:    model.Message{ID: "answer_recovered", Content: []model.Content{{Type: model.ContentText, Text: "fenced recovery complete"}}},
		StopReason: model.StopReasonComplete,
	}})
	engine := newPostgresAgentLoopEngine(t, store, newFence, modelPort, toolPort)
	outcome, err := engine.Run(ctx, state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("recover after lease loss outcome=%+v err=%v", outcome, err)
	}
	if _, execute := toolPort.Counts(); execute != 1 {
		t.Fatalf("recovered tool count = %d", execute)
	}
	journal := outcome.State.ToolJournal[0]
	if journal.Status != agentcore.ToolCallSucceeded || journal.Attempt != 2 || journal.Result == nil || journal.Result.Content[0].Text != "current worker result" {
		t.Fatalf("recovered journal = %+v", journal)
	}
	loaded, err := store.LoadAgentLoopStateContext(ctx, session.ID, started.Run.ID)
	if err != nil || len(loaded.ToolJournal) != 1 || loaded.ToolJournal[0].Result == nil || loaded.ToolJournal[0].Result.Content[0].Text != "current worker result" {
		t.Fatalf("loaded fenced state = %+v err=%v", loaded, err)
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

func newPostgresAgentLoopIntegrationStore(t testing.TB) *PostgresStore {
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

func newPostgresAgentLoopEngine(t testing.TB, store *PostgresStore, fence AgentLoopFence, modelPort agentcore.ModelPort, toolPort agentcore.ToolPort) *agentcore.Engine {
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

func leasePostgresAgentLoopTurn(t testing.TB, store *PostgresStore, sessionID, turnID, owner string) AgentLoopFence {
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
