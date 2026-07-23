package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestRunComparisonAndEvaluationEndpoints(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "evaluation-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	leftSession := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Left")
	rightSession := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Right")
	leftTurn := createCompletedEvaluationRun(t, store, leftSession.ID, "left prompt", "left result")
	rightTurn := createCompletedEvaluationRun(t, store, rightSession.ID, "right prompt", "right result")
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Millisecond, nil), nil)

	comparisonPath := "/v2/run-comparisons?left_session_id=" + url.QueryEscape(leftSession.ID) +
		"&left_turn_id=" + url.QueryEscape(leftTurn) + "&right_session_id=" + url.QueryEscape(rightSession.ID) +
		"&right_turn_id=" + url.QueryEscape(rightTurn)
	comparison := getJSON[sessionComparisonResponse](t, server, comparisonPath)
	if comparison.Left.Run == nil || comparison.Left.Run.ID != leftTurn || comparison.Left.Prompt != "left prompt" || comparison.Left.Result != "left result" {
		t.Fatalf("unexpected left Run comparison: %+v", comparison.Left)
	}
	if comparison.Right.Run == nil || comparison.Right.Run.ID != rightTurn || comparison.Right.Trace == nil || comparison.Right.Trace.TurnID != rightTurn {
		t.Fatalf("unexpected right Run comparison: %+v", comparison.Right)
	}

	rubric := postJSONWithStatus[managedagents.EvaluationRubric](t, server, http.MethodPost, "/v2/evaluation-rubrics", `{
		"workspace_id":"`+leftSession.WorkspaceID+`","name":"TMA Space default","criteria":[
			{"id":"quality","name":"Quality"},{"id":"safety","name":"Safety"}
		]
	}`, http.StatusCreated)
	rubrics := getJSON[struct {
		Rubrics []managedagents.EvaluationRubric `json:"rubrics"`
	}](t, server, "/v2/evaluation-rubrics?workspace_id="+url.QueryEscape(leftSession.WorkspaceID))
	if len(rubrics.Rubrics) != 1 || rubrics.Rubrics[0].ID != rubric.ID {
		t.Fatalf("unexpected rubric list: %+v", rubrics.Rubrics)
	}

	requestBody := `{"left_session_id":"` + leftSession.ID + `","left_turn_id":"` + leftTurn +
		`","right_session_id":"` + rightSession.ID + `","right_turn_id":"` + rightTurn +
		`","rubric_id":"` + rubric.ID + `","scores":[{"criterion_id":"quality","left_score":3,"right_score":5},` +
		`{"criterion_id":"safety","left_score":4,"right_score":4}],"conclusion":"right","notes":"B is clearer"}`
	evaluation := postJSONWithStatus[managedagents.RunEvaluation](t, server, http.MethodPost, "/v2/run-evaluations", requestBody, http.StatusCreated)
	if evaluation.RubricSnapshot.RubricID != rubric.ID || evaluation.Conclusion != managedagents.EvaluationConclusionRight {
		t.Fatalf("unexpected evaluation: %+v", evaluation)
	}
	history := getJSON[struct {
		Evaluations []managedagents.RunEvaluation `json:"evaluations"`
	}](t, server, "/v2/run-evaluations?left_session_id="+url.QueryEscape(leftSession.ID)+"&left_turn_id="+url.QueryEscape(leftTurn)+
		"&right_session_id="+url.QueryEscape(rightSession.ID)+"&right_turn_id="+url.QueryEscape(rightTurn)+"&limit=20")
	if len(history.Evaluations) != 1 || history.Evaluations[0].ID != evaluation.ID {
		t.Fatalf("unexpected evaluation history: %+v", history.Evaluations)
	}

	invalidLimit := httptest.NewRecorder()
	server.ServeHTTP(invalidLimit, httptest.NewRequest(http.MethodGet, "/v2/run-evaluations?limit=bad", nil))
	if invalidLimit.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, want 400: %s", invalidLimit.Code, invalidLimit.Body.String())
	}

	invalidScores := strings.Replace(requestBody, `"left_score":3`, `"left_score":9`, 1)
	postJSONWithStatus[map[string]any](t, server, http.MethodPost, "/v2/run-evaluations", invalidScores, http.StatusBadRequest)
	autoRequestBody := `{"left_session_id":"` + leftSession.ID + `","left_turn_id":"` + leftTurn +
		`","right_session_id":"` + rightSession.ID + `","right_turn_id":"` + rightTurn +
		`","rubric_id":"` + rubric.ID + `"}`
	automatic := postJSONWithStatus[managedagents.RunEvaluation](t, server, http.MethodPost, "/v2/run-evaluations/auto", autoRequestBody, http.StatusCreated)
	if automatic.EvaluationType != managedagents.EvaluationTypeAuto || automatic.JudgeProvider != "fake" || automatic.JudgeModel != "fake-demo" {
		t.Fatalf("unexpected automatic evaluation metadata: %+v", automatic)
	}
	if len(automatic.Scores) != len(rubric.Criteria) || automatic.Conclusion != managedagents.EvaluationConclusionTie || automatic.JudgeReasoning == "" {
		t.Fatalf("unexpected automatic evaluation result: %+v", automatic)
	}

	store.providers["judge-disabled"] = managedagents.LLMProvider{
		ID: "judge-disabled", ProviderType: "fake", Enabled: false, Revision: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	disabledJudgeServer := NewServerWithStoreRunnerAndLLMDefaults(
		store, runner.NewMockRunner(store, time.Millisecond, nil), nil, "judge-disabled", "judge-model",
	)
	postJSONWithStatus[map[string]any](t, disabledJudgeServer, http.MethodPost, "/v1/run-evaluations/auto", autoRequestBody, http.StatusBadGateway)

	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 20})
	if err != nil {
		t.Fatalf("list evaluation audit: %v", err)
	}
	var rubricAudit, successfulEvaluationAudit, failedEvaluationAudit, successfulAutoAudit, failedAutoAudit bool
	for _, audit := range audits {
		switch {
		case audit.Action == "evaluation.rubric.create" && audit.Outcome == "succeeded":
			rubricAudit = true
		case audit.Action == "evaluation.run.create" && audit.Outcome == "succeeded":
			successfulEvaluationAudit = true
		case audit.Action == "evaluation.run.create" && audit.Outcome == "failed":
			failedEvaluationAudit = true
		case audit.Action == "evaluation.run.auto" && audit.Outcome == "succeeded":
			successfulAutoAudit = true
		case audit.Action == "evaluation.run.auto" && audit.Outcome == "failed":
			failedAutoAudit = true
		}
	}
	if !rubricAudit || !successfulEvaluationAudit || !failedEvaluationAudit || !successfulAutoAudit || !failedAutoAudit {
		t.Fatalf("missing evaluation audits: %+v", audits)
	}
}

