package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

func newTestServer() http.Handler {
	store := newTestStore()
	return NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
}

type synchronizedResponseRecorder struct {
	*httptest.ResponseRecorder
	mu sync.RWMutex
}

func newSynchronizedResponseRecorder() *synchronizedResponseRecorder {
	return &synchronizedResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *synchronizedResponseRecorder) Write(payload []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(payload)
}

func (r *synchronizedResponseRecorder) WriteString(payload string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.WriteString(payload)
}

func (r *synchronizedResponseRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.WriteHeader(statusCode)
}

func (r *synchronizedResponseRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.Flush()
}

func (r *synchronizedResponseRecorder) BodyString() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Body.String()
}

type mcpStatsTestRunner struct {
	runner.Runner
	stats          mcp.StdioHostStats
	httpStats      mcp.StreamableHTTPHostStats
	guardStats     mcp.RuntimeGuardStats
	registryStates map[string][]mcp.RegistryRuntimeState
}

func (r mcpStatsTestRunner) MCPHostStats() mcp.StdioHostStats {
	return r.stats
}

func (r mcpStatsTestRunner) MCPHTTPHostStats() mcp.StreamableHTTPHostStats {
	return r.httpStats
}

func (r mcpStatsTestRunner) MCPRuntimeGuardStats() mcp.RuntimeGuardStats {
	return r.guardStats
}

func (r mcpStatsTestRunner) MCPRegistryRuntimeStates(workspaceID string) []mcp.RegistryRuntimeState {
	return append([]mcp.RegistryRuntimeState(nil), r.registryStates[workspaceID]...)
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newTestServer().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}

	if body["service"] != serviceName {
		t.Fatalf("expected service %q, got %q", serviceName, body["service"])
	}
}

func TestSkillRegistryLifecyclePreviewAndAgentBinding(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
	created := postJSON[skills.Skill](t, server, "/v1/skills", `{
		"identifier":"code-review","title":"Code Review","description":"Review changes safely"
	}`)
	version := postJSON[skills.Version](t, server, "/v1/skills/"+created.ID+"/versions", `{
		"content_format":"hybrid",
		"manifest":{"system_role":"Review with a bug-first mindset.","blocks":[{"type":"checklist","title":"Checks","items":["Regressions","Missing tests"]}]},
		"content_text":"Inspect behavior before style."
	}`)
	if version.Version != 1 || version.Checksum == "" {
		t.Fatalf("unexpected skill version: %#v", version)
	}
	invalidVersionRequest := httptest.NewRequest(http.MethodPost, "/v1/skills/"+created.ID+"/versions", strings.NewReader(`{
		"content_format":"hybrid",
		"manifest":{"inputs_schema":{"type":"object","properties":{"profile":{"$ref":"https://schemas.example/profile.json"}}}},
		"content_text":"This version must not be published."
	}`))
	invalidVersionRequest.Header.Set("Content-Type", "application/json")
	invalidVersionResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidVersionResponse, invalidVersionRequest)
	if invalidVersionResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid inputs_schema status 400, got %d: %s", invalidVersionResponse.Code, invalidVersionResponse.Body.String())
	}
	listed := getJSON[struct {
		Skills []skills.Skill `json:"skills"`
	}](t, server, "/v1/skills")
	if len(listed.Skills) != 1 || listed.Skills[0].ID != created.ID {
		t.Fatalf("unexpected skills list: %#v", listed.Skills)
	}
	listedVersions := getJSON[struct {
		Versions []skills.Version `json:"versions"`
	}](t, server, "/v1/skills/"+created.ID+"/versions")
	if len(listedVersions.Versions) != 1 {
		t.Fatalf("invalid inputs_schema published a version: %#v", listedVersions.Versions)
	}
	preview := postJSONWithStatus[skills.ResolveResult](t, server, http.MethodPost, "/v1/skills/resolve-preview", `{
		"skills":{"enabled":[{"skill":"code-review","version":1,"mode":"full"}]},"max_tokens":1000
	}`, http.StatusOK)
	if len(preview.Skills) != 1 || !strings.Contains(preview.Skills[0].Rendered, "bug-first") {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name":"Reviewer","system":"Help the user.",
		"skills":{"enabled":[{"skill":"code-review","version":1}]}
	}`)
	if !strings.Contains(string(agent.ConfigVersion.Skills), `"version":1`) {
		t.Fatalf("expected frozen skill binding, got %s", agent.ConfigVersion.Skills)
	}

	for _, body := range []string{
		`{"name":"Missing Version","skills":{"enabled":[{"skill":"code-review"}]}}`,
		`{"name":"Missing Skill Version","skills":{"enabled":[{"skill":"code-review","version":99}]}}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/agents", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid binding status 400, got %d: %s", response.Code, response.Body.String())
		}
	}

	conflict := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/skills/"+created.ID+"/archive", `{}`, http.StatusConflict)
	if !strings.Contains(conflict["error"], "disable it first") {
		t.Fatalf("expected active binding archive guidance, got %#v", conflict)
	}
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{"skills":{"enabled":[]}}`)
	archived := postJSONWithStatus[skills.Skill](t, server, http.MethodPost, "/v1/skills/"+created.ID+"/archive", `{}`, http.StatusOK)
	if archived.Status != skills.StatusArchived || archived.ArchivedAt == nil {
		t.Fatalf("expected archived skill, got %#v", archived)
	}
}

func TestCreateAgentPersistsMCPConfig(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name":"MCP Agent",
		"system":"Use MCP when helpful.",
		"mcp":{
			"mcpServers":{
				"filesystem":{
					"command":"npx",
					"args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]
				}
			}
		}
	}`)
	if !strings.Contains(string(agent.ConfigVersion.MCP), `"identifier":"filesystem"`) {
		t.Fatalf("expected normalized mcp config, got %s", agent.ConfigVersion.MCP)
	}
}

func TestAgentToolingHealthReportsMCPAndSkillState(t *testing.T) {
	store := newTestStore()
	testRunner := mcpStatsTestRunner{
		Runner: runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		stats: mcp.StdioHostStats{
			Sessions: 2, MaxSessions: 8, StartsTotal: 3, ToolsListChangedTotal: 4, ProgressNotificationsTotal: 5, LogMessagesTotal: 2,
		},
		httpStats: mcp.StreamableHTTPHostStats{
			Sessions: 1, MaxSessions: 12, StartsTotal: 2, ProgressNotificationsTotal: 3, LogMessagesTotal: 1,
			EgressPolicyEnabled: true, EgressAllowedHostCount: 2, EgressAllowedCIDRCount: 1, EgressBlockedTotal: 4,
		},
		guardStats: mcp.RuntimeGuardStats{TrackedServers: 2, InFlight: 1, OpenCircuits: 1, CallsTotal: 9},
	}
	server := NewServerWithStoreAndRunner(store, testRunner, nil)
	skill := postJSON[skills.Skill](t, server, "/v1/skills", `{
		"identifier":"health-check","title":"Health Check","description":"Validate tooling health"
	}`)
	postJSON[skills.Version](t, server, "/v1/skills/"+skill.ID+"/versions", `{
		"content_format":"markdown","manifest":{},"content_text":"Check the configured tools."
	}`)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name":"Health Agent","system":"Check tools.",
		"skills":{"enabled":[{"skill":"health-check","version":1}]},
		"mcp":{"servers":[{"identifier":"missing-server","command":"tma-command-that-does-not-exist"}]}
	}`)

	report := postJSONWithStatus[toolingHealthResponse](t, server, http.MethodPost, "/v1/agents/"+agent.ID+"/tooling-health", `{}`, http.StatusOK)
	if report.AgentID != agent.ID || len(report.MCP) != 1 || len(report.Skills) != 1 {
		t.Fatalf("unexpected tooling health report: %#v", report)
	}
	if report.MCPHost == nil || report.MCPHost.Sessions != 2 || report.MCPHost.MaxSessions != 8 || report.MCPHost.StartsTotal != 3 || report.MCPHost.ToolsListChangedTotal != 4 || report.MCPHost.ProgressNotificationsTotal != 5 || report.MCPHost.LogMessagesTotal != 2 {
		t.Fatalf("expected MCP host snapshot, got %#v", report.MCPHost)
	}
	if report.MCPHTTPHost == nil || report.MCPHTTPHost.Sessions != 1 || report.MCPHTTPHost.MaxSessions != 12 || report.MCPHTTPHost.StartsTotal != 2 || report.MCPHTTPHost.ProgressNotificationsTotal != 3 || report.MCPHTTPHost.LogMessagesTotal != 1 || !report.MCPHTTPHost.EgressPolicyEnabled || report.MCPHTTPHost.EgressAllowedHostCount != 2 || report.MCPHTTPHost.EgressAllowedCIDRCount != 1 || report.MCPHTTPHost.EgressBlockedTotal != 4 {
		t.Fatalf("expected MCP HTTP host snapshot, got %#v", report.MCPHTTPHost)
	}
	if report.MCPRuntimeGuard == nil || report.MCPRuntimeGuard.TrackedServers != 2 || report.MCPRuntimeGuard.InFlight != 1 || report.MCPRuntimeGuard.OpenCircuits != 1 || report.MCPRuntimeGuard.CallsTotal != 9 {
		t.Fatalf("expected MCP runtime guard snapshot, got %#v", report.MCPRuntimeGuard)
	}
	if report.MCP[0].Status != "offline" || report.MCP[0].Identifier != "missing_server" {
		t.Fatalf("expected offline MCP server, got %#v", report.MCP[0])
	}
	if report.Skills[0].Status != "online" || report.Skills[0].Version != 1 || report.Skills[0].TokenEstimate <= 0 {
		t.Fatalf("expected healthy skill, got %#v", report.Skills[0])
	}
}

func TestMCPToolingHealthReportsInitializeCapabilities(t *testing.T) {
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode MCP request: %v", err)
		}
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo":   map[string]any{"name": "Capability MCP"},
					"capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}, "completions": map[string]any{}},
				},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"tools": []map[string]any{{"name": "ping", "inputSchema": map[string]any{"type": "object"}}},
				},
			}); err != nil {
				t.Fatalf("write tools/list response: %v", err)
			}
		case "resources/list":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resources": []map[string]any{
						{"uri": "file:///tmp/guide.md", "name": "guide"},
						{"uri": "file:///tmp/status.md", "name": "status"},
					},
				},
			}); err != nil {
				t.Fatalf("write resources/list response: %v", err)
			}
		case "resources/templates/list":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resourceTemplates": []map[string]any{{"uriTemplate": "file:///{path}", "name": "file"}},
				},
			}); err != nil {
				t.Fatalf("write resources/templates/list response: %v", err)
			}
		case "prompts/list":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"prompts": []map[string]any{{"name": "summarize"}},
				},
			}); err != nil {
				t.Fatalf("write prompts/list response: %v", err)
			}
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer mcpServer.Close()

	raw := json.RawMessage(`{"servers":[{"identifier":"remote","transport":"streamable_http","url":"` + mcpServer.URL + `"}]}`)
	report := checkMCPHealth(t.Context(), raw, "remote")
	if len(report) != 1 || report[0].Status != "online" {
		t.Fatalf("expected online MCP health report, got %#v", report)
	}
	if !slices.Equal(report[0].Capabilities, []string{"completions", "prompts", "resources", "tools"}) {
		t.Fatalf("unexpected MCP health capabilities: %#v", report[0])
	}
	if report[0].ResourceCount != 2 || report[0].ResourceTemplateCount != 1 || report[0].PromptCount != 1 {
		t.Fatalf("unexpected MCP context catalog counts: %#v", report[0])
	}
}

func TestRootRedirectsToUserApp(t *testing.T) {
	for _, path := range []string{"/", "/app/"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		newTestServer().ServeHTTP(response, request)
		if response.Code != http.StatusTemporaryRedirect {
			t.Fatalf("expected %s to redirect, got %d", path, response.Code)
		}
		if location := response.Header().Get("Location"); location != "/app" {
			t.Fatalf("expected %s to redirect to /app, got %q", path, location)
		}
	}
}

func TestListTaskGroupTemplates(t *testing.T) {
	server := newTestServer()
	response := getJSON[tools.AgentTaskGroupTemplateListResponse](t, server, "/v1/agent/task-group-templates")
	if len(response.Templates) < 3 {
		t.Fatalf("expected builtin task group templates, got %#v", response)
	}
	if response.Templates[0].ID == "" || response.Templates[0].Strategy == "" || response.Templates[0].ResultReducer == "" {
		t.Fatalf("expected populated template metadata, got %#v", response.Templates[0])
	}
}

func TestListWorkbenchTaskTemplates(t *testing.T) {
	server := newTestServer()
	response := getJSON[struct {
		Templates []workbenchTaskTemplate `json:"templates"`
	}](t, server, "/v1/task-templates")
	if len(response.Templates) != 4 {
		t.Fatalf("expected four workbench templates, got %d", len(response.Templates))
	}
	news := response.Templates[0]
	if news.ID != "ai_news_digest" || len(news.WorkflowSteps) != 4 || len(news.Tools) == 0 {
		t.Fatalf("unexpected AI news template: %#v", news)
	}
}

func TestListSessionTaskGroups(t *testing.T) {
	store := newTestStore()
	turnRunner := runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil)
	server := NewServerWithStoreAndRunner(store, turnRunner, nil)
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	service := newAgentToolService(store, turnRunner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_http",
		TemplateID:      "module_risk_audit",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "audit auth"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "audit billing"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}

	response := getJSON[sessionTaskGroupsResponse](t, server, "/v1/sessions/"+parentSession.ID+"/task-groups")
	if len(response.TaskGroups) != 1 {
		t.Fatalf("expected one task group, got %#v", response)
	}
	entry := response.TaskGroups[0]
	if entry.TemplateID != "module_risk_audit" || entry.TemplateTitle == "" {
		t.Fatalf("expected template metadata, got %#v", entry)
	}
	if entry.State.Group.ID != created.Group.ID || len(entry.State.Items) != 2 {
		t.Fatalf("expected full task group state, got %#v", entry.State)
	}

	detail := getJSON[inspectorTaskGroupState](t, server, "/v1/sessions/"+parentSession.ID+"/task-groups/"+created.Group.ID)
	if detail.State.Group.ID != created.Group.ID || detail.TemplateID != "module_risk_audit" {
		t.Fatalf("expected task group detail, got %#v", detail)
	}
}

func TestGetSessionTaskGroupTreeIncludesDescendantGroups(t *testing.T) {
	store := newTestStore()
	turnRunner := runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil)
	server := NewServerWithStoreAndRunner(store, turnRunner, nil)
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Tree Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Tree Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "tree-parent-session")
	service := newAgentToolService(store, turnRunner, nil, defaultSubagentPolicy())

	rootGroup, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_tree_root",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "root work"},
		},
	})
	if err != nil {
		t.Fatalf("create root task group: %v", err)
	}
	childSession := rootGroup.Items[0].Session
	if childSession == nil {
		t.Fatalf("expected child session in root group: %#v", rootGroup)
	}
	childGroup, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: childSession.ID,
		ParentTurnID:    "turn_tree_child",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "nested work"},
		},
	})
	if err != nil {
		t.Fatalf("create child task group: %v", err)
	}

	response := getJSON[sessionTaskGroupTreeResponse](t, server, "/v1/sessions/"+parentSession.ID+"/task-group-tree")
	if response.Root.Session.ID != parentSession.ID || len(response.Root.TaskGroups) != 1 {
		t.Fatalf("expected root session and group, got %#v", response.Root)
	}
	if len(response.Root.Children) != 1 {
		t.Fatalf("expected one child session node, got %#v", response.Root.Children)
	}
	childNode := response.Root.Children[0]
	if childNode.Session.ID != childSession.ID || len(childNode.TaskGroups) != 1 {
		t.Fatalf("expected descendant task group, got %#v", childNode)
	}
	if childNode.TaskGroups[0].State.Group.ID != childGroup.Group.ID {
		t.Fatalf("expected child group %q, got %#v", childGroup.Group.ID, childNode.TaskGroups[0])
	}
	if response.Summary.Sessions != 3 || response.Summary.Groups != 2 || response.Summary.Items != 2 {
		t.Fatalf("unexpected tree summary: %#v", response.Summary)
	}
}

func TestSessionTaskGroupControlEndpoints(t *testing.T) {
	store := newTestStore()
	turnRunner := runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil)
	server := NewServerWithStoreAndRunner(store, turnRunner, nil)
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Control Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Control Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "control-parent-session")
	service := newAgentToolService(store, turnRunner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_controls",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "control work"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	basePath := "/v1/sessions/" + parentSession.ID + "/task-groups/" + created.Group.ID
	canceled := postJSONWithStatus[tools.AgentTaskGroupCancelResponse](t, server, http.MethodPost, basePath+"/cancel", `{"reason":"operator canceled"}`, http.StatusOK)
	if canceled.Status != "canceled" {
		t.Fatalf("expected canceled group, got %#v", canceled)
	}

	retriedItem := postJSONWithStatus[tools.AgentTaskGroupRetryResponse](t, server, http.MethodPost, basePath+"/items/0/retry", `{}`, http.StatusOK)
	if retriedItem.Group.ID != created.Group.ID || len(retriedItem.Items) != 1 {
		t.Fatalf("expected retried item state, got %#v", retriedItem)
	}

	postJSONWithStatus[tools.AgentTaskGroupCancelResponse](t, server, http.MethodPost, basePath+"/cancel", `{"reason":"cancel before group retry"}`, http.StatusOK)
	retriedGroup := postJSONWithStatus[tools.AgentTaskGroupRetryResponse](t, server, http.MethodPost, basePath+"/retry", `{}`, http.StatusOK)
	if retriedGroup.Group.ID != created.Group.ID || retriedGroup.Status == "canceled" {
		t.Fatalf("expected reactivated group, got %#v", retriedGroup)
	}

	reaped := postJSONWithStatus[struct {
		Count int `json:"count"`
	}](t, server, http.MethodPost, "/v1/subagents/reap-orphans", `{"limit":10}`, http.StatusOK)
	if reaped.Count != 0 {
		t.Fatalf("expected no test orphans, got %#v", reaped)
	}
	audit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/sessions/"+parentSession.ID+"/operator-audit")
	if len(audit.Records) != 4 {
		t.Fatalf("expected four session control audit records, got %#v", audit.Records)
	}
	if audit.Records[0].PrincipalID != "control:open" || audit.Records[0].Outcome != "succeeded" {
		t.Fatalf("unexpected open-control audit principal: %#v", audit.Records[0])
	}
}

func TestControlAuthProtectsSubagentControlEndpoints(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		nil,
		nil,
		"",
		"control-secret",
	)

	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/subagents/reap-orphans", bytes.NewBufferString(`{}`))
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected orphan reap to require control auth, got %d: %s", unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}

	authorized := httptest.NewRequest(http.MethodPost, "/v1/subagents/reap-orphans", bytes.NewBufferString(`{}`))
	authorized.Header.Set("Content-Type", "application/json")
	authorized.Header.Set("Authorization", "Bearer control-secret")
	authorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized orphan reap, got %d: %s", authorizedResponse.Code, authorizedResponse.Body.String())
	}

	groupCancel := httptest.NewRequest(http.MethodPost, "/v1/sessions/sesn_missing/task-groups/grp_missing/cancel", bytes.NewBufferString(`{}`))
	groupCancel.Header.Set("Content-Type", "application/json")
	groupCancelResponse := httptest.NewRecorder()
	server.ServeHTTP(groupCancelResponse, groupCancel)
	if groupCancelResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected group cancel to require control auth, got %d: %s", groupCancelResponse.Code, groupCancelResponse.Body.String())
	}

	failedCancel := httptest.NewRequest(http.MethodPost, "/v1/sessions/sesn_missing/task-groups/grp_missing/cancel", bytes.NewBufferString(`{}`))
	failedCancel.Header.Set("Content-Type", "application/json")
	failedCancel.Header.Set("Authorization", "Bearer control-secret")
	failedCancel.Header.Set("X-TMA-Operator", "alice@example.com")
	failedCancelResponse := httptest.NewRecorder()
	server.ServeHTTP(failedCancelResponse, failedCancel)
	if failedCancelResponse.Code != http.StatusNotFound {
		t.Fatalf("expected authorized missing group cancel to return not found, got %d: %s", failedCancelResponse.Code, failedCancelResponse.Body.String())
	}

	auditRequest := httptest.NewRequest(http.MethodGet, "/v1/operator-audit?limit=10", nil)
	auditRequest.Header.Set("Authorization", "Bearer control-secret")
	auditResponse := httptest.NewRecorder()
	server.ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized audit list, got %d: %s", auditResponse.Code, auditResponse.Body.String())
	}
	var audit struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}
	if err := json.NewDecoder(auditResponse.Body).Decode(&audit); err != nil {
		t.Fatalf("decode operator audit: %v", err)
	}
	if len(audit.Records) != 2 {
		t.Fatalf("expected reap and failed cancel audit records, got %#v", audit.Records)
	}
	failedRecord := audit.Records[0]
	if failedRecord.Action != "agent.task_group.cancel" || failedRecord.Outcome != "failed" || failedRecord.OperatorLabel != "alice@example.com" {
		t.Fatalf("unexpected failed control audit: %#v", failedRecord)
	}
	if !strings.HasPrefix(failedRecord.PrincipalID, "control:") || strings.Contains(failedRecord.PrincipalID, "control-secret") {
		t.Fatalf("expected non-secret control principal fingerprint, got %q", failedRecord.PrincipalID)
	}
}

func TestBuiltinGeneralAgentIsDefaultForNewSessions(t *testing.T) {
	server := newTestServer()
	agent := getJSON[managedagents.Agent](t, server, "/v1/agents/default")
	if agent.ID != managedagents.BuiltinGeneralAgentID || agent.Name != managedagents.BuiltinGeneralAgentName {
		t.Fatalf("unexpected builtin agent: %+v", agent)
	}
	if agent.ConfigVersion.LLMProvider != "fake" || agent.ConfigVersion.LLMModel != "fake-demo" {
		t.Fatalf("unexpected builtin agent model: %+v", agent.ConfigVersion)
	}
	if agent.ConfigVersion.System != managedagents.BuiltinGeneralAgentSystem {
		t.Fatalf("unexpected builtin agent system prompt: %q", agent.ConfigVersion.System)
	}

	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"environment_id": "`+environment.ID+`",
		"title": "uses builtin agent"
	}`)
	if session.AgentID != managedagents.BuiltinGeneralAgentID {
		t.Fatalf("expected builtin agent %q, got %q", managedagents.BuiltinGeneralAgentID, session.AgentID)
	}
}

