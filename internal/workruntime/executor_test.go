package workruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestLocalSystemCapabilitiesComeFromToolManifest(t *testing.T) {
	capabilities := LocalSystemCapabilities(tools.DefaultRegistry())

	if !contains(capabilities.Namespaces, tools.NamespaceDefault) {
		t.Fatalf("expected default namespace, got %#v", capabilities.Namespaces)
	}
	if !contains(capabilities.APIs, "default.run_command") || !contains(capabilities.APIs, "default.read_file") {
		t.Fatalf("expected default APIs, got %#v", capabilities.APIs)
	}
	if !contains(capabilities.Capabilities, tools.CapabilityExec) || !contains(capabilities.Capabilities, tools.CapabilityFilesystemRead) {
		t.Fatalf("expected default capabilities, got %#v", capabilities.Capabilities)
	}
	if len(capabilities.Runtimes) != 1 || capabilities.Runtimes[0] != tools.ToolRuntimeLocalSystem {
		t.Fatalf("expected local_system runtime, got %#v", capabilities.Runtimes)
	}
}

func TestExecutorCanDeclareWorkerCapabilities(t *testing.T) {
	declared := tools.WorkerCapabilities{
		Namespaces:   []string{tools.NamespaceArtifact},
		APIs:         []string{"artifact.write"},
		Runtimes:     []string{tools.ToolRuntimeLocalSystem},
		Capabilities: []string{"artifact.write"},
		Constraints:  map[string]any{"network": "disabled"},
	}
	executor := Executor{DeclaredCapabilities: &declared}

	capabilities := executor.WorkerCapabilities()
	if !contains(capabilities.Namespaces, tools.NamespaceArtifact) ||
		!contains(capabilities.APIs, "artifact.write") ||
		!contains(capabilities.Capabilities, "artifact.write") ||
		capabilities.Constraints["network"] != "disabled" {
		t.Fatalf("unexpected declared capabilities: %#v", capabilities)
	}

	capabilities.APIs[0] = "artifact.changed"
	if contains(executor.WorkerCapabilities().APIs, "artifact.changed") {
		t.Fatalf("expected executor capabilities result to be cloned")
	}
}

