package execution

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestWorkerBackedProviderReadFileEnqueuesAndDecodesResult(t *testing.T) {
	state, err := json.Marshal(capability.FileResult{Path: "README.md", Content: []byte("hello")})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	result, err := json.Marshal(map[string]any{
		"tool_result": tools.ExecutionResult{
			Identifier: tools.NamespaceDefault,
			APIName:    "read_file",
			Content:    "hello",
			State:      state,
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID:          "wrk_000001",
			WorkspaceID: "wksp_000001",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces":   []string{"default"},
				"apis":         []string{"default.read_file"},
				"runtimes":     []string{"local_system"},
				"capabilities": []string{"filesystem.read"},
			}),
		}},
		completedResult: result,
	}
	provider := WorkerBackedProvider{
		Store:         store,
		WorkspaceID:   "wksp_000001",
		SessionID:     "sesn_000001",
		EnvironmentID: "env_000001",
		TurnID:        "turn_000001",
		PollInterval:  time.Millisecond,
		WaitTimeout:   time.Second,
	}

	file, err := provider.ReadFile(t.Context(), capability.ReadFileRequest{Path: "README.md"})
	if err != nil {
		t.Fatalf("read file through worker: %v", err)
	}
	if file.Path != "README.md" || string(file.Content) != "hello" {
		t.Fatalf("unexpected file result: %#v", file)
	}
	if store.listInput.WorkspaceID != "wksp_000001" || store.listInput.Status != managedagents.WorkerStatusOnline {
		t.Fatalf("unexpected worker selection input: %#v", store.listInput)
	}
	if store.enqueued.WorkerID != "wrk_000001" || store.enqueued.WorkspaceID != "wksp_000001" || store.enqueued.SessionID != "sesn_000001" || store.enqueued.TurnID != "turn_000001" {
		t.Fatalf("unexpected enqueued work: %#v", store.enqueued)
	}
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(store.enqueued.Payload, &invocation); err != nil {
		t.Fatalf("decode enqueued invocation: %v", err)
	}
	if invocation.ProtocolVersion != tools.WorkProtocolVersion || invocation.Namespace != "default" || invocation.API != "read_file" || invocation.Runtime != "local_system" {
		t.Fatalf("unexpected invocation: %#v", invocation)
	}
	if invocation.Risk != tools.ToolRiskRead || len(invocation.Capabilities) != 1 || invocation.Capabilities[0] != tools.CapabilityFilesystemRead {
		t.Fatalf("expected invocation metadata from manifest, got %#v", invocation)
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(invocation.Input, &input); err != nil {
		t.Fatalf("decode invocation input: %v", err)
	}
	if input.Path != "README.md" {
		t.Fatalf("expected invocation input path, got %#v", input)
	}
}

func TestWorkerBackedProviderRunCommandDecodesExportedArtifacts(t *testing.T) {
	state, err := json.Marshal(capability.CommandResult{ExitCode: 0, Stdout: "ok"})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	result, err := json.Marshal(map[string]any{
		"tool_result": tools.ExecutionResult{
			Identifier: tools.NamespaceDefault,
			APIName:    "run_command",
			Content:    "ok",
			State:      state,
			ExportedFiles: []tools.ArtifactExport{{
				Path:          "result.txt",
				Name:          "result.txt",
				ContentType:   "text/plain",
				ContentBase64: base64.StdEncoding.EncodeToString([]byte("worker-export")),
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID:          "wrk_000001",
			WorkspaceID: "wksp_000001",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces":   []string{"default"},
				"apis":         []string{"default.run_command"},
				"runtimes":     []string{"local_system"},
				"capabilities": []string{"exec"},
			}),
		}},
		completedResult: result,
	}
	provider := WorkerBackedProvider{
		Store:         store,
		WorkspaceID:   "wksp_000001",
		SessionID:     "sesn_000001",
		EnvironmentID: "env_000001",
		TurnID:        "turn_000001",
		PollInterval:  time.Millisecond,
		WaitTimeout:   time.Second,
	}

	command, err := provider.RunCommand(t.Context(), capability.RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "printf ok"},
	})
	if err != nil {
		t.Fatalf("run command through worker: %v", err)
	}
	if command.Stdout != "ok" || len(command.ExportedArtifacts) != 1 {
		t.Fatalf("unexpected command result: %#v", command)
	}
	if string(command.ExportedArtifacts[0].Content) != "worker-export" || command.ExportedArtifacts[0].Name != "result.txt" {
		t.Fatalf("unexpected exported artifacts: %#v", command.ExportedArtifacts)
	}
}