func TestListSessionsReturnsMostRecentFirst(t *testing.T) {
	server := newTestServer()
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "history-environment",
		"config": {"type": "cloud"}
	}`)
	first := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"environment_id": "`+environment.ID+`",
		"title": "first chat"
	}`)
	second := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"environment_id": "`+environment.ID+`",
		"title": "second chat"
	}`)

	response := getJSON[struct {
		Sessions []managedagents.Session `json:"sessions"`
	}](t, server, "/v1/sessions?limit=10")
	if len(response.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(response.Sessions))
	}
	if response.Sessions[0].ID != second.ID || response.Sessions[1].ID != first.ID {
		t.Fatalf("unexpected session order: %+v", response.Sessions)
	}
}

func TestLLMProviderManagement(t *testing.T) {
	server := newTestServer()

	created := postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id": "volcengine-agent-plan",
		"provider_type": "openai",
		"base_url": "https://ark.cn-beijing.volces.com/api/plan/v3",
		"api_key_env": "TMA_LLM_API_KEY_VOLCENGINE"
	}`)
	if created.ID != "volcengine-agent-plan" || !created.Enabled {
		t.Fatalf("unexpected created provider: %+v", created)
	}
	if created.APIKeyEnv != "TMA_LLM_API_KEY_VOLCENGINE" {
		t.Fatalf("expected api key env reference only, got %q", created.APIKeyEnv)
	}

	listed := getJSON[llmProvidersResponse](t, server, "/v1/llm-providers")
	if len(listed.Providers) != 2 || listed.Providers[1].ID != created.ID {
		t.Fatalf("unexpected provider list: %+v", listed.Providers)
	}

	missingPrecondition := httptest.NewRecorder()
	server.ServeHTTP(missingPrecondition, httptest.NewRequest(http.MethodPatch, "/v1/llm-providers/"+created.ID, strings.NewReader(`{"base_url":"https://ignored.example"}`)))
	if missingPrecondition.Code != http.StatusBadRequest {
		t.Fatalf("expected provider PATCH without If-Match to return 400, got %d: %s", missingPrecondition.Code, missingPrecondition.Body.String())
	}

	updated := patchLLMProvider(t, server, created.ID, created.Revision, `{
		"base_url": "https://ark.cn-beijing.volces.com/api/v3"
	}`)
	if updated.BaseURL != "https://ark.cn-beijing.volces.com/api/v3" {
		t.Fatalf("expected updated base_url, got %q", updated.BaseURL)
	}
	if updated.ProviderType != "openai" || updated.APIKeyEnv != "TMA_LLM_API_KEY_VOLCENGINE" {
		t.Fatalf("expected update to preserve omitted fields, got %+v", updated)
	}
	if updated.Revision != created.Revision+1 {
		t.Fatalf("expected provider revision to advance, got create=%d update=%d", created.Revision, updated.Revision)
	}
	staleUpdate := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(http.MethodPatch, "/v1/llm-providers/"+created.ID, strings.NewReader(`{"base_url":"https://stale.example"}`))
	staleRequest.Header.Set("Content-Type", "application/json")
	staleRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(created.Revision, 10)))
	server.ServeHTTP(staleUpdate, staleRequest)
	if staleUpdate.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected stale provider revision to return 412, got %d: %s", staleUpdate.Code, staleUpdate.Body.String())
	}

	duplicateCreate := httptest.NewRecorder()
	server.ServeHTTP(duplicateCreate, httptest.NewRequest(http.MethodPost, "/v1/llm-providers", strings.NewReader(`{"id":"`+created.ID+`","provider_type":"fake"}`)))
	if duplicateCreate.Code != http.StatusConflict {
		t.Fatalf("expected duplicate provider creation to return 409, got %d: %s", duplicateCreate.Code, duplicateCreate.Body.String())
	}

	disabled := postLLMProviderAction(t, server, created.ID, "disable", updated.Revision)
	if disabled.Enabled {
		t.Fatalf("expected provider disabled, got %+v", disabled)
	}

	enabled := postLLMProviderAction(t, server, created.ID, "enable", disabled.Revision)
	if !enabled.Enabled {
		t.Fatalf("expected provider enabled, got %+v", enabled)
	}

	deleteResponse := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/llm-providers/"+created.ID, nil)
	deleteRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(enabled.Revision, 10)))
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected provider deletion to return 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	listed = getJSON[llmProvidersResponse](t, server, "/v1/llm-providers")
	if len(listed.Providers) != 1 || listed.Providers[0].ID != "fake" {
		t.Fatalf("unexpected provider list after deletion: %+v", listed.Providers)
	}
}

func TestLLMControlPlaneMutationAudit(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
	providerID := "audit-provider"
	modelName := "audit-model"
	secretBaseURL := "https://llm.example.test/v1?token=do-not-audit"
	secretEnvName := "TMA_LLM_PRIVATE_KEY_DO_NOT_AUDIT"

	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id":"`+providerID+`","provider_type":"openai",
		"base_url":"`+secretBaseURL+`","api_key_env":"`+secretEnvName+`"
	}`)
	createdProvider, err := store.GetLLMProvider(providerID)
	if err != nil {
		t.Fatalf("get provider before audited update: %v", err)
	}
	updatedProvider := patchLLMProvider(t, server, providerID, createdProvider.Revision, `{
		"provider_type":"azure_openai"
	}`)
	staleAuditUpdate := httptest.NewRecorder()
	staleAuditRequest := httptest.NewRequest(http.MethodPatch, "/v1/llm-providers/"+providerID, strings.NewReader(`{"provider_type":"fake"}`))
	staleAuditRequest.Header.Set("Content-Type", "application/json")
	staleAuditRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(createdProvider.Revision, 10)))
	server.ServeHTTP(staleAuditUpdate, staleAuditRequest)
	if staleAuditUpdate.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected stale audited provider update to return 412, got %d: %s", staleAuditUpdate.Code, staleAuditUpdate.Body.String())
	}
	disabledProvider := postLLMProviderAction(t, server, providerID, "disable", updatedProvider.Revision)
	enabledProvider := postLLMProviderAction(t, server, providerID, "enable", disabledProvider.Revision)
	createdModel := createLLMModel(t, server, `{
		"provider_id":"`+providerID+`","model":"`+modelName+`","context_window_tokens":1000
	}`)
	updatedModel := updateLLMModel(t, server, createdModel.Revision, `{
		"provider_id":"`+providerID+`","model":"`+modelName+`","context_window_tokens":2000,
		"capability_type":"text_image","is_default_vision":true
	}`)
	staleModelUpdate := httptest.NewRecorder()
	staleModelUpdateRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"`+providerID+`","model":"`+modelName+`","context_window_tokens":3000
	}`))
	staleModelUpdateRequest.Header.Set("Content-Type", "application/json")
	staleModelUpdateRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(createdModel.Revision, 10)))
	server.ServeHTTP(staleModelUpdate, staleModelUpdateRequest)
	if staleModelUpdate.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected stale audited model update to return 412, got %d: %s", staleModelUpdate.Code, staleModelUpdate.Body.String())
	}

	missingDelete := httptest.NewRecorder()
	server.ServeHTTP(missingDelete, httptest.NewRequest(http.MethodDelete, "/v1/llm-models/"+providerID+"/missing-model", nil))
	if missingDelete.Code != http.StatusNotFound {
		t.Fatalf("expected missing model deletion to return 404, got %d: %s", missingDelete.Code, missingDelete.Body.String())
	}
	modelDelete := httptest.NewRecorder()
	modelDeleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/llm-models/"+providerID+"/"+modelName, nil)
	modelDeleteRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(updatedModel.Revision, 10)))
	server.ServeHTTP(modelDelete, modelDeleteRequest)
	if modelDelete.Code != http.StatusNoContent {
		t.Fatalf("expected model deletion to return 204, got %d: %s", modelDelete.Code, modelDelete.Body.String())
	}
	providerDelete := httptest.NewRecorder()
	providerDeleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/llm-providers/"+providerID, nil)
	providerDeleteRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(enabledProvider.Revision, 10)))
	server.ServeHTTP(providerDelete, providerDeleteRequest)
	if providerDelete.Code != http.StatusNoContent {
		t.Fatalf("expected provider deletion to return 204, got %d: %s", providerDelete.Code, providerDelete.Body.String())
	}

	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 20})
	if err != nil {
		t.Fatalf("list llm control audits: %v", err)
	}
	expectedCounts := map[string]int{
		"llm.provider.create":  1,
		"llm.provider.update":  2,
		"llm.provider.disable": 1,
		"llm.provider.enable":  1,
		"llm.provider.delete":  1,
		"llm.model.create":     1,
		"llm.model.update":     2,
		"llm.model.delete":     2,
	}
	actualCounts := make(map[string]int)
	for _, audit := range audits {
		actualCounts[audit.Action]++
		if audit.PrincipalID != "control:open" || audit.Role != RoleAdmin {
			t.Fatalf("unexpected llm audit principal: %+v", audit)
		}
	}
	if !maps.Equal(actualCounts, expectedCounts) {
		t.Fatalf("unexpected llm control audit actions: got=%v want=%v", actualCounts, expectedCounts)
	}

	findAudit := func(action string, outcome string) managedagents.OperatorAuditRecord {
		t.Helper()
		for _, audit := range audits {
			if audit.Action == action && audit.Outcome == outcome {
				return audit
			}
		}
		t.Fatalf("missing %s audit with outcome %s", action, outcome)
		return managedagents.OperatorAuditRecord{}
	}
	var providerUpdate struct {
		Before *llmProviderAuditState `json:"before"`
		After  *llmProviderAuditState `json:"after"`
	}
	if err := json.Unmarshal(findAudit("llm.provider.update", "succeeded").Details, &providerUpdate); err != nil {
		t.Fatalf("decode provider update audit: %v", err)
	}
	if providerUpdate.Before == nil || providerUpdate.After == nil || providerUpdate.Before.ProviderType != "openai" || providerUpdate.After.ProviderType != "azure_openai" {
		t.Fatalf("unexpected provider update audit details: %+v", providerUpdate)
	}
	if !providerUpdate.Before.BaseURLConfigured || !providerUpdate.Before.CredentialConfigured {
		t.Fatalf("expected provider audit to retain safe configuration presence flags: %+v", providerUpdate.Before)
	}

	var modelUpdate struct {
		Before *llmModelAuditState `json:"before"`
		After  *llmModelAuditState `json:"after"`
	}
	if err := json.Unmarshal(findAudit("llm.model.update", "succeeded").Details, &modelUpdate); err != nil {
		t.Fatalf("decode model update audit: %v", err)
	}
	if modelUpdate.Before == nil || modelUpdate.After == nil || modelUpdate.Before.ContextWindowTokens != 1000 || modelUpdate.After.ContextWindowTokens != 2000 || !modelUpdate.After.IsDefaultVision {
		t.Fatalf("unexpected model update audit details: %+v", modelUpdate)
	}
	failedDelete := findAudit("llm.model.delete", "failed")
	if failedDelete.ResourceID != providerID+"/missing-model" || failedDelete.ErrorMessage == "" {
		t.Fatalf("unexpected failed model deletion audit: %+v", failedDelete)
	}
	failedProviderUpdate := findAudit("llm.provider.update", "failed")
	if failedProviderUpdate.ResourceID != providerID || !strings.Contains(failedProviderUpdate.ErrorMessage, "revision changed") {
		t.Fatalf("unexpected stale provider update audit: %+v", failedProviderUpdate)
	}
	failedModelUpdate := findAudit("llm.model.update", "failed")
	if failedModelUpdate.ResourceID != providerID+"/"+modelName || !strings.Contains(failedModelUpdate.ErrorMessage, "revision changed") {
		t.Fatalf("unexpected stale model update audit: %+v", failedModelUpdate)
	}

	encodedAudits, err := json.Marshal(audits)
	if err != nil {
		t.Fatalf("encode llm audits: %v", err)
	}
	if strings.Contains(string(encodedAudits), secretBaseURL) || strings.Contains(string(encodedAudits), secretEnvName) {
		t.Fatalf("llm control audit leaked provider connection configuration: %s", encodedAudits)
	}
}

func TestWorkerRegistryLifecycle(t *testing.T) {
	server := newTestServer()

	created := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.read_file"],
			"runtimes": ["local_system"],
			"capabilities": ["filesystem.read"]
		},
		"metadata": {"os":"darwin"},
		"lease_seconds": 30
	}`)
	if created.ID == "" || created.Status != managedagents.WorkerStatusOnline || created.WorkerType != managedagents.WorkerTypeLocal {
		t.Fatalf("unexpected created worker: %+v", created)
	}
	if created.LastSeenAt == nil || created.LeaseExpiresAt == nil {
		t.Fatalf("expected heartbeat timestamps on created worker: %+v", created)
	}

	listed := getJSON[struct {
		Workers []managedagents.Worker `json:"workers"`
	}](t, server, "/v1/workers?workspace_id=wksp_default&status=online")
	if len(listed.Workers) != 1 || listed.Workers[0].ID != created.ID {
		t.Fatalf("unexpected workers list: %+v", listed.Workers)
	}

	heartbeat := postJSONWithStatus[managedagents.Worker](t, server, http.MethodPost, "/v1/workers/"+created.ID+"/heartbeat", `{
		"status": "draining",
		"lease_seconds": 45
	}`, http.StatusOK)
	if heartbeat.Status != managedagents.WorkerStatusDraining {
		t.Fatalf("expected draining worker, got %+v", heartbeat)
	}

	archived := postJSONWithStatus[managedagents.Worker](t, server, http.MethodPost, "/v1/workers/"+created.ID+"/archive", `{}`, http.StatusOK)
	if archived.Status != managedagents.WorkerStatusArchived || archived.ArchivedAt == nil {
		t.Fatalf("expected archived worker, got %+v", archived)
	}
}

func TestReapExpiredWorkers(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "expired-worker",
		"worker_type": "local",
		"lease_seconds": 30
	}`)
	store.mu.Lock()
	expiredAt := time.Now().UTC().Add(-time.Minute)
	workerRecord := store.workers[worker.ID]
	workerRecord.LeaseExpiresAt = &expiredAt
	store.workers[worker.ID] = workerRecord
	store.mu.Unlock()

	response := postJSONWithStatus[struct {
		Count   int                    `json:"count"`
		Expired []managedagents.Worker `json:"expired"`
	}](t, server, http.MethodPost, "/v1/workers/reap-expired", `{"limit":10}`, http.StatusOK)
	if response.Count != 1 || len(response.Expired) != 1 {
		t.Fatalf("expected one expired worker, got %+v", response)
	}
	expired := response.Expired[0]
	if expired.ID != worker.ID || expired.Status != managedagents.WorkerStatusOffline {
		t.Fatalf("unexpected expired worker: %+v", expired)
	}

	fetched := getJSON[managedagents.Worker](t, server, "/v1/workers/"+worker.ID)
	if fetched.Status != managedagents.WorkerStatusOffline {
		t.Fatalf("expected fetched worker offline, got %+v", fetched)
	}
}

func TestWorkerDiagnoseAPI(t *testing.T) {
	server := newTestServer()

	postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "reader-only",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["filesystem.read"]
		},
		"lease_seconds": 30
	}`)
	postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "executor",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["exec"]
		},
		"lease_seconds": 30
	}`)

	response := postJSONWithStatus[workerDiagnoseResponse](t, server, http.MethodPost, "/v1/workers/diagnose", `{
		"workspace_id": "wksp_default",
		"namespace": "default",
		"api": "run_command",
		"runtime": "local_system",
		"capabilities": ["exec"],
		"input": {}
	}`, http.StatusOK)
	if response.Invocation.ProtocolVersion != tools.WorkProtocolVersion || response.Invocation.Runtime != tools.ToolRuntimeLocalSystem {
		t.Fatalf("unexpected invocation: %+v", response.Invocation)
	}
	if response.Matches != 1 || len(response.Diagnostics) != 2 {
		t.Fatalf("unexpected diagnosis summary: %+v", response)
	}
	var sawMissing bool
	var sawMatch bool
	for _, diagnosis := range response.Diagnostics {
		switch diagnosis.Name {
		case "reader-only":
			sawMissing = true
			if diagnosis.Match || !slices.Contains(diagnosis.Reasons, "missing capability exec") {
				t.Fatalf("expected reader-only mismatch, got %+v", diagnosis)
			}
		case "executor":
			sawMatch = true
			if !diagnosis.Match || len(diagnosis.Reasons) != 0 {
				t.Fatalf("expected executor match, got %+v", diagnosis)
			}
		}
	}
	if !sawMissing || !sawMatch {
		t.Fatalf("missing expected diagnostics: %+v", response.Diagnostics)
	}
}

func TestWorkerAuthProtectsWorkerConsumerEndpoints(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndWorkerAuth(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		nil,
		nil,
		"worker-secret",
	)

	unauthorized := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/workers", `{
		"name": "viito-mac"
	}`, http.StatusUnauthorized)
	if unauthorized["error"] == "" {
		t.Fatalf("expected unauthorized worker error, got %#v", unauthorized)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/workers", bytes.NewBufferString(`{
		"name": "viito-mac",
		"worker_type": "local"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer worker-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected authorized worker register status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
	}
	var worker managedagents.Worker
	if err := json.NewDecoder(response.Body).Decode(&worker); err != nil {
		t.Fatalf("decode authorized worker: %v", err)
	}

	pollRequest := httptest.NewRequest(http.MethodGet, "/v1/workers/"+worker.ID+"/work/poll", nil)
	pollResponse := httptest.NewRecorder()
	server.ServeHTTP(pollResponse, pollRequest)
	if pollResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated poll status %d, got %d: %s", http.StatusUnauthorized, pollResponse.Code, pollResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/workers", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected worker list to remain open without control token configured, got %d: %s", listResponse.Code, listResponse.Body.String())
	}
	var listed struct {
		Workers []managedagents.Worker `json:"workers"`
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(listed.Workers) != 1 || listed.Workers[0].ID != worker.ID {
		t.Fatalf("expected worker list to remain visible without control token configured, got %+v", listed.Workers)
	}
}

func TestWorkerRegistrySensitiveEndpointsRequireWorkerOrControlAuth(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		nil,
		nil,
		"worker-secret",
		"control-secret",
	)
	diagnoseBody := `{
		"workspace_id": "wksp_default",
		"namespace": "default",
		"api": "run_command",
		"runtime": "local_system",
		"capabilities": ["exec"],
		"input": {}
	}`

	registerWorker := func(name string) managedagents.Worker {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/workers", bytes.NewBufferString(`{
			"name": "`+name+`",
			"worker_type": "local"
		}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer worker-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("expected worker register status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
		}
		var worker managedagents.Worker
		if err := json.NewDecoder(response.Body).Decode(&worker); err != nil {
			t.Fatalf("decode worker: %v", err)
		}
		return worker
	}

	workerArchivedByWorker := registerWorker("archive-by-worker")
	unauthorizedDiagnose := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/workers/diagnose", diagnoseBody, http.StatusUnauthorized)
	if unauthorizedDiagnose["error"] != "worker or control authorization required" {
		t.Fatalf("expected diagnose auth error, got %#v", unauthorizedDiagnose)
	}
	workerDiagnoseRequest := httptest.NewRequest(http.MethodPost, "/v1/workers/diagnose", bytes.NewBufferString(diagnoseBody))
	workerDiagnoseRequest.Header.Set("Content-Type", "application/json")
	workerDiagnoseRequest.Header.Set("Authorization", "Bearer worker-secret")
	workerDiagnoseResponse := httptest.NewRecorder()
	server.ServeHTTP(workerDiagnoseResponse, workerDiagnoseRequest)
	if workerDiagnoseResponse.Code != http.StatusOK {
		t.Fatalf("expected worker token diagnose status %d, got %d: %s", http.StatusOK, workerDiagnoseResponse.Code, workerDiagnoseResponse.Body.String())
	}
	controlDiagnoseRequest := httptest.NewRequest(http.MethodPost, "/v1/workers/diagnose", bytes.NewBufferString(diagnoseBody))
	controlDiagnoseRequest.Header.Set("Content-Type", "application/json")
	controlDiagnoseRequest.Header.Set("Authorization", "Bearer control-secret")
	controlDiagnoseResponse := httptest.NewRecorder()
	server.ServeHTTP(controlDiagnoseResponse, controlDiagnoseRequest)
	if controlDiagnoseResponse.Code != http.StatusOK {
		t.Fatalf("expected control token diagnose status %d, got %d: %s", http.StatusOK, controlDiagnoseResponse.Code, controlDiagnoseResponse.Body.String())
	}

	unauthorized := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/workers/"+workerArchivedByWorker.ID+"/archive", `{}`, http.StatusUnauthorized)
	if unauthorized["error"] != "worker or control authorization required" {
		t.Fatalf("expected archive auth error, got %#v", unauthorized)
	}

	workerRequest := httptest.NewRequest(http.MethodPost, "/v1/workers/"+workerArchivedByWorker.ID+"/archive", bytes.NewBufferString(`{}`))
	workerRequest.Header.Set("Content-Type", "application/json")
	workerRequest.Header.Set("Authorization", "Bearer worker-secret")
	workerResponse := httptest.NewRecorder()
	server.ServeHTTP(workerResponse, workerRequest)
	if workerResponse.Code != http.StatusOK {
		t.Fatalf("expected worker token archive status %d, got %d: %s", http.StatusOK, workerResponse.Code, workerResponse.Body.String())
	}

	workerArchivedByControl := registerWorker("archive-by-control")
	controlRequest := httptest.NewRequest(http.MethodPost, "/v1/workers/"+workerArchivedByControl.ID+"/archive", bytes.NewBufferString(`{}`))
	controlRequest.Header.Set("Content-Type", "application/json")
	controlRequest.Header.Set("Authorization", "Bearer control-secret")
	controlResponse := httptest.NewRecorder()
	server.ServeHTTP(controlResponse, controlRequest)
	if controlResponse.Code != http.StatusOK {
		t.Fatalf("expected control token archive status %d, got %d: %s", http.StatusOK, controlResponse.Code, controlResponse.Body.String())
	}
}

