package execution

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/tools"
)

func TestResolveToolExecutionDefaultsToCloudSandbox(t *testing.T) {
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
		},
		TurnID: "turn_000001",
	})

	if _, ok := resolved.Context.Provider.(capability.OnlyboxesProvider); !ok {
		t.Fatalf("expected cloud_sandbox provider, got %T", resolved.Context.Provider)
	}
	if resolved.ProviderCapabilities.Runtime != tools.ToolRuntimeCloudSandbox {
		t.Fatalf("expected cloud_sandbox runtime, got %#v", resolved.ProviderCapabilities)
	}
	if len(resolved.Registry.ModelTools()) == 0 {
		t.Fatal("expected default tools to remain visible for cloud_sandbox")
	}
}

func TestResolveToolExecutionHidesLocalSystemWithoutWorker(t *testing.T) {
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime: ToolRuntimeCloudSandbox,
		},
		Store: &toolResolverStore{},
	})

	if !resolved.LocalSystemUnavailable {
		t.Fatal("expected local_system unavailable without worker")
	}
	if len(resolved.Registry.ModelTools()) != 0 {
		t.Fatalf("expected no model tools without local_system runtime, got %#v", resolved.Registry.ModelTools())
	}
	if _, ok := resolved.Context.Provider.(capability.UnavailableProvider); !ok {
		t.Fatalf("expected unavailable provider, got %T", resolved.Context.Provider)
	}
}

func TestResolveToolExecutionUsesWorkerBackedProviderForMatchingWorker(t *testing.T) {
	expiresAt := time.Date(2026, 7, 9, 12, 5, 0, 0, time.UTC)
	store := &toolResolverStore{workers: []managedagents.Worker{{
		ID:             "wrk_000001",
		WorkspaceID:    "wksp_default",
		Status:         managedagents.WorkerStatusOnline,
		LeaseExpiresAt: &expiresAt,
		Capabilities: rawWorkerCapabilities(t, tools.WorkerCapabilities{
			Namespaces:   []string{"default"},
			APIs:         []string{"default.run_command"},
			Runtimes:     []string{"local_system"},
			Capabilities: []string{"exec"},
		}),
	}}}
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime: ToolRuntimeCloudSandbox,
		},
		Store: store,
		Now:   func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) },
	})

	if !resolved.WorkerBacked {
		t.Fatal("expected worker-backed local_system")
	}
	if _, ok := resolved.Context.Provider.(WorkerBackedProvider); !ok {
		t.Fatalf("expected worker-backed provider, got %T", resolved.Context.Provider)
	}
	if got := len(resolved.Registry.ModelTools()); got != 1 {
		t.Fatalf("expected only matching worker tool to be visible, got %d", got)
	}
	if store.listInput.WorkspaceID != "wksp_default" || store.listInput.Status != managedagents.WorkerStatusOnline {
		t.Fatalf("unexpected worker list input: %#v", store.listInput)
	}
}

func TestResolveToolExecutionExposesWorkerPluginManifest(t *testing.T) {
	expiresAt := time.Date(2026, 7, 9, 12, 5, 0, 0, time.UTC)
	store := &toolResolverStore{workers: []managedagents.Worker{{
		ID:             "wrk_robot",
		WorkspaceID:    "wksp_default",
		Status:         managedagents.WorkerStatusOnline,
		LeaseExpiresAt: &expiresAt,
		Capabilities: rawWorkerCapabilities(t, tools.WorkerCapabilities{
			Namespaces:   []string{"robot"},
			APIs:         []string{"robot.get_state"},
			Runtimes:     []string{"local_system"},
			Capabilities: []string{"robot.state"},
			Manifests:    []tools.Manifest{robotManifest()},
		}),
	}}}
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["robot"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime: ToolRuntimeCloudSandbox,
		},
		Store: store,
		Now:   func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) },
	})

	if !resolved.WorkerBacked {
		t.Fatal("expected plugin tool to use worker-backed provider")
	}
	modelTools := resolved.Registry.ModelTools()
	if len(modelTools) != 1 || modelTools[0].Function.Name != "robot.get_state" {
		t.Fatalf("expected plugin model tool, got %#v", modelTools)
	}
	if _, ok := resolved.Registry.Get("robot"); !ok {
		t.Fatal("expected plugin manifest runtime in registry")
	}
}

