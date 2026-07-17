package agenteval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/httpapi"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

const SuiteVersion = "tma.agent_quality_eval.v1"

type Suite struct {
	Version    string     `json:"version"`
	Thresholds Thresholds `json:"thresholds"`
	Cases      []Case     `json:"cases"`
}

type Thresholds struct {
	CasePassRateMin        float64 `json:"case_pass_rate_min"`
	FalseSuccessRateMax    float64 `json:"false_success_rate_max"`
	RetryCorrectionRateMin float64 `json:"retry_correction_rate_min"`
	EvidenceComplianceMin  float64 `json:"evidence_compliance_rate_min"`
	HardFailRateMin        float64 `json:"hard_fail_rate_min"`
	SchemaComplianceMin    float64 `json:"schema_compliance_rate_min"`
	SchemaRetryBenefitMin  float64 `json:"schema_retry_correction_rate_min"`
	InvalidExecutionMax    float64 `json:"invalid_tool_execution_rate_max"`
	TaskGroupComplianceMin float64 `json:"task_group_compliance_rate_min"`
	TaskGroupRetryMin      float64 `json:"task_group_retry_correction_rate_min"`
	InvalidAggregateMax    float64 `json:"invalid_result_aggregation_rate_max"`
}

type Case struct {
	ID         string            `json:"id"`
	Category   string            `json:"category"`
	Flow       string            `json:"flow,omitempty"`
	MaxRetries int               `json:"max_retries"`
	Expected   Expectation       `json:"expected"`
	Candidates []Candidate       `json:"candidates"`
	TaskGroup  *TaskGroupFixture `json:"task_group,omitempty"`
}

type Expectation struct {
	Outcome          string          `json:"outcome"`
	BlockedRetries   int             `json:"blocked_retries"`
	Validator        string          `json:"validator,omitempty"`
	ErrorContains    string          `json:"error_contains,omitempty"`
	SchemaRejections int             `json:"schema_rejections,omitempty"`
	ToolExecutions   int             `json:"tool_executions,omitempty"`
	GroupStatus      string          `json:"group_status,omitempty"`
	GroupCompleted   bool            `json:"group_completed,omitempty"`
	ResultRejections int             `json:"result_rejections,omitempty"`
	AggregateJSON    json.RawMessage `json:"aggregate_json,omitempty"`
}

type TaskGroupFixture struct {
	Strategy      string           `json:"strategy"`
	ResultReducer string           `json:"result_reducer"`
	Quorum        int              `json:"quorum,omitempty"`
	FailFast      bool             `json:"fail_fast,omitempty"`
	Rounds        []TaskGroupRound `json:"rounds"`
}

type TaskGroupRound struct {
	Items []TaskGroupItem `json:"items"`
}

type TaskGroupItem struct {
	Index      int             `json:"index"`
	Status     string          `json:"status"`
	Text       string          `json:"text,omitempty"`
	Schema     json.RawMessage `json:"schema,omitempty"`
	ResultJSON json.RawMessage `json:"result_json,omitempty"`
	RetryCount int             `json:"retry_count,omitempty"`
}

type Candidate struct {
	Text     string             `json:"text,omitempty"`
	ToolCall *ToolCallCandidate `json:"tool_call,omitempty"`
	Plan     PlanSnapshot       `json:"plan,omitempty"`
}

type ToolCallCandidate struct {
	ID        string          `json:"id"`
	Arguments json.RawMessage `json:"arguments"`
}

type PlanSnapshot struct {
	Availability string     `json:"availability"`
	Error        string     `json:"error,omitempty"`
	ID           string     `json:"id,omitempty"`
	Status       string     `json:"status,omitempty"`
	Items        []PlanItem `json:"items,omitempty"`
}

type PlanItem struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Evidence    string   `json:"evidence,omitempty"`
	ToolCallIDs []string `json:"tool_call_ids,omitempty"`
}

