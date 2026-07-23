package agentruntime

import (
	"context"
	"strings"
)

const toolMarkupCompletionValidator = "builtin.tool_markup"

// ToolMarkupCompletionGate prevents provider-specific serialized tool markup
// from being published as a final assistant answer when adaptation failed.
type ToolMarkupCompletionGate struct{}

func (ToolMarkupCompletionGate) Validate(_ context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	text := responseText(candidate)
	if !strings.Contains(text, "<seed:tool_call") && !strings.Contains(text, "</seed:tool_call>") {
		return CompletionVerdict{
			Outcome:   CompletionOutcomePass,
			Validator: toolMarkupCompletionValidator,
			Evidence:  map[string]any{"serialized_tool_markup": false},
		}, nil
	}
	return CompletionVerdict{
		Outcome:   CompletionOutcomeRetry,
		Validator: toolMarkupCompletionValidator,
		Reason:    "provider tool-call markup was not decoded",
		Feedback:  "The previous response contained serialized <seed:tool_call> markup instead of a valid native tool call. Re-issue the intended tool through the available function-calling interface. Do not present tool-call markup as a final answer.",
		Evidence:  map[string]any{"serialized_tool_markup": true},
	}, nil
}
