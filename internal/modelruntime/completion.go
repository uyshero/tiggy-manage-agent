package modelruntime

import (
	"context"
	"fmt"
	"sort"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/llm"
)

type CompletionGate struct {
	Gate       agentruntime.CompletionGate
	MaxRetries int
}

var _ agentcore.CompletionPort = CompletionGate{}

func (a CompletionGate) Validate(ctx context.Context, candidate agentcore.CompletionCandidate) (agentcore.CompletionVerdict, error) {
	if a.Gate == nil {
		return agentcore.CompletionVerdict{Outcome: agentcore.CompletionPass, ValidatorID: "builtin.pass"}, nil
	}
	messages := make([]llm.Message, 0, len(candidate.State.Messages))
	for index, message := range candidate.State.Messages {
		converted, err := toLLMMessage(message)
		if err != nil {
			return agentcore.CompletionVerdict{}, fmt.Errorf("convert completion message %d: %w", index, err)
		}
		messages = append(messages, converted)
	}
	responseMessage, err := toLLMMessage(candidate.Message)
	if err != nil {
		return agentcore.CompletionVerdict{}, fmt.Errorf("convert completion candidate: %w", err)
	}
	toolExecutions := make([]agentruntime.CompletionToolExecution, 0, len(candidate.State.ToolJournal))
	for _, entry := range candidate.State.ToolJournal {
		isError := entry.Result != nil && entry.Result.IsError
		toolExecutions = append(toolExecutions, agentruntime.CompletionToolExecution{
			CallID: entry.CallID, Name: entry.Name, Status: string(entry.Status), IsError: isError,
		})
	}
	legacy, err := a.Gate.Validate(ctx, agentruntime.CompletionCandidate{
		SessionID:      candidate.State.SessionID,
		TurnID:         candidate.State.TurnID,
		ToolRound:      candidate.State.Round,
		Attempt:        candidate.Attempt,
		Response:       llm.Response{Message: responseMessage},
		Messages:       messages,
		ActiveTools:    append([]string(nil), candidate.State.ActiveTools...),
		ToolExecutions: toolExecutions,
	})
	if err != nil {
		return agentcore.CompletionVerdict{}, fmt.Errorf("completion validator %s: %w", legacy.Validator, err)
	}
	if legacy.Outcome == agentruntime.CompletionOutcomeRetry && a.MaxRetries > 0 && candidate.State.CompletionAttempts > a.MaxRetries {
		return agentcore.CompletionVerdict{
			Outcome: agentcore.CompletionFail, ValidatorID: legacy.Validator,
			ReasonCode: "completion_retry_exhausted",
			Reason:     fmt.Sprintf("completion retry limit reached after %d blocked attempt(s)", a.MaxRetries),
		}, nil
	}
	return agentcore.CompletionVerdict{
		Outcome:      agentcore.CompletionOutcome(legacy.Outcome),
		ValidatorID:  legacy.Validator,
		Reason:       legacy.Reason,
		Feedback:     legacy.Feedback,
		EvidenceRefs: completionEvidenceRefs(legacy.Evidence),
	}, nil
}

func completionEvidenceRefs(evidence map[string]any) []string {
	if len(evidence) == 0 {
		return nil
	}
	refs := make([]string, 0, len(evidence))
	for key := range evidence {
		refs = append(refs, key)
	}
	sort.Strings(refs)
	return refs
}
