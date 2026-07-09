package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/tools"
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

func TestWorkerRegistryLifecycle(t *testing.T) {
	server := newTestServer()

	created := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.read_file"],
			"runtimes": ["local_system"],
			"capabilities": ["filesystem.read"]
		},
		"metadata": {"os":"darwin"},
		"lease_seconds": 30
	}`)
	if created.ID == "" || created.Status != managedagents.WorkerStatusOnline || created.WorkerType != managedagents.WorkerTypeLocal {
		t.Fatalf("unexpected created worker: %+v", created)
	}
	if created.LastSeenAt == nil || created.LeaseExpiresAt == nil {
		t.Fatalf("expected heartbeat timestamps on created worker: %+v", created)
	}

	listed := getJSON[struct {
		Workers []managedagents.Worker `json:"workers"`
	}](t, server, "/v1/workers?workspace_id=wksp_default&status=online")
	if len(listed.Workers) != 1 || listed.Workers[0].ID != created.ID {
		t.Fatalf("unexpected workers list: %+v", listed.Workers)
	}

	heartbeat := postJSONWithStatus[managedagents.Worker](t, server, http.MethodPost, "/v1/workers/"+created.ID+"/heartbeat", `{
		"status": "draining",
		"lease_seconds": 45
	}`, http.StatusOK)
	if heartbeat.Status != managedagents.WorkerStatusDraining {
		t.Fatalf("expected draining worker, got %+v", heartbeat)
	}

	archived := postJSONWithStatus[managedagents.Worker](t, server, http.MethodPost, "/v1/workers/"+created.ID+"/archive", `{}`, http.StatusOK)
	if archived.Status != managedagents.WorkerStatusArchived || archived.ArchivedAt == nil {
		t.Fatalf("expected archived worker, got %+v", archived)
	}
}

func TestWorkerDiagnoseAPI(t *testing.T) {
	server := newTestServer()

	postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "reader-only",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["filesystem.read"]
		},
		"lease_seconds": 30
	}`)
	postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "executor",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["exec"]
		},
		"lease_seconds": 30
	}`)

	response := postJSONWithStatus[workerDiagnoseResponse](t, server, http.MethodPost, "/v1/workers/diagnose", `{
		"workspace_id": "wksp_default",
		"namespace": "default",
		"api": "run_command",
		"runtime": "local_system",
		"capabilities": ["exec"],
		"input": {}
	}`, http.StatusOK)
	if response.Invocation.ProtocolVersion != tools.WorkProtocolVersion || response.Invocation.Runtime != tools.ToolRuntimeLocalSystem {
		t.Fatalf("unexpected invocation: %+v", response.Invocation)
	}
	if response.Matches != 1 || len(response.Diagnostics) != 2 {
		t.Fatalf("unexpected diagnosis summary: %+v", response)
	}
	var sawMissing bool
	var sawMatch bool
	for _, diagnosis := range response.Diagnostics {
		switch diagnosis.Name {
		case "reader-only":
			sawMissing = true
			if diagnosis.Match || !slices.Contains(diagnosis.Reasons, "missing capability exec") {
				t.Fatalf("expected reader-only mismatch, got %+v", diagnosis)
			}
		case "executor":
			sawMatch = true
			if !diagnosis.Match || len(diagnosis.Reasons) != 0 {
				t.Fatalf("expected executor match, got %+v", diagnosis)
			}
		}
	}
	if !sawMissing || !sawMatch {
		t.Fatalf("missing expected diagnostics: %+v", response.Diagnostics)
	}
}

func TestWorkerAuthProtectsWorkerConsumerEndpoints(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndWorkerAuth(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		nil,
		nil,
		"worker-secret",
	)

	unauthorized := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/workers", `{
		"name": "viito-mac"
	}`, http.StatusUnauthorized)
	if unauthorized["error"] == "" {
		t.Fatalf("expected unauthorized worker error, got %#v", unauthorized)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/workers", bytes.NewBufferString(`{
		"name": "viito-mac",
		"worker_type": "local"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer worker-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected authorized worker register status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
	}
	var worker managedagents.Worker
	if err := json.NewDecoder(response.Body).Decode(&worker); err != nil {
		t.Fatalf("decode authorized worker: %v", err)
	}

	pollRequest := httptest.NewRequest(http.MethodGet, "/v1/workers/"+worker.ID+"/work/poll", nil)
	pollResponse := httptest.NewRecorder()
	server.ServeHTTP(pollResponse, pollRequest)
	if pollResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated poll status %d, got %d: %s", http.StatusUnauthorized, pollResponse.Code, pollResponse.Body.String())
	}

	listed := getJSON[struct {
		Workers []managedagents.Worker `json:"workers"`
	}](t, server, "/v1/workers")
	if len(listed.Workers) != 1 || listed.Workers[0].ID != worker.ID {
		t.Fatalf("expected worker list to remain control-plane visible, got %+v", listed.Workers)
	}
}

func TestControlAuthProtectsWorkerWorkControlPlaneEndpoints(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		nil,
		nil,
		"",
		"control-secret",
	)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "executor",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["exec"]
		},
		"lease_seconds": 30
	}`)

	unauthorized := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`, http.StatusUnauthorized)
	if unauthorized["error"] != "control authorization required" {
		t.Fatalf("expected control auth error, got %#v", unauthorized)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/worker-work", bytes.NewBufferString(`{
		"workspace_id": "wksp_default",
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer control-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected authorized control-plane enqueue status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
	}
	var work managedagents.WorkerWork
	if err := json.NewDecoder(response.Body).Decode(&work); err != nil {
		t.Fatalf("decode authorized work: %v", err)
	}
	if work.WorkerID != worker.ID {
		t.Fatalf("expected selected worker %q, got %+v", worker.ID, work)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/worker-work/"+work.ID, nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated work get status %d, got %d: %s", http.StatusUnauthorized, getResponse.Code, getResponse.Body.String())
	}

	getRequest = httptest.NewRequest(http.MethodGet, "/v1/worker-work/"+work.ID, nil)
	getRequest.Header.Set("Authorization", "Bearer control-secret")
	getResponse = httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized work get status %d, got %d: %s", http.StatusOK, getResponse.Code, getResponse.Body.String())
	}
}

