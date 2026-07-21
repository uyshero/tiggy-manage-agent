package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"tiggy-manage-agent/internal/model"
)

var (
	ErrInvalidTransition = errors.New("invalid agent state transition")
	ErrTruncatedToolCall = errors.New("model tool call was truncated")
)

type Engine struct {
	ports Ports
}

func NewEngine(ports Ports) (*Engine, error) {
	if ports.Model == nil || ports.Context == nil || ports.Durability == nil || ports.Clock == nil || ports.IDs == nil {
		return nil, fmt.Errorf("model, context, durability, clock, and id ports are required")
	}
	return &Engine{ports: ports}, nil
}

func (e *Engine) Run(ctx context.Context, initial State) (Outcome, error) {
	state := initial.Clone()
	if err := state.Validate(); err != nil {
		return Outcome{}, fmt.Errorf("validate initial agent state: %w", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
		}

		switch state.Phase {
		case PhasePreparing:
			next := state.Clone()
			next.Phase = PhaseAwaitingModel
			committed, err := e.commit(ctx, state, next, RuntimeEvent{Type: EventRuntimeStarted, Message: "Agent runtime started."})
			if err != nil {
				return Outcome{}, err
			}
			state = committed
		case PhaseAwaitingModel:
			next, outcome, err := e.awaitModel(ctx, state)
			if err != nil || outcome != nil {
				if outcome != nil {
					return *outcome, err
				}
				return Outcome{}, err
			}
			state = next
		case PhasePreflightingTools:
			next, outcome, err := e.preflightTools(ctx, state)
			if err != nil || outcome != nil {
				if outcome != nil {
					return *outcome, err
				}
				return Outcome{}, err
			}
			state = next
		case PhaseExecutingTools:
			next, outcome, err := e.executeTools(ctx, state)
			if err != nil || outcome != nil {
				if outcome != nil {
					return *outcome, err
				}
				return Outcome{}, err
			}
			state = next
		case PhaseValidatingCompletion:
			next, outcome, err := e.validateCompletion(ctx, state)
			if err != nil || outcome != nil {
				if outcome != nil {
					return *outcome, err
				}
				return Outcome{}, err
			}
			state = next
		case PhasePaused:
			pause := clonePauseState(state.Pause)
			return Outcome{Status: OutcomePaused, State: state, Pause: pause}, nil
		case PhaseCompleted:
			message, ok := finalPublicAssistantMessage(state.Messages)
			if !ok {
				return Outcome{}, fmt.Errorf("completed state has no final public assistant message")
			}
			return Outcome{Status: OutcomeCompleted, State: state, FinalMessage: &message}, nil
		case PhaseFailed:
			failure := cloneFailure(state.Failure)
			return Outcome{Status: OutcomeFailed, State: state, Failure: failure}, nil
		case PhaseCanceled:
			failure := cloneFailure(state.Failure)
			return Outcome{Status: OutcomeCanceled, State: state, Failure: failure}, nil
		default:
			return e.fail(ctx, state, "unknown_phase", "agent state has an unknown phase", false)
		}
	}
}

func (e *Engine) Resume(ctx context.Context, initial State, decisions []InteractionDecision) (State, error) {
	state := initial.Clone()
	if err := state.Validate(); err != nil {
		return State{}, fmt.Errorf("validate paused state: %w", err)
	}
	if state.Phase != PhasePaused || state.PendingToolBatch == nil || state.Pause == nil {
		return State{}, fmt.Errorf("%w: state is not paused", ErrInvalidTransition)
	}
	decisionByID := make(map[string]InteractionDecision, len(decisions))
	for _, decision := range decisions {
		if strings.TrimSpace(decision.InteractionID) == "" || (decision.Status != "approved" && decision.Status != "rejected") {
			return State{}, fmt.Errorf("invalid interaction decision")
		}
		if len(decision.Response) > 0 && !json.Valid(decision.Response) {
			return State{}, fmt.Errorf("interaction decision response must be valid JSON")
		}
		if _, exists := decisionByID[decision.InteractionID]; exists {
			return State{}, fmt.Errorf("duplicate decision for interaction %q", decision.InteractionID)
		}
		decisionByID[decision.InteractionID] = decision
	}
	if len(decisionByID) != len(state.Pause.Interactions) {
		return State{}, fmt.Errorf("interaction decisions do not match paused interactions")
	}

	next := state.Clone()
	callStatus := map[string]string{}
	for index := range next.PendingToolBatch.Interactions {
		interaction := &next.PendingToolBatch.Interactions[index]
		decision, ok := decisionByID[interaction.ID]
		if !ok {
			return State{}, fmt.Errorf("missing decision for interaction %q", interaction.ID)
		}
		decision.Response = append([]byte(nil), decision.Response...)
		interaction.Decision = &decision
		if callStatus[interaction.CallID] != "rejected" {
			callStatus[interaction.CallID] = decision.Status
		}
	}
	for index := range next.PendingToolBatch.Calls {
		if status := callStatus[next.PendingToolBatch.Calls[index].Call.ID]; status != "" {
			next.PendingToolBatch.Calls[index].ApprovalStatus = status
		}
	}
	next.PendingToolBatch.Interactions = cloneInteractions(next.PendingToolBatch.Interactions)
	next.Pause = nil
	next.Phase = PhaseExecutingTools
	return e.commit(ctx, state, next, RuntimeEvent{
		Type:    EventInterventionResolved,
		Message: "Required interactions were resolved.",
		Payload: decisions,
	})
}

