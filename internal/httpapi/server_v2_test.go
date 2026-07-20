package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

type liveStreamTestRunner struct {
	runner.Runner
	broker *runner.LiveEventBroker
}

func (test liveStreamTestRunner) SubscribeLiveEvents(sessionID string) (<-chan runner.LiveEvent, func(), error) {
	return test.broker.SubscribeLiveEvents(sessionID)
}

func TestV2SessionLiveStreamIsTransientAndSessionScoped(t *testing.T) {
	store := newTestStore()
	broker := runner.NewLiveEventBroker(8)
	turnRunner := liveStreamTestRunner{Runner: runner.NewMockRunner(store, time.Millisecond, nil), broker: broker}
	server := NewServerWithStoreAndRunner(store, turnRunner, nil)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{"name":"Live Environment","config":{"type":"cloud"}}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{"environment_id":"`+environment.ID+`"}`)

	ctx, cancel := context.WithCancel(context.Background())
	response := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/sessions/"+session.ID+"/live/stream", nil).WithContext(ctx))
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(response.BodyString(), ": live stream ready") })

	broker.Publish(runner.LiveEvent{
		SessionID: session.ID, TurnID: "turn-live", Type: runner.LiveEventLLMText,
		Operation: "append", ContentFormat: "markdown", Text: "streamed text",
	})
	waitFor(t, func() bool { return strings.Contains(response.BodyString(), "streamed text") })
	cancel()
	<-done

	body := response.BodyString()
	if response.Code != http.StatusOK || !strings.Contains(body, "event: llm.text") || !strings.Contains(body, `"stream_seq":1`) {
		t.Fatalf("unexpected live SSE response: status=%d body=%s", response.Code, body)
	}
	if strings.Contains(body, `"seq":`) || strings.Contains(body, `"id":"evt_`) {
		t.Fatalf("live stream must not expose durable event identity: %s", body)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-cache, no-store" {
		t.Fatalf("unexpected live stream cache policy %q", cache)
	}
}

func TestV2AliasPreservesSuccessAndNormalizesErrors(t *testing.T) {
	server := newTestServer()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/agents/default", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected v2 alias success, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get(requestIDHeader) == "" {
		t.Fatal("expected v2 request id")
	}

	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/sessions", bytes.NewBufferString(`{"unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", response.Code, response.Body.String())
	}
	var envelope v2ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode v2 error: %v", err)
	}
	if envelope.Error.Code != "invalid_request" || envelope.Error.RequestID == "" {
		t.Fatalf("unexpected v2 error: %+v", envelope.Error)
	}
}

func TestV2ErrorPreservesLegacyDiagnosticFieldsAsDetails(t *testing.T) {
	response := httptest.NewRecorder()
	writer := newV2ResponseWriter(response, "req_diagnostics")
	writer.WriteHeader(http.StatusConflict)
	_, _ = writer.Write([]byte(`{"error":"no matching worker","matches":0,"diagnostics":[{"worker_id":"wrk_1","match":false}]}`))
	writer.finish()

	var envelope v2ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Message != "no matching worker" || envelope.Error.Details["matches"] != float64(0) {
		t.Fatalf("unexpected normalized error: %+v", envelope.Error)
	}
	diagnostics, ok := envelope.Error.Details["diagnostics"].([]any)
	if !ok || len(diagnostics) != 1 {
		t.Fatalf("diagnostics were not preserved: %#v", envelope.Error.Details)
	}
}

func TestV2ErrorStatusAndRetryabilityMatrix(t *testing.T) {
	tests := []struct {
		status    int
		code      string
		retryable bool
	}{
		{400, "invalid_request", false},
		{401, "unauthorized", false},
		{403, "forbidden", false},
		{404, "not_found", false},
		{405, "method_not_allowed", false},
		{409, "conflict", false},
		{412, "revision_conflict", false},
		{413, "payload_too_large", false},
		{415, "unsupported_media_type", false},
		{422, "unprocessable_entity", false},
		{429, "rate_limited", true},
		{500, "internal_error", false},
		{502, "upstream_error", true},
		{503, "service_unavailable", true},
		{504, "upstream_timeout", true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			response := httptest.NewRecorder()
			writer := newV2ResponseWriter(response, "req_matrix")
			writer.WriteHeader(test.status)
			_, _ = writer.Write([]byte(`{"error":"classified failure"}`))
			writer.finish()
			var envelope v2ErrorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || envelope.Error.Code != test.code || envelope.Error.Retryable != test.retryable {
				t.Fatalf("status=%d error=%+v", response.Code, envelope.Error)
			}
		})
	}
}

