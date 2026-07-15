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

func TestStdioHostReusesSessionAndReapsIdleProcess(t *testing.T) {
	startMarker := t.TempDir() + "/starts.log"
	host := NewStdioHost(StdioHostOptions{IdleTimeout: time.Minute, SweepInterval: time.Hour})
	defer host.Close()
	client := host.Client("workspace/session/agent/1/filesystem", Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHostHelperProcess"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HOST_HELPER": "1",
			"MCP_HOST_START_MARKER":         startMarker,
		},
	})

	_, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list hosted tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "hostPing" {
		t.Fatalf("unexpected hosted tools: %#v", tools)
	}
	first, err := client.CallTool(t.Context(), "hostPing", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call first hosted tool: %v", err)
	}
	second, err := client.CallTool(t.Context(), "hostPing", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call second hosted tool: %v", err)
	}
	if hostResultText(first) == "" || hostResultText(first) != hostResultText(second) {
		t.Fatalf("expected calls to reuse one process: first=%#v second=%#v", first, second)
	}
	if starts := hostStartCount(t, startMarker); starts != 1 {
		t.Fatalf("expected one hosted process start, got %d", starts)
	}
	if stats := host.Stats(); stats.Sessions != 1 {
		t.Fatalf("unexpected host stats before reap: %#v", stats)
	}
	if reaped := host.ReapIdle(time.Now().Add(2 * time.Minute)); reaped != 1 {
		t.Fatalf("expected one idle process reaped, got %d", reaped)
	}
	if stats := host.Stats(); stats.Sessions != 0 {
		t.Fatalf("unexpected host stats after reap: %#v", stats)
	}
}

func TestStdioHostSeparatesScopes(t *testing.T) {
	startMarker := t.TempDir() + "/starts.log"
	host := NewStdioHost(StdioHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	base := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHostHelperProcess"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HOST_HELPER": "1",
			"MCP_HOST_START_MARKER":         startMarker,
		},
	}
	first := host.Client("session-a/filesystem", base)
	second := host.Client("session-b/filesystem", base)
	firstResult, err := first.CallTool(t.Context(), "hostPing", nil)
	if err != nil {
		t.Fatalf("call first scope: %v", err)
	}
	secondResult, err := second.CallTool(t.Context(), "hostPing", nil)
	if err != nil {
		t.Fatalf("call second scope: %v", err)
	}
	if hostResultText(firstResult) == hostResultText(secondResult) {
		t.Fatalf("expected isolated process ids, got %q", hostResultText(firstResult))
	}
	if starts := hostStartCount(t, startMarker); starts != 2 {
		t.Fatalf("expected two isolated process starts, got %d", starts)
	}
}

func TestStdioHostRestartsAfterCancelledRequest(t *testing.T) {
	startMarker := t.TempDir() + "/starts.log"
	host := NewStdioHost(StdioHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	client := host.Client("session-cancel/filesystem", Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHostHelperProcess"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HOST_HELPER": "1",
			"MCP_HOST_START_MARKER":         startMarker,
		},
	})
	if _, err := client.CallTool(t.Context(), "hostPing", nil); err != nil {
		t.Fatalf("prime hosted process: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := client.CallTool(ctx, "hang", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected hosted call deadline, got %v", err)
	}
	result, err := client.CallTool(t.Context(), "hostPing", nil)
	if err != nil {
		t.Fatalf("call after hosted process cancellation: %v", err)
	}
	if hostResultText(result) == "" {
		t.Fatalf("unexpected restarted host result: %#v", result)
	}
	if starts := hostStartCount(t, startMarker); starts != 2 {
		t.Fatalf("expected cancelled process to restart once, got %d starts", starts)
	}
}

func TestStdioHostEvictsOldestIdleEntryAtCapacity(t *testing.T) {
	startMarker := t.TempDir() + "/starts.log"
	host := NewStdioHost(StdioHostOptions{MaxSessions: 1, SweepInterval: time.Hour})
	defer host.Close()
	base := Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHostHelperProcess"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HOST_HELPER": "1",
			"MCP_HOST_START_MARKER":         startMarker,
		},
	}
	if _, err := host.Client("session-old", base).CallTool(t.Context(), "hostPing", nil); err != nil {
		t.Fatalf("prime oldest host entry: %v", err)
	}
	if _, err := host.Client("session-new", base).CallTool(t.Context(), "hostPing", nil); err != nil {
		t.Fatalf("create replacement host entry: %v", err)
	}
	stats := host.Stats()
	if stats.Sessions != 1 || stats.MaxSessions != 1 || stats.EvictionsTotal != 1 || stats.StartsTotal != 2 {
		t.Fatalf("unexpected capacity eviction stats: %#v", stats)
	}
}

func TestStdioHostRejectsNewEntryWhenCapacityIsBusy(t *testing.T) {
	host := NewStdioHost(StdioHostOptions{MaxSessions: 1, SweepInterval: time.Hour})
	defer host.Close()
	base := Client{Command: os.Args[0]}
	entry, err := host.acquire(t.Context(), "session-busy", base)
	if err != nil {
		t.Fatalf("acquire busy host entry: %v", err)
	}
	defer host.release(entry)
	_, err = host.Client("session-rejected", base).CallTool(t.Context(), "hostPing", nil)
	if err == nil || !strings.Contains(err.Error(), "capacity reached") {
		t.Fatalf("expected host capacity rejection, got %v", err)
	}
	stats := host.Stats()
	if stats.Sessions != 1 || stats.InUseSessions != 1 || stats.RejectionsTotal != 1 {
		t.Fatalf("unexpected busy capacity stats: %#v", stats)
	}
}