// Fail terminalizes a durable turn when orchestration-level validation fails
// before the engine can resume its normal execution loop.
func (e *Engine) Fail(ctx context.Context, initial State, failure Failure) (Outcome, error) {
	state := initial.Clone()
	if err := state.Validate(); err != nil {
		return Outcome{}, fmt.Errorf("validate state before failure: %w", err)
	}
	if strings.TrimSpace(failure.Code) == "" || strings.TrimSpace(failure.Message) == "" {
		return Outcome{}, fmt.Errorf("failure code and message are required")
	}
	return e.fail(ctx, state, failure.Code, failure.Message, failure.Retryable)
}

func (e *Engine) awaitModel(ctx context.Context, state State) (State, *Outcome, error) {
	compacted, outcome, err := e.compactContext(ctx, state)
	if err != nil || outcome != nil {
		return State{}, outcome, err
	}
	state = compacted
	controlled, outcome, err := e.applyControls(ctx, state, ControlBeforeModel)
	if err != nil || outcome != nil {
		return State{}, outcome, err
	}
	state = controlled
	if err := state.Budget.CheckBeforeModel(e.ports.Clock.Now(), state.Round); err != nil {
		failed, failErr := e.fail(ctx, state, "budget_exhausted", err.Error(), false)
		return State{}, &failed, failErr
	}

	if state.PendingModel != nil {
		next := state.Clone()
		abandoned := *next.PendingModel
		next.PendingModel = nil
		committed, err := e.commit(ctx, state, next, RuntimeEvent{
			Type:    EventModelAbandoned,
			Message: "An incomplete model attempt was abandoned during recovery.",
			Payload: abandoned,
		})
		if err != nil {
			return State{}, nil, err
		}
		state = committed
	}

	next := state.Clone()
	attempt := PendingModelAttempt{
		ID:     e.ports.IDs.NewID("model_attempt"),
		Number: next.ModelAttempts + 1,
		Status: "running",
	}
	if strings.TrimSpace(attempt.ID) == "" {
		return State{}, nil, fmt.Errorf("id generator returned an empty model attempt id")
	}
	next.PendingModel = &attempt
	next.ModelAttempts++
	next.Budget.ReserveModelCall()
	committed, err := e.commit(ctx, state, next, RuntimeEvent{
		Type:    EventModelRequested,
		Message: "Model request started.",
		Payload: attempt,
	})
	if err != nil {
		return State{}, nil, err
	}
	state = committed

	request, err := e.ports.Context.Build(ctx, state.Clone())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			canceled, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
			return State{}, &canceled, cancelErr
		}
		failed, failErr := e.fail(ctx, state, "context_build_failed", "agent context could not be prepared", false)
		return State{}, &failed, failErr
	}
	request.Purpose = defaultPurpose(request.Purpose)
	request.SessionID = state.SessionID
	request.TurnID = state.TurnID
	request.AttemptID = attempt.ID
	if err := request.Validate(); err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_model_request", "agent model request was invalid", false)
		return State{}, &failed, failErr
	}

	deltaCount := 0
	response, modelErr := e.ports.Model.Generate(ctx, request, func(delta model.Delta) error {
		deltaCount++
		e.publishLive(ctx, attempt, delta)
		return nil
	})
	if modelErr != nil {
		if deltaCount > 0 {
			e.publishReset(ctx, attempt)
		}
		if errors.Is(modelErr, context.Canceled) || errors.Is(modelErr, context.DeadlineExceeded) {
			canceled, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
			return State{}, &canceled, cancelErr
		}
		failure := failureFromError(modelErr)
		failed, failErr := e.fail(ctx, state, failure.Code, failure.Message, failure.Retryable)
		return State{}, &failed, failErr
	}
	switch response.StopReason {
	case model.StopReasonComplete, model.StopReasonToolCall, model.StopReasonLength:
	case model.StopReasonCanceled:
		canceled, cancelErr := e.cancel(ctx, state, "model_canceled", "model request was canceled")
		return State{}, &canceled, cancelErr
	case model.StopReasonError:
		failed, failErr := e.fail(ctx, state, "model_failed", "model request failed", false)
		return State{}, &failed, failErr
	default:
		failed, failErr := e.fail(ctx, state, "invalid_model_response", "model returned an unsupported stop reason", false)
		return State{}, &failed, failErr
	}
	if err := response.Usage.Validate(); err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_model_usage", "model returned invalid usage", false)
		return State{}, &failed, failErr
	}

	message := model.CloneMessage(response.Message)
	if message.ID == "" {
		message.ID = e.ports.IDs.NewID("message")
	}
	message.Role = model.RoleAssistant
	message.Visibility = model.VisibilityInternal
	if err := message.Validate(); err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_model_response", "model returned an invalid response", false)
		return State{}, &failed, failErr
	}

	next = state.Clone()
	next.PendingModel = nil
	next.Messages = append(next.Messages, message)
	next.Round++
	next.Usage = next.Usage.Add(response.Usage)
	next.Budget.AddUsage(response.Usage)
	calls := toolCalls(message)
	if err := next.Budget.CheckAfterUsage(); err != nil {
		failed, failErr := e.failFrom(ctx, state, next, "budget_exhausted", err.Error(), false)
		return State{}, &failed, failErr
	}
	if len(calls) > 0 {
		if response.StopReason == model.StopReasonLength {
			failed, failErr := e.failFrom(ctx, state, next, "truncated_tool_call", ErrTruncatedToolCall.Error(), false)
			return State{}, &failed, failErr
		}
		if response.StopReason != model.StopReasonToolCall {
			failed, failErr := e.failFrom(ctx, state, next, "invalid_model_response", "model returned tool calls with an incompatible stop reason", false)
			return State{}, &failed, failErr
		}
		next.PendingToolBatch = &ToolBatchPlan{Calls: plannedCalls(state, calls)}
		next.Phase = PhasePreflightingTools
	} else {
		if response.StopReason == model.StopReasonLength {
			failed, failErr := e.failFrom(ctx, state, next, "model_output_truncated", "model output was truncated before completion", true)
			return State{}, &failed, failErr
		}
		if response.StopReason == model.StopReasonToolCall {
			failed, failErr := e.failFrom(ctx, state, next, "invalid_model_response", "model stopped for a tool call without returning one", false)
			return State{}, &failed, failErr
		}
		next.PendingCompletion = &PendingCompletion{MessageID: message.ID}
		next.Phase = PhaseValidatingCompletion
	}
	committed, err = e.commit(ctx, state, next, RuntimeEvent{
		Type:    EventModelResponded,
		Message: "Model response completed.",
		Payload: map[string]any{"attempt_id": attempt.ID, "stop_reason": response.StopReason, "usage": response.Usage},
	})
	return committed, nil, err
}

