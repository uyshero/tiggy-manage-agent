package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func newTestServer() http.Handler {
	store := newTestStore()
	return NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestServer().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}

	if body["service"] != serviceName {
		t.Fatalf("expected service %q, got %q", serviceName, body["service"])
	}
}

func TestLLMProviderManagement(t *testing.T) {
	server := newTestServer()

	created := postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id": "volcengine-agent-plan",
		"provider_type": "openai",
		"base_url": "https://ark.cn-beijing.volces.com/api/plan/v3",
		"api_key_env": "TMA_LLM_API_KEY_VOLCENGINE"
	}`)
	if created.ID != "volcengine-agent-plan" || !created.Enabled {
		t.Fatalf("unexpected created provider: %+v", created)
	}
	if created.APIKeyEnv != "TMA_LLM_API_KEY_VOLCENGINE" {
		t.Fatalf("expected api key env reference only, got %q", created.APIKeyEnv)
	}

	listed := getJSON[llmProvidersResponse](t, server, "/v1/llm-providers")
	if len(listed.Providers) != 2 || listed.Providers[1].ID != created.ID {
		t.Fatalf("unexpected provider list: %+v", listed.Providers)
	}

	updated := postJSONWithStatus[managedagents.LLMProvider](t, server, http.MethodPatch, "/v1/llm-providers/"+created.ID, `{
		"base_url": "https://ark.cn-beijing.volces.com/api/v3"
	}`, http.StatusOK)
	if updated.BaseURL != "https://ark.cn-beijing.volces.com/api/v3" {
		t.Fatalf("expected updated base_url, got %q", updated.BaseURL)
	}
	if updated.ProviderType != "openai" || updated.APIKeyEnv != "TMA_LLM_API_KEY_VOLCENGINE" {
		t.Fatalf("expected update to preserve omitted fields, got %+v", updated)
	}

	disabled := postJSONWithStatus[managedagents.LLMProvider](t, server, http.MethodPost, "/v1/llm-providers/"+created.ID+"/disable", `{}`, http.StatusOK)
	if disabled.Enabled {
		t.Fatalf("expected provider disabled, got %+v", disabled)
	}

	enabled := postJSONWithStatus[managedagents.LLMProvider](t, server, http.MethodPost, "/v1/llm-providers/"+created.ID+"/enable", `{}`, http.StatusOK)
	if !enabled.Enabled {
		t.Fatalf("expected provider enabled, got %+v", enabled)
	}
}

func TestCreateAgentRejectsDisabledLLMProvider(t *testing.T) {
	server := newTestServer()
	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id": "disabled-provider",
		"provider_type": "openai",
		"enabled": false
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewBufferString(`{
		"name": "Code Assistant",
		"llm_provider": "disabled-provider",
		"llm_model": "gpt-4o"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d for disabled provider, got %d: %s", http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestAgentConfigVersionUpdateKeepsExistingSessionsPinned(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1",
		"system": "version one"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	oldSession := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if oldSession.AgentConfigVersion != 1 {
		t.Fatalf("expected old session pinned to config version 1, got %d", oldSession.AgentConfigVersion)
	}

	updated := postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)
	if updated.CurrentConfigVersion != 2 {
		t.Fatalf("expected agent current config version 2, got %d", updated.CurrentConfigVersion)
	}
	if updated.ConfigVersion.LLMProvider != "fake" {
		t.Fatalf("expected update to inherit llm provider fake, got %q", updated.ConfigVersion.LLMProvider)
	}
	if updated.ConfigVersion.LLMModel != "fake-v2" || updated.ConfigVersion.System != "version two" {
		t.Fatalf("unexpected updated config version: %+v", updated.ConfigVersion)
	}

	versions := getJSON[agentConfigVersionsResponse](t, server, "/v1/agents/"+agent.ID+"/config-versions")
	if len(versions.ConfigVersions) != 2 {
		t.Fatalf("expected 2 config versions, got %d", len(versions.ConfigVersions))
	}
	if versions.ConfigVersions[0].LLMModel != "fake-v1" || versions.ConfigVersions[1].LLMModel != "fake-v2" {
		t.Fatalf("unexpected config versions: %+v", versions.ConfigVersions)
	}

	newSession := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if newSession.AgentConfigVersion != 2 {
		t.Fatalf("expected new session pinned to config version 2, got %d", newSession.AgentConfigVersion)
	}

	oldSessionAfterUpdate := getJSON[managedagents.Session](t, server, "/v1/sessions/"+oldSession.ID)
	if oldSessionAfterUpdate.AgentConfigVersion != 1 {
		t.Fatalf("expected old session to remain pinned to config version 1, got %d", oldSessionAfterUpdate.AgentConfigVersion)
	}
}