func TestWorkerWorkLifecycle(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["exec"]
		},
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`)
	if queued.WorkerID != worker.ID {
		t.Fatalf("expected enqueue to select worker %q, got %+v", worker.ID, queued)
	}

	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=45")
	if polled.Work == nil || polled.Work.ID != queued.ID {
		t.Fatalf("expected queued work from poll, got %+v", polled.Work)
	}
	if polled.Work.Status != managedagents.WorkerWorkStatusLeased || polled.Work.WorkerID != worker.ID || polled.Work.LeaseExpiresAt == nil {
		t.Fatalf("expected leased work, got %+v", polled.Work)
	}

	acked := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/ack", `{}`, http.StatusOK)
	if acked.Status != managedagents.WorkerWorkStatusRunning || acked.StartedAt == nil {
		t.Fatalf("expected running work after ack, got %+v", acked)
	}

	heartbeat := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/heartbeat", `{
		"lease_seconds": 60
	}`, http.StatusOK)
	if heartbeat.Status != managedagents.WorkerWorkStatusRunning || heartbeat.LeaseExpiresAt == nil {
		t.Fatalf("expected running work heartbeat, got %+v", heartbeat)
	}

	completed := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/result", `{
		"success": true,
		"result": {"ok": true}
	}`, http.StatusOK)
	if completed.Status != managedagents.WorkerWorkStatusCompleted || completed.CompletedAt == nil {
		t.Fatalf("expected completed work, got %+v", completed)
	}
	if string(completed.Result) != `{"ok":true}` {
		t.Fatalf("unexpected result JSON: %s", string(completed.Result))
	}

	fetched := getJSON[managedagents.WorkerWork](t, server, "/v1/worker-work/"+queued.ID)
	if fetched.ID != queued.ID || fetched.Status != managedagents.WorkerWorkStatusCompleted || string(fetched.Result) != `{"ok":true}` {
		t.Fatalf("unexpected fetched work: %+v result=%s", fetched, string(fetched.Result))
	}

	empty := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll")
	if empty.Work != nil {
		t.Fatalf("expected no more work, got %+v", empty.Work)
	}
}

func TestReapExpiredWorkerWork(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"worker_id": "`+worker.ID+`",
		"work_type": "sandbox_command",
		"payload": {"command": "sh", "args": ["-c", "sleep 100"]}
	}`)
	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=1")
	if polled.Work == nil || polled.Work.ID != queued.ID {
		t.Fatalf("expected queued work from poll, got %+v", polled.Work)
	}

	store.mu.Lock()
	expiredAt := time.Now().UTC().Add(-time.Minute)
	work := store.workerWork[queued.ID]
	work.LeaseExpiresAt = &expiredAt
	store.workerWork[queued.ID] = work
	store.mu.Unlock()

	response := postJSONWithStatus[struct {
		Count   int                        `json:"count"`
		Expired []managedagents.WorkerWork `json:"expired"`
	}](t, server, http.MethodPost, "/v1/worker-work/reap-expired", `{"limit":10}`, http.StatusOK)
	if response.Count != 1 || len(response.Expired) != 1 {
		t.Fatalf("expected one expired work, got %+v", response)
	}
	expired := response.Expired[0]
	if expired.ID != queued.ID || expired.Status != managedagents.WorkerWorkStatusFailed || expired.CompletedAt == nil {
		t.Fatalf("unexpected expired work: %+v", expired)
	}
	if !strings.Contains(expired.ErrorMessage, "worker work lease expired") {
		t.Fatalf("expected lease expiry error message, got %q", expired.ErrorMessage)
	}

	fetched := getJSON[managedagents.WorkerWork](t, server, "/v1/worker-work/"+queued.ID)
	if fetched.Status != managedagents.WorkerWorkStatusFailed || fetched.CompletedAt == nil {
		t.Fatalf("expected fetched work to remain failed, got %+v", fetched)
	}
}

func TestWorkerWorkRejectsInvalidToolExecutionPayload(t *testing.T) {
	server := newTestServer()

	response := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/worker-work", `{
		"work_type": "tool_execution",
		"payload": {"command": "echo hello"}
	}`, http.StatusBadRequest)
	if !strings.Contains(response["error"], "unsupported tool namespace") {
		t.Fatalf("unexpected error response: %+v", response)
	}
}

func TestWorkerWorkRejectsToolExecutionWithoutMatchingWorker(t *testing.T) {
	server := newTestServer()
	postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "reader-only",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.read_file"],
			"runtimes": ["local_system"],
			"capabilities": ["filesystem.read"]
		},
		"lease_seconds": 30
	}`)

	response := postJSONWithStatus[workerWorkConflictResponse](t, server, http.MethodPost, "/v1/worker-work", `{
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`, http.StatusConflict)
	if !strings.Contains(response.Error, "no online worker matches tool invocation") {
		t.Fatalf("unexpected error response: %+v", response)
	}
	if response.Invocation.API != "run_command" || response.Matches != 0 || len(response.Diagnostics) != 1 {
		t.Fatalf("unexpected diagnostics summary: %+v", response)
	}
	diagnosis := response.Diagnostics[0]
	if diagnosis.Name != "reader-only" || diagnosis.Match || !slices.Contains(diagnosis.Reasons, "missing api default.run_command") || !slices.Contains(diagnosis.Reasons, "missing capability exec") {
		t.Fatalf("unexpected worker diagnosis: %+v", diagnosis)
	}
}

func TestLLMModelManagement(t *testing.T) {
	server := newTestServer()

	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id": "volcengine-agent-plan",
		"provider_type": "openai"
	}`)
	created := postJSONWithStatus[managedagents.LLMModel](t, server, http.MethodPost, "/v1/llm-models", `{
		"provider_id": "volcengine-agent-plan",
		"model": "doubao-test",
		"context_window_tokens": 256000
	}`, http.StatusOK)
	if created.ProviderID != "volcengine-agent-plan" || created.Model != "doubao-test" || created.ContextWindowTokens != 256000 {
		t.Fatalf("unexpected created model: %+v", created)
	}

	listed := getJSON[llmModelsResponse](t, server, "/v1/llm-models?provider_id=volcengine-agent-plan")
	if len(listed.Models) != 1 || listed.Models[0].ContextWindowTokens != 256000 {
		t.Fatalf("unexpected model list: %+v", listed.Models)
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

func TestUpgradeSessionAgentConfigToCurrent(t *testing.T) {
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
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)

	var result managedagents.UpgradeSessionAgentConfigResult
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/config/upgrade", bytes.NewBufferString(`{"to_current":true,"updated_by":"tester"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected upgrade status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Changed || result.OldAgentConfigVersion != 1 || result.NewAgentConfigVersion != 2 {
		t.Fatalf("unexpected upgrade result: %+v", result)
	}
	if result.Event.Type != managedagents.EventSessionConfigUpdated {
		t.Fatalf("expected config updated event, got %+v", result.Event)
	}
	updatedSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if updatedSession.AgentConfigVersion != 2 {
		t.Fatalf("expected session to upgrade to version 2, got %d", updatedSession.AgentConfigVersion)
	}
}