func (e *Engine) compactContext(ctx context.Context, state State) (State, *Outcome, error) {
	if e.ports.Compaction == nil {
		return state, nil, nil
	}
	if state.PendingCompaction != nil {
		next := state.Clone()
		abandoned := *next.PendingCompaction
		next.PendingCompaction = nil
		committed, err := e.commit(ctx, state, next, RuntimeEvent{
			Type: EventContextAbandoned, Message: "An incomplete context compaction attempt was abandoned during recovery.", Payload: abandoned,
		})
		if err != nil {
			return State{}, nil, err
		}
		state = committed
	}
	if !e.ports.Compaction.NeedsCompaction(state.Clone()) {
		return state, nil, nil
	}
	if err := state.Budget.CheckBeforeModel(e.ports.Clock.Now(), state.Round); err != nil {
		failed, failErr := e.fail(ctx, state, "budget_exhausted", err.Error(), false)
		return State{}, &failed, failErr
	}

	next := state.Clone()
	attempt := PendingCompaction{ID: e.ports.IDs.NewID("compaction_attempt"), Number: next.CompactionAttempts + 1}
	if strings.TrimSpace(attempt.ID) == "" {
		return State{}, nil, fmt.Errorf("id generator returned an empty compaction attempt id")
	}
	next.PendingCompaction = &attempt
	next.CompactionAttempts++
	next.Budget.ReserveModelCall()
	committed, err := e.commit(ctx, state, next, RuntimeEvent{
		Type: EventContextCompacting, Message: "Context compaction started.", Payload: attempt,
	})
	if err != nil {
		return State{}, nil, err
	}
	state = committed

	result, compactErr := e.ports.Compaction.Compact(ctx, state.Clone(), attempt.ID)
	if compactErr != nil {
		if errors.Is(compactErr, context.Canceled) || errors.Is(compactErr, context.DeadlineExceeded) {
			canceled, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
			return State{}, &canceled, cancelErr
		}
		failed, failErr := e.fail(ctx, state, "context_compaction_failed", "agent context compaction failed", true)
		return State{}, &failed, failErr
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" || result.EstimatedInputTokens < 0 {
		failed, failErr := e.fail(ctx, state, "invalid_compaction_result", "context compaction returned an invalid result", false)
		return State{}, &failed, failErr
	}
	if err := result.Usage.Validate(); err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_compaction_usage", "context compaction returned invalid usage", false)
		return State{}, &failed, failErr
	}

	next = state.Clone()
	next.PendingCompaction = nil
	next.Messages = e.compactedMessages(state.Messages, result.Summary)
	next.Usage = next.Usage.Add(result.Usage)
	next.Budget.AddUsage(result.Usage)
	next.Context.SummaryRevision = attempt.ID
	next.Context.EstimatedInputTokens = result.EstimatedInputTokens
	next.Context.CompactionCount++
	if err := next.Budget.CheckAfterUsage(); err != nil {
		failed, failErr := e.failFrom(ctx, state, next, "budget_exhausted", err.Error(), false)
		return State{}, &failed, failErr
	}
	committed, err = e.commit(ctx, state, next, RuntimeEvent{
		Type: EventContextCompacted, Message: "Context compaction completed.", Payload: map[string]any{
			"attempt_id": attempt.ID, "usage": result.Usage, "estimated_input_tokens": result.EstimatedInputTokens,
		},
	})
	return committed, nil, err
}

func (e *Engine) compactedMessages(messages []model.Message, summary string) []model.Message {
	compacted := []model.Message{{
		ID: e.ports.IDs.NewID("message"), Role: model.RoleSystem, Visibility: model.VisibilityInternal,
		Content: []model.Content{{Type: model.ContentText, Text: "Compacted conversation context:\n" + summary}},
	}}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == model.RoleUser && messages[index].Visibility == model.VisibilityPublic {
			compacted = append(compacted, model.CloneMessage(messages[index]))
			break
		}
	}
	return compacted
}

