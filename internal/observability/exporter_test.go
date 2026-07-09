package observability

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

func TestExportTurnTraceWritesPerfettoFile(t *testing.T) {
	now := time.Now().UTC()
	store := &stubTraceEventStore{events: []managedagents.Event{
		{
			Seq:       1,
			Type:      managedagents.EventUserMessage,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","content":[{"type":"text","text":"please read"}]}`),
			CreatedAt: now,
		},
		{
			Seq:       2,
			Type:      managedagents.EventRuntimeToolCall,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"tool call","data":{"id":"call_read","identifier":"default","api_name":"read_file"}}`),
			CreatedAt: now.Add(10 * time.Millisecond),
		},
		{
			Seq:       3,
			Type:      managedagents.EventRuntimeToolResult,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"tool result","data":{"id":"call_read","identifier":"default","api_name":"read_file","success":true}}`),
			CreatedAt: now.Add(30 * time.Millisecond),
		},
	}}

	result, err := ExportTurnTrace(store, "sesn_1", "turn_1", ExporterConfig{
		PerfettoEnabled: true,
		PerfettoDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("export trace: %v", err)
	}
	if result.Perfetto == nil || result.Perfetto.Path == "" {
		t.Fatalf("expected perfetto file result, got %#v", result)
	}
	content, err := os.ReadFile(result.Perfetto.Path)
	if err != nil {
		t.Fatalf("read perfetto file: %v", err)
	}
	if !strings.Contains(string(content), `"traceEvents"`) || !strings.Contains(string(content), `"tma.tool.default.read_file"`) {
		t.Fatalf("expected perfetto trace file, got %s", string(content))
	}
}

func TestExportTurnTracePushesOTLPHTTP(t *testing.T) {
	now := time.Now().UTC()
	store := &stubTraceEventStore{events: []managedagents.Event{
		{
			Seq:       1,
			Type:      managedagents.EventUserMessage,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","content":[{"type":"text","text":"please read"}]}`),
			CreatedAt: now,
		},
		{
			Seq:       2,
			Type:      managedagents.EventRuntimeLLMRequest,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"request"}`),
			CreatedAt: now.Add(10 * time.Millisecond),
		},
		{
			Seq:       3,
			Type:      managedagents.EventRuntimeLLMResponse,
			Payload:   json.RawMessage(`{"turn_id":"turn_1","message":"response"}`),
			CreatedAt: now.Add(20 * time.Millisecond),
		},
	}}
	var sawAuth bool
	var body string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != "collector.test" {
			t.Fatalf("expected collector.test endpoint, got %s", r.URL.String())
		}
		if r.URL.Path != "/v1/traces" {
			t.Fatalf("expected /v1/traces, got %s", r.URL.Path)
		}
		sawAuth = r.Header.Get("Authorization") == "Bearer test-token"
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		body = string(raw)
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Status:     "202 Accepted",
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     http.Header{},
		}, nil
	})}

	result, err := ExportTurnTrace(store, "sesn_1", "turn_1", ExporterConfig{
		OTLPEndpoint: "http://collector.test",
		OTLPToken:    "test-token",
		HTTPClient:   client,
	})
	if err != nil {
		t.Fatalf("export trace: %v", err)
	}
	if result.OTLPPush == nil || result.OTLPPush.Status != "202 Accepted" {
		t.Fatalf("expected otlp push result, got %#v", result)
	}
	if !sawAuth {
		t.Fatalf("expected bearer token")
	}
	if !strings.Contains(body, `"resourceSpans"`) || !strings.Contains(body, `"tma.llm"`) {
		t.Fatalf("expected otel payload, got %s", body)
	}
}

func TestExportTurnTraceSkipsWhenDisabled(t *testing.T) {
	result, err := ExportTurnTrace(&stubTraceEventStore{}, "sesn_1", "turn_1", ExporterConfig{})
	if err != nil {
		t.Fatalf("export trace: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("expected disabled exporters to skip, got %#v", result)
	}
}

func TestStatusFromEnvRedactsToken(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	t.Setenv("TMA_PERFETTO_DIR", "/tmp/tma-traces")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_TOKEN", "secret-token")

	status := StatusFromEnv()
	if !status.Perfetto.Enabled || status.Perfetto.Destination != "/tmp/tma-traces" {
		t.Fatalf("unexpected perfetto status: %#v", status.Perfetto)
	}
	if !status.OTLP.Enabled || status.OTLP.Destination != "http://collector.test/v1/traces" || !status.OTLP.TokenProvided {
		t.Fatalf("unexpected otlp status: %#v", status.OTLP)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(raw), "secret-token") {
		t.Fatalf("status leaked token: %s", string(raw))
	}
}

type stubTraceEventStore struct {
	events []managedagents.Event
}

func (s *stubTraceEventStore) ListEvents(string, int64) ([]managedagents.Event, error) {
	return append([]managedagents.Event(nil), s.events...), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