func TestStdioHostTracksCatalogChangesAndRefreshesLists(t *testing.T) {
	startMarker := t.TempDir() + "/starts.log"
	host := NewStdioHost(StdioHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	client := host.Client("session-catalog/dynamic", Client{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHostHelperProcess"},
		Env: map[string]string{
			"GO_WANT_MCP_STDIO_HOST_HELPER": "1",
			"MCP_HOST_START_MARKER":         startMarker,
			"MCP_HOST_DYNAMIC_CATALOG":      "1",
		},
		LoggingLevel: "warning",
	})

	_, before, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list initial hosted tools: %v", err)
	}
	if len(before) != 1 || before[0].Name != "hostPing" {
		t.Fatalf("unexpected initial hosted tools: %#v", before)
	}
	if _, err := client.CallTool(t.Context(), "emitCatalogChanges", nil); err != nil {
		t.Fatalf("emit hosted catalog changes: %v", err)
	}
	stats := host.Stats()
	if stats.ToolsListChangedTotal != 1 || stats.ResourcesListChangedTotal != 1 || stats.PromptsListChangedTotal != 1 {
		t.Fatalf("unexpected hosted catalog change stats: %#v", stats)
	}
	if stats.ProgressNotificationsTotal != 2 || stats.LogMessagesTotal != 2 || stats.InvalidNotificationsTotal != 2 || stats.LogMessagesByLevel["info"] != 1 || stats.LogMessagesByLevel["unknown"] != 1 {
		t.Fatalf("unexpected hosted notification stats: %#v", stats)
	}

	_, after, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list refreshed hosted tools: %v", err)
	}
	if len(after) != 1 || after[0].Name != "hostPingUpdated" {
		t.Fatalf("unexpected refreshed hosted tools: %#v", after)
	}
	_, resources, err := client.ListResources(t.Context())
	if err != nil {
		t.Fatalf("list refreshed hosted resources: %v", err)
	}
	if len(resources) != 1 || resources[0].URI != "memory://updated" {
		t.Fatalf("unexpected refreshed hosted resources: %#v", resources)
	}
	_, prompts, err := client.ListPrompts(t.Context())
	if err != nil {
		t.Fatalf("list refreshed hosted prompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Name != "updatedPrompt" {
		t.Fatalf("unexpected refreshed hosted prompts: %#v", prompts)
	}
	if starts := hostStartCount(t, startMarker); starts != 1 {
		t.Fatalf("expected catalog refreshes to reuse one process, got %d starts", starts)
	}
}

func TestMCPStdioHostHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HOST_HELPER") != "1" {
		return
	}
	if marker := os.Getenv("MCP_HOST_START_MARKER"); marker != "" {
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
			panic(err)
		}
		if err := file.Close(); err != nil {
			panic(err)
		}
	}
	reader := bufio.NewReader(os.Stdin)
	initialized := false
	dynamicCatalog := os.Getenv("MCP_HOST_DYNAMIC_CATALOG") == "1"
	catalogChanged := false
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
		if method == "notifications/initialized" || method == "notifications/cancelled" {
			continue
		}
		id := request["id"]
		switch method {
		case "initialize":
			if initialized {
				panic("host initialized one process more than once")
			}
			initialized = true
			result := map[string]any{"serverInfo": map[string]any{"name": "Hosted MCP"}}
			if dynamicCatalog {
				result["capabilities"] = map[string]any{
					"tools":     map[string]any{"listChanged": true},
					"resources": map[string]any{"listChanged": true},
					"prompts":   map[string]any{"listChanged": true},
					"logging":   map[string]any{},
				}
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  result,
			})
		case "tools/list":
			toolName := "hostPing"
			if catalogChanged {
				toolName = "hostPingUpdated"
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{"tools": []map[string]any{{
					"name":        toolName,
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				}}},
			})
		case "logging/setLevel":
			if !dynamicCatalog {
				writeResourceHelperMethodNotFound(id)
				continue
			}
			params, _ := request["params"].(map[string]any)
			if params["level"] != "warning" {
				panic(fmt.Sprintf("unexpected logging level: %#v", params))
			}
			writeResourceHelperMessage(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "resources/list":
			if !dynamicCatalog {
				writeResourceHelperMethodNotFound(id)
				continue
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"resources": []map[string]any{{"uri": "memory://updated", "name": "Updated resource"}}},
			})
		case "prompts/list":
			if !dynamicCatalog {
				writeResourceHelperMethodNotFound(id)
				continue
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"prompts": []map[string]any{{"name": "updatedPrompt"}}},
			})
		case "tools/call":
			params, _ := request["params"].(map[string]any)
			name, _ := params["name"].(string)
			if name == "hang" {
				continue
			}
			if dynamicCatalog && name == "emitCatalogChanges" {
				for _, notification := range []map[string]any{
					{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"},
					{"jsonrpc": "2.0", "method": "notifications/resources/list_changed"},
					{"jsonrpc": "2.0", "method": "notifications/prompts/list_changed"},
					{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{"progressToken": "call-1", "progress": 1, "total": 2, "message": "sensitive progress text"}},
					{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{"progressToken": "call-1", "progress": "bad"}},
					{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info", "logger": "fixture", "data": map[string]any{"secret": "must-not-be-stored"}}},
					{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "verbose", "data": "invalid level"}},
				} {
					writeResourceHelperMessage(notification)
				}
				catalogChanged = true
			}
			writeResourceHelperMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "pid=" + strconv.Itoa(os.Getpid())}},
				},
			})
		default:
			writeResourceHelperMethodNotFound(id)
		}
	}
}

func writeResourceHelperMethodNotFound(id any) {
	writeResourceHelperMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32601, "message": "method not found"},
	})
}

func hostResultText(result ToolCallResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].Text
}

func hostStartCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read host start marker: %v", err)
	}
	return len(strings.Fields(string(raw)))
}