func (e *Engine) preflightTools(ctx context.Context, state State) (State, *Outcome, error) {
	if e.ports.Tools == nil {
		failed, err := e.fail(ctx, state, "tool_port_missing", "model requested tools but no tool runtime is configured", false)
		return State{}, &failed, err
	}
	calls := callsFromPlan(state.PendingToolBatch)
	if err := state.Budget.CheckBeforeTools(len(calls)); err != nil {
		failed, failErr := e.fail(ctx, state, "budget_exhausted", err.Error(), false)
		return State{}, &failed, failErr
	}
	plan, err := e.ports.Tools.Preflight(ctx, state.Clone(), calls)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			canceled, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
			return State{}, &canceled, cancelErr
		}
		failed, failErr := e.fail(ctx, state, "tool_preflight_failed", "tool preflight failed", false)
		return State{}, &failed, failErr
	}
	bindToolIdempotencyKeys(state, &plan)
	if err := validatePlannedCalls(calls, plan); err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_tool_plan", "tool runtime returned an invalid execution plan", false)
		return State{}, &failed, failErr
	}

	next := state.Clone()
	next.PendingToolBatch = cloneToolBatchPlan(&plan)
	next.ToolCalls += len(plan.Calls)
	next.Budget.ReserveToolCalls(len(plan.Calls))
	if len(plan.Interactions) > 0 {
		pause := PauseState{Reason: "tool_intervention_required", Interactions: cloneInteractions(plan.Interactions)}
		next.Pause = &pause
		next.Phase = PhasePaused
		parked, parkErr := e.park(ctx, state, next, pause,
			RuntimeEvent{Type: EventToolBatchPlanned, Message: "Tool batch passed preflight.", Payload: plan},
			RuntimeEvent{Type: EventInterventionRequired, Message: "Tool batch requires human intervention.", Payload: pause},
		)
		if parkErr != nil {
			return State{}, nil, parkErr
		}
		outcome := Outcome{Status: OutcomePaused, State: parked, Pause: clonePauseState(parked.Pause)}
		return State{}, &outcome, nil
	}
	next.Phase = PhaseExecutingTools
	committed, commitErr := e.commit(ctx, state, next, RuntimeEvent{Type: EventToolBatchPlanned, Message: "Tool batch passed preflight.", Payload: plan})
	return committed, nil, commitErr
}

func (e *Engine) executeTools(ctx context.Context, state State) (State, *Outcome, error) {
	if e.ports.Tools == nil || state.PendingToolBatch == nil {
		failed, err := e.fail(ctx, state, "tool_port_missing", "tool runtime is not configured", false)
		return State{}, &failed, err
	}
	plan := *cloneToolBatchPlan(state.PendingToolBatch)
	prepared, parallel, sequential, immediate, err := e.prepareToolCalls(ctx, state, plan)
	if err != nil {
		return State{}, nil, err
	}
	state = prepared
	for _, outcome := range immediate {
		state, err = e.commitToolCallResult(context.WithoutCancel(ctx), state, outcome)
		if err != nil {
			return State{}, nil, err
		}
	}

	var fatalErr error
	var canceled bool
	state, fatalErr, canceled = e.executeParallelToolCalls(ctx, state, plan, parallel)
	if fatalErr == nil && !canceled {
		state, fatalErr, canceled = e.executeSequentialToolCalls(ctx, state, plan, sequential)
	}
	if fatalErr != nil {
		failed, failErr := e.fail(context.WithoutCancel(ctx), state, "invalid_tool_results", "tool runtime returned invalid results", false)
		return State{}, &failed, failErr
	}
	if canceled || ctx.Err() != nil {
		canceledOutcome, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
		return State{}, &canceledOutcome, cancelErr
	}

	result, err := toolBatchResultFromJournal(plan, state.ToolJournal)
	if err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_tool_results", "tool journal is incomplete", false)
		return State{}, &failed, failErr
	}
	next := state.Clone()
	for _, toolResult := range result.Results {
		message := model.Message{
			ID:         e.ports.IDs.NewID("message"),
			Role:       model.RoleTool,
			Visibility: model.VisibilityInternal,
			Content: []model.Content{{
				Type:       model.ContentToolResult,
				ToolResult: cloneToolResult(toolResult),
			}},
		}
		if err := message.Validate(); err != nil {
			failed, failErr := e.fail(ctx, state, "invalid_tool_result_message", "tool result could not be recorded", false)
			return State{}, &failed, failErr
		}
		next.Messages = append(next.Messages, message)
	}
	next.PendingToolBatch = nil
	next.Phase = PhaseAwaitingModel
	committed, commitErr := e.commit(ctx, state, next, RuntimeEvent{
		Type:    EventToolBatchCompleted,
		Message: "Tool batch completed.",
		Payload: result,
	})
	return committed, nil, commitErr
}

const maxConcurrentToolCalls = 8

type toolCallOutcome struct {
	planned  PlannedToolCall
	result   model.ToolResult
	status   ToolCallStatus
	err      error
	canceled bool
}

func (e *Engine) prepareToolCalls(ctx context.Context, state State, plan ToolBatchPlan) (State, []PlannedToolCall, []PlannedToolCall, []toolCallOutcome, error) {
	next := state.Clone()
	journalIndex := toolJournalIndex(next.ToolJournal)
	parallel := make([]PlannedToolCall, 0, len(plan.Calls))
	sequential := make([]PlannedToolCall, 0, len(plan.Calls))
	immediate := make([]toolCallOutcome, 0)
	events := make([]RuntimeEvent, 0, len(plan.Calls))
	now := e.ports.Clock.Now()
	parallelBatch := true
	for _, planned := range plan.Calls {
		if planned.ApprovalStatus != "rejected" && (planned.ExecutionMode != "parallel" || strings.TrimSpace(planned.LockKey) != "") {
			parallelBatch = false
			break
		}
	}

	for _, planned := range plan.Calls {
		index, exists := journalIndex[planned.Call.ID]
		if exists && next.ToolJournal[index].Status != ToolCallStarted {
			continue
		}
		if exists && planned.ApprovalStatus == "rejected" {
			immediate = append(immediate, toolCallOutcome{planned: planned, result: rejectedToolResult(planned), status: ToolCallFailed})
			continue
		}
		if exists && !toolCallReplayable(planned.Idempotency) {
			immediate = append(immediate, toolCallOutcome{
				planned: planned,
				result:  indeterminateToolResult(planned, "Tool execution may have completed before recovery; it was not replayed because the operation is not idempotent."),
				status:  ToolCallIndeterminate,
			})
			continue
		}
		if exists {
			next.ToolJournal[index].Attempt++
			next.ToolJournal[index].StartedAt = now
		} else {
			entry := ToolCallJournalEntry{
				CallID: planned.Call.ID, Name: planned.Call.Name,
				Idempotency: planned.Idempotency, IdempotencyKey: planned.IdempotencyKey,
				Status: ToolCallStarted, Attempt: 1, StartedAt: now,
			}
			next.ToolJournal = append(next.ToolJournal, entry)
			journalIndex[planned.Call.ID] = len(next.ToolJournal) - 1
		}
		entry := next.ToolJournal[journalIndex[planned.Call.ID]]
		events = append(events, RuntimeEvent{Type: EventToolCallStarted, Message: "Tool call started.", Payload: entry})
		if planned.ApprovalStatus == "rejected" {
			immediate = append(immediate, toolCallOutcome{planned: planned, result: rejectedToolResult(planned), status: ToolCallFailed})
		} else if parallelBatch {
			parallel = append(parallel, planned)
		} else {
			sequential = append(sequential, planned)
		}
	}
	if len(events) == 0 {
		return state, parallel, sequential, immediate, nil
	}
	committed, err := e.commit(ctx, state, next, events...)
	return committed, parallel, sequential, immediate, err
}