func TestManagedAgentsMinimumFlow(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)

	if agent.ID == "" {
		t.Fatal("expected agent id")
	}
	if agent.CurrentConfigVersion != 1 {
		t.Fatalf("expected current version 1, got %d", agent.CurrentConfigVersion)
	}
	if agent.ConfigVersion.LLMProvider != "fake" {
		t.Fatalf("expected default llm provider fake, got %q", agent.ConfigVersion.LLMProvider)
	}
	if agent.ConfigVersion.LLMModel != "gpt-4o" {
		t.Fatalf("expected llm model gpt-4o, got %q", agent.ConfigVersion.LLMModel)
	}

	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {
			"type": "cloud",
			"networking": {
				"type": "limited",
				"allowed_hosts": ["api.github.com"]
			}
		}
	}`)

	if environment.ID == "" {
		t.Fatal("expected environment id")
	}

	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`",
		"title": "First TMA task"
	}`)

	if session.ID == "" {
		t.Fatal("expected session id")
	}
	if session.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session status %q, got %q", managedagents.SessionStatusIdle, session.Status)
	}

	appendResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [
			{
				"type": "user.message",
				"payload": {
					"content": [{"type": "text", "text": "hello"}]
				}
			}
		]
	}`)

	if len(appendResponse.Events) != 2 {
		t.Fatalf("expected 2 appended events, got %d", len(appendResponse.Events))
	}
	if appendResponse.Events[0].Type != managedagents.EventSessionStatusRunning {
		t.Fatalf("expected first appended event %q, got %q", managedagents.EventSessionStatusRunning, appendResponse.Events[0].Type)
	}
	if appendResponse.Events[1].Type != managedagents.EventUserMessage {
		t.Fatalf("expected second appended event %q, got %q", managedagents.EventUserMessage, appendResponse.Events[1].Type)
	}
	if appendResponse.Events[1].Seq != 4 {
		t.Fatalf("expected user event seq 4 after session status events, got %d", appendResponse.Events[1].Seq)
	}
	turnID := payloadString(appendResponse.Events[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected user.message payload to include turn_id")
	}
	if got := payloadString(appendResponse.Events[0].Payload, "turn_id"); got != turnID {
		t.Fatalf("expected running status turn_id %q, got %q", turnID, got)
	}

	runningSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if runningSession.Status != managedagents.SessionStatusRunning {
		t.Fatalf("expected session status %q immediately after user.message, got %q", managedagents.SessionStatusRunning, runningSession.Status)
	}

	waitFor(t, func() bool {
		idleSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
		return idleSession.Status == managedagents.SessionStatusIdle
	})

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events.Events))
	}
	if events.Events[0].Type != managedagents.EventSessionStatusProvisioning {
		t.Fatalf("expected first event %q, got %q", managedagents.EventSessionStatusProvisioning, events.Events[0].Type)
	}
	if events.Events[1].Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("expected second event %q, got %q", managedagents.EventSessionStatusIdle, events.Events[1].Type)
	}
	if events.Events[2].Type != managedagents.EventSessionStatusRunning {
		t.Fatalf("expected third event %q, got %q", managedagents.EventSessionStatusRunning, events.Events[2].Type)
	}
	for _, event := range events.Events[2:] {
		if got := payloadString(event.Payload, "turn_id"); got != turnID {
			t.Fatalf("expected event %s to use turn_id %q, got %q", event.Type, turnID, got)
		}
	}

	eventsAfterSeq := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events?after_seq=2")
	if len(eventsAfterSeq.Events) != 4 {
		t.Fatalf("expected 4 events after seq 2, got %d", len(eventsAfterSeq.Events))
	}
	if eventsAfterSeq.Events[1].Type != managedagents.EventUserMessage {
		t.Fatalf("expected user.message event, got %q", eventsAfterSeq.Events[1].Type)
	}
	if eventsAfterSeq.Events[2].Type != managedagents.EventAgentMessage {
		t.Fatalf("expected agent.message event, got %q", eventsAfterSeq.Events[2].Type)
	}
}