type Report struct {
	Version                    string       `json:"version"`
	Passed                     bool         `json:"passed"`
	TotalCases                 int          `json:"total_cases"`
	PassedCases                int          `json:"passed_cases"`
	CasePassRate               float64      `json:"case_pass_rate"`
	CompletionCases            int          `json:"completion_cases"`
	FirstAttemptCompletions    int          `json:"first_attempt_completions"`
	FirstAttemptCompletionRate float64      `json:"first_attempt_completion_rate"`
	RetryCorrectionCases       int          `json:"retry_correction_cases"`
	RetryCorrections           int          `json:"retry_corrections"`
	RetryCorrectionRate        float64      `json:"retry_correction_rate"`
	ProtectedCases             int          `json:"protected_cases"`
	FalseSuccesses             int          `json:"false_successes"`
	FalseSuccessRate           float64      `json:"false_success_rate"`
	HardFailCases              int          `json:"hard_fail_cases"`
	HardFailures               int          `json:"hard_failures"`
	HardFailRate               float64      `json:"hard_fail_rate"`
	EvidenceCases              int          `json:"evidence_cases"`
	EvidenceCompliantCases     int          `json:"evidence_compliant_cases"`
	EvidenceComplianceRate     float64      `json:"evidence_compliance_rate"`
	AverageCompletionAttempts  float64      `json:"average_completion_attempts"`
	SchemaCases                int          `json:"schema_cases"`
	SchemaCompliantCases       int          `json:"schema_compliant_cases"`
	SchemaComplianceRate       float64      `json:"schema_compliance_rate"`
	SchemaRetryCases           int          `json:"schema_retry_cases"`
	SchemaRetryCorrections     int          `json:"schema_retry_corrections"`
	SchemaRetryCorrectionRate  float64      `json:"schema_retry_correction_rate"`
	SchemaProtectedCases       int          `json:"schema_protected_cases"`
	SchemaRejectedCalls        int          `json:"schema_rejected_calls"`
	InvalidToolExecutions      int          `json:"invalid_tool_executions"`
	InvalidToolExecutionRate   float64      `json:"invalid_tool_execution_rate"`
	TaskGroupCases             int          `json:"task_group_cases"`
	TaskGroupCompliantCases    int          `json:"task_group_compliant_cases"`
	TaskGroupComplianceRate    float64      `json:"task_group_compliance_rate"`
	TaskGroupRetryCases        int          `json:"task_group_retry_cases"`
	TaskGroupRetryCorrections  int          `json:"task_group_retry_corrections"`
	TaskGroupRetryRate         float64      `json:"task_group_retry_correction_rate"`
	TaskGroupRejectedResults   int          `json:"task_group_rejected_results"`
	InvalidAggregatedResults   int          `json:"invalid_aggregated_results"`
	InvalidAggregationRate     float64      `json:"invalid_result_aggregation_rate"`
	ThresholdFailures          []string     `json:"threshold_failures,omitempty"`
	Cases                      []CaseResult `json:"cases"`
}

type CaseResult struct {
	ID                 string          `json:"id"`
	Category           string          `json:"category"`
	Passed             bool            `json:"passed"`
	ExpectedOutcome    string          `json:"expected_outcome"`
	ActualOutcome      string          `json:"actual_outcome"`
	CompletionAttempts int             `json:"completion_attempts"`
	BlockedRetries     int             `json:"blocked_retries"`
	Validator          string          `json:"validator,omitempty"`
	FalseSuccess       bool            `json:"false_success"`
	SchemaRejections   int             `json:"schema_rejections,omitempty"`
	ToolExecutions     int             `json:"tool_executions,omitempty"`
	InvalidExecutions  int             `json:"invalid_executions,omitempty"`
	GroupStatus        string          `json:"group_status,omitempty"`
	GroupCompleted     bool            `json:"group_completed,omitempty"`
	ResultRejections   int             `json:"result_rejections,omitempty"`
	InvalidAggregates  int             `json:"invalid_aggregates,omitempty"`
	AggregateJSON      json.RawMessage `json:"aggregate_json,omitempty"`
	Error              string          `json:"error,omitempty"`
	Failure            string          `json:"failure,omitempty"`
}

