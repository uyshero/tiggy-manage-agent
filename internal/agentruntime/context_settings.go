package agentruntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

const defaultMaxLLMOutputTokens = 16384

type contextBudgetLimits struct {
	ContextWindowTokens     int
	InputBudgetRatioPercent int
	MaxInputTokens          int
	ReservedOutputTokens    int
}

func buildCurrentDateContext(now time.Time) string {
	if now.IsZero() {
		return ""
	}
	return fmt.Sprintf("Today's date is %s.", now.Format("2006-01-02"))
}

func contextBudgetFromSettings(contextWindowTokens int, runtimeSettings json.RawMessage) contextBudgetLimits {
	if contextWindowTokens <= 0 {
		contextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	ratio := contextBudgetRatioPercent(runtimeSettings)
	maxInputTokens := contextWindowTokens * ratio / 100
	reservedOutputTokens := contextOutputReserveTokens(runtimeSettings)
	if reservedOutputTokens > 0 {
		if reservedOutputTokens >= contextWindowTokens {
			reservedOutputTokens = contextWindowTokens - 1
		}
		maxInputTokens = minInt(maxInputTokens, contextWindowTokens-reservedOutputTokens)
	} else {
		reservedOutputTokens = contextWindowTokens - maxInputTokens
	}
	if maxInputTokens < 1 {
		maxInputTokens = 1
	}
	return contextBudgetLimits{
		ContextWindowTokens: contextWindowTokens, InputBudgetRatioPercent: ratio,
		MaxInputTokens: maxInputTokens, ReservedOutputTokens: reservedOutputTokens,
	}
}

func contextBudgetRatioPercent(runtimeSettings json.RawMessage) int {
	var settings struct {
		ContextInputBudgetRatioPercent int `json:"context_input_budget_ratio_percent"`
		ContextBudgetRatioPercent      int `json:"context_budget_ratio_percent"`
	}
	ratio := managedagents.ContextBudgetRatioPercent
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		if settings.ContextInputBudgetRatioPercent > 0 {
			ratio = settings.ContextInputBudgetRatioPercent
		} else if settings.ContextBudgetRatioPercent > 0 {
			ratio = settings.ContextBudgetRatioPercent
		}
	}
	return minInt(max(ratio, 10), 95)
}

func contextOutputReserveTokens(runtimeSettings json.RawMessage) int {
	var settings struct {
		ContextOutputReserveTokens int `json:"context_output_reserve_tokens"`
		OutputReserveTokens        int `json:"output_reserve_tokens"`
		OutputTokenReserve         int `json:"output_token_reserve"`
	}
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		switch {
		case settings.ContextOutputReserveTokens > 0:
			return settings.ContextOutputReserveTokens
		case settings.OutputReserveTokens > 0:
			return settings.OutputReserveTokens
		case settings.OutputTokenReserve > 0:
			return settings.OutputTokenReserve
		}
	}
	return 0
}

func maxLLMOutputTokens(reservedOutputTokens int) int {
	if reservedOutputTokens <= 0 {
		return 0
	}
	return minInt(reservedOutputTokens, defaultMaxLLMOutputTokens)
}

func pinnedContextFromSettings(runtimeSettings json.RawMessage) string {
	var settings struct {
		PinnedContext    any `json:"pinned_context"`
		ProtectedContext any `json:"protected_context"`
	}
	if len(runtimeSettings) == 0 || json.Unmarshal(runtimeSettings, &settings) != nil {
		return ""
	}
	value := settings.PinnedContext
	if value == nil {
		value = settings.ProtectedContext
	}
	return formatPinnedContext(value)
}

func combinePinnedContext(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatPinnedContext(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		lines := make([]string, 0, len(typed))
		for _, item := range typed {
			if line := strings.TrimSpace(formatPinnedContext(item)); line != "" {
				lines = append(lines, "- "+line)
			}
		}
		return strings.Join(lines, "\n")
	default:
		encoded, err := json.MarshalIndent(typed, "", "  ")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(encoded))
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
