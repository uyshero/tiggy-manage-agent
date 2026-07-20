package agenteval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineSuiteMeetsQualityThresholds(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("..", "..", "testdata", "agent-quality", "completion-gate.json"))
	if err != nil {
		t.Fatalf("load suite: %v", err)
	}
	report, err := Evaluate(t.Context(), suite)
	if err != nil {
		t.Fatalf("evaluate suite: %v", err)
	}
	if !report.Passed {
		t.Fatalf("quality thresholds failed: %#v", report.ThresholdFailures)
	}
	if report.TotalCases != 19 || report.PassedCases != 19 || report.CompletionCases != 9 {
		t.Fatalf("unexpected cases: %+v", report)
	}
	if report.FalseSuccesses != 0 || report.FalseSuccessRate != 0 {
		t.Fatalf("unexpected false successes: %+v", report)
	}
	if report.RetryCorrections != 3 || report.RetryCorrectionRate != 1 {
		t.Fatalf("unexpected retry correction metrics: %+v", report)
	}
	if report.EvidenceCases != 4 || report.EvidenceComplianceRate != 1 {
		t.Fatalf("unexpected evidence compliance metrics: %+v", report)
	}
	if report.HardFailCases != 5 || report.HardFailRate != 1 {
		t.Fatalf("unexpected hard-fail metrics: %+v", report)
	}
	if report.SchemaCases != 4 || report.SchemaComplianceRate != 1 || report.SchemaRetryCases != 2 || report.SchemaRetryCorrectionRate != 1 {
		t.Fatalf("unexpected schema compliance metrics: %+v", report)
	}
	if report.InvalidToolExecutions != 0 || report.InvalidToolExecutionRate != 0 {
		t.Fatalf("invalid tool execution escaped schema guard: %+v", report)
	}
	if report.TaskGroupCases != 6 || report.TaskGroupComplianceRate != 1 || report.TaskGroupRetryCases != 1 || report.TaskGroupRetryRate != 1 {
		t.Fatalf("unexpected task-group quality metrics: %+v", report)
	}
	if report.TaskGroupRejectedResults != 2 || report.InvalidAggregatedResults != 0 || report.InvalidAggregationRate != 0 {
		t.Fatalf("invalid task-group result entered aggregate: %+v", report)
	}
}

func TestFilesystemToolSuiteMeetsQualityThresholds(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("..", "..", "testdata", "agent-quality", "filesystem-tools.json"))
	if err != nil {
		t.Fatalf("load filesystem suite: %v", err)
	}
	report, err := Evaluate(t.Context(), suite)
	if err != nil {
		t.Fatalf("evaluate filesystem suite: %v", err)
	}
	if !report.Passed || report.TotalCases != 5 || report.PassedCases != 5 {
		t.Fatalf("filesystem quality thresholds failed: %+v", report)
	}
	if report.FilesystemCases != 5 || report.FilesystemComplianceRate != 1 || report.FilesystemSelectionRate != 1 {
		t.Fatalf("unexpected filesystem compliance metrics: %+v", report)
	}
	if report.FilesystemRecoveryCases != 1 || report.FilesystemRecoveryRate != 1 {
		t.Fatalf("unexpected filesystem recovery metrics: %+v", report)
	}
}

func TestEvaluateReportsFalseSuccessAndThresholdFailure(t *testing.T) {
	suite := Suite{
		Version:    SuiteVersion,
		Thresholds: Thresholds{CasePassRateMin: 1, FalseSuccessRateMax: 0},
		Cases: []Case{{
			ID: "unsafe", Category: "plan_enforcement", MaxRetries: 1,
			Expected:   Expectation{Outcome: "fail", Validator: "builtin.task_plan"},
			Candidates: []Candidate{{Text: "done", Plan: PlanSnapshot{Availability: "none"}}},
		}},
	}
	report, err := Evaluate(t.Context(), suite)
	if err != nil {
		t.Fatalf("evaluate suite: %v", err)
	}
	if report.Passed || report.FalseSuccesses != 1 || report.FalseSuccessRate != 1 {
		t.Fatalf("false success was not reported: %+v", report)
	}
	if len(report.ThresholdFailures) != 2 {
		t.Fatalf("unexpected threshold failures: %#v", report.ThresholdFailures)
	}
}

func TestLoadSuiteRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suite.json")
	content := `{"version":"tma.agent_quality_eval.v1","cases":[{"id":"one","category":"first_attempt","max_retries":1,"expected":{"outcome":"pass","blocked_retries":0},"candidates":[{"text":"done","plan":{"availability":"none"}}]}]} {}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSuite(path)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("expected trailing JSON failure, got %v", err)
	}
}

func TestEvaluateRejectsInvalidAvailablePlanSnapshot(t *testing.T) {
	suite := Suite{
		Version: SuiteVersion,
		Cases: []Case{{
			ID: "invalid", Category: "plan_enforcement", MaxRetries: 1,
			Expected:   Expectation{Outcome: "pass"},
			Candidates: []Candidate{{Text: "done", Plan: PlanSnapshot{Availability: "available", ID: "plan", Status: "unknown"}}},
		}},
	}
	_, err := Evaluate(t.Context(), suite)
	if err == nil || !strings.Contains(err.Error(), `invalid status "unknown"`) {
		t.Fatalf("expected invalid plan status failure, got %v", err)
	}
}

func TestToolSchemaCaseCountsExpectedRejectedCalls(t *testing.T) {
	suite := Suite{
		Version:    SuiteVersion,
		Thresholds: Thresholds{CasePassRateMin: 1, SchemaComplianceMin: 1, InvalidExecutionMax: 0},
		Cases: []Case{{
			ID: "schema_guard", Category: "schema_retry", Flow: "tool_schema", MaxRetries: 1,
			Expected: Expectation{Outcome: "fail", SchemaRejections: 2, ErrorContains: "repeatedly returned invalid"},
			Candidates: []Candidate{
				{ToolCall: &ToolCallCandidate{ID: "bad_1", Arguments: json.RawMessage(`{"value":"candidate"}`)}},
				{ToolCall: &ToolCallCandidate{ID: "bad_2", Arguments: json.RawMessage(`{"value":"candidate"}`)}},
			},
		}},
	}
	report, err := Evaluate(t.Context(), suite)
	if err != nil {
		t.Fatalf("evaluate schema suite: %v", err)
	}
	if !report.Passed || report.SchemaRejectedCalls != 2 || report.InvalidToolExecutions != 0 || report.FalseSuccesses != 0 {
		t.Fatalf("unexpected schema guard report: %+v", report)
	}
}
