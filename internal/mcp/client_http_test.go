package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamableHTTPClientListsToolsAndCallsTool(t *testing.T) {
	var sawSessionHeader bool
	var sawProtocolHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if accept := r.Header.Get("Accept"); !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			t.Fatalf("unexpected Accept header: %q", accept)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Mcp-Session-Id") == "session-json" {
			sawSessionHeader = true
		}
		if r.Header.Get("Mcp-Protocol-Version") == protocolVersion {
			sawProtocolHeader = true
		}
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-json")
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo":   map[string]any{"name": "HTTP MCP"},
					"capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}, "completions": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "ping",
						"description": "Ping over HTTP.",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						"annotations": map[string]any{"readOnlyHint": true},
					}},
				},
			})
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "pong"}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	client := Client{
		Transport: TransportStreamableHTTP,
		URL:       server.URL,
		Headers:   map[string]string{"Authorization": "Bearer test-token"},
	}
	initialized, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools over streamable_http: %v", err)
	}
	if initialized.ServerInfo.Name != "HTTP MCP" {
		t.Fatalf("unexpected server info: %#v", initialized.ServerInfo)
	}
	if names := strings.Join(initialized.Capabilities.Names(), ","); names != "completions,prompts,resources,tools" {
		t.Fatalf("unexpected capabilities: %q", names)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	if !sawSessionHeader {
		t.Fatalf("expected client to send Mcp-Session-Id after initialize")
	}
	if !sawProtocolHeader {
		t.Fatalf("expected client to send Mcp-Protocol-Version after initialize")
	}

	result, err := client.CallTool(t.Context(), "ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call tool over streamable_http: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "pong" {
		t.Fatalf("unexpected tool result: %#v", result)
	}
}

func TestStreamableHTTPClientRejectsConfiguredLoggingWithoutCapability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			writeHTTPTestJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": int(id), "result": map[string]any{"capabilities": map[string]any{"tools": map[string]any{}}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected MCP method after missing logging capability: %s", method)
		}
	}))
	defer server.Close()

	_, _, err := Client{Transport: TransportStreamableHTTP, URL: server.URL, LoggingLevel: "info"}.ListTools(t.Context())
	if err == nil || !strings.Contains(err.Error(), "does not declare logging capability") {
		t.Fatalf("expected missing logging capability error, got %v", err)
	}
}

func TestStreamableHTTPClientReadsSSEResponse(t *testing.T) {
	pingReply := make(chan struct{})
	var closePing sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		if request["id"] == "post-sse-ping" {
			if _, ok := request["result"].(map[string]any); !ok {
				t.Fatalf("unexpected POST SSE ping response: %#v", request)
			}
			closePing.Do(func() { close(pingReply) })
			w.WriteHeader(http.StatusAccepted)
			return
		}
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		switch method {
		case "initialize":
			w.Header().Set("Content-Type", "text/event-stream")
			writeHTTPTestSSE(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "SSE MCP"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			writeHTTPTestSSE(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      "post-sse-ping",
				"method":  "ping",
				"params":  map[string]any{},
			})
			writeHTTPTestSSE(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "streamPing",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	_, tools, err := Client{Transport: TransportStreamableHTTP, URL: server.URL}.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools from SSE stream: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "streamPing" {
		t.Fatalf("unexpected SSE tools: %#v", tools)
	}
	select {
	case <-pingReply:
	default:
		t.Fatal("expected client to reply to server request in POST SSE response")
	}
}

func TestStreamableHTTPClientListsPaginatedCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		params, _ := request["params"].(map[string]any)
		cursor, _ := params["cursor"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Paged MCP"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if cursor == "" {
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools":      []map[string]any{{"name": "firstTool"}},
						"nextCursor": "tools-page-2",
					},
				})
				return
			}
			if cursor != "tools-page-2" {
				t.Fatalf("unexpected tools cursor: %q", cursor)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"tools": []map[string]any{{"name": "secondTool"}}},
			})
		case "resources/list":
			if cursor == "" {
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"resources":  []map[string]any{{"uri": "file:///first.txt", "name": "first"}},
						"nextCursor": "resources-page-2",
					},
				})
				return
			}
			if cursor != "resources-page-2" {
				t.Fatalf("unexpected resources cursor: %q", cursor)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"resources": []map[string]any{{"uri": "file:///second.txt", "name": "second"}}},
			})
		case "prompts/list":
			if cursor == "" {
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"prompts":    []map[string]any{{"name": "firstPrompt"}},
						"nextCursor": "prompts-page-2",
					},
				})
				return
			}
			if cursor != "prompts-page-2" {
				t.Fatalf("unexpected prompts cursor: %q", cursor)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"prompts": []map[string]any{{"name": "secondPrompt"}}},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	client := Client{Transport: TransportStreamableHTTP, URL: server.URL}
	_, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list paginated tools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "firstTool" || tools[1].Name != "secondTool" {
		t.Fatalf("unexpected paginated tools: %#v", tools)
	}

	_, resources, err := client.ListResources(t.Context())
	if err != nil {
		t.Fatalf("list paginated resources: %v", err)
	}
	if len(resources) != 2 || resources[0].URI != "file:///first.txt" || resources[1].URI != "file:///second.txt" {
		t.Fatalf("unexpected paginated resources: %#v", resources)
	}

	_, prompts, err := client.ListPrompts(t.Context())
	if err != nil {
		t.Fatalf("list paginated prompts: %v", err)
	}
	if len(prompts) != 2 || prompts[0].Name != "firstPrompt" || prompts[1].Name != "secondPrompt" {
		t.Fatalf("unexpected paginated prompts: %#v", prompts)
	}
}

