package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

func TestExportTurnTraceWritesPerfettoFile(t *testing.T) {
	resetExporterHealth(t)
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
	if !strings.Contains(string(content), `"traceEvents"`) ||
		!strings.Contains(string(content), `"tma.tool.default.read_file"`) ||
		!strings.Contains(string(content), `"critical"`) ||
		!strings.Contains(string(content), `"self_duration_ms"`) ||
		!strings.Contains(string(content), `"graph"`) {
		t.Fatalf("expected perfetto trace file, got %s", string(content))
	}
	status := StatusFromEnv()
	if status.Perfetto.LastSuccess == nil || status.Perfetto.LastSuccess.SessionID != "sesn_1" || status.Perfetto.LastSuccess.TurnID != "turn_1" {
		t.Fatalf("expected perfetto health success, got %#v", status.Perfetto.LastSuccess)
	}
	if status.Perfetto.LastAttempt == nil || status.Perfetto.LastAttempt.Message != result.Perfetto.Path {
		t.Fatalf("expected perfetto last attempt path, got %#v", status.Perfetto.LastAttempt)
	}
	if len(store.runs) != 1 || store.runs[0].Exporter != managedagents.ObservabilityExporterPerfetto || store.runs[0].Status != managedagents.ObservabilityExporterRunSucceeded {
		t.Fatalf("expected persisted perfetto exporter run, got %#v", store.runs)
	}
}

func TestExportTurnTracePushesOTLPHTTP(t *testing.T) {
	resetExporterHealth(t)
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
	if !strings.Contains(body, `"resourceSpans"`) ||
		!strings.Contains(body, `"tma.llm"`) ||
		!strings.Contains(body, `"tma.critical"`) ||
		!strings.Contains(body, `"tma.self_duration_ms"`) ||
		!strings.Contains(body, `"graph"`) {
		t.Fatalf("expected otel payload, got %s", body)
	}
	status := StatusFromEnv()
	if status.OTLP.LastSuccess == nil || status.OTLP.LastSuccess.SessionID != "sesn_1" || status.OTLP.LastSuccess.TurnID != "turn_1" {
		t.Fatalf("expected otlp health success, got %#v", status.OTLP.LastSuccess)
	}
	if status.OTLP.LastAttempt == nil || !strings.Contains(status.OTLP.LastAttempt.Message, "202 Accepted") {
		t.Fatalf("expected otlp last attempt status, got %#v", status.OTLP.LastAttempt)
	}
	if len(store.runs) != 1 || store.runs[0].Exporter != managedagents.ObservabilityExporterOTLP || store.runs[0].Status != managedagents.ObservabilityExporterRunSucceeded {
		t.Fatalf("expected persisted otlp exporter run, got %#v", store.runs)
	}
}