func TestWorkerBackedProviderRunCommandDecodesUploadedArtifactRefs(t *testing.T) {
	state, err := json.Marshal(capability.CommandResult{ExitCode: 0, Stdout: "ok"})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	result, err := json.Marshal(map[string]any{
		"tool_result": tools.ExecutionResult{
			Identifier: tools.NamespaceDefault,
			APIName:    "run_command",
			Content:    "ok",
			State:      state,
			Artifacts: []tools.ArtifactRef{{
				ArtifactID:   "art_large",
				ObjectRefID:  "obj_large",
				Name:         "large.bin",
				ArtifactType: "file",
				DownloadPath: "/v1/sessions/sesn_000001/artifacts/art_large/download",
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID:          "wrk_000001",
			WorkspaceID: "wksp_000001",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces":   []string{"default"},
				"apis":         []string{"default.run_command"},
				"runtimes":     []string{"local_system"},
				"capabilities": []string{"exec"},
			}),
		}},
		completedResult: result,
	}
	provider := WorkerBackedProvider{
		Store:         store,
		WorkspaceID:   "wksp_000001",
		SessionID:     "sesn_000001",
		EnvironmentID: "env_000001",
		TurnID:        "turn_000001",
		PollInterval:  time.Millisecond,
		WaitTimeout:   time.Second,
	}

	command, err := provider.RunCommand(t.Context(), capability.RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "printf ok"},
	})
	if err != nil {
		t.Fatalf("run command through worker: %v", err)
	}
	if len(command.Artifacts) != 1 || command.Artifacts[0].ArtifactID != "art_large" {
		t.Fatalf("unexpected artifact refs: %#v", command.Artifacts)
	}
}

func TestDecodeWorkerToolResultRejectsOversizedExportedFile(t *testing.T) {
	result, err := json.Marshal(map[string]any{
		"tool_result": tools.ExecutionResult{
			Identifier: tools.NamespaceDefault,
			APIName:    "run_command",
			Content:    "ok",
			ExportedFiles: []tools.ArtifactExport{{
				Path:          "large.bin",
				ContentBase64: strings.Repeat("A", base64.StdEncoding.EncodedLen(tools.MaxTransportedArtifactBytes+1)),
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	_, err = decodeWorkerToolResult(managedagents.WorkerWork{
		ID:     "work_large",
		Status: managedagents.WorkerWorkStatusCompleted,
		Result: result,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds transported artifact limit") {
		t.Fatalf("expected transported artifact limit error, got %v", err)
	}
}

type workerBackedTestStore struct {
	listInput       managedagents.ListWorkersInput
	workers         []managedagents.Worker
	enqueued        managedagents.EnqueueWorkerWorkInput
	completedResult json.RawMessage
}

func (s *workerBackedTestStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	s.listInput = input
	return append([]managedagents.Worker(nil), s.workers...), nil
}

func (s *workerBackedTestStore) EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error) {
	s.enqueued = input
	return managedagents.WorkerWork{
		ID:          "work_000001",
		WorkspaceID: input.WorkspaceID,
		WorkerID:    input.WorkerID,
		WorkType:    managedagents.WorkerWorkTypeToolExecution,
		Status:      managedagents.WorkerWorkStatusPending,
		Payload:     input.Payload,
	}, nil
}

func (s *workerBackedTestStore) GetWorkerWork(id string) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{
		ID:          id,
		WorkspaceID: s.enqueued.WorkspaceID,
		WorkerID:    s.enqueued.WorkerID,
		WorkType:    managedagents.WorkerWorkTypeToolExecution,
		Status:      managedagents.WorkerWorkStatusCompleted,
		Result:      s.completedResult,
	}, nil
}

func rawWorkerCapabilities(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	return encoded
}