func TestUpgradeSessionAgentConfigRequiresIdleSession(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

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
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)
	if _, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"run"}]}`),
	}}); err != nil {
		t.Fatalf("start session turn: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/config/upgrade", bytes.NewBufferString(`{"to_current":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected upgrade status %d, got %d: %s", http.StatusConflict, response.Code, response.Body.String())
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

func TestSessionRuntimeSettingsHotUpdate(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	updated := postJSONWithStatus[managedagents.Session](t, server, http.MethodPatch, "/v1/sessions/"+session.ID+"/runtime-settings", `{
		"intervention_mode": "approve_for_me",
		"tool_runtime": "cloud_sandbox",
		"cloud_sandbox_root": ".",
		"cloud_sandbox_allow_network": true
	}`, http.StatusOK)
	assertRuntimeSettings(t, updated.RuntimeSettings, map[string]any{
		"intervention_mode":           "approve_for_me",
		"tool_runtime":                "cloud_sandbox",
		"cloud_sandbox_root":          ".",
		"cloud_sandbox_allow_network": true,
	})

	merged := postJSONWithStatus[managedagents.Session](t, server, http.MethodPatch, "/v1/sessions/"+session.ID+"/runtime-settings", `{
		"tool_runtime": "local_system"
	}`, http.StatusOK)
	assertRuntimeSettings(t, merged.RuntimeSettings, map[string]any{
		"intervention_mode":           "approve_for_me",
		"tool_runtime":                "local_system",
		"cloud_sandbox_root":          ".",
		"cloud_sandbox_allow_network": true,
	})

	fetched := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	assertRuntimeSettings(t, fetched.RuntimeSettings, map[string]any{
		"intervention_mode":           "approve_for_me",
		"tool_runtime":                "local_system",
		"cloud_sandbox_root":          ".",
		"cloud_sandbox_allow_network": true,
	})
}

func TestSessionInterventionApproveRejectAPI(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreAndExecutionResolver(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		objectstore.NewNoopClient(objectstore.Config{}),
		execution.SessionProviderResolver{Store: store, AllowLocalSystem: true},
	)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if _, err := store.UpdateSessionRuntimeSettings(session.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: json.RawMessage(`{"tool_runtime":"local_system"}`),
	}); err != nil {
		t.Fatalf("set local_system tool runtime: %v", err)
	}
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please read"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected started turn id")
	}

	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:            turnID,
		CallID:            "call_read",
		ToolIdentifier:    "default",
		APIName:           "read_file",
		Arguments:         json.RawMessage(`{"path":"../../README.md"}`),
		InterventionMode:  "request_approval",
		Reason:            "optional",
		Continuation:      json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"please read"}]},{"role":"assistant","content":[{"type":"text","text":""}],"tool_calls":[{"id":"call_read","type":"function","function":{"name":"default.read_file","arguments":{"path":"../../README.md"}}}]}]`),
		ContinuationRound: 0,
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	listed := getJSON[struct {
		Interventions []managedagents.SessionIntervention `json:"interventions"`
	}](t, server, "/v1/sessions/"+session.ID+"/interventions?status=pending")
	if len(listed.Interventions) != 1 {
		t.Fatalf("expected 1 pending intervention, got %#v", listed.Interventions)
	}
	if listed.Interventions[0].Status != managedagents.InterventionStatusPending {
		t.Fatalf("expected pending intervention, got %#v", listed.Interventions[0])
	}

	approved := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/approve", `{
		"reason": "looks safe"
	}`, http.StatusOK)
	if approved.Intervention.Status != managedagents.InterventionStatusApproved {
		t.Fatalf("expected approved intervention, got %#v", approved.Intervention)
	}
	expectedEventTypes := []string{
		managedagents.EventRuntimeToolInterventionApproved,
		managedagents.EventRuntimeToolResult,
		managedagents.EventRuntimeLLMRequest,
		managedagents.EventRuntimeLLMResponse,
		managedagents.EventRuntimeCompleted,
		managedagents.EventAgentMessage,
		managedagents.EventSessionStatusIdle,
	}
	if len(approved.Events) != len(expectedEventTypes) {
		t.Fatalf("expected %d events, got %#v", len(expectedEventTypes), approved.Events)
	}
	for index, eventType := range expectedEventTypes {
		if approved.Events[index].Type != eventType {
			t.Fatalf("expected event %d to be %q, got %#v", index, eventType, approved.Events)
		}
	}
	var toolResult struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if err := json.Unmarshal(approved.Events[1].Payload, &toolResult); err != nil {
		t.Fatalf("decode tool result event: %v", err)
	}
	if !toolResult.Data.Success {
		t.Fatalf("expected approved tool execution to succeed, got payload %s", string(approved.Events[1].Payload))
	}
	var agentPayload struct {
		TurnID  string `json:"turn_id"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(approved.Events[5].Payload, &agentPayload); err != nil {
		t.Fatalf("decode resumed agent message: %v", err)
	}
	if agentPayload.TurnID != turnID || len(agentPayload.Content) == 0 || !strings.Contains(agentPayload.Content[0].Text, "please read") {
		t.Fatalf("unexpected resumed agent payload: %s", string(approved.Events[5].Payload))
	}
	if len(store.usageRecords) != 1 {
		t.Fatalf("expected 1 continuation usage record, got %#v", store.usageRecords)
	}
	if usage := store.usageRecords[0]; usage.SessionID != session.ID || usage.TurnID != turnID || usage.Status != "completed" || usage.ProviderID != "fake" || usage.Model != "fake-demo" {
		t.Fatalf("unexpected continuation usage record: %#v", usage)
	}

	postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/reject", `{}`, http.StatusBadRequest)
}

