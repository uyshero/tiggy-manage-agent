package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"tiggy-manage-agent/internal/llm"
)

const (
	CompletionOutcomePass  = "pass"
	CompletionOutcomeRetry = "retry"
	CompletionOutcomeFail  = "fail"

	defaultCompletionGateMaxRetries = 3
	minCompletionGateMaxRetries     = 1
	maxCompletionGateMaxRetries     = 10
	completionEvidenceMaxKeys       = 20
	completionEvidenceMaxString     = 512
)

// CompletionGate validates a candidate response before it becomes the turn's final message.
// Returning retry feeds the verdict back into the same model loop; fail terminates the turn.
// On error, validators may still return Validator so failure events retain attribution.
type CompletionGate interface {
	Validate(context.Context, CompletionCandidate) (CompletionVerdict, error)
}

type CompletionCandidate struct {
	SessionID   string
	TurnID      string
	ToolRound   int
	Attempt     int
	Response    llm.Response
	Messages    []llm.Message
	ActiveTools []string
}

type CompletionVerdict struct {
	Outcome   string
	Validator string
	Reason    string
	Feedback  string
	Evidence  map[string]any
}

func completionGateMaxRetries(runtimeSettings json.RawMessage) int {
	var settings struct {
		CompletionGate struct {
			MaxRetries *int `json:"max_retries"`
		} `json:"completion_gate"`
	}
	value := defaultCompletionGateMaxRetries
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil && settings.CompletionGate.MaxRetries != nil {
		value = *settings.CompletionGate.MaxRetries
	}
	if value < minCompletionGateMaxRetries {
		return minCompletionGateMaxRetries
	}
	if value > maxCompletionGateMaxRetries {
		return maxCompletionGateMaxRetries
	}
	return value
}

func CompletionGateMaxRetries(runtimeSettings json.RawMessage) int {
	return completionGateMaxRetries(runtimeSettings)
}

func normalizeCompletionVerdict(verdict CompletionVerdict, fallbackValidator string) (CompletionVerdict, error) {
	verdict.Outcome = strings.ToLower(strings.TrimSpace(verdict.Outcome))
	verdict.Validator = strings.TrimSpace(verdict.Validator)
	verdict.Reason = strings.TrimSpace(verdict.Reason)
	verdict.Feedback = strings.TrimSpace(verdict.Feedback)
	if verdict.Validator == "" {
		verdict.Validator = fallbackValidator
	}
	switch verdict.Outcome {
	case CompletionOutcomePass:
		return verdict, nil
	case CompletionOutcomeRetry:
		if verdict.Feedback == "" {
			verdict.Feedback = defaultString(verdict.Reason, "Completion validation did not pass. Continue the task and try again.")
		}
		return verdict, nil
	case CompletionOutcomeFail:
		return verdict, nil
	default:
		return CompletionVerdict{}, fmt.Errorf("completion validator %q returned unsupported outcome %q", verdict.Validator, verdict.Outcome)
	}
}

func completionVerdictEventData(verdict CompletionVerdict, attempt, toolRound, maxRetries int) map[string]any {
	data := map[string]any{
		"attempt":        attempt,
		"tool_round":     toolRound,
		"max_retries":    maxRetries,
		"outcome":        verdict.Outcome,
		"validator":      truncateCompletionString(verdict.Validator),
		"feedback_chars": utf8.RuneCountInString(verdict.Feedback),
	}
	if verdict.Reason != "" {
		data["reason"] = truncateCompletionString(verdict.Reason)
	}
	if evidence := boundedCompletionEvidence(verdict.Evidence); len(evidence) > 0 {
		data["evidence"] = evidence
	}
	return data
}

func boundedCompletionEvidence(evidence map[string]any) map[string]any {
	return boundedCompletionEvidenceMap(evidence, 0)
}

func boundedCompletionEvidenceMap(evidence map[string]any, depth int) map[string]any {
	if len(evidence) == 0 {
		return nil
	}
	keys := make([]string, 0, len(evidence))
	for key := range evidence {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > completionEvidenceMaxKeys {
		keys = keys[:completionEvidenceMaxKeys]
	}
	bounded := make(map[string]any, len(keys)+1)
	for _, key := range keys {
		bounded[truncateCompletionString(key)] = boundedCompletionEvidenceValue(evidence[key], depth)
	}
	if len(evidence) > len(keys) {
		bounded["_omitted_keys"] = len(evidence) - len(keys)
	}
	return bounded
}

func boundedCompletionEvidenceValue(value any, depth int) any {
	if depth >= 2 {
		return truncateCompletionString(fmt.Sprint(value))
	}
	switch typed := value.(type) {
	case string:
		return truncateCompletionString(typed)
	case []string:
		limit := minInt(len(typed), 10)
		items := make([]any, 0, limit)
		for _, item := range typed[:limit] {
			items = append(items, truncateCompletionString(item))
		}
		return items
	case []any:
		limit := minInt(len(typed), 10)
		items := make([]any, 0, limit)
		for _, item := range typed[:limit] {
			items = append(items, boundedCompletionEvidenceValue(item, depth+1))
		}
		return items
	case map[string]any:
		return boundedCompletionEvidenceMap(typed, depth+1)
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return typed
	default:
		return truncateCompletionString(fmt.Sprint(value))
	}
}

func truncateCompletionString(value string) string {
	runes := []rune(value)
	if len(runes) <= completionEvidenceMaxString {
		return value
	}
	return string(runes[:completionEvidenceMaxString]) + "..."
}
