package agentcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modeltest"
	toolpkg "tiggy-manage-agent/internal/tools"
)

var testNow = time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)

func TestEngineCompletesWithoutTools(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_1", "done", model.Usage{OutputTokens: 3, TotalTokens: 3, Source: model.UsageSourceProvider})})
	engine, durability := newEngine(t, state, modelPort, nil, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || outcome.State.Phase != agentcore.PhaseCompleted {
		t.Fatalf("Run() outcome = %q phase = %q", outcome.Status, outcome.State.Phase)
	}
	if outcome.FinalMessage == nil || outcome.FinalMessage.ID != "answer_1" || outcome.FinalMessage.Visibility != model.VisibilityPublic {
		t.Fatalf("Run() final message = %+v", outcome.FinalMessage)
	}
	if got := durability.Transitions(); !reflect.DeepEqual(got, []string{"commit", "commit", "commit", "commit", "complete"}) {
		t.Fatalf("durability transitions = %v", got)
	}
}

func TestEngineExecutesToolResultsInSourceOrder(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{"value":1}`)},
		{ID: "call_2", Name: "second", Arguments: json.RawMessage(`{"value":2}`)},
	}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{
			Assert: func(request model.Request) error {
				var resultIDs []string
				for _, message := range request.Messages {
					for _, content := range message.Content {
						if content.ToolResult != nil {
							resultIDs = append(resultIDs, content.ToolResult.CallID)
						}
					}
				}
				if !reflect.DeepEqual(resultIDs, []string{"call_1", "call_2"}) {
					return fmt.Errorf("tool result order = %v", resultIDs)
				}
				return nil
			},
			Response: textResponse("answer_2", "finished", model.Usage{}),
		},
	)
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		return successfulResults(plan), nil
	}}
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || outcome.State.ToolCalls != 2 {
		t.Fatalf("Run() outcome = %q tool calls = %d", outcome.Status, outcome.State.ToolCalls)
	}
	if preflight, execute := tools.Counts(); preflight != 1 || execute != 2 {
		t.Fatalf("tool counts = (%d, %d)", preflight, execute)
	}
}

func TestEngineExecutesSafeToolsConcurrentlyAndJournalsInSourceOrder(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "second", Arguments: json.RawMessage(`{}`)},
	}
	var active atomic.Int32
	var maximum atomic.Int32
	bothStarted := make(chan struct{})
	var bothOnce sync.Once
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(ctx context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		if current >= 2 {
			bothOnce.Do(func() { close(bothStarted) })
		}
		select {
		case <-bothStarted:
		case <-ctx.Done():
			return agentcore.ToolBatchResult{}, ctx.Err()
		case <-time.After(time.Second):
			return agentcore.ToolBatchResult{}, errors.New("parallel tool calls did not overlap")
		}
		return successfulResults(plan), nil
	}}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{Response: textResponse("answer_2", "finished", model.Usage{})},
	)
	engine, durability := newEngine(t, state, modelPort, tools, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || maximum.Load() < 2 {
		t.Fatalf("outcome = %q max concurrency = %d", outcome.Status, maximum.Load())
	}
	if len(outcome.State.ToolJournal) != 2 || outcome.State.ToolJournal[0].CallID != "call_1" || outcome.State.ToolJournal[1].CallID != "call_2" {
		t.Fatalf("tool journal = %+v", outcome.State.ToolJournal)
	}
	for _, entry := range outcome.State.ToolJournal {
		if entry.Status != agentcore.ToolCallSucceeded || entry.Attempt != 1 || !strings.HasPrefix(entry.IdempotencyKey, "tma_tool_") {
			t.Fatalf("tool journal entry = %+v", entry)
		}
	}
	var starts, results int
	for _, event := range durability.Events() {
		if event.Type == agentcore.EventToolCallStarted {
			starts++
		}
		if event.Type == agentcore.EventToolCallResult {
			results++
		}
	}
	if starts != 2 || results != 2 {
		t.Fatalf("tool journal events: starts=%d results=%d", starts, results)
	}
}

func TestEngineLimitsParallelToolConcurrency(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.Budget.Limit.MaxToolCalls = 12
	calls := make([]model.ToolCall, 12)
	for index := range calls {
		calls[index] = model.ToolCall{ID: fmt.Sprintf("call_%02d", index), Name: "read", Arguments: json.RawMessage(`{}`)}
	}
	var active atomic.Int32
	var maximum atomic.Int32
	eightStarted := make(chan struct{})
	var eightOnce sync.Once
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(ctx context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		if current == 8 {
			eightOnce.Do(func() { close(eightStarted) })
		}
		select {
		case <-eightStarted:
		case <-ctx.Done():
			return agentcore.ToolBatchResult{}, ctx.Err()
		case <-time.After(time.Second):
			return agentcore.ToolBatchResult{}, errors.New("parallel tool concurrency did not reach eight")
		}
		return successfulResults(plan), nil
	}}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{Response: textResponse("answer_2", "finished", model.Usage{})},
	)
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
	}
	if maximum.Load() != 8 {
		t.Fatalf("max concurrency = %d, want 8", maximum.Load())
	}
}

func TestEngineExecutesMixedToolBatchSequentiallyInSourceOrder(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_parallel_1", Name: "read_first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_sequential", Name: "write_second", Arguments: json.RawMessage(`{}`)},
		{ID: "call_parallel_2", Name: "read_third", Arguments: json.RawMessage(`{}`)},
	}
	var mu sync.Mutex
	var executionOrder []string
	var active atomic.Int32
	var maximum atomic.Int32
	tools := &modeltest.ScriptedTools{
		PreflightFunc: func(_ context.Context, state agentcore.State, calls []model.ToolCall) (agentcore.ToolBatchPlan, error) {
			planned := make([]agentcore.PlannedToolCall, 0, len(calls))
			for index, call := range calls {
				mode := "parallel"
				if index == 1 {
					mode = "sequential"
				}
				planned = append(planned, agentcore.PlannedToolCall{
					Call: call, ExecutionMode: mode, SideEffect: "none", Idempotency: "safe",
					IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, call),
					Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
					ApprovalState: agentcore.ToolApprovalNotRequired,
				})
			}
			return agentcore.ToolBatchPlan{Calls: planned}, nil
		},
		ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			mu.Lock()
			executionOrder = append(executionOrder, plan.Calls[0].Call.ID)
			mu.Unlock()
			return successfulResults(plan), nil
		},
	}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{Response: textResponse("answer_2", "finished", model.Usage{})},
	)
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
	}
	if maximum.Load() != 1 || !reflect.DeepEqual(executionOrder, []string{"call_parallel_1", "call_sequential", "call_parallel_2"}) {
		t.Fatalf("max concurrency = %d execution order = %v", maximum.Load(), executionOrder)
	}
}

func TestEngineConvertsToolPortFailureAndPreservesPartialResults(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_fail", Name: "first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_ok", Name: "second", Arguments: json.RawMessage(`{}`)},
	}
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		if plan.Calls[0].Call.ID == "call_fail" {
			return agentcore.ToolBatchResult{}, errors.New("remote tool returned a business failure")
		}
		return successfulResults(plan), nil
	}}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{
			Assert: func(request model.Request) error {
				results := map[string]bool{}
				failureText := ""
				for _, message := range request.Messages {
					for _, content := range message.Content {
						if content.ToolResult != nil {
							results[content.ToolResult.CallID] = content.ToolResult.IsError
							if content.ToolResult.CallID == "call_fail" && len(content.ToolResult.Content) > 0 {
								failureText = content.ToolResult.Content[0].Text
							}
						}
					}
				}
				if !results["call_fail"] || results["call_ok"] {
					return fmt.Errorf("partial tool results = %v", results)
				}
				if !strings.Contains(failureText, "Tool execution failed") || strings.Contains(failureText, "remote tool") {
					return fmt.Errorf("unsafe default tool error text = %q", failureText)
				}
				return nil
			},
			Response: textResponse("answer_2", "recovered from tool failure", model.Usage{}),
		},
	)
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
	}
	if len(outcome.State.ToolJournal) != 2 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallFailed || outcome.State.ToolJournal[1].Status != agentcore.ToolCallSucceeded {
		t.Fatalf("tool journal = %+v", outcome.State.ToolJournal)
	}
}

func TestEngineReturnsExplicitToolResultErrorToModel(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	call := model.ToolCall{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{}`)}
	cause := errors.New("postgres://internal-secret")
	visible := agentcore.NewToolResultError(
		"database_unavailable", "Database temporarily unavailable. Retry the lookup later.", false, cause,
	)
	if !errors.Is(visible, cause) {
		t.Fatal("ToolResultError did not preserve its diagnostic cause")
	}
	toolsPort := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		return agentcore.ToolBatchResult{}, fmt.Errorf("lookup backend failed: %w", visible)
	}}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", []model.ToolCall{call})},
		modeltest.ModelStep{
			Assert: func(request model.Request) error {
				for _, message := range request.Messages {
					for _, content := range message.Content {
						result := content.ToolResult
						if result == nil || result.CallID != call.ID {
							continue
						}
						text := result.Content[0].Text
						if !result.IsError || result.Retryable || !strings.Contains(text, "Database temporarily unavailable") ||
							strings.Contains(text, "internal-secret") || !strings.Contains(string(result.State), "database_unavailable") {
							return fmt.Errorf("typed tool result = %+v", result)
						}
						return nil
					}
				}
				return errors.New("typed tool result missing")
			},
			Response: textResponse("answer_2", "will retry later", model.Usage{}),
		},
	)
	engine, _ := newEngine(t, state, modelPort, toolsPort, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
	}
}

