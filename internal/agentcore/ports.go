package agentcore

import (
	"context"
	"errors"
	"strings"
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
	// ValidateExecution checks the complete durable batch without executing a
	// tool. The Engine calls it before persisting any started journal entry.
	ValidateExecution(context.Context, State, ToolBatchPlan) error
	Execute(context.Context, State, ToolBatchPlan) (ToolBatchResult, error)
}

// ToolFatalError marks a ToolPort infrastructure failure that must terminate
// the loop instead of being returned to the model as an execution result.
type ToolFatalError struct {
	Code  string
	Cause error
}

// ToolResultError is a recoverable tool failure whose code and message are
// explicitly safe to return to the model. Cause remains available through the
// error chain for runtime diagnostics and must not be placed in model context.
type ToolResultError struct {
	code      string
	message   string
	retryable bool
	cause     error
}

func NewToolResultError(code, message string, retryable bool, cause error) *ToolResultError {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "tool_execution_failed"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Tool execution failed. Retry or use another approach."
	}
	return &ToolResultError{code: code, message: message, retryable: retryable, cause: cause}
}

func (e *ToolResultError) Error() string {
	if e == nil || e.message == "" {
		return "Tool execution failed. Retry or use another approach."
	}
	return e.message
}

func (e *ToolResultError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *ToolResultError) Code() string {
	if e == nil || e.code == "" {
		return "tool_execution_failed"
	}
	return e.code
}

func (e *ToolResultError) Retryable() bool {
	return e != nil && e.retryable
}

func NewToolFatalError(code string, cause error) *ToolFatalError {
	if cause == nil {
		cause = errors.New("tool runtime infrastructure failure")
	}
	return &ToolFatalError{Code: strings.TrimSpace(code), Cause: cause}
}

func (e *ToolFatalError) Error() string {
	if e == nil || e.Cause == nil {
		return "tool runtime infrastructure failure"
	}
	return e.Cause.Error()
}

func (e *ToolFatalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ToolFatalError) ErrorCode() string {
	if e == nil || strings.TrimSpace(e.Code) == "" {
		return "tool_runtime_failed"
	}
	return strings.TrimSpace(e.Code)
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
