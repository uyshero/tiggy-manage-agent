package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCommandWorkerRegister(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["name"] != "viito-mac" || body["worker_type"] != "local" || body["lease_seconds"].(float64) != 30 {
			t.Fatalf("unexpected worker register request: %#v", body)
		}
		capabilities, ok := body["capabilities"].(map[string]any)
		if !ok || capabilities["shell"] != true {
			t.Fatalf("unexpected capabilities: %#v", body["capabilities"])
		}
		return jsonResponse(`{"id":"wrk_000001"}`), nil
	})

	err := commandWorker(client, []string{
		"register",
		"--name", "viito-mac",
		"--type", "local",
		"--lease-seconds", "30",
		"--capabilities", `{"shell":true}`,
	})
	if err != nil {
		t.Fatalf("worker register: %v", err)
	}
}

func TestCommandWorkerList(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/workers" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("workspace_id"); got != "wksp_default" {
			t.Fatalf("unexpected workspace query %q", got)
		}
		if got := r.URL.Query().Get("status"); got != "online" {
			t.Fatalf("unexpected status query %q", got)
		}
		return jsonResponse(`{"workers":[{"id":"wrk_000001","workspace_id":"wksp_default","name":"viito-mac","worker_type":"local","status":"online","last_seen_at":"2026-07-08T00:00:00Z","lease_expires_at":"2026-07-08T00:01:00Z","capabilities":{"namespaces":["default"],"apis":["default.run_command"],"runtimes":["local_system"],"capabilities":["exec"]}}]}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWorker(client, []string{"list", "--workspace", "wksp_default", "--status", "online"}); err != nil {
			t.Fatalf("worker list: %v", err)
		}
	})
	for _, expected := range []string{
		"workers:",
		"wrk_000001 viito-mac [local/online]",
		"workspace: wksp_default",
		"lease_expires: 2026-07-08T00:01:00Z",
		"runtimes: local_system",
		"apis: default.run_command",
		"capabilities: exec",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout)
		}
	}
}

func TestCommandWorkerDiagnose(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/diagnose" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["workspace_id"] != "wksp_default" || body["namespace"] != "default" || body["api"] != "run_command" || body["runtime"] != "local_system" {
			t.Fatalf("unexpected diagnose request: %#v", body)
		}
		capabilities, ok := body["capabilities"].([]any)
		if !ok || len(capabilities) != 1 || capabilities[0] != "exec" {
			t.Fatalf("unexpected capabilities: %#v", body["capabilities"])
		}
		return jsonResponse(`{
			"invocation":{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","runtime":"local_system","capabilities":["exec"],"input":{}},
			"matches":1,
			"diagnostics":[
				{"worker_id":"wrk_missing","workspace_id":"wksp_default","name":"missing","worker_type":"local","status":"online","match":false,"reasons":["missing capability exec"],"runtimes":["local_system"],"apis":["default.run_command"],"capabilities":["filesystem.read"]},
				{"worker_id":"wrk_match","workspace_id":"wksp_default","name":"match","worker_type":"local","status":"online","match":true,"runtimes":["local_system"],"apis":["default.run_command"],"capabilities":["exec"]}
			]
		}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWorker(client, []string{
			"diagnose",
			"--workspace", "wksp_default",
			"--api", "run_command",
			"--runtime", "local_system",
			"--capabilities", "exec",
		}); err != nil {
			t.Fatalf("worker diagnose: %v", err)
		}
	})
	for _, expected := range []string{
		"diagnose default.run_command runtime=local_system capabilities=exec",
		"wrk_missing missing [local/online] match=no",
		"reasons: missing capability exec",
		"wrk_match match [local/online] match=yes",
		"reasons: (match)",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout)
		}
	}
}

func TestCommandWorkerHeartbeat(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/wrk_000001/heartbeat" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["status"] != "online" || body["lease_seconds"].(float64) != 60 {
			t.Fatalf("unexpected heartbeat request: %#v", body)
		}
		return jsonResponse(`{"id":"wrk_000001","status":"online"}`), nil
	})

	if err := commandWorker(client, []string{"heartbeat", "--id", "wrk_000001", "--status", "online", "--lease-seconds", "60"}); err != nil {
		t.Fatalf("worker heartbeat: %v", err)
	}
}

func TestCommandWorkerArchive(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/wrk_000001/archive" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"id":"wrk_000001","status":"archived"}`), nil
	})

	if err := commandWorker(client, []string{"archive", "--id", "wrk_000001"}); err != nil {
		t.Fatalf("worker archive: %v", err)
	}
}

func TestCommandWorkerReapExpired(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/reap-expired" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["limit"].(float64) != 25 {
			t.Fatalf("unexpected worker reap request: %#v", body)
		}
		return jsonResponse(`{"count":1,"expired":[{"id":"wrk_000001","status":"offline"}]}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWorker(client, []string{"reap-expired", "--limit", "25"}); err != nil {
			t.Fatalf("worker reap-expired: %v", err)
		}
	})
	if !strings.Contains(stdout, `"count": 1`) || !strings.Contains(stdout, `"status": "offline"`) {
		t.Fatalf("expected reap output, got %q", stdout)
	}
}