func TestEngineDoesNotReplayStartedNonIdempotentTools(t *testing.T) {
	t.Parallel()

	for _, idempotency := range []string{"unknown", "unsafe"} {
		t.Run(idempotency, func(t *testing.T) {
			state, planned := executingToolState(idempotency)
			tools := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
				return agentcore.ToolBatchResult{}, errors.New("non-idempotent tool must not be replayed")
			}}
			modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{
				Assert: func(request model.Request) error {
					for _, message := range request.Messages {
						for _, content := range message.Content {
							if content.ToolResult != nil && content.ToolResult.CallID == planned.Call.ID && !content.ToolResult.IsError && strings.Contains(string(content.ToolResult.State), `"outcome":"executed"`) {
								return nil
							}
						}
					}
					return errors.New("reconciled tool result missing")
				},
				Response: textResponse("answer_2", "operator confirmed execution", model.Usage{}),
			})
			engine, durability := newEngine(t, state, modelPort, tools, nil, nil)
			paused, err := engine.Run(context.Background(), state)
			if err != nil || paused.Status != agentcore.OutcomePaused || paused.Pause == nil || len(paused.Pause.Interactions) != 1 {
				t.Fatalf("Run() outcome = %+v err = %v", paused, err)
			}
			if _, execute := tools.Counts(); execute != 0 {
				t.Fatalf("non-idempotent replay count = %d", execute)
			}
			if paused.State.ToolJournal[0].Status != agentcore.ToolCallIndeterminate || paused.State.ToolJournal[0].Attempt != 1 || !strings.Contains(string(paused.Pause.Interactions[0].Request), agentcore.ToolReconciliationRequestPurpose) {
				t.Fatalf("paused reconciliation = %+v journal=%+v", paused.Pause, paused.State.ToolJournal)
			}
			if _, err := engine.Resume(context.Background(), paused.State, []agentcore.InteractionDecision{{
				InteractionID: paused.Pause.Interactions[0].ID,
				Status:        "approved",
				Response:      json.RawMessage(`{"mode":"form","fields":{"outcome":"executed","summary":"transaction tx-42 exists","evidence":"audit:tx-42"}}`),
			}}); err != nil {
				t.Fatalf("Resume() error = %v", err)
			}
			completed, err := engine.Run(context.Background(), durability.Snapshot())
			if err != nil || completed.Status != agentcore.OutcomeCompleted {
				t.Fatalf("Run(resumed) outcome = %+v err = %v", completed, err)
			}
			journal := completed.State.ToolJournal[0]
			if journal.Status != agentcore.ToolCallSucceeded || journal.Reconciliation == nil || journal.Reconciliation.Outcome != agentcore.ToolReconciliationExecuted {
				t.Fatalf("reconciled journal = %+v", journal)
			}
		})
	}
}

