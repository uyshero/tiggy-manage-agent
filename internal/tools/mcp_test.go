package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	mcppkg "tiggy-manage-agent/internal/mcp"
)

func TestLoadMCPRuntimeBuildsManifestAndExecutesTool(t *testing.T) {
	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "filesystem",
		Command:    os.Args[0],
		Args:       []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]mcppkg.EnvValue{
			"GO_WANT_MCP_HELPER": mcppkg.LiteralEnv("1"),
		},
	})
	if err != nil {
		t.Fatalf("load mcp runtime: %v", err)
	}

	manifest := runtime.Manifest()
	if manifest.Identifier != "filesystem" {
		t.Fatalf("unexpected manifest identifier: %#v", manifest)
	}
	if manifest.Metadata["mcp_transport"] != mcppkg.TransportStdio || manifest.Metadata["mcp_protocol_version"] != "2025-06-18" || manifest.Metadata["mcp_tool_count"] != 2 {
		t.Fatalf("unexpected MCP manifest metadata: %#v", manifest.Metadata)
	}
	if manifest.Metadata["mcp_timeout_seconds"] != 30 || manifest.Metadata["mcp_max_concurrency"] != 4 || manifest.Metadata["mcp_failure_threshold"] != 5 || manifest.Metadata["mcp_cooldown_seconds"] != 30 {
		t.Fatalf("unexpected MCP runtime protection metadata: %#v", manifest.Metadata)
	}
	if names := strings.Join(runtime.Capabilities.Names(), ","); names != "prompts,resources,tools" {
		t.Fatalf("unexpected MCP runtime capabilities: %q", names)
	}
	if len(manifest.API) != 2 {
		t.Fatalf("expected 2 APIs, got %#v", manifest.API)
	}
	if manifest.API[0].Name != "read_file" || manifest.API[0].APIName != "readFile" || manifest.API[0].Risk != ToolRiskRead {
		t.Fatalf("unexpected readFile api mapping: %#v", manifest.API[0])
	}
	if manifest.API[1].Name != "search_web" || manifest.API[1].APIName != "search-web" {
		t.Fatalf("unexpected search-web api mapping: %#v", manifest.API[1])
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_1",
		Identifier: "filesystem",
		APIName:    "read_file",
		Arguments:  json.RawMessage(`{"path":"/tmp/demo.txt"}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute mcp tool: %v", err)
	}
	if !strings.Contains(result.Content, "/tmp/demo.txt") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if !strings.Contains(string(result.State), `"tool_name":"readFile"`) {
		t.Fatalf("unexpected state: %s", result.State)
	}
}

func TestMCPRuntimeFailureResultIsClassifiedAndRedacted(t *testing.T) {
	result, ok := mcpRuntimeFailureResult("remote", Call{ID: "call_1", APIName: "write"}, &mcppkg.RuntimeCallError{
		Class: "transport",
		Err:   errors.New("POST https://secret.example/mcp: connection reset; Authorization: Bearer secret"),
	})
	if !ok || result.Error == nil || result.Error.Type != "mcp_transport" || result.Error.Message != "MCP transport failed." {
		t.Fatalf("unexpected classified result: %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret.example", "Bearer secret", "Authorization"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("classified result leaked %q: %s", secret, encoded)
		}
	}
}

func TestLoadMCPRuntimeAppliesIncludeExcludeFilters(t *testing.T) {
	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier:   "filesystem",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPHelperProcess"},
		IncludeTools: []string{"search-web"},
		ExcludeTools: []string{"readFile"},
		Env: map[string]mcppkg.EnvValue{
			"GO_WANT_MCP_HELPER": mcppkg.LiteralEnv("1"),
		},
	})
	if err != nil {
		t.Fatalf("load filtered mcp runtime: %v", err)
	}

	manifest := runtime.Manifest()
	if len(manifest.API) != 1 || manifest.API[0].APIName != "search-web" {
		t.Fatalf("unexpected filtered APIs: %#v", manifest.API)
	}
}

func TestLoadMCPRuntimeExposesContextCatalogTools(t *testing.T) {
	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "filesystem",
		Command:    os.Args[0],
		Args:       []string{"-test.run=TestMCPHelperProcess"},
		Expose:     mcppkg.ExposeConfig{Resources: true, Prompts: true},
		Env: map[string]mcppkg.EnvValue{
			"GO_WANT_MCP_HELPER": mcppkg.LiteralEnv("1"),
		},
	})
	if err != nil {
		t.Fatalf("load context-exposing mcp runtime: %v", err)
	}
	manifest := runtime.Manifest()
	if len(manifest.API) != 7 {
		t.Fatalf("expected 2 remote tools plus 5 context tools, got %#v", manifest.API)
	}
	for _, name := range []string{"mcp_list_resources", "mcp_list_resource_templates", "mcp_read_resource", "mcp_list_prompts", "mcp_get_prompt"} {
		if !manifestHasAPI(manifest, name, ToolRiskRead) {
			t.Fatalf("expected read-only context api %q in manifest: %#v", name, manifest.API)
		}
	}

	resourceList, err := runtime.Execute(context.Background(), Call{
		ID:         "call_resources",
		Identifier: "filesystem",
		APIName:    "mcp_list_resources",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute list resources: %v", err)
	}
	if !strings.Contains(resourceList.Content, "fixture://guide") || !strings.Contains(string(resourceList.State), `"resources"`) {
		t.Fatalf("unexpected resources result: content=%q state=%s", resourceList.Content, resourceList.State)
	}

	templates, err := runtime.Execute(context.Background(), Call{
		ID:         "call_resource_templates",
		Identifier: "filesystem",
		APIName:    "mcp_list_resource_templates",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute list resource templates: %v", err)
	}
	if !strings.Contains(templates.Content, "fixture://guide/{section}") || !strings.Contains(string(templates.State), `"resource_templates"`) {
		t.Fatalf("unexpected resource templates result: content=%q state=%s", templates.Content, templates.State)
	}

	resource, err := runtime.Execute(context.Background(), Call{
		ID:         "call_resource",
		Identifier: "filesystem",
		APIName:    "mcp_read_resource",
		Arguments:  json.RawMessage(`{"uri":"fixture://guide"}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute read resource: %v", err)
	}
	if !strings.Contains(resource.Content, "resource body for fixture://guide") || !strings.Contains(string(resource.State), `"tma.mcp_context_result.v1"`) {
		t.Fatalf("unexpected resource read result: content=%q state=%s", resource.Content, resource.State)
	}

	prompts, err := runtime.Execute(context.Background(), Call{
		ID:         "call_prompts",
		Identifier: "filesystem",
		APIName:    "mcp_list_prompts",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute list prompts: %v", err)
	}
	if !strings.Contains(prompts.Content, "summarize") || !strings.Contains(string(prompts.State), `"prompts"`) {
		t.Fatalf("unexpected prompts result: content=%q state=%s", prompts.Content, prompts.State)
	}

	prompt, err := runtime.Execute(context.Background(), Call{
		ID:         "call_prompt",
		Identifier: "filesystem",
		APIName:    "mcp_get_prompt",
		Arguments:  json.RawMessage(`{"name":"summarize","arguments":{"topic":"MCP"}}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute get prompt: %v", err)
	}
	if !strings.Contains(prompt.Content, "Summarize MCP") || !strings.Contains(string(prompt.State), `"name":"summarize"`) {
		t.Fatalf("unexpected prompt get result: content=%q state=%s", prompt.Content, prompt.State)
	}
}

func TestLoadMCPRuntimeResolvesEnvRefs(t *testing.T) {
	t.Setenv("TMA_MCP_TEST_SECRET", "runtime-secret")

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier:   "filesystem",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPHelperProcess"},
		IncludeTools: []string{"secretStatus"},
		Env: map[string]mcppkg.EnvValue{
			"GO_WANT_MCP_HELPER": mcppkg.LiteralEnv("1"),
			"MCP_SECRET":         mcppkg.EnvRef("TMA_MCP_TEST_SECRET"),
		},
	})
	if err != nil {
		t.Fatalf("load mcp runtime with env ref: %v", err)
	}

	manifest := runtime.Manifest()
	if len(manifest.API) != 1 || manifest.API[0].APIName != "secretStatus" {
		t.Fatalf("unexpected APIs for env ref MCP runtime: %#v", manifest.API)
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_2",
		Identifier: "filesystem",
		APIName:    "secret_status",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute secret status tool: %v", err)
	}
	if !strings.Contains(result.Content, "runtime-secret") {
		t.Fatalf("expected resolved secret in tool output, got %q", result.Content)
	}
}

func TestMCPRuntimeUsesRuntimeScopedResolvedClient(t *testing.T) {
	t.Setenv("TMA_MCP_TEST_SECRET", "runtime-secret")

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier:   "filesystem",
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPHelperProcess"},
		IncludeTools: []string{"secretStatus"},
		Env: map[string]mcppkg.EnvValue{
			"GO_WANT_MCP_HELPER": mcppkg.LiteralEnv("1"),
			"MCP_SECRET":         mcppkg.EnvRef("TMA_MCP_TEST_SECRET"),
		},
	})
	if err != nil {
		t.Fatalf("load mcp runtime with env ref: %v", err)
	}
	if runtime.client == nil {
		t.Fatalf("expected loaded MCP runtime to keep a resolved runtime-scoped client")
	}

	t.Setenv("TMA_MCP_TEST_SECRET", "rotated-after-runtime-load")
	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_cached_client",
		Identifier: "filesystem",
		APIName:    "secret_status",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute secret status tool with cached client: %v", err)
	}
	if !strings.Contains(result.Content, "runtime-secret") || strings.Contains(result.Content, "rotated-after-runtime-load") {
		t.Fatalf("expected runtime-scoped client to use load-time resolved env, got %q", result.Content)
	}
}

func TestLoadMCPRuntimeMissingEnvRefDoesNotLeakSecret(t *testing.T) {
	t.Setenv("TMA_MCP_TEST_SECRET", "super-secret-value")

	_, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "filesystem",
		Command:    os.Args[0],
		Args:       []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]mcppkg.EnvValue{
			"GO_WANT_MCP_HELPER": mcppkg.LiteralEnv("1"),
			"MCP_SECRET":         mcppkg.EnvRef("TMA_MCP_TEST_SECRET_MISSING"),
		},
	})
	if err == nil {
		t.Fatalf("expected missing env ref error")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("missing env ref error leaked secret: %v", err)
	}
	if !strings.Contains(err.Error(), "TMA_MCP_TEST_SECRET_MISSING") {
		t.Fatalf("missing env ref error should name the missing reference: %v", err)
	}
}

func TestLoadMCPRuntimeStreamableHTTP(t *testing.T) {
	t.Setenv("TMA_MCP_HTTP_AUTH", "Bearer runtime-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Fatalf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode MCP HTTP request: %v", err)
		}
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo": map[string]any{"name": "Runtime HTTP MCP"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "ping",
						"description": "Ping over streamable HTTP.",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						"annotations": map[string]any{"readOnlyHint": true},
					}},
				},
			})
		default:
			t.Fatalf("unexpected MCP HTTP method: %s", method)
		}
	}))
	defer server.Close()

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "remote",
		Transport:  mcppkg.TransportStreamableHTTP,
		URL:        server.URL,
		Headers: map[string]mcppkg.EnvValue{
			"Authorization": mcppkg.EnvRef("TMA_MCP_HTTP_AUTH"),
		},
	})
	if err != nil {
		t.Fatalf("load streamable_http MCP runtime: %v", err)
	}
	manifest := runtime.Manifest()
	if manifest.Identifier != "remote" || len(manifest.API) != 1 || manifest.API[0].Name != "ping" {
		t.Fatalf("unexpected streamable_http manifest: %#v", manifest)
	}
}

func TestLoadMCPRuntimeStreamableHTTPOAuthClientCredentials(t *testing.T) {
	t.Setenv("TMA_MCP_OAUTH_CLIENT_ID", "runtime-client")
	t.Setenv("TMA_MCP_OAUTH_CLIENT_SECRET", "runtime-secret")
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse oauth token form: %v", err)
			}
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "runtime-client" || r.Form.Get("client_secret") != "runtime-secret" {
				t.Fatalf("unexpected oauth token form: %#v", r.Form)
			}
			if r.Form.Get("scope") != "mcp.read" || r.Form.Get("resource") != "https://mcp.example.test/mcp" {
				t.Fatalf("unexpected oauth scope/resource: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"access_token": "runtime-oauth-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer runtime-oauth-token" {
				t.Fatalf("unexpected OAuth Authorization header: %q", r.Header.Get("Authorization"))
			}
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode MCP HTTP request: %v", err)
			}
			method, _ := request["method"].(string)
			id, _ := request["id"].(float64)
			w.Header().Set("Content-Type", "application/json")
			switch method {
			case "initialize":
				writeHTTPMCPTestMessage(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"serverInfo": map[string]any{"name": "OAuth MCP"},
					},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				writeHTTPMCPTestMessage(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "securePing",
							"description": "Ping over OAuth-protected streamable HTTP.",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
							"annotations": map[string]any{"readOnlyHint": true},
						}},
					},
				})
			case "tools/call":
				writeHTTPMCPTestMessage(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": "secure pong",
						}},
					},
				})
			default:
				t.Fatalf("unexpected MCP HTTP method: %s", method)
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "secure_remote",
		Transport:  mcppkg.TransportStreamableHTTP,
		URL:        server.URL + "/mcp",
		OAuth: &mcppkg.OAuthConfig{
			GrantType:    "client_credentials",
			TokenURL:     server.URL + "/token",
			ClientID:     envValuePtr(mcppkg.EnvRef("TMA_MCP_OAUTH_CLIENT_ID")),
			ClientSecret: envValuePtr(mcppkg.SecretRef("env:TMA_MCP_OAUTH_CLIENT_SECRET")),
			Scopes:       []string{"mcp.read"},
			Resource:     "https://mcp.example.test/mcp",
		},
	})
	if err != nil {
		t.Fatalf("load OAuth streamable_http MCP runtime: %v", err)
	}
	if tokenRequests != 1 {
		t.Fatalf("expected one oauth token request, got %d", tokenRequests)
	}
	manifest := runtime.Manifest()
	if manifest.Identifier != "secure_remote" || len(manifest.API) != 1 || manifest.API[0].Name != "secure_ping" {
		t.Fatalf("unexpected OAuth streamable_http manifest: %#v", manifest)
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_secure_ping",
		Identifier: "secure_remote",
		APIName:    "secure_ping",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute OAuth streamable_http MCP tool: %v", err)
	}
	if !strings.Contains(result.Content, "secure pong") {
		t.Fatalf("unexpected OAuth MCP result: %#v", result)
	}
	if tokenRequests != 1 {
		t.Fatalf("expected OAuth token cache to reuse manifest-load token, got %d requests", tokenRequests)
	}
}

func TestLoadMCPRuntimeStreamableHTTPRefreshTokenOAuth(t *testing.T) {
	t.Setenv("TMA_MCP_OAUTH_CLIENT_ID", "runtime-client")
	t.Setenv("TMA_MCP_OAUTH_CLIENT_SECRET", "runtime-secret")
	t.Setenv("TMA_MCP_OAUTH_REFRESH_TOKEN", "runtime-refresh")
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse oauth token form: %v", err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "runtime-refresh" || r.Form.Get("client_id") != "runtime-client" || r.Form.Get("client_secret") != "runtime-secret" {
				t.Fatalf("unexpected refresh oauth token form: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"access_token": "runtime-refresh-oauth-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer runtime-refresh-oauth-token" {
				t.Fatalf("unexpected OAuth Authorization header: %q", r.Header.Get("Authorization"))
			}
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode MCP HTTP request: %v", err)
			}
			method, _ := request["method"].(string)
			id, _ := request["id"].(float64)
			w.Header().Set("Content-Type", "application/json")
			switch method {
			case "initialize":
				writeHTTPMCPTestMessage(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"serverInfo": map[string]any{"name": "Refresh OAuth MCP"},
					},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				writeHTTPMCPTestMessage(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "refreshPing",
							"description": "Ping over refresh-token protected streamable HTTP.",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
							"annotations": map[string]any{"readOnlyHint": true},
						}},
					},
				})
			case "tools/call":
				writeHTTPMCPTestMessage(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": "refresh pong",
						}},
					},
				})
			default:
				t.Fatalf("unexpected MCP HTTP method: %s", method)
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "refresh_remote",
		Transport:  mcppkg.TransportStreamableHTTP,
		URL:        server.URL + "/mcp",
		OAuth: &mcppkg.OAuthConfig{
			GrantType:    "refresh_token",
			TokenURL:     server.URL + "/token",
			ClientID:     envValuePtr(mcppkg.EnvRef("TMA_MCP_OAUTH_CLIENT_ID")),
			ClientSecret: envValuePtr(mcppkg.SecretRef("env:TMA_MCP_OAUTH_CLIENT_SECRET")),
			RefreshToken: envValuePtr(mcppkg.EnvRef("TMA_MCP_OAUTH_REFRESH_TOKEN")),
			Scopes:       []string{"mcp.read"},
			Resource:     "https://mcp.example.test/mcp",
		},
	})
	if err != nil {
		t.Fatalf("load refresh-token OAuth streamable_http MCP runtime: %v", err)
	}
	if tokenRequests != 1 {
		t.Fatalf("expected one refresh oauth token request, got %d", tokenRequests)
	}
	manifest := runtime.Manifest()
	if manifest.Identifier != "refresh_remote" || len(manifest.API) != 1 || manifest.API[0].Name != "refresh_ping" {
		t.Fatalf("unexpected refresh-token OAuth streamable_http manifest: %#v", manifest)
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_refresh_ping",
		Identifier: "refresh_remote",
		APIName:    "refresh_ping",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute refresh-token OAuth streamable_http MCP tool: %v", err)
	}
	if !strings.Contains(result.Content, "refresh pong") {
		t.Fatalf("unexpected refresh-token OAuth MCP result: %#v", result)
	}
	if tokenRequests != 1 {
		t.Fatalf("expected refresh OAuth token cache to reuse manifest-load token, got %d requests", tokenRequests)
	}
}

func TestLoadMCPRuntimeAllowsResourceOnlyServerWhenExposed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode MCP HTTP request: %v", err)
		}
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo":   map[string]any{"name": "Resource Only MCP"},
					"capabilities": map[string]any{"resources": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		case "resources/list":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resources": []map[string]any{{"uri": "fixture://resource-only", "name": "resource-only"}},
				},
			})
		case "resources/templates/list":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resourceTemplates": []map[string]any{{"uriTemplate": "fixture://resource/{id}", "name": "resource"}},
				},
			})
		case "resources/read":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"contents": []map[string]any{{"uri": "fixture://resource-only", "mimeType": "text/plain", "text": "resource-only body"}},
				},
			})
		default:
			t.Fatalf("unexpected MCP HTTP method: %s", method)
		}
	}))
	defer server.Close()

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "knowledge",
		Transport:  mcppkg.TransportStreamableHTTP,
		URL:        server.URL,
		Expose:     mcppkg.ExposeConfig{Resources: true},
	})
	if err != nil {
		t.Fatalf("load resource-only MCP runtime: %v", err)
	}
	if names := strings.Join(runtime.Capabilities.Names(), ","); names != "resources" {
		t.Fatalf("unexpected resource-only capabilities: %q", names)
	}
	manifest := runtime.Manifest()
	if len(manifest.API) != 3 || !manifestHasAPI(manifest, "mcp_list_resources", ToolRiskRead) || !manifestHasAPI(manifest, "mcp_list_resource_templates", ToolRiskRead) || !manifestHasAPI(manifest, "mcp_read_resource", ToolRiskRead) {
		t.Fatalf("expected resource bridge APIs only, got %#v", manifest.API)
	}

	templates, err := runtime.Execute(context.Background(), Call{
		ID:         "call_resource_templates_only",
		Identifier: "knowledge",
		APIName:    "mcp_list_resource_templates",
	}, ExecutionContext{})
	if err != nil || !strings.Contains(templates.Content, "fixture://resource/{id}") {
		t.Fatalf("execute resource-only templates: result=%#v err=%v", templates, err)
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_resource_only",
		Identifier: "knowledge",
		APIName:    "mcp_read_resource",
		Arguments:  json.RawMessage(`{"uri":"fixture://resource-only"}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute resource-only read: %v", err)
	}
	if !strings.Contains(result.Content, "resource-only body") || !strings.Contains(string(result.State), `"tma.mcp_context_result.v1"`) {
		t.Fatalf("unexpected resource-only result: content=%q state=%s", result.Content, result.State)
	}
}

func TestLoadMCPRuntimeAllowsPromptOnlyServerWhenExposed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode MCP HTTP request: %v", err)
		}
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo":   map[string]any{"name": "Prompt Only MCP"},
					"capabilities": map[string]any{"prompts": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		case "prompts/list":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"prompts": []map[string]any{{
						"name":        "brief",
						"title":       "Brief",
						"description": "Create a short brief.",
						"arguments": []map[string]any{{
							"name":     "topic",
							"required": true,
						}},
					}},
				},
			})
		case "prompts/get":
			writeHTTPMCPTestMessage(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"description": "Brief prompt",
					"messages": []map[string]any{{
						"role": "user",
						"content": map[string]any{
							"type": "text",
							"text": "Write a concise brief about prompt-only MCP.",
						},
					}},
				},
			})
		default:
			t.Fatalf("unexpected MCP HTTP method: %s", method)
		}
	}))
	defer server.Close()

	runtime, err := LoadMCPRuntime(t.Context(), mcppkg.ServerConfig{
		Identifier: "prompt_catalog",
		Transport:  mcppkg.TransportStreamableHTTP,
		URL:        server.URL,
		Expose:     mcppkg.ExposeConfig{Prompts: true},
	})
	if err != nil {
		t.Fatalf("load prompt-only MCP runtime: %v", err)
	}
	if names := strings.Join(runtime.Capabilities.Names(), ","); names != "prompts" {
		t.Fatalf("unexpected prompt-only capabilities: %q", names)
	}
	manifest := runtime.Manifest()
	if len(manifest.API) != 2 || !manifestHasAPI(manifest, "mcp_list_prompts", ToolRiskRead) || !manifestHasAPI(manifest, "mcp_get_prompt", ToolRiskRead) {
		t.Fatalf("expected prompt bridge APIs only, got %#v", manifest.API)
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_prompt_only",
		Identifier: "prompt_catalog",
		APIName:    "mcp_get_prompt",
		Arguments:  json.RawMessage(`{"name":"brief","arguments":{"topic":"MCP"}}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute prompt-only get: %v", err)
	}
	if !strings.Contains(result.Content, "prompt-only MCP") || !strings.Contains(string(result.State), `"tma.mcp_context_result.v1"`) {
		t.Fatalf("unexpected prompt-only result: content=%q state=%s", result.Content, result.State)
	}
}

func TestMCPClientFromConfigIncludesStreamableHTTPSettings(t *testing.T) {
	t.Setenv("TMA_MCP_HTTP_AUTH", "Bearer runtime-token")

	client, err := mcpClientFromConfig(mcppkg.ServerConfig{
		Identifier: "remote",
		Transport:  mcppkg.TransportStreamableHTTP,
		URL:        "https://mcp.example.test/mcp",
		Listen:     true,
		Roots: []mcppkg.Root{{
			URI:  "file:///workspace/project",
			Name: "Project",
		}},
		Sampling:    &mcppkg.SamplingConfig{Enabled: true},
		Elicitation: &mcppkg.ElicitationConfig{Enabled: true},
		Headers: map[string]mcppkg.EnvValue{
			"Authorization": mcppkg.EnvRef("TMA_MCP_HTTP_AUTH"),
		},
	})
	if err != nil {
		t.Fatalf("build streamable_http MCP client: %v", err)
	}
	if client.Transport != mcppkg.TransportStreamableHTTP || client.URL != "https://mcp.example.test/mcp" || !client.Listen {
		t.Fatalf("unexpected streamable_http client settings: %#v", client)
	}
	if client.Headers["Authorization"] != "Bearer runtime-token" {
		t.Fatalf("unexpected streamable_http client headers: %#v", client.Headers)
	}
	if len(client.Roots) != 1 || client.Roots[0].URI != "file:///workspace/project" {
		t.Fatalf("unexpected streamable_http client roots: %#v", client.Roots)
	}
	if client.Sampling == nil || !client.Sampling.Enabled {
		t.Fatalf("unexpected streamable_http client sampling config: %#v", client.Sampling)
	}
	if client.Elicitation == nil || !client.Elicitation.Enabled {
		t.Fatalf("unexpected streamable_http client elicitation config: %#v", client.Elicitation)
	}
}

func manifestHasAPI(manifest Manifest, name string, risk string) bool {
	for _, api := range manifest.API {
		if api.Name == name && api.Risk == risk {
			return true
		}
	}
	return false
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readTestMCPMessage(reader)
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
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo": map[string]any{
						"name":    "Stub MCP",
						"version": "1.0.0",
					},
					"capabilities": map[string]any{
						"tools":     map[string]any{},
						"resources": map[string]any{},
						"prompts":   map[string]any{},
					},
				},
			})
		case "tools/list":
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"tools": append([]map[string]any{
						{
							"name":        "readFile",
							"description": "Read a file from disk.",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"path": map[string]any{"type": "string"},
								},
								"required": []string{"path"},
							},
							"annotations": map[string]any{"readOnlyHint": true},
						},
						{
							"name":        "search-web",
							"description": "Search the web.",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"query": map[string]any{"type": "string"},
								},
							},
							"annotations": map[string]any{"readOnlyHint": true},
						},
					}, testMCPSecretTool()...),
				},
			})
		case "tools/call":
			params, _ := request["params"].(map[string]any)
			name, _ := params["name"].(string)
			arguments, _ := params["arguments"].(map[string]any)
			switch name {
			case "readFile":
				path, _ := arguments["path"].(string)
				writeTestMCPMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": "read file " + path,
						}},
						"structuredContent": map[string]any{
							"path": path,
						},
					},
				})
			case "secretStatus":
				writeTestMCPMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": "secret=" + os.Getenv("MCP_SECRET"),
						}},
					},
				})
			default:
				writeTestMCPMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"content": []map[string]any{{
							"type": "text",
							"text": "search completed",
						}},
					},
				})
			}
		case "resources/list":
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resources": []map[string]any{{
						"uri":         "fixture://guide",
						"name":        "guide",
						"title":       "Fixture Guide",
						"description": "A resource exposed by the MCP fixture.",
						"mimeType":    "text/plain",
					}},
				},
			})
		case "resources/templates/list":
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resourceTemplates": []map[string]any{{
						"uriTemplate": "fixture://guide/{section}",
						"name":        "guide-section",
						"title":       "Fixture Guide Section",
					}},
				},
			})
		case "resources/read":
			params, _ := request["params"].(map[string]any)
			uri, _ := params["uri"].(string)
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"contents": []map[string]any{{
						"uri":      uri,
						"mimeType": "text/plain",
						"text":     "resource body for " + uri,
					}},
				},
			})
		case "prompts/list":
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"prompts": []map[string]any{{
						"name":        "summarize",
						"title":       "Summarize",
						"description": "Summarize a topic.",
						"arguments": []map[string]any{{
							"name":     "topic",
							"required": true,
						}},
					}},
				},
			})
		case "prompts/get":
			params, _ := request["params"].(map[string]any)
			name, _ := params["name"].(string)
			arguments, _ := params["arguments"].(map[string]any)
			topic, _ := arguments["topic"].(string)
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"description": "Prompt " + name,
					"messages": []map[string]any{{
						"role": "user",
						"content": map[string]any{
							"type": "text",
							"text": "Summarize " + topic,
						},
					}},
				},
			})
		default:
			writeTestMCPMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error": map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
		}
	}
}

func testMCPSecretTool() []map[string]any {
	if os.Getenv("MCP_SECRET") != "runtime-secret" {
		return nil
	}
	return []map[string]any{{
		"name":        "secretStatus",
		"description": "Report whether MCP secret env was resolved.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		"annotations": map[string]any{"readOnlyHint": true},
	}}
}

func readTestMCPMessage(reader *bufio.Reader) ([]byte, error) {
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

func writeTestMCPMessage(message any) {
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

func writeHTTPMCPTestMessage(t *testing.T, w http.ResponseWriter, message any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(message); err != nil {
		t.Fatalf("write MCP HTTP message: %v", err)
	}
}

func envValuePtr(value mcppkg.EnvValue) *mcppkg.EnvValue {
	return &value
}