func (e *Engine) executeParallelToolCalls(ctx context.Context, state State, plan ToolBatchPlan, calls []PlannedToolCall) (State, error, bool) {
	if len(calls) == 0 {
		return state, nil, false
	}
	outcomes := make(chan toolCallOutcome, len(calls))
	semaphore := make(chan struct{}, maxConcurrentToolCalls)
	executionState := state.Clone()
	for _, planned := range calls {
		planned := planned
		go func(snapshot State) {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			outcomes <- e.executeToolCall(ctx, snapshot, plan, planned)
		}(executionState)
	}
	var fatalErr error
	var canceled bool
	for range calls {
		outcome := <-outcomes
		if outcome.err != nil {
			fatalErr = errors.Join(fatalErr, outcome.err)
			continue
		}
		var err error
		state, err = e.commitToolCallResult(context.WithoutCancel(ctx), state, outcome)
		if err != nil {
			return State{}, err, canceled
		}
		canceled = canceled || outcome.canceled
	}
	return state, fatalErr, canceled
}

func (e *Engine) executeSequentialToolCalls(ctx context.Context, state State, plan ToolBatchPlan, calls []PlannedToolCall) (State, error, bool) {
	for _, planned := range calls {
		outcome := e.executeToolCall(ctx, state, plan, planned)
		if outcome.err != nil {
			return state, outcome.err, false
		}
		var err error
		state, err = e.commitToolCallResult(context.WithoutCancel(ctx), state, outcome)
		if err != nil {
			return State{}, err, outcome.canceled
		}
		if outcome.canceled {
			return state, nil, true
		}
	}
	return state, nil, false
}

func (e *Engine) executeToolCall(ctx context.Context, state State, plan ToolBatchPlan, planned PlannedToolCall) toolCallOutcome {
	single := singleToolPlan(plan, planned)
	executed, err := e.ports.Tools.Execute(ctx, state.Clone(), single)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return toolCallOutcome{planned: planned, result: failedToolResult(planned, "tool_execution_canceled", "Tool execution was canceled.", true), status: ToolCallFailed, canceled: true}
		}
		return toolCallOutcome{planned: planned, result: failedToolResult(planned, "tool_execution_failed", err.Error(), true), status: ToolCallFailed}
	}
	if err := validateToolResults(single, executed); err != nil {
		return toolCallOutcome{planned: planned, err: fmt.Errorf("tool %s: %w", planned.Call.ID, err)}
	}
	result := executed.Results[0]
	status := ToolCallSucceeded
	if result.IsError {
		status = ToolCallFailed
	}
	return toolCallOutcome{planned: planned, result: result, status: status}
}

func (e *Engine) commitToolCallResult(ctx context.Context, state State, outcome toolCallOutcome) (State, error) {
	next := state.Clone()
	index, ok := toolJournalIndex(next.ToolJournal)[outcome.planned.Call.ID]
	if !ok || next.ToolJournal[index].Status != ToolCallStarted {
		return State{}, fmt.Errorf("tool journal call %q is not started", outcome.planned.Call.ID)
	}
	completedAt := e.ports.Clock.Now()
	next.ToolJournal[index].Status = outcome.status
	next.ToolJournal[index].CompletedAt = &completedAt
	next.ToolJournal[index].Result = cloneToolResult(outcome.result)
	return e.commit(ctx, state, next, RuntimeEvent{
		Type: EventToolCallResult, Message: "Tool call result recorded.", Payload: next.ToolJournal[index],
	})
}

func singleToolPlan(plan ToolBatchPlan, planned PlannedToolCall) ToolBatchPlan {
	single := ToolBatchPlan{Calls: []PlannedToolCall{planned}}
	for _, interaction := range plan.Interactions {
		if interaction.CallID == planned.Call.ID {
			single.Interactions = append(single.Interactions, interaction)
		}
	}
	return single
}

func toolJournalIndex(journal []ToolCallJournalEntry) map[string]int {
	indexes := make(map[string]int, len(journal))
	for index, entry := range journal {
		indexes[entry.CallID] = index
	}
	return indexes
}

func toolCallReplayable(idempotency string) bool {
	switch strings.ToLower(strings.TrimSpace(idempotency)) {
	case "safe", "keyed", "idempotent":
		return true
	default:
		return false
	}
}

func toolBatchResultFromJournal(plan ToolBatchPlan, journal []ToolCallJournalEntry) (ToolBatchResult, error) {
	indexes := toolJournalIndex(journal)
	result := ToolBatchResult{Results: make([]model.ToolResult, 0, len(plan.Calls))}
	for _, planned := range plan.Calls {
		index, ok := indexes[planned.Call.ID]
		if !ok || journal[index].Status == ToolCallStarted || journal[index].Result == nil {
			return ToolBatchResult{}, fmt.Errorf("tool call %q has no terminal journal result", planned.Call.ID)
		}
		result.Results = append(result.Results, *cloneToolResult(*journal[index].Result))
	}
	return result, validateToolResults(plan, result)
}

