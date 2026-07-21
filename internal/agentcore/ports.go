package agentcore

import (
	"context"
	"time"

	"tiggy-manage-agent/internal/model"
)

type DeltaSink func(model.Delta) error

type ModelPort interface {
	Generate(context.Context, model.Request, DeltaSink) (model.Response, error)
}

type ContextPort interface {
	Build(context.Context, State) (model.Request, error)
}

type CompactionResult struct {
	Summary              string      `json:"summary"`
	Usage                model.Usage `json:"usage"`
	EstimatedInputTokens int64       `json:"estimated_input_tokens,omitempty"`
}

type CompactionPort interface {
	NeedsCompaction(State) bool
	Compact(context.Context, State, string) (CompactionResult, error)
}

type ToolPort interface {
	Preflight(context.Context, State, []model.ToolCall) (ToolBatchPlan, error)
	Execute(context.Context, State, ToolBatchPlan) (ToolBatchResult, error)
}

type CompletionOutcome string

const (
	CompletionPass  CompletionOutcome = "pass"
	CompletionRetry CompletionOutcome = "retry"
	CompletionFail  CompletionOutcome = "fail"
)

type CompletionCandidate struct {
	Message model.Message
	Attempt int
	State   State
}

type CompletionVerdict struct {
	Outcome          CompletionOutcome `json:"outcome"`
	ValidatorID      string            `json:"validator_id"`
	ValidatorVersion string            `json:"validator_version,omitempty"`
	ReasonCode       string            `json:"reason_code,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	Feedback         string            `json:"feedback,omitempty"`
	EvidenceRefs     []string          `json:"evidence_refs,omitempty"`
}

type CompletionPort interface {
	Validate(context.Context, CompletionCandidate) (CompletionVerdict, error)
}

type ControlMode string

const (
	ControlSteer    ControlMode = "steer"
	ControlFollowUp ControlMode = "follow_up"
	ControlCancel   ControlMode = "cancel"
)

type ControlPoint string

const (
	ControlBeforeModel    ControlPoint = "before_model"
	ControlBeforeComplete ControlPoint = "before_complete"
)

type ControlCommand struct {
	Seq     int64          `json:"seq"`
	Mode    ControlMode    `json:"mode"`
	Message *model.Message `json:"message,omitempty"`
	Reason  string         `json:"reason,omitempty"`
}

type ControlPort interface {
	Drain(context.Context, State, ControlPoint) ([]ControlCommand, error)
}

type DurabilityPort interface {
	Commit(context.Context, Transition) (State, error)
	Park(context.Context, ParkTransition) (State, error)
	Complete(context.Context, CompleteTransition) (State, error)
	Fail(context.Context, TerminalTransition) (State, error)
	Cancel(context.Context, TerminalTransition) (State, error)
}

type StateRepository interface {
	DurabilityPort
	Load(context.Context, string, string) (State, error)
}

type LivePort interface {
	Publish(context.Context, model.LiveDelta) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID(prefix string) string
}

type Ports struct {
	Model      ModelPort
	Context    ContextPort
	Compaction CompactionPort
	Tools      ToolPort
	Completion CompletionPort
	Controls   ControlPort
	Durability DurabilityPort
	Live       LivePort
	Clock      Clock
	IDs        IDGenerator
}

type OutcomeStatus string

const (
	OutcomeCompleted OutcomeStatus = "completed"
	OutcomePaused    OutcomeStatus = "paused"
	OutcomeFailed    OutcomeStatus = "failed"
	OutcomeCanceled  OutcomeStatus = "canceled"
)

type Outcome struct {
	Status       OutcomeStatus  `json:"status"`
	State        State          `json:"state"`
	FinalMessage *model.Message `json:"final_message,omitempty"`
	Pause        *PauseState    `json:"pause,omitempty"`
	Failure      *Failure       `json:"failure,omitempty"`
}
