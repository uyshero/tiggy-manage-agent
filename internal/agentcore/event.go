package agentcore

type EventType string

const (
	EventRuntimeStarted       EventType = "runtime.started"
	EventModelRequested       EventType = "model.requested"
	EventModelAbandoned       EventType = "model.abandoned"
	EventModelResponded       EventType = "model.responded"
	EventContextCompacting    EventType = "context.compacting"
	EventContextCompacted     EventType = "context.compacted"
	EventContextAbandoned     EventType = "context.compaction_abandoned"
	EventToolBatchPlanned     EventType = "tool.batch_planned"
	EventToolCallStarted      EventType = "tool.call_started"
	EventToolCallResult       EventType = "tool.call_result"
	EventInterventionRequired EventType = "intervention.required"
	EventInterventionResolved EventType = "intervention.resolved"
	EventToolBatchCompleted   EventType = "tool.batch_completed"
	EventCompletionStarted    EventType = "completion.started"
	EventCompletionRetried    EventType = "completion.retried"
	EventCompletionValidated  EventType = "completion.validated"
	EventRuntimeCompleted     EventType = "runtime.completed"
	EventRuntimeFailed        EventType = "runtime.failed"
	EventRuntimeCanceled      EventType = "runtime.canceled"
)

type RuntimeEvent struct {
	Type    EventType `json:"type"`
	Message string    `json:"message,omitempty"`
	Payload any       `json:"payload,omitempty"`
}

type Transition struct {
	ExpectedRevision int64          `json:"expected_revision"`
	Next             State          `json:"next"`
	Events           []RuntimeEvent `json:"events,omitempty"`
}

type ParkTransition struct {
	Transition
	Pause PauseState `json:"pause"`
}

type CompleteTransition struct {
	Transition
	FinalMessageID string `json:"final_message_id"`
}

type TerminalTransition struct {
	Transition
	Failure Failure `json:"failure"`
}