func LoadSuite(path string) (Suite, error) {
	file, err := os.Open(path)
	if err != nil {
		return Suite{}, fmt.Errorf("open agent quality suite: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var suite Suite
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode agent quality suite: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Suite{}, errors.New("decode agent quality suite: multiple JSON values are not allowed")
		}
		return Suite{}, fmt.Errorf("decode agent quality suite trailing data: %w", err)
	}
	if err := validateSuite(suite); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

func Evaluate(ctx context.Context, suite Suite) (Report, error) {
	if err := validateSuite(suite); err != nil {
		return Report{}, err
	}
	report := Report{Version: suite.Version, TotalCases: len(suite.Cases), Cases: make([]CaseResult, 0, len(suite.Cases))}
	totalAttempts := 0

	for _, fixture := range suite.Cases {
		result := evaluateCase(ctx, fixture)
		report.Cases = append(report.Cases, result)
		if fixture.Flow == "" || fixture.Flow == "completion" {
			report.CompletionCases++
			totalAttempts += result.CompletionAttempts
		}
		if result.Passed {
			report.PassedCases++
		}
		if (fixture.Flow == "" || fixture.Flow == "completion") && result.ActualOutcome == "pass" && result.CompletionAttempts == 1 {
			report.FirstAttemptCompletions++
		}
		if fixture.Expected.Outcome == "pass" && fixture.Expected.BlockedRetries > 0 {
			report.RetryCorrectionCases++
			if result.Passed {
				report.RetryCorrections++
			}
		}
		if fixture.Expected.Outcome == "fail" || fixture.Expected.BlockedRetries > 0 || fixture.Expected.SchemaRejections > 0 || fixture.Expected.ResultRejections > 0 {
			report.ProtectedCases++
			if result.FalseSuccess {
				report.FalseSuccesses++
			}
		}
		if fixture.Expected.Outcome == "fail" {
			report.HardFailCases++
			if result.Passed {
				report.HardFailures++
			}
		}
		if strings.HasPrefix(fixture.Category, "evidence_") {
			report.EvidenceCases++
			if result.Passed {
				report.EvidenceCompliantCases++
			}
		}
		if fixture.Flow == "tool_schema" {
			report.SchemaCases++
			if result.Passed {
				report.SchemaCompliantCases++
			}
			if fixture.Expected.SchemaRejections > 0 && fixture.Expected.Outcome == "pass" {
				report.SchemaRetryCases++
				if result.Passed {
					report.SchemaRetryCorrections++
				}
			}
			if fixture.Expected.SchemaRejections > 0 {
				report.SchemaProtectedCases++
				report.SchemaRejectedCalls += fixture.Expected.SchemaRejections
				report.InvalidToolExecutions += result.InvalidExecutions
			}
		}
		if fixture.Flow == "task_group" {
			report.TaskGroupCases++
			if result.Passed {
				report.TaskGroupCompliantCases++
			}
			if fixture.TaskGroup != nil && len(fixture.TaskGroup.Rounds) > 1 {
				report.TaskGroupRetryCases++
				if result.Passed {
					report.TaskGroupRetryCorrections++
				}
			}
			report.TaskGroupRejectedResults += result.ResultRejections
			report.InvalidAggregatedResults += result.InvalidAggregates
		}
	}

	report.CasePassRate = rate(report.PassedCases, report.TotalCases)
	report.FirstAttemptCompletionRate = rate(report.FirstAttemptCompletions, report.CompletionCases)
	report.RetryCorrectionRate = rate(report.RetryCorrections, report.RetryCorrectionCases)
	report.FalseSuccessRate = rate(report.FalseSuccesses, report.ProtectedCases)
	report.EvidenceComplianceRate = rate(report.EvidenceCompliantCases, report.EvidenceCases)
	report.HardFailRate = rate(report.HardFailures, report.HardFailCases)
	report.AverageCompletionAttempts = rate(totalAttempts, report.CompletionCases)
	report.SchemaComplianceRate = rate(report.SchemaCompliantCases, report.SchemaCases)
	report.SchemaRetryCorrectionRate = rate(report.SchemaRetryCorrections, report.SchemaRetryCases)
	report.InvalidToolExecutionRate = rate(report.InvalidToolExecutions, report.SchemaRejectedCalls)
	report.TaskGroupComplianceRate = rate(report.TaskGroupCompliantCases, report.TaskGroupCases)
	report.TaskGroupRetryRate = rate(report.TaskGroupRetryCorrections, report.TaskGroupRetryCases)
	report.InvalidAggregationRate = rate(report.InvalidAggregatedResults, report.TaskGroupRejectedResults)
	report.ThresholdFailures = thresholdFailures(report, suite.Thresholds)
	report.Passed = len(report.ThresholdFailures) == 0
	return report, nil
}

func evaluateCase(ctx context.Context, fixture Case) CaseResult {
	if fixture.Flow == "tool_schema" {
		return evaluateToolSchemaCase(ctx, fixture)
	}
	if fixture.Flow == "task_group" {
		return evaluateTaskGroupCase(fixture)
	}
	reader := &replayPlanReader{}
	client := &replayClient{candidates: fixture.Candidates, reader: reader}
	steps := make([]agentruntime.Step, 0, len(fixture.Candidates)*3)
	runtimeSettings, _ := json.Marshal(map[string]any{"completion_gate": map[string]any{"max_retries": fixture.MaxRetries}})

	_, runErr := (agentruntime.DemoRuntime{
		Client:         client,
		CompletionGate: agentruntime.TaskPlanCompletionGate{Reader: reader},
	}).RunTurn(ctx, agentruntime.TurnRequest{
		SessionID:   "eval_" + fixture.ID,
		TurnID:      "turn_1",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"complete the task"}]}`),
		Config:      agentruntime.Config{RuntimeSettings: runtimeSettings},
		EmitStep: func(_ context.Context, step agentruntime.Step) error {
			steps = append(steps, step)
			return nil
		},
	})

	result := CaseResult{
		ID: fixture.ID, Category: fixture.Category, ExpectedOutcome: fixture.Expected.Outcome,
		ActualOutcome: "pass", CompletionAttempts: countSteps(steps, managedagents.EventRuntimeTurnCompleting),
		BlockedRetries: countSteps(steps, managedagents.EventRuntimeCompletionBlocked),
	}
	if runErr != nil {
		result.ActualOutcome = "fail"
		result.Error = runErr.Error()
	}
	result.Validator = lastCompletionValidator(steps)
	result.FalseSuccess = (fixture.Expected.Outcome == "fail" && result.ActualOutcome == "pass") ||
		(fixture.Expected.BlockedRetries > 0 && result.ActualOutcome == "pass" && result.BlockedRetries < fixture.Expected.BlockedRetries)

	failures := make([]string, 0, 4)
	if result.ActualOutcome != fixture.Expected.Outcome {
		failures = append(failures, fmt.Sprintf("outcome=%s, want %s", result.ActualOutcome, fixture.Expected.Outcome))
	}
	if result.BlockedRetries != fixture.Expected.BlockedRetries {
		failures = append(failures, fmt.Sprintf("blocked_retries=%d, want %d", result.BlockedRetries, fixture.Expected.BlockedRetries))
	}
	if fixture.Expected.Validator != "" && result.Validator != fixture.Expected.Validator {
		failures = append(failures, fmt.Sprintf("validator=%q, want %q", result.Validator, fixture.Expected.Validator))
	}
	if fixture.Expected.ErrorContains != "" && !strings.Contains(result.Error, fixture.Expected.ErrorContains) {
		failures = append(failures, fmt.Sprintf("error does not contain %q", fixture.Expected.ErrorContains))
	}
	result.Failure = strings.Join(failures, "; ")
	result.Passed = result.Failure == ""
	return result
}

func evaluateTaskGroupCase(fixture Case) CaseResult {
	group := managedagents.SubagentTaskGroup{
		ID: fixture.ID, Strategy: fixture.TaskGroup.Strategy, ResultReducer: fixture.TaskGroup.ResultReducer,
		Quorum: fixture.TaskGroup.Quorum, FailFast: fixture.TaskGroup.FailFast,
	}
	var final tools.AgentTaskGroupResponse
	firstRoundRejections := 0
	invalidAggregates := 0
	totalRejections := 0
	for roundIndex, round := range fixture.TaskGroup.Rounds {
		group.PlannedCount = len(round.Items)
		states := make([]tools.AgentTaskGroupItemState, 0, len(round.Items))
		for _, item := range round.Items {
			states = append(states, tools.AgentTaskGroupItemState{
				Item:   managedagents.SubagentTaskGroupItem{ItemIndex: item.Index, ExpectedResultSchema: item.Schema, RetryCount: item.RetryCount},
				Status: item.Status, AgentText: item.Text, ResultJSON: item.ResultJSON,
			})
		}
		final = httpapi.ReplayTaskGroup(group, states)
		completedIndexes := make(map[int]bool, len(final.Aggregate.CompletedItemIndexes))
		for _, index := range final.Aggregate.CompletedItemIndexes {
			completedIndexes[index] = true
		}
		for _, item := range final.Items {
			if item.ResultValid || len(item.Item.ExpectedResultSchema) == 0 {
				continue
			}
			totalRejections++
			if roundIndex == 0 {
				firstRoundRejections++
			}
			if completedIndexes[item.Item.ItemIndex] {
				invalidAggregates++
			}
		}
	}

	result := CaseResult{
		ID: fixture.ID, Category: fixture.Category, ExpectedOutcome: fixture.Expected.Outcome,
		ActualOutcome: "pass", GroupStatus: final.Status, GroupCompleted: final.Completed,
		ResultRejections: totalRejections, InvalidAggregates: invalidAggregates, AggregateJSON: cloneRaw(final.Aggregate.JSON),
	}
	failures := make([]string, 0, 6)
	if result.GroupStatus != fixture.Expected.GroupStatus {
		failures = append(failures, fmt.Sprintf("group_status=%s, want %s", result.GroupStatus, fixture.Expected.GroupStatus))
	}
	if result.GroupCompleted != fixture.Expected.GroupCompleted {
		failures = append(failures, fmt.Sprintf("group_completed=%t, want %t", result.GroupCompleted, fixture.Expected.GroupCompleted))
	}
	if firstRoundRejections != fixture.Expected.ResultRejections {
		failures = append(failures, fmt.Sprintf("first_round_result_rejections=%d, want %d", firstRoundRejections, fixture.Expected.ResultRejections))
	}
	if len(fixture.Expected.AggregateJSON) > 0 && !jsonEqual(result.AggregateJSON, fixture.Expected.AggregateJSON) {
		failures = append(failures, fmt.Sprintf("aggregate_json=%s, want %s", result.AggregateJSON, fixture.Expected.AggregateJSON))
	}
	if result.InvalidAggregates != 0 {
		failures = append(failures, fmt.Sprintf("invalid_aggregates=%d, want 0", result.InvalidAggregates))
	}
	result.Failure = strings.Join(failures, "; ")
	result.Passed = result.Failure == ""
	if !result.Passed {
		result.ActualOutcome = "fail"
	}
	result.FalseSuccess = result.InvalidAggregates > 0 || (fixture.Expected.GroupStatus == "failed" && result.GroupStatus == "completed")
	return result
}

func evaluateToolSchemaCase(ctx context.Context, fixture Case) CaseResult {
	runtimeTool := &schemaEvalRuntime{}
	registry := tools.NewRegistry(runtimeTool)
	client := &schemaReplayClient{candidates: fixture.Candidates}
	steps := make([]agentruntime.Step, 0, len(fixture.Candidates)*3)
	invalidCallIDs := make(map[string]bool)
	for _, candidate := range fixture.Candidates {
		if candidate.ToolCall == nil {
			continue
		}
		if validationError := registry.ValidateCallArguments(tools.Call{Identifier: "quality_schema", APIName: "check", Arguments: candidate.ToolCall.Arguments}); validationError != nil {
			invalidCallIDs[candidate.ToolCall.ID] = true
		}
	}

	_, runErr := (agentruntime.DemoRuntime{Client: client, MaxToolRounds: 8}).RunTurn(ctx, agentruntime.TurnRequest{
		SessionID: "eval_" + fixture.ID, TurnID: "turn_1",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"run the schema evaluation tool"}]}`),
		Config: agentruntime.Config{
			ModelTools: registry.ModelTools(), ToolRegistry: registry,
			ToolExecutor: tools.RegistryExecutor{Registry: registry},
		},
		EmitStep: func(_ context.Context, step agentruntime.Step) error {
			steps = append(steps, step)
			return nil
		},
	})

	result := CaseResult{
		ID: fixture.ID, Category: fixture.Category, ExpectedOutcome: fixture.Expected.Outcome,
		ActualOutcome: "pass", CompletionAttempts: countSteps(steps, managedagents.EventRuntimeTurnCompleting),
		SchemaRejections: countToolErrors(steps, "invalid_tool_arguments"), ToolExecutions: runtimeTool.calls,
	}
	if runErr != nil {
		result.ActualOutcome = "fail"
		result.Error = runErr.Error()
	}
	for _, callID := range runtimeTool.executedCallIDs {
		if invalidCallIDs[callID] {
			result.InvalidExecutions++
		}
	}
	result.FalseSuccess = (fixture.Expected.Outcome == "fail" && result.ActualOutcome == "pass") ||
		(fixture.Expected.SchemaRejections > 0 && result.ActualOutcome == "pass" && result.SchemaRejections < fixture.Expected.SchemaRejections) ||
		result.InvalidExecutions > 0

	failures := make([]string, 0, 5)
	if result.ActualOutcome != fixture.Expected.Outcome {
		failures = append(failures, fmt.Sprintf("outcome=%s, want %s", result.ActualOutcome, fixture.Expected.Outcome))
	}
	if result.SchemaRejections != fixture.Expected.SchemaRejections {
		failures = append(failures, fmt.Sprintf("schema_rejections=%d, want %d", result.SchemaRejections, fixture.Expected.SchemaRejections))
	}
	if result.ToolExecutions != fixture.Expected.ToolExecutions {
		failures = append(failures, fmt.Sprintf("tool_executions=%d, want %d", result.ToolExecutions, fixture.Expected.ToolExecutions))
	}
	if fixture.Expected.ErrorContains != "" && !strings.Contains(result.Error, fixture.Expected.ErrorContains) {
		failures = append(failures, fmt.Sprintf("error does not contain %q", fixture.Expected.ErrorContains))
	}
	result.Failure = strings.Join(failures, "; ")
	result.Passed = result.Failure == ""
	return result
}