func TestSessionInterventionRejectContinuesTurnWithObservation(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if _, err := store.UpdateSessionRuntimeSettings(session.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: json.RawMessage(`{"tool_runtime":"local_system"}`),
	}); err != nil {
		t.Fatalf("set local_system tool runtime: %v", err)
	}
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:           turnID,
		CallID:           "call_edit",
		ToolIdentifier:   "default",
		APIName:          "edit_file",
		Arguments:        json.RawMessage(`{"path":"README.md","old_string":"x","new_string":"y"}`),
		InterventionMode: "request_approval",
		Reason:           "optional",
		Continuation:     json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"please edit"}]},{"role":"assistant","content":[{"type":"text","text":""}],"tool_calls":[{"id":"call_edit","type":"function","function":{"name":"default.edit_file","arguments":{"path":"README.md","old_string":"x","new_string":"y"}}}]}]`),
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	rejected := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_edit/reject", `{
		"reason": "unsafe edit"
	}`, http.StatusOK)
	if rejected.Intervention.Status != managedagents.InterventionStatusRejected {
		t.Fatalf("expected rejected intervention, got %#v", rejected.Intervention)
	}
	expectedEventTypes := []string{
		managedagents.EventRuntimeToolInterventionRejected,
		managedagents.EventRuntimeToolResult,
		managedagents.EventRuntimeLLMRequest,
		managedagents.EventRuntimeLLMResponse,
		managedagents.EventRuntimeCompleted,
		managedagents.EventAgentMessage,
		managedagents.EventSessionStatusIdle,
	}
	if len(rejected.Events) != len(expectedEventTypes) {
		t.Fatalf("expected %d rejected continuation events, got %#v", len(expectedEventTypes), rejected.Events)
	}
	for index, eventType := range expectedEventTypes {
		if rejected.Events[index].Type != eventType {
			t.Fatalf("expected event %d to be %q, got %#v", index, eventType, rejected.Events[index])
		}
	}
	fetched := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if fetched.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session idle after reject, got %q", fetched.Status)
	}
	var toolResult struct {
		Data struct {
			Success        bool   `json:"success"`
			DecisionReason string `json:"decision_reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rejected.Events[1].Payload, &toolResult); err != nil {
		t.Fatalf("decode rejected tool result: %v", err)
	}
	if toolResult.Data.Success || toolResult.Data.DecisionReason != "unsafe edit" {
		t.Fatalf("unexpected rejected tool result payload: %s", string(rejected.Events[1].Payload))
	}
	var agentPayload struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rejected.Events[5].Payload, &agentPayload); err != nil {
		t.Fatalf("decode rejected continuation agent payload: %v", err)
	}
	if len(agentPayload.Content) == 0 || !strings.Contains(agentPayload.Content[0].Text, "please edit") {
		t.Fatalf("unexpected rejected continuation payload: %s", string(rejected.Events[5].Payload))
	}
	if len(store.usageRecords) != 1 || store.usageRecords[0].Status != "completed" {
		t.Fatalf("expected one completed continuation usage record, got %#v", store.usageRecords)
	}
}

func TestGetSessionTraceProjectsTurnTimeline(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	events, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please read"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(events[1].Payload, "turn_id")
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolCall,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool call request.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file"}
		}`),
	}); err != nil {
		t.Fatalf("append tool call: %v", err)
	}
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolResult,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool result.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}
		}`),
	}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete turn: %v", err)
	}

	trace := getJSON[struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
		TraceID   string `json:"trace_id"`
		Status    string `json:"status"`
		Summary   string `json:"summary"`
		Stats     struct {
			StepCount int `json:"step_count"`
			SpanCount int `json:"span_count"`
			ToolCalls int `json:"tool_calls"`
		} `json:"stats"`
		Turns []struct {
			TurnID string `json:"turn_id"`
			Status string `json:"status"`
		} `json:"turns"`
		Steps []struct {
			Type    string `json:"type"`
			APIName string `json:"api_name"`
			Outcome string `json:"outcome"`
		} `json:"steps"`
		Spans []struct {
			Name string `json:"name"`
		} `json:"spans"`
	}](t, server, "/v1/sessions/"+session.ID+"/trace?turn_id="+turnID)
	if trace.SessionID != session.ID || trace.TurnID != turnID {
		t.Fatalf("unexpected trace identity: %+v", trace)
	}
	if trace.TraceID == "" || len(trace.Spans) == 0 || trace.Spans[0].Name != "tma.interaction" {
		t.Fatalf("expected span trace projection, got %+v", trace)
	}
	if trace.Status != managedagents.TurnStatusCompleted || trace.Stats.StepCount < 4 || trace.Stats.ToolCalls != 1 {
		t.Fatalf("expected projected trace stats, got %+v", trace)
	}
	if len(trace.Turns) != 1 || trace.Turns[0].TurnID != turnID || trace.Turns[0].Status != managedagents.TurnStatusCompleted {
		t.Fatalf("expected projected turn catalog, got %+v", trace.Turns)
	}
	if !strings.Contains(trace.Summary, "tool result: default.read_file success") {
		t.Fatalf("expected projected summary to mention tool result, got %q", trace.Summary)
	}
	if len(trace.Steps) < 4 {
		t.Fatalf("expected projected steps, got %+v", trace.Steps)
	}

	perfetto := getJSON[map[string]any](t, server, "/v1/sessions/"+session.ID+"/trace?turn_id="+turnID+"&format=perfetto")
	if _, ok := perfetto["traceEvents"]; !ok {
		t.Fatalf("expected perfetto traceEvents, got %+v", perfetto)
	}
	otel := getJSON[map[string]any](t, server, "/v1/sessions/"+session.ID+"/trace?turn_id="+turnID+"&format=otel")
	if _, ok := otel["resourceSpans"]; !ok {
		t.Fatalf("expected otel resourceSpans, got %+v", otel)
	}
}

