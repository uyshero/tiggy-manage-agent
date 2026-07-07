package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/capability"
)

func TestDefaultRegistryIncludesLocalSystemManifest(t *testing.T) {
	registry := DefaultRegistry()

	runtime, ok := registry.Get(LocalSystemIdentifier)
	if !ok {
		t.Fatalf("expected %s runtime", LocalSystemIdentifier)
	}
	manifest := runtime.Manifest()
	if manifest.Identifier != LocalSystemIdentifier || len(manifest.API) != 5 {
		t.Fatalf("unexpected local system manifest: %#v", manifest)
	}
}

func TestRegistryModelContextIncludesManifestAndCallFormat(t *testing.T) {
	context := DefaultRegistry().ModelContext()
	if len(context) == 0 {
		t.Fatal("expected model context")
	}

	var decoded struct {
		ProtocolVersion string `json:"protocol_version"`
		ToolCallFormat  struct {
			ProtocolVersion string `json:"protocol_version"`
		} `json:"tool_call_format"`
		Tools []Manifest `json:"tools"`
	}
	if err := json.Unmarshal(context, &decoded); err != nil {
		t.Fatalf("decode model context: %v", err)
	}
	if decoded.ProtocolVersion != ManifestProtocolVersion {
		t.Fatalf("unexpected protocol version: %#v", decoded)
	}
	if decoded.ToolCallFormat.ProtocolVersion != ToolCallProtocolVersion {
		t.Fatalf("unexpected tool call format: %#v", decoded.ToolCallFormat)
	}
	if len(decoded.Tools) != 1 || decoded.Tools[0].Identifier != LocalSystemIdentifier {
		t.Fatalf("unexpected tools: %#v", decoded.Tools)
	}
}

func TestRegistryModelToolsUsesQualifiedFunctionNames(t *testing.T) {
	modelTools := DefaultRegistry().ModelTools()
	if len(modelTools) != 5 {
		t.Fatalf("expected local system APIs as model tools, got %#v", modelTools)
	}

	names := make(map[string]bool)
	for _, modelTool := range modelTools {
		names[modelTool.Function.Name] = true
		if modelTool.Type != "function" {
			t.Fatalf("expected function tool, got %#v", modelTool)
		}
		if len(modelTool.Function.Parameters) == 0 {
			t.Fatalf("expected parameters for %s", modelTool.Function.Name)
		}
	}
	if !names[LocalSystemIdentifier+".run_command"] || !names[LocalSystemIdentifier+".edit_file"] {
		t.Fatalf("missing expected qualified names: %#v", names)
	}
}

func TestParseInterventionModeDefaultsAndNormalizes(t *testing.T) {
	if got := ParseInterventionMode(nil); got != InterventionModeRequestApproval {
		t.Fatalf("expected default intervention mode, got %q", got)
	}
	if got := ParseInterventionMode(json.RawMessage(`{"intervention_mode":"APPROVE_FOR_ME"}`)); got != InterventionModeApproveForMe {
		t.Fatalf("expected normalized approve_for_me, got %q", got)
	}
	if got := ParseInterventionMode(json.RawMessage(`{"intervention_mode":"wat"}`)); got != InterventionModeRequestApproval {
		t.Fatalf("expected invalid value to fall back to default, got %q", got)
	}
}

func TestRegistryGetAPIReturnsManifestMetadata(t *testing.T) {
	manifest, api, ok := DefaultRegistry().GetAPI(LocalSystemIdentifier, "edit_file")
	if !ok {
		t.Fatal("expected edit_file api")
	}
	if manifest.Identifier != LocalSystemIdentifier || api.Name != "edit_file" || api.HumanIntervention != "optional" {
		t.Fatalf("unexpected api lookup result: manifest=%#v api=%#v", manifest, api)
	}
}

func TestRegistryExecutorRunsLocalSystemCommand(t *testing.T) {
	executor := NewDefaultExecutor()

	result, err := executor.Execute(context.Background(), Call{
		ID:   "call_1",
		Name: "run_command",
		Arguments: json.RawMessage(`{
			"command": "sh",
			"args": ["-c", "printf tool-output"]
		}`),
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if result.Identifier != LocalSystemIdentifier || result.APIName != "run_command" || result.Content != "tool-output" {
		t.Fatalf("unexpected result: %#v", result)
	}

	var state capability.CommandResult
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Stdout != "tool-output" || state.ExitCode != 0 {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestNormalizeCallSplitsLocalSystemFunctionName(t *testing.T) {
	call := NormalizeCall(Call{
		APIName: "tma.local_system.run_command",
	})

	if call.Identifier != LocalSystemIdentifier || call.APIName != "run_command" {
		t.Fatalf("unexpected normalized call: %#v", call)
	}
}

func TestRegistryExecutorRunsLocalSystemEditFile(t *testing.T) {
	executor := NewDefaultExecutor()
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	arguments, err := json.Marshal(map[string]any{
		"file_path":  path,
		"old_string": "world",
		"new_string": "gopher",
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	result, err := executor.Execute(context.Background(), Call{
		ID:        "call_edit",
		Name:      "edit_file",
		Arguments: arguments,
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error result: %#v", result.Error)
	}
	if result.APIName != "edit_file" || !strings.Contains(result.Content, "1 replacement") {
		t.Fatalf("unexpected result: %#v", result)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello gopher" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestRegistryExecutorReturnsStableUnsupportedToolError(t *testing.T) {
	executor := NewDefaultExecutor()

	result, err := executor.Execute(context.Background(), Call{
		ID:         "call_2",
		Identifier: "tma.missing",
		APIName:    "run_command",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("expected stable tool result, got error: %v", err)
	}
	if result.Error == nil || result.Error.Type != "unsupported_tool" {
		t.Fatalf("expected unsupported tool error result, got %#v", result)
	}
}
