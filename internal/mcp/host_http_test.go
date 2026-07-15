package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamableHTTPHostReusesSessionAndDeletesOnClose(t *testing.T) {
	var mu sync.Mutex
	initializeCount := 0
	deleteCount := 0
	sessionRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if r.Header.Get("Mcp-Session-Id") != "hosted-session-1" {
				t.Errorf("unexpected delete session id: %q", r.Header.Get("Mcp-Session-Id"))
			}
			mu.Lock()
			deleteCount++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		request := decodeHostedHTTPRequest(t, r)
		method, _ := request["method"].(string)
		id := request["id"]
		if method != "initialize" && method != "notifications/initialized" {
			if r.Header.Get("Mcp-Session-Id") != "hosted-session-1" {
				t.Errorf("missing hosted session id for %s: %q", method, r.Header.Get("Mcp-Session-Id"))
			}
			mu.Lock()
			sessionRequests++
			mu.Unlock()
		}
		switch method {
		case "initialize":
			mu.Lock()
			initializeCount++
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", "hosted-session-1")
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"serverInfo": map[string]any{"name": "Hosted HTTP"}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []map[string]any{{"name": "hostPing", "inputSchema": map[string]any{"type": "object"}}}}})
		case "tools/call":
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}})
		default:
			t.Errorf("unexpected hosted HTTP method: %s", method)
		}
	}))
	defer server.Close()

	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{SweepInterval: time.Hour})
	client := host.Client("workspace/session/agent/1/remote", Client{Transport: TransportStreamableHTTP, URL: server.URL})
	_, tools, err := client.ListTools(t.Context())
	if err != nil {
		t.Fatalf("list hosted HTTP tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "hostPing" {
		t.Fatalf("unexpected hosted HTTP tools: %#v", tools)
	}
	result, err := client.CallTool(t.Context(), "hostPing", nil)
	if err != nil {
		t.Fatalf("call hosted HTTP tool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "pong" {
		t.Fatalf("unexpected hosted HTTP result: %#v", result)
	}
	host.Close()

	mu.Lock()
	defer mu.Unlock()
	if initializeCount != 1 || sessionRequests != 2 || deleteCount != 1 {
		t.Fatalf("unexpected hosted HTTP lifecycle initialize=%d requests=%d deletes=%d", initializeCount, sessionRequests, deleteCount)
	}
}

func TestStreamableHTTPHostKeepsSessionAfterRequestCancellation(t *testing.T) {
	var mu sync.Mutex
	initializeCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		request := decodeHostedHTTPRequest(t, r)
		method, _ := request["method"].(string)
		id := request["id"]
		switch method {
		case "initialize":
			mu.Lock()
			initializeCount++
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", "cancel-session")
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []map[string]any{{"name": "ping", "inputSchema": map[string]any{"type": "object"}}}}})
		case "tools/call":
			<-r.Context().Done()
		default:
			t.Errorf("unexpected hosted HTTP method: %s", method)
		}
	}))
	defer server.Close()

	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	client := host.Client("cancel-scope", Client{Transport: TransportStreamableHTTP, URL: server.URL})
	if _, _, err := client.ListTools(t.Context()); err != nil {
		t.Fatalf("prime hosted HTTP session: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.CallTool(ctx, "hang", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected hosted HTTP deadline, got %v", err)
	}
	if _, _, err := client.ListTools(t.Context()); err != nil {
		t.Fatalf("reuse hosted HTTP session after cancellation: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if initializeCount != 1 {
		t.Fatalf("expected cancellation to keep remote session, got %d initializes", initializeCount)
	}
}

func TestStreamableHTTPHostReapsIdleSession(t *testing.T) {
	deleteCalled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalled <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		request := decodeHostedHTTPRequest(t, r)
		method, _ := request["method"].(string)
		id := request["id"]
		switch method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "idle-session")
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []any{}}})
		}
	}))
	defer server.Close()

	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{IdleTimeout: time.Minute, SweepInterval: time.Hour})
	defer host.Close()
	if _, _, err := host.Client("idle-scope", Client{Transport: TransportStreamableHTTP, URL: server.URL}).ListTools(t.Context()); err != nil {
		t.Fatalf("prime idle hosted HTTP session: %v", err)
	}
	if reaped := host.ReapIdle(time.Now().Add(2 * time.Minute)); reaped != 1 {
		t.Fatalf("expected one reaped hosted HTTP session, got %d", reaped)
	}
	select {
	case <-deleteCalled:
	case <-time.After(time.Second):
		t.Fatal("expected idle hosted HTTP session DELETE")
	}
}

