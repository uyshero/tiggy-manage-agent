package execution

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workruntime"
)

func TestWorkerBackedProviderEditFileRunsThroughWorkerRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("alpha old omega\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := (capability.LocalSystemProvider{}).ReadFile(t.Context(), capability.ReadFileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	workerExecutor := workruntime.DefaultExecutor("loopback-worker")
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID: "wrk_edit", WorkspaceID: "wksp_edit", Status: managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces": []string{"default"}, "apis": []string{"default.edit_file"},
				"runtimes":     []string{"local_system"},
				"capabilities": []string{"filesystem.read", "filesystem.write"},
			}),
		}},
		loopbackExecutor: &workerExecutor,
	}
	provider := WorkerBackedProvider{
		Store: store, WorkspaceID: "wksp_edit", SessionID: "sesn_edit", TurnID: "turn_edit",
		PollInterval: time.Millisecond, WaitTimeout: time.Second,
	}
	result, err := provider.EditFile(t.Context(), capability.EditFileRequest{
		Path: path, OldString: "old", NewString: "new",
		ExpectedRevision: read.FileRevision, ExpectedContentSHA256: read.ContentSHA256,
	})
	if err != nil {
		t.Fatalf("worker-backed edit failed: %v", err)
	}
	if !result.Success || result.Replacements != 1 || result.FileRevision == "" || result.ContentSHA256 == "" || !strings.Contains(result.DiffText, "@@ -") {
		t.Fatalf("worker-backed edit lost result metadata: %#v", result)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "alpha new omega\n" {
		t.Fatalf("worker-backed edit content = %q err=%v", content, err)
	}
}

func TestWorkerBackedProviderReadFileEnqueuesAndDecodesResult(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	environmentCipher, err := envvars.NewCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
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
		Store:             store,
		WorkspaceID:       "wksp_000001",
		SessionID:         "sesn_000001",
		EnvironmentID:     "env_000001",
		TurnID:            "turn_000001",
		PollInterval:      time.Millisecond,
		WaitTimeout:       time.Second,
		Environment:       map[string]string{"SERVICE_API_KEY": "managed-secret-value"},
		EnvironmentCipher: environmentCipher,
	}

	offset := int64(4096)
	maxBytes := 2048
	file, err := provider.ReadFile(t.Context(), capability.ReadFileRequest{
		Path: "README.md", OffsetBytes: &offset, MaxBytes: &maxBytes, FileRevision: "stat-v1:revision",
	})
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
	if strings.Contains(string(store.enqueued.Payload), "managed-secret-value") || invocation.EnvironmentEnvelope == "" {
		t.Fatalf("expected encrypted environment envelope, got %s", store.enqueued.Payload)
	}
	resolvedEnvironment, err := environmentCipher.OpenMap(invocation.EnvironmentEnvelope, envvars.EnvelopeAssociatedData("wksp_000001", "sesn_000001", "turn_000001"))
	if err != nil || resolvedEnvironment["SERVICE_API_KEY"] != "managed-secret-value" {
		t.Fatalf("unexpected worker environment: %#v: %v", resolvedEnvironment, err)
	}
	if invocation.ProtocolVersion != tools.WorkProtocolVersion || invocation.Namespace != "default" || invocation.API != "read_file" || invocation.Runtime != "local_system" {
		t.Fatalf("unexpected invocation: %#v", invocation)
	}
	if invocation.Risk != tools.ToolRiskRead || len(invocation.Capabilities) != 1 || invocation.Capabilities[0] != tools.CapabilityFilesystemRead {
		t.Fatalf("expected invocation metadata from manifest, got %#v", invocation)
	}
	var input capability.ReadFileRequest
	if err := json.Unmarshal(invocation.Input, &input); err != nil {
		t.Fatalf("decode invocation input: %v", err)
	}
	if strings.Contains(string(invocation.Input), `"meta"`) {
		t.Fatalf("capability metadata must not leak into tool arguments: %s", invocation.Input)
	}
	if validationError := tools.DefaultRegistry().ValidateCallArguments(tools.Call{
		Identifier: tools.NamespaceDefault,
		APIName:    "read_file",
		Arguments:  invocation.Input,
	}); validationError != nil {
		t.Fatalf("worker invocation must satisfy the model-visible schema: %v", validationError)
	}
	if input.Path != "README.md" || input.OffsetBytes == nil || *input.OffsetBytes != offset || input.MaxBytes == nil || *input.MaxBytes != maxBytes || input.FileRevision != "stat-v1:revision" {
		t.Fatalf("expected invocation input path, got %#v", input)
	}
}

