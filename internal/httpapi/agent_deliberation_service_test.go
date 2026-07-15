package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestAgentDeliberationTwoRoundStateMachineAndRestartRecovery(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Deliberation Parent")
	participantAgent := mustCreateAgentForSubagentTest(t, store, "Deliberation Participant")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parent := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "deliberation-parent")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy()).(tools.AgentDeliberationService)
	request := tools.AgentDeliberationCreateRequest{
		ParentSessionID: parent.ID,
		ParentTurnID:    "turn_deliberation",
		Objective:       "Choose a safe migration plan",
		Strategy:        tools.AgentDeliberationStrategyBrainstormCritique,
		IdempotencyKey:  "migration-plan-v1",
		Participants: []tools.AgentDeliberationParticipantRequest{
			{RoleID: "architect", RoleTitle: "Architect", Goal: "Propose the migration", AgentID: participantAgent.ID, EnvironmentID: environment.ID},
			{RoleID: "risk_reviewer", RoleTitle: "Risk Reviewer", Goal: "Challenge failure modes", AgentID: participantAgent.ID, EnvironmentID: environment.ID},
		},
		ModeratorAgentID:       parentAgent.ID,
		ModeratorEnvironmentID: environment.ID,
		Budget:                 tools.AgentDeliberationBudget{MaxTokens: 50000, MaxSeconds: 600},
	}

	created, err := service.CreateDeliberation(t.Context(), request)
	if err != nil {
		t.Fatalf("create deliberation: %v", err)
	}
	if created.Deliberation.Phase != managedagents.AgentDeliberationPhaseRound1Running || len(created.Rounds) != 1 || len(created.Participants) != 2 {
		t.Fatalf("unexpected initial deliberation: %#v", created)
	}
	roundOneGroupID := created.Rounds[0].Round.TaskGroupID
	completeDeliberationGroup(t, store, newAgentToolService(store, runner, nil, defaultSubagentPolicy()), parent.ID, roundOneGroupID, []json.RawMessage{
		json.RawMessage(`{"position":"phased migration","key_points":["dual write"],"risks":["drift"],"questions":["cutover?"],"confidence":0.8}`),
		json.RawMessage(`{"position":"require rollback","key_points":["shadow reads"],"risks":["data loss"],"questions":["rollback window?"],"confidence":0.9}`),
	})

	restartedService := newAgentToolService(store, runner, nil, defaultSubagentPolicy()).(tools.AgentDeliberationService)
	afterRestart, err := restartedService.GetDeliberation(t.Context(), tools.AgentDeliberationRequest{ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID})
	if err != nil {
		t.Fatalf("reconcile after restart: %v", err)
	}
	if afterRestart.Deliberation.Phase != managedagents.AgentDeliberationPhaseRound1Moderating || afterRestart.Rounds[0].Round.ModeratorGroupID == "" {
		t.Fatalf("expected round one moderation after restart: %#v", afterRestart)
	}
	completeDeliberationGroup(t, store, newAgentToolService(store, runner, nil, defaultSubagentPolicy()), parent.ID, afterRestart.Rounds[0].Round.ModeratorGroupID, []json.RawMessage{
		json.RawMessage(`{"agreements":["phased rollout"],"disagreements":["cutover timing"],"missing_evidence":["load test"],"questions_by_role":{"architect":["Define cutover"],"risk_reviewer":["Define rollback"]}}`),
	})

	roundTwo, err := restartedService.GetDeliberation(t.Context(), tools.AgentDeliberationRequest{ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID})
	if err != nil {
		t.Fatalf("advance to round two: %v", err)
	}
	if roundTwo.Deliberation.Phase != managedagents.AgentDeliberationPhaseRound2Running || len(roundTwo.Rounds) != 2 {
		t.Fatalf("expected round two: %#v", roundTwo)
	}
	if !strings.Contains(string(roundTwo.Rounds[0].Round.Questions), "Define cutover") {
		t.Fatalf("expected persisted moderator questions: %s", roundTwo.Rounds[0].Round.Questions)
	}
	completeDeliberationGroup(t, store, newAgentToolService(store, runner, nil, defaultSubagentPolicy()), parent.ID, roundTwo.Rounds[1].Round.TaskGroupID, []json.RawMessage{
		json.RawMessage(`{"position":"phased with gates","key_points":["canary"],"risks":["drift"],"questions":[],"confidence":0.9}`),
		json.RawMessage(`{"position":"support conditionally","key_points":["tested rollback"],"risks":["operator error"],"questions":[],"confidence":0.85}`),
	})

	finalizing, err := restartedService.GetDeliberation(t.Context(), tools.AgentDeliberationRequest{ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID})
	if err != nil {
		t.Fatalf("advance to final moderation: %v", err)
	}
	if finalizing.Deliberation.Phase != managedagents.AgentDeliberationPhaseFinalizing || finalizing.Deliberation.FinalGroupID == "" {
		t.Fatalf("expected finalizing deliberation: %#v", finalizing)
	}
	completeDeliberationGroup(t, store, newAgentToolService(store, runner, nil, defaultSubagentPolicy()), parent.ID, finalizing.Deliberation.FinalGroupID, []json.RawMessage{
		json.RawMessage(`{"recommendation":"Use phased migration with tested rollback","consensus":["canary first"],"dissenting_opinions":["cutover remains risky"],"risks":["data drift"],"followups":["load test"],"confidence":0.88}`),
	})

	completed, err := restartedService.CollectDeliberation(t.Context(), tools.AgentDeliberationRequest{ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID})
	if err != nil {
		t.Fatalf("collect completed deliberation: %v", err)
	}
	if !completed.Completed || completed.Deliberation.Status != managedagents.AgentDeliberationStatusCompleted || !strings.Contains(string(completed.Deliberation.FinalResult), "phased migration") {
		t.Fatalf("unexpected completed deliberation: %#v", completed)
	}

	idempotent, err := restartedService.CreateDeliberation(t.Context(), request)
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if idempotent.Deliberation.ID != created.Deliberation.ID {
		t.Fatalf("expected idempotent deliberation %s, got %s", created.Deliberation.ID, idempotent.Deliberation.ID)
	}
}