func TestStreamableHTTPHostKeepsListenerAndTracksCatalogChanges(t *testing.T) {
	listenerStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet {
			if r.Header.Get("Mcp-Session-Id") != "listener-host-session" {
				t.Errorf("unexpected hosted listener session id: %q", r.Header.Get("Mcp-Session-Id"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprint(w, strings.Join([]string{
				"event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n",
				"event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progressToken\":\"http-1\",\"progress\":1,\"total\":3,\"message\":\"private\"}}\n\n",
				"event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"error\",\"logger\":\"fixture\",\"data\":{\"secret\":\"hidden\"}}}\n\n",
			}, "")); err != nil {
				t.Errorf("write hosted listener notification: %v", err)
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case listenerStarted <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		request := decodeHostedHTTPRequest(t, r)
		method, _ := request["method"].(string)
		id := request["id"]
		switch method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "listener-host-session")
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"capabilities": map[string]any{"tools": map[string]any{"listChanged": true}, "logging": map[string]any{}}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "logging/setLevel":
			params, _ := request["params"].(map[string]any)
			if params["level"] != "error" {
				t.Errorf("unexpected hosted HTTP logging level: %#v", params)
			}
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "tools/list":
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []any{}}})
		}
	}))
	defer server.Close()

	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	client := host.Client("listener-scope", Client{Transport: TransportStreamableHTTP, URL: server.URL, Listen: true, LoggingLevel: "error"})
	if _, _, err := client.ListTools(t.Context()); err != nil {
		t.Fatalf("list tools with hosted listener: %v", err)
	}
	select {
	case <-listenerStarted:
	case <-time.After(time.Second):
		t.Fatal("hosted listener did not remain active after list request")
	}
	deadline := time.Now().Add(time.Second)
	for host.Stats().LogMessagesTotal != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if stats := host.Stats(); stats.ToolsListChangedTotal != 1 || stats.ProgressNotificationsTotal != 1 || stats.LogMessagesTotal != 1 || stats.LogMessagesByLevel["error"] != 1 || stats.Sessions != 1 {
		t.Fatalf("unexpected hosted listener stats: %#v", stats)
	}
}

func TestStreamableHTTPHostRejectsNewEntryWhenCapacityIsBusy(t *testing.T) {
	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{MaxSessions: 1, SweepInterval: time.Hour})
	defer host.Close()
	entry, err := host.acquire(t.Context(), "busy", Client{Transport: TransportStreamableHTTP, URL: "http://127.0.0.1"})
	if err != nil {
		t.Fatalf("acquire busy hosted HTTP entry: %v", err)
	}
	defer host.release(entry)
	_, err = host.Client("rejected", Client{Transport: TransportStreamableHTTP, URL: "http://127.0.0.1"}).CallTool(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected hosted HTTP capacity rejection")
	}
	stats := host.Stats()
	if stats.Sessions != 1 || stats.InUseSessions != 1 || stats.RejectionsTotal != 1 {
		t.Fatalf("unexpected hosted HTTP capacity stats: %#v", stats)
	}
}

func TestStreamableHTTPHostReportsSanitizedEgressPolicyStats(t *testing.T) {
	policy, err := NewEgressPolicy(EgressPolicyConfig{
		AllowedHosts: []string{"mcp.internal.example"},
		AllowedCIDRs: []string{"10.20.0.0/16"},
	})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{EgressPolicy: policy})
	defer host.Close()
	if err := policy.ValidateURL(t.Context(), "http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatal("expected unsafe egress target to be blocked")
	}
	stats := host.Stats()
	if !stats.EgressPolicyEnabled || stats.EgressAllowHTTP || stats.EgressAllowPrivateNetworks || stats.EgressAllowedHostCount != 1 || stats.EgressAllowedCIDRCount != 1 || stats.EgressBlockedTotal != 1 {
		t.Fatalf("unexpected egress policy stats: %#v", stats)
	}
}

func TestStreamableHTTPHostReinitializesAfterRemoteSessionExpires(t *testing.T) {
	var mu sync.Mutex
	initializeCount := 0
	toolCallCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		request := decodeHostedHTTPRequest(t, r)
		method, _ := request["method"].(string)
		id := request["id"]
		switch method {
		case "initialize":
			mu.Lock()
			initializeCount++
			sessionID := fmt.Sprintf("expiring-session-%d", initializeCount)
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sessionID)
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHostedJSON(t, w, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []map[string]any{{"name": "expire", "inputSchema": map[string]any{"type": "object"}}}}})
		case "tools/call":
			mu.Lock()
			toolCallCount++
			mu.Unlock()
			http.Error(w, "session expired", http.StatusNotFound)
		}
	}))
	defer server.Close()

	host := NewStreamableHTTPHost(StreamableHTTPHostOptions{SweepInterval: time.Hour})
	defer host.Close()
	client := host.Client("expiring-scope", Client{Transport: TransportStreamableHTTP, URL: server.URL})
	if _, _, err := client.ListTools(t.Context()); err != nil {
		t.Fatalf("prime expiring hosted HTTP session: %v", err)
	}
	if _, err := client.CallTool(t.Context(), "expire", nil); err == nil {
		t.Fatal("expected expired remote session error")
	}
	if _, _, err := client.ListTools(t.Context()); err != nil {
		t.Fatalf("reinitialize expired hosted HTTP session: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if initializeCount != 2 || toolCallCount != 1 {
		t.Fatalf("expected one failed call without replay and one reinitialize, initializes=%d calls=%d", initializeCount, toolCallCount)
	}
	if stats := host.Stats(); stats.DiscardsTotal != 1 || stats.StartsTotal != 2 {
		t.Fatalf("unexpected expired hosted HTTP stats: %#v", stats)
	}
}

func decodeHostedHTTPRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatalf("decode hosted HTTP request: %v", err)
	}
	return request
}

func writeHostedJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write hosted HTTP response: %v", err)
	}
}