func bindToolIdempotencyKeys(state State, plan *ToolBatchPlan) {
	if plan == nil {
		return
	}
	for index := range plan.Calls {
		if strings.TrimSpace(plan.Calls[index].IdempotencyKey) == "" {
			plan.Calls[index].IdempotencyKey = StableToolIdempotencyKey(state.SessionID, state.TurnID, plan.Calls[index].Call)
		}
	}
}

func rejectedToolResult(planned PlannedToolCall) model.ToolResult {
	return model.ToolResult{
		CallID: planned.Call.ID, Name: planned.Call.Name,
		Content: []model.Content{{Type: model.ContentText, Text: "Tool execution was rejected by the required approver."}},
		State:   json.RawMessage(`{"status":"rejected"}`), IsError: true,
	}
}

func indeterminateToolResult(planned PlannedToolCall, message string) model.ToolResult {
	return failedToolResult(planned, "tool_execution_indeterminate", message, false)
}

func failedToolResult(planned PlannedToolCall, code, message string, retryable bool) model.ToolResult {
	state, _ := json.Marshal(map[string]any{"status": "failed", "error_type": code, "idempotency_key": planned.IdempotencyKey})
	return model.ToolResult{
		CallID: planned.Call.ID, Name: planned.Call.Name,
		Content: []model.Content{{Type: model.ContentText, Text: message}},
		State:   state, IsError: true, Retryable: retryable,
	}
}

func (e *Engine) validateCompletion(ctx context.Context, state State) (State, *Outcome, error) {
	candidate, ok := state.messageByID(state.PendingCompletion.MessageID)
	if !ok {
		failed, err := e.fail(ctx, state, "completion_candidate_missing", "completion candidate is missing", false)
		return State{}, &failed, err
	}

	next := state.Clone()
	next.PendingCompletion.Attempt++
	next.CompletionAttempts++
	committed, err := e.commit(ctx, state, next, RuntimeEvent{
		Type:    EventCompletionStarted,
		Message: "Completion validation started.",
		Payload: map[string]any{"message_id": candidate.ID, "attempt": next.PendingCompletion.Attempt},
	})
	if err != nil {
		return State{}, nil, err
	}
	state = committed
	candidate, _ = state.messageByID(state.PendingCompletion.MessageID)

	verdict := CompletionVerdict{Outcome: CompletionPass, ValidatorID: "builtin.pass"}
	if e.ports.Completion != nil {
		verdict, err = e.ports.Completion.Validate(ctx, CompletionCandidate{Message: candidate, Attempt: state.PendingCompletion.Attempt, State: state.Clone()})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				canceled, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
				return State{}, &canceled, cancelErr
			}
			failed, failErr := e.fail(ctx, state, "completion_validator_failed", "completion validation failed", false)
			return State{}, &failed, failErr
		}
	}
	if err := validateCompletionVerdict(verdict); err != nil {
		failed, failErr := e.fail(ctx, state, "invalid_completion_verdict", "completion validator returned an invalid verdict", false)
		return State{}, &failed, failErr
	}

	switch verdict.Outcome {
	case CompletionRetry:
		next = state.Clone()
		next.Messages = append(next.Messages, model.Message{
			ID:         e.ports.IDs.NewID("message"),
			Role:       model.RoleSystem,
			Visibility: model.VisibilityInternal,
			Content:    []model.Content{{Type: model.ContentText, Text: verdict.Feedback}},
		})
		next.PendingCompletion = nil
		next.Phase = PhaseAwaitingModel
		committed, commitErr := e.commit(ctx, state, next, RuntimeEvent{Type: EventCompletionRetried, Message: "Completion validation requested another model turn.", Payload: verdict})
		return committed, nil, commitErr
	case CompletionFail:
		failed, failErr := e.fail(ctx, state, defaultString(verdict.ReasonCode, "completion_rejected"), defaultString(verdict.Reason, "completion was rejected"), false)
		return State{}, &failed, failErr
	case CompletionPass:
		controlled, outcome, controlErr := e.applyControls(ctx, state, ControlBeforeComplete)
		if controlErr != nil || outcome != nil {
			return State{}, outcome, controlErr
		}
		if controlled.Revision != state.Revision {
			next = controlled.Clone()
			next.PendingCompletion = nil
			next.Phase = PhaseAwaitingModel
			committed, commitErr := e.commit(ctx, controlled, next, RuntimeEvent{Type: EventCompletionRetried, Message: "A follow-up command continued the agent turn."})
			return committed, nil, commitErr
		}
		next = state.Clone()
		if !markMessagePublic(next.Messages, candidate.ID) {
			failed, failErr := e.fail(ctx, state, "completion_candidate_missing", "completion candidate is missing", false)
			return State{}, &failed, failErr
		}
		next.PendingCompletion = nil
		next.Phase = PhaseCompleted
		completed, completeErr := e.complete(ctx, state, next, candidate.ID,
			RuntimeEvent{Type: EventCompletionValidated, Message: "Completion candidate passed validation.", Payload: verdict},
			RuntimeEvent{Type: EventRuntimeCompleted, Message: "Agent runtime completed."},
		)
		if completeErr != nil {
			return State{}, nil, completeErr
		}
		final, _ := completed.messageByID(candidate.ID)
		completedOutcome := Outcome{Status: OutcomeCompleted, State: completed, FinalMessage: &final}
		return State{}, &completedOutcome, nil
	default:
		panic("validated completion verdict has an unsupported outcome")
	}
}

