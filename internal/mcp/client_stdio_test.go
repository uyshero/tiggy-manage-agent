package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStdioClientListsAndReadsResources(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPResourceHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_RESOURCE_HELPER": "1"},
	}
	initialized, resources, err := client.ListResources(t.Context())
	if err != nil {
		t.Fatalf("list resources over stdio: %v", err)
	}
	if initialized.ServerInfo.Name != "Resource Helper MCP" {
		t.Fatalf("unexpected server info: %#v", initialized.ServerInfo)
	}
	if len(resources) != 2 || resources[0].URI != "file:///tmp/demo.txt" || resources[1].URI != "file:///tmp/second.txt" {
		t.Fatalf("unexpected resources: %#v", resources)
	}

	result, err := client.ReadResource(t.Context(), "file:///tmp/demo.txt")
	if err != nil {
		t.Fatalf("read resource over stdio: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "demo resource" {
		t.Fatalf("unexpected resource read result: %#v", result)
	}
}

func TestStdioClientListsResourceTemplatesAndCompletesArguments(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPResourceHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_RESOURCE_HELPER": "1"},
	}
	_, templates, err := client.ListResourceTemplates(t.Context())
	if err != nil {
		t.Fatalf("list resource templates over stdio: %v", err)
	}
	if len(templates) != 2 || templates[0].URITemplate != "file:///{path}" || templates[1].Name != "issue" {
		t.Fatalf("unexpected resource templates: %#v", templates)
	}

	result, err := client.Complete(t.Context(), CompletionReference{
		Type: CompletionReferencePrompt,
		Name: "summarize",
	}, CompletionArgument{Name: "topic", Value: "MC"}, CompletionContext{
		Arguments: map[string]string{"audience": "engineers"},
	})
	if err != nil {
		t.Fatalf("complete prompt argument over stdio: %v", err)
	}
	if len(result.Completion.Values) != 2 || result.Completion.Values[0] != "MCP" || result.Completion.Total != 2 || result.Completion.HasMore {
		t.Fatalf("unexpected completion result: %#v", result)
	}
}

func TestStdioClientSupportsJSONLinesFraming(t *testing.T) {
	client := Client{
		StdioFraming: StdioFramingJSONLines,
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestMCPJSONLinesHelperProcess"},
		Env:          map[string]string{"GO_WANT_MCP_JSON_LINES_HELPER": "1"},
	}
	initialized, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools over JSON Lines stdio: %v", err)
	}
	if initialized.ProtocolVersion != protocolVersion || initialized.ServerInfo.Name != "JSON Lines MCP" || len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("unexpected JSON Lines catalog: initialized=%#v tools=%#v", initialized, tools)
	}
	result, err := client.CallTool(t.Context(), "ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call tool over JSON Lines stdio: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "pong" {
		t.Fatalf("unexpected JSON Lines result: %#v", result)
	}
}

func TestReadResourceRequiresURI(t *testing.T) {
	_, err := Client{Command: os.Args[0]}.ReadResource(t.Context(), "  ")
	if err == nil {
		t.Fatalf("expected missing resource uri error")
	}
	if !strings.Contains(err.Error(), "uri is required") {
		t.Fatalf("unexpected missing resource uri error: %v", err)
	}
}

func TestStdioClientListsAndGetsPrompts(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPResourceHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_RESOURCE_HELPER": "1"},
	}
	initialized, prompts, err := client.ListPrompts(t.Context())
	if err != nil {
		t.Fatalf("list prompts over stdio: %v", err)
	}
	if initialized.ServerInfo.Name != "Resource Helper MCP" {
		t.Fatalf("unexpected server info: %#v", initialized.ServerInfo)
	}
	if len(prompts) != 2 || prompts[0].Name != "summarize" || prompts[1].Name != "expand" || len(prompts[0].Arguments) != 1 {
		t.Fatalf("unexpected prompts: %#v", prompts)
	}

	result, err := client.GetPrompt(t.Context(), "summarize", json.RawMessage(`{"topic":"MCP"}`))
	if err != nil {
		t.Fatalf("get prompt over stdio: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Content.Text != "Summarize MCP" {
		t.Fatalf("unexpected prompt get result: %#v", result)
	}
}

func TestGetPromptRequiresName(t *testing.T) {
	_, err := Client{Command: os.Args[0]}.GetPrompt(t.Context(), "  ", nil)
	if err == nil {
		t.Fatalf("expected missing prompt name error")
	}
	if !strings.Contains(err.Error(), "prompt name is required") {
		t.Fatalf("unexpected missing prompt name error: %v", err)
	}
}

