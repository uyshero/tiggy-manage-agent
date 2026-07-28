package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolCatalogInspectReturnsFullVisibleToolDetails(t *testing.T) {
	registry := DefaultRegistry()
	result, err := (RegistryExecutor{Registry: registry}).Execute(t.Context(), Call{
		Name:      ToolCatalogIdentifier + "_" + ToolCatalogAPIInspect,
		Arguments: json.RawMessage(`{"function_name":"default_edit_file"}`),
	}, ExecutionContext{ToolRegistry: registry})
	if err != nil {
		t.Fatalf("inspect tool: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected inspect error: %#v", result.Error)
	}

	var response ToolCatalogInspectResponse
	if err := json.Unmarshal(result.State, &response); err != nil {
		t.Fatalf("decode inspect state: %v", err)
	}
	if response.ProtocolVersion != ManifestProtocolVersion || response.FunctionName != "default_edit_file" || response.Identifier != DefaultIdentifier || response.APIName != "edit_file" {
		t.Fatalf("unexpected inspect response: %#v", response)
	}
	if !strings.Contains(response.Manifest.SystemRole, "Use default_* tools") {
		t.Fatalf("expected full manifest system role, got %q", response.Manifest.SystemRole)
	}
	if !json.Valid(response.Parameters) || !strings.Contains(string(response.Parameters), `"old_string"`) {
		t.Fatalf("expected full API parameters, got %s", response.Parameters)
	}
}

func TestToolCatalogInspectRejectsHiddenDefaultTools(t *testing.T) {
	registry := DefaultRegistry()
	result, err := (RegistryExecutor{Registry: registry}).Execute(t.Context(), Call{
		Name:      ToolCatalogIdentifier + "_" + ToolCatalogAPIInspect,
		Arguments: json.RawMessage(`{"function_name":"default_execute_code"}`),
	}, ExecutionContext{ToolRegistry: registry})
	if err != nil {
		t.Fatalf("inspect hidden tool: %v", err)
	}
	if result.Error == nil || result.Error.Type != "tool_not_visible" {
		t.Fatalf("expected hidden tool rejection, got %#v", result)
	}
}

func TestToolCatalogInspectHonorsCurrentRegistryAvailability(t *testing.T) {
	registry := DefaultRegistry().Available(AvailableCapabilities{
		Runtime:      ToolRuntimeLocalSystem,
		Capabilities: []string{CapabilityFilesystemRead},
	})
	result, err := (RegistryExecutor{Registry: registry}).Execute(t.Context(), Call{
		Name:      ToolCatalogIdentifier + "_" + ToolCatalogAPIInspect,
		Arguments: json.RawMessage(`{"function_name":"default_run_command"}`),
	}, ExecutionContext{ToolRegistry: registry})
	if err != nil {
		t.Fatalf("inspect unavailable tool: %v", err)
	}
	if result.Error == nil || result.Error.Type != "tool_not_found" {
		t.Fatalf("expected unavailable tool rejection, got %#v", result)
	}
}
