package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectcleanup"
	"tiggy-manage-agent/internal/objectreconcile"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
)

func TestObjectCleanupOperationsAPIAndAudit(t *testing.T) {
	base := newTestStore()
	store := &objectCleanupOperationsTestStore{
		testStore: base,
		jobs: []objectcleanup.Job{{
			ID: "ocj_1", WorkspaceID: "wksp", Status: objectcleanup.StatusDeadLetter,
			Reason: objectcleanup.ReasonArtifactCreateFailed, SizeBytes: 128, SafeToDelete: true,
		}},
		retryResult:   objectcleanup.Job{ID: "ocj_1", WorkspaceID: "wksp", Status: objectcleanup.StatusPending, SafeToDelete: true},
		approveResult: objectcleanup.Job{ID: "ocj_2", WorkspaceID: "wksp", Status: objectcleanup.StatusPending, SafeToDelete: true},
	}
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/object-cleanup/jobs?workspace_id=wksp&status=dead_letter&reason=artifact_create_failed&limit=10", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list cleanup jobs: status=%d body=%s", response.Code, response.Body.String())
	}
	if store.listInput.WorkspaceID != "wksp" || store.listInput.Status != objectcleanup.StatusDeadLetter || store.listInput.Reason != objectcleanup.ReasonArtifactCreateFailed || store.listInput.Limit != 10 {
		t.Fatalf("unexpected cleanup list input: %+v", store.listInput)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/jobs/ocj_1/retry?workspace_id=wksp", nil))
	if response.Code != http.StatusOK || store.retryInput.JobID != "ocj_1" {
		t.Fatalf("retry cleanup job: status=%d body=%s input=%+v", response.Code, response.Body.String(), store.retryInput)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/jobs/ocj_2/approve?workspace_id=wksp", bytes.NewBufferString(`{"confirm":"DELETE wrong"}`)))
	if response.Code != http.StatusBadRequest || store.approveInput.JobID != "" {
		t.Fatalf("invalid approval confirmation: status=%d body=%s input=%+v", response.Code, response.Body.String(), store.approveInput)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/jobs/ocj_2/approve?workspace_id=wksp", bytes.NewBufferString(`{"confirm":"DELETE ocj_2"}`)))
	if response.Code != http.StatusOK || store.approveInput.JobID != "ocj_2" {
		t.Fatalf("approve cleanup job: status=%d body=%s input=%+v", response.Code, response.Body.String(), store.approveInput)
	}

	base.mu.Lock()
	audits := make([]string, 0, len(base.operatorAudits))
	for _, record := range base.operatorAudits {
		audits = append(audits, record.Action)
	}
	base.mu.Unlock()
	if len(audits) != 3 || audits[0] != "object_cleanup.retry" || audits[1] != "object_cleanup.approve_blocked" || audits[2] != "object_cleanup.approve_blocked" {
		t.Fatalf("unexpected object cleanup audits: %+v", audits)
	}
}

func TestMetricsIncludeObjectCleanupBacklog(t *testing.T) {
	base := newTestStore()
	store := &objectCleanupOperationsTestStore{
		testStore: base,
		stats: objectcleanup.Stats{
			WorkspaceID: "wksp", OldestPendingAge: 90, OrphansStaged: 4, TotalRetriedJobs: 1, TotalDeletedBytes: 512,
			Statuses: []objectcleanup.StatusStats{{Status: objectcleanup.StatusPending, Jobs: 2, Bytes: 256}},
		},
	}
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics?workspace_id=wksp", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get metrics: status=%d body=%s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		`tma_object_cleanup_jobs{status="pending"} 2`,
		`tma_object_cleanup_oldest_pending_age_seconds 90`,
		`tma_object_cleanup_orphans_staged 4`,
		`tma_object_cleanup_deleted_bytes 512`,
		`tma_object_cleanup_retry_ratio 0.5`,
	} {
		if !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("cleanup metric %q missing:\n%s", expected, response.Body.String())
		}
	}
}

