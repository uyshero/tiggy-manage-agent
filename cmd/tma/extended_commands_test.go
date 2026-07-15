package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandAgentExportAndImport(t *testing.T) {
	document := `{"format":"tma.agent","schema_version":1,"exported_at":"2026-07-15T00:00:00Z","source_agent_id":"agt/1","agent":{"name":"Portable","llm_provider":"fake","llm_model":"fake-demo","system":"S","tools":{"custom":true}}}`
	exportClient := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v2/agents/agt%2F1/export" {
			t.Fatalf("unexpected export request %s %s", r.Method, r.URL.EscapedPath())
		}
		return jsonResponse(document), nil
	})
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := commandAgent(exportClient, []string{"export", "--id", "agt/1", "--output", path}); err != nil {
		t.Fatalf("agent export: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), `"custom": true`) {
		t.Fatalf("exported agent=%s err=%v", raw, err)
	}

	importClient := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/agents/import" {
			t.Fatalf("unexpected import request %s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["workspace_id"] != "wksp/1" || request["name"] != "Imported" || request["exported_at"] != nil {
			t.Fatalf("unexpected import request: %#v", request)
		}
		agent, _ := request["agent"].(map[string]any)
		if tools, _ := agent["tools"].(map[string]any); tools["custom"] != true {
			t.Fatalf("dynamic tools were not preserved: %#v", request)
		}
		return jsonResponse(`{"id":"agt_imported","name":"Imported"}`), nil
	})
	output := captureStdout(t, func() {
		if err := commandAgent(importClient, []string{"import", "--file", path, "--name", "Imported", "--workspace", "wksp/1"}); err != nil {
			t.Fatalf("agent import: %v", err)
		}
	})
	if !strings.Contains(output, `"id": "agt_imported"`) {
		t.Fatalf("unexpected import output: %s", output)
	}
}

func TestCommandSessionCompare(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/session-comparisons" || r.URL.Query().Get("left_session_id") != "left/1" || r.URL.Query().Get("right_session_id") != "right/1" {
			t.Fatalf("unexpected comparison request: %s", r.URL.String())
		}
		return jsonResponse(`{"left":{"session":{"id":"left/1"},"prompt":"A","usage":{"records":[],"summary":{}},"artifacts":[]},"right":{"session":{"id":"right/1"},"result":"B","usage":{"records":[],"summary":{}},"artifacts":[]}}`), nil
	})
	output := captureStdout(t, func() {
		if err := commandSession(client, []string{"compare", "--left", "left/1", "--right", "right/1"}); err != nil {
			t.Fatalf("session compare: %v", err)
		}
	})
	if !strings.Contains(output, `"prompt": "A"`) || !strings.Contains(output, `"result": "B"`) {
		t.Fatalf("unexpected comparison output: %s", output)
	}
}

func TestCommandTraceListAndShowByID(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		switch r.URL.EscapedPath() {
		case "/v2/traces":
			if r.URL.Query().Get("session_id") != "sesn/1" || r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("cursor") != "opaque/1" {
				t.Fatalf("unexpected trace list query: %s", r.URL.RawQuery)
			}
			return jsonResponse(`{"items":[{"trace_id":"trace/1","session_id":"sesn/1","turn_id":"turn/1","turn_status":"future_state","duration_ms":1,"step_count":1,"span_count":0,"tool_calls":0,"errors":0}],"next_cursor":"opaque/2","has_more":true}`), nil
		case "/v2/traces/trace%2F1":
			return jsonResponse(`{"session_id":"sesn/1","turn_id":"turn/1","trace_id":"trace/1","status":"future_state","stats":{},"steps":[]}`), nil
		default:
			return nil, fmt.Errorf("unexpected trace request %s", r.URL.String())
		}
	})
	listOutput := captureStdout(t, func() {
		if err := commandTrace(client, []string{"list", "--session", "sesn/1", "--limit", "5", "--cursor", "opaque/1"}); err != nil {
			t.Fatalf("trace list: %v", err)
		}
	})
	if !strings.Contains(listOutput, `"next_cursor": "opaque/2"`) || !strings.Contains(listOutput, `"turn_status": "future_state"`) {
		t.Fatalf("unexpected trace list output: %s", listOutput)
	}
	showOutput := captureStdout(t, func() {
		if err := commandTrace(client, []string{"show", "--trace", "trace/1", "--json"}); err != nil {
			t.Fatalf("trace show by ID: %v", err)
		}
	})
	if !strings.Contains(showOutput, `"status": "future_state"`) {
		t.Fatalf("unexpected trace show output: %s", showOutput)
	}
}
