package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCommandWorkEnqueueSandboxCommandPayload(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/worker-work" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["worker_id"] != "wrk_000001" || body["work_type"] != "sandbox_command" {
			t.Fatalf("unexpected enqueue request: %#v", body)
		}
		payload, ok := body["payload"].(map[string]any)
		if !ok || payload["command"] != "sh" {
			t.Fatalf("unexpected payload: %#v", body["payload"])
		}
		return jsonResponse(`{"id":"work_000001","status":"pending"}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{
			"enqueue",
			"--worker", "wrk_000001",
			"--type", "sandbox_command",
			"--payload", `{"command":"sh","args":["-c","printf hello"]}`,
		}); err != nil {
			t.Fatalf("work enqueue: %v", err)
		}
	})
	if !strings.Contains(stdout, `"id": "work_000001"`) {
		t.Fatalf("expected enqueue output to include work id, got %q", stdout)
	}
}

func TestCommandWorkEnqueueIncludesControlAuthHeader(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer control-secret" {
			t.Fatalf("expected control auth header, got %q", got)
		}
		return jsonResponse(`{"id":"work_000001","status":"pending"}`), nil
	})
	client.authToken = "control-secret"

	if err := commandWork(client, []string{
		"enqueue",
		"--worker", "wrk_000001",
		"--type", "sandbox_command",
		"--payload", `{"command":"sh","args":["-c","printf hello"]}`,
	}); err != nil {
		t.Fatalf("work enqueue: %v", err)
	}
}

func TestCommandWorkEnqueueToolInvocationFlags(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/worker-work" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["worker_id"] != "wrk_000001" {
			t.Fatalf("unexpected enqueue request: %#v", body)
		}
		if _, ok := body["work_type"]; ok {
			t.Fatalf("expected default tool_execution work type to be omitted, got %#v", body["work_type"])
		}
		payload, ok := body["payload"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected payload: %#v", body["payload"])
		}
		if payload["protocol_version"] != "tma.work.v1" ||
			payload["namespace"] != "default" ||
			payload["api"] != "run_command" ||
			payload["risk"] != "exec" ||
			payload["runtime"] != "local_system" {
			t.Fatalf("unexpected tool invocation payload: %#v", payload)
		}
		capabilities, ok := payload["capabilities"].([]any)
		if !ok || len(capabilities) != 1 || capabilities[0] != "exec" {
			t.Fatalf("unexpected capabilities: %#v", payload["capabilities"])
		}
		input, ok := payload["input"].(map[string]any)
		if !ok || input["command"] != "sh" {
			t.Fatalf("unexpected input: %#v", payload["input"])
		}
		return jsonResponse(`{"id":"work_000002","status":"pending"}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{
			"enqueue",
			"--worker", "wrk_000001",
			"--api", "run_command",
			"--capabilities", "exec",
			"--risk", "exec",
			"--runtime", "local_system",
			"--input", `{"command":"sh","args":["-c","printf hello"]}`,
		}); err != nil {
			t.Fatalf("work enqueue: %v", err)
		}
	})
	if !strings.Contains(stdout, `"id": "work_000002"`) {
		t.Fatalf("expected enqueue output to include work id, got %q", stdout)
	}
}

func TestCommandWorkEnqueuePrintsWorkerDiagnosticsOnConflict(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/worker-work" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusConflict,
			Status:     "409 Conflict",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"error":"conflict: no online worker matches tool invocation default_run_command runtime local_system",
				"invocation":{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","runtime":"local_system","capabilities":["exec"],"input":{}},
				"matches":0,
				"diagnostics":[
					{"worker_id":"wrk_reader","workspace_id":"wksp_default","name":"reader","worker_type":"local","status":"online","match":false,"reasons":["missing capability exec"],"runtimes":["local_system"],"apis":["default_run_command"],"capabilities":["filesystem.read"]}
				]
			}`)),
		}, nil
	})

	stdout := captureStdout(t, func() {
		err := commandWork(client, []string{
			"enqueue",
			"--api", "run_command",
			"--capabilities", "exec",
			"--runtime", "local_system",
			"--input", `{}`,
		})
		if err == nil {
			t.Fatal("expected work enqueue conflict")
		}
	})
	for _, expected := range []string{
		"worker selection failed:",
		"diagnose default_run_command runtime=local_system capabilities=exec",
		"wrk_reader reader [local/online] match=no",
		"reasons: missing capability exec",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout)
		}
	}
}

func TestCommandWorkPoll(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/workers/wrk_000001/work/poll" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("lease_seconds"); got != "45" {
			t.Fatalf("unexpected lease_seconds query %q", got)
		}
		return jsonResponse(`{"work":{"id":"work_000001","status":"leased"}}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{"poll", "--worker", "wrk_000001", "--lease-seconds", "45"}); err != nil {
			t.Fatalf("work poll: %v", err)
		}
	})
	if !strings.Contains(stdout, `"id": "work_000001"`) {
		t.Fatalf("expected poll output to include work id, got %q", stdout)
	}
}