func TestStreamableHTTPClientDegradesUnsupportedOptionalLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Optional Lists MCP"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list", "resources/templates/list", "prompts/list":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	client := Client{Transport: TransportStreamableHTTP, URL: server.URL}
	_, resources, err := client.ListResources(t.Context())
	if err != nil {
		t.Fatalf("unsupported resources/list should degrade: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected no resources for unsupported list, got %#v", resources)
	}

	_, prompts, err := client.ListPrompts(t.Context())
	if err != nil {
		t.Fatalf("unsupported prompts/list should degrade: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("expected no prompts for unsupported list, got %#v", prompts)
	}

	_, templates, err := client.ListResourceTemplates(t.Context())
	if err != nil {
		t.Fatalf("unsupported resources/templates/list should degrade: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("expected no resource templates for unsupported list, got %#v", templates)
	}
}

func TestStreamableHTTPClientDoesNotDegradeUnsupportedToolsList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "No Tools MCP"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	_, _, err := Client{Transport: TransportStreamableHTTP, URL: server.URL}.ListTools(t.Context())
	if err == nil {
		t.Fatalf("expected unsupported tools/list to remain an error")
	}
	if !strings.Contains(err.Error(), "tools/list failed (-32601)") {
		t.Fatalf("unexpected unsupported tools/list error: %v", err)
	}
}