func TestWorkerBackedProviderSearchFileEnqueuesAndDecodesResult(t *testing.T) {
	state, err := json.Marshal(capability.SearchFileResult{
		Path: "app.log", SizeBytes: 100, FileRevision: "stat-v1:current", Query: "target",
		Matches: []capability.SearchFileMatch{{LineNumber: 7, OffsetBytes: 42, Line: "target line"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(map[string]any{"tool_result": tools.ExecutionResult{
		Identifier: tools.NamespaceDefault, APIName: "search_file", State: state,
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID: "wrk_search", WorkspaceID: "wksp_search", Status: managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces": []string{"default"}, "apis": []string{"default.search_file"},
				"runtimes": []string{"local_system"}, "capabilities": []string{"filesystem.read"},
			}),
		}},
		completedResult: result,
	}
	provider := WorkerBackedProvider{
		Store: store, WorkspaceID: "wksp_search", SessionID: "sesn_search", TurnID: "turn_search",
		PollInterval: time.Millisecond, WaitTimeout: time.Second,
	}

	searchResult, err := provider.SearchFile(t.Context(), capability.SearchFileRequest{
		Path: "app.log", Query: "target", MaxResults: 12, FileRevision: "stat-v1:expected",
	})
	if err != nil {
		t.Fatalf("search file through worker: %v", err)
	}
	if len(searchResult.Matches) != 1 || searchResult.Matches[0].OffsetBytes != 42 || searchResult.FileRevision != "stat-v1:current" {
		t.Fatalf("unexpected search result: %#v", searchResult)
	}
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(store.enqueued.Payload, &invocation); err != nil {
		t.Fatal(err)
	}
	if invocation.API != "search_file" || invocation.Risk != tools.ToolRiskRead {
		t.Fatalf("unexpected search invocation: %#v", invocation)
	}
	var input capability.SearchFileRequest
	if err := json.Unmarshal(invocation.Input, &input); err != nil {
		t.Fatal(err)
	}
	if input.Path != "app.log" || input.Query != "target" || input.MaxResults != 12 || input.FileRevision != "stat-v1:expected" {
		t.Fatalf("unexpected search input: %#v", input)
	}
}

func TestWorkerBackedProviderFindAndSearchFilesUseStandardWorkProtocol(t *testing.T) {
	t.Run("find_files", func(t *testing.T) {
		state, err := json.Marshal(capability.FindFilesResult{Root: ".", Pattern: "**/*.go", Files: []capability.FoundFile{{Path: "main.go", SizeBytes: 10}}})
		if err != nil {
			t.Fatal(err)
		}
		provider, store := workerProviderForFilesystemResult(t, "find_files", state)
		result, err := provider.FindFiles(t.Context(), capability.FindFilesRequest{Pattern: "**/*.go"})
		if err != nil || len(result.Files) != 1 || result.Files[0].Path != "main.go" {
			t.Fatalf("unexpected worker discovery: %#v err=%v", result, err)
		}
		assertWorkerFilesystemInvocation(t, store, "find_files")
	})
	t.Run("search_files", func(t *testing.T) {
		state, err := json.Marshal(capability.SearchFilesResult{Query: "needle", Mode: "literal", Matches: []capability.SearchFilesMatch{{Path: "main.go", LineNumber: 2}}})
		if err != nil {
			t.Fatal(err)
		}
		provider, store := workerProviderForFilesystemResult(t, "search_files", state)
		result, err := provider.SearchFiles(t.Context(), capability.SearchFilesRequest{Query: "needle", Paths: []string{"**/*.go"}})
		if err != nil || len(result.Matches) != 1 || result.Matches[0].Path != "main.go" {
			t.Fatalf("unexpected worker search: %#v err=%v", result, err)
		}
		assertWorkerFilesystemInvocation(t, store, "search_files")
	})
}

func workerProviderForFilesystemResult(t *testing.T, api string, state json.RawMessage) (WorkerBackedProvider, *workerBackedTestStore) {
	t.Helper()
	completed, err := json.Marshal(map[string]any{"tool_result": tools.ExecutionResult{
		Identifier: tools.NamespaceDefault, APIName: api, State: state,
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID: "wrk_files", WorkspaceID: "wksp_files", Status: managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces": []string{"default"}, "apis": []string{"default." + api},
				"runtimes": []string{"local_system"}, "capabilities": []string{"filesystem.read"},
			}),
		}},
		completedResult: completed,
	}
	return WorkerBackedProvider{
		Store: store, WorkspaceID: "wksp_files", SessionID: "sesn_files", TurnID: "turn_files",
		PollInterval: time.Millisecond, WaitTimeout: time.Second,
	}, store
}

func assertWorkerFilesystemInvocation(t *testing.T, store *workerBackedTestStore, api string) {
	t.Helper()
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(store.enqueued.Payload, &invocation); err != nil {
		t.Fatal(err)
	}
	if invocation.ProtocolVersion != tools.WorkProtocolVersion || invocation.API != api || invocation.Risk != tools.ToolRiskRead {
		t.Fatalf("unexpected worker invocation: %#v", invocation)
	}
}

