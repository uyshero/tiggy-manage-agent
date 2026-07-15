package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSecurityAuditExporterBatchesOTLPLogs(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var requestPath string
	var authorization string
	var payload map[string]any
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		decoded := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("decode OTLP logs: %v", err)
		}
		mu.Lock()
		requestPath = r.URL.Path
		authorization = r.Header.Get("Authorization")
		payload = decoded
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()

	exporter, err := NewSecurityAuditExporter(SecurityAuditExporterConfig{
		Endpoint: collector.URL, Token: "collector-token", ServiceName: "tma-test",
		QueueSize: 4, BatchSize: 2, FlushInterval: time.Hour,
		HTTPClient: collector.Client(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new security audit exporter: %v", err)
	}
	for _, event := range []AuthorizationDecisionEvent{
		{
			At: time.Unix(1_700_000_000, 0), Outcome: "allowed", Reason: "identity_boundary", AuthType: "oidc",
			Method: http.MethodGet, Path: "/v1/agents", Subject: "alice", WorkspaceID: "wksp_finance",
			Roles: []string{"operator"}, AuthorizationSources: []string{"group_mapping:finance-operators"},
		},
		{
			At: time.Unix(1_700_000_001, 0), Outcome: "denied", Reason: "control_role_required", AuthType: "oidc",
			Method: http.MethodPost, Path: "/v1/llm-models", Subject: "bob", WorkspaceID: "wksp_finance",
		},
	} {
		if !exporter.EnqueueAuthorizationDecision(event) {
			t.Fatal("expected audit event to be queued")
		}
	}
	waitForSecurityAuditMetric(t, exporter, func(metrics SecurityAuditExporterMetrics) bool { return metrics.Sent == 2 })
	if err := exporter.Close(context.Background()); err != nil {
		t.Fatalf("close security audit exporter: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestPath != "/v1/logs" || authorization != "Bearer collector-token" {
		t.Fatalf("unexpected OTLP request path/auth: %q %q", requestPath, authorization)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode captured payload: %v", err)
	}
	text := string(encoded)
	for _, expected := range []string{
		`"resourceLogs"`, `"service.name"`, `"tma-test"`, `"scopeLogs"`, `"logRecords"`,
		`"auth.authorization_sources"`, `"group_mapping:finance-operators"`, `"tma.workspace.id"`, `"wksp_finance"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected OTLP payload to contain %q, got %s", expected, text)
		}
	}
	if strings.Contains(text, "collector-token") {
		t.Fatalf("OTLP payload leaked exporter token: %s", text)
	}
}

func TestSecurityAuditExporterCloseFlushesAndCountsFailures(t *testing.T) {
	t.Parallel()
	requests := 0
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
	}))
	defer collector.Close()
	exporter, err := NewSecurityAuditExporter(SecurityAuditExporterConfig{
		Endpoint: collector.URL + "/custom", QueueSize: 2, BatchSize: 2, FlushInterval: time.Hour,
		HTTPClient: collector.Client(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new security audit exporter: %v", err)
	}
	if !exporter.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "denied", Reason: "authentication_failed", AuthType: "jwt"}) {
		t.Fatal("expected audit event to be queued")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := exporter.Close(ctx); err != nil {
		t.Fatalf("close security audit exporter: %v", err)
	}
	metrics := exporter.SecurityAuditMetrics()
	if requests != 1 || metrics.Failed != 1 || metrics.Sent != 0 {
		t.Fatalf("unexpected failed export state: requests=%d metrics=%+v", requests, metrics)
	}
}

func TestSecurityAuditExporterDropsWhenQueueIsFull(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	exporter, err := NewSecurityAuditExporter(SecurityAuditExporterConfig{
		Endpoint: collector.URL, QueueSize: 1, BatchSize: 1, FlushInterval: time.Hour,
		HTTPClient: collector.Client(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new security audit exporter: %v", err)
	}
	if !exporter.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "allowed", Reason: "identity_boundary"}) {
		t.Fatal("expected first event to be queued")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("collector did not receive first batch")
	}
	if !exporter.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "denied", Reason: "role_required"}) {
		t.Fatal("expected second event to fill queue")
	}
	if exporter.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "denied", Reason: "authentication_failed"}) {
		t.Fatal("expected full queue to drop third event")
	}
	close(release)
	if err := exporter.Close(context.Background()); err != nil {
		t.Fatalf("close security audit exporter: %v", err)
	}
	metrics := exporter.SecurityAuditMetrics()
	if metrics.Sent != 2 || metrics.Dropped != 1 {
		t.Fatalf("unexpected queue metrics: %+v", metrics)
	}
}

func TestNormalizeOTLPLogsEndpoint(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"https://collector.example":           "https://collector.example/v1/logs",
		"https://collector.example/otlp":      "https://collector.example/otlp/v1/logs",
		"https://collector.example/v1/traces": "https://collector.example/v1/logs",
		"https://collector.example/v1/logs":   "https://collector.example/v1/logs",
	}
	for input, expected := range tests {
		actual, err := NormalizeOTLPLogsEndpoint(input)
		if err != nil || actual != expected {
			t.Fatalf("normalize %q: got %q, %v; want %q", input, actual, err, expected)
		}
	}
	for _, invalid := range []string{"ftp://collector.example", "https://user:pass@collector.example", "https://collector.example?token=secret"} {
		if _, err := NormalizeOTLPLogsEndpoint(invalid); err == nil {
			t.Fatalf("expected invalid endpoint %q to fail", invalid)
		}
	}
}

func waitForSecurityAuditMetric(t *testing.T, exporter *SecurityAuditExporter, ready func(SecurityAuditExporterMetrics) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready(exporter.SecurityAuditMetrics()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("security audit metric did not reach expected state: %+v", exporter.SecurityAuditMetrics())
}
