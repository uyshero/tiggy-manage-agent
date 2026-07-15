package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommandSessionRuntimeUpdateSandboxSettings(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v2/sessions/sesn_000001/runtime-settings" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["intervention_mode"] != "approve_for_me" || body["tool_runtime"] != "cloud_sandbox" || body["cloud_sandbox_root"] != "." || body["cloud_sandbox_image"] != "onlyboxes/test:latest" || body["cloud_sandbox_allow_network"] != true {
			t.Fatalf("unexpected runtime settings request: %#v", body)
		}
		return jsonResponse(`{"runtime_settings":{"intervention_mode":"approve_for_me","tool_runtime":"cloud_sandbox","cloud_sandbox_root":".","cloud_sandbox_image":"onlyboxes/test:latest","cloud_sandbox_allow_network":true}}`), nil
	})

	if err := commandSessionRuntime(client, []string{
		"update",
		"--session", "sesn_000001",
		"--intervention-mode", "approve_for_me",
		"--tool-runtime", "cloud_sandbox",
		"--cloud-sandbox-root", ".",
		"--cloud-sandbox-image", "onlyboxes/test:latest",
		"--cloud-sandbox-allow-network",
	}); err != nil {
		t.Fatalf("session runtime update: %v", err)
	}
}