func TestStreamableHTTPClientListsAndReadsResources(t *testing.T) {
	var sawSessionHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		if r.Header.Get("Mcp-Session-Id") == "session-resources" {
			sawSessionHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-resources")
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Resource MCP"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resources": []map[string]any{{
						"uri":         "file:///workspace/README.md",
						"name":        "readme",
						"title":       "README",
						"description": "Project readme",
						"mimeType":    "text/markdown",
					}},
				},
			})
		case "resources/read":
			params, _ := request["params"].(map[string]any)
			if params["uri"] != "file:///workspace/README.md" {
				t.Fatalf("unexpected resource read params: %#v", params)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"contents": []map[string]any{{
						"uri":      "file:///workspace/README.md",
						"mimeType": "text/markdown",
						"text":     "# Demo",
					}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	client := Client{Transport: TransportStreamableHTTP, URL: server.URL}
	initialized, resources, err := client.ListResources(t.Context())
	if err != nil {
		t.Fatalf("list resources over streamable_http: %v", err)
	}
	if initialized.ServerInfo.Name != "Resource MCP" {
		t.Fatalf("unexpected server info: %#v", initialized.ServerInfo)
	}
	if len(resources) != 1 || resources[0].URI != "file:///workspace/README.md" || resources[0].MimeType != "text/markdown" {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	if !sawSessionHeader {
		t.Fatalf("expected resources/list to reuse Mcp-Session-Id after initialize")
	}

	result, err := client.ReadResource(t.Context(), "file:///workspace/README.md")
	if err != nil {
		t.Fatalf("read resource over streamable_http: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "# Demo" {
		t.Fatalf("unexpected resource contents: %#v", result)
	}
}

func TestStreamableHTTPClientListsResourceTemplatesAndCompletesArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		params, _ := request["params"].(map[string]any)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo":   map[string]any{"name": "Template MCP"},
					"capabilities": map[string]any{"resources": map[string]any{}, "completions": map[string]any{}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/templates/list":
			cursor, _ := params["cursor"].(string)
			if cursor == "" {
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"resourceTemplates": []map[string]any{{"uriTemplate": "repo:///{owner}/{name}", "name": "repository"}},
						"nextCursor":        "template-page-2",
					},
				})
				return
			}
			if cursor != "template-page-2" {
				t.Fatalf("unexpected resource template cursor: %q", cursor)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resourceTemplates": []map[string]any{{"uriTemplate": "issue:///{id}", "name": "issue"}},
				},
			})
		case "completion/complete":
			reference, _ := params["ref"].(map[string]any)
			argument, _ := params["argument"].(map[string]any)
			completionContext, _ := params["context"].(map[string]any)
			contextArguments, _ := completionContext["arguments"].(map[string]any)
			if reference["type"] != CompletionReferenceResource || reference["uri"] != "repo:///{owner}/{name}" || argument["name"] != "owner" || argument["value"] != "op" || contextArguments["name"] != "codex" {
				t.Fatalf("unexpected completion params: %#v", params)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"completion": map[string]any{"values": []string{"openai", "openapi"}, "total": 2},
				},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	client := Client{Transport: TransportStreamableHTTP, URL: server.URL}
	initialized, templates, err := client.ListResourceTemplates(t.Context())
	if err != nil {
		t.Fatalf("list resource templates over streamable_http: %v", err)
	}
	if names := strings.Join(initialized.Capabilities.Names(), ","); names != "completions,resources" {
		t.Fatalf("unexpected template server capabilities: %q", names)
	}
	if len(templates) != 2 || templates[0].Name != "repository" || templates[1].URITemplate != "issue:///{id}" {
		t.Fatalf("unexpected resource templates: %#v", templates)
	}

	result, err := client.Complete(t.Context(), CompletionReference{
		Type: CompletionReferenceResource,
		URI:  "repo:///{owner}/{name}",
	}, CompletionArgument{Name: "owner", Value: "op"}, CompletionContext{
		Arguments: map[string]string{"name": "codex"},
	})
	if err != nil {
		t.Fatalf("complete resource argument over streamable_http: %v", err)
	}
	if len(result.Completion.Values) != 2 || result.Completion.Values[0] != "openai" || result.Completion.Total != 2 {
		t.Fatalf("unexpected HTTP completion result: %#v", result)
	}
}

func TestStreamableHTTPClientListsAndGetsPrompts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeHTTPTestMCPRequest(t, r)
		method, _ := request["method"].(string)
		id, _ := request["id"].(float64)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Prompt MCP"}},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "prompts/list":
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"prompts": []map[string]any{{
						"name":        "summarize",
						"title":       "Summarize",
						"description": "Summarize input text.",
						"arguments": []map[string]any{{
							"name":        "topic",
							"description": "Topic to summarize.",
							"required":    true,
						}},
					}},
				},
			})
		case "prompts/get":
			params, _ := request["params"].(map[string]any)
			if params["name"] != "summarize" {
				t.Fatalf("unexpected prompt get params: %#v", params)
			}
			arguments, _ := params["arguments"].(map[string]any)
			if arguments["topic"] != "MCP" {
				t.Fatalf("unexpected prompt arguments: %#v", arguments)
			}
			writeHTTPTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"description": "Prompt for summarization.",
					"messages": []map[string]any{{
						"role": "user",
						"content": map[string]any{
							"type": "text",
							"text": "Summarize MCP",
						},
					}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method: %s", method)
		}
	}))
	defer server.Close()

	client := Client{Transport: TransportStreamableHTTP, URL: server.URL}
	initialized, prompts, err := client.ListPrompts(t.Context())
	if err != nil {
		t.Fatalf("list prompts over streamable_http: %v", err)
	}
	if initialized.ServerInfo.Name != "Prompt MCP" {
		t.Fatalf("unexpected server info: %#v", initialized.ServerInfo)
	}
	if len(prompts) != 1 || prompts[0].Name != "summarize" || len(prompts[0].Arguments) != 1 || !prompts[0].Arguments[0].Required {
		t.Fatalf("unexpected prompts: %#v", prompts)
	}

	result, err := client.GetPrompt(t.Context(), "summarize", json.RawMessage(`{"topic":"MCP"}`))
	if err != nil {
		t.Fatalf("get prompt over streamable_http: %v", err)
	}
	if result.Description != "Prompt for summarization." || len(result.Messages) != 1 || result.Messages[0].Content.Text != "Summarize MCP" {
		t.Fatalf("unexpected prompt result: %#v", result)
	}
}

