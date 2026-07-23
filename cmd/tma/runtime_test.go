package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommandSessionRuntimeUpdateSandboxSettings(t *testing.T) {
	requests := 0
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			if r.Method != http.MethodGet || r.URL.Path != "/v2/sessions/sesn_000001" {
				t.Fatalf("unexpected revision request %s %s", r.Method, r.URL.Path)
			}
			return jsonResponse(`{"id":"sesn_000001","runtime_settings_revision":7}`), nil
		}
		if r.Method != http.MethodPatch || r.URL.Path != "/v2/sessions/sesn_000001/runtime-settings" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("If-Match") != `"7"` {
			t.Fatalf("If-Match = %q, want quoted revision 7", r.Header.Get("If-Match"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["intervention_mode"] != "approve_for_me" || body["cloud_sandbox_root"] != "." || body["cloud_sandbox_image"] != "onlyboxes/test:latest" || body["cloud_sandbox_allow_network"] != true {
			t.Fatalf("unexpected runtime settings request: %#v", body)
		}
		return jsonResponse(`{"runtime_settings":{"intervention_mode":"approve_for_me","cloud_sandbox_root":".","cloud_sandbox_image":"onlyboxes/test:latest","cloud_sandbox_allow_network":true}}`), nil
	})

	if err := commandSessionRuntime(client, []string{
		"update",
		"--session", "sesn_000001",
		"--intervention-mode", "approve_for_me",
		"--cloud-sandbox-root", ".",
		"--cloud-sandbox-image", "onlyboxes/test:latest",
		"--cloud-sandbox-allow-network",
	}); err != nil {
		t.Fatalf("session runtime update: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want GET then PATCH", requests)
	}
}