func TestExecutorRunsDefaultToolExecution(t *testing.T) {
	result := DefaultExecutor("test-worker").Execute(t.Context(), managedagents.WorkerWork{
		ID:       "work_000001",
		WorkType: managedagents.WorkerWorkTypeToolExecution,
		Payload:  json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","capabilities":["exec"],"risk":"exec","runtime":"local_system","input":{"command":"sh","args":["-c","printf workruntime"]}}`),
	})
	if !result.Success {
		t.Fatalf("expected successful execution, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		WorkerName string `json:"worker_name"`
		ToolResult struct {
			Content string `json:"content"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if body.WorkerName != "test-worker" || body.ToolResult.Content != "workruntime" {
		t.Fatalf("unexpected result body: %+v", body)
	}
}

func TestExecutorDecryptsManagedEnvironmentAndRedactsToolOutput(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(key)
	t.Setenv(envvars.MasterKeyEnvironmentVariable, encodedKey)
	cipher, err := envvars.NewCipher(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := cipher.SealMap(map[string]string{"SERVICE_API_KEY": "managed-secret-value"}, envvars.EnvelopeAssociatedData("wksp_default", "sesn_env", "turn_env"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(tools.WorkInvocation{
		ProtocolVersion:     tools.WorkProtocolVersion,
		Namespace:           tools.NamespaceDefault,
		API:                 "run_command",
		Capabilities:        []string{"exec"},
		Risk:                tools.ToolRiskExec,
		Runtime:             tools.ToolRuntimeLocalSystem,
		Input:               json.RawMessage(`{"command":"sh","args":["-c","printf %s \"$SERVICE_API_KEY\""]}`),
		EnvironmentEnvelope: envelope,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := DefaultExecutor("test-worker").Execute(t.Context(), managedagents.WorkerWork{
		ID: "work_env", WorkspaceID: "wksp_default", SessionID: "sesn_env", TurnID: "turn_env",
		WorkType: managedagents.WorkerWorkTypeToolExecution, Payload: payload,
	})
	if !result.Success {
		t.Fatalf("expected successful execution, got %q", result.ErrorMessage)
	}
	if strings.Contains(string(result.Result), "managed-secret-value") || !strings.Contains(string(result.Result), "[REDACTED_ENV:SERVICE_API_KEY]") {
		t.Fatalf("worker result was not redacted: %s", result.Result)
	}
}

func TestExecutorRunsProcessPluginToolExecution(t *testing.T) {
	plugin := writeExecutable(t, `#!/bin/sh
case "$1" in
  manifest)
    printf '%s' '{"identifier":"robot","type":"process_plugin","meta":{"title":"Robot","description":"Robot control plugin."},"system_role":"Use robot.* tools only for robot control tasks.","api":[{"name":"get_state","description":"Read robot state.","parameters":{"type":"object","properties":{}},"capabilities":["robot.state"],"risk":"read","runtime":{"allowed":["local_system"],"preferred":"local_system"},"implementation":"worker_capability"}]}'
    ;;
  execute)
    cat >/dev/null
    printf '%s' '{"protocol_version":"tma.plugin_result.v1","success":true,"content":"robot state: idle","state":{"status":"idle"}}'
    ;;
  *)
    exit 64
    ;;
esac
`)
	pluginRuntime, err := tools.LoadProcessPluginRuntime(t.Context(), plugin)
	if err != nil {
		t.Fatalf("load plugin runtime: %v", err)
	}
	registry := tools.DefaultRegistry()
	registry.Register(pluginRuntime)
	executor := DefaultExecutor("test-worker")
	executor.Registry = registry

	capabilities := executor.WorkerCapabilities()
	if !contains(capabilities.Namespaces, "robot") ||
		!contains(capabilities.APIs, "robot.get_state") ||
		!contains(capabilities.Capabilities, "robot.state") {
		t.Fatalf("expected plugin capabilities to be advertised, got %#v", capabilities)
	}
	if len(capabilities.Manifests) == 0 || capabilities.Manifests[len(capabilities.Manifests)-1].Identifier != "robot" {
		t.Fatalf("expected plugin manifest to be advertised, got %#v", capabilities.Manifests)
	}

	result := executor.Execute(t.Context(), managedagents.WorkerWork{
		ID:       "work_plugin_000001",
		WorkType: managedagents.WorkerWorkTypeToolExecution,
		Payload:  json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"robot","api":"get_state","capabilities":["robot.state"],"risk":"read","runtime":"local_system","input":{}}`),
	})
	if !result.Success {
		t.Fatalf("expected successful plugin execution, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		ToolResult struct {
			Content string          `json:"content"`
			State   json.RawMessage `json:"state"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode plugin result: %v", err)
	}
	if body.ToolResult.Content != "robot state: idle" || !strings.Contains(string(body.ToolResult.State), `"status":"idle"`) {
		t.Fatalf("unexpected plugin result: %+v", body.ToolResult)
	}
}

func TestExecutorEmbedsExportedFilesInToolResult(t *testing.T) {
	workDir := t.TempDir()
	result := DefaultExecutor("test-worker").Execute(t.Context(), managedagents.WorkerWork{
		ID:       "work_000002",
		WorkType: managedagents.WorkerWorkTypeToolExecution,
		Payload:  json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","capabilities":["exec"],"risk":"exec","runtime":"local_system","input":{"command":"sh","args":["-c","printf worker-export > result.txt && printf ok"],"work_dir":` + `"` + workDir + `"` + `,"output_paths":["result.txt"]}}`),
	})
	if !result.Success {
		t.Fatalf("expected successful execution, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		ToolResult struct {
			ExportedFiles []tools.ArtifactExport `json:"exported_files"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(body.ToolResult.ExportedFiles) != 1 {
		t.Fatalf("expected 1 exported file, got %#v", body.ToolResult.ExportedFiles)
	}
	content, err := base64.StdEncoding.DecodeString(body.ToolResult.ExportedFiles[0].ContentBase64)
	if err != nil {
		t.Fatalf("decode exported content: %v", err)
	}
	if string(content) != "worker-export" {
		t.Fatalf("unexpected exported content: %q", string(content))
	}
	if _, err := os.Stat(filepath.Join(workDir, "result.txt")); err != nil {
		t.Fatalf("expected worker output file to exist: %v", err)
	}
}

func TestExecutorSkipsOversizedExportedFiles(t *testing.T) {
	workDir := t.TempDir()
	largePath := filepath.Join(workDir, "large.bin")
	if err := os.WriteFile(largePath, []byte(strings.Repeat("x", tools.MaxTransportedArtifactBytes+1)), 0o644); err != nil {
		t.Fatalf("write large export file: %v", err)
	}

	result := DefaultExecutor("test-worker").Execute(t.Context(), managedagents.WorkerWork{
		ID:       "work_000003",
		WorkType: managedagents.WorkerWorkTypeToolExecution,
		Payload:  json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","capabilities":["exec"],"risk":"exec","runtime":"local_system","input":{"command":"sh","args":["-c","printf ok"],"work_dir":` + `"` + workDir + `"` + `,"output_paths":["large.bin"]}}`),
	})
	if !result.Success {
		t.Fatalf("expected command success even when artifact export is skipped, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		ToolResult struct {
			ExportedFiles []tools.ArtifactExport `json:"exported_files"`
			ArtifactError string                 `json:"artifact_error"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(body.ToolResult.ExportedFiles) != 0 {
		t.Fatalf("expected oversized export to be omitted, got %#v", body.ToolResult.ExportedFiles)
	}
	if !strings.Contains(body.ToolResult.ArtifactError, "exceeds transported artifact limit") {
		t.Fatalf("expected artifact error to mention transport limit, got %q", body.ToolResult.ArtifactError)
	}
}

func TestExecutorUploadsOversizedExportedFilesWhenUploaderIsConfigured(t *testing.T) {
	workDir := t.TempDir()
	largePath := filepath.Join(workDir, "large.bin")
	if err := os.WriteFile(largePath, []byte(strings.Repeat("x", tools.MaxTransportedArtifactBytes+1)), 0o644); err != nil {
		t.Fatalf("write large export file: %v", err)
	}
	uploader := &recordingArtifactUploader{
		ref: tools.ArtifactRef{
			ArtifactID:   "art_large",
			ObjectRefID:  "obj_large",
			Name:         "large.bin",
			ArtifactType: "file",
			DownloadPath: "/v1/sessions/sesn_000001/artifacts/art_large/download",
		},
	}
	executor := DefaultExecutor("test-worker")
	executor.ArtifactUploader = uploader

	result := executor.Execute(t.Context(), managedagents.WorkerWork{
		ID:            "work_000004",
		WorkspaceID:   "wksp_default",
		SessionID:     "sesn_000001",
		EnvironmentID: "env_000001",
		TurnID:        "turn_000001",
		WorkType:      managedagents.WorkerWorkTypeToolExecution,
		Payload:       json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","capabilities":["exec"],"risk":"exec","runtime":"local_system","input":{"command":"sh","args":["-c","printf ok"],"work_dir":` + `"` + workDir + `"` + `,"output_paths":["large.bin"]}}`),
	})
	if !result.Success {
		t.Fatalf("expected command success, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		ToolResult struct {
			ExportedFiles []tools.ArtifactExport `json:"exported_files"`
			Artifacts     []tools.ArtifactRef    `json:"artifacts"`
			ArtifactError string                 `json:"artifact_error"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(body.ToolResult.ExportedFiles) != 0 {
		t.Fatalf("expected uploaded export not to include inline content, got %#v", body.ToolResult.ExportedFiles)
	}
	if len(body.ToolResult.Artifacts) != 1 || body.ToolResult.Artifacts[0].ArtifactID != "art_large" {
		t.Fatalf("expected uploaded artifact ref, got %#v", body.ToolResult.Artifacts)
	}
	if body.ToolResult.ArtifactError != "" {
		t.Fatalf("expected no artifact error, got %q", body.ToolResult.ArtifactError)
	}
	if len(uploader.uploads) != 1 || uploader.uploads[0].SessionID != "sesn_000001" || uploader.uploads[0].TurnID != "turn_000001" || uploader.uploads[0].ToolCallID != "work_000004" {
		t.Fatalf("unexpected upload input: %#v", uploader.uploads)
	}
	if len(uploader.uploads[0].Content) != tools.MaxTransportedArtifactBytes+1 {
		t.Fatalf("unexpected uploaded content size: %d", len(uploader.uploads[0].Content))
	}
}

func TestExecutorUploadsImageExportedFilesWhenUploaderIsConfigured(t *testing.T) {
	workDir := t.TempDir()
	uploader := &recordingArtifactUploader{
		ref: tools.ArtifactRef{
			ArtifactID:   "art_image",
			ObjectRefID:  "obj_image",
			Name:         "screen.png",
			ArtifactType: "asset",
			DownloadPath: "/v1/sessions/sesn_000001/artifacts/art_image/download",
		},
	}
	executor := DefaultExecutor("test-worker")
	executor.ArtifactUploader = uploader

	result := executor.Execute(t.Context(), managedagents.WorkerWork{
		ID:            "work_000005",
		WorkspaceID:   "wksp_default",
		SessionID:     "sesn_000001",
		EnvironmentID: "env_000001",
		TurnID:        "turn_000001",
		WorkType:      managedagents.WorkerWorkTypeToolExecution,
		Payload:       json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","capabilities":["exec"],"risk":"exec","runtime":"local_system","input":{"command":"sh","args":["-c","printf '\\211PNG\\r\\n\\032\\n' > screen.png && printf ok"],"work_dir":` + `"` + workDir + `"` + `,"output_paths":["screen.png"]}}`),
	})
	if !result.Success {
		t.Fatalf("expected command success, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		ToolResult struct {
			ExportedFiles []tools.ArtifactExport `json:"exported_files"`
			Artifacts     []tools.ArtifactRef    `json:"artifacts"`
			ArtifactError string                 `json:"artifact_error"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(body.ToolResult.ExportedFiles) != 0 {
		t.Fatalf("expected image export not to include inline content, got %#v", body.ToolResult.ExportedFiles)
	}
	if len(body.ToolResult.Artifacts) != 1 || body.ToolResult.Artifacts[0].ArtifactID != "art_image" {
		t.Fatalf("expected uploaded image artifact ref, got %#v", body.ToolResult.Artifacts)
	}
	if body.ToolResult.ArtifactError != "" {
		t.Fatalf("expected no artifact error, got %q", body.ToolResult.ArtifactError)
	}
	if len(uploader.uploads) != 1 || uploader.uploads[0].ContentType != "image/png" || string(uploader.uploads[0].Content) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("unexpected upload input: %#v", uploader.uploads)
	}
}

func TestExecutorUsesCustomWorkHandler(t *testing.T) {
	executor := Executor{
		WorkerName: "custom-worker",
		Handlers: map[string]WorkHandler{
			"artifact_sync": WorkHandlerFunc(func(_ context.Context, executor Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
				result, _ := json.Marshal(map[string]any{
					"status":      "handled",
					"worker_name": executor.WorkerName,
					"work_id":     work.ID,
				})
				return managedagents.CompleteWorkerWorkInput{Success: true, Result: result}
			}),
		},
	}

	result := executor.Execute(t.Context(), managedagents.WorkerWork{
		ID:       "work_artifact",
		WorkType: managedagents.WorkerWorkTypeArtifactSync,
	})
	if !result.Success {
		t.Fatalf("expected custom handler success, got %q", result.ErrorMessage)
	}
	var body map[string]string
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if body["status"] != "handled" || body["worker_name"] != "custom-worker" || body["work_id"] != "work_artifact" {
		t.Fatalf("unexpected custom handler body: %#v", body)
	}
}

type recordingArtifactUploader struct {
	uploads []ArtifactUpload
	ref     tools.ArtifactRef
}

func (u *recordingArtifactUploader) UploadArtifact(_ context.Context, input ArtifactUpload) (tools.ArtifactRef, error) {
	u.uploads = append(u.uploads, input)
	return u.ref, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func writeExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}