func validateSuite(suite Suite) error {
	if suite.Version != SuiteVersion {
		return fmt.Errorf("agent quality suite version %q is unsupported", suite.Version)
	}
	if len(suite.Cases) == 0 {
		return errors.New("agent quality suite requires at least one case")
	}
	seen := map[string]struct{}{}
	for index, fixture := range suite.Cases {
		if strings.TrimSpace(fixture.ID) == "" {
			return fmt.Errorf("agent quality case %d requires an id", index)
		}
		if _, exists := seen[fixture.ID]; exists {
			return fmt.Errorf("duplicate agent quality case id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		if strings.TrimSpace(fixture.Category) == "" {
			return fmt.Errorf("agent quality case %q requires a category", fixture.ID)
		}
		if fixture.Expected.Outcome != "pass" && fixture.Expected.Outcome != "fail" {
			return fmt.Errorf("agent quality case %q has invalid expected outcome %q", fixture.ID, fixture.Expected.Outcome)
		}
		if fixture.MaxRetries < 1 || fixture.MaxRetries > 10 {
			return fmt.Errorf("agent quality case %q max_retries must be between 1 and 10", fixture.ID)
		}
		if fixture.Flow != "task_group" && len(fixture.Candidates) == 0 {
			return fmt.Errorf("agent quality case %q requires candidates", fixture.ID)
		}
		if fixture.Flow != "" && fixture.Flow != "completion" && fixture.Flow != "tool_schema" && fixture.Flow != "task_group" {
			return fmt.Errorf("agent quality case %q has invalid flow %q", fixture.ID, fixture.Flow)
		}
		if fixture.Expected.BlockedRetries < 0 || fixture.Expected.BlockedRetries > fixture.MaxRetries {
			return fmt.Errorf("agent quality case %q blocked_retries must be between 0 and max_retries", fixture.ID)
		}
		if fixture.Flow != "task_group" && len(fixture.Candidates) < fixture.Expected.BlockedRetries+1 {
			return fmt.Errorf("agent quality case %q requires at least %d candidates", fixture.ID, fixture.Expected.BlockedRetries+1)
		}
		for candidateIndex, candidate := range fixture.Candidates {
			if fixture.Flow == "task_group" {
				break
			}
			if fixture.Flow == "tool_schema" {
				if err := validateToolSchemaCandidate(candidate); err != nil {
					return fmt.Errorf("agent quality case %q candidate %d: %w", fixture.ID, candidateIndex, err)
				}
				continue
			}
			if strings.TrimSpace(candidate.Text) == "" {
				return fmt.Errorf("agent quality case %q candidate %d requires non-empty text", fixture.ID, candidateIndex)
			}
			switch candidate.Plan.Availability {
			case "none":
			case "error":
				if strings.TrimSpace(candidate.Plan.Error) == "" {
					return fmt.Errorf("agent quality case %q candidate %d plan error is empty", fixture.ID, candidateIndex)
				}
			case "available":
				if err := validatePlanSnapshot(candidate.Plan); err != nil {
					return fmt.Errorf("agent quality case %q candidate %d: %w", fixture.ID, candidateIndex, err)
				}
			default:
				return fmt.Errorf("agent quality case %q candidate %d has invalid plan availability %q", fixture.ID, candidateIndex, candidate.Plan.Availability)
			}
		}
		if fixture.Flow == "task_group" {
			if err := validateTaskGroupFixture(fixture); err != nil {
				return fmt.Errorf("agent quality case %q: %w", fixture.ID, err)
			}
		}
	}
	for name, value := range map[string]float64{
		"case_pass_rate_min":                   suite.Thresholds.CasePassRateMin,
		"false_success_rate_max":               suite.Thresholds.FalseSuccessRateMax,
		"retry_correction_rate_min":            suite.Thresholds.RetryCorrectionRateMin,
		"evidence_compliance_rate_min":         suite.Thresholds.EvidenceComplianceMin,
		"hard_fail_rate_min":                   suite.Thresholds.HardFailRateMin,
		"schema_compliance_rate_min":           suite.Thresholds.SchemaComplianceMin,
		"schema_retry_correction_rate_min":     suite.Thresholds.SchemaRetryBenefitMin,
		"invalid_tool_execution_rate_max":      suite.Thresholds.InvalidExecutionMax,
		"task_group_compliance_rate_min":       suite.Thresholds.TaskGroupComplianceMin,
		"task_group_retry_correction_rate_min": suite.Thresholds.TaskGroupRetryMin,
		"invalid_result_aggregation_rate_max":  suite.Thresholds.InvalidAggregateMax,
	} {
		if value < 0 || value > 1 {
			return fmt.Errorf("agent quality threshold %s must be between 0 and 1", name)
		}
	}
	return nil
}

func validateTaskGroupFixture(fixture Case) error {
	if fixture.TaskGroup == nil {
		return errors.New("task_group flow requires task_group")
	}
	switch fixture.TaskGroup.Strategy {
	case managedagents.SubagentTaskGroupStrategyAllCompleted, managedagents.SubagentTaskGroupStrategyAnyCompleted, managedagents.SubagentTaskGroupStrategyQuorum:
	default:
		return fmt.Errorf("invalid task group strategy %q", fixture.TaskGroup.Strategy)
	}
	switch fixture.TaskGroup.ResultReducer {
	case managedagents.SubagentTaskGroupReducerNone, managedagents.SubagentTaskGroupReducerConcatText,
		managedagents.SubagentTaskGroupReducerJSONList, managedagents.SubagentTaskGroupReducerJSONObject,
		managedagents.SubagentTaskGroupReducerFirstSuccess, managedagents.SubagentTaskGroupReducerMajorityText,
		managedagents.SubagentTaskGroupReducerJSONValues, managedagents.SubagentTaskGroupReducerMergeObjects,
		managedagents.SubagentTaskGroupReducerFirstValue, managedagents.SubagentTaskGroupReducerMajorityValue:
	default:
		return fmt.Errorf("invalid task group reducer %q", fixture.TaskGroup.ResultReducer)
	}
	if len(fixture.TaskGroup.Rounds) == 0 {
		return errors.New("task_group flow requires rounds")
	}
	for roundIndex, round := range fixture.TaskGroup.Rounds {
		if len(round.Items) == 0 {
			return fmt.Errorf("task group round %d requires items", roundIndex)
		}
		seen := map[int]bool{}
		for _, item := range round.Items {
			if seen[item.Index] {
				return fmt.Errorf("task group round %d duplicates item index %d", roundIndex, item.Index)
			}
			seen[item.Index] = true
			switch item.Status {
			case managedagents.TurnStatusCompleted, managedagents.TurnStatusFailed, managedagents.SessionStatusTerminated,
				managedagents.SubagentTaskGroupItemStateRejected, managedagents.SubagentTaskGroupItemStateQueued,
				managedagents.SubagentTaskGroupItemStateCreated, managedagents.SessionStatusRunning,
				managedagents.TurnStatusWaitingApproval, managedagents.TurnStatusWaitingHuman:
			default:
				return fmt.Errorf("task group round %d item %d has invalid status %q", roundIndex, item.Index, item.Status)
			}
			if len(item.Schema) > 0 {
				if _, err := tools.CompileJSONSchema(item.Schema); err != nil {
					return fmt.Errorf("task group round %d item %d schema is invalid: %w", roundIndex, item.Index, err)
				}
			}
		}
	}
	return nil
}

func validateToolSchemaCandidate(candidate Candidate) error {
	if candidate.ToolCall == nil {
		if strings.TrimSpace(candidate.Text) == "" {
			return errors.New("tool schema candidate requires text or tool_call")
		}
		return nil
	}
	if strings.TrimSpace(candidate.ToolCall.ID) == "" {
		return errors.New("tool schema candidate tool_call requires an id")
	}
	if len(bytes.TrimSpace(candidate.ToolCall.Arguments)) == 0 || !json.Valid(candidate.ToolCall.Arguments) {
		return errors.New("tool schema candidate tool_call arguments must be valid JSON")
	}
	return nil
}

func validatePlanSnapshot(plan PlanSnapshot) error {
	if strings.TrimSpace(plan.ID) == "" {
		return errors.New("available plan requires an id")
	}
	switch plan.Status {
	case managedagents.TaskPlanStatusActive, managedagents.TaskPlanStatusCompleted, managedagents.TaskPlanStatusCanceled, managedagents.TaskPlanStatusSuperseded:
	default:
		return fmt.Errorf("available plan has invalid status %q", plan.Status)
	}
	seenItems := map[string]struct{}{}
	for index, item := range plan.Items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("plan item %d requires an id", index)
		}
		if _, exists := seenItems[item.ID]; exists {
			return fmt.Errorf("plan item id %q is duplicated", item.ID)
		}
		seenItems[item.ID] = struct{}{}
		switch item.Status {
		case managedagents.TaskItemStatusPending, managedagents.TaskItemStatusInProgress, managedagents.TaskItemStatusCompleted, managedagents.TaskItemStatusBlocked:
		default:
			return fmt.Errorf("plan item %q has invalid status %q", item.ID, item.Status)
		}
		seenCalls := map[string]struct{}{}
		for callIndex, callID := range item.ToolCallIDs {
			if strings.TrimSpace(callID) == "" {
				return fmt.Errorf("plan item %q tool_call_ids[%d] is empty", item.ID, callIndex)
			}
			if _, exists := seenCalls[callID]; exists {
				return fmt.Errorf("plan item %q tool call id %q is duplicated", item.ID, callID)
			}
			seenCalls[callID] = struct{}{}
		}
	}
	return nil
}

