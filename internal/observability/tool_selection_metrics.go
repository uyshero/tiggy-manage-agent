package observability

import (
	"sort"
	"strings"
	"sync"
)

type ToolSelectionMetricInput struct {
	Mode                  string
	CandidateToolCount    int
	SelectedToolCount     int
	CandidateSchemaBytes  int
	SelectedSchemaBytes   int
	CandidateSchemaTokens int
	SelectedSchemaTokens  int
	Triggers              []string
}

type ToolSelectionMetric struct {
	Mode                  string
	Runs                  int64
	CandidateTools        int64
	SelectedTools         int64
	CandidateSchemaBytes  int64
	SelectedSchemaBytes   int64
	CandidateSchemaTokens int64
	SelectedSchemaTokens  int64
	TriggerCounts         map[string]int64
}

var toolSelectionMetrics = struct {
	sync.Mutex
	byMode map[string]*ToolSelectionMetric
}{byMode: map[string]*ToolSelectionMetric{}}

func RecordToolSelectionMetric(input ToolSelectionMetricInput) {
	mode, ok := boundedToolSelectionMode(input.Mode)
	if !ok || !validToolSelectionCounts(input) {
		return
	}
	toolSelectionMetrics.Lock()
	defer toolSelectionMetrics.Unlock()
	metric := toolSelectionMetrics.byMode[mode]
	if metric == nil {
		metric = &ToolSelectionMetric{Mode: mode, TriggerCounts: map[string]int64{}}
		toolSelectionMetrics.byMode[mode] = metric
	}
	metric.Runs++
	metric.CandidateTools += int64(input.CandidateToolCount)
	metric.SelectedTools += int64(input.SelectedToolCount)
	metric.CandidateSchemaBytes += int64(input.CandidateSchemaBytes)
	metric.SelectedSchemaBytes += int64(input.SelectedSchemaBytes)
	metric.CandidateSchemaTokens += int64(input.CandidateSchemaTokens)
	metric.SelectedSchemaTokens += int64(input.SelectedSchemaTokens)
	seen := map[string]bool{}
	for _, raw := range input.Triggers {
		trigger, ok := boundedToolSelectionTrigger(raw)
		if !ok || seen[trigger] {
			continue
		}
		seen[trigger] = true
		metric.TriggerCounts[trigger]++
	}
}

func ToolSelectionMetricsSnapshot() []ToolSelectionMetric {
	toolSelectionMetrics.Lock()
	defer toolSelectionMetrics.Unlock()
	modes := make([]string, 0, len(toolSelectionMetrics.byMode))
	for mode := range toolSelectionMetrics.byMode {
		modes = append(modes, mode)
	}
	sort.Strings(modes)
	result := make([]ToolSelectionMetric, 0, len(modes))
	for _, mode := range modes {
		metric := *toolSelectionMetrics.byMode[mode]
		metric.TriggerCounts = cloneInt64Map(metric.TriggerCounts)
		result = append(result, metric)
	}
	return result
}

func validToolSelectionCounts(input ToolSelectionMetricInput) bool {
	return input.CandidateToolCount >= 0 && input.SelectedToolCount >= 0 && input.SelectedToolCount <= input.CandidateToolCount &&
		input.CandidateSchemaBytes >= 0 && input.SelectedSchemaBytes >= 0 && input.SelectedSchemaBytes <= input.CandidateSchemaBytes &&
		input.CandidateSchemaTokens >= 0 && input.SelectedSchemaTokens >= 0 && input.SelectedSchemaTokens <= input.CandidateSchemaTokens
}

func boundedToolSelectionMode(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "progressive", "explicit":
		return strings.TrimSpace(value), true
	default:
		return "", false
	}
}

func boundedToolSelectionTrigger(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "explicit_config", "web_request", "web_skill", "skill_management",
		"image_attachment", "image_request", "image_skill", "upload_request", "upload_skill",
		"active_plan", "task_request", "task_skill", "agent_request", "group_request", "discussion_request":
		return strings.TrimSpace(value), true
	default:
		return "", false
	}
}

func resetToolSelectionMetricsForTest() {
	toolSelectionMetrics.Lock()
	defer toolSelectionMetrics.Unlock()
	toolSelectionMetrics.byMode = map[string]*ToolSelectionMetric{}
}