func TestStreamableHTTPListenerRepliesUnsupportedServerRequestAndReconnects(t *testing.T) {
	rootsReply := make(chan struct{})
	reconnected := make(chan struct{})
	var closeRoots sync.Once
	var closeReconnected sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.Header.Get("Mcp-Session-Id") != "session-listener" {
				t.Fatalf("unexpected listener session header: %q", r.Header.Get("Mcp-Session-Id"))
			}
			if r.Header.Get("Mcp-Protocol-Version") != protocolVersion {
				t.Fatalf("unexpected listener protocol header: %q", r.Header.Get("Mcp-Protocol-Version"))
			}
			if r.Header.Get("Last-Event-ID") == "evt-1" {
				closeReconnected.Do(func() { close(reconnected) })
				w.Header().Set("Content-Type", "text/event-stream")
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprintf(w, "id: evt-1\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"roots-99\",\"method\":\"roots/list\",\"params\":{}}\n\n"); err != nil {
				t.Fatalf("write listener event: %v", err)
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		case http.MethodPost:
			request := decodeHTTPTestMCPRequest(t, r)
			method, _ := request["method"].(string)
			if request["id"] == "roots-99" {
				result, _ := request["result"].(map[string]any)
				roots, _ := result["roots"].([]any)
				if len(roots) != 1 {
					t.Fatalf("unexpected roots/list reply: %#v", request)
				}
				root, _ := roots[0].(map[string]any)
				if root["uri"] != "file:///workspace/project" || root["name"] != "Project" {
					t.Fatalf("unexpected root payload: %#v", root)
				}
				closeRoots.Do(func() { close(rootsReply) })
				w.WriteHeader(http.StatusAccepted)
				return
			}
			id, _ := request["id"].(float64)
			switch method {
			case "initialize":
				params, _ := request["params"].(map[string]any)
				capabilities, _ := params["capabilities"].(map[string]any)
				rootsCapability, _ := capabilities["roots"].(map[string]any)
				if rootsCapability == nil || rootsCapability["listChanged"] != false {
					t.Fatalf("expected roots client capability: %#v", request)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Mcp-Session-Id", "session-listener")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result":  map[string]any{"serverInfo": map[string]any{"name": "Listener MCP"}},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				select {
				case <-rootsReply:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for roots/list reply")
				}
				select {
				case <-reconnected:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for listener reconnect with Last-Event-ID")
				}
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "listenerPing",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						}},
					},
				})
			default:
				t.Fatalf("unexpected MCP POST method: %s", method)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	_, tools, err := Client{
		Transport: TransportStreamableHTTP,
		URL:       server.URL,
		Listen:    true,
		Roots: []Root{{
			URI:  "file:///workspace/project",
			Name: "Project",
		}},
	}.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools with streamable_http listener: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "listenerPing" {
		t.Fatalf("unexpected listener tools: %#v", tools)
	}
}

func TestStreamableHTTPListenerRepliesUnsupportedRequestFallback(t *testing.T) {
	unsupportedReply := make(chan struct{})
	var closeUnsupported sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":77,\"method\":\"workspace/unknown\",\"params\":{}}\n\n"); err != nil {
				t.Fatalf("write listener event: %v", err)
			}
			return
		case http.MethodPost:
			request := decodeHTTPTestMCPRequest(t, r)
			method, _ := request["method"].(string)
			id, _ := request["id"].(float64)
			if int(id) == 77 {
				errorPayload, _ := request["error"].(map[string]any)
				if code, _ := errorPayload["code"].(float64); int(code) != -32601 {
					t.Fatalf("unexpected unsupported fallback reply: %#v", request)
				}
				closeUnsupported.Do(func() { close(unsupportedReply) })
				w.WriteHeader(http.StatusAccepted)
				return
			}
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result":  map[string]any{"serverInfo": map[string]any{"name": "Fallback MCP"}},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				select {
				case <-unsupportedReply:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for unsupported fallback reply")
				}
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "fallbackPing",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						}},
					},
				})
			default:
				t.Fatalf("unexpected MCP POST method: %s", method)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	_, tools, err := Client{Transport: TransportStreamableHTTP, URL: server.URL, Listen: true}.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools with unsupported request fallback: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fallbackPing" {
		t.Fatalf("unexpected fallback tools: %#v", tools)
	}
}

