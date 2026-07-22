package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/tools"
)

func TestSessionRuntimeSettingsPersistsValidatedPermissionRules(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "permission-rules-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	session := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Permission rules")
	server := &Server{store: store}

	rules := []tools.PermissionRule{{
		ID: "source-edit", Tool: "default.edit_file", Argument: "path",
		Pattern: "/workspace/src/**", Behavior: tools.PermissionRuleAllow,
	}}
	updated, err := server.applySessionRuntimeSettingsPatch(t.Context(), session, sessionRuntimeSettingsRequest{PermissionRules: &rules})
	if err != nil {
		t.Fatalf("persist permission rules: %v", err)
	}
	parsed, err := tools.ParsePermissionRules(updated.RuntimeSettings)
	if err != nil || len(parsed) != 1 || parsed[0].ID != "source-edit" || parsed[0].Source != "session" {
		t.Fatalf("persisted rules = %#v, err=%v", parsed, err)
	}

	invalid := []tools.PermissionRule{{
		ID: "bash", Tool: "default.run_command", Argument: "command",
		Pattern: "rm *", Behavior: tools.PermissionRuleDeny,
	}}
	if _, err := server.applySessionRuntimeSettingsPatch(t.Context(), updated, sessionRuntimeSettingsRequest{PermissionRules: &invalid}); err == nil {
		t.Fatal("unsupported permission rule was accepted")
	}
}

func TestRerunSessionPreservesConfigSnapshotAndAllowsModelOverride(t *testing.T) {
	store := newTestStore()
	for _, model := range []string{"fake-v1", "fake-v2"} {
		if _, err := store.UpsertLLMModel(managedagents.UpsertLLMModelInput{
			ProviderID: "fake", Model: model, ContextWindowTokens: managedagents.DefaultContextWindowTokens,
		}); err != nil {
			t.Fatalf("create model %s: %v", model, err)
		}
	}
	agent := mustCreateAgentForSubagentTest(t, store, "rerun-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	source := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Review this change")
	if _, err := store.UpdateSessionRuntimeSettings(source.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings:  json.RawMessage(`{"intervention_mode":"auto_approve","llm_provider":"fake","llm_model":"fake-v1","tool_runtime":"cloud_sandbox"}`),
		ExpectedRevision: source.RuntimeSettingsRevision,
	}); err != nil {
		t.Fatalf("set source runtime settings: %v", err)
	}
	sourceEvents, err := store.AppendEvents(source.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"Find the regression."}]}`),
	}})
	if err != nil {
		t.Fatalf("append source message: %v", err)
	}
	turnID := payloadString(sourceEvents[len(sourceEvents)-1].Payload, "turn_id")
	if _, err := store.CompleteSessionTurn(source.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"Original result"}]}`)); err != nil {
		t.Fatalf("complete source turn: %v", err)
	}
	updatedAgent, err := store.CreateAgentConfigVersion(managedagents.CreateAgentConfigVersionInput{
		AgentID: agent.ID, LLMProvider: "fake", LLMModel: "fake-demo", System: "A newer system prompt.",
	})
	if err != nil {
		t.Fatalf("create newer agent config: %v", err)
	}
	if updatedAgent.CurrentConfigVersion != 2 {
		t.Fatalf("expected current config version 2, got %d", updatedAgent.CurrentConfigVersion)
	}

	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Millisecond, nil), nil)
	rerun := postJSONWithStatus[rerunSessionResponse](t, server, http.MethodPost, "/v1/sessions/"+source.ID+"/rerun", `{
		"llm_provider":"fake",
		"llm_model":"fake-v2"
	}`, http.StatusCreated)
	if rerun.SourceSessionID != source.ID || rerun.SourceEventSeq == 0 {
		t.Fatalf("unexpected rerun source: %#v", rerun)
	}
	if rerun.Session.ID == source.ID || rerun.Session.AgentConfigVersion != source.AgentConfigVersion {
		t.Fatalf("expected rerun to preserve config version %d, got %#v", source.AgentConfigVersion, rerun.Session)
	}
	assertRuntimeSettings(t, rerun.Session.RuntimeSettings, map[string]any{
		"intervention_mode": "auto_approve",
		"llm_provider":      "fake",
		"llm_model":         "fake-v2",
		"tool_runtime":      "cloud_sandbox",
	})

	deadline := time.Now().Add(time.Second)
	for {
		current, getErr := store.GetSession(rerun.Session.ID)
		if getErr != nil {
			t.Fatalf("get rerun session: %v", getErr)
		}
		if current.Status == managedagents.SessionStatusIdle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rerun session did not complete: %#v", current)
		}
		time.Sleep(time.Millisecond)
	}

	comparison := getJSON[sessionComparisonResponse](t, server, "/v1/session-comparisons?left_session_id="+source.ID+"&right_session_id="+rerun.Session.ID)
	if comparison.Left.Prompt != "Find the regression." || comparison.Left.Result != "Original result" {
		t.Fatalf("unexpected left comparison: %#v", comparison.Left)
	}
	if comparison.Right.Prompt != "Find the regression." || comparison.Right.Result == "" {
		t.Fatalf("unexpected right comparison: %#v", comparison.Right)
	}
	if comparison.Left.LLMModel != "fake-v1" || comparison.Right.LLMModel != "fake-v2" {
		t.Fatalf("unexpected comparison models: left=%q right=%q", comparison.Left.LLMModel, comparison.Right.LLMModel)
	}
}

func TestRerunSessionRequiresSourceUserMessage(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "empty-rerun-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	source := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Empty task")
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Millisecond, nil), nil)

	postJSONWithStatus[map[string]any](t, server, http.MethodPost, "/v1/sessions/"+source.ID+"/rerun", `{}`, http.StatusBadRequest)
	sessions, err := store.ListSessions(managedagents.ListSessionsInput{IncludeArchived: true, Limit: 100})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != source.ID {
		t.Fatalf("expected no empty rerun session, got %#v", sessions)
	}
}