func TestControlAuthProtectsWorkerWorkControlPlaneEndpoints(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		nil,
		nil,
		"",
		"control-secret",
	)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "executor",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["exec"]
		},
		"lease_seconds": 30
	}`)

	unauthorized := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`, http.StatusUnauthorized)
	if unauthorized["error"] != "control authorization required" {
		t.Fatalf("expected control auth error, got %#v", unauthorized)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/worker-work", bytes.NewBufferString(`{
		"workspace_id": "wksp_default",
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer control-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected authorized control-plane enqueue status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
	}
	var work managedagents.WorkerWork
	if err := json.NewDecoder(response.Body).Decode(&work); err != nil {
		t.Fatalf("decode authorized work: %v", err)
	}
	if work.WorkerID != worker.ID {
		t.Fatalf("expected selected worker %q, got %+v", worker.ID, work)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/workers", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated worker list status %d, got %d: %s", http.StatusUnauthorized, listResponse.Code, listResponse.Body.String())
	}
	listRequest = httptest.NewRequest(http.MethodGet, "/v1/workers", nil)
	listRequest.Header.Set("Authorization", "Bearer control-secret")
	listResponse = httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized worker list status %d, got %d: %s", http.StatusOK, listResponse.Code, listResponse.Body.String())
	}

	workerGetRequest := httptest.NewRequest(http.MethodGet, "/v1/workers/"+worker.ID, nil)
	workerGetResponse := httptest.NewRecorder()
	server.ServeHTTP(workerGetResponse, workerGetRequest)
	if workerGetResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated worker get status %d, got %d: %s", http.StatusUnauthorized, workerGetResponse.Code, workerGetResponse.Body.String())
	}
	workerGetRequest = httptest.NewRequest(http.MethodGet, "/v1/workers/"+worker.ID, nil)
	workerGetRequest.Header.Set("Authorization", "Bearer control-secret")
	workerGetResponse = httptest.NewRecorder()
	server.ServeHTTP(workerGetResponse, workerGetRequest)
	if workerGetResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized worker get status %d, got %d: %s", http.StatusOK, workerGetResponse.Code, workerGetResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/worker-work/"+work.ID, nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated work get status %d, got %d: %s", http.StatusUnauthorized, getResponse.Code, getResponse.Body.String())
	}

	getRequest = httptest.NewRequest(http.MethodGet, "/v1/worker-work/"+work.ID, nil)
	getRequest.Header.Set("Authorization", "Bearer control-secret")
	getResponse = httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized work get status %d, got %d: %s", http.StatusOK, getResponse.Code, getResponse.Body.String())
	}

	diagnoseRequest := httptest.NewRequest(http.MethodGet, "/v1/worker-work/"+work.ID+"/diagnose", nil)
	diagnoseResponse := httptest.NewRecorder()
	server.ServeHTTP(diagnoseResponse, diagnoseRequest)
	if diagnoseResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated work diagnose status %d, got %d: %s", http.StatusUnauthorized, diagnoseResponse.Code, diagnoseResponse.Body.String())
	}
	diagnoseRequest = httptest.NewRequest(http.MethodGet, "/v1/worker-work/"+work.ID+"/diagnose", nil)
	diagnoseRequest.Header.Set("Authorization", "Bearer control-secret")
	diagnoseResponse = httptest.NewRecorder()
	server.ServeHTTP(diagnoseResponse, diagnoseRequest)
	if diagnoseResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized work diagnose status %d, got %d: %s", http.StatusOK, diagnoseResponse.Code, diagnoseResponse.Body.String())
	}

	cancelRequest := httptest.NewRequest(http.MethodPost, "/v1/worker-work/"+work.ID+"/cancel", bytes.NewBufferString(`{"reason":"test cancel"}`))
	cancelRequest.Header.Set("Content-Type", "application/json")
	cancelResponse := httptest.NewRecorder()
	server.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated work cancel status %d, got %d: %s", http.StatusUnauthorized, cancelResponse.Code, cancelResponse.Body.String())
	}
	cancelRequest = httptest.NewRequest(http.MethodPost, "/v1/worker-work/"+work.ID+"/cancel", bytes.NewBufferString(`{"reason":"test cancel"}`))
	cancelRequest.Header.Set("Content-Type", "application/json")
	cancelRequest.Header.Set("Authorization", "Bearer control-secret")
	cancelResponse = httptest.NewRecorder()
	server.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized work cancel status %d, got %d: %s", http.StatusOK, cancelResponse.Code, cancelResponse.Body.String())
	}

	requeueRequest := httptest.NewRequest(http.MethodPost, "/v1/worker-work/"+work.ID+"/requeue", bytes.NewBufferString(`{}`))
	requeueRequest.Header.Set("Content-Type", "application/json")
	requeueResponse := httptest.NewRecorder()
	server.ServeHTTP(requeueResponse, requeueRequest)
	if requeueResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated work requeue status %d, got %d: %s", http.StatusUnauthorized, requeueResponse.Code, requeueResponse.Body.String())
	}
	requeueRequest = httptest.NewRequest(http.MethodPost, "/v1/worker-work/"+work.ID+"/requeue", bytes.NewBufferString(`{}`))
	requeueRequest.Header.Set("Content-Type", "application/json")
	requeueRequest.Header.Set("Authorization", "Bearer control-secret")
	requeueResponse = httptest.NewRecorder()
	server.ServeHTTP(requeueResponse, requeueRequest)
	if requeueResponse.Code != http.StatusCreated {
		t.Fatalf("expected authorized work requeue status %d, got %d: %s", http.StatusCreated, requeueResponse.Code, requeueResponse.Body.String())
	}

	reapRequest := httptest.NewRequest(http.MethodPost, "/v1/worker-work/reap-expired", bytes.NewBufferString(`{}`))
	reapRequest.Header.Set("Content-Type", "application/json")
	reapResponse := httptest.NewRecorder()
	server.ServeHTTP(reapResponse, reapRequest)
	if reapResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated work reap status %d, got %d: %s", http.StatusUnauthorized, reapResponse.Code, reapResponse.Body.String())
	}
	reapRequest = httptest.NewRequest(http.MethodPost, "/v1/worker-work/reap-expired", bytes.NewBufferString(`{}`))
	reapRequest.Header.Set("Content-Type", "application/json")
	reapRequest.Header.Set("Authorization", "Bearer control-secret")
	reapResponse = httptest.NewRecorder()
	server.ServeHTTP(reapResponse, reapRequest)
	if reapResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized work reap status %d, got %d: %s", http.StatusOK, reapResponse.Code, reapResponse.Body.String())
	}

	workerReapRequest := httptest.NewRequest(http.MethodPost, "/v1/workers/reap-expired", bytes.NewBufferString(`{}`))
	workerReapRequest.Header.Set("Content-Type", "application/json")
	workerReapResponse := httptest.NewRecorder()
	server.ServeHTTP(workerReapResponse, workerReapRequest)
	if workerReapResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated worker reap status %d, got %d: %s", http.StatusUnauthorized, workerReapResponse.Code, workerReapResponse.Body.String())
	}
	workerReapRequest = httptest.NewRequest(http.MethodPost, "/v1/workers/reap-expired", bytes.NewBufferString(`{}`))
	workerReapRequest.Header.Set("Content-Type", "application/json")
	workerReapRequest.Header.Set("Authorization", "Bearer control-secret")
	workerReapResponse = httptest.NewRecorder()
	server.ServeHTTP(workerReapResponse, workerReapRequest)
	if workerReapResponse.Code != http.StatusOK {
		t.Fatalf("expected authorized worker reap status %d, got %d: %s", http.StatusOK, workerReapResponse.Code, workerReapResponse.Body.String())
	}
}

func TestWorkerWorkLifecycle(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.run_command"],
			"runtimes": ["local_system"],
			"capabilities": ["exec"]
		},
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`)
	if queued.WorkerID != worker.ID {
		t.Fatalf("expected enqueue to select worker %q, got %+v", worker.ID, queued)
	}

	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=45")
	if polled.Work == nil || polled.Work.ID != queued.ID {
		t.Fatalf("expected queued work from poll, got %+v", polled.Work)
	}
	if polled.Work.Status != managedagents.WorkerWorkStatusLeased || polled.Work.WorkerID != worker.ID || polled.Work.LeaseExpiresAt == nil {
		t.Fatalf("expected leased work, got %+v", polled.Work)
	}

	acked := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/ack", `{}`, http.StatusOK)
	if acked.Status != managedagents.WorkerWorkStatusRunning || acked.StartedAt == nil {
		t.Fatalf("expected running work after ack, got %+v", acked)
	}

	heartbeat := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/heartbeat", `{
		"lease_seconds": 60
	}`, http.StatusOK)
	if heartbeat.Status != managedagents.WorkerWorkStatusRunning || heartbeat.LeaseExpiresAt == nil {
		t.Fatalf("expected running work heartbeat, got %+v", heartbeat)
	}

	completed := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/result", `{
		"success": true,
		"result": {"ok": true}
	}`, http.StatusOK)
	if completed.Status != managedagents.WorkerWorkStatusCompleted || completed.CompletedAt == nil {
		t.Fatalf("expected completed work, got %+v", completed)
	}
	if string(completed.Result) != `{"ok":true}` {
		t.Fatalf("unexpected result JSON: %s", string(completed.Result))
	}

	fetched := getJSON[managedagents.WorkerWork](t, server, "/v1/worker-work/"+queued.ID)
	if fetched.ID != queued.ID || fetched.Status != managedagents.WorkerWorkStatusCompleted || string(fetched.Result) != `{"ok":true}` {
		t.Fatalf("unexpected fetched work: %+v result=%s", fetched, string(fetched.Result))
	}

	empty := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll")
	if empty.Work != nil {
		t.Fatalf("expected no more work, got %+v", empty.Work)
	}
}

func TestReapExpiredWorkerWork(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"worker_id": "`+worker.ID+`",
		"work_type": "sandbox_command",
		"payload": {"command": "sh", "args": ["-c", "sleep 100"]}
	}`)
	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=1")
	if polled.Work == nil || polled.Work.ID != queued.ID {
		t.Fatalf("expected queued work from poll, got %+v", polled.Work)
	}

	store.mu.Lock()
	expiredAt := time.Now().UTC().Add(-time.Minute)
	work := store.workerWork[queued.ID]
	work.LeaseExpiresAt = &expiredAt
	store.workerWork[queued.ID] = work
	store.mu.Unlock()

	response := postJSONWithStatus[struct {
		Count   int                        `json:"count"`
		Expired []managedagents.WorkerWork `json:"expired"`
	}](t, server, http.MethodPost, "/v1/worker-work/reap-expired", `{"limit":10}`, http.StatusOK)
	if response.Count != 1 || len(response.Expired) != 1 {
		t.Fatalf("expected one expired work, got %+v", response)
	}
	expired := response.Expired[0]
	if expired.ID != queued.ID || expired.Status != managedagents.WorkerWorkStatusFailed || expired.CompletedAt == nil {
		t.Fatalf("unexpected expired work: %+v", expired)
	}
	if !strings.Contains(expired.ErrorMessage, "worker work lease expired") {
		t.Fatalf("expected lease expiry error message, got %q", expired.ErrorMessage)
	}

	fetched := getJSON[managedagents.WorkerWork](t, server, "/v1/worker-work/"+queued.ID)
	if fetched.Status != managedagents.WorkerWorkStatusFailed || fetched.CompletedAt == nil {
		t.Fatalf("expected fetched work to remain failed, got %+v", fetched)
	}
}

func TestCancelWorkerWork(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"worker_id": "`+worker.ID+`",
		"work_type": "sandbox_command",
		"payload": {"command": "sh", "args": ["-c", "sleep 100"]}
	}`)
	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=30")
	if polled.Work == nil || polled.Work.ID != queued.ID {
		t.Fatalf("expected queued work from poll, got %+v", polled.Work)
	}

	canceled := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/worker-work/"+queued.ID+"/cancel", `{
		"reason": "user stopped it"
	}`, http.StatusOK)
	if canceled.Status != managedagents.WorkerWorkStatusCanceled || canceled.ErrorMessage != "user stopped it" || canceled.CompletedAt == nil {
		t.Fatalf("expected canceled work, got %+v", canceled)
	}

	heartbeat := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/heartbeat", `{
		"lease_seconds": 30
	}`, http.StatusOK)
	if heartbeat.Status != managedagents.WorkerWorkStatusCanceled {
		t.Fatalf("expected heartbeat to return canceled work, got %+v", heartbeat)
	}

	completed := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/workers/"+worker.ID+"/work/"+queued.ID+"/result", `{
		"success": true,
		"result": {"ok": true}
	}`, http.StatusOK)
	if completed.Status != managedagents.WorkerWorkStatusCanceled || string(completed.Result) == `{"ok":true}` {
		t.Fatalf("expected result after cancel to be ignored, got %+v result=%s", completed, string(completed.Result))
	}

	diagnosis := getJSON[workerWorkDiagnoseResponse](t, server, "/v1/worker-work/"+queued.ID+"/diagnose")
	if diagnosis.Work.Status != managedagents.WorkerWorkStatusCanceled || !containsString(diagnosis.Reasons, "work was canceled") {
		t.Fatalf("expected canceled diagnosis, got %+v", diagnosis)
	}
}

func TestRequeueWorkerWork(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"worker_id": "`+worker.ID+`",
		"environment_id": "env_local",
		"session_id": "sess_000001",
		"turn_id": "turn_000001",
		"work_type": "sandbox_command",
		"payload": {"command": "sh", "args": ["-c", "printf retry"]}
	}`)
	if queued.Status != managedagents.WorkerWorkStatusPending {
		t.Fatalf("expected queued work, got %+v", queued)
	}

	conflict := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/worker-work/"+queued.ID+"/requeue", `{}`, http.StatusConflict)
	if !strings.Contains(conflict["error"], "only failed or canceled") {
		t.Fatalf("expected non-terminal requeue conflict, got %+v", conflict)
	}

	canceled := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/worker-work/"+queued.ID+"/cancel", `{
		"reason": "operator retry"
	}`, http.StatusOK)
	if canceled.Status != managedagents.WorkerWorkStatusCanceled {
		t.Fatalf("expected canceled work, got %+v", canceled)
	}

	requeued := postJSONWithStatus[managedagents.WorkerWork](t, server, http.MethodPost, "/v1/worker-work/"+queued.ID+"/requeue", `{
		"clear_worker": true
	}`, http.StatusCreated)
	if requeued.ID == queued.ID || requeued.Status != managedagents.WorkerWorkStatusPending || requeued.WorkerID != "" {
		t.Fatalf("unexpected requeued work: %+v", requeued)
	}
	if requeued.WorkspaceID != queued.WorkspaceID || requeued.EnvironmentID != queued.EnvironmentID || requeued.SessionID != queued.SessionID || requeued.TurnID != queued.TurnID || requeued.WorkType != queued.WorkType {
		t.Fatalf("requeued work did not preserve original routing fields: original=%+v requeued=%+v", queued, requeued)
	}
	if string(requeued.Payload) != string(queued.Payload) || string(requeued.Result) != `{}` || requeued.CompletedAt != nil || requeued.StartedAt != nil || requeued.LeaseExpiresAt != nil {
		t.Fatalf("requeued work did not reset execution fields: %+v payload=%s result=%s", requeued, string(requeued.Payload), string(requeued.Result))
	}

	original := getJSON[managedagents.WorkerWork](t, server, "/v1/worker-work/"+queued.ID)
	if original.Status != managedagents.WorkerWorkStatusCanceled || original.ErrorMessage != "operator retry" {
		t.Fatalf("expected original work to remain canceled, got %+v", original)
	}

	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=30")
	if polled.Work == nil || polled.Work.ID != requeued.ID {
		t.Fatalf("expected worker to poll requeued work, got %+v", polled.Work)
	}
}

func TestDiagnoseWorkerWorkReportsExpiredLease(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	worker := postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "viito-mac",
		"worker_type": "local",
		"lease_seconds": 30
	}`)
	queued := postJSON[managedagents.WorkerWork](t, server, "/v1/worker-work", `{
		"workspace_id": "wksp_default",
		"worker_id": "`+worker.ID+`",
		"work_type": "sandbox_command",
		"payload": {"command": "sh", "args": ["-c", "sleep 100"]}
	}`)
	polled := getJSON[struct {
		Work *managedagents.WorkerWork `json:"work"`
	}](t, server, "/v1/workers/"+worker.ID+"/work/poll?lease_seconds=1")
	if polled.Work == nil || polled.Work.ID != queued.ID {
		t.Fatalf("expected queued work from poll, got %+v", polled.Work)
	}

	store.mu.Lock()
	expiredAt := time.Now().UTC().Add(-time.Minute)
	work := store.workerWork[queued.ID]
	work.LeaseExpiresAt = &expiredAt
	store.workerWork[queued.ID] = work
	workerRecord := store.workers[worker.ID]
	workerRecord.LeaseExpiresAt = &expiredAt
	store.workers[worker.ID] = workerRecord
	store.mu.Unlock()

	response := getJSON[struct {
		Work struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"work"`
		Worker *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"worker"`
		Reasons []string `json:"reasons"`
		Actions []string `json:"actions"`
	}](t, server, "/v1/worker-work/"+queued.ID+"/diagnose")
	if response.Work.ID != queued.ID || response.Work.Status != managedagents.WorkerWorkStatusLeased {
		t.Fatalf("unexpected diagnosed work: %+v", response.Work)
	}
	if response.Worker == nil || response.Worker.ID != worker.ID {
		t.Fatalf("expected assigned worker summary, got %+v", response.Worker)
	}
	joinedReasons := strings.Join(response.Reasons, "\n")
	if !strings.Contains(joinedReasons, "work lease expired") || !strings.Contains(joinedReasons, "assigned worker lease expired") {
		t.Fatalf("expected lease expiry reasons, got %+v", response.Reasons)
	}
	if len(response.Actions) == 0 || !strings.Contains(strings.Join(response.Actions, "\n"), "work reap-expired") {
		t.Fatalf("expected reap action, got %+v", response.Actions)
	}
}

