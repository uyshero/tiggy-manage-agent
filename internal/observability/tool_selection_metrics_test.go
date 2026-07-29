package observability

import (
	"strings"
	"testing"
)

func TestToolSelectionMetricsAggregateOnlyBoundedMetadata(t *testing.T) {
	resetToolSelectionMetricsForTest()
	defer resetToolSelectionMetricsForTest()

	RecordToolSelectionMetric(ToolSelectionMetricInput{
		Mode:               "progressive",
		CandidateToolCount: 56, SelectedToolCount: 10,
		CandidateSchemaBytes: 56000, SelectedSchemaBytes: 9000,
		CandidateSchemaTokens: 14000, SelectedSchemaTokens: 2250,
		Triggers: []string{"image_request", "image_request", "tenant-secret"},
	})
	RecordToolSelectionMetric(ToolSelectionMetricInput{
		Mode:               "progressive",
		CandidateToolCount: 56, SelectedToolCount: 12,
		CandidateSchemaBytes: 56000, SelectedSchemaBytes: 11000,
		CandidateSchemaTokens: 14000, SelectedSchemaTokens: 2750,
		Triggers: []string{"web_request"},
	})
	RecordToolSelectionMetric(ToolSelectionMetricInput{
		Mode: "tenant-mode", CandidateToolCount: 1, SelectedToolCount: 1,
	})

	metrics := ToolSelectionMetricsSnapshot()
	if len(metrics) != 1 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
	metric := metrics[0]
	if metric.Mode != "progressive" || metric.Runs != 2 || metric.CandidateTools != 112 || metric.SelectedTools != 22 || metric.CandidateSchemaTokens != 28000 || metric.SelectedSchemaTokens != 5000 {
		t.Fatalf("unexpected aggregate: %#v", metric)
	}
	if metric.TriggerCounts["image_request"] != 1 || metric.TriggerCounts["web_request"] != 1 || metric.TriggerCounts["tenant-secret"] != 0 {
		t.Fatalf("unexpected bounded triggers: %#v", metric.TriggerCounts)
	}
}

func TestPrometheusTextIncludesToolSelectionSavingsWithoutSourceText(t *testing.T) {
	text := PrometheusText(MetricsSnapshot{ToolSelections: []ToolSelectionMetric{{
		Mode: "progressive", Runs: 2,
		CandidateTools: 112, SelectedTools: 22,
		CandidateSchemaBytes: 112000, SelectedSchemaBytes: 20000,
		CandidateSchemaTokens: 28000, SelectedSchemaTokens: 5000,
		TriggerCounts: map[string]int64{"image_request": 1},
	}}})
	for _, expected := range []string{
		`tma_tool_selection_runs_total{mode="progressive"} 2`,
		`tma_tool_selection_tools_total{mode="progressive",set="candidate"} 112`,
		`tma_tool_selection_tools_total{mode="progressive",set="selected"} 22`,
		`tma_tool_selection_schema_tokens_total{mode="progressive",set="candidate"} 28000`,
		`tma_tool_selection_schema_tokens_total{mode="progressive",set="selected"} 5000`,
		`tma_tool_selection_triggers_total{mode="progressive",trigger="image_request"} 1`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Prometheus output missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "tenant-secret") {
		t.Fatalf("Prometheus output leaked source text: %s", text)
	}
}
