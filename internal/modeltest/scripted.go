package modeltest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
)

type ModelStep struct {
	Deltas   []model.Delta
	Response model.Response
	Error    error
	Assert   func(model.Request) error
}

type ScriptedModel struct {
	mu       sync.Mutex
	steps    []ModelStep
	requests []model.Request
}

func NewScriptedModel(steps ...ModelStep) *ScriptedModel {
	return &ScriptedModel{steps: append([]ModelStep(nil), steps...)}
}

func (m *ScriptedModel) Generate(_ context.Context, request model.Request, sink agentcore.DeltaSink) (model.Response, error) {
	m.mu.Lock()
	if len(m.steps) == 0 {
		m.mu.Unlock()
		return model.Response{}, errors.New("scripted model has no remaining step")
	}
	step := m.steps[0]
	m.steps = m.steps[1:]
	m.requests = append(m.requests, request)
	m.mu.Unlock()

	if step.Assert != nil {
		if err := step.Assert(request); err != nil {
			return model.Response{}, fmt.Errorf("assert scripted model request: %w", err)
		}
	}
	for _, delta := range step.Deltas {
		if sink != nil {
			if err := sink(delta); err != nil {
				return model.Response{}, err
			}
		}
	}
	if step.Error != nil {
		return model.Response{}, step.Error
	}
	return step.Response, nil
}

func (m *ScriptedModel) Requests() []model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.Request(nil), m.requests...)
}

func (m *ScriptedModel) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps)
}

type StaticContext struct {
	Route           model.Route
	Tools           []model.ToolDefinition
	MaxOutputTokens int
}

func (c StaticContext) Build(_ context.Context, state agentcore.State) (model.Request, error) {
	return model.Request{
		Purpose:         model.PurposeAgent,
		Route:           c.Route,
		Messages:        model.CloneMessages(state.Messages),
		Tools:           append([]model.ToolDefinition(nil), c.Tools...),
		MaxOutputTokens: c.MaxOutputTokens,
	}, nil
}

type SequenceIDs struct {
	mu       sync.Mutex
	counters map[string]int
}

func NewSequenceIDs() *SequenceIDs {
	return &SequenceIDs{counters: map[string]int{}}
}

func (g *SequenceIDs) NewID(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counters[prefix]++
	return fmt.Sprintf("%s_%06d", prefix, g.counters[prefix])
}

type FixedClock struct {
	Time time.Time
}

func (c FixedClock) Now() time.Time {
	return c.Time
}

type MemoryDurability struct {
	mu           sync.Mutex
	state        agentcore.State
	events       []agentcore.RuntimeEvent
	eventBatches [][]agentcore.RuntimeEvent
	transitions  []string
}

func NewMemoryDurability(initial agentcore.State) *MemoryDurability {
	return &MemoryDurability{state: initial.Clone()}
}

func (d *MemoryDurability) Commit(_ context.Context, transition agentcore.Transition) (agentcore.State, error) {
	return d.apply("commit", transition)
}

func (d *MemoryDurability) Park(_ context.Context, transition agentcore.ParkTransition) (agentcore.State, error) {
	if transition.Next.Phase != agentcore.PhasePaused || transition.Next.Pause == nil || !reflect.DeepEqual(*transition.Next.Pause, transition.Pause) {
		return agentcore.State{}, errors.New("park transition does not match paused state")
	}
	return d.apply("park", transition.Transition)
}

func (d *MemoryDurability) Complete(_ context.Context, transition agentcore.CompleteTransition) (agentcore.State, error) {
	if transition.Next.Phase != agentcore.PhaseCompleted || transition.FinalMessageID == "" {
		return agentcore.State{}, errors.New("complete transition is incomplete")
	}
	found := false
	for _, message := range transition.Next.Messages {
		if message.ID == transition.FinalMessageID && message.Role == model.RoleAssistant && message.Visibility == model.VisibilityPublic {
			found = true
			break
		}
	}
	if !found {
		return agentcore.State{}, errors.New("complete transition final message is not public")
	}
	return d.apply("complete", transition.Transition)
}

func (d *MemoryDurability) Fail(_ context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	if transition.Next.Phase != agentcore.PhaseFailed || transition.Next.Failure == nil || !reflect.DeepEqual(*transition.Next.Failure, transition.Failure) {
		return agentcore.State{}, errors.New("fail transition does not match failed state")
	}
	return d.apply("fail", transition.Transition)
}

func (d *MemoryDurability) Cancel(_ context.Context, transition agentcore.TerminalTransition) (agentcore.State, error) {
	if transition.Next.Phase != agentcore.PhaseCanceled || transition.Next.Failure == nil || !reflect.DeepEqual(*transition.Next.Failure, transition.Failure) {
		return agentcore.State{}, errors.New("cancel transition does not match canceled state")
	}
	return d.apply("cancel", transition.Transition)
}

