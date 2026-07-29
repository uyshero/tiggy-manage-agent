package managedagents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tiggy-manage-agent/internal/objectcleanup"
	"tiggy-manage-agent/internal/objectstore"
)

func TestPersistSessionArtifactObjectRecordsActualProvider(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	store := &artifactWriteTestStore{}

	objectRef, artifact, err := PersistSessionArtifactObject(context.Background(), store, client, artifactWriteTestInput())
	if err != nil {
		t.Fatalf("persist artifact object: %v", err)
	}
	if objectRef.StorageProvider != objectstore.ProviderLocalFS {
		t.Fatalf("expected localfs provider, got %q", objectRef.StorageProvider)
	}
	if artifact.ObjectRefID != objectRef.ID {
		t.Fatalf("artifact should reference %q, got %q", objectRef.ID, artifact.ObjectRefID)
	}
}

func TestPersistSessionArtifactObjectMergesManagedLifecycleMetadata(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	store := &artifactWriteTestStore{}
	input := artifactWriteTestInput()
	input.ObjectRef.Metadata = json.RawMessage(`{"deliverable":{"template_version":"v3"},"object_lifecycle":{"source":"tool"}}`)

	if _, _, err := PersistSessionArtifactObject(context.Background(), store, client, input); err != nil {
		t.Fatalf("persist artifact object: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(store.createdObjectRefInput.Metadata, &metadata); err != nil {
		t.Fatalf("decode merged metadata: %v", err)
	}
	lifecycle, _ := metadata[objectLifecycleMetadataKey].(map[string]any)
	deliverable, _ := metadata["deliverable"].(map[string]any)
	if lifecycle[objectLifecycleClassKey] != objectLifecycleManaged || lifecycle["source"] != "tool" || deliverable["template_version"] != "v3" {
		t.Fatalf("unexpected merged metadata: %s", store.createdObjectRefInput.Metadata)
	}
}

func TestPersistSessionArtifactObjectMarksCustomKeyExternal(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	store := &artifactWriteTestStore{}
	input := artifactWriteTestInput()
	input.DeleteObjectOnFailure = false
	input.ObjectRef.Metadata = json.RawMessage(`{"object_lifecycle":{"class":"managed"}}`)

	if _, _, err := PersistSessionArtifactObject(context.Background(), store, client, input); err != nil {
		t.Fatalf("persist artifact object: %v", err)
	}
	var metadata struct {
		Lifecycle struct {
			Class string `json:"class"`
		} `json:"object_lifecycle"`
	}
	if err := json.Unmarshal(store.createdObjectRefInput.Metadata, &metadata); err != nil {
		t.Fatalf("decode lifecycle metadata: %v", err)
	}
	if metadata.Lifecycle.Class != objectLifecycleExternal {
		t.Fatalf("custom key must not be managed by orphan sweep: %s", store.createdObjectRefInput.Metadata)
	}
}

func TestPersistSessionArtifactObjectDeletesBodyWhenObjectRefCreationFails(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	createErr := errors.New("create object ref failed")
	store := &artifactWriteTestStore{createObjectRefErr: createErr}

	_, _, err := PersistSessionArtifactObject(context.Background(), store, client, artifactWriteTestInput())
	if !errors.Is(err, createErr) {
		t.Fatalf("expected create error, got %v", err)
	}
	assertArtifactWriteObjectMissing(t, client)
}

func TestPersistSessionArtifactObjectPreservesUnownedKeyWhenObjectRefCreationFails(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	createErr := errors.New("create object ref failed")
	store := &artifactWriteTestStore{createObjectRefErr: createErr}
	input := artifactWriteTestInput()
	input.DeleteObjectOnFailure = false

	_, _, err := PersistSessionArtifactObject(context.Background(), store, client, input)
	if !errors.Is(err, createErr) {
		t.Fatalf("expected create error, got %v", err)
	}
	object, getErr := client.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"})
	if getErr != nil {
		t.Fatalf("unowned key should not be deleted: %v", getErr)
	}
	_ = object.Body.Close()
	if len(store.enqueuedCleanup) != 1 || store.enqueuedCleanup[0].SafeToDelete || store.enqueuedCleanup[0].Reason != objectcleanup.ReasonUnsafeCustomKey {
		t.Fatalf("expected blocked cleanup journal, got %+v", store.enqueuedCleanup)
	}
}

func TestPersistSessionArtifactObjectJournalsFailedAutomaticDelete(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	deleteErr := errors.New("object store delete failed")
	failingClient := &artifactWriteDeleteFailStore{
		Client: client, config: objectstore.Config{Provider: objectstore.ProviderLocalFS}, err: deleteErr,
	}
	createErr := errors.New("create object ref failed")
	store := &artifactWriteTestStore{createObjectRefErr: createErr}

	_, _, err := PersistSessionArtifactObject(context.Background(), store, failingClient, artifactWriteTestInput())
	if !errors.Is(err, createErr) || !errors.Is(err, deleteErr) {
		t.Fatalf("expected create and delete errors, got %v", err)
	}
	if len(store.enqueuedCleanup) != 1 || !store.enqueuedCleanup[0].SafeToDelete || store.enqueuedCleanup[0].Reason != objectcleanup.ReasonObjectRefCreateFailed {
		t.Fatalf("expected retryable cleanup journal, got %+v", store.enqueuedCleanup)
	}
	object, getErr := client.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"})
	if getErr != nil {
		t.Fatalf("failed delete should leave object for retry: %v", getErr)
	}
	_ = object.Body.Close()
}

func TestPersistSessionArtifactObjectRollsBackRefAndBodyWhenArtifactCreationFails(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	createErr := errors.New("create artifact failed")
	store := &artifactWriteTestStore{createArtifactErr: createErr}

	_, _, err := PersistSessionArtifactObject(context.Background(), store, client, artifactWriteTestInput())
	if !errors.Is(err, createErr) {
		t.Fatalf("expected create error, got %v", err)
	}
	if store.deletedObjectRefID != "obj_test" {
		t.Fatalf("expected object ref rollback, got %q", store.deletedObjectRefID)
	}
	assertArtifactWriteObjectMissing(t, client)
}

func TestPersistSessionArtifactObjectPreservesBodyWhenRefRollbackFails(t *testing.T) {
	client := newArtifactWriteTestClient(t)
	createErr := errors.New("create artifact failed")
	deleteErr := errors.New("delete object ref failed")
	store := &artifactWriteTestStore{createArtifactErr: createErr, deleteObjectRefErr: deleteErr}

	_, _, err := PersistSessionArtifactObject(context.Background(), store, client, artifactWriteTestInput())
	if !errors.Is(err, createErr) || !errors.Is(err, deleteErr) {
		t.Fatalf("expected create and rollback errors, got %v", err)
	}
	object, getErr := client.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"})
	if getErr != nil {
		t.Fatalf("body should remain while object ref exists: %v", getErr)
	}
	_ = object.Body.Close()
}

func newArtifactWriteTestClient(t *testing.T) *objectstore.LocalFSClient {
	t.Helper()
	client, err := objectstore.NewLocalFSClient(objectstore.Config{RootDir: t.TempDir(), Bucket: "artifacts"})
	if err != nil {
		t.Fatalf("new local object store: %v", err)
	}
	return client
}

func artifactWriteTestInput() PersistSessionArtifactObjectInput {
	content := []byte("artifact body")
	return PersistSessionArtifactObjectInput{
		DeleteObjectOnFailure: true,
		PutObject: objectstore.PutObjectInput{
			Bucket:      "artifacts",
			Key:         "wksp/session/output.txt",
			Body:        bytes.NewReader(content),
			ContentType: "text/plain",
			SizeBytes:   int64(len(content)),
		},
		ObjectRef: CreateObjectRefInput{
			WorkspaceID: "wksp",
			Visibility:  ObjectVisibilitySession,
		},
		SessionArtifact: CreateSessionArtifactInput{
			WorkspaceID:  "wksp",
			SessionID:    "session",
			Name:         "output.txt",
			ArtifactType: ArtifactTypeFile,
		},
	}
}

func assertArtifactWriteObjectMissing(t *testing.T, client objectstore.Client) {
	t.Helper()
	_, err := client.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"})
	if !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("expected stored object cleanup, got %v", err)
	}
}