func TestParseAutoJudgeResult(t *testing.T) {
	criteria := []managedagents.EvaluationCriterion{{ID: "quality"}, {ID: "safety"}}
	valid := `{"scores":[{"criterion_id":"quality","left_score":4,"right_score":3},{"criterion_id":"safety","left_score":5,"right_score":5}],"conclusion":"left","reasoning":"A 的回答更清晰。"}`
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "plain JSON", value: valid},
		{name: "fenced JSON", value: "```json\n" + valid + "\n```"},
		{name: "malformed", value: `{"scores":`, wantErr: true},
		{name: "missing criterion", value: `{"scores":[{"criterion_id":"quality","left_score":4,"right_score":3}],"conclusion":"left","reasoning":"依据"}`, wantErr: true},
		{name: "duplicate criterion", value: `{"scores":[{"criterion_id":"quality","left_score":4,"right_score":3},{"criterion_id":"quality","left_score":5,"right_score":5}],"conclusion":"left","reasoning":"依据"}`, wantErr: true},
		{name: "invalid score", value: `{"scores":[{"criterion_id":"quality","left_score":6,"right_score":3},{"criterion_id":"safety","left_score":5,"right_score":5}],"conclusion":"left","reasoning":"依据"}`, wantErr: true},
		{name: "extra content", value: valid + " trailing", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := parseAutoJudgeResult(test.value, criteria)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse automatic Judge result: %v", err)
			}
			if result.Conclusion != managedagents.EvaluationConclusionLeft || len(result.Scores) != 2 {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestEvaluationDatasetBatchExperimentLifecycle(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "batch-evaluation-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	leftTemplate := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Batch template A")
	rightTemplate := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Batch template B")
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Millisecond, nil), nil)

	rubric := postJSONWithStatus[managedagents.EvaluationRubric](t, server, http.MethodPost, "/v2/evaluation-rubrics", `{
		"workspace_id":"`+leftTemplate.WorkspaceID+`","name":"Batch rubric","criteria":[
			{"id":"quality","name":"Quality"},{"id":"safety","name":"Safety"}
		]
	}`, http.StatusCreated)
	dataset := postJSONWithStatus[managedagents.EvaluationDataset](t, server, http.MethodPost, "/v2/evaluation-datasets", `{
		"workspace_id":"`+leftTemplate.WorkspaceID+`","name":"Regression set","description":"Two prompts","items":[
			{"prompt":"Explain snapshot isolation.","expected_output":"Mention MVCC.","tags":["database"]},
			{"prompt":"Explain row-level security.","expected_output":"Mention tenant isolation.","tags":["security"]}
		]
	}`, http.StatusCreated)
	if len(dataset.Items) != 2 || dataset.Items[0].Prompt == "" {
		t.Fatalf("unexpected dataset: %+v", dataset)
	}
	datasets := getJSON[struct {
		Datasets []managedagents.EvaluationDataset `json:"datasets"`
	}](t, server, "/v2/evaluation-datasets?workspace_id="+url.QueryEscape(leftTemplate.WorkspaceID))
	if len(datasets.Datasets) != 1 || datasets.Datasets[0].ID != dataset.ID {
		t.Fatalf("unexpected datasets: %+v", datasets.Datasets)
	}

	experiment := postJSONWithStatus[managedagents.EvaluationExperiment](t, server, http.MethodPost, "/v2/evaluation-experiments", `{
		"name":"Nightly regression","dataset_id":"`+dataset.ID+`","rubric_id":"`+rubric.ID+`",
		"left_template_session_id":"`+leftTemplate.ID+`","right_template_session_id":"`+rightTemplate.ID+`"
	}`, http.StatusCreated)
	if len(experiment.Items) != 2 || experiment.Summary.Total != 2 {
		t.Fatalf("unexpected experiment: %+v", experiment)
	}
	for _, item := range experiment.Items {
		if item.LeftSessionID == "" || item.LeftTurnID == "" || item.RightSessionID == "" || item.RightTurnID == "" || item.Status != managedagents.EvaluationExperimentItemStatusRunning {
			t.Fatalf("experiment item was not started: %+v", item)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		allTerminal := true
		for _, item := range experiment.Items {
			leftRun, leftErr := store.GetSessionRunContext(t.Context(), item.LeftSessionID, item.LeftTurnID)
			rightRun, rightErr := store.GetSessionRunContext(t.Context(), item.RightSessionID, item.RightTurnID)
			if leftErr != nil || rightErr != nil || !evaluationRunTerminal(leftRun.Status) || !evaluationRunTerminal(rightRun.Status) {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for experiment Runs")
		}
		time.Sleep(5 * time.Millisecond)
	}

	experiment = postJSONWithStatus[managedagents.EvaluationExperiment](t, server, http.MethodPost, "/v2/evaluation-experiments/"+experiment.ID+"/reconcile", `{}`, http.StatusOK)
	if experiment.Status != managedagents.EvaluationExperimentStatusCompleted || experiment.Summary.Completed != 2 || experiment.Summary.Ties != 2 {
		t.Fatalf("unexpected completed experiment summary: %+v", experiment)
	}
	for _, item := range experiment.Items {
		if item.EvaluationID == "" || item.Status != managedagents.EvaluationExperimentItemStatusCompleted || item.LeftAverage != 3 || item.RightAverage != 3 {
			t.Fatalf("experiment item was not evaluated: %+v", item)
		}
	}

	experiments := getJSON[struct {
		Experiments []managedagents.EvaluationExperiment `json:"experiments"`
	}](t, server, "/v2/evaluation-experiments?workspace_id="+url.QueryEscape(leftTemplate.WorkspaceID)+"&limit=10")
	if len(experiments.Experiments) != 1 || experiments.Experiments[0].ID != experiment.ID {
		t.Fatalf("unexpected experiments: %+v", experiments.Experiments)
	}
	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 50})
	if err != nil {
		t.Fatalf("list experiment audit: %v", err)
	}
	actions := map[string]bool{}
	for _, audit := range audits {
		if audit.Outcome == "succeeded" {
			actions[audit.Action] = true
		}
	}
	for _, action := range []string{"evaluation.dataset.create", "evaluation.experiment.create", "evaluation.experiment.reconcile"} {
		if !actions[action] {
			t.Fatalf("missing successful %s audit: %+v", action, audits)
		}
	}
}

func createCompletedEvaluationRun(t *testing.T, store *testStore, sessionID string, prompt string, result string) string {
	t.Helper()
	events, err := store.AppendEvents(sessionID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"` + prompt + `"}]}`),
	}})
	if err != nil {
		t.Fatalf("append evaluation Run: %v", err)
	}
	turnID := payloadString(events[len(events)-1].Payload, "turn_id")
	if _, err := store.CompleteSessionTurn(sessionID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"`+result+`"}]}`)); err != nil {
		t.Fatalf("complete evaluation Run: %v", err)
	}
	return turnID
}
