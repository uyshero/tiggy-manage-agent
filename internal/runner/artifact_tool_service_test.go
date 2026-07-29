package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/tools"
)

type artifactToolTestStore struct {
	artifacts map[string]managedagents.SessionArtifact
	objects   map[string]managedagents.ObjectRef
}

func (s artifactToolTestStore) GetSessionArtifact(sessionID string, artifactID string) (managedagents.SessionArtifact, error) {
	artifact, ok := s.artifacts[sessionID+"/"+artifactID]
	if !ok {
		return managedagents.SessionArtifact{}, managedagents.ErrNotFound
	}
	return artifact, nil
}

func (s artifactToolTestStore) GetObjectRef(id string) (managedagents.ObjectRef, error) {
	objectRef, ok := s.objects[id]
	if !ok {
		return managedagents.ObjectRef{}, managedagents.ErrNotFound
	}
	return objectRef, nil
}

func TestArtifactToolServiceReadsVerifiedUTF8Pages(t *testing.T) {
	content := []byte("hello世界tail")
	service, _ := newArtifactToolTestService(t, content, "text/plain", "result.txt")

	first, err := service.Read(t.Context(), "session_1", tools.ArtifactReadRequest{ArtifactID: "art_1", MaxBytes: 7})
	if err != nil {
		t.Fatalf("read first page: %v", err)
	}
	if first.Content != "hello" || first.ReturnedBytes != 5 || first.NextOffsetBytes != 5 || first.EOF {
		t.Fatalf("first page split a UTF-8 character: %#v", first)
	}
	second, err := service.Read(t.Context(), "session_1", tools.ArtifactReadRequest{ArtifactID: "art_1", OffsetBytes: first.NextOffsetBytes, MaxBytes: 6})
	if err != nil {
		t.Fatalf("read second page: %v", err)
	}
	if second.Content != "世界" || second.ReturnedBytes != 6 || second.NextOffsetBytes != 11 || second.EOF {
		t.Fatalf("unexpected second page: %#v", second)
	}
	descriptor, err := service.Inspect(t.Context(), "session_1", "art_1")
	if err != nil {
		t.Fatalf("inspect Artifact: %v", err)
	}
	if descriptor.ChecksumSHA256 == "" || descriptor.SizeBytes != int64(len(content)) || !strings.Contains(string(descriptor.Metadata), "source_artifact_ids") {
		t.Fatalf("expected integrity and lineage metadata, got %#v", descriptor)
	}
	if _, err := service.Inspect(t.Context(), "session_other", "art_1"); err != managedagents.ErrNotFound {
		t.Fatalf("expected Session-scoped lookup, got %v", err)
	}
}

func TestArtifactToolServiceRejectsBinaryAndChecksumMismatch(t *testing.T) {
	binaryService, _ := newArtifactToolTestService(t, []byte("not really a pdf"), "application/pdf", "report.pdf")
	if _, err := binaryService.Read(t.Context(), "session_1", tools.ArtifactReadRequest{ArtifactID: "art_1", MaxBytes: 1024}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary rejection, got %v", err)
	}

	textService, store := newArtifactToolTestService(t, []byte("verified text"), "text/plain", "result.txt")
	objectRef := store.objects["obj_1"]
	objectRef.ChecksumSHA256 = strings.Repeat("0", 64)
	store.objects["obj_1"] = objectRef
	textService.Store = store
	if _, err := textService.Read(t.Context(), "session_1", tools.ArtifactReadRequest{ArtifactID: "art_1", MaxBytes: 1024}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum rejection, got %v", err)
	}
}

func newArtifactToolTestService(t *testing.T, content []byte, contentType string, name string) (ArtifactToolService, artifactToolTestStore) {
	t.Helper()
	client, err := objectstore.NewLocalFSClient(objectstore.Config{RootDir: t.TempDir(), Bucket: "artifacts"})
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}
	put, err := client.PutObject(context.Background(), objectstore.PutObjectInput{
		Bucket: "artifacts", Key: "session_1/art_1", Body: bytes.NewReader(content), ContentType: contentType, SizeBytes: int64(len(content)),
	})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}
	store := artifactToolTestStore{
		artifacts: map[string]managedagents.SessionArtifact{
			"session_1/art_1": {ID: "art_1", SessionID: "session_1", WorkspaceID: "workspace_1", ObjectRefID: "obj_1", Name: name, ArtifactType: managedagents.ArtifactTypeAsset, Metadata: json.RawMessage(`{"source_artifact_ids":["art_0"]}`)},
		},
		objects: map[string]managedagents.ObjectRef{
			"obj_1": {ID: "obj_1", WorkspaceID: "workspace_1", Bucket: put.Bucket, ObjectKey: put.Key, ObjectVersion: put.Version, ContentType: contentType, SizeBytes: put.SizeBytes, ChecksumSHA256: put.ChecksumSHA256},
		},
	}
	return ArtifactToolService{Store: store, ObjectStore: client}, store
}