func TestAppendEventsUsesInjectedRunner(t *testing.T) {
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(newTestStore(), recorder, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"run"}]}}]
	}`)
	turnID := payloadString(startResponse.Events[1].Payload, "turn_id")
	if len(recorder.starts) != 1 {
		t.Fatalf("expected 1 runner start, got %d", len(recorder.starts))
	}
	if recorder.starts[0].SessionID != session.ID || recorder.starts[0].TurnID != turnID {
		t.Fatalf("unexpected runner start request: %+v", recorder.starts[0])
	}
	if recorder.starts[0].UserEventSeq != startResponse.Events[1].Seq {
		t.Fatalf("expected runner user event seq %d, got %d", startResponse.Events[1].Seq, recorder.starts[0].UserEventSeq)
	}

	postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.interrupt"}]
	}`)
	if len(recorder.interrupts) != 1 {
		t.Fatalf("expected 1 runner interrupt, got %d", len(recorder.interrupts))
	}
	if recorder.interrupts[0].SessionID != session.ID || recorder.interrupts[0].TurnID != turnID {
		t.Fatalf("unexpected runner interrupt request: %+v", recorder.interrupts[0])
	}
}

func TestRunnerStartFailureMarksTurnFailedAndSessionIdle(t *testing.T) {
	server := NewServerWithStoreAndRunner(newTestStore(), failingRunner{}, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"run"}]}}]
	}`)
	turnID := payloadString(startResponse.Events[1].Payload, "turn_id")

	idleSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if idleSession.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session status %q, got %q", managedagents.SessionStatusIdle, idleSession.Status)
	}

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 5 {
		t.Fatalf("expected 5 events after runner start failure, got %d", len(events.Events))
	}
	idleEvent := events.Events[4]
	if idleEvent.Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("expected idle event %q, got %q", managedagents.EventSessionStatusIdle, idleEvent.Type)
	}
	if got := payloadString(idleEvent.Payload, "turn_id"); got != turnID {
		t.Fatalf("expected failed event turn_id %q, got %q", turnID, got)
	}
	if got := payloadString(idleEvent.Payload, "last_turn_status"); got != "failed" {
		t.Fatalf("expected last_turn_status %q, got %q", "failed", got)
	}
	if got := payloadString(idleEvent.Payload, "reason"); got != "runner unavailable" {
		t.Fatalf("expected failed reason %q, got %q", "runner unavailable", got)
	}

	secondResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"retry"}]}}]
	}`)
	if len(secondResponse.Events) != 2 {
		t.Fatalf("expected retry user.message to be accepted with 2 immediate events, got %d", len(secondResponse.Events))
	}
}

func TestStreamSessionEventsReplaysHistoryAfterSeq(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID+"/events/stream?after_seq=1", nil).WithContext(ctx)
	response := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, request)
		close(done)
	}()

	waitFor(t, func() bool {
		return strings.Contains(response.Body.String(), "event: session.status_idle") &&
			strings.Contains(response.Body.String(), ": stream ready")
	})
	cancel()
	<-done

	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, body)
	}
	if strings.Contains(body, "event: session.status_provisioning") {
		t.Fatalf("did not expect provisioning event after seq 1: %s", body)
	}
	if !strings.Contains(body, "event: session.status_idle") {
		t.Fatalf("expected idle event in stream: %s", body)
	}
	if !strings.Contains(body, `"seq":2`) {
		t.Fatalf("expected seq 2 event in stream: %s", body)
	}
}

func TestArchiveSessionTerminatesAndBlocksNewEvents(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	archived := postJSONWithStatus[managedagents.Session](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/archive", `{}`, http.StatusOK)
	if archived.Status != managedagents.SessionStatusTerminated {
		t.Fatalf("expected archived session status %q, got %q", managedagents.SessionStatusTerminated, archived.Status)
	}

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 3 {
		t.Fatalf("expected 3 events after archive, got %d", len(events.Events))
	}
	if events.Events[2].Type != managedagents.EventSessionStatusTerminated {
		t.Fatalf("expected termination event %q, got %q", managedagents.EventSessionStatusTerminated, events.Events[2].Type)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/events", bytes.NewBufferString(`{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"blocked"}]}}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status %d after append to terminated session, got %d: %s", http.StatusConflict, response.Code, response.Body.String())
	}
}

