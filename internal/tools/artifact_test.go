package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

type stubArtifactToolService struct {
	descriptor ArtifactDescriptor
	page       ArtifactReadPage
	err        error
	read       ArtifactReadRequest
}

func (s *stubArtifactToolService) Inspect(context.Context, string, string) (ArtifactDescriptor, error) {
	return s.descriptor, s.err
}

func (s *stubArtifactToolService) Read(_ context.Context, _ string, request ArtifactReadRequest) (ArtifactReadPage, error) {
	s.read = request
	return s.page, s.err
}

func TestArtifactReadReturnsContentWithPagingMetadataOnlyInState(t *testing.T) {
	service := &stubArtifactToolService{page: ArtifactReadPage{
		Artifact: ArtifactDescriptor{ArtifactID: "art_1", Name: "result.json", ContentType: "application/json", SizeBytes: 20000, Metadata: json.RawMessage(`{"lineage":"large metadata"}`)},
		Content:  "focused page", OffsetBytes: 0, ReturnedBytes: 12, NextOffsetBytes: 12,
	}}
	result, err := (RegistryExecutor{Registry: NewRegistry(ArtifactRuntime{})}).Execute(t.Context(), Call{
		Name: "artifact_read", Arguments: json.RawMessage(`{"artifact_id":"art_1"}`),
	}, ExecutionContext{SessionID: "session_1", ArtifactService: service})
	if err != nil {
		t.Fatalf("read Artifact: %v", err)
	}
	if result.Error != nil || result.Content != "focused page" {
		t.Fatalf("unexpected Artifact result: %#v", result)
	}
	if service.read.MaxBytes != DefaultArtifactReadMaxBytes {
		t.Fatalf("expected default page size %d, got %#v", DefaultArtifactReadMaxBytes, service.read)
	}
	if strings.Contains(string(result.State), "focused page") || strings.Contains(string(result.State), "large metadata") {
		t.Fatalf("Artifact content and metadata must not be duplicated in read state: %s", result.State)
	}
	var state artifactReadState
	if err := json.Unmarshal(result.State, &state); err != nil || state.NextOffsetBytes != 12 || state.EOF {
		t.Fatalf("unexpected paging state %s: %v", result.State, err)
	}
}

func TestArtifactReadDoesNotCreateRecursiveToolArtifacts(t *testing.T) {
	service := &stubArtifactToolService{page: ArtifactReadPage{
		Artifact: ArtifactDescriptor{ArtifactID: "art_1", Name: "result.json"}, Content: "page", EOF: true,
	}}
	recorded := 0
	recorder := testArtifactRecorderFunc(func(context.Context, Call, ExecutionContext, ExecutionResult) ([]ArtifactRef, error) {
		recorded++
		return nil, nil
	})
	result, err := (RegistryExecutor{Registry: NewRegistry(ArtifactRuntime{})}).Execute(t.Context(), Call{
		Name: "artifact_read", Arguments: json.RawMessage(`{"artifact_id":"art_1"}`),
	}, ExecutionContext{SessionID: "session_1", ArtifactService: service, ArtifactRecorder: recorder})
	if err != nil || result.Error != nil {
		t.Fatalf("read Artifact: result=%#v err=%v", result, err)
	}
	if recorded != 0 || len(result.Artifacts) != 0 {
		t.Fatalf("Artifact reads must not create recursive artifacts: recorded=%d refs=%#v", recorded, result.Artifacts)
	}
}

type testArtifactRecorderFunc func(context.Context, Call, ExecutionContext, ExecutionResult) ([]ArtifactRef, error)

func (f testArtifactRecorderFunc) RecordToolArtifact(ctx context.Context, call Call, executionContext ExecutionContext, result ExecutionResult) ([]ArtifactRef, error) {
	return f(ctx, call, executionContext, result)
}

func TestArtifactInspectMapsSessionAccessErrors(t *testing.T) {
	service := &stubArtifactToolService{err: managedagents.ErrForbidden}
	result, err := (ArtifactRuntime{}).Execute(t.Context(), Call{
		Identifier: ArtifactIdentifier, APIName: ArtifactAPIInspect, Arguments: json.RawMessage(`{"artifact_id":"art_other"}`),
	}, ExecutionContext{SessionID: "session_1", ArtifactService: service})
	if err != nil {
		t.Fatalf("inspect Artifact: %v", err)
	}
	if result.Error == nil || result.Error.Type != "artifact_forbidden" {
		t.Fatalf("expected scoped Artifact rejection, got %#v", result)
	}
}

func TestArtifactRuntimeRequiresService(t *testing.T) {
	result, err := (ArtifactRuntime{}).Execute(t.Context(), Call{Identifier: ArtifactIdentifier, APIName: ArtifactAPIInspect}, ExecutionContext{SessionID: "session_1"})
	if err != nil {
		t.Fatalf("inspect without service: %v", err)
	}
	if result.Error == nil || result.Error.Type != "artifact_service_unavailable" {
		t.Fatalf("expected unavailable service error, got %#v", result)
	}
}