func TestStdioClientListsPaginatedTools(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPResourceHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_RESOURCE_HELPER": "1"},
	}
	_, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools over stdio: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "firstTool" || tools[1].Name != "secondTool" {
		t.Fatalf("unexpected paginated tools: %#v", tools)
	}
}

func TestStdioClientDegradesUnsupportedOptionalLists(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPUnsupportedCapabilityHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_UNSUPPORTED_HELPER": "1"},
	}
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

func TestCompletionValidation(t *testing.T) {
	client := Client{Command: os.Args[0]}
	for _, testCase := range []struct {
		name      string
		reference CompletionReference
		argument  CompletionArgument
		want      string
	}{
		{name: "unknown-reference", reference: CompletionReference{Type: "unknown"}, argument: CompletionArgument{Name: "topic"}, want: "reference type"},
		{name: "missing-prompt", reference: CompletionReference{Type: CompletionReferencePrompt}, argument: CompletionArgument{Name: "topic"}, want: "prompt name"},
		{name: "missing-resource", reference: CompletionReference{Type: CompletionReferenceResource}, argument: CompletionArgument{Name: "path"}, want: "resource uri"},
		{name: "missing-argument", reference: CompletionReference{Type: CompletionReferencePrompt, Name: "demo"}, want: "argument name"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := client.Complete(t.Context(), testCase.reference, testCase.argument, CompletionContext{})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("unexpected completion validation error: %v", err)
			}
		})
	}

	values := make([]string, 101)
	if _, err := validateCompletionResult(CompletionResult{Completion: CompletionValues{Values: values}}); err == nil || !strings.Contains(err.Error(), "maximum is 100") {
		t.Fatalf("unexpected oversized completion error: %v", err)
	}
}

func TestStdioClientDoesNotDegradeUnsupportedToolsList(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPUnsupportedCapabilityHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_UNSUPPORTED_HELPER": "1"},
	}
	_, _, err := client.ListTools(t.Context())
	if err == nil {
		t.Fatalf("expected unsupported tools/list to remain an error")
	}
	if !strings.Contains(err.Error(), "tools/list failed (-32601)") {
		t.Fatalf("unexpected unsupported tools/list error: %v", err)
	}
}

func TestStdioClientReturnsContextErrorOnTimeout(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHangingHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_HANGING_HELPER": "1"},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := client.CallTool(ctx, "hang", json.RawMessage(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if strings.Contains(err.Error(), "read mcp header") || strings.Contains(err.Error(), "EOF") {
		t.Fatalf("expected context-level timeout, got transport detail: %v", err)
	}
}

