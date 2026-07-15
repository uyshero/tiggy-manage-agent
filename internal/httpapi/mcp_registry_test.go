package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/runner"
)

func TestMCPRegistryVersionedAgentBindingLifecycle(t *testing.T) {
	server := newTestServer()
	created := postJSONWithStatus[mcpregistry.Server](t, server, http.MethodPost, "/v1/mcp-servers", `{
		"identifier":"team-files","name":"Team Files","description":"Shared filesystem fixture.",
		"config":{"transport":"stdio","command":"python3","args":["scripts/mcp_stdio_fixture.py"]}
	}`, http.StatusCreated)
	if created.ID == "" || created.CurrentVersion != 1 || created.Status != mcpregistry.StatusActive || created.Identifier != "team_files" {
		t.Fatalf("unexpected registry server: %#v", created)
	}

	agent := postJSONWithStatus[managedagents.Agent](t, server, http.MethodPost, "/v1/agents", `{
		"name":"Registry Agent","system":"Use registered MCP.",
		"mcp":{"bindings":[{"server_id":"`+created.ID+`","version":0,"identifier":"files"}]}
	}`, http.StatusCreated)
	if !strings.Contains(string(agent.ConfigVersion.MCP), `"server_id":"`+created.ID+`"`) || !strings.Contains(string(agent.ConfigVersion.MCP), `"version":1`) || strings.Contains(string(agent.ConfigVersion.MCP), `"command"`) {
		t.Fatalf("expected reference-only pinned MCP config, got %s", agent.ConfigVersion.MCP)
	}

	updated := postJSONWithStatus[mcpregistry.Server](t, server, http.MethodPatch, "/v1/mcp-servers/"+created.ID, `{
		"config":{"transport":"stdio","command":"python3","args":["scripts/mcp_stdio_fixture.py","v2"]}
	}`, http.StatusOK)
	if updated.CurrentVersion != 2 || updated.UsageCount != 1 {
		t.Fatalf("unexpected updated server: %#v", updated)
	}
	versions := getJSON[struct {
		Versions []mcpregistry.Version `json:"versions"`
	}](t, server, "/v1/mcp-servers/"+created.ID+"/versions")
	if len(versions.Versions) != 2 || versions.Versions[0].Version != 2 || versions.Versions[1].Version != 1 {
		t.Fatalf("unexpected versions: %#v", versions.Versions)
	}
	restored := postJSONWithStatus[mcpregistry.RestoreResult](t, server, http.MethodPost, "/v1/mcp-servers/"+created.ID+"/versions/1/restore", `{}`, http.StatusOK)
	if restored.SourceVersion != 1 || restored.PreviousVersion != 2 || restored.NewVersion != 3 || restored.Server.CurrentVersion != 3 {
		t.Fatalf("unexpected restore result: %#v", restored)
	}
	if strings.Contains(string(restored.Server.Config), `"v2"`) || !strings.Contains(string(restored.Server.Config), `mcp_stdio_fixture.py`) {
		t.Fatalf("expected restored v1 config, got %s", restored.Server.Config)
	}
	versions = getJSON[struct {
		Versions []mcpregistry.Version `json:"versions"`
	}](t, server, "/v1/mcp-servers/"+created.ID+"/versions")
	if len(versions.Versions) != 3 || versions.Versions[0].Version != 3 || versions.Versions[0].Checksum != versions.Versions[2].Checksum {
		t.Fatalf("expected restored v3 to preserve v1 checksum: %#v", versions.Versions)
	}
	pinnedAgent := getJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID)
	if !strings.Contains(string(pinnedAgent.ConfigVersion.MCP), `"version":1`) || pinnedAgent.CurrentConfigVersion != agent.CurrentConfigVersion {
		t.Fatalf("restore changed pinned Agent config: %#v", pinnedAgent)
	}
	audit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/operator-audit?action=mcp_registry.version.restore")
	if len(audit.Records) != 1 || audit.Records[0].ResourceID != created.ID || audit.Records[0].Outcome != "succeeded" || audit.Records[0].WorkspaceID != created.WorkspaceID || audit.Records[0].SessionID != "" {
		t.Fatalf("unexpected restore audit: %#v", audit.Records)
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/mcp-servers/"+created.ID+"/versions/3/restore", `{}`, http.StatusBadRequest)
	conflict := postJSONWithStatus[map[string]string](t, server, http.MethodDelete, "/v1/mcp-servers/"+created.ID, `{}`, http.StatusConflict)
	if !strings.Contains(conflict["error"], "still bound") {
		t.Fatalf("unexpected archive conflict: %#v", conflict)
	}

	disabled := postJSONWithStatus[mcpregistry.Server](t, server, http.MethodPost, "/v1/mcp-servers/"+created.ID+"/disable", `{}`, http.StatusOK)
	if disabled.Status != mcpregistry.StatusDisabled {
		t.Fatalf("expected disabled registry server: %#v", disabled)
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/agents", `{
		"name":"Disabled Binding","mcp":{"bindings":[{"server_id":"`+created.ID+`"}]}
	}`, http.StatusConflict)
}

func TestMCPRegistryRuntimeStatusIsWorkspaceScopedAndFiltersUnknownServers(t *testing.T) {
	store := newTestStore()
	testRunner := mcpStatsTestRunner{
		Runner: runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		registryStates: map[string][]mcp.RegistryRuntimeState{
			"wksp_alpha": {
				{ServerID: "mcps_000001", Version: 1, State: "open", ConsecutiveFailures: 5, FailureThreshold: 5, LastFailureClass: "timeout", CooldownRemaining: 24},
				{ServerID: "mcps_unknown", Version: 9, State: "open"},
			},
			"wksp_beta": {{ServerID: "mcps_beta", Version: 1, State: "saturated"}},
		},
	}
	server := NewServerWithStoreAndRunner(store, testRunner, nil)
	created := postJSONWithStatus[mcpregistry.Server](t, server, http.MethodPost, "/v1/mcp-servers", `{
		"workspace_id":"wksp_alpha","identifier":"alpha","name":"Alpha MCP",
		"config":{"command":"fixture"}
	}`, http.StatusCreated)
	if created.ID != "mcps_000001" {
		t.Fatalf("unexpected fixture server: %#v", created)
	}

	response := getJSON[struct {
		CheckedAt time.Time                  `json:"checked_at"`
		States    []mcp.RegistryRuntimeState `json:"states"`
	}](t, server, "/v1/mcp-servers/runtime-status?workspace_id=wksp_alpha")
	if response.CheckedAt.IsZero() || len(response.States) != 1 {
		t.Fatalf("unexpected runtime status response: %#v", response)
	}
	state := response.States[0]
	if state.ServerID != created.ID || state.Version != 1 || state.State != "open" || state.LastFailureClass != "timeout" || state.CooldownRemaining != 24 {
		t.Fatalf("unexpected filtered runtime state: %#v", state)
	}

	beta := getJSON[struct {
		States []mcp.RegistryRuntimeState `json:"states"`
	}](t, server, "/v1/mcp-servers/runtime-status?workspace_id=wksp_beta")
	if len(beta.States) != 0 {
		t.Fatalf("runtime status exposed a non-Registry or cross-workspace state: %#v", beta.States)
	}
}
