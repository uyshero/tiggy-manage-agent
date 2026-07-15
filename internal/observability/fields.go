package observability

import (
	"fmt"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type EventTraceFieldsInput struct {
	SessionID       string
	TurnID          string
	EventType       string
	CallID          string
	Identifier      string
	APIName         string
	Status          string
	Duration        time.Duration
	ParentSpanID    string
	InteractionRoot bool
}

func EventTraceFields(input EventTraceFieldsInput) map[string]any {
	traceID := traceID(input.SessionID, input.TurnID)
	kind := spanKind(input.EventType)
	interactionRoot := input.InteractionRoot || isInteractionEvent(input.EventType)
	if interactionRoot {
		kind = "interaction"
		if input.EventType == managedagents.EventRuntimeStarted && input.Status == "" {
			input.Status = "running"
		}
	}
	fields := map[string]any{
		"trace_id":  traceID,
		"span_id":   eventSpanID(input),
		"span_name": eventSpanName(input),
		"span_kind": kind,
	}
	parent := input.ParentSpanID
	if parent == "" && !interactionRoot {
		parent = InteractionSpanID(input.TurnID)
	}
	if parent != "" {
		fields["parent_span_id"] = parent
	}
	if input.Status != "" {
		fields["span_status"] = input.Status
	}
	if input.Duration > 0 {
		fields["duration_ms"] = input.Duration.Milliseconds()
	}
	return fields
}

func InteractionSpanID(turnID string) string {
	return spanIDFromKey("interaction:" + turnID)
}

func ToolSpanID(turnID string, callID string, seqFallback int64) string {
	callKey := callID
	if callKey == "" && seqFallback > 0 {
		callKey = fmt.Sprintf("tool-%d", seqFallback)
	}
	return spanIDFromKey("tool:" + turnID + ":" + callKey)
}

func eventSpanID(input EventTraceFieldsInput) string {
	switch input.EventType {
	case managedagents.EventRuntimeStarted, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		return InteractionSpanID(input.TurnID)
	case managedagents.EventRuntimeToolCall, managedagents.EventRuntimeToolResult:
		return ToolSpanID(input.TurnID, input.CallID, 0)
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
		callKey := input.CallID
		if callKey == "" {
			callKey = "approval"
		}
		return ApprovalSpanID(input.TurnID, callKey)
	case managedagents.EventRuntimeLLMRequest, managedagents.EventRuntimeLLMResponse:
		round := input.CallID
		if round == "" {
			round = "0"
		}
		return spanIDFromKey("llm:" + input.TurnID + ":" + round)
	case managedagents.EventRuntimeContextCompacting, managedagents.EventRuntimeContextCompacted, managedagents.EventRuntimeContextCompactionFailed:
		return spanIDFromKey("context:" + input.TurnID)
	default:
		return spanIDFromKey("event:" + input.TurnID + ":" + input.EventType)
	}
}

func eventSpanName(input EventTraceFieldsInput) string {
	switch input.EventType {
	case managedagents.EventRuntimeStarted, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		return "tma.interaction"
	case managedagents.EventRuntimeToolCall, managedagents.EventRuntimeToolResult:
		apiName := input.APIName
		if apiName == "" {
			return "tma.tool"
		}
		return "tma.tool." + defaultString(input.Identifier, "default") + "." + apiName
	case managedagents.EventRuntimeLLMRequest, managedagents.EventRuntimeLLMResponse:
		return "tma.llm"
	default:
		return spanName(TraceStep{Type: input.EventType, Identifier: input.Identifier, APIName: input.APIName})
	}
}

func ApprovalSpanID(turnID string, callID string) string {
	if callID == "" {
		callID = "approval"
	}
	return spanIDFromKey("approval:" + turnID + ":" + callID)
}

func isInteractionEvent(eventType string) bool {
	switch eventType {
	case managedagents.EventRuntimeStarted, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		return true
	default:
		return false
	}
}