func TestEngineReconcilesNonSuccessfulToolOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome   string
		retryable bool
	}{
		{outcome: agentcore.ToolReconciliationNotExecuted, retryable: true},
		{outcome: agentcore.ToolReconciliationCompensated},
	}
	for _, test := range tests {
		t.Run(test.outcome, func(t *testing.T) {
			state, planned := executingToolState("unknown")
			modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{
				Assert: func(request model.Request) error {
					for _, message := range request.Messages {
						for _, content := range message.Content {
							result := content.ToolResult
							if result != nil && result.CallID == planned.Call.ID && result.IsError && result.Retryable == test.retryable && strings.Contains(string(result.State), `"outcome":"`+test.outcome+`"`) {
								return nil
							}
						}
					}
					return errors.New("reconciled failure result missing")
				},
				Response: textResponse("answer_2", "reconciliation recorded", model.Usage{}),
			})
			tools := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
				return agentcore.ToolBatchResult{}, errors.New("non-idempotent tool must not be replayed")
			}}
			engine, durability := newEngine(t, state, modelPort, tools, nil, nil)
			paused, err := engine.Run(context.Background(), state)
			if err != nil || paused.Status != agentcore.OutcomePaused {
				t.Fatalf("Run() outcome = %+v err = %v", paused, err)
			}
			response := json.RawMessage(fmt.Sprintf(`{"outcome":%q,"summary":"checked external system"}`, test.outcome))
			if _, err := engine.Resume(context.Background(), paused.State, []agentcore.InteractionDecision{{
				InteractionID: paused.Pause.Interactions[0].ID, Status: "approved", Response: response,
			}}); err != nil {
				t.Fatalf("Resume() error = %v", err)
			}
			completed, err := engine.Run(context.Background(), durability.Snapshot())
			if err != nil || completed.Status != agentcore.OutcomeCompleted {
				t.Fatalf("Run(resumed) outcome = %+v err = %v", completed, err)
			}
			journal := completed.State.ToolJournal[0]
			if journal.Status != agentcore.ToolCallFailed || journal.Reconciliation == nil || journal.Reconciliation.Outcome != test.outcome {
				t.Fatalf("reconciled journal = %+v", journal)
			}
		})
	}
}

func TestEngineReconciliationPreservesResolvedToolApproval(t *testing.T) {
	t.Parallel()

	state, planned := executingToolState("unsafe")
	planned.ApprovalState = agentcore.ToolApprovalApproved
	planned.ApprovalSource = agentcore.ToolApprovalSourceHuman
	state.PendingToolBatch.Calls[0] = planned
	state.PendingToolBatch.Interactions = []agentcore.RequiredInteraction{{
		ID: "tool_approval:" + planned.Call.ID, Kind: "tool_approval", CallID: planned.Call.ID,
		Request:  json.RawMessage(`{"risk":"write"}`),
		Decision: &agentcore.InteractionDecision{InteractionID: "tool_approval:" + planned.Call.ID, Status: "approved"},
	}}
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		return agentcore.ToolBatchResult{}, errors.New("approved non-idempotent tool must not be replayed")
	}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_2", "reconciled", model.Usage{})})
	engine, durability := newEngine(t, state, modelPort, tools, nil, nil)
	paused, err := engine.Run(context.Background(), state)
	if err != nil || paused.Status != agentcore.OutcomePaused || paused.Pause == nil || len(paused.Pause.Interactions) != 1 || len(paused.State.PendingToolBatch.Interactions) != 2 {
		t.Fatalf("Run() outcome = %+v err=%v", paused, err)
	}
	if paused.State.PendingToolBatch.Interactions[0].Decision == nil || paused.Pause.Interactions[0].ID != agentcore.ToolReconciliationRequestPurpose+":"+planned.Call.ID {
		t.Fatalf("approval/reconciliation interactions = %+v pause=%+v", paused.State.PendingToolBatch.Interactions, paused.Pause)
	}
	if _, err := engine.Resume(context.Background(), paused.State, []agentcore.InteractionDecision{{
		InteractionID: paused.Pause.Interactions[0].ID, Status: "approved",
		Response: json.RawMessage(`{"outcome":"compensated","summary":"external change was reverted"}`),
	}}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	completed, err := engine.Run(context.Background(), durability.Snapshot())
	if err != nil || completed.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run(resumed) outcome = %+v err=%v", completed, err)
	}
	if _, execute := tools.Counts(); execute != 0 {
		t.Fatalf("reconciled tool replay count = %d", execute)
	}
}

func TestEngineRecoversRejectedStartedToolWithoutIndeterminateResult(t *testing.T) {
	t.Parallel()

	state, planned := executingToolState("unsafe")
	planned.ApprovalState = agentcore.ToolApprovalRejected
	planned.ApprovalSource = agentcore.ToolApprovalSourceHuman
	state.PendingToolBatch.Calls[0] = planned
	state.PendingToolBatch.Interactions = []agentcore.RequiredInteraction{{
		ID: "approval_1", Kind: "approval", CallID: planned.Call.ID, Request: json.RawMessage(`{}`),
		Decision: &agentcore.InteractionDecision{InteractionID: "approval_1", Status: "rejected"},
	}}
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		return agentcore.ToolBatchResult{}, errors.New("rejected tool must not reach executor")
	}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{
		Assert: func(request model.Request) error {
			for _, message := range request.Messages {
				for _, content := range message.Content {
					if content.ToolResult != nil && content.ToolResult.CallID == planned.Call.ID && content.ToolResult.IsError && string(content.ToolResult.State) == `{"status":"rejected"}` {
						return nil
					}
				}
			}
			return errors.New("rejected tool result missing")
		},
		Response: textResponse("answer_2", "rejection preserved", model.Usage{}),
	})
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
	}
	if _, execute := tools.Counts(); execute != 0 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallFailed {
		t.Fatalf("execute=%d journal=%+v", execute, outcome.State.ToolJournal)
	}
}

func TestEngineReplaysSafeAndKeyedToolsWithStableIdempotencyKey(t *testing.T) {
	t.Parallel()

	for _, idempotency := range []string{"safe", "keyed"} {
		t.Run(idempotency, func(t *testing.T) {
			state, planned := executingToolState(idempotency)
			tools := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
				if plan.Calls[0].IdempotencyKey != planned.IdempotencyKey {
					return agentcore.ToolBatchResult{}, fmt.Errorf("idempotency key = %q, want %q", plan.Calls[0].IdempotencyKey, planned.IdempotencyKey)
				}
				return successfulResults(plan), nil
			}}
			modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_2", idempotency+" replay complete", model.Usage{})})
			engine, _ := newEngine(t, state, modelPort, tools, nil, nil)
			outcome, err := engine.Run(context.Background(), state)
			if err != nil || outcome.Status != agentcore.OutcomeCompleted {
				t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
			}
			if _, execute := tools.Counts(); execute != 1 || outcome.State.ToolJournal[0].Attempt != 2 || outcome.State.ToolJournal[0].IdempotencyKey != planned.IdempotencyKey {
				t.Fatalf("execute=%d journal=%+v", execute, outcome.State.ToolJournal)
			}
		})
	}
}