func TestCommandWorkGet(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/worker-work/work_000001" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"id":"work_000001","status":"completed"}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{"get", "--work", "work_000001"}); err != nil {
			t.Fatalf("work get: %v", err)
		}
	})
	if !strings.Contains(stdout, `"status": "completed"`) {
		t.Fatalf("expected work get output to include status, got %q", stdout)
	}
}

func TestCommandWorkReapExpired(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/worker-work/reap-expired" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["limit"].(float64) != 25 {
			t.Fatalf("unexpected reap body: %#v", body)
		}
		return jsonResponse(`{"count":1,"expired":[{"id":"work_000001","status":"failed","error_message":"worker work lease expired at 2026-07-09T00:00:00Z"}]}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{"reap-expired", "--limit", "25"}); err != nil {
			t.Fatalf("work reap-expired: %v", err)
		}
	})
	if !strings.Contains(stdout, `"count": 1`) || !strings.Contains(stdout, `"status": "failed"`) {
		t.Fatalf("expected reap output to include failed work, got %q", stdout)
	}
}

func TestCommandWorkCancel(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/worker-work/work_000001/cancel" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["reason"] != "user stopped it" {
			t.Fatalf("unexpected cancel body: %#v", body)
		}
		return jsonResponse(`{"id":"work_000001","status":"canceled","error_message":"user stopped it"}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{"cancel", "--work", "work_000001", "--reason", "user stopped it"}); err != nil {
			t.Fatalf("work cancel: %v", err)
		}
	})
	if !strings.Contains(stdout, `"status": "canceled"`) || !strings.Contains(stdout, `"error_message": "user stopped it"`) {
		t.Fatalf("expected cancel output to include canceled work, got %q", stdout)
	}
}

func TestCommandWorkRequeue(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/worker-work/work_000001/requeue" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["clear_worker"] != true {
			t.Fatalf("unexpected requeue body: %#v", body)
		}
		return jsonResponse(`{"id":"work_000002","status":"pending","payload":{"ok":true}}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{"requeue", "--work", "work_000001", "--clear-worker"}); err != nil {
			t.Fatalf("work requeue: %v", err)
		}
	})
	if !strings.Contains(stdout, `"id": "work_000002"`) || !strings.Contains(stdout, `"status": "pending"`) {
		t.Fatalf("expected requeue output to include new pending work, got %q", stdout)
	}
}

func TestCommandWorkDiagnose(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/worker-work/work_000001/diagnose" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{
			"work":{"id":"work_000001","workspace_id":"wksp_default","worker_id":"wrk_000001","work_type":"tool_execution","status":"leased","lease_expires_at":"2026-07-09T00:00:00Z"},
			"worker":{"id":"wrk_000001","workspace_id":"wksp_default","name":"viito-mac","worker_type":"local","status":"online","lease_expires_at":"2026-07-09T00:00:00Z"},
			"reasons":["work is leased but not acknowledged","work lease expired at 2026-07-09T00:00:00Z"],
			"actions":["run: bin/tma work reap-expired"]
		}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandWork(client, []string{"diagnose", "--work", "work_000001"}); err != nil {
			t.Fatalf("work diagnose: %v", err)
		}
	})
	for _, expected := range []string{
		"work diagnose work_000001",
		"status: leased",
		"worker: wrk_000001 viito-mac [local/online]",
		"work lease expired",
		"run: bin/tma work reap-expired",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout)
		}
	}
}

func TestCommandWorkAck(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/wrk_000001/work/work_000001/ack" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"id":"work_000001","status":"running"}`), nil
	})

	if err := commandWork(client, []string{"ack", "--worker", "wrk_000001", "--work", "work_000001"}); err != nil {
		t.Fatalf("work ack: %v", err)
	}
}

func TestCommandWorkHeartbeat(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/wrk_000001/work/work_000001/heartbeat" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["lease_seconds"].(float64) != 60 {
			t.Fatalf("unexpected heartbeat body: %#v", body)
		}
		return jsonResponse(`{"id":"work_000001","status":"running"}`), nil
	})

	if err := commandWork(client, []string{"heartbeat", "--worker", "wrk_000001", "--work", "work_000001", "--lease-seconds", "60"}); err != nil {
		t.Fatalf("work heartbeat: %v", err)
	}
}

func TestCommandWorkResult(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/wrk_000001/work/work_000001/result" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !body.Success || string(body.Result) != `{"ok":true}` {
			t.Fatalf("unexpected result body: success=%v result=%s", body.Success, string(body.Result))
		}
		return jsonResponse(`{"id":"work_000001","status":"completed"}`), nil
	})

	if err := commandWork(client, []string{"result", "--worker", "wrk_000001", "--work", "work_000001", "--success", "--result", `{"ok":true}`}); err != nil {
		t.Fatalf("work result: %v", err)
	}
}

func TestCommandWorkResultRequiresSingleOutcome(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	if err := commandWork(client, []string{"result", "--worker", "wrk_000001", "--work", "work_000001"}); err == nil {
		t.Fatal("expected missing outcome to fail")
	}
	if err := commandWork(client, []string{"result", "--worker", "wrk_000001", "--work", "work_000001", "--success", "--failure"}); err == nil {
		t.Fatal("expected conflicting outcomes to fail")
	}
}