func TestStdioClientSendsCancelledNotificationOnTimeout(t *testing.T) {
	markerPath := t.TempDir() + "/cancelled.json"
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHangingHelperProcess"},
		Env: map[string]string{
			"GO_WANT_MCP_HANGING_HELPER": "1",
			"MCP_CANCEL_MARKER":          markerPath,
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := client.CallTool(ctx, "hang", json.RawMessage(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}

	raw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected cancellation marker: %v", err)
	}
	var notification struct {
		Method string `json:"method"`
		Params struct {
			RequestID float64 `json:"requestId"`
			Reason    string  `json:"reason"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &notification); err != nil {
		t.Fatalf("decode cancellation marker: %v", err)
	}
	if notification.Method != "notifications/cancelled" {
		t.Fatalf("unexpected cancellation method: %#v", notification)
	}
	if notification.Params.RequestID != 2 {
		t.Fatalf("expected tools/call request id 2, got %#v", notification.Params.RequestID)
	}
	if notification.Params.Reason != context.DeadlineExceeded.Error() {
		t.Fatalf("unexpected cancellation reason: %q", notification.Params.Reason)
	}
}

func TestStdioClientHandlesServerRequests(t *testing.T) {
	client := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPServerRequestHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_SERVER_REQUEST_HELPER": "1"},
		Roots: []Root{{
			URI:  "file:///workspace/project",
			Name: "Project",
		}},
		Sampling:    &SamplingConfig{Enabled: true},
		Elicitation: &ElicitationConfig{Enabled: true},
	}

	_, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list tools while handling stdio server requests: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "serverRequestPing" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
}

func TestMCPServerRequestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_SERVER_REQUEST_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readResourceHelperMessage(reader)
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
		id := request["id"]
		switch method {
		case "initialize":
			assertServerRequestInitializeCapabilities(request)
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Server Request MCP"}},
			})
		case "tools/list":
			assertStdioServerRequestRoundTrips(reader)
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "serverRequestPing",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					}},
				},
			})
		default:
			panic("unexpected client method: " + method)
		}
	}
}

func assertServerRequestInitializeCapabilities(request map[string]any) {
	params, _ := request["params"].(map[string]any)
	capabilities, _ := params["capabilities"].(map[string]any)
	roots, _ := capabilities["roots"].(map[string]any)
	if roots == nil || roots["listChanged"] != false {
		panic(fmt.Sprintf("expected roots capability, got %#v", capabilities))
	}
	if _, ok := capabilities["sampling"]; ok {
		panic("sampling must not be advertised before an audited backend exists")
	}
	if _, ok := capabilities["elicitation"]; ok {
		panic("elicitation must not be advertised before an interaction backend exists")
	}
}

func assertStdioServerRequestRoundTrips(reader *bufio.Reader) {
	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": "roots-request", "method": "roots/list", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": float64(71), "method": "ping", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": "sampling-request", "method": "sampling/createMessage", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": "elicitation-request", "method": "elicitation/create", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": "unknown-request", "method": "workspace/unknown", "params": map[string]any{}},
	}
	for _, request := range requests {
		writeResourceHelperMessage(request)
		payload, err := readResourceHelperMessage(reader)
		if err != nil {
			panic(err)
		}
		var response map[string]any
		if err := json.Unmarshal(payload, &response); err != nil {
			panic(err)
		}
		if response["id"] != request["id"] {
			panic(fmt.Sprintf("server request id changed: request=%#v response=%#v", request, response))
		}
		assertStdioServerRequestResponse(request["method"].(string), response)
	}
}

func assertStdioServerRequestResponse(method string, response map[string]any) {
	switch method {
	case "roots/list":
		result, _ := response["result"].(map[string]any)
		roots, _ := result["roots"].([]any)
		if len(roots) != 1 {
			panic(fmt.Sprintf("unexpected roots response: %#v", response))
		}
		root, _ := roots[0].(map[string]any)
		if root["uri"] != "file:///workspace/project" || root["name"] != "Project" {
			panic(fmt.Sprintf("unexpected root response: %#v", response))
		}
	case "ping":
		if _, ok := response["result"].(map[string]any); !ok {
			panic(fmt.Sprintf("unexpected ping response: %#v", response))
		}
	case "sampling/createMessage", "elicitation/create":
		errorPayload, _ := response["error"].(map[string]any)
		if errorPayload["code"] != float64(-32000) || !strings.Contains(fmt.Sprint(errorPayload["message"]), "not implemented") {
			panic(fmt.Sprintf("unexpected policy response: %#v", response))
		}
	default:
		errorPayload, _ := response["error"].(map[string]any)
		if errorPayload["code"] != float64(-32601) {
			panic(fmt.Sprintf("unexpected fallback response: %#v", response))
		}
	}
}

func TestMCPResourceHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_RESOURCE_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readResourceHelperMessage(reader)
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
		if method == "notifications/cancelled" {
			if markerPath := os.Getenv("MCP_CANCEL_MARKER"); markerPath != "" {
				if err := os.WriteFile(markerPath, payload, 0o600); err != nil {
					panic(err)
				}
			}
			continue
		}
		if method == "notifications/initialized" {
			continue
		}
		id, _ := request["id"].(float64)
		params, _ := request["params"].(map[string]any)
		cursor, _ := params["cursor"].(string)
		switch method {
		case "initialize":
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"serverInfo": map[string]any{"name": "Resource Helper MCP"},
				},
			})
		case "tools/list":
			if cursor == "" {
				writeResourceHelperMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"tools":      []map[string]any{{"name": "firstTool"}},
						"nextCursor": "tools-page-2",
					},
				})
				continue
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"tools": []map[string]any{{"name": "secondTool"}}},
			})
		case "resources/list":
			if cursor == "" {
				writeResourceHelperMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"resources": []map[string]any{{
							"uri":      "file:///tmp/demo.txt",
							"name":     "demo",
							"mimeType": "text/plain",
						}},
						"nextCursor": "resources-page-2",
					},
				})
				continue
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resources": []map[string]any{{
						"uri":      "file:///tmp/second.txt",
						"name":     "second",
						"mimeType": "text/plain",
					}},
				},
			})
		case "resources/read":
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"contents": []map[string]any{{
						"uri":      "file:///tmp/demo.txt",
						"mimeType": "text/plain",
						"text":     "demo resource",
					}},
				},
			})
		case "resources/templates/list":
			if cursor == "" {
				writeResourceHelperMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"resourceTemplates": []map[string]any{{
							"uriTemplate": "file:///{path}",
							"name":        "file",
							"title":       "File",
						}},
						"nextCursor": "templates-page-2",
					},
				})
				continue
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"resourceTemplates": []map[string]any{{
						"uriTemplate": "issue:///{id}",
						"name":        "issue",
					}},
				},
			})
		case "prompts/list":
			if cursor == "" {
				writeResourceHelperMessage(map[string]any{
					"jsonrpc": "2.0",
					"id":      int(id),
					"result": map[string]any{
						"prompts": []map[string]any{{
							"name":        "summarize",
							"description": "Summarize a topic.",
							"arguments": []map[string]any{{
								"name":     "topic",
								"required": true,
							}},
						}},
						"nextCursor": "prompts-page-2",
					},
				})
				continue
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"prompts": []map[string]any{{
						"name":        "expand",
						"description": "Expand a topic.",
					}},
				},
			})
		case "prompts/get":
			writeResourceHelperMessage(map[string]any{
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
		case "completion/complete":
			reference, _ := params["ref"].(map[string]any)
			argument, _ := params["argument"].(map[string]any)
			completionContext, _ := params["context"].(map[string]any)
			contextArguments, _ := completionContext["arguments"].(map[string]any)
			if reference["type"] != CompletionReferencePrompt || reference["name"] != "summarize" || argument["name"] != "topic" || argument["value"] != "MC" || contextArguments["audience"] != "engineers" {
				panic(fmt.Sprintf("unexpected completion params: %#v", params))
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result": map[string]any{
					"completion": map[string]any{
						"values":  []string{"MCP", "MCP Client"},
						"total":   2,
						"hasMore": false,
					},
				},
			})
		default:
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
}

func TestMCPUnsupportedCapabilityHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_UNSUPPORTED_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readResourceHelperMessage(reader)
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
		if method == "notifications/cancelled" {
			if markerPath := os.Getenv("MCP_CANCEL_MARKER"); markerPath != "" {
				if err := os.WriteFile(markerPath, payload, 0o600); err != nil {
					panic(err)
				}
			}
			continue
		}
		if method == "notifications/initialized" {
			continue
		}
		id, _ := request["id"].(float64)
		switch method {
		case "initialize":
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Unsupported Capability MCP"}},
			})
		default:
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
}

func TestMCPHangingHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HANGING_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readResourceHelperMessage(reader)
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
		if method == "notifications/cancelled" {
			if markerPath := os.Getenv("MCP_CANCEL_MARKER"); markerPath != "" {
				if err := os.WriteFile(markerPath, payload, 0o600); err != nil {
					panic(err)
				}
			}
			continue
		}
		if method == "notifications/initialized" {
			continue
		}
		id, _ := request["id"].(float64)
		switch method {
		case "initialize":
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"result":  map[string]any{"serverInfo": map[string]any{"name": "Hanging MCP"}},
			})
		case "tools/call":
			if os.Getenv("MCP_CANCEL_MARKER") != "" {
				continue
			}
			for {
				time.Sleep(time.Second)
			}
		default:
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      int(id),
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
}

func TestMCPJSONLinesHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_JSON_LINES_HELPER") != "1" {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			os.Exit(0)
		}
		if err != nil {
			panic(err)
		}
		var request map[string]any
		if err := json.Unmarshal(line, &request); err != nil {
			panic(err)
		}
		method, _ := request["method"].(string)
		if method == "notifications/initialized" || method == "notifications/cancelled" {
			continue
		}
		id, _ := request["id"].(float64)
		var result any
		switch method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": protocolVersion,
				"serverInfo":      map[string]any{"name": "JSON Lines MCP", "version": "1.0.0"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "ping", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}
		default:
			writeJSONLine(map[string]any{"jsonrpc": "2.0", "id": int(id), "error": map[string]any{"code": -32601, "message": "method not found"}})
			continue
		}
		writeJSONLine(map[string]any{"jsonrpc": "2.0", "id": int(id), "result": result})
	}
}

func writeJSONLine(message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(append(payload, '\n')); err != nil {
		panic(err)
	}
}

func readResourceHelperMessage(reader *bufio.Reader) ([]byte, error) {
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

func writeResourceHelperMessage(message any) {
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