func TestExportTurnTraceRecordsOTLPFailureHealth(t *testing.T) {
	resetExporterHealth(t)
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
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader(`collector unavailable`)),
			Header:     http.Header{},
		}, nil
	})}

	_, err := ExportTurnTrace(store, "sesn_1", "turn_1", ExporterConfig{
		OTLPEndpoint:      "http://collector.test",
		HTTPClient:        client,
		RetryEnabled:      true,
		RetryMaxAttempts:  3,
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Second,
	})
	if err == nil {
		t.Fatalf("expected otlp export failure")
	}
	status := StatusFromEnv()
	if status.OTLP.LastFailure == nil || status.OTLP.LastFailure.SessionID != "sesn_1" || status.OTLP.LastFailure.TurnID != "turn_1" {
		t.Fatalf("expected otlp health failure, got %#v", status.OTLP.LastFailure)
	}
	if status.OTLP.LastAttempt == nil || !strings.Contains(status.OTLP.LastAttempt.Message, "500 Internal Server Error") {
		t.Fatalf("expected otlp last attempt failure message, got %#v", status.OTLP.LastAttempt)
	}
	if len(store.runs) != 1 || store.runs[0].Exporter != managedagents.ObservabilityExporterOTLP || store.runs[0].Status != managedagents.ObservabilityExporterRunFailed {
		t.Fatalf("expected persisted otlp exporter failure run, got %#v", store.runs)
	}
	if store.runs[0].AttemptCount != 1 || store.runs[0].NextRetryAt == nil {
		t.Fatalf("expected retry metadata on failed run, got %#v", store.runs[0])
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

func TestRetryFailedExporterRunsRetriesDueOTLPFailure(t *testing.T) {
	resetExporterHealth(t)
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
	var pushes int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		pushes++
		if pushes == 1 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Body:       io.NopCloser(strings.NewReader(`collector unavailable`)),
				Header:     http.Header{},
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Status:     "202 Accepted",
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     http.Header{},
		}, nil
	})}
	config := ExporterConfig{
		OTLPEndpoint:      "http://collector.test",
		HTTPClient:        client,
		RetryEnabled:      true,
		RetryMaxAttempts:  3,
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Second,
	}
	if _, err := ExportTurnTrace(store, "sesn_1", "turn_1", config); err == nil {
		t.Fatalf("expected first export to fail")
	}
	if len(store.runs) != 1 || store.runs[0].NextRetryAt == nil {
		t.Fatalf("expected scheduled failed run, got %#v", store.runs)
	}

	result, err := RetryFailedExporterRuns(store, config, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("retry exporter runs: %v", err)
	}
	if result.Attempted != 1 || result.Succeeded != 1 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("unexpected retry result: %#v", result)
	}
	if len(store.runs) != 2 {
		t.Fatalf("expected retry run to be recorded, got %#v", store.runs)
	}
	retryRun := store.runs[1]
	if retryRun.Status != managedagents.ObservabilityExporterRunSucceeded || retryRun.AttemptCount != 2 || retryRun.NextRetryAt != nil {
		t.Fatalf("expected successful second attempt, got %#v", retryRun)
	}
}

func TestExportTurnTraceFromEnvSkipsAndPersistsSamplingDecision(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	t.Setenv("TMA_PERFETTO_DIR", "/tmp/tma-traces")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test")
	t.Setenv("TMA_OBSERVABILITY_SAMPLE_RATE", "0")
	store := &stubTraceEventStore{}

	result, err := ExportTurnTraceFromEnv(store, "sesn_1", "turn_1")
	if err != nil {
		t.Fatalf("export trace: %v", err)
	}
	if !result.Skipped || !strings.Contains(result.SkipMessage, "sampling") {
		t.Fatalf("expected sampling skip, got %#v", result)
	}
	if len(store.runs) != 2 {
		t.Fatalf("expected skipped perfetto and otlp runs, got %#v", store.runs)
	}
	for _, run := range store.runs {
		if run.Status != managedagents.ObservabilityExporterRunSkipped || run.SessionID != "sesn_1" || run.TurnID != "turn_1" {
			t.Fatalf("expected persisted skipped run, got %#v", run)
		}
	}
	status := StatusFromEnvWithRuns(store.runs)
	if !status.Sampling.Enabled || status.Sampling.SampleRate != 0 || !status.Sampling.Configured {
		t.Fatalf("expected sampling status, got %#v", status.Sampling)
	}
	if status.Perfetto.LastSuccess != nil || status.OTLP.LastFailure != nil {
		t.Fatalf("expected skipped runs not to count as success/failure, got %#v %#v", status.Perfetto, status.OTLP)
	}
}

func TestStatusFromEnvRedactsToken(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	t.Setenv("TMA_PERFETTO_DIR", "/tmp/tma-traces")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.test")
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_TOKEN", "secret-token")
	t.Setenv("TMA_OBSERVABILITY_SAMPLE_RATE", "")

	status := StatusFromEnv()
	if !status.Perfetto.Enabled || status.Perfetto.Destination != "/tmp/tma-traces" {
		t.Fatalf("unexpected perfetto status: %#v", status.Perfetto)
	}
	if !status.OTLP.Enabled || status.OTLP.Destination != "http://collector.test/v1/traces" || !status.OTLP.TokenProvided {
		t.Fatalf("unexpected otlp status: %#v", status.OTLP)
	}
	if status.Sampling.SampleRate != 1 || status.Sampling.Enabled || status.Sampling.Configured {
		t.Fatalf("unexpected sampling status: %#v", status.Sampling)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(raw), "secret-token") {
		t.Fatalf("status leaked token: %s", string(raw))
	}
}