func TestAgentDeliberationCancelAndParticipantRetry(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "Discussion Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parent := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "discussion-parent")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy()).(tools.AgentDeliberationService)
	created, err := service.CreateDeliberation(t.Context(), tools.AgentDeliberationCreateRequest{
		ParentSessionID: parent.ID, ParentTurnID: "turn_retry", Objective: "Review launch risk",
		Participants: []tools.AgentDeliberationParticipantRequest{
			{RoleID: "proposer", RoleTitle: "Proposer", Goal: "Propose", AgentID: agent.ID, EnvironmentID: environment.ID},
			{RoleID: "critic", RoleTitle: "Critic", Goal: "Critique", AgentID: agent.ID, EnvironmentID: environment.ID},
		},
	})
	if err != nil {
		t.Fatalf("create deliberation: %v", err)
	}
	groupID := created.Rounds[0].Round.TaskGroupID
	groupService := newAgentToolService(store, runner, nil, defaultSubagentPolicy())
	group, err := groupService.GetTaskGroup(t.Context(), tools.AgentTaskGroupRequest{ParentSessionID: parent.ID, GroupID: groupID})
	if err != nil {
		t.Fatalf("get participant group: %v", err)
	}
	failedSession := group.Items[0].Session
	turnID := deliberationSessionTurnID(t, store, failedSession.ID)
	if _, err := store.FailSessionTurn(failedSession.ID, turnID, "participant failed"); err != nil {
		t.Fatalf("fail participant: %v", err)
	}
	secondTurnID := deliberationSessionTurnID(t, store, group.Items[1].Session.ID)
	if _, err := store.FailSessionTurn(group.Items[1].Session.ID, secondTurnID, "second participant failed"); err != nil {
		t.Fatalf("fail second participant: %v", err)
	}
	afterFailure, err := service.GetDeliberation(t.Context(), tools.AgentDeliberationRequest{
		ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID,
	})
	if err != nil {
		t.Fatalf("reconcile failed participant: %v", err)
	}
	if afterFailure.Deliberation.Phase != managedagents.AgentDeliberationPhaseRound1Running || afterFailure.Rounds[0].Round.Status != "failed" || afterFailure.Rounds[0].Contributions[0].Status != managedagents.TurnStatusFailed {
		t.Fatalf("failed contribution must remain retryable in its active round: %#v", afterFailure)
	}
	retried, err := service.RetryDeliberationParticipant(t.Context(), tools.AgentDeliberationRetryParticipantRequest{
		ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID, RoundNumber: 1, ParticipantIndex: 0,
	})
	if err != nil {
		t.Fatalf("retry participant: %v", err)
	}
	if retried.Rounds[0].Contributions[0].RetryCount != 1 {
		t.Fatalf("expected participant retry count, got %#v", retried.Rounds[0].Contributions)
	}
	if retried.Rounds[0].Round.Status != "running" {
		t.Fatalf("expected retried round to return to running, got %#v", retried.Rounds[0].Round)
	}
	canceled, err := service.CancelDeliberation(t.Context(), tools.AgentDeliberationCancelRequest{
		ParentSessionID: parent.ID, DeliberationID: created.Deliberation.ID, Reason: "stop discussion",
	})
	if err != nil {
		t.Fatalf("cancel deliberation: %v", err)
	}
	if canceled.Deliberation.Status != managedagents.AgentDeliberationStatusCanceled || canceled.Deliberation.CancelReason != "stop discussion" {
		t.Fatalf("unexpected canceled deliberation: %#v", canceled.Deliberation)
	}
}

