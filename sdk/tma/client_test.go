package tma

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientInjectsTokenAndDecodesV2Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rotated-token" {
			t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"session_busy","message":"busy","request_id":"req_test","retryable":false}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, WithTokenSource(func(context.Context) (string, error) { return "rotated-token", nil }))
	if err != nil {
		t.Fatal(err)
	}
	err = client.DoJSON(t.Context(), http.MethodPost, "/v2/sessions", map[string]any{}, nil)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "session_busy" || apiError.RequestID != "req_test" || apiError.Retryable {
		t.Fatalf("unexpected API error: %#v", err)
	}
}

func TestClientSupportsLegacyErrorsAndDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/legacy":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"legacy failure"}`)
		case "/download":
			fmt.Fprint(w, "artifact-data")
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	err := client.DoJSON(t.Context(), http.MethodGet, "/legacy", nil, nil)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Message != "legacy failure" {
		t.Fatalf("unexpected legacy error: %#v", err)
	}
	var output bytes.Buffer
	if err := client.Download(t.Context(), "/download", &output); err != nil || output.String() != "artifact-data" {
		t.Fatalf("unexpected download: output=%q err=%v", output.String(), err)
	}
}

func TestClientUploadAndCustomTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v2/sessions/sesn_1/artifacts/upload" {
			t.Fatalf("unexpected upload request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.FormValue("description") != "SDK upload" {
			t.Fatalf("unexpected description %q", r.FormValue("description"))
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("read upload file: %v", err)
		}
		defer file.Close()
		if header.Filename != "report.txt" || header.Header.Get("Content-Type") != "text/plain" || readAll(t, file) != "report data" {
			t.Fatalf("unexpected upload file: filename=%q content_type=%q", header.Filename, header.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"object_ref":{"id":"obj_1","size_bytes":11},"artifact":{"id":"art_1","session_id":"sesn_1","name":"report.txt","artifact_type":"file"},"workspace_path":"artifacts/report.txt"}`)
	}))
	defer server.Close()

	transport := &countingTransport{base: http.DefaultTransport}
	client, err := NewClient(server.URL, WithTransport(transport))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Artifacts.Upload(t.Context(), "sesn_1", map[string]string{"description": "SDK upload"}, UploadFile{
		FileName: "report.txt", ContentType: "text/plain", Body: strings.NewReader("report data"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.ID != "art_1" || result.ObjectRef.ID != "obj_1" || result.WorkspacePath != "artifacts/report.txt" || calls.Load() != 1 || transport.calls.Load() != 1 {
		t.Fatalf("unexpected upload result: %+v server_calls=%d transport_calls=%d", result, calls.Load(), transport.calls.Load())
	}
}

func TestTypedSessionInterventionAndArtifactServices(t *testing.T) {
	expected := map[string]bool{
		"POST /v2/sessions":                                                  true,
		"GET /v2/sessions/sesn%2F1":                                          true,
		"POST /v2/sessions/sesn%2F1/archive":                                 true,
		"POST /v2/sessions/sesn%2F1/restore":                                 true,
		"PATCH /v2/sessions/sesn%2F1/runtime-settings":                       true,
		"POST /v2/sessions/sesn%2F1/config/upgrade":                          true,
		"POST /v2/sessions/sesn%2F1/events":                                  true,
		"GET /v2/sessions/sesn%2F1/events?after_seq=7":                       true,
		"GET /v2/sessions/sesn%2F1/summary":                                  true,
		"PUT /v2/sessions/sesn%2F1/summary":                                  true,
		"GET /v2/sessions/sesn%2F1/task-plan":                                true,
		"GET /v2/sessions/sesn%2F1/task-plans":                               true,
		"GET /v2/sessions/sesn%2F1/interventions?status=pending":             true,
		"POST /v2/sessions/sesn%2F1/interventions/turn%2F1/call%2F1/approve": true,
		"POST /v2/sessions/sesn%2F1/artifacts":                               true,
		"GET /v2/sessions/sesn%2F1/artifacts":                                true,
		"DELETE /v2/sessions/sesn%2F1/artifacts/art%2F1":                     true,
		"DELETE /v2/sessions/sesn%2F1":                                       true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		if !expected[key] {
			t.Fatalf("unexpected typed service request %s", key)
		}
		if strings.HasSuffix(r.URL.Path, "/runtime-settings") && r.Header.Get("If-Match") != `"1"` {
			t.Fatalf("runtime settings If-Match = %q, want quoted revision 1", r.Header.Get("If-Match"))
		}
		delete(expected, key)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/config/upgrade"):
			fmt.Fprint(w, `{"changed":true,"old_agent_config_version":1,"new_agent_config_version":2}`)
		case strings.HasSuffix(r.URL.Path, "/events") && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"events":[{"id":"evt_8","session_id":"sesn/1","seq":8,"type":"custom","created_at":"2026-07-15T00:00:00Z"}]}`)
		case strings.HasSuffix(r.URL.Path, "/events"):
			fmt.Fprint(w, `{"events":[{"id":"evt_1","session_id":"sesn/1","seq":1,"type":"custom","created_at":"2026-07-15T00:00:00Z"}]}`)
		case strings.HasSuffix(r.URL.Path, "/summary"):
			fmt.Fprint(w, `{"session_id":"sesn/1","summary_text":"summary","source_until_seq":8,"created_at":"2026-07-15T00:00:00Z","updated_at":"2026-07-15T00:00:00Z"}`)
		case strings.HasSuffix(r.URL.Path, "/task-plans"):
			fmt.Fprint(w, `{"plans":[{"id":"plan_1","workspace_id":"default","owner_id":"user","session_id":"sesn/1","goal":"Ship","handling_mode":"planned","status":"active","items":[],"created_at":"2026-07-15T00:00:00Z","updated_at":"2026-07-15T00:00:00Z"}]}`)
		case strings.HasSuffix(r.URL.Path, "/task-plan"):
			fmt.Fprint(w, `{"plan":{"id":"plan_1","workspace_id":"default","owner_id":"user","session_id":"sesn/1","goal":"Ship","handling_mode":"planned","status":"active","items":[],"created_at":"2026-07-15T00:00:00Z","updated_at":"2026-07-15T00:00:00Z"}}`)
		case strings.Contains(r.URL.Path, "/interventions/"):
			fmt.Fprint(w, `{"intervention":{"session_id":"sesn/1","turn_id":"turn/1","call_id":"call/1","status":"approved"},"events":[]}`)
		case strings.HasSuffix(r.URL.Path, "/interventions"):
			fmt.Fprint(w, `{"interventions":[]}`)
		case strings.HasSuffix(r.URL.Path, "/artifacts") && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"artifacts":[]}`)
		case strings.HasSuffix(r.URL.Path, "/artifacts"):
			fmt.Fprint(w, `{"id":"art/1","session_id":"sesn/1","object_ref_id":"obj_1","name":"report","artifact_type":"file","created_at":"2026-07-15T00:00:00Z"}`)
		default:
			fmt.Fprint(w, `{"id":"sesn/1","agent_id":"agt_1","environment_id":"env_1","status":"idle","created_at":"2026-07-15T00:00:00Z"}`)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err = client.Sessions.Create(ctx, CreateSessionRequest{AgentID: "agt_1", EnvironmentID: "env_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.Get(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.Archive(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.Restore(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	completionRetries := 3
	if _, err = client.Sessions.UpdateRuntimeSettings(ctx, "sesn/1", 1, UpdateSessionRuntimeSettingsRequest{CompletionGate: &CompletionGateRuntimeSettings{MaxRetries: &completionRetries}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.UpgradeConfig(ctx, "sesn/1", UpgradeSessionConfigRequest{ToVersion: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.AppendEvents(ctx, "sesn/1", AppendEventsRequest{Events: []AppendEvent{{Type: "custom"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.ListEvents(ctx, "sesn/1", 7); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.GetSummary(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if plan, taskPlanErr := client.Sessions.TaskPlan(ctx, "sesn/1"); taskPlanErr != nil || plan.ID != "plan_1" {
		t.Fatalf("unexpected current task plan: plan=%+v err=%v", plan, taskPlanErr)
	}
	if plans, taskPlansErr := client.Sessions.TaskPlans(ctx, "sesn/1"); taskPlansErr != nil || len(plans) != 1 || plans[0].ID != "plan_1" {
		t.Fatalf("unexpected task plan history: plans=%+v err=%v", plans, taskPlansErr)
	}
	if _, err = client.Sessions.UpsertSummary(ctx, "sesn/1", UpsertSessionSummaryRequest{SummaryText: "summary", SourceUntilSeq: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Interventions.List(ctx, "sesn/1", "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Interventions.DecideResult(ctx, "sesn/1", "turn/1", "call/1", "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Artifacts.Create(ctx, "sesn/1", CreateArtifactRequest{ObjectRefID: "obj_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Artifacts.List(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if err = client.Artifacts.Delete(ctx, "sesn/1", "art/1"); err != nil {
		t.Fatal(err)
	}
	if err = client.Sessions.Delete(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("typed service operations not called: %#v", expected)
	}
}

func TestTypedAgentEnvironmentAndLLMServices(t *testing.T) {
	expected := map[string]bool{
		"POST /v2/agents":                                 true,
		"GET /v2/agents/agt%2F1":                          true,
		"GET /v2/agents":                                  true,
		"PATCH /v2/agents/agt%2F1":                        true,
		"GET /v2/agents/agt%2F1/config-versions":          true,
		"POST /v2/agents/agt%2F1/config-versions":         true,
		"POST /v2/environments":                           true,
		"GET /v2/llm-providers":                           true,
		"GET /v2/llm-providers/provider%2F1":              true,
		"POST /v2/llm-providers":                          true,
		"PATCH /v2/llm-providers/provider%2F1":            true,
		"POST /v2/llm-providers/provider%2F1/disable":     true,
		"POST /v2/llm-providers/provider%2F1/test":        true,
		"DELETE /v2/llm-providers/provider%2F1":           true,
		"GET /v2/llm-models?provider_id=provider%2F1":     true,
		"POST /v2/llm-models#create":                      true,
		"POST /v2/llm-models#update":                      true,
		"DELETE /v2/llm-models/provider%2F1/model%2F1":    true,
		"POST /v2/llm-models/provider%2F1/model%2F1/test": true,
		"GET /v2/llm-usage?group_by=provider&model=gpt-5": true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v2/llm-models" {
			if r.Header.Get("If-None-Match") == "*" {
				key += "#create"
			} else {
				key += "#update"
			}
		}
		if !expected[key] {
			t.Fatalf("unexpected typed service request %s", key)
		}
		delete(expected, key)
		if strings.Contains(key, "PATCH /v2/llm-providers/") ||
			strings.Contains(key, "POST /v2/llm-providers/provider%2F1/disable") ||
			strings.Contains(key, "DELETE /v2/llm-providers/") ||
			strings.HasSuffix(key, "#update") || strings.Contains(key, "DELETE /v2/llm-models/") {
			if r.Header.Get("If-Match") != `"7"` {
				t.Fatalf("unexpected If-Match for %s: %q", key, r.Header.Get("If-Match"))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "config-versions") && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"config_versions":[{"version":1,"llm_model":"gpt-5"}]}`)
		case r.URL.Path == "/v2/agents" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"agents":[{"id":"agt/1","current_config_version":1}]}`)
		case strings.HasPrefix(r.URL.Path, "/v2/agents"):
			fmt.Fprint(w, `{"id":"agt/1","current_config_version":1,"config_version":{"version":1}}`)
		case r.URL.Path == "/v2/environments":
			fmt.Fprint(w, `{"id":"env_1","name":"dev","config":{}}`)
		case r.URL.Path == "/v2/llm-providers" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"providers":[{"id":"provider/1","provider_type":"openai","revision":7}]}`)
		case strings.HasSuffix(r.URL.Path, "/test"):
			fmt.Fprint(w, `{"status":"succeeded","latency_ms":12,"authenticated":true,"message":"diagnostic succeeded","retryable":false,"checked_at":"2026-07-15T00:00:00Z"}`)
		case strings.HasPrefix(r.URL.Path, "/v2/llm-providers"):
			fmt.Fprint(w, `{"id":"provider/1","provider_type":"openai","revision":7}`)
		case r.URL.Path == "/v2/llm-models" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"models":[{"provider_id":"provider/1","model":"model/1","revision":7}]}`)
		case r.URL.Path == "/v2/llm-models":
			fmt.Fprint(w, `{"provider_id":"provider/1","model":"model/1","revision":7}`)
		case r.URL.Path == "/v2/llm-usage":
			fmt.Fprint(w, `{"group_by":"provider","filters":{},"summary":{},"groups":[]}`)
		default:
			t.Fatalf("missing response fixture for %s", key)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err = client.Agents.Create(ctx, CreateAgentRequest{Name: "agent", LLMModel: "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Agents.Get(ctx, "agt/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Agents.List(ctx); err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	if _, err = client.Agents.Update(ctx, "agt/1", UpdateAgentRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Agents.ListConfigVersions(ctx, "agt/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Agents.CreateConfigVersion(ctx, "agt/1", CreateAgentConfigVersionRequest{System: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Environments.Create(ctx, CreateEnvironmentRequest{Name: "dev", Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LLM.ListProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LLM.GetProvider(ctx, "provider/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LLM.CreateProvider(ctx, CreateLLMProviderRequest{ID: "provider/1", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LLM.UpdateProvider(ctx, "provider/1", 7, UpdateLLMProviderRequest{BaseURL: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LLM.SetProviderEnabled(ctx, "provider/1", 7, false); err != nil {
		t.Fatal(err)
	}
	if err = client.LLM.DeleteProvider(ctx, "provider/1", 7); err != nil {
		t.Fatal(err)
	}
	if result, diagnosticErr := client.LLM.TestProvider(ctx, "provider/1"); diagnosticErr != nil || result.Status != "succeeded" {
		t.Fatalf("unexpected provider diagnostic: %+v err=%v", result, diagnosticErr)
	}
	if _, err = client.LLM.ListModels(ctx, "provider/1"); err != nil {
		t.Fatal(err)
	}
	modelRequest := PutLLMModelRequest{ProviderID: "provider/1", Model: "model/1"}
	if _, err = client.LLM.CreateModel(ctx, modelRequest); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LLM.UpdateModel(ctx, 7, modelRequest); err != nil {
		t.Fatal(err)
	}
	if err = client.LLM.DeleteModel(ctx, "provider/1", "model/1", 7); err != nil {
		t.Fatal(err)
	}
	if result, diagnosticErr := client.LLM.TestModel(ctx, "provider/1", "model/1"); diagnosticErr != nil || result.LatencyMS != 12 {
		t.Fatalf("unexpected model diagnostic: %+v err=%v", result, diagnosticErr)
	}
	if _, err = client.LLM.Usage(ctx, LLMUsageQuery{Model: "gpt-5", GroupBy: "provider"}); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("typed service operations not called: %#v", expected)
	}
}

func TestEventStreamReconnectsFromLastSequenceAndAllowsUnknownEvents(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after_seq") != strconv.Itoa(int(calls.Load())) {
			t.Fatalf("unexpected cursor on call %d: %s", calls.Load(), r.URL.RawQuery)
		}
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: custom.future\ndata: {\"id\":\"evt_%d\",\"session_id\":\"sesn_1\",\"turn_id\":\"turn_1\",\"seq\":%d,\"type\":\"custom.future\",\"created_at\":\"2026-07-14T00:00:00Z\"}\n\n", call, call)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	stream := newEventStream(t.Context(), client, "/v2/sessions/sesn_1/runs/turn_1/events/stream", 0)
	defer stream.Close()
	first, err := stream.Next(t.Context())
	if err != nil || first.Seq != 1 || first.Type != "custom.future" {
		t.Fatalf("unexpected first event: %+v err=%v", first, err)
	}
	second, err := stream.Next(t.Context())
	if err != nil || second.Seq != 2 || calls.Load() < 2 {
		t.Fatalf("unexpected reconnected event: %+v calls=%d err=%v", second, calls.Load(), err)
	}
}

func TestEventStreamRetriesOnlyNetworkAndServerErrors(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":{"code":"unavailable","message":"try later","request_id":"req_1","retryable":false}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"evt_1\",\"session_id\":\"sesn_1\",\"seq\":1,\"type\":\"custom.future\",\"created_at\":\"2026-07-14T00:00:00Z\"}\n\n")
		}))
		defer server.Close()
		client, _ := NewClient(server.URL)
		stream, _ := client.Events(t.Context(), "/events", 0)
		defer stream.Close()
		event, err := stream.Next(t.Context())
		if err != nil || event.Seq != 1 || calls.Load() != 2 {
			t.Fatalf("expected 5xx reconnect, event=%+v calls=%d err=%v", event, calls.Load(), err)
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"code":"rate_limited","message":"slow down","request_id":"req_2","retryable":true}}`)
		}))
		defer server.Close()
		client, _ := NewClient(server.URL)
		stream, _ := client.Events(t.Context(), "/events", 0)
		defer stream.Close()
		_, err := stream.Next(t.Context())
		var apiError *APIError
		if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusTooManyRequests || calls.Load() != 1 {
			t.Fatalf("expected non-retried 429, calls=%d err=%v", calls.Load(), err)
		}
	})
}

