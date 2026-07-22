package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tokenestimate"
)

type LLMCompactor struct {
	Model           agentcore.ModelPort
	Route           coremodel.Route
	ThresholdTokens int
	MaxOutputTokens int
	SummaryMaxChars int
}

var _ agentcore.CompactionPort = LLMCompactor{}

func (c LLMCompactor) NeedsCompaction(state agentcore.State) bool {
	if c.Model == nil || c.ThresholdTokens <= 0 {
		return false
	}
	estimated := int64(estimateCoreMessages(state.Messages))
	trigger := int64(c.ThresholdTokens)
	if state.Context.CompactionCount > 0 && state.Context.EstimatedInputTokens >= trigger {
		trigger = state.Context.EstimatedInputTokens + int64(c.ThresholdTokens)
	}
	return estimated > trigger
}

func (c LLMCompactor) Compact(ctx context.Context, state agentcore.State, attemptID string) (agentcore.CompactionResult, error) {
	if c.Model == nil {
		return agentcore.CompactionResult{}, errors.New("compaction model is required")
	}
	raw, err := json.Marshal(state.Messages)
	if err != nil {
		return agentcore.CompactionResult{}, fmt.Errorf("encode compaction messages: %w", err)
	}
	request := coremodel.Request{
		Purpose: coremodel.PurposeCompaction,
		Route:   cloneModelRoute(c.Route),
		Messages: []coremodel.Message{
			{
				ID: "compaction_system_" + attemptID, Role: coremodel.RoleSystem, Visibility: coremodel.VisibilityInternal,
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "Summarize the agent conversation for continued execution. Preserve the objective, user constraints, decisions, tool outcomes, failures, file paths, commands, unresolved work, and facts needed by the next model call. Do not issue tool calls."}},
			},
			{
				ID: "compaction_input_" + attemptID, Role: coremodel.RoleUser, Visibility: coremodel.VisibilityInternal,
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: string(raw)}},
			},
		},
		MaxOutputTokens: positiveCompactionInt(c.MaxOutputTokens, 4096),
		SessionID:       state.SessionID, TurnID: state.TurnID, AttemptID: attemptID,
	}
	response, err := c.Model.Generate(ctx, request, nil)
	if err != nil {
		return agentcore.CompactionResult{}, err
	}
	if response.StopReason != coremodel.StopReasonComplete || containsToolCall(response.Message) {
		return agentcore.CompactionResult{}, errors.New("compaction model returned an incomplete or tool-calling response")
	}
	summary := strings.TrimSpace(flattenModelContent(response.Message.Content))
	summary = truncateCompactionSummary(summary, c.SummaryMaxChars)
	if summary == "" {
		return agentcore.CompactionResult{}, errors.New("compaction model returned an empty summary")
	}
	estimated := tokenestimate.Text(summary)
	if latest, ok := latestPublicUserMessage(state.Messages); ok {
		rawLatest, _ := json.Marshal(latest)
		estimated += tokenestimate.Text(string(rawLatest))
	}
	return agentcore.CompactionResult{Summary: summary, Usage: response.Usage, EstimatedInputTokens: int64(estimated)}, nil
}

func estimateCoreMessages(messages []coremodel.Message) int {
	raw, err := json.Marshal(messages)
	if err != nil {
		return 0
	}
	return tokenestimate.Text(string(raw))
}

func containsToolCall(message coremodel.Message) bool {
	for _, content := range message.Content {
		if content.ToolCall != nil {
			return true
		}
	}
	return false
}

func latestPublicUserMessage(messages []coremodel.Message) (coremodel.Message, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == coremodel.RoleUser && messages[index].Visibility == coremodel.VisibilityPublic {
			return coremodel.CloneMessage(messages[index]), true
		}
	}
	return coremodel.Message{}, false
}

func truncateCompactionSummary(value string, maximum int) string {
	maximum = positiveCompactionInt(maximum, 12000)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maximum {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:maximum])) + "\n[Compaction summary truncated.]"
}

func positiveCompactionInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
