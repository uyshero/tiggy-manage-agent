package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommandAgentConfigUpdateTools(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/agents/agt_000001/config-versions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		toolsConfig, ok := body["tools"].(map[string]any)
		if !ok || toolsConfig["runtime"] != "local_system" {
			t.Fatalf("unexpected tools config: %#v", body["tools"])
		}
		toolsList, ok := toolsConfig["tools"].([]any)
		if !ok || len(toolsList) != 1 || toolsList[0] != "default" {
			t.Fatalf("unexpected enabled tools: %#v", toolsConfig["tools"])
		}
		return jsonResponse(`{"id":"agt_000001","current_config_version":2}`), nil
	})

	if err := commandAgentConfig(client, []string{
		"update",
		"--agent", "agt_000001",
		"--tools", `{"tools":["default"],"runtime":"local_system"}`,
	}); err != nil {
		t.Fatalf("agent config update: %v", err)
	}
}

func TestCommandAgentConfigUpdateMCP(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/agents/agt_000001/config-versions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mcpConfig, ok := body["mcp"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected mcp config: %#v", body["mcp"])
		}
		servers, ok := mcpConfig["servers"].([]any)
		if !ok || len(servers) != 1 {
			t.Fatalf("unexpected mcp servers: %#v", mcpConfig["servers"])
		}
		return jsonResponse(`{"id":"agt_000001","current_config_version":2}`), nil
	})

	if err := commandAgentConfig(client, []string{
		"update",
		"--agent", "agt_000001",
		"--mcp", `{"servers":[{"identifier":"filesystem","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]}`,
	}); err != nil {
		t.Fatalf("agent config update mcp: %v", err)
	}
}
