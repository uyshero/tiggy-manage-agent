package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestModelToolNameUsesProviderSafeCanonicalFormat(t *testing.T) {
	t.Parallel()

	if got := ModelToolName("default", "run_command"); got != "default_run_command" {
		t.Fatalf("ModelToolName() = %q", got)
	}
	if got := ModelToolName("vendor.files", "read-file"); got != "vendor_files_read_file" {
		t.Fatalf("ModelToolName() sanitized = %q", got)
	}
	for _, char := range ModelToolName("vendor.files", "read-file") {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_') {
			t.Fatalf("model tool name contains unsafe character %q", char)
		}
	}
}

func TestRegistryResolveCallPreservesUnderscoresInNamespaceAndAPI(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(toolNameRuntime{identifier: "team_files", apiName: "read_file"})
	call := registry.ResolveCall(Call{Name: "team_files_read_file", Arguments: json.RawMessage(`{}`)})
	if call.Identifier != "team_files" || call.APIName != "read_file" {
		t.Fatalf("ResolveCall() = %+v", call)
	}
}

func TestRegistryRejectsCollidingCanonicalModelToolNames(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(
		toolNameRuntime{identifier: "team_files", apiName: "read"},
		toolNameRuntime{identifier: "team", apiName: "files_read"},
	)
	_, _, err := registry.Snapshot()
	if err == nil || !strings.Contains(err.Error(), "map to the same model name") {
		t.Fatalf("Snapshot() error = %v", err)
	}
}

type toolNameRuntime struct {
	identifier string
	apiName    string
}

func (runtime toolNameRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: runtime.identifier,
		Meta:       Meta{Title: runtime.identifier},
		API: []API{{
			Name: runtime.apiName, Description: "Test canonical model tool names.", Risk: ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		}},
	}
}

func (toolNameRuntime) Execute(context.Context, Call, ExecutionContext) (ExecutionResult, error) {
	return ExecutionResult{Content: "ok"}, nil
}