func TestObjectReconciliationPreviewAPIIsScopedReadOnlyAndAudited(t *testing.T) {
	base := newTestStore()
	store := &objectReconciliationTestStore{testStore: base, page: objectreconcile.ReferencePage{References: []objectreconcile.Reference{{
		ID: "obj_1", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderLocalFS,
		Bucket: "artifacts", ObjectKey: "wksp/report.docx", SizeBytes: 10,
	}}}}
	provider := &objectReconciliationTestProvider{
		list:       objectstore.ListObjectsResult{Objects: []objectstore.ObjectInfo{{Bucket: "artifacts", Key: "wksp/orphan.pdf", SizeBytes: 5}}},
		statErrors: map[string]error{"wksp/report.docx": objectstore.ErrNotFound},
	}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, 0, nil), nil, "fake", "fake-demo", provider)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/reconciliation/preview", strings.NewReader(`{"workspace_id":"wksp","prefix":"wksp/reports/","limit":25}`))
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview reconciliation: status=%d body=%s", response.Code, response.Body.String())
	}
	var report objectreconcile.Report
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.DryRun || report.Summary.MissingObjects != 1 || report.Summary.OrphanObjects != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if store.listInput.WorkspaceID != "wksp" || store.listInput.Prefix != "wksp/reports/" || store.listInput.Limit != 25 {
		t.Fatalf("unexpected scoped list input: %+v", store.listInput)
	}
	if provider.listInput.Prefix != "wksp/reports/" || provider.listInput.Bucket != "artifacts" {
		t.Fatalf("unexpected provider list input: %+v", provider.listInput)
	}
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.operatorAudits) != 1 || base.operatorAudits[0].Action != "object_cleanup.reconciliation.preview" || base.operatorAudits[0].Outcome != "succeeded" {
		t.Fatalf("unexpected reconciliation audit: %+v", base.operatorAudits)
	}
}

func TestObjectReconciliationPreviewRejectsPrefixOutsideWorkspace(t *testing.T) {
	base := newTestStore()
	store := &objectReconciliationTestStore{testStore: base}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, 0, nil), nil, "fake", "fake-demo", &objectReconciliationTestProvider{})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/reconciliation/preview", strings.NewReader(`{"workspace_id":"wksp","prefix":"other/"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid prefix status, got %d body=%s", response.Code, response.Body.String())
	}
	if store.listCalls != 0 {
		t.Fatalf("invalid prefix must not reach database, got %d calls", store.listCalls)
	}
}

