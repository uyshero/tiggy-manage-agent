package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestWorkspaceToolPermissionsRoundTrip(t *testing.T) {
	handler := newTestServer()
	body := []byte(`{"permission_rules":[{
		"id":"deny-secrets","tool":"default.edit_file","argument":"path",
		"pattern":"/workspace/secrets/**","behavior":"deny","reason":"protected"
	}]}`)
	request := httptest.NewRequest(http.MethodPut, "/v2/workspaces/wksp_default/tool-permissions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"1"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/workspaces/wksp_default/tool-permissions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", response.Code, response.Body.String())
	}
	var payload workspaceToolPermissionResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.WorkspaceID != "wksp_default" || payload.Revision != 2 || len(payload.PermissionRules) != 1 || payload.PermissionRules[0].ID != "deny-secrets" {
		t.Fatalf("unexpected response: %#v", payload)
	}
	if etag := response.Header().Get("ETag"); etag != `"2"` {
		t.Fatalf("ETag = %q, want quoted revision 2", etag)
	}

	stale := httptest.NewRequest(http.MethodPut, "/v2/workspaces/wksp_default/tool-permissions", bytes.NewReader(body))
	stale.Header.Set("Content-Type", "application/json")
	stale.Header.Set("If-Match", `"1"`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, stale)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected stale update status 412, got %d: %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceToolPermissionsRejectAllowRule(t *testing.T) {
	handler := newTestServer()
	body := []byte(`{"permission_rules":[{
		"id":"allow-source","tool":"default.edit_file","argument":"path",
		"pattern":"/workspace/src/**","behavior":"allow"
	}]}`)
	request := httptest.NewRequest(http.MethodPut, "/v2/workspaces/wksp_default/tool-permissions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"1"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestEvaluateWorkspaceToolPermissionWorkspaceDenyCannotBeBypassed(t *testing.T) {
	store := newTestStore()
	store.workspaceToolPolicies[managedagents.DefaultWorkspaceID] = managedagents.WorkspaceToolPermissionPolicy{
		WorkspaceID: managedagents.DefaultWorkspaceID,
		Policy: json.RawMessage(`{"permission_rules":[{
			"id":"workspace-secrets","tool":"default.edit_file","argument":"path",
			"pattern":"/workspace/secrets/**","behavior":"deny","reason":"workspace_boundary"
		}]}`),
		Revision: 2, UpdatedBy: "operator", UpdatedAt: time.Now().UTC(),
	}
	handler := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	response := evaluateToolPermission(t, handler, managedagents.DefaultWorkspaceID, map[string]any{
		"tool": "default.edit_file", "path": "/workspace/secrets/token.txt", "intervention_mode": "full_access",
	})
	if response.Decision != "deny" || response.Allowed || response.Required {
		t.Fatalf("unexpected decision: %#v", response)
	}
	if response.MatchedRuleID != "workspace-secrets" || response.RuleSource != "workspace" || response.Reason != "workspace_boundary" {
		t.Fatalf("unexpected matched rule: %#v", response)
	}
}

func TestEvaluateWorkspaceToolPermissionSessionOverridesAgentRule(t *testing.T) {
	store := newTestStore()
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: managedagents.DefaultWorkspaceID, Name: "permission-preview",
		LLMProvider: "fake", LLMModel: "fake-demo", System: "You are helpful.",
		Tools: json.RawMessage(`{"permission_rules":[{
			"id":"agent-deny-src","tool":"default.edit_file","argument":"path",
			"pattern":"/workspace/src/**","behavior":"deny"
		}]}`),
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	session := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Permission preview")
	if _, err := store.UpdateSessionRuntimeSettings(session.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: json.RawMessage(`{"intervention_mode":"request_approval","permission_rules":[{
			"id":"session-allow-src","tool":"default.edit_file","argument":"path",
			"pattern":"/workspace/src/**","behavior":"allow"
		}]}`),
		ExpectedRevision: session.RuntimeSettingsRevision,
	}); err != nil {
		t.Fatalf("update session settings: %v", err)
	}
	handler := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	response := evaluateToolPermission(t, handler, managedagents.DefaultWorkspaceID, map[string]any{
		"session_id": session.ID, "tool": "default.edit_file", "path": "/workspace/src/main.go",
	})
	if response.Decision != "allow" || !response.Allowed || response.Required {
		t.Fatalf("unexpected decision: %#v", response)
	}
	if response.AgentID != agent.ID || response.SessionID != session.ID || response.MatchedRuleID != "session-allow-src" || response.RuleSource != "session" {
		t.Fatalf("unexpected resolved context: %#v", response)
	}
}

func TestEvaluateWorkspaceToolPermissionUsesManifestFallback(t *testing.T) {
	handler := newTestServer()
	response := evaluateToolPermission(t, handler, managedagents.DefaultWorkspaceID, map[string]any{
		"tool": "default.read_file", "path": "/workspace/README.md",
	})
	if response.Decision != "allow" || !response.Allowed || response.Required || response.ApprovalPolicy != "never" {
		t.Fatalf("unexpected manifest fallback: %#v", response)
	}
	autoApproved := evaluateToolPermission(t, handler, managedagents.DefaultWorkspaceID, map[string]any{
		"tool": "default.edit_file", "path": "/workspace/README.md", "intervention_mode": "approve_for_me",
	})
	if autoApproved.Decision != "allow" || !autoApproved.Allowed || !autoApproved.Required || autoApproved.ApprovalPolicy != "conditional" {
		t.Fatalf("unexpected auto-approved fallback: %#v", autoApproved)
	}
}

func TestEvaluateWorkspaceToolPermissionRejectsCrossWorkspaceAgent(t *testing.T) {
	store := newTestStore()
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: "wksp_other", Name: "other-workspace",
		LLMProvider: "fake", LLMModel: "fake-demo", System: "You are helpful.",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	handler := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	body, _ := json.Marshal(map[string]any{
		"agent_id": agent.ID, "tool": "default.edit_file", "path": "/workspace/src/main.go",
	})
	request := httptest.NewRequest(http.MethodPost, "/v2/workspaces/"+managedagents.DefaultWorkspaceID+"/tool-permissions/evaluate", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func evaluateToolPermission(t *testing.T, handler http.Handler, workspaceID string, payload map[string]any) evaluateWorkspaceToolPermissionResponse {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v2/workspaces/"+workspaceID+"/tool-permissions/evaluate", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("evaluate status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response evaluateWorkspaceToolPermissionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}