func (e *Engine) applyControls(ctx context.Context, state State, point ControlPoint) (State, *Outcome, error) {
	if e.ports.Controls == nil {
		return state, nil, nil
	}
	commands, err := e.ports.Controls.Drain(ctx, state.Clone(), point)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			canceled, cancelErr := e.cancel(context.WithoutCancel(ctx), state, "context_canceled", "agent execution was canceled")
			return State{}, &canceled, cancelErr
		}
		failed, failErr := e.fail(ctx, state, "control_read_failed", "agent control commands could not be read", true)
		return State{}, &failed, failErr
	}
	if len(commands) == 0 {
		return state, nil, nil
	}
	next := state.Clone()
	changed := false
	sort.Slice(commands, func(i, j int) bool { return commands[i].Seq < commands[j].Seq })
	for _, command := range commands {
		if command.Seq <= next.ControlCursor {
			continue
		}
		switch command.Mode {
		case ControlCancel:
			next.ControlCursor = command.Seq
			canceled, cancelErr := e.cancel(ctx, next, "user_canceled", defaultString(command.Reason, "agent execution was canceled"))
			return State{}, &canceled, cancelErr
		case ControlSteer:
			if (point != ControlBeforeModel && point != ControlBeforeComplete) || command.Message == nil {
				return e.deferControls(ctx, state, next, changed)
			}
			next.Messages = append(next.Messages, model.CloneMessage(*command.Message))
			next.ControlCursor = command.Seq
			changed = true
		case ControlFollowUp:
			if point != ControlBeforeComplete || command.Message == nil {
				return e.deferControls(ctx, state, next, changed)
			}
			next.Messages = append(next.Messages, model.CloneMessage(*command.Message))
			next.ControlCursor = command.Seq
			changed = true
		default:
			return e.deferControls(ctx, state, next, changed)
		}
	}
	if !changed && next.ControlCursor == state.ControlCursor {
		return state, nil, nil
	}
	committed, commitErr := e.commit(ctx, state, next)
	return committed, nil, commitErr
}

func (e *Engine) deferControls(ctx context.Context, state, next State, changed bool) (State, *Outcome, error) {
	if !changed {
		return state, nil, nil
	}
	committed, err := e.commit(ctx, state, next)
	return committed, nil, err
}

func (e *Engine) commit(ctx context.Context, current, next State, events ...RuntimeEvent) (State, error) {
	next.Revision = current.Revision
	if err := ValidatePhaseTransition(current.Phase, next.Phase); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	if err := next.Validate(); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	committed, err := e.ports.Durability.Commit(ctx, Transition{ExpectedRevision: current.Revision, Next: next.Clone(), Events: events})
	if err != nil {
		return State{}, err
	}
	return validateCommittedState(current.Revision, committed)
}

func (e *Engine) park(ctx context.Context, current, next State, pause PauseState, events ...RuntimeEvent) (State, error) {
	next.Revision = current.Revision
	if err := ValidatePhaseTransition(current.Phase, next.Phase); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	if err := next.Validate(); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	committed, err := e.ports.Durability.Park(ctx, ParkTransition{
		Transition: Transition{ExpectedRevision: current.Revision, Next: next.Clone(), Events: events},
		Pause:      pause,
	})
	if err != nil {
		return State{}, err
	}
	return validateCommittedState(current.Revision, committed)
}

func (e *Engine) complete(ctx context.Context, current, next State, finalMessageID string, events ...RuntimeEvent) (State, error) {
	next.Revision = current.Revision
	if err := ValidatePhaseTransition(current.Phase, next.Phase); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	if err := next.Validate(); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	committed, err := e.ports.Durability.Complete(ctx, CompleteTransition{
		Transition:     Transition{ExpectedRevision: current.Revision, Next: next.Clone(), Events: events},
		FinalMessageID: finalMessageID,
	})
	if err != nil {
		return State{}, err
	}
	return validateCommittedState(current.Revision, committed)
}

func (e *Engine) fail(ctx context.Context, current State, code, message string, retryable bool) (Outcome, error) {
	return e.failFrom(ctx, current, current, code, message, retryable)
}

func (e *Engine) failFrom(ctx context.Context, current, basis State, code, message string, retryable bool) (Outcome, error) {
	failure := Failure{Code: code, Message: message, Retryable: retryable}
	next := terminalState(basis, PhaseFailed, failure)
	next.Revision = current.Revision
	if err := ValidatePhaseTransition(current.Phase, next.Phase); err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	committed, err := e.ports.Durability.Fail(ctx, TerminalTransition{
		Transition: Transition{
			ExpectedRevision: current.Revision,
			Next:             next,
			Events:           []RuntimeEvent{{Type: EventRuntimeFailed, Message: "Agent runtime failed.", Payload: failure}},
		},
		Failure: failure,
	})
	if err != nil {
		return Outcome{}, err
	}
	committed, err = validateCommittedState(current.Revision, committed)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: OutcomeFailed, State: committed, Failure: cloneFailure(&failure)}, nil
}

func (e *Engine) cancel(ctx context.Context, current State, code, message string) (Outcome, error) {
	failure := Failure{Code: code, Message: message}
	next := terminalState(current, PhaseCanceled, failure)
	if err := ValidatePhaseTransition(current.Phase, next.Phase); err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	committed, err := e.ports.Durability.Cancel(ctx, TerminalTransition{
		Transition: Transition{
			ExpectedRevision: current.Revision,
			Next:             next,
			Events:           []RuntimeEvent{{Type: EventRuntimeCanceled, Message: "Agent runtime was canceled.", Payload: failure}},
		},
		Failure: failure,
	})
	if err != nil {
		return Outcome{}, err
	}
	committed, err = validateCommittedState(current.Revision, committed)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: OutcomeCanceled, State: committed, Failure: cloneFailure(&failure)}, nil
}

