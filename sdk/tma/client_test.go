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

	"github.com/coder/websocket"
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

func TestRetrievalServicePathsUploadAndSearch(t *testing.T) {
	expected := []string{
		"POST /v2/retrieval/collections",
		"GET /v2/retrieval/collections",
		"POST /v2/retrieval/collections/rcol%2F1/documents",
		"GET /v2/retrieval/ingestion-jobs/rijob%2F1",
		"POST /v2/retrieval/search",
	}
	requestIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIndex >= len(expected) {
			t.Fatalf("unexpected retrieval request %s %s", r.Method, r.URL.EscapedPath())
		}
		got := r.Method + " " + r.URL.EscapedPath()
		if got != expected[requestIndex] {
			t.Fatalf("retrieval request %d = %q, want %q", requestIndex, got, expected[requestIndex])
		}
		requestIndex++
		w.Header().Set("Content-Type", "application/json")
		switch got {
		case expected[0]:
			fmt.Fprint(w, `{"id":"rcol/1","name":"Shared"}`)
		case expected[1]:
			fmt.Fprint(w, `{"collections":[{"id":"rcol/1","name":"Shared"}]}`)
		case expected[2]:
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse retrieval multipart: %v", err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("read retrieval multipart file: %v", err)
			}
			defer file.Close()
			if header.Filename != "source.txt" || r.FormValue("name") != "Source" || readAll(t, file) != "retrieval body" {
				t.Fatalf("unexpected retrieval multipart: header=%+v name=%q", header, r.FormValue("name"))
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"document":{"id":"rdoc/1","collection_id":"rcol/1"},"object_ref":{"id":"obj/1"},"ingestion_job":{"id":"rijob/1","status":"ready"}}`)
		case expected[3]:
			fmt.Fprint(w, `{"id":"rijob/1","status":"ready","document_id":"rdoc/1"}`)
		case expected[4]:
			var request RetrievalSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Query != "deployment" {
				t.Fatalf("unexpected retrieval search request: %+v err=%v", request, err)
			}
			fmt.Fprint(w, `{"results":[{"document_id":"rdoc/1","collection_id":"rcol/1","content":"ten days","score":0.9}],"citations":[{"collection_id":"rcol/1","document_id":"rdoc/1","score":0.9}]}`)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := client.Retrieval.Collections.Create(t.Context(), CreateRetrievalCollectionRequest{Name: "Shared"})
	if err != nil || collection.ID != "rcol/1" {
		t.Fatalf("create retrieval collection: %+v err=%v", collection, err)
	}
	collections, err := client.Retrieval.Collections.List(t.Context())
	if err != nil || len(collections) != 1 {
		t.Fatalf("list retrieval collections: %+v err=%v", collections, err)
	}
	upload, err := client.Retrieval.Documents.Upload(t.Context(), "rcol/1", map[string]string{"name": "Source"}, UploadFile{
		FileName: "source.txt", ContentType: "text/plain", Body: strings.NewReader("retrieval body"),
	})
	if err != nil || upload.Document.ID != "rdoc/1" || upload.IngestionJob.Status != "ready" {
		t.Fatalf("upload retrieval document: %+v err=%v", upload, err)
	}
	job, err := client.Retrieval.IngestionJobs.Get(t.Context(), "rijob/1")
	if err != nil || job.DocumentID != "rdoc/1" {
		t.Fatalf("get retrieval ingestion job: %+v err=%v", job, err)
	}
	search, err := client.Retrieval.Search(t.Context(), RetrievalSearchRequest{CollectionIDs: []string{"rcol/1"}, Query: "deployment"})
	if err != nil || len(search.Results) != 1 || len(search.Citations) != 1 || search.Citations[0].Score != 0.9 {
		t.Fatalf("search retrieval: %+v err=%v", search, err)
	}
	if requestIndex != len(expected) {
		t.Fatalf("received %d retrieval requests, want %d", requestIndex, len(expected))
	}
}

func TestModelRuntimeServiceGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v2/model-runtime/generate" {
			t.Fatalf("unexpected model runtime request %s %s", r.Method, r.URL.EscapedPath())
		}
		var request ModelGenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) != 1 || request.Messages[0].Content != "Summarize this" || request.MaxOutputTokens != 300 {
			t.Fatalf("unexpected request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"Summary","provider_id":"fake","model":"fake-demo","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4},"finish_reason":"stop"}`)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL)
	response, err := client.ModelRuntime.Generate(t.Context(), ModelGenerateRequest{
		Messages: []ModelMessage{{Role: "user", Content: "Summarize this"}}, MaxOutputTokens: 300,
	})
	if err != nil || response.Text != "Summary" || response.Usage.TotalTokens != 4 {
		t.Fatalf("unexpected model runtime response: %+v err=%v", response, err)
	}
}