func TestWorkerWorkRejectsInvalidToolExecutionPayload(t *testing.T) {
	server := newTestServer()

	response := postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/worker-work", `{
		"work_type": "tool_execution",
		"payload": {"command": "echo hello"}
	}`, http.StatusBadRequest)
	if !strings.Contains(response["error"], "unsupported tool namespace") {
		t.Fatalf("unexpected error response: %+v", response)
	}
}

func TestWorkerWorkRejectsToolExecutionWithoutMatchingWorker(t *testing.T) {
	server := newTestServer()
	postJSON[managedagents.Worker](t, server, "/v1/workers", `{
		"name": "reader-only",
		"worker_type": "local",
		"capabilities": {
			"namespaces": ["default"],
			"apis": ["default.read_file"],
			"runtimes": ["local_system"],
			"capabilities": ["filesystem.read"]
		},
		"lease_seconds": 30
	}`)

	response := postJSONWithStatus[workerWorkConflictResponse](t, server, http.MethodPost, "/v1/worker-work", `{
		"work_type": "tool_execution",
		"payload": {
			"protocol_version": "tma.work.v1",
			"namespace": "default",
			"api": "run_command",
			"capabilities": ["exec"],
			"risk": "exec",
			"runtime": "local_system",
			"input": {"command": "sh", "args": ["-c", "printf hello"]}
		}
	}`, http.StatusConflict)
	if !strings.Contains(response.Error, "no online worker matches tool invocation") {
		t.Fatalf("unexpected error response: %+v", response)
	}
	if response.Invocation.API != "run_command" || response.Matches != 0 || len(response.Diagnostics) != 1 {
		t.Fatalf("unexpected diagnostics summary: %+v", response)
	}
	diagnosis := response.Diagnostics[0]
	if diagnosis.Name != "reader-only" || diagnosis.Match || !slices.Contains(diagnosis.Reasons, "missing api default.run_command") || !slices.Contains(diagnosis.Reasons, "missing capability exec") {
		t.Fatalf("unexpected worker diagnosis: %+v", diagnosis)
	}
}

func TestLLMModelManagement(t *testing.T) {
	server := newTestServer()

	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id": "volcengine-agent-plan",
		"provider_type": "openai"
	}`)
	created := createLLMModel(t, server, `{
		"provider_id": "volcengine-agent-plan",
		"model": "doubao-test",
		"context_window_tokens": 256000
	}`)
	if created.ProviderID != "volcengine-agent-plan" || created.Model != "doubao-test" || created.ContextWindowTokens != 256000 {
		t.Fatalf("unexpected created model: %+v", created)
	}
	if created.Revision != 1 {
		t.Fatalf("expected new model revision 1, got %+v", created)
	}

	missingCreatePrecondition := httptest.NewRecorder()
	missingCreateRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"volcengine-agent-plan","model":"missing-precondition","context_window_tokens":1024
	}`))
	missingCreateRequest.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(missingCreatePrecondition, missingCreateRequest)
	if missingCreatePrecondition.Code != http.StatusBadRequest {
		t.Fatalf("expected model create without If-None-Match to return 400, got %d: %s", missingCreatePrecondition.Code, missingCreatePrecondition.Body.String())
	}

	duplicateCreate := httptest.NewRecorder()
	duplicateCreateRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"volcengine-agent-plan","model":"doubao-test","context_window_tokens":512000
	}`))
	duplicateCreateRequest.Header.Set("Content-Type", "application/json")
	duplicateCreateRequest.Header.Set("If-None-Match", "*")
	server.ServeHTTP(duplicateCreate, duplicateCreateRequest)
	if duplicateCreate.Code != http.StatusConflict {
		t.Fatalf("expected duplicate model create to return 409, got %d: %s", duplicateCreate.Code, duplicateCreate.Body.String())
	}

	missingUpdatePrecondition := httptest.NewRecorder()
	missingUpdateRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"volcengine-agent-plan","model":"doubao-test","context_window_tokens":512000
	}`))
	missingUpdateRequest.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(missingUpdatePrecondition, missingUpdateRequest)
	if missingUpdatePrecondition.Code != http.StatusBadRequest {
		t.Fatalf("expected model update without If-Match to return 400, got %d: %s", missingUpdatePrecondition.Code, missingUpdatePrecondition.Body.String())
	}

	updated := updateLLMModel(t, server, created.Revision, `{
		"provider_id":"volcengine-agent-plan","model":"doubao-test","context_window_tokens":512000
	}`)
	if updated.Revision != created.Revision+1 || updated.ContextWindowTokens != 512000 {
		t.Fatalf("expected model update to advance revision, got create=%+v update=%+v", created, updated)
	}
	staleUpdate := httptest.NewRecorder()
	staleUpdateRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"volcengine-agent-plan","model":"doubao-test","context_window_tokens":1024
	}`))
	staleUpdateRequest.Header.Set("Content-Type", "application/json")
	staleUpdateRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(created.Revision, 10)))
	server.ServeHTTP(staleUpdate, staleUpdateRequest)
	if staleUpdate.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected stale model update to return 412, got %d: %s", staleUpdate.Code, staleUpdate.Body.String())
	}

	listed := getJSON[llmModelsResponse](t, server, "/v1/llm-models?provider_id=volcengine-agent-plan")
	if len(listed.Models) != 1 || listed.Models[0].ContextWindowTokens != updated.ContextWindowTokens || listed.Models[0].Revision != updated.Revision {
		t.Fatalf("unexpected model list: %+v", listed.Models)
	}

	deletePath := "/v1/llm-models/volcengine-agent-plan/doubao-test"
	deleteResponse := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, deletePath, nil)
	missingDeletePrecondition := httptest.NewRecorder()
	server.ServeHTTP(missingDeletePrecondition, httptest.NewRequest(http.MethodDelete, deletePath, nil))
	if missingDeletePrecondition.Code != http.StatusBadRequest {
		t.Fatalf("expected model delete without If-Match to return 400, got %d: %s", missingDeletePrecondition.Code, missingDeletePrecondition.Body.String())
	}
	staleDelete := httptest.NewRecorder()
	staleDeleteRequest := httptest.NewRequest(http.MethodDelete, deletePath, nil)
	staleDeleteRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(created.Revision, 10)))
	server.ServeHTTP(staleDelete, staleDeleteRequest)
	if staleDelete.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected stale model delete to return 412, got %d: %s", staleDelete.Code, staleDelete.Body.String())
	}
	deleteRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(updated.Revision, 10)))
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected model deletion to return 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	listed = getJSON[llmModelsResponse](t, server, "/v1/llm-models?provider_id=volcengine-agent-plan")
	if len(listed.Models) != 0 {
		t.Fatalf("expected empty model list after deletion, got %+v", listed.Models)
	}

	inUse := createLLMModel(t, server, `{
		"provider_id": "volcengine-agent-plan",
		"model": "doubao-in-use",
		"context_window_tokens": 256000
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "model-delete-guard",
		"llm_provider": "volcengine-agent-plan",
		"llm_model": "doubao-in-use"
	}`)
	conflictResponse := httptest.NewRecorder()
	conflictRequest := httptest.NewRequest(http.MethodDelete, "/v1/llm-models/volcengine-agent-plan/doubao-in-use", nil)
	conflictRequest.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(inUse.Revision, 10)))
	server.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("expected referenced model deletion to return 409, got %d: %s", conflictResponse.Code, conflictResponse.Body.String())
	}
}

func TestLLMModelCapabilitiesAndDefaultVisionSelection(t *testing.T) {
	server := newTestServer()
	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{"id":"vision-provider","provider_type":"openai"}`)

	first := createLLMModel(t, server, `{
		"provider_id":"vision-provider","model":"vision-one","context_window_tokens":128000,
		"capability_type":"text_image","is_default_vision":true
	}`)
	if first.CapabilityType != managedagents.LLMModelCapabilityTextImage || !first.IsDefaultVision {
		t.Fatalf("unexpected first vision model: %+v", first)
	}

	second := createLLMModel(t, server, `{
		"provider_id":"vision-provider","model":"vision-two","context_window_tokens":128000,
		"capability_type":"text_image","is_default_vision":true
	}`)
	if !second.IsDefaultVision {
		t.Fatalf("expected second model to become default vision: %+v", second)
	}
	listed := getJSON[llmModelsResponse](t, server, "/v1/llm-models?provider_id=vision-provider")
	defaults := 0
	for _, model := range listed.Models {
		if model.Model == first.Model && (model.IsDefaultVision || model.Revision != first.Revision+1) {
			t.Fatalf("expected displaced default model revision to advance, before=%+v after=%+v", first, model)
		}
		if model.IsDefaultVision {
			defaults++
			if model.Model != "vision-two" {
				t.Fatalf("unexpected default vision model: %+v", model)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly one default vision model, got %+v", listed.Models)
	}

	invalidDefault := httptest.NewRecorder()
	invalidDefaultRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"vision-provider","model":"text-only","context_window_tokens":128000,
		"capability_type":"text","is_default_vision":true
	}`))
	invalidDefaultRequest.Header.Set("Content-Type", "application/json")
	invalidDefaultRequest.Header.Set("If-None-Match", "*")
	server.ServeHTTP(invalidDefault, invalidDefaultRequest)
	if invalidDefault.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid default vision model to return 400, got %d: %s", invalidDefault.Code, invalidDefault.Body.String())
	}
}

func TestLLMEmbeddingAndRerankerModelConfiguration(t *testing.T) {
	server := newTestServer()
	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{"id":"knowledge-provider","provider_type":"openai-compatible"}`)

	firstEmbedding := createLLMModel(t, server, `{
		"provider_id":"knowledge-provider","model":"bge-m3","context_window_tokens":8192,
		"capability_type":"embedding","is_default_embedding":true,
		"capabilities":{"dimensions":1024,"distance_metric":"cosine","normalized":true,"max_batch_size":32,"protocol":"openai_embeddings"}
	}`)
	if firstEmbedding.CapabilityType != managedagents.LLMModelCapabilityEmbedding || !firstEmbedding.IsDefaultEmbedding || firstEmbedding.Capabilities.Dimensions != 1024 {
		t.Fatalf("unexpected embedding model: %+v", firstEmbedding)
	}

	secondEmbedding := createLLMModel(t, server, `{
		"provider_id":"knowledge-provider","model":"text-embedding-small","context_window_tokens":8192,
		"capability_type":"embedding","is_default_embedding":true,
		"capabilities":{"dimensions":1536,"distance_metric":"cosine","max_batch_size":64,"protocol":"openai_embeddings"}
	}`)
	reranker := createLLMModel(t, server, `{
		"provider_id":"knowledge-provider","model":"bge-reranker-v2-m3","context_window_tokens":8192,
		"capability_type":"reranker","is_default_reranker":true,
		"capabilities":{"max_candidates":50,"protocol":"jina_rerank"}
	}`)
	if !secondEmbedding.IsDefaultEmbedding || !reranker.IsDefaultReranker || reranker.Capabilities.MaxCandidates != 50 {
		t.Fatalf("unexpected knowledge model defaults: embedding=%+v reranker=%+v", secondEmbedding, reranker)
	}

	listed := getJSON[llmModelsResponse](t, server, "/v1/llm-models?provider_id=knowledge-provider")
	embeddingDefaults, rerankerDefaults := 0, 0
	for _, model := range listed.Models {
		if model.IsDefaultEmbedding {
			embeddingDefaults++
			if model.Model != secondEmbedding.Model {
				t.Fatalf("unexpected default embedding model: %+v", model)
			}
		}
		if model.IsDefaultReranker {
			rerankerDefaults++
		}
		if model.Model == firstEmbedding.Model && model.Revision != firstEmbedding.Revision+1 {
			t.Fatalf("expected displaced embedding default revision to advance: before=%+v after=%+v", firstEmbedding, model)
		}
	}
	if embeddingDefaults != 1 || rerankerDefaults != 1 {
		t.Fatalf("unexpected default counts: embedding=%d reranker=%d models=%+v", embeddingDefaults, rerankerDefaults, listed.Models)
	}

	invalidEmbedding := httptest.NewRecorder()
	invalidEmbeddingRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"knowledge-provider","model":"invalid-embedding","capability_type":"embedding",
		"capabilities":{"protocol":"openai_embeddings"}
	}`))
	invalidEmbeddingRequest.Header.Set("Content-Type", "application/json")
	invalidEmbeddingRequest.Header.Set("If-None-Match", "*")
	server.ServeHTTP(invalidEmbedding, invalidEmbeddingRequest)
	if invalidEmbedding.Code != http.StatusBadRequest {
		t.Fatalf("expected missing embedding dimensions to return 400, got %d: %s", invalidEmbedding.Code, invalidEmbedding.Body.String())
	}

	invalidDefault := httptest.NewRecorder()
	invalidDefaultRequest := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(`{
		"provider_id":"knowledge-provider","model":"invalid-default","capability_type":"text",
		"is_default_reranker":true
	}`))
	invalidDefaultRequest.Header.Set("Content-Type", "application/json")
	invalidDefaultRequest.Header.Set("If-None-Match", "*")
	server.ServeHTTP(invalidDefault, invalidDefaultRequest)
	if invalidDefault.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid reranker default to return 400, got %d: %s", invalidDefault.Code, invalidDefault.Body.String())
	}
}

func TestCreateAgentRejectsDisabledLLMProvider(t *testing.T) {
	server := newTestServer()
	postJSON[managedagents.LLMProvider](t, server, "/v1/llm-providers", `{
		"id": "disabled-provider",
		"provider_type": "openai",
		"enabled": false
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewBufferString(`{
		"name": "Code Assistant",
		"llm_provider": "disabled-provider",
		"llm_model": "gpt-4o"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d for disabled provider, got %d: %s", http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestAgentConfigVersionUpdateKeepsExistingSessionsPinned(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1",
		"system": "version one"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	oldSession := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if oldSession.AgentConfigVersion != 1 {
		t.Fatalf("expected old session pinned to config version 1, got %d", oldSession.AgentConfigVersion)
	}

	updated := postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)
	if updated.CurrentConfigVersion != 2 {
		t.Fatalf("expected agent current config version 2, got %d", updated.CurrentConfigVersion)
	}
	if updated.ConfigVersion.LLMProvider != "fake" {
		t.Fatalf("expected update to inherit llm provider fake, got %q", updated.ConfigVersion.LLMProvider)
	}
	if updated.ConfigVersion.LLMModel != "fake-v2" || updated.ConfigVersion.System != "version two" {
		t.Fatalf("unexpected updated config version: %+v", updated.ConfigVersion)
	}

	versions := getJSON[agentConfigVersionsResponse](t, server, "/v1/agents/"+agent.ID+"/config-versions")
	if len(versions.ConfigVersions) != 2 {
		t.Fatalf("expected 2 config versions, got %d", len(versions.ConfigVersions))
	}
	if versions.ConfigVersions[0].LLMModel != "fake-v1" || versions.ConfigVersions[1].LLMModel != "fake-v2" {
		t.Fatalf("unexpected config versions: %+v", versions.ConfigVersions)
	}

	newSession := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	if newSession.AgentConfigVersion != 2 {
		t.Fatalf("expected new session pinned to config version 2, got %d", newSession.AgentConfigVersion)
	}

	oldSessionAfterUpdate := getJSON[managedagents.Session](t, server, "/v1/sessions/"+oldSession.ID)
	if oldSessionAfterUpdate.AgentConfigVersion != 1 {
		t.Fatalf("expected old session to remain pinned to config version 1, got %d", oldSessionAfterUpdate.AgentConfigVersion)
	}
}

func TestAgentConfigVersionRollbackCreatesNewImmutableVersion(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Rollback Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1",
		"system": "version one",
		"tools": {"enabled_tools":["filesystem"]}
	}`)
	updated := postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two",
		"tools": {"enabled_tools":["web"]}
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "rollback-cloud",
		"config": {"type": "cloud"}
	}`)
	pinnedSession := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	result := postJSON[agentConfigRollbackResponse](t, server, "/v1/agents/"+agent.ID+"/config-versions/1/rollback", `{}`)
	if result.SourceVersion != 1 || result.PreviousVersion != 2 || result.NewVersion != 3 {
		t.Fatalf("unexpected rollback metadata: %+v", result)
	}
	if result.Agent.CurrentConfigVersion != 3 || result.Agent.ConfigVersion.Version != 3 {
		t.Fatalf("expected rollback to create current version 3: %+v", result.Agent)
	}
	if result.Agent.ConfigVersion.LLMModel != "fake-v1" || result.Agent.ConfigVersion.System != "version one" {
		t.Fatalf("expected version 1 model and system to be copied: %+v", result.Agent.ConfigVersion)
	}
	if string(result.Agent.ConfigVersion.Tools) != `{"enabled_tools":["filesystem"]}` {
		t.Fatalf("expected version 1 tools to be copied, got %s", result.Agent.ConfigVersion.Tools)
	}
	if pinnedSession.AgentConfigVersion != updated.CurrentConfigVersion {
		t.Fatalf("expected existing session to remain pinned to version 2, got %d", pinnedSession.AgentConfigVersion)
	}
	pinnedSessionAfter := getJSON[managedagents.Session](t, server, "/v1/sessions/"+pinnedSession.ID)
	if pinnedSessionAfter.AgentConfigVersion != 2 {
		t.Fatalf("expected existing session to remain pinned after rollback, got %d", pinnedSessionAfter.AgentConfigVersion)
	}
	versions := getJSON[agentConfigVersionsResponse](t, server, "/v1/agents/"+agent.ID+"/config-versions")
	if len(versions.ConfigVersions) != 3 || versions.ConfigVersions[2].System != "version one" {
		t.Fatalf("unexpected config versions after rollback: %+v", versions.ConfigVersions)
	}
	audit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/operator-audit?action=agent.config.rollback")
	if len(audit.Records) != 1 || audit.Records[0].ResourceID != agent.ID || audit.Records[0].Outcome != "succeeded" {
		t.Fatalf("unexpected rollback audit: %+v", audit.Records)
	}
}

func TestAgentConfigVersionRollbackRejectsCurrentVersion(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Rollback Guard",
		"llm_provider": "fake",
		"llm_model": "fake-v1"
	}`)
	postJSONWithStatus[map[string]any](t, server, http.MethodPost, "/v1/agents/"+agent.ID+"/config-versions/1/rollback", `{}`, http.StatusBadRequest)
}

func TestUpgradeSessionAgentConfigToCurrent(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1",
		"system": "version one"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)

	var result managedagents.UpgradeSessionAgentConfigResult
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/config/upgrade", bytes.NewBufferString(`{"to_current":true,"updated_by":"tester"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected upgrade status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Changed || result.OldAgentConfigVersion != 1 || result.NewAgentConfigVersion != 2 {
		t.Fatalf("unexpected upgrade result: %+v", result)
	}
	if result.Event.Type != managedagents.EventSessionConfigUpdated {
		t.Fatalf("expected config updated event, got %+v", result.Event)
	}
	updatedSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if updatedSession.AgentConfigVersion != 2 {
		t.Fatalf("expected session to upgrade to version 2, got %d", updatedSession.AgentConfigVersion)
	}
}

func TestUpgradeSessionAgentConfigToExactVersion(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Exact Config Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1",
		"system": "version one"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "exact-config-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-demo",
		"system": "version three"
	}`)

	var result managedagents.UpgradeSessionAgentConfigResult
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/config/upgrade", bytes.NewBufferString(`{"to_version":2,"updated_by":"workbench"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected exact upgrade status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Changed || result.OldAgentConfigVersion != 1 || result.NewAgentConfigVersion != 2 || result.LatestAgentConfigVersion != 3 {
		t.Fatalf("unexpected exact upgrade result: %+v", result)
	}
	updatedSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if updatedSession.AgentConfigVersion != 2 {
		t.Fatalf("expected session to use exact version 2, got %d", updatedSession.AgentConfigVersion)
	}
}

func TestUpgradeSessionAgentConfigRejectsInvalidExactTargets(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Config Target Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "config-target-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2"
	}`)
	newerSession := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"agent_config_version": 2,
		"environment_id": "`+environment.ID+`"
	}`)

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "both selectors", body: `{"to_current":true,"to_version":1}`, status: http.StatusBadRequest},
		{name: "explicitly no selector", body: `{"to_current":false}`, status: http.StatusBadRequest},
		{name: "unknown version", body: `{"to_version":99}`, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/config/upgrade", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("expected status %d, got %d: %s", test.status, response.Code, response.Body.String())
			}
		})
	}

	downgradeRequest := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+newerSession.ID+"/config/upgrade", bytes.NewBufferString(`{"to_version":1}`))
	downgradeRequest.Header.Set("Content-Type", "application/json")
	downgradeResponse := httptest.NewRecorder()
	server.ServeHTTP(downgradeResponse, downgradeRequest)
	if downgradeResponse.Code != http.StatusConflict {
		t.Fatalf("expected downgrade status %d, got %d: %s", http.StatusConflict, downgradeResponse.Code, downgradeResponse.Body.String())
	}
}

func TestUpgradeSessionAgentConfigRequiresIdleSession(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-v1",
		"system": "version one"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	postJSON[managedagents.Agent](t, server, "/v1/agents/"+agent.ID+"/config-versions", `{
		"llm_model": "fake-v2",
		"system": "version two"
	}`)
	if _, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"run"}]}`),
	}}); err != nil {
		t.Fatalf("start session turn: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/config/upgrade", bytes.NewBufferString(`{"to_current":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected upgrade status %d, got %d: %s", http.StatusConflict, response.Code, response.Body.String())
	}
}

func TestManagedAgentsMinimumFlow(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)

	if agent.ID == "" {
		t.Fatal("expected agent id")
	}
	if agent.CurrentConfigVersion != 1 {
		t.Fatalf("expected current version 1, got %d", agent.CurrentConfigVersion)
	}
	if agent.ConfigVersion.LLMProvider != "fake" {
		t.Fatalf("expected default llm provider fake, got %q", agent.ConfigVersion.LLMProvider)
	}
	if agent.ConfigVersion.LLMModel != "gpt-4o" {
		t.Fatalf("expected llm model gpt-4o, got %q", agent.ConfigVersion.LLMModel)
	}

	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {
			"type": "cloud",
			"networking": {
				"type": "limited",
				"allowed_hosts": ["api.github.com"]
			}
		}
	}`)

	if environment.ID == "" {
		t.Fatal("expected environment id")
	}

	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`",
		"title": "First TMA task"
	}`)

	if session.ID == "" {
		t.Fatal("expected session id")
	}
	if session.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session status %q, got %q", managedagents.SessionStatusIdle, session.Status)
	}

	appendResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [
			{
				"type": "user.message",
				"payload": {
					"content": [{"type": "text", "text": "hello"}]
				}
			}
		]
	}`)

	if len(appendResponse.Events) != 2 {
		t.Fatalf("expected 2 appended events, got %d", len(appendResponse.Events))
	}
	if appendResponse.Events[0].Type != managedagents.EventSessionStatusRunning {
		t.Fatalf("expected first appended event %q, got %q", managedagents.EventSessionStatusRunning, appendResponse.Events[0].Type)
	}
	if appendResponse.Events[1].Type != managedagents.EventUserMessage {
		t.Fatalf("expected second appended event %q, got %q", managedagents.EventUserMessage, appendResponse.Events[1].Type)
	}
	if appendResponse.Events[1].Seq != 4 {
		t.Fatalf("expected user event seq 4 after session status events, got %d", appendResponse.Events[1].Seq)
	}
	turnID := payloadString(appendResponse.Events[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected user.message payload to include turn_id")
	}
	if got := payloadString(appendResponse.Events[0].Payload, "turn_id"); got != turnID {
		t.Fatalf("expected running status turn_id %q, got %q", turnID, got)
	}

	runningSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if runningSession.Status != managedagents.SessionStatusRunning {
		t.Fatalf("expected session status %q immediately after user.message, got %q", managedagents.SessionStatusRunning, runningSession.Status)
	}

	waitFor(t, func() bool {
		idleSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
		return idleSession.Status == managedagents.SessionStatusIdle
	})

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events.Events))
	}
	if events.Events[0].Type != managedagents.EventSessionStatusProvisioning {
		t.Fatalf("expected first event %q, got %q", managedagents.EventSessionStatusProvisioning, events.Events[0].Type)
	}
	if events.Events[1].Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("expected second event %q, got %q", managedagents.EventSessionStatusIdle, events.Events[1].Type)
	}
	if events.Events[2].Type != managedagents.EventSessionStatusRunning {
		t.Fatalf("expected third event %q, got %q", managedagents.EventSessionStatusRunning, events.Events[2].Type)
	}
	for _, event := range events.Events[2:] {
		if got := payloadString(event.Payload, "turn_id"); got != turnID {
			t.Fatalf("expected event %s to use turn_id %q, got %q", event.Type, turnID, got)
		}
	}

	eventsAfterSeq := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events?after_seq=2")
	if len(eventsAfterSeq.Events) != 4 {
		t.Fatalf("expected 4 events after seq 2, got %d", len(eventsAfterSeq.Events))
	}
	if eventsAfterSeq.Events[1].Type != managedagents.EventUserMessage {
		t.Fatalf("expected user.message event, got %q", eventsAfterSeq.Events[1].Type)
	}
	if eventsAfterSeq.Events[2].Type != managedagents.EventAgentMessage {
		t.Fatalf("expected agent.message event, got %q", eventsAfterSeq.Events[2].Type)
	}
}

