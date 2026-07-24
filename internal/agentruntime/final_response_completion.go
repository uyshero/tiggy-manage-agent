package agentruntime

import (
	"context"
	"strings"
)

const finalResponseCompletionValidator = "builtin.final_response"

// FinalResponseCompletionGate prevents an internal-only model response from
// completing a turn without any user-visible answer.
type FinalResponseCompletionGate struct{}

func (FinalResponseCompletionGate) Validate(_ context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	if strings.TrimSpace(responseText(candidate)) != "" {
		return CompletionVerdict{
			Outcome:   CompletionOutcomePass,
			Validator: finalResponseCompletionValidator,
			Evidence:  map[string]any{"visible_response": true},
		}, nil
	}
	return CompletionVerdict{
		Outcome:   CompletionOutcomeRetry,
		Validator: finalResponseCompletionValidator,
		Reason:    "completion candidate has no user-visible text",
		Feedback:  "The previous response contained no user-visible final answer. Continue any intended tool work using valid native tool calls, or provide a concise final response that clearly states the outcome, produced deliverables, and any remaining failure. Do not finish with thinking or reasoning content only.",
		Evidence:  map[string]any{"visible_response": false},
	}, nil
}