func TestStreamableHTTPListenerRejectsSamplingCreateMessage(t *testing.T) {
	samplingReply := make(chan struct{})
	var closeSampling sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":88,\"method\":\"sampling/createMessage\",\"params\":{}}\n\n"); err != nil {
				t.Fatalf("write listener event: %v", err)
			}
			return
		case http.MethodPost:
			request := decodeHTTPTestMCPRequest(t, r)
			method, _ := request["method"].(string)
			id, _ := request["id"].(float64)
			if int(id) == 88 {
				errorPayload, _ := request["error"].(map[string]any)
				if code, _ := errorPayload["code"].(float64); int(code) != -32000 {
					t.Fatalf("unexpected sampling policy reply: %#v", request)
				}
				message, _ := errorPayload["message"].(string)
				if !strings.Contains(message, "disabled") {
					t.Fatalf("unexpected sampling policy message: %#v", request)
				}
				closeSampling.Do(func() { close(samplingReply) })
				w.WriteHeader(http.StatusAccepted)
				return
			}
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result":  map[string]any{"serverInfo": map[string]any{"name": "Sampling MCP"}},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				select {
				case <-samplingReply:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for sampling policy reply")
				}
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "samplingPing",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						}},
					},
				})
			default:
				t.Fatalf("unexpected MCP POST method: %s", method)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	_, tools, err := Client{Transport: TransportStreamableHTTP, URL: server.URL, Listen: true}.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools with sampling policy reply: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "samplingPing" {
		t.Fatalf("unexpected sampling tools: %#v", tools)
	}
}

func TestStreamableHTTPListenerRejectsElicitationCreate(t *testing.T) {
	elicitationReply := make(chan struct{})
	var closeElicitation sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":89,\"method\":\"elicitation/create\",\"params\":{}}\n\n"); err != nil {
				t.Fatalf("write listener event: %v", err)
			}
			return
		case http.MethodPost:
			request := decodeHTTPTestMCPRequest(t, r)
			method, _ := request["method"].(string)
			id, _ := request["id"].(float64)
			if int(id) == 89 {
				errorPayload, _ := request["error"].(map[string]any)
				if code, _ := errorPayload["code"].(float64); int(code) != -32000 {
					t.Fatalf("unexpected elicitation policy reply: %#v", request)
				}
				message, _ := errorPayload["message"].(string)
				if !strings.Contains(message, "disabled") {
					t.Fatalf("unexpected elicitation policy message: %#v", request)
				}
				closeElicitation.Do(func() { close(elicitationReply) })
				w.WriteHeader(http.StatusAccepted)
				return
			}
			switch method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result":  map[string]any{"serverInfo": map[string]any{"name": "Elicitation MCP"}},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/list":
				select {
				case <-elicitationReply:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for elicitation policy reply")
				}
				w.Header().Set("Content-Type", "application/json")
				writeHTTPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "elicitationPing",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						}},
					},
				})
			default:
				t.Fatalf("unexpected MCP POST method: %s", method)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	_, tools, err := Client{Transport: TransportStreamableHTTP, URL: server.URL, Listen: true}.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools with elicitation policy reply: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "elicitationPing" {
		t.Fatalf("unexpected elicitation tools: %#v", tools)
	}
}

func decodeHTTPTestMCPRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatalf("decode MCP HTTP request: %v", err)
	}
	return request
}

func writeHTTPTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write HTTP MCP JSON response: %v", err)
	}
}

func writeHTTPTestSSE(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode SSE data: %v", err)
	}
	if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw); err != nil {
		t.Fatalf("write SSE data: %v", err)
	}
}