func TestSessionRuntimeSettingsHotUpdate(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	updated := patchSessionRuntimeSettings(t, server, session.ID, session.RuntimeSettingsRevision, `{
		"intervention_mode": "approve_for_me",
		"tool_runtime": "cloud_sandbox",
		"cloud_sandbox_root": ".",
		"cloud_sandbox_allow_network": true,
		"human_interaction": {"enabled": false, "modes": ["form", "select"], "fallback": "fail"},
		"completion_gate": {"max_retries": 99}
	}`, http.StatusOK)
	assertRuntimeSettings(t, updated.RuntimeSettings, map[string]any{
		"intervention_mode":           "approve_for_me",
		"tool_runtime":                "cloud_sandbox",
		"cloud_sandbox_root":          ".",
		"cloud_sandbox_allow_network": true,
		"human_interaction":           map[string]any{"enabled": false, "modes": []any{"form", "select"}, "fallback": "fail"},
		"completion_gate":             map[string]any{"max_retries": float64(10)},
	})

	merged := patchSessionRuntimeSettings(t, server, session.ID, updated.RuntimeSettingsRevision, `{
		"tool_runtime": "local_system"
	}`, http.StatusOK)
	assertRuntimeSettings(t, merged.RuntimeSettings, map[string]any{
		"intervention_mode":           "approve_for_me",
		"tool_runtime":                "local_system",
		"cloud_sandbox_root":          ".",
		"cloud_sandbox_allow_network": true,
		"human_interaction":           map[string]any{"enabled": false, "modes": []any{"form", "select"}, "fallback": "fail"},
		"completion_gate":             map[string]any{"max_retries": float64(10)},
	})

	fetched := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	assertRuntimeSettings(t, fetched.RuntimeSettings, map[string]any{
		"intervention_mode":           "approve_for_me",
		"tool_runtime":                "local_system",
		"cloud_sandbox_root":          ".",
		"cloud_sandbox_allow_network": true,
		"human_interaction":           map[string]any{"enabled": false, "modes": []any{"form", "select"}, "fallback": "fail"},
		"completion_gate":             map[string]any{"max_retries": float64(10)},
	})
	capabilities := getJSON[sessionRuntimeCapabilitiesResponse](t, server, "/v1/sessions/"+session.ID+"/runtime-capabilities")
	if capabilities.HumanInteraction.Enabled || capabilities.HumanInteraction.Fallback != "fail" || !containsString(capabilities.HumanInteraction.Modes, "form") {
		t.Fatalf("unexpected human interaction capabilities: %#v", capabilities.HumanInteraction)
	}
}

func TestSessionInterventionApproveRejectAPI(t *testing.T) {
	store := newTestStore()
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, recorder, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please read"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	continuation := json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"please read"}]},{"role":"assistant","content":[{"type":"text","text":""}],"tool_calls":[{"id":"call_read","type":"function","function":{"name":"default.read_file","arguments":{"path":"README.md"}}}]}]`)
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:            turnID,
		CallID:            "call_read",
		ToolIdentifier:    "default",
		APIName:           "read_file",
		Arguments:         json.RawMessage(`{"path":"README.md"}`),
		InterventionMode:  "request_approval",
		Reason:            "optional",
		Continuation:      continuation,
		ContinuationRound: 1,
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	approved := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/approve", `{
		"reason": "looks safe"
	}`, http.StatusOK)
	if approved.Intervention.Status != managedagents.InterventionStatusApproved {
		t.Fatalf("expected approved intervention, got %#v", approved.Intervention)
	}
	if len(approved.Events) != 1 || approved.Events[0].Type != managedagents.EventRuntimeToolInterventionApproved {
		t.Fatalf("expected only persisted decision event in response, got %#v", approved.Events)
	}
	if len(recorder.starts) != 1 || recorder.starts[0].ResumeIntervention == nil {
		t.Fatalf("expected intervention resume to be scheduled, got %#v", recorder.starts)
	}
	resume := recorder.starts[0].ResumeIntervention
	if resume.SessionID != session.ID || resume.TurnID != turnID || resume.CallID != "call_read" || resume.DecisionReason != "looks safe" {
		t.Fatalf("unexpected scheduled intervention: %#v", resume)
	}
	if string(resume.Continuation) != string(continuation) || resume.ContinuationRound != 1 {
		t.Fatalf("expected persisted continuation to reach runner, got %#v", resume)
	}

	retried := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/approve", `{
		"reason": "retry after disconnect"
	}`, http.StatusOK)
	if len(retried.Events) != 0 || len(recorder.starts) != 2 {
		t.Fatalf("expected idempotent retry to reschedule without duplicate event, response=%#v starts=%#v", retried, recorder.starts)
	}
	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete resumed turn: %v", err)
	}
	postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/approve", `{}`, http.StatusOK)
	if len(recorder.starts) != 2 {
		t.Fatalf("expected completed turn retry not to execute again, got %#v", recorder.starts)
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_read/reject", `{}`, http.StatusBadRequest)
}

func TestSessionInterventionDecisionRetriesAfterSchedulingFailure(t *testing.T) {
	store := newTestStore()
	runner := &flakyStartRunner{failures: 1}
	server := NewServerWithStoreAndRunner(store, runner, nil)
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{Name: "retry-agent", LLMProvider: "fake", LLMModel: "fake-demo"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "retry-env", Config: json.RawMessage(`{"type":"cloud"}`)})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, EnvironmentID: environment.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	events, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"content":[]}`)}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(events[1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_retry", ToolIdentifier: "default", APIName: "read_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_retry/approve", `{}`, http.StatusInternalServerError)
	retried := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_retry/approve", `{}`, http.StatusOK)
	if len(retried.Events) != 0 || len(runner.starts) != 2 || runner.starts[1].ResumeIntervention == nil {
		t.Fatalf("expected persisted decision to be resumable after scheduling failure, response=%#v starts=%#v", retried, runner.starts)
	}
}

func TestSessionClarificationResponseResumesTurn(t *testing.T) {
	store := newTestStore()
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, recorder, nil)
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{Name: "clarifying-agent", LLMProvider: "fake", LLMModel: "fake-demo"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "clarifying-env", Config: json.RawMessage(`{"type":"cloud"}`)})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, EnvironmentID: environment.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	events, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"content":[]}`)}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(events[1].Payload, "turn_id")
	request := json.RawMessage(`{"question":"Deployment?","mode":"select","choices":[{"id":"private","label":"Private"},{"id":"saas","label":"SaaS"}]}`)
	continuation := json.RawMessage(`[{"role":"assistant","tool_calls":[{"id":"call_ask","type":"function","function":{"name":"interaction.ask_user","arguments":{"question":"Deployment?","mode":"select"}}}]}]`)
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_ask", ToolIdentifier: "interaction", APIName: "ask_user",
		Kind: managedagents.InterventionKindClarification, Request: request, Arguments: request,
		InterventionMode: "request_user_input", Reason: "clarification", Continuation: continuation,
	}); err != nil {
		t.Fatalf("save clarification: %v", err)
	}

	answered := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost,
		"/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_ask/respond",
		`{"response":{"deployment":"private"}}`, http.StatusOK)
	if answered.Intervention.Status != managedagents.InterventionStatusAnswered || string(answered.Intervention.Response) != `{"deployment":"private"}` {
		t.Fatalf("unexpected clarification result: %#v", answered.Intervention)
	}
	if len(answered.Events) != 1 || answered.Events[0].Type != managedagents.EventRuntimeHumanInputSubmitted {
		t.Fatalf("expected submitted event, got %#v", answered.Events)
	}
	if len(recorder.starts) != 1 || recorder.starts[0].ResumeIntervention == nil || recorder.starts[0].ResumeIntervention.Kind != managedagents.InterventionKindClarification {
		t.Fatalf("expected clarification resume, got %#v", recorder.starts)
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPost,
		"/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_ask/approve", `{}`, http.StatusBadRequest)
}

func TestSessionPlanApprovalDecisionResumesTurn(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    string
		status    string
		eventType string
	}{
		{name: "approve", action: "approve", status: managedagents.InterventionStatusApproved, eventType: managedagents.EventRuntimePlanApprovalApproved},
		{name: "reject", action: "reject", status: managedagents.InterventionStatusRejected, eventType: managedagents.EventRuntimePlanApprovalRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore()
			recorder := &recordingRunner{}
			server := NewServerWithStoreAndRunner(store, recorder, nil)
			agent, err := store.CreateAgent(managedagents.CreateAgentInput{Name: "planning-agent", LLMProvider: "fake", LLMModel: "fake-demo"})
			if err != nil {
				t.Fatalf("create agent: %v", err)
			}
			environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "planning-env", Config: json.RawMessage(`{"type":"cloud"}`)})
			if err != nil {
				t.Fatalf("create environment: %v", err)
			}
			session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, EnvironmentID: environment.ID})
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			events, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{Type: managedagents.EventUserMessage, Payload: json.RawMessage(`{"content":[]}`)}})
			if err != nil {
				t.Fatalf("start turn: %v", err)
			}
			turnID := payloadString(events[1].Payload, "turn_id")
			request := json.RawMessage(`{"plan":{"id":"plan_000001","goal":"Ship safely","handling_mode":"planned","status":"active","items":[{"id":"item_1","index":0,"description":"Prepare","status":"pending"}]}}`)
			continuation := json.RawMessage(`[{"role":"assistant","tool_calls":[{"id":"call_plan","type":"function","function":{"name":"interaction.request_plan_approval","arguments":{"plan_id":"plan_000001"}}}]}]`)
			if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
				TurnID: turnID, CallID: "call_plan", ToolIdentifier: "interaction", APIName: "request_plan_approval",
				Kind: managedagents.InterventionKindPlanApproval, Request: request, Arguments: json.RawMessage(`{"plan_id":"plan_000001"}`),
				InterventionMode: "request_plan_approval", Reason: "plan_review", Continuation: continuation,
			}); err != nil {
				t.Fatalf("save plan approval: %v", err)
			}
			if err := store.MarkSessionTurnWaitingApproval(session.ID, turnID); err != nil {
				t.Fatalf("mark waiting approval: %v", err)
			}
			postJSONWithStatus[map[string]string](t, server, http.MethodPost,
				"/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_plan/respond", `{"response":{"answer":"yes"}}`, http.StatusBadRequest)

			decided := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost,
				"/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_plan/"+test.action,
				`{"reason":"reviewed in app"}`, http.StatusOK)
			if decided.Intervention.Status != test.status || len(decided.Events) != 1 || decided.Events[0].Type != test.eventType {
				t.Fatalf("unexpected plan decision: %#v", decided)
			}
			if len(recorder.starts) != 1 || recorder.starts[0].ResumeIntervention == nil || recorder.starts[0].ResumeIntervention.Kind != managedagents.InterventionKindPlanApproval {
				t.Fatalf("expected same-turn plan resume, got %#v", recorder.starts)
			}
		})
	}
}

func TestSessionInterventionRejectContinuesTurnWithObservation(t *testing.T) {
	store := newTestStore()
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, recorder, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:           turnID,
		CallID:           "call_edit",
		ToolIdentifier:   "default",
		APIName:          "edit_file",
		Arguments:        json.RawMessage(`{"path":"README.md","old_string":"x","new_string":"y"}`),
		InterventionMode: "request_approval",
		Reason:           "optional",
		Continuation:     json.RawMessage(`[{"role":"assistant","tool_calls":[{"id":"call_edit","type":"function","function":{"name":"default.edit_file","arguments":{"path":"README.md"}}}]}]`),
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	rejected := postJSONWithStatus[managedagents.DecideSessionInterventionResult](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_edit/reject", `{
		"reason": "unsafe edit"
	}`, http.StatusOK)
	if len(rejected.Events) != 1 || rejected.Events[0].Type != managedagents.EventRuntimeToolInterventionRejected {
		t.Fatalf("expected only persisted rejection event, got %#v", rejected.Events)
	}
	if len(recorder.starts) != 1 || recorder.starts[0].ResumeIntervention == nil {
		t.Fatalf("expected rejected continuation to be scheduled, got %#v", recorder.starts)
	}
	resume := recorder.starts[0].ResumeIntervention
	if resume.Status != managedagents.InterventionStatusRejected || resume.DecisionReason != "unsafe edit" {
		t.Fatalf("unexpected rejected resume input: %#v", resume)
	}
}

func TestGetSessionTraceProjectsTurnTimeline(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	events, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please read"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(events[1].Payload, "turn_id")
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolCall,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool call request.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file"}
		}`),
	}); err != nil {
		t.Fatalf("append tool call: %v", err)
	}
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolResult,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool result.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}
		}`),
	}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete turn: %v", err)
	}

	trace := getJSON[struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
		TraceID   string `json:"trace_id"`
		Status    string `json:"status"`
		Summary   string `json:"summary"`
		Stats     struct {
			StepCount int `json:"step_count"`
			SpanCount int `json:"span_count"`
			ToolCalls int `json:"tool_calls"`
		} `json:"stats"`
		Turns []struct {
			TurnID string `json:"turn_id"`
			Status string `json:"status"`
		} `json:"turns"`
		Graph struct {
			RootSpanIDs []string `json:"root_span_ids"`
			Edges       []struct {
				ParentSpanID string `json:"parent_span_id"`
				ChildSpanID  string `json:"child_span_id"`
			} `json:"edges"`
			CriticalSpanIDs []string `json:"critical_span_ids"`
			MaxDepth        int      `json:"max_depth"`
		} `json:"graph"`
		Steps []struct {
			Type    string `json:"type"`
			APIName string `json:"api_name"`
			Outcome string `json:"outcome"`
		} `json:"steps"`
		Spans []struct {
			Name               string   `json:"name"`
			Depth              int      `json:"depth"`
			StartOffsetMillis  int64    `json:"start_offset_ms"`
			SelfDurationMillis int64    `json:"self_duration_ms"`
			Critical           bool     `json:"critical"`
			ChildSpanIDs       []string `json:"child_span_ids"`
			Events             []struct {
				Seq  int64  `json:"seq"`
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"events"`
		} `json:"spans"`
	}](t, server, "/v1/sessions/"+session.ID+"/trace?turn_id="+turnID)
	if trace.SessionID != session.ID || trace.TurnID != turnID {
		t.Fatalf("unexpected trace identity: %+v", trace)
	}
	if trace.TraceID == "" || len(trace.Spans) == 0 || trace.Spans[0].Name != "tma.interaction" {
		t.Fatalf("expected span trace projection, got %+v", trace)
	}
	if len(trace.Spans[0].ChildSpanIDs) == 0 || len(trace.Spans[0].Events) == 0 {
		t.Fatalf("expected span tree details, got %+v", trace.Spans[0])
	}
	if len(trace.Graph.RootSpanIDs) == 0 || len(trace.Graph.Edges) == 0 || len(trace.Graph.CriticalSpanIDs) == 0 || trace.Graph.MaxDepth == 0 {
		t.Fatalf("expected trace graph metadata, got %+v", trace.Graph)
	}
	if !trace.Spans[0].Critical || trace.Spans[0].SelfDurationMillis < 0 || trace.Spans[1].Depth == 0 || trace.Spans[1].StartOffsetMillis < 0 {
		t.Fatalf("expected span waterfall annotations, got %+v", trace.Spans)
	}
	if trace.Status != managedagents.TurnStatusCompleted || trace.Stats.StepCount < 4 || trace.Stats.ToolCalls != 1 {
		t.Fatalf("expected projected trace stats, got %+v", trace)
	}
	if len(trace.Turns) != 1 || trace.Turns[0].TurnID != turnID || trace.Turns[0].Status != managedagents.TurnStatusCompleted {
		t.Fatalf("expected projected turn catalog, got %+v", trace.Turns)
	}
	if !strings.Contains(trace.Summary, "tool result: default.read_file success") {
		t.Fatalf("expected projected summary to mention tool result, got %q", trace.Summary)
	}
	if len(trace.Steps) < 4 {
		t.Fatalf("expected projected steps, got %+v", trace.Steps)
	}
	indexedTrace, err := store.GetTraceIndex(trace.TraceID)
	if err != nil {
		t.Fatalf("expected trace index to be persisted: %v", err)
	}
	if indexedTrace.SessionID != session.ID || indexedTrace.TurnID != turnID || indexedTrace.SpanCount != len(trace.Spans) {
		t.Fatalf("unexpected persisted trace index: %+v", indexedTrace)
	}
	indexedSpans, err := store.ListTraceSpanIndexes(managedagents.ListTraceSpanIndexInput{TraceID: trace.TraceID, Limit: 20})
	if err != nil {
		t.Fatalf("list trace span indexes: %v", err)
	}
	if len(indexedSpans) != len(trace.Spans) {
		t.Fatalf("expected persisted span index entries, got %d want %d", len(indexedSpans), len(trace.Spans))
	}

	perfetto := getJSON[map[string]any](t, server, "/v1/sessions/"+session.ID+"/trace?turn_id="+turnID+"&format=perfetto")
	if _, ok := perfetto["traceEvents"]; !ok {
		t.Fatalf("expected perfetto traceEvents, got %+v", perfetto)
	}
	otel := getJSON[map[string]any](t, server, "/v1/sessions/"+session.ID+"/trace?turn_id="+turnID+"&format=otel")
	if _, ok := otel["resourceSpans"]; !ok {
		t.Fatalf("expected otel resourceSpans, got %+v", otel)
	}

	catalog := getJSON[struct {
		Traces []struct {
			TraceID   string `json:"trace_id"`
			SessionID string `json:"session_id"`
			TurnID    string `json:"turn_id"`
			SpanCount int    `json:"span_count"`
		} `json:"traces"`
		Limit      int  `json:"limit"`
		Offset     int  `json:"offset"`
		NextOffset int  `json:"next_offset"`
		HasMore    bool `json:"has_more"`
	}](t, server, "/v1/traces?limit=10")
	if len(catalog.Traces) == 0 || catalog.Traces[0].TraceID != trace.TraceID || catalog.Traces[0].SessionID != session.ID || catalog.Traces[0].TurnID != turnID || catalog.Traces[0].SpanCount == 0 {
		t.Fatalf("expected trace catalog entry, got %+v", catalog.Traces)
	}
	if catalog.Limit != 10 || catalog.Offset != 0 || catalog.NextOffset != len(catalog.Traces) {
		t.Fatalf("expected trace catalog pagination metadata, got %+v", catalog)
	}
	filteredCatalog := getJSON[struct {
		Traces []struct {
			TraceID   string `json:"trace_id"`
			SessionID string `json:"session_id"`
			TurnID    string `json:"turn_id"`
		} `json:"traces"`
	}](t, server, "/v1/traces?session_id="+session.ID+"&limit=10")
	if len(filteredCatalog.Traces) != 1 || filteredCatalog.Traces[0].TraceID != trace.TraceID || filteredCatalog.Traces[0].SessionID != session.ID || filteredCatalog.Traces[0].TurnID != turnID {
		t.Fatalf("expected session-filtered trace catalog entry, got %+v", filteredCatalog.Traces)
	}
	direct := getJSON[struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
		TraceID   string `json:"trace_id"`
	}](t, server, "/v1/traces/"+trace.TraceID)
	if direct.SessionID != session.ID || direct.TurnID != turnID || direct.TraceID != trace.TraceID {
		t.Fatalf("expected direct trace lookup, got %+v", direct)
	}

	spans := getJSON[struct {
		Spans []struct {
			TraceID            string `json:"trace_id"`
			SessionID          string `json:"session_id"`
			TurnID             string `json:"turn_id"`
			SpanID             string `json:"span_id"`
			Name               string `json:"name"`
			Kind               string `json:"kind"`
			Depth              int    `json:"depth"`
			SelfDurationMillis int64  `json:"self_duration_ms"`
			Critical           bool   `json:"critical"`
		} `json:"spans"`
		KindCounts     map[string]int `json:"kind_counts"`
		CriticalCounts map[string]int `json:"critical_counts"`
		Limit          int            `json:"limit"`
		Offset         int            `json:"offset"`
		NextOffset     int            `json:"next_offset"`
		HasMore        bool           `json:"has_more"`
	}](t, server, "/v1/spans?q=read_file&limit=10")
	if len(spans.Spans) == 0 || spans.Spans[0].TraceID != trace.TraceID || spans.Spans[0].SessionID != session.ID || spans.Spans[0].TurnID != turnID {
		t.Fatalf("expected span search result, got %+v", spans.Spans)
	}
	if spans.KindCounts["tool"] == 0 {
		t.Fatalf("expected span kind aggregate, got %+v", spans.KindCounts)
	}
	if spans.CriticalCounts["true"] == 0 {
		t.Fatalf("expected critical span aggregate, got %+v", spans.CriticalCounts)
	}
	if spans.Spans[0].SpanID == "" {
		t.Fatalf("expected span search result to include span_id, got %+v", spans.Spans[0])
	}
	if spans.Limit != 10 || spans.Offset != 0 || spans.NextOffset != len(spans.Spans) {
		t.Fatalf("expected span catalog pagination metadata, got %+v", spans)
	}
	pagedSpans := getJSON[struct {
		Spans []struct {
			SpanID string `json:"span_id"`
		} `json:"spans"`
		Limit      int  `json:"limit"`
		Offset     int  `json:"offset"`
		NextOffset int  `json:"next_offset"`
		HasMore    bool `json:"has_more"`
	}](t, server, "/v1/spans?session_id="+session.ID+"&turn_id="+turnID+"&limit=1")
	if len(pagedSpans.Spans) != 1 || pagedSpans.Limit != 1 || pagedSpans.Offset != 0 || pagedSpans.NextOffset != 1 || !pagedSpans.HasMore {
		t.Fatalf("expected first span page with has_more, got %+v", pagedSpans)
	}
	nextSpans := getJSON[struct {
		Spans []struct {
			SpanID string `json:"span_id"`
		} `json:"spans"`
		Offset int `json:"offset"`
	}](t, server, "/v1/spans?session_id="+session.ID+"&turn_id="+turnID+"&limit=1&offset=1")
	if len(nextSpans.Spans) != 1 || nextSpans.Offset != 1 || nextSpans.Spans[0].SpanID == pagedSpans.Spans[0].SpanID {
		t.Fatalf("expected second span page with different span, got first=%+v second=%+v", pagedSpans, nextSpans)
	}
	v2Traces := getJSON[struct {
		Items []struct {
			TraceID string `json:"trace_id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}](t, server, "/v2/traces?session_id="+session.ID+"&limit=10")
	if len(v2Traces.Items) != 1 || v2Traces.Items[0].TraceID != trace.TraceID || v2Traces.NextCursor != "" || v2Traces.HasMore {
		t.Fatalf("expected v2 trace cursor page, got %+v", v2Traces)
	}
	v2Spans := getJSON[struct {
		Items []struct {
			SpanID string `json:"span_id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}](t, server, "/v2/spans?session_id="+session.ID+"&turn_id="+turnID+"&limit=1")
	if len(v2Spans.Items) != 1 || v2Spans.NextCursor == "" || !v2Spans.HasMore {
		t.Fatalf("expected first v2 span cursor page, got %+v", v2Spans)
	}
	v2NextSpans := getJSON[struct {
		Items []struct {
			SpanID string `json:"span_id"`
		} `json:"items"`
	}](t, server, "/v2/spans?session_id="+session.ID+"&turn_id="+turnID+"&limit=1&cursor="+url.QueryEscape(v2Spans.NextCursor))
	if len(v2NextSpans.Items) != 1 || v2NextSpans.Items[0].SpanID == v2Spans.Items[0].SpanID {
		t.Fatalf("expected second v2 span cursor page, got first=%+v second=%+v", v2Spans, v2NextSpans)
	}
	invalidCursorRequest := httptest.NewRequest(http.MethodGet, "/v2/spans?session_id=another&limit=1&cursor="+url.QueryEscape(v2Spans.NextCursor), nil)
	invalidCursorResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidCursorResponse, invalidCursorRequest)
	if invalidCursorResponse.Code != http.StatusBadRequest || !strings.Contains(invalidCursorResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("expected filter-bound cursor rejection, got %d %s", invalidCursorResponse.Code, invalidCursorResponse.Body.String())
	}
	criticalSpans := getJSON[struct {
		Spans []struct {
			TraceID   string `json:"trace_id"`
			SessionID string `json:"session_id"`
			TurnID    string `json:"turn_id"`
			Critical  bool   `json:"critical"`
		} `json:"spans"`
	}](t, server, "/v1/spans?trace_id="+trace.TraceID+"&session_id="+session.ID+"&turn_id="+turnID+"&critical=true&min_duration_ms=0&limit=10")
	if len(criticalSpans.Spans) == 0 {
		t.Fatalf("expected critical span search results, got %+v", criticalSpans.Spans)
	}
	for _, span := range criticalSpans.Spans {
		if span.TraceID != trace.TraceID || span.SessionID != session.ID || span.TurnID != turnID || !span.Critical {
			t.Fatalf("expected filtered critical span, got %+v", span)
		}
	}
	spanDetail := getJSON[struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
		TraceID   string `json:"trace_id"`
		Span      struct {
			SpanID     string            `json:"span_id"`
			Name       string            `json:"name"`
			Kind       string            `json:"kind"`
			Attributes map[string]string `json:"attributes"`
			Events     []struct {
				Seq  int64  `json:"seq"`
				Type string `json:"type"`
			} `json:"events"`
		} `json:"span"`
	}](t, server, "/v1/traces/"+trace.TraceID+"/spans/"+spans.Spans[0].SpanID)
	if spanDetail.SessionID != session.ID || spanDetail.TurnID != turnID || spanDetail.TraceID != trace.TraceID {
		t.Fatalf("expected span detail trace identity, got %+v", spanDetail)
	}
	if spanDetail.Span.SpanID != spans.Spans[0].SpanID || spanDetail.Span.Kind != "tool" || spanDetail.Span.Attributes["tool_api"] != "read_file" || len(spanDetail.Span.Events) == 0 {
		t.Fatalf("expected detailed tool span with events and attributes, got %+v", spanDetail.Span)
	}
	pruned, err := store.PruneTraceIndexes(managedagents.PruneTraceIndexInput{Before: time.Now().UTC().Add(time.Hour), Limit: 10})
	if err != nil {
		t.Fatalf("prune trace indexes: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected one pruned trace index, got %d", pruned)
	}
	if _, err := store.GetTraceIndex(trace.TraceID); !errors.Is(err, managedagents.ErrNotFound) {
		t.Fatalf("expected trace index pruned, got %v", err)
	}
}