type artifactWriteTestStore struct {
	createObjectRefErr    error
	createArtifactErr     error
	deleteObjectRefErr    error
	deletedObjectRefID    string
	enqueuedCleanup       []objectcleanup.EnqueueInput
	createdObjectRefInput CreateObjectRefInput
}

func (s *artifactWriteTestStore) CreateObjectRef(input CreateObjectRefInput) (ObjectRef, error) {
	s.createdObjectRefInput = input
	if s.createObjectRefErr != nil {
		return ObjectRef{}, s.createObjectRefErr
	}
	return ObjectRef{
		ID:              "obj_test",
		WorkspaceID:     input.WorkspaceID,
		StorageProvider: input.StorageProvider,
		Bucket:          input.Bucket,
		ObjectKey:       input.ObjectKey,
		ObjectVersion:   input.ObjectVersion,
		ContentType:     input.ContentType,
		SizeBytes:       input.SizeBytes,
		ChecksumSHA256:  input.ChecksumSHA256,
		ETag:            input.ETag,
		Visibility:      input.Visibility,
		Metadata:        input.Metadata,
	}, nil
}

func (s *artifactWriteTestStore) DeleteObjectRef(id string) error {
	s.deletedObjectRefID = id
	return s.deleteObjectRefErr
}

func (s *artifactWriteTestStore) CreateSessionArtifact(input CreateSessionArtifactInput) (SessionArtifact, error) {
	if s.createArtifactErr != nil {
		return SessionArtifact{}, s.createArtifactErr
	}
	return SessionArtifact{
		ID:           "art_test",
		WorkspaceID:  input.WorkspaceID,
		SessionID:    input.SessionID,
		ObjectRefID:  input.ObjectRefID,
		Name:         input.Name,
		ArtifactType: input.ArtifactType,
	}, nil
}

func (s *artifactWriteTestStore) EnqueueObjectCleanup(_ context.Context, input objectcleanup.EnqueueInput) (objectcleanup.Job, error) {
	s.enqueuedCleanup = append(s.enqueuedCleanup, input)
	return objectcleanup.Job{ID: "ocj_test"}, nil
}

type artifactWriteDeleteFailStore struct {
	objectstore.Client
	config objectstore.Config
	err    error
}

func (s *artifactWriteDeleteFailStore) Config() objectstore.Config {
	return s.config
}

func (s *artifactWriteDeleteFailStore) DeleteObject(context.Context, objectstore.DeleteObjectInput) error {
	return s.err
}