func TestStatusFromEnvWithRunsOverlaysPersistedHealth(t *testing.T) {
	t.Setenv("TMA_PERFETTO", "1")
	t.Setenv("TMA_OBSERVABILITY_SAMPLE_RATE", "")
	run := managedagents.ObservabilityExporterRun{
		Exporter:   managedagents.ObservabilityExporterPerfetto,
		Status:     managedagents.ObservabilityExporterRunSucceeded,
		SessionID:  "sesn_1",
		TurnID:     "turn_1",
		TraceID:    "trace_1",
		Message:    "/tmp/trace.perfetto.json",
		FinishedAt: time.Unix(123, 0).UTC(),
	}
	status := StatusFromEnvWithRuns([]managedagents.ObservabilityExporterRun{run})
	if len(status.RecentRuns) != 1 {
		t.Fatalf("expected recent runs, got %#v", status.RecentRuns)
	}
	if status.Perfetto.LastSuccess == nil || status.Perfetto.LastSuccess.SessionID != "sesn_1" || status.Perfetto.LastSuccess.At.Unix() != 123 {
		t.Fatalf("expected persisted perfetto health, got %#v", status.Perfetto.LastSuccess)
	}
}

type stubTraceEventStore struct {
	events    []managedagents.Event
	runs      []managedagents.ObservabilityExporterRun
	nextRunID int
}

func (s *stubTraceEventStore) ListEvents(string, int64) ([]managedagents.Event, error) {
	return append([]managedagents.Event(nil), s.events...), nil
}

func (s *stubTraceEventStore) RecordObservabilityExporterRun(input managedagents.RecordObservabilityExporterRunInput) (managedagents.ObservabilityExporterRun, error) {
	s.nextRunID++
	run := managedagents.ObservabilityExporterRun{
		ID:           fmt.Sprintf("oexp_%06d", s.nextRunID),
		Exporter:     input.Exporter,
		Status:       input.Status,
		SessionID:    input.SessionID,
		TurnID:       input.TurnID,
		TraceID:      input.TraceID,
		Destination:  input.Destination,
		Message:      input.Message,
		AttemptCount: input.AttemptCount,
		NextRetryAt:  input.NextRetryAt,
		StartedAt:    input.StartedAt,
		FinishedAt:   input.FinishedAt,
	}
	if run.AttemptCount <= 0 {
		run.AttemptCount = 1
	}
	s.runs = append(s.runs, run)
	return run, nil
}

func (s *stubTraceEventStore) ListObservabilityExporterRuns(input managedagents.ListObservabilityExporterRunsInput) ([]managedagents.ObservabilityExporterRun, error) {
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	runs := make([]managedagents.ObservabilityExporterRun, 0, limit)
	for index := len(s.runs) - 1; index >= 0; index-- {
		run := s.runs[index]
		if input.Exporter != "" && run.Exporter != input.Exporter {
			continue
		}
		if input.Status != "" && run.Status != input.Status {
			continue
		}
		if input.SessionID != "" && run.SessionID != input.SessionID {
			continue
		}
		if input.TurnID != "" && run.TurnID != input.TurnID {
			continue
		}
		if !input.RetryDueBefore.IsZero() {
			if run.Status != managedagents.ObservabilityExporterRunFailed || run.NextRetryAt == nil || run.NextRetryAt.After(input.RetryDueBefore) {
				continue
			}
		}
		if input.MaxAttemptCount > 0 && run.AttemptCount >= input.MaxAttemptCount {
			continue
		}
		runs = append(runs, run)
		if len(runs) >= limit {
			break
		}
	}
	return runs, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func resetExporterHealth(t *testing.T) {
	t.Helper()
	exporterHealth.Lock()
	previous := exporterHealth.byName
	exporterHealth.byName = map[string]exporterHealthRecord{}
	exporterHealth.Unlock()
	t.Cleanup(func() {
		exporterHealth.Lock()
		exporterHealth.byName = previous
		exporterHealth.Unlock()
	})
}