func TestMetricsEndpointAndInspectorPage(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	if _, err := store.RecordLLMUsage(managedagents.RecordLLMUsageInput{
		WorkspaceID:        managedagents.DefaultWorkspaceID,
		AgentID:            "agt_000001",
		AgentConfigVersion: 1,
		SessionID:          "sesn_000001",
		TurnID:             "turn_000001",
		ProviderID:         "fake",
		Model:              "fake-demo",
		InputTokens:        5,
		OutputTokens:       7,
		TotalTokens:        12,
		LatencyMillis:      99,
		Status:             "completed",
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if _, err := store.RegisterWorker(managedagents.RegisterWorkerInput{
		Name:       "local-worker",
		WorkerType: managedagents.WorkerTypeLocal,
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Inspector Agent",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "inspector-env",
		"config": {"type":"cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user event: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolCall,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool call request.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file"}
		}`),
	}); err != nil {
		t.Fatalf("append tool call: %v", err)
	}
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolResult,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool result.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}
		}`),
	}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete turn: %v", err)
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResponse := httptest.NewRecorder()
	server.ServeHTTP(metricsResponse, metricsRequest)
	if metricsResponse.Code != http.StatusOK {
		t.Fatalf("metrics expected status 200, got %d: %s", metricsResponse.Code, metricsResponse.Body.String())
	}
	metrics := metricsResponse.Body.String()
	if !strings.Contains(metrics, `tma_llm_tokens_total{kind="total",model="fake-demo",provider="fake"} 12`) {
		t.Fatalf("expected metrics token total, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_workers_total{status="online",type="local"} 1`) {
		t.Fatalf("expected worker gauge, got:\n%s", metrics)
	}
	sessionMetricsRequest := httptest.NewRequest(http.MethodGet, "/metrics?session_id="+session.ID+"&turn_id="+turnID, nil)
	sessionMetricsResponse := httptest.NewRecorder()
	server.ServeHTTP(sessionMetricsResponse, sessionMetricsRequest)
	if sessionMetricsResponse.Code != http.StatusOK {
		t.Fatalf("session metrics expected status 200, got %d: %s", sessionMetricsResponse.Code, sessionMetricsResponse.Body.String())
	}
	sessionMetrics := sessionMetricsResponse.Body.String()
	for _, expected := range []string{
		`tma_session_events_total{event_type="runtime.tool_call",session_id="` + session.ID + `"} 1`,
		`tma_trace_steps_total{session_id="` + session.ID + `",turn_id="` + turnID + `"} 6`,
		`tma_tool_calls_total{api_name="read_file",outcome="success",session_id="` + session.ID + `",tool_identifier="default",turn_id="` + turnID + `"} 1`,
	} {
		if !strings.Contains(sessionMetrics, expected) {
			t.Fatalf("expected session metrics to contain %q, got:\n%s", expected, sessionMetrics)
		}
	}

	inspectorRequest := httptest.NewRequest(http.MethodGet, "/inspector", nil)
	inspectorResponse := httptest.NewRecorder()
	server.ServeHTTP(inspectorResponse, inspectorRequest)
	if inspectorResponse.Code != http.StatusOK {
		t.Fatalf("inspector expected status 200, got %d: %s", inspectorResponse.Code, inspectorResponse.Body.String())
	}
	if contentType := inspectorResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("expected html content type, got %q", contentType)
	}
	if body := inspectorResponse.Body.String(); !strings.Contains(body, "TMA Inspector") ||
		!strings.Contains(body, "Turns") ||
		!strings.Contains(body, "Spans") ||
		!strings.Contains(body, "Exporters") ||
		!strings.Contains(body, "/v1/observability/status") ||
		!strings.Contains(body, "OTLP HTTP") ||
		!strings.Contains(body, "Auto refresh every 5s") ||
		!strings.Contains(body, "Copy CLI") ||
		!strings.Contains(body, "data-copy") ||
		!strings.Contains(body, "bin/tma session artifact download --session") {
		t.Fatalf("expected inspector UI body, got %q", body)
	}
}

func TestObservabilityStatusEndpoint(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	t.Setenv("TMA_PERFETTO_DIR", "/tmp/tma-traces")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_TOKEN", "secret-token")

	response := getJSON[struct {
		Perfetto struct {
			Enabled     bool   `json:"enabled"`
			Destination string `json:"destination"`
		} `json:"perfetto"`
		OTLP struct {
			Enabled       bool   `json:"enabled"`
			Destination   string `json:"destination"`
			TokenProvided bool   `json:"token_provided"`
		} `json:"otlp"`
	}](t, newTestServer(), "/v1/observability/status")
	if !response.Perfetto.Enabled || response.Perfetto.Destination != "/tmp/tma-traces" {
		t.Fatalf("unexpected perfetto status: %+v", response.Perfetto)
	}
	if !response.OTLP.Enabled || response.OTLP.Destination != "http://collector.test/v1/traces" || !response.OTLP.TokenProvided {
		t.Fatalf("unexpected otlp status: %+v", response.OTLP)
	}
}

func TestSessionInterventionContinuationCanRequireAnotherApproval(t *testing.T) {
	store := newTestStore()
	testServer := &Server{
		mux:                http.NewServeMux(),
		store:              store,
		runner:             runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		defaultLLMProvider: "fake",
		defaultLLMModel:    "fake-demo",
		continuationClient: continuationToolCallClient{},
		executionResolver:  execution.SessionProviderResolver{Store: store, AllowLocalSystem: true},
	}
	testServer.routes()
	server := testServer.mux

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if _, err := store.UpdateSessionRuntimeSettings(session.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: json.RawMessage(`{"tool_runtime":"local_system"}`),
	}); err != nil {
		t.Fatalf("set local_system tool runtime: %v", err)
	}
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")

	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:            turnID,
		CallID:            "call_read",
		ToolIdentifier:    "default",
		APIName:           "read_file",
		Arguments:         json.RawMessage(`{"path":"../../README.md"}`),
		InterventionMode:  "request_approval",
		Reason:            "optional",
		Continuation:      json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"please edit"}]},{"role":"assistant","content":[{"type":"text","text":""}],"tool_calls":[{"id":"call_read","type":"function","function":{"name":"default.read_file","arguments":{"path":"../../README.md"}}}]}]`),
		ContinuationRound: 0,
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	approved := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/approve", `{
		"reason": "read first"
	}`, http.StatusOK)
	expectedEventTypes := []string{
		managedagents.EventRuntimeToolInterventionApproved,
		managedagents.EventRuntimeToolResult,
		managedagents.EventRuntimeLLMRequest,
		managedagents.EventRuntimeLLMResponse,
		managedagents.EventRuntimeToolCall,
		managedagents.EventRuntimeToolInterventionRequired,
	}
	if len(approved.Events) != len(expectedEventTypes) {
		t.Fatalf("expected %d events, got %#v", len(expectedEventTypes), approved.Events)
	}
	for index, eventType := range expectedEventTypes {
		if approved.Events[index].Type != eventType {
			t.Fatalf("expected event %d to be %q, got %#v", index, eventType, approved.Events)
		}
	}
	listed := getJSON[struct {
		Interventions []managedagents.SessionIntervention `json:"interventions"`
	}](t, server, "/v1/sessions/"+session.ID+"/interventions?status=pending")
	if len(listed.Interventions) != 1 || listed.Interventions[0].CallID != "call_edit_again" {
		t.Fatalf("expected second pending intervention, got %#v", listed.Interventions)
	}
	fetched := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if fetched.Status != managedagents.SessionStatusRunning {
		t.Fatalf("expected session to keep running while waiting for second approval, got %q", fetched.Status)
	}
	if len(store.usageRecords) != 1 || store.usageRecords[0].TotalTokens != 14 || store.usageRecords[0].Status != "completed" {
		t.Fatalf("unexpected continuation usage records: %#v", store.usageRecords)
	}
}

func TestUserMessageWhileWaitingApprovalReturnsReminder(t *testing.T) {
	store := newTestStore()
	runner := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, runner, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:           turnID,
		CallID:           "call_edit",
		ToolIdentifier:   "default",
		APIName:          "edit_file",
		Arguments:        json.RawMessage(`{"path":"README.md","old_string":"x","new_string":"y"}`),
		InterventionMode: "request_approval",
		Reason:           "optional",
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	response := postJSONWithStatus[struct {
		Events []managedagents.Event `json:"events"`
	}](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"hello?"}]}}]
	}`, http.StatusAccepted)
	if len(response.Events) != 2 || response.Events[0].Type != managedagents.EventAgentMessage || response.Events[1].Type != managedagents.EventRuntimeToolInterventionRequired {
		t.Fatalf("expected reminder agent message and reissued approval event, got %#v", response.Events)
	}
	if len(runner.starts) != 0 {
		t.Fatalf("expected reminder not to start a new turn, got %#v", runner.starts)
	}
}