func TestMetricsEndpointAndInspectorPage(t *testing.T) {
	store := newTestStore()
	testRunner := mcpStatsTestRunner{
		Runner: runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		stats: mcp.StdioHostStats{
			Sessions: 2, InUseSessions: 1, MaxSessions: 8, IdleTimeoutSeconds: 120,
			StartsTotal: 3, RejectionsTotal: 1, ToolsListChangedTotal: 2, ProgressNotificationsTotal: 4, LogMessagesTotal: 2, InvalidNotificationsTotal: 1,
			LogMessagesByLevel: map[string]int64{"info": 2},
		},
		httpStats: mcp.StreamableHTTPHostStats{
			Sessions: 1, InUseSessions: 1, MaxSessions: 12, IdleTimeoutSeconds: 180,
			StartsTotal: 2, ToolsListChangedTotal: 1, ProgressNotificationsTotal: 3, LogMessagesTotal: 1,
			LogMessagesByLevel: map[string]int64{"warning": 1},
		},
	}
	server := NewServerWithStoreAndRunner(store, testRunner, nil)

	if _, err := store.RecordLLMUsage(managedagents.RecordLLMUsageInput{
		WorkspaceID:        managedagents.DefaultWorkspaceID,
		AgentID:            "agt_000001",
		AgentConfigVersion: 1,
		SessionID:          "sesn_000001",
		TurnID:             "turn_000001",
		ProviderID:         "fake",
		Model:              "fake-demo",
		InputTokens:        5,
		OutputTokens:       7,
		TotalTokens:        12,
		LatencyMillis:      99,
		Status:             "completed",
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if _, err := store.RegisterWorker(managedagents.RegisterWorkerInput{
		Name:       "local-worker",
		WorkerType: managedagents.WorkerTypeLocal,
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Inspector Agent",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "inspector-env",
		"config": {"type":"cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
	}})
	if err != nil {
		t.Fatalf("append user event: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolCall,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool call request.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file"}
		}`),
	}); err != nil {
		t.Fatalf("append tool call: %v", err)
	}
	if _, err := store.AppendRuntimeEvent(session.ID, turnID, managedagents.AppendEventInput{
		Type: managedagents.EventRuntimeToolResult,
		Payload: json.RawMessage(`{
			"turn_id":"` + turnID + `",
			"message":"Received tool result.",
			"data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}
		}`),
	}); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if _, err := store.CompleteSessionTurn(session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete turn: %v", err)
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResponse := httptest.NewRecorder()
	server.ServeHTTP(metricsResponse, metricsRequest)
	if metricsResponse.Code != http.StatusOK {
		t.Fatalf("metrics expected status 200, got %d: %s", metricsResponse.Code, metricsResponse.Body.String())
	}
	metrics := metricsResponse.Body.String()
	if !strings.Contains(metrics, `tma_llm_tokens_total{kind="total",model="fake-demo",provider="fake"} 12`) {
		t.Fatalf("expected metrics token total, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_workers_total{status="online",type="local"} 1`) {
		t.Fatalf("expected worker gauge, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_mcp_stdio_host_sessions 2`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_in_use_sessions 1`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_max_sessions 8`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_events_total{event="start"} 3`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_events_total{event="tools_list_changed"} 2`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_notifications_total{type="progress"} 4`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_log_messages_total{level="info"} 2`) ||
		!strings.Contains(metrics, `tma_mcp_stdio_host_events_total{event="reject"} 1`) {
		t.Fatalf("expected MCP host metrics, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_mcp_streamable_http_host_sessions 1`) ||
		!strings.Contains(metrics, `tma_mcp_streamable_http_host_in_use_sessions 1`) ||
		!strings.Contains(metrics, `tma_mcp_streamable_http_host_max_sessions 12`) ||
		!strings.Contains(metrics, `tma_mcp_streamable_http_host_events_total{event="start"} 2`) ||
		!strings.Contains(metrics, `tma_mcp_streamable_http_host_notifications_total{type="progress"} 3`) ||
		!strings.Contains(metrics, `tma_mcp_streamable_http_host_log_messages_total{level="warning"} 1`) ||
		!strings.Contains(metrics, `tma_mcp_streamable_http_host_events_total{event="tools_list_changed"} 1`) {
		t.Fatalf("expected MCP HTTP host metrics, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_subagent_status_total{status="queued"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_status_total{status="running"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_status_total{status="rejected"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_wait_seconds 0`) {
		t.Fatalf("expected subagent gauges, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_subagent_group_status_total{status="pending"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_status_total{status="running"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_status_total{status="completed"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_status_total{status="failed"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_status_total{status="canceled"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_items_total{status="created"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_items_total{status="started"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_items_total{status="queued"} 0`) ||
		!strings.Contains(metrics, `tma_subagent_group_items_total{status="rejected"} 0`) {
		t.Fatalf("expected task group gauges, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `tma_observability_exporter_enabled{exporter="perfetto"}`) ||
		!strings.Contains(metrics, `tma_observability_exporter_sample_rate 1`) ||
		!strings.Contains(metrics, `tma_observability_exporter_last_attempt_timestamp_seconds{exporter="otlp"}`) {
		t.Fatalf("expected observability exporter metrics, got:\n%s", metrics)
	}
	sessionMetricsRequest := httptest.NewRequest(http.MethodGet, "/metrics?session_id="+session.ID+"&turn_id="+turnID, nil)
	sessionMetricsResponse := httptest.NewRecorder()
	server.ServeHTTP(sessionMetricsResponse, sessionMetricsRequest)
	if sessionMetricsResponse.Code != http.StatusOK {
		t.Fatalf("session metrics expected status 200, got %d: %s", sessionMetricsResponse.Code, sessionMetricsResponse.Body.String())
	}
	sessionMetrics := sessionMetricsResponse.Body.String()
	for _, expected := range []string{
		`tma_session_events_total{event_type="runtime.tool_call",session_id="` + session.ID + `"} 1`,
		`tma_trace_steps_total{session_id="` + session.ID + `",turn_id="` + turnID + `"} 6`,
		`tma_trace_critical_path_duration_milliseconds{session_id="` + session.ID + `",status="completed",turn_id="` + turnID + `"}`,
		`tma_trace_max_span_depth{session_id="` + session.ID + `",turn_id="` + turnID + `"}`,
		`tma_trace_critical_spans_total{session_id="` + session.ID + `",turn_id="` + turnID + `"}`,
		`tma_tool_calls_total{api_name="read_file",outcome="success",session_id="` + session.ID + `",tool_identifier="default",turn_id="` + turnID + `"} 1`,
	} {
		if !strings.Contains(sessionMetrics, expected) {
			t.Fatalf("expected session metrics to contain %q, got:\n%s", expected, sessionMetrics)
		}
	}

	inspectorRequest := httptest.NewRequest(http.MethodGet, "/inspector", nil)
	inspectorResponse := httptest.NewRecorder()
	server.ServeHTTP(inspectorResponse, inspectorRequest)
	if inspectorResponse.Code != http.StatusOK {
		t.Fatalf("inspector expected status 200, got %d: %s", inspectorResponse.Code, inspectorResponse.Body.String())
	}
	if contentType := inspectorResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("expected html content type, got %q", contentType)
	}
	if body := inspectorResponse.Body.String(); !strings.Contains(body, "TMA Inspector") ||
		!strings.Contains(body, `href="/inspector/assets/styles.css"`) ||
		!strings.Contains(body, `type="module" crossorigin src="/inspector/assets/app.js"`) ||
		!strings.Contains(body, `id="root"`) {
		t.Fatalf("expected React inspector shell, got %q", body)
	}
	appRequest := httptest.NewRequest(http.MethodGet, "/app", nil)
	appResponse := httptest.NewRecorder()
	server.ServeHTTP(appResponse, appRequest)
	if appResponse.Code != http.StatusOK {
		t.Fatalf("app expected status 200, got %d: %s", appResponse.Code, appResponse.Body.String())
	}
	if body := appResponse.Body.String(); !strings.Contains(body, "TMA Workbench") ||
		!strings.Contains(body, `href="/app/assets/styles.css"`) ||
		!strings.Contains(body, `type="module" crossorigin src="/app/assets/app.js"`) ||
		!strings.Contains(body, `id="root"`) {
		t.Fatalf("expected React app shell, got %q", body)
	}
	appJSRequest := httptest.NewRequest(http.MethodGet, "/app/assets/app.js", nil)
	appJSResponse := httptest.NewRecorder()
	server.ServeHTTP(appJSResponse, appJSRequest)
	if appJSResponse.Code != http.StatusOK {
		t.Fatalf("app app.js expected status 200, got %d: %s", appJSResponse.Code, appJSResponse.Body.String())
	}
	if contentType := appJSResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("expected app javascript content type, got %q", contentType)
	}
	if appJS := appJSResponse.Body.String(); !strings.Contains(appJS, "TMA Workbench") ||
		!strings.Contains(appJS, "starter-grid") ||
		!strings.Contains(appJS, "sendSessionMessage") ||
		!strings.Contains(appJS, "Package Security") ||
		!strings.Contains(appJS, "Marketplace Policy") ||
		!strings.Contains(appJS, "市场管理") ||
		!strings.Contains(appJS, "提交审核") ||
		!strings.Contains(appJS, "让 TMA 帮你构建、检查、修改或执行某项工作") ||
		strings.Contains(appJS, "TMA Inspector") ||
		strings.Contains(appJS, "Trace ID") {
		t.Fatalf("expected standalone user app bundle, got %q", appJS)
	}
	inspectorJSRequest := httptest.NewRequest(http.MethodGet, "/inspector/assets/app.js", nil)
	inspectorJSResponse := httptest.NewRecorder()
	server.ServeHTTP(inspectorJSResponse, inspectorJSRequest)
	if inspectorJSResponse.Code != http.StatusOK {
		t.Fatalf("inspector app.js expected status 200, got %d: %s", inspectorJSResponse.Code, inspectorJSResponse.Body.String())
	}
	if contentType := inspectorJSResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("expected javascript content type, got %q", contentType)
	}
	if appJS := inspectorJSResponse.Body.String(); !strings.Contains(appJS, "React") ||
		!strings.Contains(appJS, "createRoot") ||
		!strings.Contains(appJS, "function App") ||
		!strings.Contains(appJS, "TMAInspectorAPI") ||
		!strings.Contains(appJS, "TMAInspectorUtils") ||
		!strings.Contains(appJS, "Turns") ||
		!strings.Contains(appJS, "Session Traces") ||
		!strings.Contains(appJS, "1. Agent") ||
		!strings.Contains(appJS, "2. Session") ||
		!strings.Contains(appJS, "3. Trace") ||
		!strings.Contains(appJS, "Session Span Search") ||
		!strings.Contains(appJS, "globalSpanKind") ||
		!strings.Contains(appJS, "globalSpanCritical") ||
		!strings.Contains(appJS, "globalSpanMinDuration") ||
		!strings.Contains(appJS, "Spans") ||
		!strings.Contains(appJS, "Waterfall") ||
		!strings.Contains(appJS, "Select a span to inspect events and attributes.") ||
		!strings.Contains(appJS, "spanFilter") ||
		!strings.Contains(appJS, "spanKind") ||
		!strings.Contains(appJS, "Artifact Preview") ||
		!strings.Contains(appJS, "Context Coverage") ||
		!strings.Contains(appJS, "Plan History") ||
		!strings.Contains(appJS, "taskPlanHistory") ||
		!strings.Contains(appJS, "Evidence") ||
		!strings.Contains(appJS, "Completion Quality") ||
		!strings.Contains(appJS, "completionQualitySummary") ||
		!strings.Contains(appJS, "Retry rate") ||
		!strings.Contains(appJS, "Context Budget") ||
		!strings.Contains(appJS, "Exporters") ||
		!strings.Contains(appJS, "Auto refresh every 5s") ||
		!strings.Contains(appJS, "traceCatalog") ||
		!strings.Contains(appJS, "spanCatalog") ||
		!strings.Contains(appJS, "observabilityStatus") ||
		!strings.Contains(appJS, "loadTraceCatalog") ||
		!strings.Contains(appJS, "inspectorHashParams") ||
		!strings.Contains(appJS, "syncInspectorHash") ||
		!strings.Contains(appJS, "bootInspectorFromHash") ||
		!strings.Contains(appJS, "hashchange") ||
		!strings.Contains(appJS, "URLSearchParams") ||
		!strings.Contains(appJS, "data-trace-id") ||
		!strings.Contains(appJS, "loadTraceByID") ||
		!strings.Contains(appJS, "loadTrace") ||
		!strings.Contains(appJS, "SpanSearch") ||
		!strings.Contains(appJS, "critical_counts") ||
		!strings.Contains(appJS, "globalSpanKind") ||
		!strings.Contains(appJS, "globalSpanCritical") ||
		!strings.Contains(appJS, "self_duration_ms") ||
		!strings.Contains(appJS, "data-span-trace-id") ||
		!strings.Contains(appJS, "data-span-id") ||
		!strings.Contains(appJS, "loadSpanByID") ||
		!strings.Contains(appJS, "Waterfall") ||
		!strings.Contains(appJS, "start_offset_ms") ||
		!strings.Contains(appJS, "critical_path_duration_ms") ||
		!strings.Contains(appJS, "data-waterfall-span") ||
		!strings.Contains(appJS, "data-span") ||
		!strings.Contains(appJS, "data-span-select") ||
		!strings.Contains(appJS, "data-preview") ||
		!strings.Contains(appJS, "previewArtifact") ||
		!strings.Contains(appJS, "source_until_seq") ||
		!strings.Contains(appJS, "unsummarized events") ||
		!strings.Contains(appJS, "ContextCoverage") ||
		!strings.Contains(appJS, "renderContextBudget") ||
		!strings.Contains(appJS, "context_budget") ||
		!strings.Contains(appJS, "pinned_context_included") ||
		!strings.Contains(appJS, "runtime.context_compacting") ||
		!strings.Contains(appJS, "runtime.tool_result") ||
		!strings.Contains(appJS, "MCP Protocol") ||
		!strings.Contains(appJS, "payload values redacted") ||
		!strings.Contains(appJS, "collectMCPProtocolOperations") ||
		!strings.Contains(appJS, "Sampling") ||
		!strings.Contains(appJS, "sample_rate") ||
		!strings.Contains(appJS, "Retry due exporters") ||
		!strings.Contains(appJS, "OTLP HTTP") ||
		!strings.Contains(appJS, "Recent exporter runs") ||
		!strings.Contains(appJS, "No persisted exporter runs.") ||
		!strings.Contains(appJS, "last success") ||
		!strings.Contains(appJS, "last failure") ||
		!strings.Contains(appJS, "No exporter attempts recorded.") ||
		!strings.Contains(appJS, "Copy CLI") ||
		!strings.Contains(appJS, "data-copy") {
		t.Fatalf("expected inspector app.js behavior, got %q", appJS)
	}
	inspectorCSSRequest := httptest.NewRequest(http.MethodGet, "/inspector/assets/styles.css", nil)
	inspectorCSSResponse := httptest.NewRecorder()
	server.ServeHTTP(inspectorCSSResponse, inspectorCSSRequest)
	if inspectorCSSResponse.Code != http.StatusOK {
		t.Fatalf("inspector styles.css expected status 200, got %d: %s", inspectorCSSResponse.Code, inspectorCSSResponse.Body.String())
	}
	if contentType := inspectorCSSResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "text/css") {
		t.Fatalf("expected css content type, got %q", contentType)
	}
	if styles := inspectorCSSResponse.Body.String(); !strings.Contains(styles, ".span-controls") ||
		!strings.Contains(styles, ".span-search-controls") ||
		!strings.Contains(styles, ".waterfall-row") ||
		!strings.Contains(styles, ".waterfall-bar.critical") ||
		!strings.Contains(styles, ".coverage-grid") ||
		!strings.Contains(styles, ".budget-grid") ||
		!strings.Contains(styles, ".budget-bar") ||
		!strings.Contains(styles, ".mcp-protocol-summary") ||
		!strings.Contains(styles, ".mcp-operation-lifecycle") ||
		!strings.Contains(styles, ".task-plan-history") ||
		!strings.Contains(styles, ".task-plan-evidence") ||
		!strings.Contains(styles, ".preview-media") ||
		!strings.Contains(styles, ".health-line") {
		t.Fatalf("expected inspector styles, got %q", styles)
	}
}

func TestObservabilityStatusEndpoint(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	t.Setenv("TMA_PERFETTO_DIR", "/tmp/tma-traces")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_TOKEN", "secret-token")
	t.Setenv("TMA_OBSERVABILITY_SAMPLE_RATE", "0.25")
	store := newTestStore()
	if _, err := store.RecordObservabilityExporterRun(managedagents.RecordObservabilityExporterRunInput{
		Exporter:    managedagents.ObservabilityExporterPerfetto,
		Status:      managedagents.ObservabilityExporterRunSucceeded,
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		TraceID:     "trace_test",
		Destination: "/tmp/tma-traces/turn_000001.perfetto.json",
		Message:     "exported",
		StartedAt:   time.Unix(100, 0).UTC(),
		FinishedAt:  time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatalf("record exporter run: %v", err)
	}
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	response := getJSON[struct {
		Perfetto struct {
			Enabled     bool   `json:"enabled"`
			Destination string `json:"destination"`
			LastSuccess *struct {
				SessionID string `json:"session_id"`
				TurnID    string `json:"turn_id"`
				TraceID   string `json:"trace_id"`
			} `json:"last_success"`
		} `json:"perfetto"`
		OTLP struct {
			Enabled       bool   `json:"enabled"`
			Destination   string `json:"destination"`
			TokenProvided bool   `json:"token_provided"`
		} `json:"otlp"`
		Sampling struct {
			Enabled    bool    `json:"enabled"`
			SampleRate float64 `json:"sample_rate"`
			Configured bool    `json:"configured"`
		} `json:"sampling"`
		Retry struct {
			Enabled     bool `json:"enabled"`
			MaxAttempts int  `json:"max_attempts"`
		} `json:"retry"`
		RecentRuns []managedagents.ObservabilityExporterRun `json:"recent_runs"`
	}](t, server, "/v1/observability/status")
	if !response.Perfetto.Enabled || response.Perfetto.Destination != "/tmp/tma-traces" {
		t.Fatalf("unexpected perfetto status: %+v", response.Perfetto)
	}
	if response.Perfetto.LastSuccess == nil || response.Perfetto.LastSuccess.TraceID != "trace_test" {
		t.Fatalf("expected persisted perfetto last_success, got %+v", response.Perfetto.LastSuccess)
	}
	if len(response.RecentRuns) != 1 || response.RecentRuns[0].Exporter != managedagents.ObservabilityExporterPerfetto {
		t.Fatalf("expected recent exporter runs, got %+v", response.RecentRuns)
	}
	if !response.OTLP.Enabled || response.OTLP.Destination != "http://collector.test/v1/traces" || !response.OTLP.TokenProvided {
		t.Fatalf("unexpected otlp status: %+v", response.OTLP)
	}
	if !response.Sampling.Enabled || response.Sampling.SampleRate != 0.25 || !response.Sampling.Configured {
		t.Fatalf("unexpected sampling status: %+v", response.Sampling)
	}
	if !response.Retry.Enabled || response.Retry.MaxAttempts != 3 {
		t.Fatalf("unexpected retry status: %+v", response.Retry)
	}
}

func TestObservabilityRetryEndpoint(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	traceDir := t.TempDir()
	t.Setenv("TMA_PERFETTO_DIR", traceDir)
	store := newTestStore()
	store.sessions["sesn_retry"] = managedagents.Session{
		ID:                 "sesn_retry",
		WorkspaceID:        managedagents.DefaultWorkspaceID,
		AgentID:            "agt_retry",
		AgentConfigVersion: 1,
		EnvironmentID:      "env_retry",
		Status:             managedagents.SessionStatusIdle,
		CreatedAt:          time.Now().Add(-5 * time.Minute).UTC(),
	}
	store.events["sesn_retry"] = []managedagents.Event{
		{
			ID:        "evt_retry_1",
			Seq:       1,
			SessionID: "sesn_retry",
			Type:      managedagents.EventUserMessage,
			Payload:   json.RawMessage(`{"turn_id":"turn_retry","content":[{"type":"text","text":"retry"}]}`),
			CreatedAt: time.Now().Add(-3 * time.Minute).UTC(),
		},
	}
	nextRetry := time.Now().Add(-time.Minute).UTC()
	if _, err := store.RecordObservabilityExporterRun(managedagents.RecordObservabilityExporterRunInput{
		Exporter:     managedagents.ObservabilityExporterPerfetto,
		Status:       managedagents.ObservabilityExporterRunFailed,
		SessionID:    "sesn_retry",
		TurnID:       "turn_retry",
		TraceID:      "trace_retry",
		Destination:  traceDir,
		Message:      "write failed",
		AttemptCount: 1,
		NextRetryAt:  &nextRetry,
		StartedAt:    time.Now().Add(-2 * time.Minute).UTC(),
		FinishedAt:   time.Now().Add(-2 * time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("record exporter run: %v", err)
	}
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	request := httptest.NewRequest(http.MethodPost, "/v1/observability/retry", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("retry expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Attempted int `json:"attempted"`
		Succeeded int `json:"succeeded"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if result.Attempted != 1 || result.Succeeded != 1 {
		runs, _ := store.ListObservabilityExporterRuns(managedagents.ListObservabilityExporterRunsInput{
			Exporter:  managedagents.ObservabilityExporterPerfetto,
			SessionID: "sesn_retry",
			TurnID:    "turn_retry",
			Limit:     3,
		})
		t.Fatalf("expected successful retry attempt, got %+v runs=%+v", result, runs)
	}
	runs, err := store.ListObservabilityExporterRuns(managedagents.ListObservabilityExporterRunsInput{
		Exporter:  managedagents.ObservabilityExporterPerfetto,
		SessionID: "sesn_retry",
		TurnID:    "turn_retry",
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("list retry runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != managedagents.ObservabilityExporterRunSucceeded || runs[0].AttemptCount != 2 {
		t.Fatalf("expected retry attempt to be persisted, got %+v", runs)
	}
}

func TestSessionInterventionDecisionUsesRunnerLifecycleContext(t *testing.T) {
	store := newTestStore()
	recorder := &contextRecordingRunner{}
	server := NewServerWithStoreAndRunner(store, recorder, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:           turnID,
		CallID:           "call_edit",
		ToolIdentifier:   "default",
		APIName:          "edit_file",
		Arguments:        json.RawMessage(`{"path":"README.md"}`),
		InterventionMode: "request_approval",
		Continuation:     json.RawMessage(`[{"role":"assistant","tool_calls":[{"id":"call_edit","type":"function","function":{"name":"default.edit_file","arguments":{"path":"README.md"}}}]}]`),
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/interventions/"+turnID+"/call_edit/approve", strings.NewReader(`{"reason":"approved"}`))
	request.Header.Set("Content-Type", "application/json")
	requestContext, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(requestContext)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected decision to survive canceled request context, got %d: %s", response.Code, response.Body.String())
	}
	if recorder.contextErr != nil {
		t.Fatalf("expected runner lifecycle context, got request cancellation: %v", recorder.contextErr)
	}
	if len(recorder.starts) != 1 || recorder.starts[0].ResumeIntervention == nil {
		t.Fatalf("expected resume scheduling, got %#v", recorder.starts)
	}
}

func TestUserMessageWhileWaitingApprovalReturnsReminder(t *testing.T) {
	store := newTestStore()
	runner := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, runner, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	startEvents, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(startEvents[1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID:           turnID,
		CallID:           "call_edit",
		ToolIdentifier:   "default",
		APIName:          "edit_file",
		Arguments:        json.RawMessage(`{"path":"README.md","old_string":"x","new_string":"y"}`),
		InterventionMode: "request_approval",
		Reason:           "optional",
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	response := postJSONWithStatus[struct {
		Events []managedagents.Event `json:"events"`
	}](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"hello?"}]}}]
	}`, http.StatusAccepted)
	if len(response.Events) != 2 || response.Events[0].Type != managedagents.EventAgentMessage || response.Events[1].Type != managedagents.EventRuntimeToolInterventionRequired {
		t.Fatalf("expected reminder agent message and reissued approval event, got %#v", response.Events)
	}
	if len(runner.starts) != 0 {
		t.Fatalf("expected reminder not to start a new turn, got %#v", runner.starts)
	}
}

func TestGetSessionLLMUsageIncludesSummaryAndRecords(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	_, err := store.RecordLLMUsage(managedagents.RecordLLMUsageInput{
		WorkspaceID:        session.WorkspaceID,
		AgentID:            agent.ID,
		AgentConfigVersion: session.AgentConfigVersion,
		SessionID:          session.ID,
		TurnID:             "turn_000001",
		ProviderID:         "fake",
		ProviderType:       "fake",
		Model:              "fake-demo",
		InputTokens:        10,
		OutputTokens:       5,
		TotalTokens:        15,
		CachedInputTokens:  2,
		ReasoningTokens:    1,
		LatencyMillis:      120,
		Status:             "completed",
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}
	_, err = store.RecordLLMUsage(managedagents.RecordLLMUsageInput{
		WorkspaceID:        session.WorkspaceID,
		AgentID:            agent.ID,
		AgentConfigVersion: session.AgentConfigVersion,
		SessionID:          session.ID,
		TurnID:             "turn_000002",
		ProviderID:         "fake",
		ProviderType:       "fake",
		Model:              "fake-demo",
		InputTokens:        7,
		OutputTokens:       3,
		TotalTokens:        10,
		LatencyMillis:      80,
		Status:             "completed",
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}

	report := getJSON[managedagents.LLMUsageReport](t, server, "/v1/sessions/"+session.ID+"/usage")
	if report.SessionID != session.ID {
		t.Fatalf("expected session_id %q, got %q", session.ID, report.SessionID)
	}
	if report.Summary.RecordCount != 2 || report.Summary.InputTokens != 17 || report.Summary.OutputTokens != 8 || report.Summary.TotalTokens != 25 {
		t.Fatalf("unexpected usage summary: %+v", report.Summary)
	}
	if report.Summary.CachedInputTokens != 2 || report.Summary.ReasoningTokens != 1 || report.Summary.LatencyMillis != 200 {
		t.Fatalf("unexpected usage summary details: %+v", report.Summary)
	}
	if len(report.Records) != 2 || report.Records[0].TurnID != "turn_000001" || report.Records[1].TurnID != "turn_000002" {
		t.Fatalf("unexpected usage records: %+v", report.Records)
	}
}

func TestUpsertSessionSummaryWritesCompactionEvents(t *testing.T) {
	server := newTestServer()

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	result := postJSONWithStatus[managedagents.UpsertSessionSummaryResult](t, server, http.MethodPut, "/v1/sessions/"+session.ID+"/summary", `{
		"summary_text": "User prefers concise replies.",
		"source_until_seq": 2
	}`, http.StatusOK)
	if result.Summary.SummaryText != "User prefers concise replies." || result.Summary.SourceUntilSeq != 2 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if len(result.Events) != 2 ||
		result.Events[0].Type != managedagents.EventSessionStatusCompacting ||
		result.Events[1].Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("unexpected summary events: %+v", result.Events)
	}

	summary := getJSON[managedagents.SessionSummary](t, server, "/v1/sessions/"+session.ID+"/summary")
	if summary.SummaryText != result.Summary.SummaryText {
		t.Fatalf("expected stored summary, got %+v", summary)
	}
}

func TestSessionTaskPlanSnapshotAndHistory(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	emptyResponse := httptest.NewRecorder()
	server.ServeHTTP(emptyResponse, httptest.NewRequest(http.MethodGet, "/v2/sessions/"+session.ID+"/task-plan", nil))
	if emptyResponse.Code != http.StatusNotFound {
		t.Fatalf("expected no active task plan status 404, got %d: %s", emptyResponse.Code, emptyResponse.Body.String())
	}

	now := time.Now().UTC()
	active := managedagents.SessionTaskPlan{
		ID: "plan_active", SessionID: session.ID, WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID,
		Goal: "Ship the snapshot API", HandlingMode: managedagents.TaskPlanModePlanned, Status: managedagents.TaskPlanStatusActive,
		CreatedAt: now, UpdatedAt: now,
		Items: []managedagents.SessionTaskItem{{ID: "task_active_1", PlanID: "plan_active", Index: 0, Description: "Expose the API", Status: managedagents.TaskItemStatusInProgress, CreatedAt: now, UpdatedAt: now}},
	}
	completed := managedagents.SessionTaskPlan{
		ID: "plan_completed", SessionID: session.ID, WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID,
		Goal: "Prepare storage", HandlingMode: managedagents.TaskPlanModeTracked, Status: managedagents.TaskPlanStatusCompleted,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute), CompletedAt: &now,
		Items: []managedagents.SessionTaskItem{{ID: "task_completed_1", PlanID: "plan_completed", Index: 0, Description: "Create tables", Status: managedagents.TaskItemStatusCompleted, Evidence: "migration passed", CreatedAt: now.Add(-time.Hour), UpdatedAt: now}},
	}
	store.mu.Lock()
	store.taskPlans[session.ID] = []managedagents.SessionTaskPlan{active, completed}
	store.mu.Unlock()

	current := getJSON[struct {
		Plan managedagents.SessionTaskPlan `json:"plan"`
	}](t, server, "/v2/sessions/"+session.ID+"/task-plan")
	if current.Plan.ID != active.ID || len(current.Plan.Items) != 1 {
		t.Fatalf("unexpected current task plan snapshot: %+v", current)
	}
	history := getJSON[struct {
		Plans []managedagents.SessionTaskPlan `json:"plans"`
	}](t, server, "/v1/sessions/"+session.ID+"/task-plans")
	if len(history.Plans) != 2 || history.Plans[0].ID != active.ID || history.Plans[1].Items[0].Evidence != "migration passed" {
		t.Fatalf("unexpected task plan history: %+v", history)
	}
}

func TestListLLMUsageAggregatesByProviderAndModel(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	for _, input := range []managedagents.RecordLLMUsageInput{
		{
			WorkspaceID:        session.WorkspaceID,
			AgentID:            agent.ID,
			AgentConfigVersion: session.AgentConfigVersion,
			SessionID:          session.ID,
			TurnID:             "turn_000001",
			ProviderID:         "fake",
			Model:              "fake-demo",
			InputTokens:        10,
			OutputTokens:       5,
			TotalTokens:        15,
			Status:             "completed",
		},
		{
			WorkspaceID:        session.WorkspaceID,
			AgentID:            agent.ID,
			AgentConfigVersion: session.AgentConfigVersion,
			SessionID:          session.ID,
			TurnID:             "turn_000002",
			ProviderID:         "volcengine-agent-plan",
			Model:              "doubao-test",
			InputTokens:        20,
			OutputTokens:       10,
			TotalTokens:        30,
			Status:             "completed",
		},
	} {
		if _, err := store.RecordLLMUsage(input); err != nil {
			t.Fatalf("record usage: %v", err)
		}
	}

	report := getJSON[managedagents.LLMUsageAggregateReport](t, server, "/v1/llm-usage")
	if report.GroupBy != managedagents.LLMUsageGroupByProviderModel {
		t.Fatalf("expected default group_by provider_model, got %q", report.GroupBy)
	}
	if report.Summary.RecordCount != 2 || report.Summary.TotalTokens != 45 {
		t.Fatalf("unexpected usage summary: %+v", report.Summary)
	}
	if len(report.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", report.Groups)
	}

	filtered := getJSON[managedagents.LLMUsageAggregateReport](t, server, "/v1/llm-usage?provider_id=fake&group_by=provider")
	if filtered.GroupBy != managedagents.LLMUsageGroupByProvider {
		t.Fatalf("expected provider group_by, got %q", filtered.GroupBy)
	}
	if filtered.Summary.RecordCount != 1 || filtered.Summary.TotalTokens != 15 {
		t.Fatalf("unexpected filtered summary: %+v", filtered.Summary)
	}
	if len(filtered.Groups) != 1 || filtered.Groups[0].ProviderID != "fake" || filtered.Groups[0].Model != "" {
		t.Fatalf("unexpected filtered groups: %+v", filtered.Groups)
	}
}

func TestObjectRefsAndSessionArtifacts(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	object := postJSONWithStatus[managedagents.ObjectRef](t, server, http.MethodPost, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/sesn_000001/output.txt",
		"content_type": "text/plain",
		"size_bytes": 42,
		"checksum_sha256": "abc123",
		"metadata": {"source": "tool"},
		"created_by": "test"
	}`, http.StatusCreated)
	if object.ID != "obj_000001" || object.StorageProvider != managedagents.ObjectStorageProviderS3 || object.Visibility != managedagents.ObjectVisibilityWorkspace {
		t.Fatalf("unexpected object defaults: %+v", object)
	}
	if string(object.Metadata) != `{"source":"tool"}` {
		t.Fatalf("unexpected object metadata: %s", string(object.Metadata))
	}
	fetchedObject := getJSON[managedagents.ObjectRef](t, server, "/v1/object-refs/"+object.ID)
	if fetchedObject.ID != object.ID || fetchedObject.ObjectKey != object.ObjectKey {
		t.Fatalf("unexpected fetched object: %+v", fetchedObject)
	}

	artifact := postJSONWithStatus[managedagents.SessionArtifact](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"turn_id": "turn_000001",
		"tool_call_id": "call_write",
		"name": "output.txt",
		"artifact_type": "file",
		"metadata": {"preview": "hello"},
		"created_by": "test"
	}`, http.StatusCreated)
	if artifact.ID != "art_000001" || artifact.EnvironmentID != environment.ID || artifact.WorkspaceID != session.WorkspaceID {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}

	listed := getJSON[struct {
		Artifacts []managedagents.SessionArtifact `json:"artifacts"`
	}](t, server, "/v1/sessions/"+session.ID+"/artifacts")
	if len(listed.Artifacts) != 1 || listed.Artifacts[0].ObjectRefID != object.ID || listed.Artifacts[0].TurnID != "turn_000001" {
		t.Fatalf("unexpected session artifacts: %+v", listed.Artifacts)
	}

	foreignObject := postJSONWithStatus[managedagents.ObjectRef](t, server, http.MethodPost, "/v1/object-refs", `{
		"workspace_id": "wksp_other",
		"bucket": "tma-artifacts",
		"object_key": "wksp_other/file.txt"
	}`, http.StatusCreated)
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts", bytes.NewBufferString(`{
		"object_ref_id": "`+foreignObject.ID+`"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected workspace mismatch status %d, got %d: %s", http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestUploadSessionArtifactUsesObjectStore(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	body, contentType := multipartArtifactUpload(t, map[string]string{
		"bucket":        "tma-artifacts",
		"object_key":    "wksp_default/" + session.ID + "/uploads/output.txt",
		"turn_id":       "turn_000001",
		"tool_call_id":  "call_write",
		"metadata":      `{"preview":"hello"}`,
		"artifact_type": "file",
	}, "file", "output.txt", "hello")
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", body)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected upload status %d, got %d: %s", http.StatusCreated, response.Code, response.Body.String())
	}
	if len(objectStore.puts) != 1 {
		t.Fatalf("expected 1 object store put, got %#v", objectStore.puts)
	}
	if objectStore.puts[0].Bucket != "tma-artifacts" || objectStore.puts[0].Key != "wksp_default/"+session.ID+"/uploads/output.txt" || objectStore.puts[0].Content != "hello" {
		t.Fatalf("unexpected object store put: %#v", objectStore.puts[0])
	}

	var decoded struct {
		ObjectRef     managedagents.ObjectRef       `json:"object_ref"`
		Artifact      managedagents.SessionArtifact `json:"artifact"`
		WorkspacePath string                        `json:"workspace_path"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if decoded.ObjectRef.ID == "" || decoded.ObjectRef.Bucket != "tma-artifacts" || decoded.ObjectRef.ChecksumSHA256 == "" {
		t.Fatalf("unexpected object ref: %+v", decoded.ObjectRef)
	}
	if decoded.Artifact.ID == "" || decoded.Artifact.ObjectRefID != decoded.ObjectRef.ID || decoded.Artifact.TurnID != "turn_000001" {
		t.Fatalf("unexpected artifact: %+v", decoded.Artifact)
	}
	if decoded.WorkspacePath != "/workspace/uploads/"+decoded.Artifact.ID+"/output.txt" {
		t.Fatalf("unexpected workspace path: %q", decoded.WorkspacePath)
	}

	unsupported := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", strings.NewReader("not multipart"))
	unsupported.Header.Set("Content-Type", "text/plain")
	unsupportedResponse := httptest.NewRecorder()
	server.ServeHTTP(unsupportedResponse, unsupported)
	if unsupportedResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected unsupported upload media type status 415, got %d: %s", unsupportedResponse.Code, unsupportedResponse.Body.String())
	}

	oversized := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", strings.NewReader("x"))
	oversized.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	oversized.ContentLength = maxArtifactUploadBytes + 1025
	oversizedResponse := httptest.NewRecorder()
	server.ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized upload status 413, got %d: %s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestUploadSessionArtifactWithoutObjectStoreReturnsUnavailable(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	body, contentType := multipartArtifactUpload(t, map[string]string{
		"bucket": "tma-artifacts",
	}, "file", "output.txt", "hello")
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", body)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected upload status %d, got %d: %s", http.StatusServiceUnavailable, response.Code, response.Body.String())
	}
}

func TestDownloadSessionArtifactProxiesObjectContent(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{downloads: map[string]string{}}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	object := postJSON[managedagents.ObjectRef](t, server, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/`+session.ID+`/files/report.txt",
		"content_type": "text/plain",
		"size_bytes": 7
	}`)
	artifact := postJSON[managedagents.SessionArtifact](t, server, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"name": "report.txt",
		"artifact_type": "file"
	}`)

	objectStore.downloads[object.Bucket+"/"+object.ObjectKey] = "report-1"
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID+"/artifacts/"+artifact.ID+"/download", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected download status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != "report-1" {
		t.Fatalf("unexpected body: %q", got)
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("unexpected content type: %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "report.txt") {
		t.Fatalf("unexpected content disposition: %q", got)
	}
}

func TestDownloadObjectRefRequiresSessionContext(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{downloads: map[string]string{}}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	object := postJSONWithStatus[managedagents.ObjectRef](t, server, http.MethodPost, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/`+session.ID+`/files/secret.txt",
		"content_type": "text/plain",
		"size_bytes": 9,
		"visibility": "session"
	}`, http.StatusCreated)
	artifact := postJSON[managedagents.SessionArtifact](t, server, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"name": "secret.txt",
		"artifact_type": "file"
	}`)
	_ = artifact

	objectStore.downloads[object.Bucket+"/"+object.ObjectKey] = "secret-1"

	noSessionReq := httptest.NewRequest(http.MethodGet, "/v1/object-refs/"+object.ID+"/download", nil)
	noSessionResp := httptest.NewRecorder()
	server.ServeHTTP(noSessionResp, noSessionReq)
	if noSessionResp.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden without session, got %d: %s", noSessionResp.Code, noSessionResp.Body.String())
	}

	withSessionReq := httptest.NewRequest(http.MethodGet, "/v1/object-refs/"+object.ID+"/download?session_id="+session.ID, nil)
	withSessionResp := httptest.NewRecorder()
	server.ServeHTTP(withSessionResp, withSessionReq)
	if withSessionResp.Code != http.StatusOK {
		t.Fatalf("expected download status %d, got %d: %s", http.StatusOK, withSessionResp.Code, withSessionResp.Body.String())
	}
	if got := withSessionResp.Body.String(); got != "secret-1" {
		t.Fatalf("unexpected session download body: %q", got)
	}
}

func TestDeleteObjectRefRequiresArtifactCleanup(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", &fakeObjectStore{downloads: map[string]string{}})

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "fake-demo",
		"system": "You are helpful."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	object := postJSON[managedagents.ObjectRef](t, server, "/v1/object-refs", `{
		"bucket": "tma-artifacts",
		"object_key": "wksp_default/`+session.ID+`/files/report.txt",
		"size_bytes": 7
	}`)
	postJSON[managedagents.SessionArtifact](t, server, "/v1/sessions/"+session.ID+"/artifacts", `{
		"object_ref_id": "`+object.ID+`",
		"artifact_type": "file"
	}`)

	request := httptest.NewRequest(http.MethodDelete, "/v1/object-refs/"+object.ID, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected conflict when deleting referenced object, got %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/sessions/"+session.ID+"/artifacts/art_000001", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected artifact delete status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/object-refs/"+object.ID, nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected object delete status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
	}
}

func TestAppendEventsUsesInjectedRunner(t *testing.T) {
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(newTestStore(), recorder, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"run"}]}}]
	}`)
	turnID := payloadString(startResponse.Events[1].Payload, "turn_id")
	if len(recorder.starts) != 1 {
		t.Fatalf("expected 1 runner start, got %d", len(recorder.starts))
	}
	if recorder.starts[0].SessionID != session.ID || recorder.starts[0].TurnID != turnID {
		t.Fatalf("unexpected runner start request: %+v", recorder.starts[0])
	}
	if recorder.starts[0].UserEventSeq != startResponse.Events[1].Seq {
		t.Fatalf("expected runner user event seq %d, got %d", startResponse.Events[1].Seq, recorder.starts[0].UserEventSeq)
	}

	postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.interrupt"}]
	}`)
	if len(recorder.interrupts) != 1 {
		t.Fatalf("expected 1 runner interrupt, got %d", len(recorder.interrupts))
	}
	if recorder.interrupts[0].SessionID != session.ID || recorder.interrupts[0].TurnID != turnID {
		t.Fatalf("unexpected runner interrupt request: %+v", recorder.interrupts[0])
	}
}

func TestAppendEventsPreferLatestInterruptsRunningTurnAndStartsNewestTurn(t *testing.T) {
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(newTestStore(), recorder, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"open the desktop"}]}}]
	}`)
	firstTurnID := payloadString(startResponse.Events[1].Payload, "turn_id")

	replaceResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"prefer_latest": true,
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"just answer my question"}]}}]
	}`)
	if len(replaceResponse.Events) != 5 {
		t.Fatalf("expected interrupt + newest turn events, got %+v", replaceResponse.Events)
	}
	if replaceResponse.Events[0].Type != managedagents.EventUserInterrupt ||
		replaceResponse.Events[1].Type != managedagents.EventSessionStatusInterrupting ||
		replaceResponse.Events[2].Type != managedagents.EventSessionStatusIdle ||
		replaceResponse.Events[3].Type != managedagents.EventSessionStatusRunning ||
		replaceResponse.Events[4].Type != managedagents.EventUserMessage {
		t.Fatalf("unexpected prefer-latest event order: %+v", replaceResponse.Events)
	}

	if len(recorder.interrupts) != 1 {
		t.Fatalf("expected 1 runner interrupt, got %d", len(recorder.interrupts))
	}
	if recorder.interrupts[0].SessionID != session.ID || recorder.interrupts[0].TurnID != firstTurnID {
		t.Fatalf("unexpected runner interrupt request: %+v", recorder.interrupts[0])
	}
	if len(recorder.starts) != 2 {
		t.Fatalf("expected 2 runner starts, got %d", len(recorder.starts))
	}

	secondTurnID := payloadString(replaceResponse.Events[4].Payload, "turn_id")
	if secondTurnID == "" || secondTurnID == firstTurnID {
		t.Fatalf("expected a new turn id, first=%q second=%q", firstTurnID, secondTurnID)
	}
	if recorder.starts[1].SessionID != session.ID || recorder.starts[1].TurnID != secondTurnID {
		t.Fatalf("unexpected runner start request for newest turn: %+v", recorder.starts[1])
	}
	if recorder.starts[1].UserEventSeq != replaceResponse.Events[4].Seq {
		t.Fatalf("expected newest user event seq %d, got %d", replaceResponse.Events[4].Seq, recorder.starts[1].UserEventSeq)
	}
}

func TestAppendSessionControlEventsTargetsRunningTurn(t *testing.T) {
	recorder := &recordingRunner{}
	server := NewServerWithStoreAndRunner(newTestStore(), recorder, nil)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{"name":"Control Agent","model":"fake-demo","system":"Follow controls."}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{"name":"Control Env","config":{"type":"cloud"}}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{"agent_id":"`+agent.ID+`","environment_id":"`+environment.ID+`"}`)
	started := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events":[{"type":"user.message","payload":{"content":[{"type":"text","text":"start"}]}}]
	}`)
	turnID := payloadString(started.Events[len(started.Events)-1].Payload, "turn_id")
	controls := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events":[
			{"type":"user.steer","payload":{"content":[{"type":"text","text":"focus on correctness"}]}},
			{"type":"user.follow_up","payload":{"content":[{"type":"text","text":"include tests"}]}}
		]
	}`)
	if len(controls.Events) != 2 || controls.Events[0].Type != managedagents.EventUserSteer || controls.Events[1].Type != managedagents.EventUserFollowUp {
		t.Fatalf("control events = %+v", controls.Events)
	}
	for _, event := range controls.Events {
		if got := payloadString(event.Payload, "turn_id"); got != turnID {
			t.Fatalf("control event %s turn_id = %q, want %q", event.Type, got, turnID)
		}
	}
	if len(recorder.starts) != 1 || len(recorder.interrupts) != 0 {
		t.Fatalf("runner dispatch starts=%d interrupts=%d", len(recorder.starts), len(recorder.interrupts))
	}
}