func TestModelRuntimeServiceEmbedAndRerank(t *testing.T) {
	requestIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestIndex++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case "/v2/model-runtime/embeddings":
			var request ModelEmbeddingRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ProviderID != "embeddings" || len(request.Inputs) != 2 {
				t.Fatalf("unexpected embedding request: %+v", request)
			}
			fmt.Fprint(w, `{"embeddings":[{"index":0,"embedding":[0.1,0.2]},{"index":1,"embedding":[0.3,0.4]}],"provider_id":"embeddings","model":"embed-v1","dimensions":2,"usage":{"input_tokens":5,"total_tokens":5}}`)
		case "/v2/model-runtime/rerank":
			var request ModelRerankRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Query != "best" || len(request.Documents) != 2 || request.TopN != 1 {
				t.Fatalf("unexpected rerank request: %+v", request)
			}
			fmt.Fprint(w, `{"results":[{"index":1,"score":0.9}],"provider_id":"reranker","model":"rerank-v1"}`)
		default:
			t.Fatalf("unexpected model runtime request %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer server.Close()

	client, _ := NewClient(server.URL)
	embeddings, err := client.ModelRuntime.Embed(t.Context(), ModelEmbeddingRequest{
		ProviderID: "embeddings", Model: "embed-v1", Inputs: []string{"first", "second"},
	})
	if err != nil || embeddings.Dimensions != 2 || embeddings.Embeddings[1].Embedding[0] != 0.3 || embeddings.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected embedding response: %+v err=%v", embeddings, err)
	}
	reranked, err := client.ModelRuntime.Rerank(t.Context(), ModelRerankRequest{
		ProviderID: "reranker", Model: "rerank-v1", Query: "best", Documents: []string{"first", "second"}, TopN: 1,
	})
	if err != nil || len(reranked.Results) != 1 || reranked.Results[0].Index != 1 {
		t.Fatalf("unexpected rerank response: %+v err=%v", reranked, err)
	}
	if requestIndex != 2 {
		t.Fatalf("received %d requests, want 2", requestIndex)
	}
}

func TestModelRuntimeServiceListsInvocations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v2/model-runtime/invocations" ||
			r.URL.Query().Get("capability") != "embedding" || r.URL.Query().Get("principal_id") != "service/knowledge" ||
			r.URL.Query().Get("service_identity_id") != "svc/knowledge" || r.URL.Query().Get("limit") != "25" {
			t.Fatalf("unexpected invocation request %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"summary":{"record_count":1,"completed_count":1,"input_tokens":5},"records":[{"id":"minv_1","workspace_id":"wksp_1","principal_id":"service/knowledge","request_id":"req_1","capability":"embedding","provider_id":"fake","model":"embed","status":"completed","input_tokens":5,"output_tokens":0,"total_tokens":5,"cached_input_tokens":0,"reasoning_tokens":0,"input_items":1,"output_items":1,"input_bytes":0,"output_bytes":0,"input_characters":4,"output_characters":0,"input_audio_ms":0,"output_audio_ms":0,"latency_ms":10,"started_at":"2026-07-31T00:00:00Z","completed_at":"2026-07-31T00:00:00.01Z"}]}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	report, err := client.ModelRuntime.Invocations(t.Context(), ModelInvocationQuery{PrincipalID: "service/knowledge", ServiceIdentityID: "svc/knowledge", Capability: "embedding", Limit: 25})
	if err != nil || report.Summary.RecordCount != 1 || len(report.Records) != 1 || report.Records[0].InputTokens != 5 {
		t.Fatalf("unexpected invocation report: %+v err=%v", report, err)
	}
}

func TestSpeechRealtimeService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/speech/realtime" || r.Header.Get("Authorization") != "Bearer speech-token" {
			t.Fatalf("unexpected speech handshake: path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		messageType, payload, _ := connection.Read(r.Context())
		var start map[string]any
		_ = json.Unmarshal(payload, &start)
		if messageType != websocket.MessageText || start["provider_id"] != "speech-asr" || start["model"] != "seed-asr" {
			t.Errorf("unexpected speech start: %#v", start)
			return
		}
		_ = connection.Write(r.Context(), websocket.MessageText, []byte(`{"type":"session.started","mode":"transcription","session_id":"bio-1"}`))
		messageType, payload, _ = connection.Read(r.Context())
		if messageType != websocket.MessageBinary || string(payload) != "pcm" {
			t.Errorf("unexpected audio: type=%v payload=%q", messageType, payload)
			return
		}
		_, payload, _ = connection.Read(r.Context())
		if !strings.Contains(string(payload), "audio.commit") {
			t.Errorf("unexpected commit: %s", payload)
			return
		}
		_ = connection.Write(r.Context(), websocket.MessageText, []byte(`{"type":"transcript.final","session_id":"bio-1","text":"final text"}`))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, WithBearerToken("speech-token"))
	realtime, err := client.Speech.DialRealtime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer realtime.CloseNow()
	if err := realtime.Start(t.Context(), SpeechSessionStart{ProviderID: "speech-asr", Model: "seed-asr", SessionID: "bio-1"}); err != nil {
		t.Fatal(err)
	}
	started, err := realtime.Read(t.Context())
	if err != nil || started.Type != "session.started" {
		t.Fatalf("unexpected start event: %+v err=%v", started, err)
	}
	if err := realtime.SendAudio(t.Context(), []byte("pcm")); err != nil {
		t.Fatal(err)
	}
	if err := realtime.CommitAudio(t.Context()); err != nil {
		t.Fatal(err)
	}
	final, err := realtime.Read(t.Context())
	if err != nil || final.Type != "transcript.final" || final.Text != "final text" {
		t.Fatalf("unexpected transcript: %+v err=%v", final, err)
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