func TestAgentDeliberationHTTPEndpointsAndOperatorAudit(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "HTTP Discussion Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parent := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "http-discussion-parent")
	turnRunner := &recordingRunner{}
	service := newAgentToolService(store, turnRunner, nil, defaultSubagentPolicy()).(tools.AgentDeliberationService)
	created, err := service.CreateDeliberation(t.Context(), tools.AgentDeliberationCreateRequest{
		ParentSessionID: parent.ID, ParentTurnID: "turn_http_discussion", Objective: "Choose launch controls",
		Participants: []tools.AgentDeliberationParticipantRequest{
			{RoleID: "operator", RoleTitle: "Operator", Goal: "Propose controls", AgentID: agent.ID, EnvironmentID: environment.ID},
			{RoleID: "reviewer", RoleTitle: "Reviewer", Goal: "Challenge controls", AgentID: agent.ID, EnvironmentID: environment.ID},
		},
	})
	if err != nil {
		t.Fatalf("create deliberation: %v", err)
	}
	server := NewServerWithStoreAndRunner(store, turnRunner, nil)

	strategies := getJSON[tools.AgentDeliberationStrategyListResponse](t, server, "/v1/agent/discussion-strategies")
	if len(strategies.Strategies) != 4 || len(strategies.TeamPlanSchema) == 0 {
		t.Fatalf("unexpected discussion strategies: %#v", strategies)
	}
	listed := getJSON[sessionDeliberationsResponse](t, server, "/v1/sessions/"+parent.ID+"/deliberations")
	if len(listed.Deliberations) != 1 || listed.Deliberations[0].Deliberation.ID != created.Deliberation.ID {
		t.Fatalf("unexpected deliberation list: %#v", listed)
	}
	detailPath := "/v1/sessions/" + parent.ID + "/deliberations/" + created.Deliberation.ID
	detail := getJSON[tools.AgentDeliberationResponse](t, server, detailPath)
	if detail.Deliberation.Objective != "Choose launch controls" || len(detail.Participants) != 2 {
		t.Fatalf("unexpected deliberation detail: %#v", detail)
	}

	groupService := newAgentToolService(store, turnRunner, nil, defaultSubagentPolicy())
	group, err := groupService.GetTaskGroup(t.Context(), tools.AgentTaskGroupRequest{ParentSessionID: parent.ID, GroupID: created.Rounds[0].Round.TaskGroupID})
	if err != nil {
		t.Fatalf("get participant group: %v", err)
	}
	turnID := deliberationSessionTurnID(t, store, group.Items[0].Session.ID)
	if _, err := store.FailSessionTurn(group.Items[0].Session.ID, turnID, "retry through HTTP"); err != nil {
		t.Fatalf("fail participant: %v", err)
	}
	retried := postJSONWithStatus[tools.AgentDeliberationResponse](t, server, http.MethodPost, detailPath+"/participants/0/retry", `{"round_number":1}`, http.StatusOK)
	if retried.Rounds[0].Contributions[0].RetryCount != 1 {
		t.Fatalf("expected HTTP participant retry: %#v", retried.Rounds[0].Contributions)
	}
	canceled := postJSONWithStatus[tools.AgentDeliberationResponse](t, server, http.MethodPost, detailPath+"/cancel", `{"reason":"operator stop"}`, http.StatusOK)
	if canceled.Deliberation.Status != managedagents.AgentDeliberationStatusCanceled {
		t.Fatalf("expected HTTP cancellation: %#v", canceled.Deliberation)
	}
	audit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/sessions/"+parent.ID+"/operator-audit")
	if len(audit.Records) != 2 || audit.Records[0].Action != "agent.deliberation.cancel" || audit.Records[1].Action != "agent.deliberation.participant.retry" {
		t.Fatalf("unexpected deliberation operator audit: %#v", audit.Records)
	}

	protectedServer := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(store, turnRunner, nil, "fake", "fake-demo", nil, nil, "", "control-secret")
	request := httptest.NewRequest(http.MethodPost, detailPath+"/cancel", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	protectedServer.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected discussion cancellation to require control auth, got %d: %s", response.Code, response.Body.String())
	}
}