func TestRunnerStartFailureMarksTurnFailedAndSessionIdle(t *testing.T) {
	server := NewServerWithStoreAndRunner(newTestStore(), failingRunner{}, nil)

	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"run"}]}}]
	}`)
	turnID := payloadString(startResponse.Events[1].Payload, "turn_id")

	idleSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if idleSession.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session status %q, got %q", managedagents.SessionStatusIdle, idleSession.Status)
	}

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 5 {
		t.Fatalf("expected 5 events after runner start failure, got %d", len(events.Events))
	}
	idleEvent := events.Events[4]
	if idleEvent.Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("expected idle event %q, got %q", managedagents.EventSessionStatusIdle, idleEvent.Type)
	}
	if got := payloadString(idleEvent.Payload, "turn_id"); got != turnID {
		t.Fatalf("expected failed event turn_id %q, got %q", turnID, got)
	}
	if got := payloadString(idleEvent.Payload, "last_turn_status"); got != "failed" {
		t.Fatalf("expected last_turn_status %q, got %q", "failed", got)
	}
	if got := payloadString(idleEvent.Payload, "reason"); got != "runner unavailable" {
		t.Fatalf("expected failed reason %q, got %q", "runner unavailable", got)
	}

	secondResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"retry"}]}}]
	}`)
	if len(secondResponse.Events) != 2 {
		t.Fatalf("expected retry user.message to be accepted with 2 immediate events, got %d", len(secondResponse.Events))
	}
}

