package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestSessionRuntimeSettingsAgentCoreHotUpdate(t *testing.T) {
	server := newTestServer()
	session := createAgentCoreSettingsSession(t, server)

	updated := patchSessionRuntimeSettings(t, server, session.ID, session.RuntimeSettingsRevision, `{
		"agent_core_compaction_threshold_tokens": 1000,
		"agent_core_compaction_summary_max_chars": 2000,
		"agent_core_budget": {
			"max_rounds": 12,
			"max_model_calls": 20,
			"max_tool_calls": 64,
			"max_input_tokens": 100000,
			"max_output_tokens": 20000,
			"max_reasoning_tokens": 30000,
			"max_cost_micros": 400000
		}
	}`, http.StatusOK)
	assertRuntimeSettings(t, updated.RuntimeSettings, map[string]any{
		"agent_core_compaction_threshold_tokens":  float64(1000),
		"agent_core_compaction_summary_max_chars": float64(2000),
		"agent_core_budget": map[string]any{
			"max_rounds": float64(12), "max_model_calls": float64(20), "max_tool_calls": float64(64),
			"max_input_tokens": float64(100000), "max_output_tokens": float64(20000),
			"max_reasoning_tokens": float64(30000), "max_cost_micros": float64(400000),
		},
	})

	merged := patchSessionRuntimeSettings(t, server, session.ID, updated.RuntimeSettingsRevision, `{
		"agent_core_compaction_threshold_tokens": 0,
		"agent_core_budget": {"max_rounds": 16}
	}`, http.StatusOK)
	assertRuntimeSettings(t, merged.RuntimeSettings, map[string]any{
		"agent_core_compaction_threshold_tokens":  float64(0),
		"agent_core_compaction_summary_max_chars": float64(2000),
		"agent_core_budget": map[string]any{
			"max_rounds": float64(16), "max_model_calls": float64(20), "max_tool_calls": float64(64),
			"max_input_tokens": float64(100000), "max_output_tokens": float64(20000),
			"max_reasoning_tokens": float64(30000), "max_cost_micros": float64(400000),
		},
	})
}

func TestSessionRuntimeSettingsRejectsInvalidAgentCoreValues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "negative threshold", body: `{"agent_core_compaction_threshold_tokens":-1}`, want: "must be non-negative"},
		{name: "zero summary limit", body: `{"agent_core_compaction_summary_max_chars":0}`, want: "must be positive"},
		{name: "zero budget", body: `{"agent_core_budget":{"max_model_calls":0}}`, want: "agent_core_budget.max_model_calls must be positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer()
			session := createAgentCoreSettingsSession(t, server)

			request := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+session.ID+"/runtime-settings", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", `"1"`)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
			}
			var payload map[string]string
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if !strings.Contains(payload["error"], test.want) {
				t.Fatalf("error = %q, want substring %q", payload["error"], test.want)
			}
		})
	}
}

func TestSessionRuntimeSettingsRejectsStaleRevision(t *testing.T) {
	server := newTestServer()
	session := createAgentCoreSettingsSession(t, server)
	updated := patchSessionRuntimeSettings(t, server, session.ID, session.RuntimeSettingsRevision, `{"intervention_mode":"approve_for_me"}`, http.StatusOK)
	if updated.RuntimeSettingsRevision != session.RuntimeSettingsRevision+1 {
		t.Fatalf("revision = %d, want %d", updated.RuntimeSettingsRevision, session.RuntimeSettingsRevision+1)
	}

	request := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+session.ID+"/runtime-settings", bytes.NewBufferString(`{"tool_runtime":"local_system"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(session.RuntimeSettingsRevision, 10)))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale status = %d, want 412: %s", response.Code, response.Body.String())
	}

	fetched := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if fetched.RuntimeSettingsRevision != updated.RuntimeSettingsRevision {
		t.Fatalf("stale write changed revision: got %d want %d", fetched.RuntimeSettingsRevision, updated.RuntimeSettingsRevision)
	}
	assertRuntimeSettings(t, fetched.RuntimeSettings, map[string]any{"intervention_mode": "approve_for_me"})
}

func patchSessionRuntimeSettings(t *testing.T, handler http.Handler, sessionID string, revision int64, body string, expectedStatus int) managedagents.Session {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+sessionID+"/runtime-settings", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(revision, 10)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != expectedStatus {
		t.Fatalf("runtime settings status = %d, want %d: %s", response.Code, expectedStatus, response.Body.String())
	}
	var session managedagents.Session
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatalf("decode runtime settings response: %v", err)
	}
	wantETag := strconv.Quote(strconv.FormatInt(session.RuntimeSettingsRevision, 10))
	if etag := response.Header().Get("ETag"); etag != wantETag {
		t.Fatalf("ETag = %q, want %q", etag, wantETag)
	}
	return session
}

func createAgentCoreSettingsSession(t *testing.T, server http.Handler) managedagents.Session {
	t.Helper()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Core Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "Core Environment",
		"config": {"type": "cloud"}
	}`)
	return postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
}