func TestRunWaitFollowsSSEToTerminalState(t *testing.T) {
	var streamCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"run":{"id":"turn_1","session_id":"sesn_1","status":"running","user_event_seq":2,"attempt":0,"started_at":"2026-07-14T00:00:00Z"},"created":true}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: agent.message\ndata: {\"id\":\"evt_3\",\"session_id\":\"sesn_1\",\"turn_id\":\"turn_1\",\"seq\":3,\"type\":\"agent.message\",\"payload\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]},\"created_at\":\"2026-07-14T00:00:01Z\"}\n\n")
			fmt.Fprint(w, "event: session.status_idle\ndata: {\"id\":\"evt_4\",\"session_id\":\"sesn_1\",\"turn_id\":\"turn_1\",\"seq\":4,\"type\":\"session.status_idle\",\"created_at\":\"2026-07-14T00:00:02Z\"}\n\n")
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs/turn_1"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"turn_1","session_id":"sesn_1","status":"completed","attempt":1,"started_at":"2026-07-14T00:00:00Z","ended_at":"2026-07-14T00:00:02Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	handle, err := client.Runs.Start(t.Context(), "sesn_1", StartRunRequest{Input: TextInput("work")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handle.Wait(t.Context())
	if err != nil || result.Run.Status != RunStatusCompleted || !bytes.Contains(result.Output, []byte("done")) || streamCalls.Load() != 1 {
		t.Fatalf("unexpected run result: %+v calls=%d err=%v", result, streamCalls.Load(), err)
	}
}

func TestEventStreamContextCancellationDoesNotCallRemoteCancel(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stream := newEventStream(ctx, client, "/events", 0)
	_, err := stream.Next(ctx)
	if err == nil || requests.Load() != 1 {
		t.Fatalf("expected local cancellation, requests=%d err=%v", requests.Load(), err)
	}
	_ = stream.Close()
}

func readAll(t *testing.T, reader io.Reader) string {
	t.Helper()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

type countingTransport struct {
	base  http.RoundTripper
	calls atomic.Int32
}

func (t *countingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return t.base.RoundTrip(request)
}
