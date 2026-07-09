package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommandSessionConfigUpgradeToCurrent(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/sessions/sesn_000001/config/upgrade" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["to_current"] != true || body["updated_by"] != "tester" {
			t.Fatalf("unexpected upgrade request: %#v", body)
		}
		return jsonResponse(`{"changed":true,"old_agent_config_version":1,"new_agent_config_version":2}`), nil
	})

	if err := commandSessionConfig(client, []string{
		"upgrade",
		"--session", "sesn_000001",
		"--to-current",
		"--updated-by", "tester",
	}); err != nil {
		t.Fatalf("session config upgrade: %v", err)
	}
}

func TestCommandSessionConfigUpgradeRequiresToCurrent(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})

	if err := commandSessionConfig(client, []string{"upgrade", "--session", "sesn_000001"}); err == nil {
		t.Fatal("expected missing --to-current error")
	}
}