func TestV2RevisionConflictAndEmptyListEncoding(t *testing.T) {
	server := newTestServer()

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPatch, "/v2/llm-providers/fake", bytes.NewBufferString(`{"base_url":"https://first.example.test"}`))
	firstRequest.Header.Set("Content-Type", "application/json")
	firstRequest.Header.Set("If-Match", `"1"`)
	server.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("initial provider update failed: %d %s", first.Code, first.Body.String())
	}

	stale := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(http.MethodPatch, "/v2/llm-providers/fake", bytes.NewBufferString(`{"base_url":"https://stale.example.test"}`))
	staleRequest.Header.Set("Content-Type", "application/json")
	staleRequest.Header.Set("If-Match", `"1"`)
	server.ServeHTTP(stale, staleRequest)
	var envelope v2ErrorEnvelope
	if err := json.Unmarshal(stale.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if stale.Code != http.StatusPreconditionFailed || envelope.Error.Code != "revision_conflict" || envelope.Error.Retryable {
		t.Fatalf("unexpected stale revision response: status=%d error=%+v", stale.Code, envelope.Error)
	}

	empty := httptest.NewRecorder()
	server.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/v2/llm-models?provider_id=missing", nil))
	if empty.Code != http.StatusOK || !bytes.Contains(empty.Body.Bytes(), []byte(`"models":[]`)) {
		t.Fatalf("empty list must encode as []: status=%d body=%s", empty.Code, empty.Body.String())
	}
}

func TestV2ExcludesWorkerConsumerProtocol(t *testing.T) {
	server := newTestServer()
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v2/workers"},
		{http.MethodPost, "/v2/workers/wrk_1/heartbeat"},
		{http.MethodGet, "/v2/workers/wrk_1/work/poll"},
		{http.MethodPost, "/v2/workers/wrk_1/work/work_1/result"},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("expected %s %s to be excluded, got %d", test.method, test.path, response.Code)
		}
	}
}