func TestResolveToolExecutionAllowsExplicitServerLocalFallback(t *testing.T) {
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime:   ToolRuntimeCloudSandbox,
			AllowLocalSystem: true,
		},
		Store: &toolResolverStore{},
	})

	if resolved.LocalSystemUnavailable || resolved.WorkerBacked {
		t.Fatalf("unexpected resolver flags: %#v", resolved)
	}
	if _, ok := resolved.Context.Provider.(capability.LocalSystemProvider); !ok {
		t.Fatalf("expected server-local provider, got %T", resolved.Context.Provider)
	}
	if len(resolved.Registry.ModelTools()) == 0 {
		t.Fatal("expected local_system tools when dev fallback is enabled")
	}
}

func TestResolveToolExecutionLoadsMCPToolsFromConfig(t *testing.T) {
	rawConfig, err := json.Marshal(map[string]any{
		"servers": []map[string]any{{
			"identifier":    "filesystem",
			"command":       os.Args[0],
			"args":          []string{"-test.run=TestExecutionMCPHelperProcess"},
			"stdio_framing": "content_length",
			"env": map[string]any{
				"GO_WANT_EXECUTION_MCP_HELPER": "1",
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal mcp config: %v", err)
	}
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			MCP:         rawConfig,
		},
		TurnID: "turn_000001",
	})
	modelTools := resolved.Registry.ModelTools()
	names := make([]string, 0, len(modelTools))
	for _, tool := range modelTools {
		names = append(names, tool.Function.Name)
	}
	if !containsToolName(names, "filesystem.read_file") {
		t.Fatalf("expected MCP tool to be exposed, got %#v", names)
	}
}

func TestResolveToolExecutionReusesServerHostedMCPBySession(t *testing.T) {
	startMarker := t.TempDir() + "/starts.log"
	rawConfig, err := json.Marshal(map[string]any{
		"servers": []map[string]any{{
			"identifier":    "filesystem",
			"command":       os.Args[0],
			"args":          []string{"-test.run=TestExecutionMCPHelperProcess"},
			"stdio_framing": "content_length",
			"env": map[string]any{
				"GO_WANT_EXECUTION_MCP_HELPER": "1",
				"EXECUTION_MCP_START_MARKER":   startMarker,
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal hosted mcp config: %v", err)
	}
	host := mcp.NewStdioHost(mcp.StdioHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	config := managedagents.AgentRuntimeConfig{
		WorkspaceID:        "wksp_default",
		SessionID:          "sesn_hosted",
		AgentID:            "agt_hosted",
		AgentConfigVersion: 3,
		MCP:                rawConfig,
	}
	for turn := 1; turn <= 2; turn++ {
		resolved := ResolveToolExecution(ToolExecutionRequest{
			Context: t.Context(),
			Config:  config,
			TurnID:  "turn_00000" + strconv.Itoa(turn),
			MCPHost: host,
		})
		if _, ok := resolved.Registry.Get("filesystem"); !ok {
			t.Fatalf("turn %d did not expose hosted MCP runtime", turn)
		}
	}
	if starts := executionMCPStartCount(t, startMarker); starts != 1 {
		t.Fatalf("expected same session to reuse one MCP process, got %d starts", starts)
	}
	config.SessionID = "sesn_hosted_other"
	resolved := ResolveToolExecution(ToolExecutionRequest{Context: t.Context(), Config: config, TurnID: "turn_000001", MCPHost: host})
	if _, ok := resolved.Registry.Get("filesystem"); !ok {
		t.Fatal("other session did not expose hosted MCP runtime")
	}
	if starts := executionMCPStartCount(t, startMarker); starts != 2 {
		t.Fatalf("expected different sessions to use isolated MCP processes, got %d starts", starts)
	}
}

func TestResolveToolExecutionReusesServerHostedHTTPMCPBySession(t *testing.T) {
	var mu sync.Mutex
	initializeCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode hosted HTTP MCP request: %v", err)
		}
		method, _ := request["method"].(string)
		id := request["id"]
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			mu.Lock()
			initializeCount++
			sessionID := "remote-session-" + strconv.Itoa(initializeCount)
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sessionID)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"serverInfo": map[string]any{"name": "Remote MCP"}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") == "" {
				t.Error("expected hosted HTTP MCP session header")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []map[string]any{{"name": "remotePing", "inputSchema": map[string]any{"type": "object"}}}}})
		default:
			t.Fatalf("unexpected hosted HTTP MCP method: %s", method)
		}
	}))
	defer server.Close()

	rawConfig, err := json.Marshal(map[string]any{"servers": []map[string]any{{
		"identifier": "remote", "transport": "streamable_http", "url": server.URL,
	}}})
	if err != nil {
		t.Fatalf("marshal hosted HTTP MCP config: %v", err)
	}
	host := mcp.NewStreamableHTTPHost(mcp.StreamableHTTPHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	config := managedagents.AgentRuntimeConfig{
		WorkspaceID: "wksp_default", SessionID: "sesn_http_hosted", AgentID: "agt_http_hosted", AgentConfigVersion: 4, MCP: rawConfig,
	}
	for turn := 1; turn <= 2; turn++ {
		resolved := ResolveToolExecution(ToolExecutionRequest{
			Context: t.Context(), Config: config, TurnID: "turn_00000" + strconv.Itoa(turn), MCPHTTPHost: host,
		})
		if _, ok := resolved.Registry.Get("remote"); !ok {
			t.Fatalf("turn %d did not expose hosted HTTP MCP runtime", turn)
		}
	}
	mu.Lock()
	if initializeCount != 1 {
		t.Fatalf("expected same session to reuse one remote MCP session, got %d initializes", initializeCount)
	}
	mu.Unlock()
	config.SessionID = "sesn_http_hosted_other"
	resolved := ResolveToolExecution(ToolExecutionRequest{Context: t.Context(), Config: config, TurnID: "turn_000001", MCPHTTPHost: host})
	if _, ok := resolved.Registry.Get("remote"); !ok {
		t.Fatal("other session did not expose hosted HTTP MCP runtime")
	}
	mu.Lock()
	defer mu.Unlock()
	if initializeCount != 2 {
		t.Fatalf("expected different sessions to use isolated remote MCP sessions, got %d initializes", initializeCount)
	}
}