func TestWorkerBackedProviderPreservesStructuredFileReadError(t *testing.T) {
	state, err := json.Marshal(map[string]any{"error": capability.FileReadError{
		Code: "stale_file_revision", Message: "file changed", Metadata: map[string]any{"path": "app.log"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(map[string]any{"tool_result": tools.ExecutionResult{
		Identifier: tools.NamespaceDefault, APIName: "read_file", State: state,
		Error: &tools.ExecutionError{Type: "stale_file_revision", Message: "file changed"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID: "wrk_read_error", WorkspaceID: "wksp_read_error", Status: managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, map[string]any{
				"namespaces": []string{"default"}, "apis": []string{"default.read_file"},
				"runtimes": []string{"local_system"}, "capabilities": []string{"filesystem.read"},
			}),
		}},
		completedResult: result,
	}
	provider := WorkerBackedProvider{
		Store: store, WorkspaceID: "wksp_read_error", SessionID: "sesn_read_error", TurnID: "turn_read_error",
		PollInterval: time.Millisecond, WaitTimeout: time.Second,
	}

	_, err = provider.ReadFile(t.Context(), capability.ReadFileRequest{Path: "app.log"})
	var readErr *capability.FileReadError
	if !errors.As(err, &readErr) || readErr.Code != "stale_file_revision" || readErr.Metadata["path"] != "app.log" {
		t.Fatalf("structured worker file error was lost: %v", err)
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

func TestWorkerBackedProviderCommandWaitTimeoutTracksToolDeadline(t *testing.T) {
	provider := WorkerBackedProvider{}
	derived := provider.withCommandWaitTimeout(90_000)
	if derived.WaitTimeout != 120*time.Second {
		t.Fatalf("expected command timeout plus worker grace, got %s", derived.WaitTimeout)
	}
	explicit := (WorkerBackedProvider{WaitTimeout: time.Second}).withCommandWaitTimeout(90_000)
	if explicit.WaitTimeout != time.Second {
		t.Fatalf("explicit worker wait timeout must be preserved, got %s", explicit.WaitTimeout)
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

func TestWorkerBackedProviderExecutesArbitraryWorkerTool(t *testing.T) {
	state := json.RawMessage(`{"status":"idle"}`)
	result, err := json.Marshal(map[string]any{
		"tool_result": tools.ExecutionResult{
			Identifier: "robot",
			APIName:    "get_state",
			Content:    "robot state: idle",
			State:      state,
		},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	store := &workerBackedTestStore{
		workers: []managedagents.Worker{{
			ID:          "wrk_robot",
			WorkspaceID: "wksp_000001",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: rawWorkerCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"robot"},
				APIs:         []string{"robot.get_state"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"robot.state"},
				Manifests:    []tools.Manifest{robotManifest()},
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
	manifest := robotManifest()
	executionResult, err := provider.ExecuteWorkerTool(t.Context(), manifest, manifest.API[0], tools.Call{
		ID:         "call_robot",
		Identifier: "robot",
		APIName:    "get_state",
		Arguments:  json.RawMessage(`{}`),
	}, tools.ExecutionContext{})
	if err != nil {
		t.Fatalf("execute arbitrary worker tool: %v", err)
	}
	if executionResult.Content != "robot state: idle" {
		t.Fatalf("unexpected execution result: %#v", executionResult)
	}
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(store.enqueued.Payload, &invocation); err != nil {
		t.Fatalf("decode enqueued invocation: %v", err)
	}
	if invocation.Namespace != "robot" || invocation.API != "get_state" || invocation.Runtime != tools.ToolRuntimeLocalSystem {
		t.Fatalf("unexpected robot invocation: %#v", invocation)
	}
	if invocation.Risk != tools.ToolRiskRead || len(invocation.Capabilities) != 1 || invocation.Capabilities[0] != "robot.state" {
		t.Fatalf("expected robot invocation metadata from manifest, got %#v", invocation)
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
	listInput        managedagents.ListWorkersInput
	workers          []managedagents.Worker
	enqueued         managedagents.EnqueueWorkerWorkInput
	completedResult  json.RawMessage
	loopbackExecutor *workruntime.Executor
}

func (s *workerBackedTestStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	s.listInput = input
	return append([]managedagents.Worker(nil), s.workers...), nil
}

func (s *workerBackedTestStore) EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error) {
	s.enqueued = input
	work := managedagents.WorkerWork{
		ID:          "work_000001",
		WorkspaceID: input.WorkspaceID,
		WorkerID:    input.WorkerID,
		WorkType:    managedagents.WorkerWorkTypeToolExecution,
		Status:      managedagents.WorkerWorkStatusPending,
		Payload:     input.Payload,
	}
	if s.loopbackExecutor != nil {
		completion := s.loopbackExecutor.Execute(context.Background(), work)
		s.completedResult = completion.Result
	}
	return work, nil
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