func terminalState(current State, phase Phase, failure Failure) State {
	next := current.Clone()
	next.Phase = phase
	next.PendingModel = nil
	next.PendingCompaction = nil
	next.PendingToolBatch = nil
	next.PendingCompletion = nil
	next.Pause = nil
	next.Failure = &failure
	next.Revision = current.Revision
	return next
}

func validateCommittedState(previousRevision int64, committed State) (State, error) {
	if committed.Revision <= previousRevision {
		return State{}, fmt.Errorf("durability port did not advance state revision")
	}
	if err := committed.Validate(); err != nil {
		return State{}, fmt.Errorf("durability port returned invalid state: %w", err)
	}
	return committed.Clone(), nil
}

func toolCalls(message model.Message) []model.ToolCall {
	calls := make([]model.ToolCall, 0)
	for _, content := range message.Content {
		if content.Type == model.ContentToolCall && content.ToolCall != nil {
			call := *content.ToolCall
			call.Arguments = append([]byte(nil), call.Arguments...)
			calls = append(calls, call)
		}
	}
	return calls
}

func plannedCalls(state State, calls []model.ToolCall) []PlannedToolCall {
	planned := make([]PlannedToolCall, len(calls))
	for index, call := range calls {
		planned[index] = PlannedToolCall{
			Call: call, ExecutionMode: "sequential", SideEffect: "unknown", Idempotency: "unknown",
			IdempotencyKey: StableToolIdempotencyKey(state.SessionID, state.TurnID, call),
		}
	}
	return planned
}

func callsFromPlan(plan *ToolBatchPlan) []model.ToolCall {
	if plan == nil {
		return nil
	}
	calls := make([]model.ToolCall, len(plan.Calls))
	for index, planned := range plan.Calls {
		calls[index] = planned.Call
	}
	return calls
}

func validatePlannedCalls(source []model.ToolCall, plan ToolBatchPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if len(source) != len(plan.Calls) {
		return fmt.Errorf("planned tool count does not match source calls")
	}
	for index, call := range source {
		if plan.Calls[index].Call.ID != call.ID || plan.Calls[index].Call.Name != call.Name {
			return fmt.Errorf("planned tool order does not match source calls")
		}
	}
	return nil
}

func validateToolResults(plan ToolBatchPlan, result ToolBatchResult) error {
	if len(plan.Calls) != len(result.Results) {
		return fmt.Errorf("tool result count does not match planned calls")
	}
	for index, planned := range plan.Calls {
		toolResult := result.Results[index]
		if toolResult.CallID != planned.Call.ID || toolResult.Name != planned.Call.Name {
			return fmt.Errorf("tool results are not in source call order")
		}
		message := model.Message{ID: "validation", Role: model.RoleTool, Visibility: model.VisibilityInternal, Content: []model.Content{{Type: model.ContentToolResult, ToolResult: cloneToolResult(toolResult)}}}
		if err := message.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func cloneToolResult(result model.ToolResult) *model.ToolResult {
	cloned := result
	cloned.Content = model.CloneContent(result.Content)
	cloned.State = append([]byte(nil), result.State...)
	return &cloned
}

func validateCompletionVerdict(verdict CompletionVerdict) error {
	if strings.TrimSpace(verdict.ValidatorID) == "" {
		return fmt.Errorf("completion validator id is required")
	}
	switch verdict.Outcome {
	case CompletionPass:
		return nil
	case CompletionRetry:
		if strings.TrimSpace(verdict.Feedback) == "" {
			return fmt.Errorf("completion retry feedback is required")
		}
		return nil
	case CompletionFail:
		if strings.TrimSpace(verdict.Reason) == "" {
			return fmt.Errorf("completion failure reason is required")
		}
		return nil
	default:
		return fmt.Errorf("unsupported completion outcome %q", verdict.Outcome)
	}
}

func markMessagePublic(messages []model.Message, id string) bool {
	for index := range messages {
		if messages[index].ID == id {
			messages[index].Visibility = model.VisibilityPublic
			return true
		}
	}
	return false
}

func finalPublicAssistantMessage(messages []model.Message) (model.Message, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == model.RoleAssistant && messages[index].Visibility == model.VisibilityPublic {
			return model.CloneMessage(messages[index]), true
		}
	}
	return model.Message{}, false
}

func (e *Engine) publishLive(ctx context.Context, attempt PendingModelAttempt, delta model.Delta) {
	if e.ports.Live == nil || (delta.Type != model.DeltaText && delta.Type != model.DeltaThinking) {
		return
	}
	_ = e.ports.Live.Publish(ctx, model.LiveDelta{
		StreamID:  attempt.ID,
		Attempt:   attempt.Number,
		Index:     delta.Index,
		Operation: "append",
		Kind:      string(delta.Type),
		Text:      delta.Text,
		CreatedAt: e.ports.Clock.Now(),
	})
}

func (e *Engine) publishReset(ctx context.Context, attempt PendingModelAttempt) {
	if e.ports.Live == nil {
		return
	}
	_ = e.ports.Live.Publish(ctx, model.LiveDelta{
		StreamID:  attempt.ID,
		Attempt:   attempt.Number,
		Operation: "reset",
		Kind:      "status",
		CreatedAt: e.ports.Clock.Now(),
	})
}

func failureFromError(err error) Failure {
	var providerError *model.ProviderError
	if errors.As(err, &providerError) {
		return Failure{Code: defaultString(providerError.Code, string(providerError.Class)), Message: providerError.Error(), Retryable: providerError.Retryable}
	}
	var budgetError *BudgetExceededError
	if errors.As(err, &budgetError) {
		return Failure{Code: "budget_exhausted", Message: budgetError.Error()}
	}
	return Failure{Code: "model_request_failed", Message: "model request failed"}
}

func defaultPurpose(value model.RequestPurpose) model.RequestPurpose {
	if value == "" {
		return model.PurposeAgent
	}
	return value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