func thresholdFailures(report Report, thresholds Thresholds) []string {
	failures := make([]string, 0, 4)
	if report.CasePassRate < thresholds.CasePassRateMin {
		failures = append(failures, fmt.Sprintf("case_pass_rate %.4f is below %.4f", report.CasePassRate, thresholds.CasePassRateMin))
	}
	if report.FalseSuccessRate > thresholds.FalseSuccessRateMax {
		failures = append(failures, fmt.Sprintf("false_success_rate %.4f exceeds %.4f", report.FalseSuccessRate, thresholds.FalseSuccessRateMax))
	}
	if report.RetryCorrectionRate < thresholds.RetryCorrectionRateMin {
		failures = append(failures, fmt.Sprintf("retry_correction_rate %.4f is below %.4f", report.RetryCorrectionRate, thresholds.RetryCorrectionRateMin))
	}
	if report.EvidenceComplianceRate < thresholds.EvidenceComplianceMin {
		failures = append(failures, fmt.Sprintf("evidence_compliance_rate %.4f is below %.4f", report.EvidenceComplianceRate, thresholds.EvidenceComplianceMin))
	}
	if report.HardFailRate < thresholds.HardFailRateMin {
		failures = append(failures, fmt.Sprintf("hard_fail_rate %.4f is below %.4f", report.HardFailRate, thresholds.HardFailRateMin))
	}
	if report.SchemaComplianceRate < thresholds.SchemaComplianceMin {
		failures = append(failures, fmt.Sprintf("schema_compliance_rate %.4f is below %.4f", report.SchemaComplianceRate, thresholds.SchemaComplianceMin))
	}
	if report.SchemaRetryCorrectionRate < thresholds.SchemaRetryBenefitMin {
		failures = append(failures, fmt.Sprintf("schema_retry_correction_rate %.4f is below %.4f", report.SchemaRetryCorrectionRate, thresholds.SchemaRetryBenefitMin))
	}
	if report.InvalidToolExecutionRate > thresholds.InvalidExecutionMax {
		failures = append(failures, fmt.Sprintf("invalid_tool_execution_rate %.4f exceeds %.4f", report.InvalidToolExecutionRate, thresholds.InvalidExecutionMax))
	}
	if report.TaskGroupComplianceRate < thresholds.TaskGroupComplianceMin {
		failures = append(failures, fmt.Sprintf("task_group_compliance_rate %.4f is below %.4f", report.TaskGroupComplianceRate, thresholds.TaskGroupComplianceMin))
	}
	if report.TaskGroupRetryRate < thresholds.TaskGroupRetryMin {
		failures = append(failures, fmt.Sprintf("task_group_retry_correction_rate %.4f is below %.4f", report.TaskGroupRetryRate, thresholds.TaskGroupRetryMin))
	}
	if report.InvalidAggregationRate > thresholds.InvalidAggregateMax {
		failures = append(failures, fmt.Sprintf("invalid_result_aggregation_rate %.4f exceeds %.4f", report.InvalidAggregationRate, thresholds.InvalidAggregateMax))
	}
	return failures
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftJSON, leftErr := json.Marshal(leftValue)
	rightJSON, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func rate(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func countSteps(steps []agentruntime.Step, eventType string) int {
	count := 0
	for _, step := range steps {
		if step.Type == eventType {
			count++
		}
	}
	return count
}

func lastCompletionValidator(steps []agentruntime.Step) string {
	for index := len(steps) - 1; index >= 0; index-- {
		switch steps[index].Type {
		case managedagents.EventRuntimeCompletionValidated, managedagents.EventRuntimeCompletionBlocked, managedagents.EventRuntimeCompletionFailed:
			validator, _ := steps[index].Data["validator"].(string)
			return validator
		}
	}
	return ""
}

func countToolErrors(steps []agentruntime.Step, errorType string) int {
	count := 0
	for _, step := range steps {
		if step.Type != managedagents.EventRuntimeToolResult {
			continue
		}
		executionError, _ := step.Data["error"].(*tools.ExecutionError)
		if executionError != nil && executionError.Type == errorType {
			count++
		}
	}
	return count
}

type schemaReplayClient struct {
	candidates []Candidate
	calls      int
}

func (client *schemaReplayClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	client.calls++
	if len(client.candidates) == 0 {
		return llm.Response{}, errors.New("agent quality schema replay exhausted candidates")
	}
	candidate := client.candidates[0]
	client.candidates = client.candidates[1:]
	if candidate.ToolCall == nil {
		return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: candidate.Text}}}}, nil
	}
	return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: candidate.ToolCall.ID, Type: "function",
		Function: llm.ToolCallFunction{Name: "quality_schema.check", Arguments: candidate.ToolCall.Arguments},
	}}}}, nil
}

