package tma

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTypedControlPlaneServices(t *testing.T) {
	expected := map[string]string{
		"GET /v2/agent/task-group-templates":                           `{"templates":[{"id":"parallel","title":"Parallel","description":"Run in parallel","strategy":"all_completed","result_reducer":"json_array"}]}`,
		"GET /v2/agent/discussion-strategies":                          `{"strategies":[{"id":"debate","title":"Debate","description":"Structured debate"}],"team_plan_schema":{}}`,
		"POST /v2/object-refs":                                         `{"id":"obj/1","bucket":"objects","object_key":"key"}`,
		"GET /v2/object-refs/obj%2F1":                                  `{"id":"obj/1","bucket":"objects","object_key":"key"}`,
		"DELETE /v2/object-refs/obj%2F1":                               "",
		"GET /v2/object-refs/obj%2F1/download?session_id=sesn%2F1":     "payload",
		"GET /v2/sessions/sesn%2F1/trace?turn_id=turn%2F1":             `{"session_id":"sesn/1","turn_id":"turn/1","steps":[]}`,
		"GET /v2/sessions/sesn%2F1/trace?format=otel&turn_id=turn%2F1": `{"resourceSpans":[]}`,
		"GET /v2/workers?status=online&workspace_id=wksp%2F1":          `{"workers":[]}`,
		"GET /v2/workers/wrk%2F1":                                      `{"id":"wrk/1","status":"online"}`,
		"POST /v2/workers/wrk%2F1/archive":                             `{"id":"wrk/1","status":"archived"}`,
		"POST /v2/workers/reap-expired":                                `{"count":0,"expired":[]}`,
		"POST /v2/workers/diagnose":                                    `{"invocation":{"protocol_version":"tma.work.v1","namespace":"default","api":"run"},"matches":0,"diagnostics":[]}`,
		"POST /v2/worker-work":                                         `{"id":"work/1","status":"pending"}`,
		"GET /v2/worker-work/work%2F1":                                 `{"id":"work/1","status":"pending"}`,
		"POST /v2/worker-work/reap-expired":                            `{"count":0,"expired":[]}`,
		"POST /v2/worker-work/work%2F1/cancel":                         `{"id":"work/1","status":"canceled"}`,
		"POST /v2/worker-work/work%2F1/requeue":                        `{"id":"work/2","status":"pending"}`,
		"GET /v2/worker-work/work%2F1/diagnose":                        `{"work":{"id":"work/1","status":"pending"}}`,
		"GET /v2/observability/status":                                 `{"perfetto":{},"otlp":{},"sampling":{},"retry":{}}`,
		"POST /v2/observability/retry":                                 `{"attempted":1,"succeeded":1,"failed":0,"skipped":0}`,
		"GET /v2/observability/security-audit/integrity-keys":          `{"active_key_id":"key_1","keys":[]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		body, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected request %s", key)
		}
		delete(expected, key)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.Contains(key, "/download") {
			fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err = client.Orchestration.TaskGroupTemplates(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.DiscussionStrategies(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ObjectRefs.Create(ctx, CreateObjectRefRequest{Bucket: "objects", ObjectKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ObjectRefs.Get(ctx, "obj/1"); err != nil {
		t.Fatal(err)
	}
	if err = client.ObjectRefs.Delete(ctx, "obj/1"); err != nil {
		t.Fatal(err)
	}
	var download bytes.Buffer
	if err = client.ObjectRefs.Download(ctx, "obj/1", "sesn/1", &download); err != nil || download.String() != "payload" {
		t.Fatalf("download=%q err=%v", download.String(), err)
	}
	if _, err = client.Traces.GetSession(ctx, "sesn/1", "turn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Traces.Export(ctx, "sesn/1", "turn/1", "otel"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Workers.List(ctx, WorkerListQuery{WorkspaceID: "wksp/1", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Workers.Get(ctx, "wrk/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Workers.Archive(ctx, "wrk/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Workers.ReapExpired(ctx, ReapExpiredWorkersRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Workers.Diagnose(ctx, WorkerDiagnoseRequest{Namespace: "default", API: "run"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WorkerWork.Enqueue(ctx, EnqueueWorkerWorkRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WorkerWork.Get(ctx, "work/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WorkerWork.ReapExpired(ctx, ReapExpiredWorkerWorkRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WorkerWork.Cancel(ctx, "work/1", CancelWorkerWorkRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WorkerWork.Requeue(ctx, "work/1", RequeueWorkerWorkRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.WorkerWork.Diagnose(ctx, "work/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Observability.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Observability.Retry(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Observability.IntegrityKeys(ctx); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("operations not called: %#v", expected)
	}
}

func TestClientDoesNotExposeLegacyTemplatesService(t *testing.T) {
	if _, exists := reflect.TypeOf(Client{}).FieldByName("Templates"); exists {
		t.Fatal("legacy Workbench task templates must not be exposed by the Core SDK")
	}
}

func TestWorkerWorkConflictDiagnosticsUseOneRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"conflict","message":"no matching worker","request_id":"req_1","retryable":false,"details":{"invocation":{"protocol_version":"tma.work.v1","namespace":"default","api":"run"},"matches":0,"diagnostics":[{"worker_id":"wrk_1","match":false,"reasons":["missing capability"]}]}}}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	_, conflict, err := client.WorkerWork.EnqueueWithDiagnostics(context.Background(), EnqueueWorkerWorkRequest{})
	if err == nil || conflict == nil || len(conflict.Diagnostics) != 1 || conflict.Error != "no matching worker" {
		t.Fatalf("conflict=%+v err=%v", conflict, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("write request was replayed %d times", calls.Load())
	}
}
