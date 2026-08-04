package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
)

func (s *testStore) CreateArtifactExchangeContext(ctx context.Context, input managedagents.CreateArtifactExchangeInput) (managedagents.ArtifactExchange, error) {
	scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx)
	if !ok || scope.WorkspaceID == "" {
		return managedagents.ArtifactExchange{}, managedagents.ErrInvalid
	}
	if input.WorkspaceID != "" && input.WorkspaceID != scope.WorkspaceID {
		return managedagents.ArtifactExchange{}, managedagents.ErrForbidden
	}
	if len(input.TokenHash) != 32 || input.OwnerID == "" || input.Filename == "" {
		return managedagents.ArtifactExchange{}, managedagents.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextArtifactExchangeID++
	now := time.Now().UTC()
	exchange := managedagents.ArtifactExchange{
		ID: fmt.Sprintf("aex_%06d", s.nextArtifactExchangeID), WorkspaceID: scope.WorkspaceID,
		AppID: input.AppID, OwnerID: input.OwnerID, Direction: input.Direction,
		Status: managedagents.ArtifactExchangeStatusPending, SessionID: input.SessionID,
		ObjectRefID: input.ObjectRefID, ArtifactID: input.ArtifactID, Filename: input.Filename,
		Description: input.Description, ArtifactType: input.ArtifactType, EnvironmentID: input.EnvironmentID,
		TurnID: input.TurnID, ToolCallID: input.ToolCallID, Visibility: input.Visibility,
		ContentType: input.ContentType, ExpectedSizeBytes: cloneInt64Pointer(input.ExpectedSizeBytes),
		MaxSizeBytes: input.MaxSizeBytes, ExpectedChecksumSHA256: input.ExpectedChecksumSHA256,
		ExpiresAt: input.ExpiresAt, Metadata: append(json.RawMessage(nil), input.Metadata...),
		CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	s.artifactExchanges[exchange.ID] = testArtifactExchange{Exchange: exchange, TokenHash: append([]byte(nil), input.TokenHash...)}
	return exchange, nil
}

func (s *testStore) GetArtifactExchangeContext(ctx context.Context, id string) (managedagents.ArtifactExchange, error) {
	scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return managedagents.ArtifactExchange{}, managedagents.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.artifactExchanges[id]
	if !exists || record.Exchange.WorkspaceID != scope.WorkspaceID {
		return managedagents.ArtifactExchange{}, managedagents.ErrNotFound
	}
	if record.Exchange.Status == managedagents.ArtifactExchangeStatusPending && !record.Exchange.ExpiresAt.After(time.Now().UTC()) {
		record.Exchange.Status = managedagents.ArtifactExchangeStatusExpired
		record.Exchange.UpdatedAt = time.Now().UTC()
		s.artifactExchanges[id] = record
	}
	return record.Exchange, nil
}

func (s *testStore) ClaimArtifactExchangeContext(ctx context.Context, input managedagents.ClaimArtifactExchangeInput) (managedagents.ArtifactExchange, error) {
	scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx)
	if !ok || scope.WorkspaceID != input.WorkspaceID {
		return managedagents.ArtifactExchange{}, managedagents.ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.artifactExchanges[input.ID]
	if !exists || record.Exchange.WorkspaceID != scope.WorkspaceID || record.Exchange.Direction != input.Direction ||
		record.Exchange.Status != managedagents.ArtifactExchangeStatusPending || !record.Exchange.ExpiresAt.After(input.ClaimedAt) ||
		len(input.TokenHash) != len(record.TokenHash) || subtle.ConstantTimeCompare(input.TokenHash, record.TokenHash) != 1 {
		if exists && record.Exchange.Status == managedagents.ArtifactExchangeStatusPending && !record.Exchange.ExpiresAt.After(input.ClaimedAt) {
			record.Exchange.Status = managedagents.ArtifactExchangeStatusExpired
			record.Exchange.UpdatedAt = input.ClaimedAt
			s.artifactExchanges[input.ID] = record
		}
		return managedagents.ArtifactExchange{}, managedagents.ErrNotFound
	}
	claimedAt := input.ClaimedAt.UTC()
	record.Exchange.Status = managedagents.ArtifactExchangeStatusProcessing
	record.Exchange.ClaimedAt = &claimedAt
	record.Exchange.UpdatedAt = claimedAt
	s.artifactExchanges[input.ID] = record
	return record.Exchange, nil
}

func (s *testStore) CompleteArtifactImportContext(ctx context.Context, input managedagents.CompleteArtifactImportInput) (managedagents.ArtifactExchange, managedagents.ObjectRef, managedagents.SessionArtifact, error) {
	exchange, err := s.GetArtifactExchangeContext(ctx, input.ID)
	if err != nil || exchange.Status != managedagents.ArtifactExchangeStatusProcessing || exchange.Direction != managedagents.ArtifactExchangeDirectionImport {
		return managedagents.ArtifactExchange{}, managedagents.ObjectRef{}, managedagents.SessionArtifact{}, managedagents.ErrConflict
	}
	input.ObjectRef.WorkspaceID = exchange.WorkspaceID
	objectRef, err := s.CreateObjectRef(input.ObjectRef)
	if err != nil {
		return managedagents.ArtifactExchange{}, managedagents.ObjectRef{}, managedagents.SessionArtifact{}, err
	}
	input.Artifact.SessionID = exchange.SessionID
	input.Artifact.ObjectRefID = objectRef.ID
	artifact, err := s.CreateSessionArtifact(input.Artifact)
	if err != nil {
		_ = s.DeleteObjectRef(objectRef.ID)
		return managedagents.ArtifactExchange{}, managedagents.ObjectRef{}, managedagents.SessionArtifact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.artifactExchanges[input.ID]
	completedAt := input.CompletedAt.UTC()
	record.Exchange.Status = managedagents.ArtifactExchangeStatusCompleted
	record.Exchange.ObjectRefID = objectRef.ID
	record.Exchange.ArtifactID = artifact.ID
	record.Exchange.CompletedAt = &completedAt
	record.Exchange.UpdatedAt = completedAt
	s.artifactExchanges[input.ID] = record
	return record.Exchange, objectRef, artifact, nil
}

func (s *testStore) CompleteArtifactExportContext(ctx context.Context, id string, completedAt time.Time) (managedagents.ArtifactExchange, error) {
	return s.finishTestArtifactExchange(ctx, id, completedAt, managedagents.ArtifactExchangeStatusCompleted, "")
}

func (s *testStore) FailArtifactExchangeContext(ctx context.Context, id string, failedAt time.Time, message string) (managedagents.ArtifactExchange, error) {
	return s.finishTestArtifactExchange(ctx, id, failedAt, managedagents.ArtifactExchangeStatusFailed, message)
}

func (s *testStore) finishTestArtifactExchange(ctx context.Context, id string, at time.Time, status, message string) (managedagents.ArtifactExchange, error) {
	scope, ok := managedagents.DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return managedagents.ArtifactExchange{}, managedagents.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.artifactExchanges[id]
	if !exists || record.Exchange.WorkspaceID != scope.WorkspaceID || record.Exchange.Status != managedagents.ArtifactExchangeStatusProcessing {
		return managedagents.ArtifactExchange{}, managedagents.ErrConflict
	}
	record.Exchange.Status = status
	record.Exchange.ErrorMessage = message
	record.Exchange.UpdatedAt = at.UTC()
	if status == managedagents.ArtifactExchangeStatusCompleted {
		completedAt := at.UTC()
		record.Exchange.CompletedAt = &completedAt
	}
	s.artifactExchanges[id] = record
	return record.Exchange, nil
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type artifactExchangeTestObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newArtifactExchangeTestObjectStore() *artifactExchangeTestObjectStore {
	return &artifactExchangeTestObjectStore{objects: make(map[string][]byte)}
}

func (s *artifactExchangeTestObjectStore) Config() objectstore.Config {
	return objectstore.Config{Provider: objectstore.ProviderS3, Bucket: "artifact-exchange-test"}
}

func (s *artifactExchangeTestObjectStore) PutObject(_ context.Context, input objectstore.PutObjectInput) (objectstore.PutObjectResult, error) {
	payload, err := io.ReadAll(input.Body)
	if err != nil {
		return objectstore.PutObjectResult{}, err
	}
	s.mu.Lock()
	s.objects[input.Bucket+"/"+input.Key] = append([]byte(nil), payload...)
	s.mu.Unlock()
	return objectstore.PutObjectResult{Bucket: input.Bucket, Key: input.Key, SizeBytes: int64(len(payload)), ChecksumSHA256: input.ChecksumSHA256, ETag: "exchange-etag"}, nil
}

func (s *artifactExchangeTestObjectStore) GetObject(_ context.Context, input objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	s.mu.Lock()
	payload, ok := s.objects[input.Bucket+"/"+input.Key]
	s.mu.Unlock()
	if !ok {
		return objectstore.GetObjectResult{}, objectstore.ErrNotFound
	}
	return objectstore.GetObjectResult{Bucket: input.Bucket, Key: input.Key, Body: io.NopCloser(bytes.NewReader(payload)), SizeBytes: int64(len(payload)), ContentType: "text/plain", ETag: "exchange-etag"}, nil
}

func (s *artifactExchangeTestObjectStore) DeleteObject(_ context.Context, input objectstore.DeleteObjectInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := input.Bucket + "/" + input.Key
	if _, ok := s.objects[key]; !ok {
		return objectstore.ErrNotFound
	}
	delete(s.objects, key)
	return nil
}

func (s *artifactExchangeTestObjectStore) PresignGetObject(context.Context, objectstore.PresignGetObjectInput) (objectstore.PresignedURL, error) {
	return objectstore.PresignedURL{}, objectstore.ErrNotConfigured
}

func TestArtifactExchangeImportAndReplayProtection(t *testing.T) {
	server, store, session, _ := newArtifactExchangeTestServer(t)
	payload := "signed import payload"
	digest := sha256Hex([]byte(payload))
	create := performArtifactExchangeRequest(t, server, http.MethodPost, "/v2/artifact-exchanges/imports", fmt.Sprintf(`{
		"session_id":%q,"filename":"result.txt","content_type":"text/plain",
		"expected_size_bytes":%d,"expected_checksum_sha256":%q
	}`, session.ID, len(payload), digest), "application/json")
	if create.Code != http.StatusCreated {
		t.Fatalf("create import exchange: %d %s", create.Code, create.Body.String())
	}
	var grant artifactExchangeGrant
	if err := json.Unmarshal(create.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(t, grant.Exchange)), "token") {
		t.Fatalf("persisted exchange exposed token material: %+v", grant.Exchange)
	}
	upload := performArtifactExchangeRequest(t, server, http.MethodPut, grant.ContentURL, payload, "text/plain")
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload exchange content: %d %s", upload.Code, upload.Body.String())
	}
	var result artifactExchangeImportResult
	if err := json.Unmarshal(upload.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Exchange.Status != managedagents.ArtifactExchangeStatusCompleted || result.Artifact.SessionID != session.ID || result.ObjectRef.ChecksumSHA256 != digest {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if artifacts, err := store.ListSessionArtifacts(session.ID); err != nil || len(artifacts) != 1 || artifacts[0].ID != result.Artifact.ID {
		t.Fatalf("import did not persist artifact: %+v err=%v", artifacts, err)
	}
	replay := performArtifactExchangeRequest(t, server, http.MethodPut, grant.ContentURL, payload, "text/plain")
	if replay.Code != http.StatusNotFound {
		t.Fatalf("replayed import exchange returned %d: %s", replay.Code, replay.Body.String())
	}
}

func TestArtifactExchangeExportAndReplayProtection(t *testing.T) {
	server, store, session, objects := newArtifactExchangeTestServer(t)
	payload := []byte("exported artifact")
	objects.objects["artifact-exchange-test/source.txt"] = payload
	objectRef, err := store.CreateObjectRef(managedagents.CreateObjectRefInput{
		WorkspaceID: session.WorkspaceID, StorageProvider: objectstore.ProviderS3,
		Bucket: "artifact-exchange-test", ObjectKey: "source.txt", ContentType: "text/plain",
		SizeBytes: int64(len(payload)), Visibility: managedagents.ObjectVisibilitySession, CreatedBy: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateSessionArtifact(managedagents.CreateSessionArtifactInput{
		SessionID: session.ID, ObjectRefID: objectRef.ID, Name: "source.txt", ArtifactType: managedagents.ArtifactTypeFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := performArtifactExchangeRequest(t, server, http.MethodPost, "/v2/artifact-exchanges/exports", fmt.Sprintf(`{
		"session_id":%q,"artifact_id":%q
	}`, session.ID, artifact.ID), "application/json")
	if create.Code != http.StatusCreated {
		t.Fatalf("create export exchange: %d %s", create.Code, create.Body.String())
	}
	var grant artifactExchangeGrant
	if err := json.Unmarshal(create.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	download := performArtifactExchangeRequest(t, server, http.MethodGet, grant.ContentURL, "", "")
	if download.Code != http.StatusOK || download.Body.String() != string(payload) {
		t.Fatalf("download exchange content: %d %q", download.Code, download.Body.String())
	}
	replay := performArtifactExchangeRequest(t, server, http.MethodGet, grant.ContentURL, "", "")
	if replay.Code != http.StatusNotFound {
		t.Fatalf("replayed export exchange returned %d: %s", replay.Code, replay.Body.String())
	}
}

func TestArtifactExchangeChecksumFailureConsumesGrant(t *testing.T) {
	server, _, session, _ := newArtifactExchangeTestServer(t)
	create := performArtifactExchangeRequest(t, server, http.MethodPost, "/v2/artifact-exchanges/imports", fmt.Sprintf(`{
		"session_id":%q,"filename":"result.txt","content_type":"text/plain",
		"expected_checksum_sha256":%q
	}`, session.ID, sha256Hex([]byte("expected"))), "application/json")
	var grant artifactExchangeGrant
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &grant) != nil {
		t.Fatalf("create checksum exchange: %d %s", create.Code, create.Body.String())
	}
	failed := performArtifactExchangeRequest(t, server, http.MethodPut, grant.ContentURL, "different", "text/plain")
	if failed.Code != http.StatusBadRequest {
		t.Fatalf("checksum mismatch returned %d: %s", failed.Code, failed.Body.String())
	}
	replay := performArtifactExchangeRequest(t, server, http.MethodPut, grant.ContentURL, "expected", "text/plain")
	if replay.Code != http.StatusNotFound {
		t.Fatalf("failed grant was reusable: %d %s", replay.Code, replay.Body.String())
	}
}

func TestOnlyArtifactExchangeContentRouteIsPublic(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/v2/artifact-exchanges/aex_1/content", true},
		{http.MethodPut, "/v2/artifact-exchanges/aex_1/content", true},
		{http.MethodPost, "/v2/artifact-exchanges/aex_1/content", false},
		{http.MethodGet, "/v2/artifact-exchanges/aex_1", false},
		{http.MethodGet, "/v2/artifact-exchanges/imports", false},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		if got := isPublicRequest(request); got != test.want {
			t.Fatalf("isPublicRequest(%s %s) = %t, want %t", test.method, test.path, got, test.want)
		}
	}
}

func newArtifactExchangeTestServer(t *testing.T) (http.Handler, *testStore, managedagents.Session, *artifactExchangeTestObjectStore) {
	t.Helper()
	store := newTestStore()
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "exchange-environment", Config: json.RawMessage(`{"type":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{EnvironmentID: environment.ID, Name: "exchange-agent", Model: "fake-demo", System: "test"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	objects := newArtifactExchangeTestObjectStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(
		store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objects,
	)
	return server, store, session, objects
}

func performArtifactExchangeRequest(t *testing.T, server http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