type schemaEvalRuntime struct {
	calls           int
	executedCallIDs []string
}

func (runtime *schemaEvalRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "quality_schema", Type: "builtin", Executors: []string{tools.ExecutorServer},
		API: []tools.API{{
			Name: "check", Description: "Validate deterministic agent quality arguments.", Risk: tools.ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"value":{"type":"string","minLength":1},"mode":{"type":"string","enum":["strict"]}},"required":["value","mode"]}`),
		}},
	}
}

func (runtime *schemaEvalRuntime) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	runtime.calls++
	runtime.executedCallIDs = append(runtime.executedCallIDs, call.ID)
	return tools.ExecutionResult{Identifier: "quality_schema", APIName: "check", Content: "schema check passed"}, nil
}

type replayClient struct {
	candidates []Candidate
	reader     *replayPlanReader
}

func (client *replayClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	if len(client.candidates) == 0 {
		return llm.Response{}, errors.New("agent quality replay exhausted candidate responses")
	}
	candidate := client.candidates[0]
	client.candidates = client.candidates[1:]
	client.reader.snapshot = candidate.Plan
	return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: candidate.Text}}}}, nil
}

type replayPlanReader struct {
	snapshot PlanSnapshot
}

func (reader *replayPlanReader) GetCurrentSessionTaskPlanContext(context.Context, string) (managedagents.SessionTaskPlan, error) {
	snapshot := reader.snapshot
	switch snapshot.Availability {
	case "none":
		return managedagents.SessionTaskPlan{}, managedagents.ErrNotFound
	case "error":
		return managedagents.SessionTaskPlan{}, errors.New(snapshot.Error)
	case "available":
		items := make([]managedagents.SessionTaskItem, 0, len(snapshot.Items))
		for _, item := range snapshot.Items {
			refs := make([]managedagents.TaskEvidenceRef, 0, len(item.ToolCallIDs))
			for _, callID := range item.ToolCallIDs {
				refs = append(refs, managedagents.TaskEvidenceRef{Kind: managedagents.TaskEvidenceKindToolResult, TurnID: "turn_1", ToolCallID: callID, Tool: "verify.check"})
			}
			items = append(items, managedagents.SessionTaskItem{ID: item.ID, PlanID: snapshot.ID, Description: item.Description, Status: item.Status, Evidence: item.Evidence, EvidenceRefs: refs})
		}
		return managedagents.SessionTaskPlan{ID: snapshot.ID, SessionID: "eval", Status: snapshot.Status, Items: items}, nil
	default:
		return managedagents.SessionTaskPlan{}, fmt.Errorf("unsupported replay plan availability %q", snapshot.Availability)
	}
}

func (reader *replayPlanReader) ListSessionTaskPlansContext(context.Context, string) ([]managedagents.SessionTaskPlan, error) {
	return nil, nil
}
