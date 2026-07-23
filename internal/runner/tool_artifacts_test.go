package runner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/tools"
)

func TestToolArtifactRecorderPersistsToolOutput(t *testing.T) {
	store := &mockStore{sessions: map[string]managedagents.Session{
		"sesn_000001": {
			ID:            "sesn_000001",
			WorkspaceID:   "wksp_default",
			EnvironmentID: "env_000001",
		},
	}}
	objectStore, err := objectstore.NewLocalFSClient(objectstore.Config{RootDir: t.TempDir(), Bucket: "tma-artifacts"})
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}
	recorder := ToolArtifactRecorder{Store: store, ObjectStore: objectStore, Bucket: "tma-artifacts"}

	result := tools.ExecutionResult{
		ID:            "call_1",
		Identifier:    "default",
		APIName:       "run_command",
		Content:       strings.Repeat("output line\n", 200),
		State:         json.RawMessage(`{"exit_code":0,"stdout":"` + strings.Repeat("output line ", 80) + `"}`),
		ArtifactError: "worker artifact upload warning",
		Artifacts: []tools.ArtifactRef{{
			ArtifactID:   "art_existing",
			ObjectRefID:  "obj_existing",
			Name:         "worker-export.txt",
			ArtifactType: managedagents.ArtifactTypeFile,
			DownloadPath: "/v1/sessions/sesn_000001/artifacts/art_existing/download",
		}},
	}
	refs, err := recorder.RecordToolArtifact(context.Background(), tools.Call{ID: "call_1", Identifier: "default", APIName: "run_command"}, tools.ExecutionContext{SessionID: "sesn_000001", TurnID: "turn_000001"}, result)
	if err != nil {
		t.Fatalf("record tool artifact: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 artifact ref, got %#v", refs)
	}
	if len(store.createdObjects) != 1 || len(store.createdArtifacts) != 1 {
		t.Fatalf("expected object ref and artifact to be recorded, got objects=%#v artifacts=%#v", store.createdObjects, store.createdArtifacts)
	}
	if store.createdArtifacts[0].ObjectRefID == "" || store.createdArtifacts[0].SessionID != "sesn_000001" {
		t.Fatalf("unexpected artifact input: %#v", store.createdArtifacts[0])
	}

	get, err := objectStore.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "tma-artifacts", Key: store.createdObjects[0].ObjectKey})
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	defer get.Body.Close()
	body, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatalf("read object body: %v", err)
	}
	if !strings.Contains(string(body), "tma.tool_artifact.v1") {
		t.Fatalf("unexpected artifact payload: %s", string(body))
	}
	var payload struct {
		Result struct {
			Artifacts     []tools.ArtifactRef `json:"artifacts"`
			ArtifactError string              `json:"artifact_error"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode artifact payload: %v", err)
	}
	if len(payload.Result.Artifacts) != 1 || payload.Result.Artifacts[0].ArtifactID != "art_existing" {
		t.Fatalf("expected existing artifact ref in persisted payload, got %#v", payload.Result.Artifacts)
	}
	if payload.Result.ArtifactError != "worker artifact upload warning" {
		t.Fatalf("expected artifact error in persisted payload, got %q", payload.Result.ArtifactError)
	}
}

func TestRegistryExecutorAttachesArtifactRefs(t *testing.T) {
	recorder := artifactRecorderFunc(func(context.Context, tools.Call, tools.ExecutionContext, tools.ExecutionResult) ([]tools.ArtifactRef, error) {
		return []tools.ArtifactRef{{ArtifactID: "art_000001", ObjectRefID: "obj_000001", Name: "tool_result.json", ArtifactType: managedagents.ArtifactTypeAsset, DownloadPath: "/v1/sessions/sesn_000001/artifacts/art_000001/download"}}, nil
	})
	executor := tools.RegistryExecutor{Registry: tools.NewRegistry(testArtifactRuntime{})}
	result, err := executor.Execute(context.Background(), tools.Call{ID: "call_1", Name: "artifact_test_run"}, tools.ExecutionContext{ArtifactRecorder: recorder})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("unexpected artifact refs: %#v", result.Artifacts)
	}
	if result.Artifacts[0].ArtifactID != "art_existing" || result.Artifacts[1].ArtifactID != "art_000001" {
		t.Fatalf("artifact refs should preserve runtime refs before recorder refs, got %#v", result.Artifacts)
	}
	encoded := tools.ResultMessage(result)
	if !strings.Contains(encoded, "art_existing") || !strings.Contains(encoded, "art_000001") || !strings.Contains(encoded, "artifact_error") {
		t.Fatalf("expected artifact refs in result message, got %s", encoded)
	}
}

func TestToolArtifactRecorderPersistsExportedFiles(t *testing.T) {
	store := &mockStore{sessions: map[string]managedagents.Session{
		"sesn_000001": {
			ID:            "sesn_000001",
			WorkspaceID:   "wksp_default",
			EnvironmentID: "env_000001",
		},
	}}
	objectStore, err := objectstore.NewLocalFSClient(objectstore.Config{RootDir: t.TempDir(), Bucket: "tma-artifacts"})
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}
	recorder := ToolArtifactRecorder{Store: store, ObjectStore: objectStore, Bucket: "tma-artifacts"}

	workDir := t.TempDir()
	exportPath := filepath.Join(workDir, "report.txt")
	if err := os.WriteFile(exportPath, []byte("artifact body"), 0o644); err != nil {
		t.Fatalf("write export file: %v", err)
	}
	result := tools.ExecutionResult{
		ID:         "call_2",
		Identifier: "default",
		APIName:    "run_command",
		Content:    "saved file",
		ExportedFiles: []tools.ArtifactExport{{
			Path:    "report.txt",
			WorkDir: workDir,
		}},
	}
	refs, err := recorder.RecordToolArtifact(context.Background(), tools.Call{ID: "call_2", Identifier: "default", APIName: "run_command"}, tools.ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	}, result)
	if err != nil {
		t.Fatalf("record exported file artifact: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected tool result artifact plus exported file artifact, got %#v", refs)
	}
	if len(store.createdObjects) != 2 || len(store.createdArtifacts) != 2 {
		t.Fatalf("expected two created objects/artifacts, got objects=%#v artifacts=%#v", store.createdObjects, store.createdArtifacts)
	}
	if store.createdArtifacts[1].ArtifactType != managedagents.ArtifactTypeFile || store.createdArtifacts[1].Name != "report.txt" {
		t.Fatalf("unexpected exported artifact input: %#v", store.createdArtifacts[1])
	}
	get, err := objectStore.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "tma-artifacts", Key: store.createdObjects[1].ObjectKey})
	if err != nil {
		t.Fatalf("get exported object: %v", err)
	}
	defer get.Body.Close()
	body, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatalf("read exported object body: %v", err)
	}
	if string(body) != "artifact body" {
		t.Fatalf("unexpected exported object body: %q", string(body))
	}
}

func TestToolArtifactRecorderPersistsTransportedExportedFilesWithoutExporter(t *testing.T) {
	store := &mockStore{sessions: map[string]managedagents.Session{
		"sesn_000001": {
			ID:            "sesn_000001",
			WorkspaceID:   "wksp_default",
			EnvironmentID: "env_000001",
		},
	}}
	objectStore, err := objectstore.NewLocalFSClient(objectstore.Config{RootDir: t.TempDir(), Bucket: "tma-artifacts"})
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}
	recorder := ToolArtifactRecorder{Store: store, ObjectStore: objectStore, Bucket: "tma-artifacts"}

	result := tools.ExecutionResult{
		ID:         "call_3",
		Identifier: "default",
		APIName:    "run_command",
		Content:    "saved file",
		ExportedFiles: []tools.ArtifactExport{{
			Path:        "result.txt",
			Name:        "result.txt",
			ContentType: "text/plain",
			Content:     []byte("worker artifact body"),
		}},
	}
	refs, err := recorder.RecordToolArtifact(context.Background(), tools.Call{ID: "call_3", Identifier: "default", APIName: "run_command"}, tools.ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  nil,
	}, result)
	if err != nil {
		t.Fatalf("record transported exported file artifact: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected tool result artifact plus transported file artifact, got %#v", refs)
	}
	get, err := objectStore.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: "tma-artifacts", Key: store.createdObjects[1].ObjectKey})
	if err != nil {
		t.Fatalf("get transported object: %v", err)
	}
	defer get.Body.Close()
	body, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatalf("read transported object body: %v", err)
	}
	if string(body) != "worker artifact body" {
		t.Fatalf("unexpected transported object body: %q", string(body))
	}
}

type artifactRecorderFunc func(context.Context, tools.Call, tools.ExecutionContext, tools.ExecutionResult) ([]tools.ArtifactRef, error)

func (fn artifactRecorderFunc) RecordToolArtifact(ctx context.Context, call tools.Call, executionContext tools.ExecutionContext, result tools.ExecutionResult) ([]tools.ArtifactRef, error) {
	return fn(ctx, call, executionContext, result)
}

type testArtifactRuntime struct{}

func (testArtifactRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "artifact.test",
		API:        []tools.API{{Name: "run", Description: "run"}},
	}
}

func (testArtifactRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{
		Identifier: "artifact.test",
		APIName:    "run",
		Content:    "done",
		Artifacts: []tools.ArtifactRef{{
			ArtifactID:   "art_existing",
			ObjectRefID:  "obj_existing",
			Name:         "worker-export.txt",
			ArtifactType: managedagents.ArtifactTypeFile,
			DownloadPath: "/v1/sessions/sesn_000001/artifacts/art_existing/download",
		}},
	}, nil
}