func (d *MemoryDurability) apply(kind string, transition agentcore.Transition) (agentcore.State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if transition.ExpectedRevision != d.state.Revision {
		return agentcore.State{}, fmt.Errorf("revision conflict: expected=%d actual=%d", transition.ExpectedRevision, d.state.Revision)
	}
	if transition.Next.SessionID != d.state.SessionID || transition.Next.TurnID != d.state.TurnID {
		return agentcore.State{}, errors.New("transition changed turn identity")
	}
	next := transition.Next.Clone()
	next.Revision = d.state.Revision + 1
	if err := next.Validate(); err != nil {
		return agentcore.State{}, err
	}
	d.state = next
	d.events = append(d.events, transition.Events...)
	d.eventBatches = append(d.eventBatches, append([]agentcore.RuntimeEvent(nil), transition.Events...))
	d.transitions = append(d.transitions, kind)
	return next.Clone(), nil
}

func (d *MemoryDurability) Snapshot() agentcore.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Clone()
}

func (d *MemoryDurability) Events() []agentcore.RuntimeEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]agentcore.RuntimeEvent(nil), d.events...)
}

func (d *MemoryDurability) EventBatches() [][]agentcore.RuntimeEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([][]agentcore.RuntimeEvent, len(d.eventBatches))
	for index := range d.eventBatches {
		result[index] = append([]agentcore.RuntimeEvent(nil), d.eventBatches[index]...)
	}
	return result
}

func (d *MemoryDurability) Transitions() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.transitions...)
}

type ScriptedTools struct {
	PreflightFunc         func(context.Context, agentcore.State, []model.ToolCall) (agentcore.ToolBatchPlan, error)
	ValidateExecutionFunc func(context.Context, agentcore.State, agentcore.ToolBatchPlan) error
	ExecuteFunc           func(context.Context, agentcore.State, agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error)

	mu                     sync.Mutex
	preflightCalls         int
	validateExecutionCalls int
	executeCalls           int
}

func (t *ScriptedTools) ValidateExecution(ctx context.Context, state agentcore.State, plan agentcore.ToolBatchPlan) error {
	t.mu.Lock()
	t.validateExecutionCalls++
	t.mu.Unlock()
	if t.ValidateExecutionFunc == nil {
		return nil
	}
	return t.ValidateExecutionFunc(ctx, state, plan)
}

func (t *ScriptedTools) Preflight(ctx context.Context, state agentcore.State, calls []model.ToolCall) (agentcore.ToolBatchPlan, error) {
	t.mu.Lock()
	t.preflightCalls++
	t.mu.Unlock()
	if t.PreflightFunc == nil {
		planned := make([]agentcore.PlannedToolCall, len(calls))
		for index, call := range calls {
			planned[index] = agentcore.PlannedToolCall{
				Call: call, ExecutionMode: "parallel", SideEffect: "none", Idempotency: "safe",
				IdempotencyKey: agentcore.StableToolIdempotencyKey(state.SessionID, state.TurnID, call),
				Disposition:    agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
				ApprovalState: agentcore.ToolApprovalNotRequired,
			}
		}
		return agentcore.ToolBatchPlan{Calls: planned}, nil
	}
	return t.PreflightFunc(ctx, state, calls)
}

func (t *ScriptedTools) Execute(ctx context.Context, state agentcore.State, plan agentcore.ToolBatchPlan) (agentcore.ToolBatchResult, error) {
	t.mu.Lock()
	t.executeCalls++
	t.mu.Unlock()
	if t.ExecuteFunc == nil {
		results := make([]model.ToolResult, len(plan.Calls))
		for index, planned := range plan.Calls {
			results[index] = model.ToolResult{
				CallID:  planned.Call.ID,
				Name:    planned.Call.Name,
				Content: []model.Content{{Type: model.ContentText, Text: "ok"}},
			}
		}
		return agentcore.ToolBatchResult{Results: results}, nil
	}
	return t.ExecuteFunc(ctx, state, plan)
}

func (t *ScriptedTools) Counts() (preflight int, execute int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.preflightCalls, t.executeCalls
}

type ScriptedCompletion struct {
	mu       sync.Mutex
	verdicts []agentcore.CompletionVerdict
	calls    int
}

func NewScriptedCompletion(verdicts ...agentcore.CompletionVerdict) *ScriptedCompletion {
	return &ScriptedCompletion{verdicts: append([]agentcore.CompletionVerdict(nil), verdicts...)}
}

func (c *ScriptedCompletion) Validate(_ context.Context, _ agentcore.CompletionCandidate) (agentcore.CompletionVerdict, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if len(c.verdicts) == 0 {
		return agentcore.CompletionVerdict{}, errors.New("scripted completion has no remaining verdict")
	}
	verdict := c.verdicts[0]
	c.verdicts = c.verdicts[1:]
	return verdict, nil
}

func (c *ScriptedCompletion) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