type toolResolverStore struct {
	listInput managedagents.ListWorkersInput
	workers   []managedagents.Worker
}

func (s *toolResolverStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	s.listInput = input
	return append([]managedagents.Worker(nil), s.workers...), nil
}

func (s *toolResolverStore) EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *toolResolverStore) GetWorkerWork(id string) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func robotManifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "robot",
		Type:       "process_plugin",
		Meta: tools.Meta{
			Title:       "Robot",
			Description: "Robot control plugin.",
		},
		SystemRole: "Use robot.* tools only for robot control tasks.",
		API: []tools.API{{
			Name:           "get_state",
			Namespace:      "robot",
			APIName:        "get_state",
			Description:    "Read robot state.",
			Parameters:     json.RawMessage(`{"type":"object","properties":{}}`),
			Capabilities:   []string{"robot.state"},
			Risk:           tools.ToolRiskRead,
			Runtime:        &tools.RuntimePolicy{Allowed: []string{tools.ToolRuntimeLocalSystem}, Preferred: tools.ToolRuntimeLocalSystem},
			Implementation: tools.ToolImplementationWorkerCapability,
		}},
	}
}

func TestExecutionMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXECUTION_MCP_HELPER") != "1" {
		return
	}
	if marker := os.Getenv("EXECUTION_MCP_START_MARKER"); marker != "" {
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		if _, err := io.WriteString(file, strconv.Itoa(os.Getpid())+"\n"); err != nil {
			panic(err)
		}
		if err := file.Close(); err != nil {
			panic(err)
		}
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readExecutionMCPMessage(reader)
		if err == io.EOF {
			os.Exit(0)
		}
		if err != nil {
			panic(err)
		}
		var request map[string]any
		if err := json.Unmarshal(payload, &request); err != nil {
			panic(err)
		}
		method, _ := request["method"].(string)
		if method == "notifications/initialized" {
			continue
		}
		id, _ := request["id"].(float64)
		switch method {
		case "initialize":
			writeExecutionMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo": map[string]any{"name": "Execution Stub MCP"},
				},
			})
		case "tools/list":
			writeExecutionMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "readFile",
						"description": "Read a file.",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						"annotations": map[string]any{"readOnlyHint": true},
					}},
				},
			})
		case "tools/call":
			writeExecutionMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}},
			})
		default:
			writeExecutionMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
}

func readExecutionMCPMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(line[len("Content-Length:"):])
			length, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			contentLength = length
		}
	}
	if contentLength < 0 {
		return nil, io.EOF
	}
	payload := make([]byte, contentLength)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeExecutionMCPMessage(message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	if _, err := io.WriteString(os.Stdout, "Content-Length: "+strconv.Itoa(len(payload))+"\r\n\r\n"); err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(payload); err != nil {
		panic(err)
	}
}

func containsToolName(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func executionMCPStartCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read execution MCP start marker: %v", err)
	}
	return len(strings.Fields(string(raw)))
}
