package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	raw, err := json.Marshal(projectCompactionMessages(state.Messages))
	if err != nil {
		return agentcore.CompactionResult{}, fmt.Errorf("encode compaction messages: %w", err)
	}
	request := coremodel.Request{
		Purpose: coremodel.PurposeCompaction,
		Route:   cloneModelRoute(c.Route),
		Messages: []coremodel.Message{
			{
				ID: "compaction_system_" + attemptID, Role: coremodel.RoleSystem, Visibility: coremodel.VisibilityInternal,
				Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "Summarize the agent conversation for continued execution. Preserve the objective, user constraints, decisions, tool outcomes, failures, file paths, commands, unresolved work, and facts needed by the next model call. Preserve Artifact IDs and paging cursors as references; summarize large Artifact or tool-result content instead of copying it. Do not issue tool calls."}},
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

const (
	compactionToolResultMaxChars = 4096
	compactionToolStateMaxBytes  = 4096
)

func projectCompactionMessages(messages []coremodel.Message) []coremodel.Message {
	projected := make([]coremodel.Message, len(messages))
	for messageIndex, message := range messages {
		projected[messageIndex] = coremodel.CloneMessage(message)
		for contentIndex := range projected[messageIndex].Content {
			content := &projected[messageIndex].Content[contentIndex]
			if content.Type != coremodel.ContentToolResult || content.ToolResult == nil {
				continue
			}
			projectToolResultForCompaction(content.ToolResult)
		}
	}
	return projected
}

func projectToolResultForCompaction(result *coremodel.ToolResult) {
	artifactIDs := toolResultArtifactIDs(result)
	if len(artifactIDs) == 0 {
		return
	}
	for index := range result.Content {
		content := &result.Content[index]
		if content.Type != coremodel.ContentText || len([]rune(content.Text)) <= compactionToolResultMaxChars {
			continue
		}
		if projected, ok := projectToolResultEnvelope(content.Text, artifactIDs); ok {
			content.Text = projected
		}
	}
	if len(result.State) > compactionToolStateMaxBytes {
		result.State = mustMarshalCompactionProjection(map[string]any{
			"compaction_projection": "artifact_reference",
			"original_state_bytes":  len(result.State),
			"artifact_ids":          artifactIDs,
		})
	}
}

func toolResultArtifactIDs(result *coremodel.ToolResult) []string {
	seen := map[string]bool{}
	for _, content := range result.Content {
		if content.Type != coremodel.ContentText {
			continue
		}
		var envelope map[string]any
		if json.Unmarshal([]byte(content.Text), &envelope) != nil {
			continue
		}
		if contextValue, ok := envelope["context"].(map[string]any); ok {
			for _, key := range []string{"result_artifact_id", "content_artifact_id", "state_artifact_id"} {
				if value, ok := contextValue[key].(string); ok && strings.TrimSpace(value) != "" {
					seen[strings.TrimSpace(value)] = true
				}
			}
		}
		if artifacts, ok := envelope["artifacts"].([]any); ok {
			for _, item := range artifacts {
				artifact, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if value, ok := artifact["artifact_id"].(string); ok && strings.TrimSpace(value) != "" {
					seen[strings.TrimSpace(value)] = true
				}
			}
		}
	}
	artifactIDs := make([]string, 0, len(seen))
	for artifactID := range seen {
		artifactIDs = append(artifactIDs, artifactID)
	}
	sort.Strings(artifactIDs)
	return artifactIDs
}

func projectToolResultEnvelope(text string, artifactIDs []string) (string, bool) {
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return "", false
	}
	projected := map[string]any{
		"compaction_projection": "artifact_reference",
		"artifact_ids":          artifactIDs,
		"original_chars":        len([]rune(text)),
		"content":               "Tool result content omitted from compaction input; use artifact_read with the referenced Artifact ID when details are needed.",
	}
	for _, key := range []string{
		"protocol_version", "id", "identifier", "api_name", "success", "pending_intervention",
		"error", "recoverable", "retryable", "redacted", "artifact_error", "context", "artifacts",
	} {
		if value, ok := envelope[key]; ok && value != nil {
			projected[key] = value
		}
	}
	projected["state"] = map[string]any{
		"omitted_from_compaction": true,
		"artifact_ids":            artifactIDs,
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func mustMarshalCompactionProjection(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"compaction_projection":"artifact_reference"}`)
	}
	return encoded
}

func estimateCoreMessages(messages []coremodel.Message) int {
	total := 0
	for _, message := range messages {
		// Keep this aligned with the text the model sees. Marshaling the whole
		// message slice counts JSON transport escaping as prompt content and can
		// trigger compaction far too early for JSON-heavy tool manifests.
		total += 4
		for _, content := range message.Content {
			total += estimateCoreContent(content)
		}
	}
	return total
}

func estimateCoreContent(content coremodel.Content) int {
	switch content.Type {
	case coremodel.ContentText:
		return tokenestimate.Text(content.Text)
	case coremodel.ContentImage:
		return 256
	case coremodel.ContentThinking:
		if content.Thinking == nil {
			return 0
		}
		return tokenestimate.Text(content.Thinking.Text) + tokenestimate.Text(content.Thinking.Signature)
	case coremodel.ContentToolCall:
		if content.ToolCall == nil {
			return 0
		}
		return 8 + tokenestimate.Text(content.ToolCall.Name) + tokenestimate.Text(string(content.ToolCall.Arguments))
	case coremodel.ContentToolResult:
		if content.ToolResult == nil {
			return 0
		}
		// State is durable runtime data and is not sent by the provider adapter.
		total := 8 + tokenestimate.Text(content.ToolResult.Name)
		for _, nested := range content.ToolResult.Content {
			total += estimateCoreContent(nested)
		}
		return total
	default:
		return 0
	}
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