func TestEngineRecoversPartiallyCompletedToolBatch(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_completed", Name: "read.completed", Arguments: json.RawMessage(`{}`)},
		{ID: "call_interrupted", Name: "read.interrupted", Arguments: json.RawMessage(`{}`)},
	}
	planned := make([]agentcore.PlannedToolCall, len(calls))
	for index, call := range calls {
		planned[index] = agentcore.PlannedToolCall{
			Call: call, ExecutionMode: "parallel", SideEffect: "none", Idempotency: "safe",
			IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, call),
			Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
			ApprovalState: agentcore.ToolApprovalNotRequired,
		}
	}
	completedResult := model.ToolResult{
		CallID: calls[0].ID, Name: calls[0].Name,
		Content: []model.Content{{Type: model.ContentText, Text: "already completed"}},
	}
	completedAt := testNow
	state.Messages = append(state.Messages, model.Message{
		ID: "assistant_tools", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
		Content: []model.Content{
			{Type: model.ContentToolCall, ToolCall: &calls[0]},
			{Type: model.ContentToolCall, ToolCall: &calls[1]},
		},
	})
	state.Phase = agentcore.PhaseExecutingTools
	state.PendingToolBatch = &agentcore.ToolBatchPlan{Calls: planned}
	state.ToolCalls = len(calls)
	state.Budget.ToolCalls = len(calls)
	state.ToolJournal = []agentcore.ToolCallJournalEntry{
		{
			CallID: calls[0].ID, Name: calls[0].Name, Idempotency: "safe", IdempotencyKey: planned[0].IdempotencyKey,
			Status: agentcore.ToolCallSucceeded, Attempt: 1, StartedAt: testNow, CompletedAt: &completedAt, Result: &completedResult,
		},
		{
			CallID: calls[1].ID, Name: calls[1].Name, Idempotency: "safe", IdempotencyKey: planned[1].IdempotencyKey,
			Status: agentcore.ToolCallStarted, Attempt: 1, StartedAt: testNow,
		},
	}

	tools := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		if len(plan.Calls) != 1 || plan.Calls[0].Call.ID != calls[1].ID || plan.Calls[0].IdempotencyKey != planned[1].IdempotencyKey {
			return agentcore.ToolBatchResult{}, fmt.Errorf("replayed plan = %+v", plan.Calls)
		}
		return successfulResults(plan), nil
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
			if !reflect.DeepEqual(resultIDs, []string{calls[0].ID, calls[1].ID}) {
				return fmt.Errorf("tool result order = %v", resultIDs)
			}
			return nil
		},
		Response: textResponse("answer_2", "partial batch recovered", model.Usage{}),
	})
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil || outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v err = %v", outcome, err)
	}
	if _, execute := tools.Counts(); execute != 1 {
		t.Fatalf("replayed tool count = %d", execute)
	}
	if len(outcome.State.ToolJournal) != 2 || outcome.State.ToolJournal[0].Attempt != 1 || outcome.State.ToolJournal[1].Attempt != 2 || outcome.State.ToolJournal[1].Status != agentcore.ToolCallSucceeded {
		t.Fatalf("tool journal = %+v", outcome.State.ToolJournal)
	}
}

func TestEngineFailsClosedForMismatchedToolResult(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "second", Arguments: json.RawMessage(`{}`)},
	}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)})
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		result := successfulResults(plan)
		result.Results[0].CallID = "wrong_call"
		return result, nil
	}}
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "invalid_tool_results" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
}

func TestEngineFailsClosedForMissingToolResult(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "second", Arguments: json.RawMessage(`{}`)},
	}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)})
	tools := &modeltest.ScriptedTools{ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		result := successfulResults(plan)
		result.Results = nil
		return result, nil
	}}
	engine, _ := newEngine(t, state, modelPort, tools, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "invalid_tool_results" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
}

func TestEngineValidatesParallelBatchBeforeStartingTools(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "second", Arguments: json.RawMessage(`{}`)},
	}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)})
	var validationCalls atomic.Int32
	toolsPort := &modeltest.ScriptedTools{
		ValidateExecutionFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) error {
			validationCalls.Add(1)
			return agentcore.NewToolFatalError("tool_registry_changed", errors.New("registry revision changed"))
		},
		ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
			return agentcore.ToolBatchResult{}, errors.New("execute must not run after failed batch validation")
		},
	}
	engine, durability := newEngine(t, state, modelPort, toolsPort, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "tool_registry_changed" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
	if len(outcome.State.ToolJournal) != 0 {
		t.Fatalf("fatal tool journal = %+v", outcome.State.ToolJournal)
	}
	_, executeCalls := toolsPort.Counts()
	if validationCalls.Load() != 1 || executeCalls != 0 {
		t.Fatalf("validation calls = %d, execute calls = %d", validationCalls.Load(), executeCalls)
	}
	for _, event := range durability.Events() {
		if event.Type == agentcore.EventToolCallStarted {
			t.Fatalf("tool start event persisted after failed batch validation: %+v", event)
		}
	}
}