func TestObjectReconciliationExportCreatesManagedArtifactAndAudit(t *testing.T) {
	base := newTestStore()
	base.sessions["sess_reconciliation"] = managedagents.Session{
		ID: "sess_reconciliation", WorkspaceID: "wksp", Status: managedagents.SessionStatusIdle,
	}
	store := &objectReconciliationTestStore{testStore: base}
	provider, err := objectstore.NewLocalFSClient(objectstore.Config{
		Provider: objectstore.ProviderLocalFS, Bucket: "artifacts", RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create local object store: %v", err)
	}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, 0, nil), nil, "fake", "fake-demo", provider)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/reconciliation/artifacts", strings.NewReader(`{
		"workspace_id":"wksp","session_id":"sess_reconciliation","prefix":"wksp/reports/","limit":25,"name":"storage-audit"
	}`))
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("export reconciliation artifact: status=%d body=%s", response.Code, response.Body.String())
	}
	var exported exportObjectReconciliationArtifactResponse
	if err := json.Unmarshal(response.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if exported.Artifact.SessionID != "sess_reconciliation" || exported.Artifact.Name != "storage-audit.json" || exported.ObjectRef.ID != exported.Artifact.ObjectRefID {
		t.Fatalf("unexpected exported artifact: %+v object_ref=%+v", exported.Artifact, exported.ObjectRef)
	}
	if exported.Report.Prefix != "wksp/reports/" || exported.WorkspacePath == "" {
		t.Fatalf("unexpected exported report response: %+v", exported)
	}
	object, err := provider.GetObject(context.Background(), objectstore.GetObjectInput{
		Bucket: exported.ObjectRef.Bucket, Key: exported.ObjectRef.ObjectKey, Version: exported.ObjectRef.ObjectVersion,
	})
	if err != nil {
		t.Fatalf("get exported report object: %v", err)
	}
	defer object.Body.Close()
	content, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("read exported report object: %v", err)
	}
	var storedReport objectreconcile.Report
	if err := json.Unmarshal(content, &storedReport); err != nil || storedReport.GeneratedAt.IsZero() || !storedReport.DryRun {
		t.Fatalf("unexpected stored report: report=%+v err=%v", storedReport, err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(exported.Artifact.Metadata, &metadata); err != nil || metadata["protocol_version"] != objectReconciliationReportProtocolVersion {
		t.Fatalf("unexpected artifact metadata: metadata=%s err=%v", exported.Artifact.Metadata, err)
	}
	lineage, _ := metadata["lineage"].(map[string]any)
	validation, _ := metadata["validation"].(map[string]any)
	if lineage["session_id"] != "sess_reconciliation" || lineage["tool"] != "object_reconciliation" || validation["status"] != "passed" || validation["checksum_sha256"] != exported.ObjectRef.ChecksumSHA256 {
		t.Fatalf("unexpected artifact lineage or validation: lineage=%+v validation=%+v", lineage, validation)
	}
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.operatorAudits) != 1 || base.operatorAudits[0].Action != "object_cleanup.reconciliation.export_artifact" || base.operatorAudits[0].ResourceID != exported.Artifact.ID {
		t.Fatalf("unexpected export audit: %+v", base.operatorAudits)
	}
}

func TestObjectReconciliationExportRejectsSessionOutsideWorkspaceBeforeScan(t *testing.T) {
	base := newTestStore()
	base.sessions["sess_other"] = managedagents.Session{ID: "sess_other", WorkspaceID: "other", Status: managedagents.SessionStatusIdle}
	store := &objectReconciliationTestStore{testStore: base}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, 0, nil), nil, "fake", "fake-demo", &objectReconciliationTestProvider{})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v2/object-cleanup/reconciliation/artifacts", strings.NewReader(`{
		"workspace_id":"wksp","session_id":"sess_other","prefix":"wksp/"
	}`)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden session mismatch, got %d body=%s", response.Code, response.Body.String())
	}
	if store.listCalls != 0 {
		t.Fatalf("session mismatch must not run reconciliation, got %d calls", store.listCalls)
	}
}

type objectCleanupOperationsTestStore struct {
	*testStore
	jobs          []objectcleanup.Job
	stats         objectcleanup.Stats
	listInput     objectcleanup.ListInput
	retryInput    objectcleanup.RetryInput
	approveInput  objectcleanup.ApproveInput
	retryResult   objectcleanup.Job
	approveResult objectcleanup.Job
}

type objectReconciliationTestStore struct {
	*testStore
	page        objectreconcile.ReferencePage
	lookup      []objectreconcile.Reference
	listInput   objectreconcile.ListReferencesInput
	lookupInput objectreconcile.LookupReferencesInput
	listCalls   int
}

func (store *objectReconciliationTestStore) ListObjectReconciliationReferences(_ context.Context, input objectreconcile.ListReferencesInput) (objectreconcile.ReferencePage, error) {
	store.listCalls++
	store.listInput = input
	return store.page, nil
}

func (store *objectReconciliationTestStore) LookupObjectReconciliationReferences(_ context.Context, input objectreconcile.LookupReferencesInput) ([]objectreconcile.Reference, error) {
	store.lookupInput = input
	return store.lookup, nil
}

type objectReconciliationTestProvider struct {
	list       objectstore.ListObjectsResult
	listInput  objectstore.ListObjectsInput
	stats      map[string]objectstore.ObjectInfo
	statErrors map[string]error
}

func (provider *objectReconciliationTestProvider) Config() objectstore.Config {
	return objectstore.Config{Provider: objectstore.ProviderLocalFS, Bucket: "artifacts"}
}

func (provider *objectReconciliationTestProvider) PutObject(context.Context, objectstore.PutObjectInput) (objectstore.PutObjectResult, error) {
	return objectstore.PutObjectResult{}, errors.New("unexpected write")
}

func (provider *objectReconciliationTestProvider) GetObject(context.Context, objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	return objectstore.GetObjectResult{}, objectstore.ErrNotFound
}

func (provider *objectReconciliationTestProvider) DeleteObject(context.Context, objectstore.DeleteObjectInput) error {
	return errors.New("unexpected delete")
}

func (provider *objectReconciliationTestProvider) PresignGetObject(context.Context, objectstore.PresignGetObjectInput) (objectstore.PresignedURL, error) {
	return objectstore.PresignedURL{}, objectstore.ErrNotFound
}

func (provider *objectReconciliationTestProvider) ListObjects(_ context.Context, input objectstore.ListObjectsInput) (objectstore.ListObjectsResult, error) {
	provider.listInput = input
	return provider.list, nil
}

func (provider *objectReconciliationTestProvider) StatObject(_ context.Context, input objectstore.StatObjectInput) (objectstore.ObjectInfo, error) {
	if err := provider.statErrors[input.Key]; err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return provider.stats[input.Key], nil
}

func (s *objectCleanupOperationsTestStore) ListObjectCleanup(_ context.Context, input objectcleanup.ListInput) ([]objectcleanup.Job, error) {
	s.listInput = input
	return append([]objectcleanup.Job(nil), s.jobs...), nil
}

func (s *objectCleanupOperationsTestStore) GetObjectCleanupStats(_ context.Context, workspaceID string, _ time.Time) (objectcleanup.Stats, error) {
	stats := s.stats
	if stats.WorkspaceID == "" {
		stats.WorkspaceID = workspaceID
	}
	return stats, nil
}

func (s *objectCleanupOperationsTestStore) RetryObjectCleanup(_ context.Context, input objectcleanup.RetryInput) (objectcleanup.Job, error) {
	s.retryInput = input
	return s.retryResult, nil
}

func (s *objectCleanupOperationsTestStore) ApproveBlockedObjectCleanup(_ context.Context, input objectcleanup.ApproveInput) (objectcleanup.Job, error) {
	s.approveInput = input
	return s.approveResult, nil
}