func TestWorkbenchTaskTemplatesRemainV1Only(t *testing.T) {
	server := newTestServer()
	v1 := httptest.NewRecorder()
	server.ServeHTTP(v1, httptest.NewRequest(http.MethodGet, "/v1/task-templates", nil))
	if v1.Code != http.StatusOK || !bytes.Contains(v1.Body.Bytes(), []byte(`"templates"`)) {
		t.Fatalf("expected v1 task templates compatibility response, got %d: %s", v1.Code, v1.Body.String())
	}

	v2 := httptest.NewRecorder()
	server.ServeHTTP(v2, httptest.NewRequest(http.MethodGet, "/v2/task-templates", nil))
	if v2.Code != http.StatusNotFound {
		t.Fatalf("expected v2 task templates to be excluded, got %d: %s", v2.Code, v2.Body.String())
	}
	for _, path := range []string{"/v2/agent/task-group-templates", "/v2/agent/discussion-strategies"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("expected Orchestration endpoint %s to remain available, got %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestV2RunLifecycleAndIdempotency(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 25*time.Millisecond, nil), nil)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{"name":"SDK Agent","llm_provider":"fake","llm_model":"fake-demo"}`)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{"name":"SDK Environment","config":{"type":"cloud"}}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{"agent_id":"`+agent.ID+`","environment_id":"`+environment.ID+`"}`)

	body := `{"input":{"content":[{"type":"text","text":"hello SDK"}]},"idempotency_key":"sdk-run-1"}`
	first := postJSONWithStatus[managedagents.StartSessionRunResult](t, server, http.MethodPost, "/v2/sessions/"+session.ID+"/runs", body, http.StatusCreated)
	if !first.Created || first.Run.ID == "" || first.Run.UserEventSeq == 0 {
		t.Fatalf("unexpected first run: %+v", first)
	}
	second := postJSONWithStatus[managedagents.StartSessionRunResult](t, server, http.MethodPost, "/v2/sessions/"+session.ID+"/runs", body, http.StatusOK)
	if second.Created || second.Run.ID != first.Run.ID {
		t.Fatalf("idempotent run mismatch: first=%+v second=%+v", first, second)
	}

	conflict := httptest.NewRecorder()
	conflictRequest := httptest.NewRequest(http.MethodPost, "/v2/sessions/"+session.ID+"/runs", bytes.NewBufferString(`{"input":{"content":[{"type":"text","text":"different"}]},"idempotency_key":"sdk-run-1"}`))
	conflictRequest.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(conflict, conflictRequest)
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte(`"idempotency_conflict"`)) {
		t.Fatalf("expected idempotency conflict, got %d: %s", conflict.Code, conflict.Body.String())
	}

	waitFor(t, func() bool {
		run := getJSON[managedagents.SessionRun](t, server, "/v2/sessions/"+session.ID+"/runs/"+first.Run.ID)
		return run.Status == managedagents.TurnStatusCompleted
	})
	runs := getJSON[struct {
		Runs []managedagents.SessionRun `json:"runs"`
	}](t, server, "/v2/sessions/"+session.ID+"/runs")
	if len(runs.Runs) != 1 || runs.Runs[0].ID != first.Run.ID {
		t.Fatalf("unexpected run list: %+v", runs.Runs)
	}
	events := getJSON[eventsResponse](t, server, "/v2/sessions/"+session.ID+"/runs/"+first.Run.ID+"/events")
	if len(events.Events) < 4 {
		t.Fatalf("expected run events, got %+v", events.Events)
	}
	for _, event := range events.Events {
		if event.TurnID != first.Run.ID {
			t.Fatalf("event missing run attribution: %+v", event)
		}
	}
}

func TestV2RunCancelIsIdempotent(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Second, nil), nil)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{"name":"Cancel Environment","config":{"type":"cloud"}}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{"environment_id":"`+environment.ID+`"}`)
	started := postJSONWithStatus[managedagents.StartSessionRunResult](t, server, http.MethodPost, "/v2/sessions/"+session.ID+"/runs", `{"input":{"content":[{"type":"text","text":"cancel"}]}}`, http.StatusCreated)
	path := "/v2/sessions/" + session.ID + "/runs/" + started.Run.ID + "/cancel"
	first := postJSONWithStatus[managedagents.SessionRun](t, server, http.MethodPost, path, `{}`, http.StatusOK)
	second := postJSONWithStatus[managedagents.SessionRun](t, server, http.MethodPost, path, `{}`, http.StatusOK)
	if first.Status != managedagents.TurnStatusInterrupted || second.Status != managedagents.TurnStatusInterrupted {
		t.Fatalf("cancel should be idempotent: first=%+v second=%+v", first, second)
	}
}

func TestV2RunReturnsSessionBusyForConcurrentStart(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Second, nil), nil)
	environment := postJSON[managedagents.Environment](t, server, "/v1/environments", `{"name":"Busy Environment","config":{"type":"cloud"}}`)
	session := postJSON[managedagents.Session](t, server, "/v1/sessions", `{"environment_id":"`+environment.ID+`"}`)
	postJSONWithStatus[managedagents.StartSessionRunResult](t, server, http.MethodPost, "/v2/sessions/"+session.ID+"/runs", `{"input":{"content":[{"type":"text","text":"first"}]}}`, http.StatusCreated)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/sessions/"+session.ID+"/runs", bytes.NewBufferString(`{"input":{"content":[{"type":"text","text":"second"}]}}`))
	request.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(response, request)
	var envelope v2ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || envelope.Error.Code != "session_busy" || envelope.Error.Retryable {
		t.Fatalf("unexpected session busy response: status=%d error=%+v", response.Code, envelope.Error)
	}
}