func TestEnginePreservesCodedToolPreflightFailure(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	call := model.ToolCall{ID: "call_1", Name: "broken.inspect", Arguments: json.RawMessage(`{}`)}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: toolResponse("assistant_tools", []model.ToolCall{call})})
	toolsPort := &modeltest.ScriptedTools{PreflightFunc: func(context.Context, agentcore.State, []model.ToolCall) (agentcore.ToolBatchPlan, error) {
		return agentcore.ToolBatchPlan{}, fmt.Errorf("snapshot validation: %w", toolpkg.NewToolContractError(
			"invalid_tool_schema", errors.New("registered schema contains an external reference"),
		))
	}}
	engine, _ := newEngine(t, state, modelPort, toolsPort, nil, nil)

	outcome, err := engine.Run(t.Context(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "invalid_tool_schema" ||
		outcome.Failure.Message != "tool preflight infrastructure failed" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
}

func TestEngineTerminalizesStartedJournalAfterUnexpectedExecutionFatal(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	call := model.ToolCall{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{}`)}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: toolResponse("assistant_tools", []model.ToolCall{call})})
	toolsPort := &modeltest.ScriptedTools{ExecuteFunc: func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
		return agentcore.ToolBatchResult{}, agentcore.NewToolFatalError("tool_runtime_failed", errors.New("unexpected execution failure"))
	}}
	engine, _ := newEngine(t, state, modelPort, toolsPort, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "tool_runtime_failed" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
	if len(outcome.State.ToolJournal) != 1 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallIndeterminate || outcome.State.ToolJournal[0].Result == nil {
		t.Fatalf("fatal tool journal = %+v", outcome.State.ToolJournal)
	}
}

func TestEnginePreservesToolContractRevisionsForSingleCallExecution(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	calls := []model.ToolCall{
		{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "second", Arguments: json.RawMessage(`{}`)},
	}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", calls)},
		modeltest.ModelStep{Response: textResponse("assistant_done", "done", model.Usage{})},
	)
	toolsPort := &modeltest.ScriptedTools{
		PreflightFunc: func(_ context.Context, _ agentcore.State, source []model.ToolCall) (agentcore.ToolBatchPlan, error) {
			planned := make([]agentcore.PlannedToolCall, len(source))
			for index, call := range source {
				planned[index] = agentcore.PlannedToolCall{
					Call: call, ExecutionMode: "parallel", SideEffect: "none", Idempotency: "safe",
					IdempotencyKey: "key_" + call.ID,
					Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
					ApprovalState: agentcore.ToolApprovalNotRequired,
				}
			}
			return agentcore.ToolBatchPlan{
				Calls: planned, RegistryRevision: "sha256:registry", PolicyRevision: "sha256:policy",
			}, nil
		},
		ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
			if plan.RegistryRevision != "sha256:registry" || plan.PolicyRevision != "sha256:policy" {
				return agentcore.ToolBatchResult{}, fmt.Errorf("tool contract revisions were lost: %+v", plan)
			}
			return successfulResults(plan), nil
		},
	}
	engine, _ := newEngine(t, state, modelPort, toolsPort, nil, nil)
	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
}

func TestEngineParksBeforeApprovalAndResumes(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	call := model.ToolCall{ID: "call_write", Name: "write_record", Arguments: json.RawMessage(`{"id":"42"}`)}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", []model.ToolCall{call})},
		modeltest.ModelStep{Response: textResponse("answer_2", "approved write complete", model.Usage{})},
	)
	tools := &modeltest.ScriptedTools{
		PreflightFunc: func(_ context.Context, _ agentcore.State, calls []model.ToolCall) (agentcore.ToolBatchPlan, error) {
			return agentcore.ToolBatchPlan{
				Calls: []agentcore.PlannedToolCall{{
					Call: calls[0], ExecutionMode: "sequential", SideEffect: "write", Idempotency: "keyed",
					Disposition: agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
					ApprovalState: agentcore.ToolApprovalPending, ApprovalSource: agentcore.ToolApprovalSourceHuman,
				}},
				Interactions: []agentcore.RequiredInteraction{{ID: "approval_1", Kind: "approval", CallID: calls[0].ID, Request: json.RawMessage(`{"risk":"write"}`)}},
			}, nil
		},
		ExecuteFunc: func(_ context.Context, _ agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
			if plan.Calls[0].ApprovalState != agentcore.ToolApprovalApproved || plan.Calls[0].ApprovalSource != agentcore.ToolApprovalSourceHuman || plan.Interactions[0].Decision == nil {
				return agentcore.ToolBatchResult{}, errors.New("approved decision missing from execution plan")
			}
			return successfulResults(plan), nil
		},
	}
	engine, durability := newEngine(t, state, modelPort, tools, nil, nil)

	paused, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if paused.Status != agentcore.OutcomePaused || paused.State.Phase != agentcore.PhasePaused {
		t.Fatalf("Run() outcome = %q phase = %q", paused.Status, paused.State.Phase)
	}
	if _, execute := tools.Counts(); execute != 0 {
		t.Fatalf("tool executed before approval: count = %d", execute)
	}
	resumed, err := engine.Resume(context.Background(), paused.State, []agentcore.InteractionDecision{{InteractionID: "approval_1", Status: "approved", Response: json.RawMessage(`{"actor":"operator"}`)}})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Phase != agentcore.PhaseExecutingTools {
		t.Fatalf("Resume() phase = %q", resumed.Phase)
	}
	completed, err := engine.Run(context.Background(), durability.Snapshot())
	if err != nil {
		t.Fatalf("Run(resumed) error = %v", err)
	}
	if completed.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run(resumed) status = %q", completed.Status)
	}
}

func TestEngineDoesNotSendRejectedToolToExecutor(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	call := model.ToolCall{ID: "call_delete", Name: "delete_record", Arguments: json.RawMessage(`{"id":"42"}`)}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: toolResponse("assistant_tools", []model.ToolCall{call})},
		modeltest.ModelStep{
			Assert: func(request model.Request) error {
				for _, message := range request.Messages {
					for _, content := range message.Content {
						if content.ToolResult != nil && content.ToolResult.CallID == call.ID && content.ToolResult.IsError && string(content.ToolResult.State) == `{"status":"rejected"}` {
							return nil
						}
					}
				}
				return errors.New("rejected tool result missing from next model request")
			},
			Response: textResponse("answer_2", "delete was not performed", model.Usage{}),
		},
	)
	tools := &modeltest.ScriptedTools{PreflightFunc: func(_ context.Context, _ agentcore.State, calls []model.ToolCall) (agentcore.ToolBatchPlan, error) {
		return agentcore.ToolBatchPlan{
			Calls: []agentcore.PlannedToolCall{{
				Call: calls[0], ExecutionMode: "sequential", SideEffect: "destructive", Idempotency: "unsafe",
				Disposition: agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
				ApprovalState: agentcore.ToolApprovalPending, ApprovalSource: agentcore.ToolApprovalSourceHuman,
			}},
			Interactions: []agentcore.RequiredInteraction{{ID: "approval_1", Kind: "approval", CallID: calls[0].ID, Request: json.RawMessage(`{"risk":"destructive"}`)}},
		}, nil
	}}
	engine, durability := newEngine(t, state, modelPort, tools, nil, nil)

	paused, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := engine.Resume(context.Background(), paused.State, []agentcore.InteractionDecision{{InteractionID: "approval_1", Status: "rejected", Reason: "outside change window"}}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	completed, err := engine.Run(context.Background(), durability.Snapshot())
	if err != nil {
		t.Fatalf("Run(resumed) error = %v", err)
	}
	if completed.Status != agentcore.OutcomeCompleted {
		t.Fatalf("Run(resumed) status = %q", completed.Status)
	}
	if _, execute := tools.Counts(); execute != 0 {
		t.Fatalf("rejected tool reached executor: count = %d", execute)
	}
}

func TestEngineCompletionRetryAddsInternalFeedback(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: textResponse("answer_1", "incomplete", model.Usage{})},
		modeltest.ModelStep{
			Assert: func(request model.Request) error {
				for _, message := range request.Messages {
					if message.Role == model.RoleSystem && message.Visibility == model.VisibilityInternal && message.Content[0].Text == "include the requested evidence" {
						return nil
					}
				}
				return errors.New("completion feedback missing from next model request")
			},
			Response: textResponse("answer_2", "complete", model.Usage{}),
		},
	)
	completion := modeltest.NewScriptedCompletion(
		agentcore.CompletionVerdict{Outcome: agentcore.CompletionRetry, ValidatorID: "policy", Feedback: "include the requested evidence"},
		agentcore.CompletionVerdict{Outcome: agentcore.CompletionPass, ValidatorID: "policy"},
	)
	engine, _ := newEngine(t, state, modelPort, nil, completion, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || completion.Calls() != 2 || outcome.State.CompletionAttempts != 2 {
		t.Fatalf("outcome = %q validator calls = %d completion attempts = %d", outcome.Status, completion.Calls(), outcome.State.CompletionAttempts)
	}
}

func TestEngineDefersControlsUntilTheirSafePoint(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	followUp := model.Message{ID: "follow_up_1", Role: model.RoleUser, Visibility: model.VisibilityPublic, Content: []model.Content{{Type: model.ContentText, Text: "also include risks"}}}
	steer := model.Message{ID: "steer_2", Role: model.RoleUser, Visibility: model.VisibilityInternal, Content: []model.Content{{Type: model.ContentText, Text: "prioritize operational risks"}}}
	controls := staticControls{commands: []agentcore.ControlCommand{
		{Seq: 2, Mode: agentcore.ControlSteer, Message: &steer},
		{Seq: 1, Mode: agentcore.ControlFollowUp, Message: &followUp},
	}}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: textResponse("answer_1", "first answer", model.Usage{})},
		modeltest.ModelStep{
			Assert: func(request model.Request) error {
				found := map[string]bool{}
				for _, message := range request.Messages {
					found[message.ID] = true
				}
				if !found[followUp.ID] || !found[steer.ID] {
					return fmt.Errorf("deferred controls missing: %v", found)
				}
				return nil
			},
			Response: textResponse("answer_2", "revised answer", model.Usage{}),
		},
	)
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model:      modelPort,
		Context:    testContext(),
		Controls:   controls,
		Durability: durability,
		Clock:      modeltest.FixedClock{Time: testNow},
		IDs:        modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || outcome.State.ControlCursor != 2 || len(modelPort.Requests()) != 2 {
		t.Fatalf("outcome = %q cursor = %d model requests = %d", outcome.Status, outcome.State.ControlCursor, len(modelPort.Requests()))
	}
}

func TestEngineRecoversAbandonedModelAttempt(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.Phase = agentcore.PhaseAwaitingModel
	state.PendingModel = &agentcore.PendingModelAttempt{ID: "old_attempt", Number: 1, Status: "running"}
	state.ModelAttempts = 1
	state.Budget.ModelCalls = 1
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_1", "recovered", model.Usage{})})
	engine, durability := newEngine(t, state, modelPort, nil, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || outcome.State.ModelAttempts != 2 {
		t.Fatalf("outcome = %q model attempts = %d", outcome.Status, outcome.State.ModelAttempts)
	}
	found := false
	for _, event := range durability.Events() {
		found = found || event.Type == agentcore.EventModelAbandoned
	}
	if !found {
		t.Fatal("model.abandoned event was not persisted")
	}
}

func TestEngineCompactsContextDuringTurn(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.Messages = append(state.Messages,
		model.Message{ID: "assistant_old", Role: model.RoleAssistant, Visibility: model.VisibilityInternal, Content: []model.Content{{Type: model.ContentText, Text: strings.Repeat("old context ", 200)}}},
		model.Message{ID: "user_latest", Role: model.RoleUser, Visibility: model.VisibilityPublic, Content: []model.Content{{Type: model.ContentText, Text: "continue with the latest request"}}},
	)
	compactor := &scriptedCompactor{result: agentcore.CompactionResult{
		Summary:              "Objective: finish the durable runtime.\nCompleted work: inspected old context.",
		Usage:                model.Usage{InputTokens: 20, OutputTokens: 8, TotalTokens: 28, Source: model.UsageSourceProvider},
		EstimatedInputTokens: 40,
	}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{
		Assert: func(request model.Request) error {
			if len(request.Messages) != 2 || request.Messages[0].Role != model.RoleSystem || !strings.Contains(request.Messages[0].Content[0].Text, "Compacted conversation context") || request.Messages[1].ID != "user_latest" {
				return fmt.Errorf("compacted model messages = %+v", request.Messages)
			}
			return nil
		},
		Response: textResponse("answer_1", "done after compaction", model.Usage{OutputTokens: 2, TotalTokens: 2, Source: model.UsageSourceProvider}),
	})
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelPort, Context: testContext(), Compaction: compactor, Durability: durability,
		Clock: modeltest.FixedClock{Time: testNow}, IDs: modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || outcome.State.CompactionAttempts != 1 || outcome.State.Context.CompactionCount != 1 || outcome.State.Budget.ModelCalls != 2 {
		t.Fatalf("compacted outcome = %+v", outcome)
	}
	if outcome.State.Usage.TotalTokens != 30 || outcome.State.Context.EstimatedInputTokens != 40 || len(compactor.attemptIDs) != 1 {
		t.Fatalf("compaction usage/context = %+v attempts=%v", outcome.State, compactor.attemptIDs)
	}
	var compacting, compacted bool
	for _, event := range durability.Events() {
		compacting = compacting || event.Type == agentcore.EventContextCompacting
		compacted = compacted || event.Type == agentcore.EventContextCompacted
	}
	if !compacting || !compacted {
		t.Fatalf("compaction events: compacting=%t compacted=%t", compacting, compacted)
	}
}

func TestEngineRecoversAbandonedCompactionAttempt(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.Phase = agentcore.PhaseAwaitingModel
	state.PendingCompaction = &agentcore.PendingCompaction{ID: "compaction_old", Number: 1}
	state.CompactionAttempts = 1
	state.Budget.ModelCalls = 1
	compactor := &scriptedCompactor{result: agentcore.CompactionResult{Summary: "Recovered summary.", EstimatedInputTokens: 10}}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_1", "recovered", model.Usage{})})
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelPort, Context: testContext(), Compaction: compactor, Durability: durability,
		Clock: modeltest.FixedClock{Time: testNow}, IDs: modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCompleted || outcome.State.CompactionAttempts != 2 || len(compactor.attemptIDs) != 1 {
		t.Fatalf("recovered compaction outcome = %+v attempts=%v", outcome, compactor.attemptIDs)
	}
	abandoned := false
	for _, event := range durability.Events() {
		abandoned = abandoned || event.Type == agentcore.EventContextAbandoned
	}
	if !abandoned {
		t.Fatal("context.compaction_abandoned event was not persisted")
	}
}

func TestEnginePersistsUsageWhenModelCrossesBudget(t *testing.T) {
	t.Parallel()

	state := initialState(10)
	usage := model.Usage{OutputTokens: 11, TotalTokens: 11, CostMicros: 2, Source: model.UsageSourceProvider}
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_1", "too expensive", usage)})
	engine, durability := newEngine(t, state, modelPort, nil, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "budget_exhausted" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
	stored := durability.Snapshot()
	if stored.Usage.OutputTokens != 11 || stored.Budget.Usage.OutputTokens != 11 {
		t.Fatalf("persisted usage = state:%+v budget:%+v", stored.Usage, stored.Budget.Usage)
	}
	if len(stored.Messages) != 2 || stored.Messages[1].ID != "answer_1" || stored.Messages[1].Visibility != model.VisibilityInternal {
		t.Fatalf("persisted response messages = %+v", stored.Messages)
	}
}

func TestEngineFailsClosedForTruncatedFinalOutput(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	response := textResponse("answer_1", "unfinished", model.Usage{})
	response.StopReason = model.StopReasonLength
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: response})
	engine, _ := newEngine(t, state, modelPort, nil, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "model_output_truncated" || !outcome.Failure.Retryable {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
}

func TestEngineRejectsUnsupportedStopReason(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	response := textResponse("answer_1", "ambiguous", model.Usage{})
	response.StopReason = "provider_specific"
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: response})
	engine, _ := newEngine(t, state, modelPort, nil, nil, nil)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || outcome.Failure.Code != "invalid_model_response" {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
}

func TestEngineResetsPartialLiveStreamOnModelFailure(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{
		Deltas: []model.Delta{{Type: model.DeltaText, Index: 0, Text: "partial"}},
		Error:  &model.ProviderError{Class: model.ErrorNetwork, Code: "connection_lost", Retryable: true, SafeDetail: "connection lost"},
	})
	live := &recordingLive{}
	engine, _ := newEngine(t, state, modelPort, nil, nil, live)

	outcome, err := engine.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeFailed || outcome.Failure == nil || !outcome.Failure.Retryable {
		t.Fatalf("Run() outcome = %+v", outcome)
	}
	deltas := live.Deltas()
	if len(deltas) != 2 || deltas[0].Operation != "append" || deltas[1].Operation != "reset" || deltas[0].StreamID != deltas[1].StreamID {
		t.Fatalf("live deltas = %+v", deltas)
	}
}

func TestRevisionConflictPreventsModelCall(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.Phase = agentcore.PhaseAwaitingModel
	modelPort := modeltest.NewScriptedModel(modeltest.ModelStep{Response: textResponse("answer_1", "must not run", model.Usage{})})
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model:      modelPort,
		Context:    testContext(),
		Durability: rejectingDurability{},
		Clock:      modeltest.FixedClock{Time: testNow},
		IDs:        modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	if _, err := engine.Run(context.Background(), state); err == nil {
		t.Fatal("Run() error = nil, want revision conflict")
	}
	if got := len(modelPort.Requests()); got != 0 {
		t.Fatalf("model request count = %d, want 0", got)
	}
}

func TestCanceledRunUsesLiveContextForDurableOutcome(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	durability := &cancellationDurability{MemoryDurability: modeltest.NewMemoryDurability(state)}
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model:      modeltest.NewScriptedModel(),
		Context:    testContext(),
		Durability: durability,
		Clock:      modeltest.FixedClock{Time: testNow},
		IDs:        modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, err := engine.Run(ctx, state)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != agentcore.OutcomeCanceled || !durability.cancelContextActive {
		t.Fatalf("outcome = %q cancel context active = %t", outcome.Status, durability.cancelContextActive)
	}
}

func TestStateRejectsFailureOutsideTerminalPhase(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.Failure = &agentcore.Failure{Code: "unexpected", Message: "should not be here"}
	if err := state.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want nonterminal failure error")
	}
}

func TestStateRejectsBudgetAccountingDrift(t *testing.T) {
	t.Parallel()

	state := initialState(100)
	state.ModelAttempts = 1
	if err := state.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want accounting mismatch")
	}
}

func TestValidatePhaseTransition(t *testing.T) {
	t.Parallel()

	valid := [][2]agentcore.Phase{
		{agentcore.PhasePreparing, agentcore.PhaseAwaitingModel},
		{agentcore.PhaseAwaitingModel, agentcore.PhaseAwaitingModel},
		{agentcore.PhaseAwaitingModel, agentcore.PhasePreflightingTools},
		{agentcore.PhasePreflightingTools, agentcore.PhasePaused},
		{agentcore.PhasePaused, agentcore.PhaseExecutingTools},
		{agentcore.PhaseExecutingTools, agentcore.PhasePaused},
		{agentcore.PhaseExecutingTools, agentcore.PhaseAwaitingModel},
		{agentcore.PhaseValidatingCompletion, agentcore.PhaseCompleted},
		{agentcore.PhaseAwaitingModel, agentcore.PhaseFailed},
	}
	for _, transition := range valid {
		if err := agentcore.ValidatePhaseTransition(transition[0], transition[1]); err != nil {
			t.Errorf("ValidatePhaseTransition(%q, %q) error = %v", transition[0], transition[1], err)
		}
	}
	invalid := [][2]agentcore.Phase{
		{agentcore.PhasePreparing, agentcore.PhaseCompleted},
		{agentcore.PhaseAwaitingModel, agentcore.PhaseExecutingTools},
		{agentcore.PhasePaused, agentcore.PhaseCompleted},
		{agentcore.PhaseCompleted, agentcore.PhaseFailed},
		{agentcore.PhaseFailed, agentcore.PhaseAwaitingModel},
	}
	for _, transition := range invalid {
		if err := agentcore.ValidatePhaseTransition(transition[0], transition[1]); err == nil {
			t.Errorf("ValidatePhaseTransition(%q, %q) error = nil", transition[0], transition[1])
		}
	}
}

func newEngine(t *testing.T, state agentcore.State, modelPort agentcore.ModelPort, tools agentcore.ToolPort, completion agentcore.CompletionPort, live agentcore.LivePort) (*agentcore.Engine, *modeltest.MemoryDurability) {
	t.Helper()
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model:      modelPort,
		Context:    testContext(),
		Tools:      tools,
		Completion: completion,
		Durability: durability,
		Live:       live,
		Clock:      modeltest.FixedClock{Time: testNow},
		IDs:        modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine, durability
}

func initialState(maxOutputTokens int64) agentcore.State {
	state := agentcore.NewState("session_1", "turn_1", agentcore.Budget{
		MaxRounds:          8,
		MaxModelCalls:      8,
		MaxToolCalls:       8,
		MaxInputTokens:     1_000,
		MaxOutputTokens:    maxOutputTokens,
		MaxReasoningTokens: 1_000,
		MaxCostMicros:      1_000_000,
		Deadline:           testNow.Add(time.Hour),
	})
	state.Messages = []model.Message{{
		ID:         "user_1",
		Role:       model.RoleUser,
		Visibility: model.VisibilityPublic,
		Content:    []model.Content{{Type: model.ContentText, Text: "do the task"}},
	}}
	return state
}

func executingToolState(idempotency string) (agentcore.State, agentcore.PlannedToolCall) {
	state := initialState(100)
	call := model.ToolCall{ID: "call_recovery", Name: "recover.write", Arguments: json.RawMessage(`{"value":"once"}`)}
	executionMode := "sequential"
	if idempotency == "safe" {
		executionMode = "parallel"
	}
	planned := agentcore.PlannedToolCall{
		Call: call, ExecutionMode: executionMode, SideEffect: "write", Idempotency: idempotency,
		IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, call),
		Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
		ApprovalState: agentcore.ToolApprovalNotRequired,
	}
	state.Messages = append(state.Messages, model.Message{
		ID: "assistant_tools", Role: model.RoleAssistant, Visibility: model.VisibilityInternal,
		Content: []model.Content{{Type: model.ContentToolCall, ToolCall: &call}},
	})
	state.Phase = agentcore.PhaseExecutingTools
	state.PendingToolBatch = &agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{planned}}
	state.ToolCalls = 1
	state.Budget.ToolCalls = 1
	state.ToolJournal = []agentcore.ToolCallJournalEntry{{
		CallID: call.ID, Name: call.Name, Idempotency: idempotency, IdempotencyKey: planned.IdempotencyKey,
		Status: agentcore.ToolCallStarted, Attempt: 1, StartedAt: testNow,
	}}
	return state, planned
}

func testContext() modeltest.StaticContext {
	return modeltest.StaticContext{
		Route: model.Route{
			ProviderInstanceID:    "provider_1",
			ProviderConfigVersion: 1,
			ModelID:               "faux-model",
			CatalogRevision:       "catalog_1",
		},
		MaxOutputTokens: 128,
	}
}

func textResponse(id, text string, usage model.Usage) model.Response {
	return model.Response{
		Message:    model.Message{ID: id, Content: []model.Content{{Type: model.ContentText, Text: text}}},
		StopReason: model.StopReasonComplete,
		Usage:      usage,
	}
}

func toolResponse(id string, calls []model.ToolCall) model.Response {
	content := make([]model.Content, len(calls))
	for index := range calls {
		call := calls[index]
		content[index] = model.Content{Type: model.ContentToolCall, ToolCall: &call}
	}
	return model.Response{
		Message:    model.Message{ID: id, Content: content},
		StopReason: model.StopReasonToolCall,
	}
}

func successfulResults(plan agentcore.ToolBatchPlan) agentcore.ToolBatchResult {
	results := make([]model.ToolResult, len(plan.Calls))
	for index, planned := range plan.Calls {
		results[index] = model.ToolResult{
			CallID:  planned.Call.ID,
			Name:    planned.Call.Name,
			Content: []model.Content{{Type: model.ContentText, Text: "ok"}},
		}
	}
	return agentcore.ToolBatchResult{Results: results}
}

type recordingLive struct {
	mu     sync.Mutex
	deltas []model.LiveDelta
}

type staticControls struct {
	commands []agentcore.ControlCommand
}

type scriptedCompactor struct {
	mu         sync.Mutex
	result     agentcore.CompactionResult
	err        error
	attemptIDs []string
}

func (c *scriptedCompactor) NeedsCompaction(state agentcore.State) bool {
	return state.Context.CompactionCount == 0
}

func (c *scriptedCompactor) Compact(_ context.Context, _ agentcore.State, attemptID string) (agentcore.CompactionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attemptIDs = append(c.attemptIDs, attemptID)
	return c.result, c.err
}

func (c staticControls) Drain(context.Context, agentcore.State, agentcore.ControlPoint) ([]agentcore.ControlCommand, error) {
	return append([]agentcore.ControlCommand(nil), c.commands...), nil
}

func (r *recordingLive) Publish(_ context.Context, delta model.LiveDelta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deltas = append(r.deltas, delta)
	return nil
}

func (r *recordingLive) Deltas() []model.LiveDelta {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.LiveDelta(nil), r.deltas...)
}

type rejectingDurability struct{}

type cancellationDurability struct {
	*modeltest.MemoryDurability
	cancelContextActive bool
}

func (d *cancellationDurability) Cancel(ctx context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	d.cancelContextActive = ctx.Err() == nil
	return d.MemoryDurability.Cancel(ctx, transition)
}

func (rejectingDurability) Commit(context.Context, agentcore.Transition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("revision conflict")
}
func (rejectingDurability) Park(context.Context, agentcore.ParkTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("revision conflict")
}
func (rejectingDurability) Complete(context.Context, agentcore.CompleteTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("revision conflict")
}
func (rejectingDurability) Fail(context.Context, agentcore.TerminalTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("revision conflict")
}
func (rejectingDurability) Cancel(context.Context, agentcore.TerminalTransition) (agentcore.State, error) {
	return agentcore.State{}, errors.New("revision conflict")
}