func TestGetSessionLLMUsageIncludesSummaryAndRecords(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	_, err := store.RecordLLMUsage(managedagents.RecordLLMUsageInput{
		WorkspaceID:        session.WorkspaceID,
		AgentID:            agent.ID,
		AgentConfigVersion: session.AgentConfigVersion,
		SessionID:          session.ID,
		TurnID:             "turn_000001",
		ProviderID:         "fake",
		ProviderType:       "fake",
		Model:              "fake-demo",
		InputTokens:        10,
		OutputTokens:       5,
		TotalTokens:        15,
		CachedInputTokens:  2,
		ReasoningTokens:    1,
		LatencyMillis:      120,
		Status:             "completed",
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}
	_, err = store.RecordLLMUsage(managedagents.RecordLLMUsageInput{
		WorkspaceID:        session.WorkspaceID,
		AgentID:            agent.ID,
		AgentConfigVersion: session.AgentConfigVersion,
		SessionID:          session.ID,
		TurnID:             "turn_000002",
		ProviderID:         "fake",
		ProviderType:       "fake",
		Model:              "fake-demo",
		InputTokens:        7,
		OutputTokens:       3,
		TotalTokens:        10,
		LatencyMillis:      80,
		Status:             "completed",
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}

	report := getJSON[managedagents.LLMUsageReport](t, server, "/v1/sessions/"+session.ID+"/usage")
	if report.SessionID != session.ID {
		t.Fatalf("expected session_id %q, got %q", session.ID, report.SessionID)
	}
	if report.Summary.RecordCount != 2 || report.Summary.InputTokens != 17 || report.Summary.OutputTokens != 8 || report.Summary.TotalTokens != 25 {
		t.Fatalf("unexpected usage summary: %+v", report.Summary)
	}
	if report.Summary.CachedInputTokens != 2 || report.Summary.ReasoningTokens != 1 || report.Summary.LatencyMillis != 200 {
		t.Fatalf("unexpected usage summary details: %+v", report.Summary)
	}
	if len(report.Records) != 2 || report.Records[0].TurnID != "turn_000001" || report.Records[1].TurnID != "turn_000002" {
		t.Fatalf("unexpected usage records: %+v", report.Records)
	}
}

func TestUpsertSessionSummaryWritesCompactionEvents(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	result := postJSONWithStatus[managedagents.UpsertSessionSummaryResult](t, server, http.MethodPut, "/v1/sessions/"+session.ID+"/summary", `{
		"summary_text": "User prefers concise replies.",
		"source_until_seq": 2
	}`, http.StatusOK)
	if result.Summary.SummaryText != "User prefers concise replies." || result.Summary.SourceUntilSeq != 2 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if len(result.Events) != 2 ||
		result.Events[0].Type != managedagents.EventSessionStatusCompacting ||
		result.Events[1].Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("unexpected summary events: %+v", result.Events)
	}

	summary := getJSON[managedagents.SessionSummary](t, server, "/v1/sessions/"+session.ID+"/summary")
	if summary.SummaryText != result.Summary.SummaryText {
		t.Fatalf("expected stored summary, got %+v", summary)
	}
}

func TestListLLMUsageAggregatesByProviderAndModel(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	for _, input := range []managedagents.RecordLLMUsageInput{
		{
			WorkspaceID:        session.WorkspaceID,
			AgentID:            agent.ID,
			AgentConfigVersion: session.AgentConfigVersion,
			SessionID:          session.ID,
			TurnID:             "turn_000001",
			ProviderID:         "fake",
			Model:              "fake-demo",
			InputTokens:        10,
			OutputTokens:       5,
			TotalTokens:        15,
			Status:             "completed",
		},
		{
			WorkspaceID:        session.WorkspaceID,
			AgentID:            agent.ID,
			AgentConfigVersion: session.AgentConfigVersion,
			SessionID:          session.ID,
			TurnID:             "turn_000002",
			ProviderID:         "volcengine-agent-plan",
			Model:              "doubao-test",
			InputTokens:        20,
			OutputTokens:       10,
			TotalTokens:        30,
			Status:             "completed",
		},
	} {
		if _, err := store.RecordLLMUsage(input); err != nil {
			t.Fatalf("record usage: %v", err)
		}
	}

	report := getJSON[managedagents.LLMUsageAggregateReport](t, server, "/v1/llm-usage")
	if report.GroupBy != managedagents.LLMUsageGroupByProviderModel {
		t.Fatalf("expected default group_by provider_model, got %q", report.GroupBy)
	}
	if report.Summary.RecordCount != 2 || report.Summary.TotalTokens != 45 {
		t.Fatalf("unexpected usage summary: %+v", report.Summary)
	}
	if len(report.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", report.Groups)
	}

	filtered := getJSON[managedagents.LLMUsageAggregateReport](t, server, "/v1/llm-usage?provider_id=fake&group_by=provider")
	if filtered.GroupBy != managedagents.LLMUsageGroupByProvider {
		t.Fatalf("expected provider group_by, got %q", filtered.GroupBy)
	}
	if filtered.Summary.RecordCount != 1 || filtered.Summary.TotalTokens != 15 {
		t.Fatalf("unexpected filtered summary: %+v", filtered.Summary)
	}
	if len(filtered.Groups) != 1 || filtered.Groups[0].ProviderID != "fake" || filtered.Groups[0].Model != "" {
		t.Fatalf("unexpected filtered groups: %+v", filtered.Groups)
	}
}

func TestObjectRefsAndSessionArtifacts(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	object := postJSONWithStatus[managedagents.ObjectRef](t, server, http.MethodPost, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/sesn_000001/output.txt",
		"content_type": "text/plain",
		"size_bytes": 42,
		"checksum_sha256": "abc123",
		"metadata": {"source": "tool"},
		"created_by": "test"
	}`, http.StatusCreated)
	if object.ID != "obj_000001" || object.StorageProvider != managedagents.ObjectStorageProviderS3 || object.Visibility != managedagents.ObjectVisibilityWorkspace {
		t.Fatalf("unexpected object defaults: %+v", object)
	}
	if string(object.Metadata) != `{"source":"tool"}` {
		t.Fatalf("unexpected object metadata: %s", string(object.Metadata))
	}
	fetchedObject := getJSON[managedagents.ObjectRef](t, server, "/v1/object-refs/"+object.ID)
	if fetchedObject.ID != object.ID || fetchedObject.ObjectKey != object.ObjectKey {
		t.Fatalf("unexpected fetched object: %+v", fetchedObject)
	}

	artifact := postJSONWithStatus[managedagents.SessionArtifact](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"turn_id": "turn_000001",
		"tool_call_id": "call_write",
		"name": "output.txt",
		"artifact_type": "file",
		"metadata": {"preview": "hello"},
		"created_by": "test"
	}`, http.StatusCreated)
	if artifact.ID != "art_000001" || artifact.EnvironmentID != environment.ID || artifact.WorkspaceID != session.WorkspaceID {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}

	listed := getJSON[struct {
		Artifacts []managedagents.SessionArtifact `json:"artifacts"`
	}](t, server, "/v1/sessions/"+session.ID+"/artifacts")
	if len(listed.Artifacts) != 1 || listed.Artifacts[0].ObjectRefID != object.ID || listed.Artifacts[0].TurnID != "turn_000001" {
		t.Fatalf("unexpected session artifacts: %+v", listed.Artifacts)
	}

	foreignObject := postJSONWithStatus[managedagents.ObjectRef](t, server, http.MethodPost, "/v1/object-refs", `{
		"workspace_id": "wksp_other",
		"bucket": "tma-artifacts",
		"object_key": "wksp_other/file.txt"
	}`, http.StatusCreated)
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts", bytes.NewBufferString(`{
		"object_ref_id": "`+foreignObject.ID+`"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected workspace mismatch status %d, got %d: %s", http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestUploadSessionArtifactUsesObjectStore(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	body, contentType := multipartArtifactUpload(t, map[string]string{
		"bucket":        "tma-artifacts",
		"object_key":    "wksp_default/" + session.ID + "/uploads/output.txt",
		"turn_id":       "turn_000001",
		"tool_call_id":  "call_write",
		"metadata":      `{"preview":"hello"}`,
		"artifact_type": "file",
	}, "file", "output.txt", "hello")
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", body)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected upload status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
	}
	if len(objectStore.puts) != 1 {
		t.Fatalf("expected 1 object store put, got %#v", objectStore.puts)
	}
	if objectStore.puts[0].Bucket != "tma-artifacts" || objectStore.puts[0].Key != "wksp_default/"+session.ID+"/uploads/output.txt" || objectStore.puts[0].Content != "hello" {
		t.Fatalf("unexpected object store put: %#v", objectStore.puts[0])
	}

	var decoded struct {
		ObjectRef managedagents.ObjectRef       `json:"object_ref"`
		Artifact  managedagents.SessionArtifact `json:"artifact"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if decoded.ObjectRef.ID == "" || decoded.ObjectRef.Bucket != "tma-artifacts" || decoded.ObjectRef.ChecksumSHA256 == "" {
		t.Fatalf("unexpected object ref: %+v", decoded.ObjectRef)
	}
	if decoded.Artifact.ID == "" || decoded.Artifact.ObjectRefID != decoded.ObjectRef.ID || decoded.Artifact.TurnID != "turn_000001" {
		t.Fatalf("unexpected artifact: %+v", decoded.Artifact)
	}
}

func TestUploadSessionArtifactWithoutObjectStoreReturnsUnavailable(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	body, contentType := multipartArtifactUpload(t, map[string]string{
		"bucket": "tma-artifacts",
	}, "file", "output.txt", "hello")
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", body)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected upload status %d, got %d: %s", http.StatusServiceUnavailable, response.Code, response.Body.String())
	}
}

func TestDownloadSessionArtifactProxiesObjectContent(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{downloads: map[string]string{}}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	object := postJSON[managedagents.ObjectRef](t, server, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/`+session.ID+`/files/report.txt",
		"content_type": "text/plain",
		"size_bytes": 7
	}`)
	artifact := postJSON[managedagents.SessionArtifact](t, server, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"name": "report.txt",
		"artifact_type": "file"
	}`)

	objectStore.downloads[object.Bucket+"/"+object.ObjectKey] = "report-1"
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID+"/artifacts/"+artifact.ID+"/download", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected download status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != "report-1" {
		t.Fatalf("unexpected body: %q", got)
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("unexpected content type: %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "report.txt") {
		t.Fatalf("unexpected content disposition: %q", got)
	}
}

func TestDownloadObjectRefRequiresSessionContext(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{downloads: map[string]string{}}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	object := postJSONWithStatus[managedagents.ObjectRef](t, server, http.MethodPost, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/`+session.ID+`/files/secret.txt",
		"content_type": "text/plain",
		"size_bytes": 9,
		"visibility": "session"
	}`, http.StatusCreated)
	artifact := postJSON[managedagents.SessionArtifact](t, server, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"name": "secret.txt",
		"artifact_type": "file"
	}`)
	_ = artifact

	objectStore.downloads[object.Bucket+"/"+object.ObjectKey] = "secret-1"

	noSessionReq := httptest.NewRequest(http.MethodGet, "/v1/object-refs/"+object.ID+"/download", nil)
	noSessionResp := httptest.NewRecorder()
	server.ServeHTTP(noSessionResp, noSessionReq)
	if noSessionResp.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden without session, got %d: %s", noSessionResp.Code, noSessionResp.Body.String())
	}

	withSessionReq := httptest.NewRequest(http.MethodGet, "/v1/object-refs/"+object.ID+"/download?session_id="+session.ID, nil)
	withSessionResp := httptest.NewRecorder()
	server.ServeHTTP(withSessionResp, withSessionReq)
	if withSessionResp.Code != http.StatusOK {
		t.Fatalf("expected download status %d, got %d: %s", http.StatusOK, withSessionResp.Code, withSessionResp.Body.String())
	}
	if got := withSessionResp.Body.String(); got != "secret-1" {
		t.Fatalf("unexpected session download body: %q", got)
	}
}

func TestDeleteObjectRefRequiresArtifactCleanup(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", &fakeObjectStore{downloads: map[string]string{}})

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	object := postJSON[managedagents.ObjectRef](t, server, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/`+session.ID+`/files/report.txt",
		"size_bytes": 7
	}`)
	postJSON[managedagents.SessionArtifact](t, server, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"artifact_type": "file"
	}`)

	request := httptest.NewRequest(http.MethodDelete, "/v1/object-refs/"+object.ID, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected conflict when deleting referenced object, got %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/sessions/"+session.ID+"/artifacts/art_000001", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected artifact delete status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/object-refs/"+object.ID, nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected object delete status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
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

type llmModelsResponse struct {
	Models []managedagents.LLMModel `json:"models"`
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

type fakeObjectStore struct {
	puts      []fakeObjectStorePut
	downloads map[string]string
}

type fakeObjectStorePut struct {
	Bucket      string
	Key         string
	Content     string
	ContentType string
	SizeBytes   int64
	Checksum    string
}

func (f *fakeObjectStore) PutObject(_ context.Context, input objectstore.PutObjectInput) (objectstore.PutObjectResult, error) {
	content, err := io.ReadAll(input.Body)
	if err != nil {
		return objectstore.PutObjectResult{}, err
	}
	f.puts = append(f.puts, fakeObjectStorePut{
		Bucket:      input.Bucket,
		Key:         input.Key,
		Content:     string(content),
		ContentType: input.ContentType,
		SizeBytes:   input.SizeBytes,
		Checksum:    input.ChecksumSHA256,
	})
	return objectstore.PutObjectResult{
		Bucket:         input.Bucket,
		Key:            input.Key,
		ETag:           "fake-etag",
		SizeBytes:      input.SizeBytes,
		ChecksumSHA256: input.ChecksumSHA256,
	}, nil
}

func (f *fakeObjectStore) GetObject(_ context.Context, input objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	if f.downloads != nil {
		if content, ok := f.downloads[input.Bucket+"/"+input.Key]; ok {
			return objectstore.GetObjectResult{
				Bucket:      input.Bucket,
				Key:         input.Key,
				Body:        io.NopCloser(strings.NewReader(content)),
				ContentType: "text/plain",
				SizeBytes:   int64(len(content)),
				ETag:        "fake-download-etag",
			}, nil
		}
		if content, ok := f.downloads[input.Key]; ok {
			return objectstore.GetObjectResult{
				Bucket:      input.Bucket,
				Key:         input.Key,
				Body:        io.NopCloser(strings.NewReader(content)),
				ContentType: "text/plain",
				SizeBytes:   int64(len(content)),
				ETag:        "fake-download-etag",
			}, nil
		}
	}
	return objectstore.GetObjectResult{}, objectstore.ErrNotFound
}

func (f *fakeObjectStore) DeleteObject(context.Context, objectstore.DeleteObjectInput) error {
	return objectstore.ErrNotConfigured
}

func (f *fakeObjectStore) PresignGetObject(context.Context, objectstore.PresignGetObjectInput) (objectstore.PresignedURL, error) {
	return objectstore.PresignedURL{}, objectstore.ErrNotConfigured
}

func multipartArtifactUpload(t *testing.T, fields map[string]string, fileField string, fileName string, content string) (*bytes.Buffer, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field %s: %v", key, err)
		}
	}
	file, err := writer.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

type continuationToolCallClient struct{}

func (continuationToolCallClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID:   "call_edit_again",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "default.edit_file",
					Arguments: json.RawMessage(`{"path":"README.md","old_string":"x","new_string":"y"}`),
				},
			}},
		},
		Usage: llm.Usage{
			InputTokens:  11,
			OutputTokens: 3,
			TotalTokens:  14,
		},
	}, nil
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

func assertRuntimeSettings(t *testing.T, raw json.RawMessage, expected map[string]any) {
	t.Helper()

	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatalf("decode runtime settings: %v", err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("unexpected runtime settings size: got %#v want %#v", actual, expected)
	}
	for key, value := range expected {
		if actual[key] != value {
			t.Fatalf("unexpected runtime setting %s: got %q want %q in %#v", key, actual[key], value, actual)
		}
	}
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