func TestStreamSessionEventsReplaysHistoryAfterSeq(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID+"/events/stream?after_seq=1", nil).WithContext(ctx)
	response := newSynchronizedResponseRecorder()

	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, request)
		close(done)
	}()

	waitFor(t, func() bool {
		body := response.BodyString()
		return strings.Contains(body, "event: session.status_idle") &&
			strings.Contains(body, ": stream ready")
	})
	cancel()
	<-done

	body := response.BodyString()
	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, body)
	}
	if strings.Contains(body, "event: session.status_provisioning") {
		t.Fatalf("did not expect provisioning event after seq 1: %s", body)
	}
	if !strings.Contains(body, "event: session.status_idle") {
		t.Fatalf("expected idle event in stream: %s", body)
	}
	if !strings.Contains(body, `"seq":2`) {
		t.Fatalf("expected seq 2 event in stream: %s", body)
	}
}

func TestArchiveSessionTerminatesAndBlocksNewEvents(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	archived := postJSONWithStatus[managedagents.Session](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/archive", `{}`, http.StatusOK)
	if archived.Status != managedagents.SessionStatusTerminated {
		t.Fatalf("expected archived session status %q, got %q", managedagents.SessionStatusTerminated, archived.Status)
	}

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 3 {
		t.Fatalf("expected 3 events after archive, got %d", len(events.Events))
	}
	if events.Events[2].Type != managedagents.EventSessionStatusTerminated {
		t.Fatalf("expected termination event %q, got %q", managedagents.EventSessionStatusTerminated, events.Events[2].Type)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/events", bytes.NewBufferString(`{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"blocked"}]}}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected status %d after append to terminated session, got %d: %s", http.StatusConflict, response.Code, response.Body.String())
	}
}

func TestRestoreSessionReturnsArchivedSessionToIdle(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Restore Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	archived := postJSONWithStatus[managedagents.Session](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/archive", `{}`, http.StatusOK)
	if archived.ArchivedAt == nil {
		t.Fatal("expected archived_at after archive")
	}

	restored := postJSONWithStatus[managedagents.Session](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/restore", `{}`, http.StatusOK)
	if restored.Status != managedagents.SessionStatusIdle || restored.ArchivedAt != nil {
		t.Fatalf("expected restored idle session, got %+v", restored)
	}

	response := postJSONWithStatus[struct {
		Events []managedagents.Event `json:"events"`
	}](t, server, http.MethodPost, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"continue"}]}}]
	}`, http.StatusCreated)
	if len(response.Events) < 2 || response.Events[1].Type != managedagents.EventUserMessage {
		t.Fatalf("expected restored session to accept a new user message, got %+v", response.Events)
	}
}

func TestDeleteSessionRemovesSessionAndEvents(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	request := httptest.NewRequest(http.MethodDelete, "/v1/sessions/"+session.ID, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected delete status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
	}

	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID, nil))
	if getResponse.Code != http.StatusNotFound {
		t.Fatalf("expected get deleted session status %d, got %d: %s", http.StatusNotFound, getResponse.Code, getResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session.ID+"/events", nil))
	if listResponse.Code != http.StatusNotFound {
		t.Fatalf("expected list deleted session events status %d, got %d: %s", http.StatusNotFound, listResponse.Code, listResponse.Body.String())
	}
}

func TestInterruptRequiresRunningSession(t *testing.T) {
	server := newTestServer()
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"model": "gpt-4o",
		"system": "You are a coding agent."
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)

	startResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.message", "payload": {"content": [{"type":"text","text":"run"}]}}]
	}`)
	turnID := payloadString(startResponse.Events[1].Payload, "turn_id")
	if turnID == "" {
		t.Fatal("expected user.message payload to include turn_id")
	}

	interruptResponse := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.interrupt"}]
	}`)

	if len(interruptResponse.Events) != 3 {
		t.Fatalf("expected 3 interrupt events, got %d", len(interruptResponse.Events))
	}
	if interruptResponse.Events[0].Type != managedagents.EventUserInterrupt {
		t.Fatalf("expected first interrupt event %q, got %q", managedagents.EventUserInterrupt, interruptResponse.Events[0].Type)
	}
	if interruptResponse.Events[1].Type != managedagents.EventSessionStatusInterrupting {
		t.Fatalf("expected second interrupt event %q, got %q", managedagents.EventSessionStatusInterrupting, interruptResponse.Events[1].Type)
	}
	if interruptResponse.Events[2].Type != managedagents.EventSessionStatusIdle {
		t.Fatalf("expected third interrupt event %q, got %q", managedagents.EventSessionStatusIdle, interruptResponse.Events[2].Type)
	}
	for _, event := range interruptResponse.Events {
		if got := payloadString(event.Payload, "turn_id"); got != turnID {
			t.Fatalf("expected interrupt event %s to use turn_id %q, got %q", event.Type, turnID, got)
		}
	}

	idleSession := getJSON[managedagents.Session](t, server, "/v1/sessions/"+session.ID)
	if idleSession.Status != managedagents.SessionStatusIdle {
		t.Fatalf("expected session status %q after interrupt, got %q", managedagents.SessionStatusIdle, idleSession.Status)
	}

	time.Sleep(runner.DefaultMockTurnDelay + 100*time.Millisecond)

	events := getJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events")
	if len(events.Events) != 7 {
		t.Fatalf("expected 7 events after interrupted turn, got %d", len(events.Events))
	}
	for _, event := range events.Events {
		if event.Type == managedagents.EventAgentMessage {
			t.Fatalf("did not expect agent.message after interrupt: %+v", events.Events)
		}
	}
}

type eventsResponse struct {
	Events []managedagents.Event `json:"events"`
}

type llmProvidersResponse struct {
	Providers []managedagents.LLMProvider `json:"providers"`
}

type llmModelsResponse struct {
	Models []managedagents.LLMModel `json:"models"`
}

type agentConfigVersionsResponse struct {
	ConfigVersions []managedagents.AgentConfigVersion `json:"config_versions"`
}

func TestInterruptClearsPendingSessionApprovals(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, &recordingRunner{}, nil)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name": "Code Assistant",
		"llm_provider": "fake",
		"llm_model": "fake-demo"
	}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{
		"name": "default-cloud",
		"config": {"type": "cloud"}
	}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{
		"agent_id": "`+agent.ID+`",
		"environment_id": "`+environment.ID+`"
	}`)
	started, err := store.AppendEvents(session.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"edit"}]}`),
	}})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	turnID := payloadString(started[len(started)-1].Payload, "turn_id")
	if _, err := store.SaveSessionIntervention(session.ID, managedagents.SaveSessionInterventionInput{
		TurnID: turnID, CallID: "call_edit", ToolIdentifier: "default", APIName: "edit_file", InterventionMode: "request_approval",
	}); err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	interrupted := postJSON[eventsResponse](t, server, "/v1/sessions/"+session.ID+"/events", `{
		"events": [{"type": "user.interrupt"}]
	}`)
	if len(interrupted.Events) != 4 || interrupted.Events[2].Type != managedagents.EventRuntimeToolInterventionRejected {
		t.Fatalf("expected interrupt response to reject pending approval, got %+v", interrupted.Events)
	}
	pending := getJSON[struct {
		Interventions []managedagents.SessionIntervention `json:"interventions"`
	}](t, server, "/v1/sessions/"+session.ID+"/interventions?status=pending")
	if len(pending.Interventions) != 0 {
		t.Fatalf("expected no pending approvals after interrupt, got %+v", pending.Interventions)
	}
}

type recordingRunner struct {
	starts     []runner.TurnRequest
	interrupts []runner.InterruptRequest
}

func (r *recordingRunner) StartTurn(_ context.Context, request runner.TurnRequest) error {
	r.starts = append(r.starts, request)
	return nil
}

func (r *recordingRunner) InterruptTurn(_ context.Context, request runner.InterruptRequest) error {
	r.interrupts = append(r.interrupts, request)
	return nil
}

type contextRecordingRunner struct {
	starts     []runner.TurnRequest
	contextErr error
}

func (r *contextRecordingRunner) StartTurn(ctx context.Context, request runner.TurnRequest) error {
	r.contextErr = ctx.Err()
	r.starts = append(r.starts, request)
	return nil
}

func (r *contextRecordingRunner) InterruptTurn(context.Context, runner.InterruptRequest) error {
	return nil
}

type flakyStartRunner struct {
	failures int
	starts   []runner.TurnRequest
}

func (r *flakyStartRunner) StartTurn(_ context.Context, request runner.TurnRequest) error {
	r.starts = append(r.starts, request)
	if r.failures > 0 {
		r.failures--
		return errors.New("runner unavailable")
	}
	return nil
}

func (r *flakyStartRunner) InterruptTurn(context.Context, runner.InterruptRequest) error {
	return nil
}

type failingRunner struct{}

func (failingRunner) StartTurn(context.Context, runner.TurnRequest) error {
	return errors.New("runner unavailable")
}

func (failingRunner) InterruptTurn(context.Context, runner.InterruptRequest) error {
	return nil
}

type fakeObjectStore struct {
	puts      []fakeObjectStorePut
	downloads map[string]string
}

type fakeObjectStorePut struct {
	Bucket      string
	Key         string
	Content     string
	ContentType string
	SizeBytes   int64
	Checksum    string
}

func (f *fakeObjectStore) PutObject(_ context.Context, input objectstore.PutObjectInput) (objectstore.PutObjectResult, error) {
	content, err := io.ReadAll(input.Body)
	if err != nil {
		return objectstore.PutObjectResult{}, err
	}
	f.puts = append(f.puts, fakeObjectStorePut{
		Bucket:      input.Bucket,
		Key:         input.Key,
		Content:     string(content),
		ContentType: input.ContentType,
		SizeBytes:   input.SizeBytes,
		Checksum:    input.ChecksumSHA256,
	})
	return objectstore.PutObjectResult{
		Bucket:         input.Bucket,
		Key:            input.Key,
		ETag:           "fake-etag",
		SizeBytes:      input.SizeBytes,
		ChecksumSHA256: input.ChecksumSHA256,
	}, nil
}

func (f *fakeObjectStore) GetObject(_ context.Context, input objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	if f.downloads != nil {
		if content, ok := f.downloads[input.Bucket+"/"+input.Key]; ok {
			return objectstore.GetObjectResult{
				Bucket:      input.Bucket,
				Key:         input.Key,
				Body:        io.NopCloser(strings.NewReader(content)),
				ContentType: "text/plain",
				SizeBytes:   int64(len(content)),
				ETag:        "fake-download-etag",
			}, nil
		}
		if content, ok := f.downloads[input.Key]; ok {
			return objectstore.GetObjectResult{
				Bucket:      input.Bucket,
				Key:         input.Key,
				Body:        io.NopCloser(strings.NewReader(content)),
				ContentType: "text/plain",
				SizeBytes:   int64(len(content)),
				ETag:        "fake-download-etag",
			}, nil
		}
	}
	return objectstore.GetObjectResult{}, objectstore.ErrNotFound
}

func (f *fakeObjectStore) DeleteObject(context.Context, objectstore.DeleteObjectInput) error {
	return objectstore.ErrNotConfigured
}

func (f *fakeObjectStore) PresignGetObject(context.Context, objectstore.PresignGetObjectInput) (objectstore.PresignedURL, error) {
	return objectstore.PresignedURL{}, objectstore.ErrNotConfigured
}

func multipartArtifactUpload(t *testing.T, fields map[string]string, fileField string, fileName string, content string) (*bytes.Buffer, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field %s: %v", key, err)
		}
	}
	file, err := writer.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func postJSON[T any](t *testing.T, handler http.Handler, path string, body string) T {
	t.Helper()
	return postJSONWithStatus[T](t, handler, http.MethodPost, path, body, http.StatusCreated)
}

func postJSONWithStatus[T any](t *testing.T, handler http.Handler, method, path string, body string, expectedStatus int) T {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != expectedStatus {
		t.Fatalf("%s %s expected status %d, got %d: %s", method, path, expectedStatus, response.Code, response.Body.String())
	}

	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode %s %s response: %v", method, path, err)
	}

	return value
}

func patchLLMProvider(t *testing.T, handler http.Handler, providerID string, revision int64, body string) managedagents.LLMProvider {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, "/v1/llm-providers/"+providerID, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(revision, 10)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH provider expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var provider managedagents.LLMProvider
	if err := json.NewDecoder(response.Body).Decode(&provider); err != nil {
		t.Fatalf("decode PATCH provider response: %v", err)
	}
	expectedETag := strconv.Quote(strconv.FormatInt(provider.Revision, 10))
	if response.Header().Get("ETag") != expectedETag {
		t.Fatalf("unexpected provider ETag: got=%q want=%q", response.Header().Get("ETag"), expectedETag)
	}
	return provider
}

func postLLMProviderAction(t *testing.T, handler http.Handler, providerID string, action string, revision int64) managedagents.LLMProvider {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/llm-providers/"+providerID+"/"+action, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(revision, 10)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST provider %s expected status 200, got %d: %s", action, response.Code, response.Body.String())
	}
	var provider managedagents.LLMProvider
	if err := json.NewDecoder(response.Body).Decode(&provider); err != nil {
		t.Fatalf("decode provider %s response: %v", action, err)
	}
	expectedETag := strconv.Quote(strconv.FormatInt(provider.Revision, 10))
	if response.Header().Get("ETag") != expectedETag {
		t.Fatalf("unexpected provider %s ETag: got=%q want=%q", action, response.Header().Get("ETag"), expectedETag)
	}
	return provider
}

func createLLMModel(t *testing.T, handler http.Handler, body string) managedagents.LLMModel {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-None-Match", "*")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create LLM model expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	return decodeLLMModelResponse(t, response)
}

func updateLLMModel(t *testing.T, handler http.Handler, revision int64, body string) managedagents.LLMModel {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/llm-models", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(revision, 10)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update LLM model expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	return decodeLLMModelResponse(t, response)
}

func decodeLLMModelResponse(t *testing.T, response *httptest.ResponseRecorder) managedagents.LLMModel {
	t.Helper()
	var model managedagents.LLMModel
	if err := json.NewDecoder(response.Body).Decode(&model); err != nil {
		t.Fatalf("decode LLM model response: %v", err)
	}
	expectedETag := strconv.Quote(strconv.FormatInt(model.Revision, 10))
	if response.Header().Get("ETag") != expectedETag {
		t.Fatalf("unexpected LLM model ETag: got=%q want=%q", response.Header().Get("ETag"), expectedETag)
	}
	return model
}

func getJSON[T any](t *testing.T, handler http.Handler, path string) T {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET %s expected status %d, got %d: %s", path, http.StatusOK, response.Code, response.Body.String())
	}

	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode GET %s response: %v", path, err)
	}

	return value
}

func assertRuntimeSettings(t *testing.T, raw json.RawMessage, expected map[string]any) {
	t.Helper()

	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatalf("decode runtime settings: %v", err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("unexpected runtime settings size: got %#v want %#v", actual, expected)
	}
	for key, value := range expected {
		if !reflect.DeepEqual(actual[key], value) {
			t.Fatalf("unexpected runtime setting %s: got %q want %q in %#v", key, actual[key], value, actual)
		}
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition was not met")
}