func completeDeliberationGroup(t *testing.T, store *testStore, service tools.AgentToolService, parentSessionID string, groupID string, results []json.RawMessage) {
	t.Helper()
	group, err := service.GetTaskGroup(t.Context(), tools.AgentTaskGroupRequest{ParentSessionID: parentSessionID, GroupID: groupID})
	if err != nil {
		t.Fatalf("get group %s: %v", groupID, err)
	}
	if len(group.Items) != len(results) {
		t.Fatalf("group %s expected %d results, got %d items", groupID, len(results), len(group.Items))
	}
	for index, item := range group.Items {
		if item.Session == nil {
			t.Fatalf("group %s item %d has no session", groupID, index)
		}
		turnID := deliberationSessionTurnID(t, store, item.Session.ID)
		payload := map[string]any{
			"content":     []map[string]string{{"type": "text", "text": "structured contribution"}},
			"result_json": json.RawMessage(results[index]),
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal completion payload: %v", err)
		}
		if _, err := store.CompleteSessionTurn(item.Session.ID, turnID, encoded); err != nil {
			t.Fatalf("complete group %s item %d: %v", groupID, index, err)
		}
	}
}

func deliberationSessionTurnID(t *testing.T, store *testStore, sessionID string) string {
	t.Helper()
	events, err := store.ListEvents(sessionID, 0)
	if err != nil {
		t.Fatalf("list session events: %v", err)
	}
	for _, event := range events {
		if event.Type == managedagents.EventUserMessage {
			if turnID := payloadString(event.Payload, "turn_id"); turnID != "" {
				return turnID
			}
		}
	}
	t.Fatalf("session %s has no user-message turn", sessionID)
	return ""
}