func TestDeleteSessionRemovesSessionAndEvents(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	request := httptest.NewRequest(http.MethodDelete, "/v1/sessions/"+session.ID, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected delete status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
	}

	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID, nil))
	if getResponse.Code != http.StatusNotFound {
		t.Fatalf("expected get deleted session status %d, got %d: %s", http.StatusNotFound, getResponse.Code, getResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID+"/events", nil))
	if listResponse.Code != http.StatusNotFound {
		t.Fatalf("expected list deleted session events status %d, got %d: %s", http.StatusNotFound, listResponse.Code, listResponse.Body.String())
	}
}

func TestInterruptRequiresRunningSession(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"run"}]}}]
	}`)
	turnID := payloadString(startResponse.Events[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected user.message payload to include turn_id")
	}

	interruptResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.interrupt"}]
	}`)

	if len(interruptResponse.Events) != 3 {
		t.Fatalf("expected 3 interrupt events, got %d", len(interruptResponse.Events))
	}
	if interruptResponse.Events[0].Type != managedagents.EventUserInterrupt {
		t.Fatalf("expected first interrupt event %q, got %q", managedagents.EventUserInterrupt, interruptResponse.Events[0].Type)
	}
	if interruptResponse.Events[1].Type != managedagents.EventSessionStatusInterrupting {
		t.Fatalf("expected second interrupt event %q, got %q", managedagents.EventSessionStatusInterrupting, interruptResponse.Events[1].Type)
	}
	if interruptResponse.Events[2].Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("expected third interrupt event %q, got %q", managedagents.EventSessionStatusIdle, interruptResponse.Events[2].Type)
	}
	for _, event := range interruptResponse.Events {
		if got := payloadString(event.Payload, "turn_id"); got != turnID {
			t.Fatalf("expected interrupt event %s to use turn_id %q, got %q", event.Type, turnID, got)
		}
	}

	idleSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if idleSession.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session status %q after interrupt, got %q", managedagents.SessionStatusIdle, idleSession.Status)
	}

	time.Sleep(runner.DefaultMockTurnDelay + 100*time.Millisecond)

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 7 {
		t.Fatalf("expected 7 events after interrupted turn, got %d", len(events.Events))
	}
	for _, event := range events.Events {
		if event.Type == managedagents.EventAgentMessage {
			t.Fatalf("did not expect agent.message after interrupt: %+v", events.Events)
		}
	}
}

type eventsResponse struct {
	Events []managedagents.Event `json:"events"`
}

type llmProvidersResponse struct {
	Providers []managedagents.LLMProvider `json:"providers"`
}

type agentConfigVersionsResponse struct {
	ConfigVersions []managedagents.AgentConfigVersion `json:"config_versions"`
}

type recordingRunner struct {
	starts     []runner.TurnRequest
	interrupts []runner.InterruptRequest
}

func (r *recordingRunner) StartTurn(_ context.Context, request runner.TurnRequest) error {
	r.starts = append(r.starts, request)
	return nil
}

func (r *recordingRunner) InterruptTurn(_ context.Context, request runner.InterruptRequest) error {
	r.interrupts = append(r.interrupts, request)
	return nil
}

type failingRunner struct{}

func (failingRunner) StartTurn(context.Context, runner.TurnRequest) error {
	return errors.New("runner unavailable")
}

func (failingRunner) InterruptTurn(context.Context, runner.InterruptRequest) error {
	return nil
}

func postJSON[T any](t *testing.T, handler http.Handler, path string, body string) T {
	t.Helper()
	return postJSONWithStatus[T](t, handler, http.MethodPost, path, body, http.StatusCreated)
}

func postJSONWithStatus[T any](t *testing.T, handler http.Handler, method, path string, body string, expectedStatus int) T {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != expectedStatus {
		t.Fatalf("%s %s expected status %d, got %d: %s", method, path, expectedStatus, response.Code, response.Body.String())
	}

	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode %s %s response: %v", method, path, err)
	}

	return value
}

func getJSON[T any](t *testing.T, handler http.Handler, path string) T {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET %s expected status %d, got %d: %s", path, http.StatusOK, response.Code, response.Body.String())
	}

	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode GET %s response: %v", path, err)
	}

	return value
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition was not met")
}
