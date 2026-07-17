package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryValidateCallArgumentsEnforcesSchemaWithoutValues(t *testing.T) {
	runtime := &argumentSchemaTestRuntime{}
	registry := NewRegistry(runtime)

	tests := []struct {
		name      string
		arguments string
		wantPath  string
	}{
		{name: "required", arguments: `{"mode":"safe"}`, wantPath: "/required"},
		{name: "enum", arguments: `{"query":"sensitive-query-value","mode":"unsafe"}`, wantPath: "/properties/mode/enum"},
		{name: "additional property", arguments: `{"query":"ok","mode":"safe","secret":"sensitive-extra-value"}`, wantPath: "/additionalProperties"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validationError := registry.ValidateCallArguments(Call{Identifier: "schema_test", APIName: "search", Arguments: json.RawMessage(test.arguments)})
			if validationError == nil || validationError.Type != "invalid_tool_arguments" || !strings.Contains(validationError.Message, test.wantPath) {
				t.Fatalf("unexpected validation error: %#v", validationError)
			}
			if strings.Contains(validationError.Message, "sensitive-query-value") || strings.Contains(validationError.Message, "sensitive-extra-value") || strings.Contains(validationError.Message, "unsafe") {
				t.Fatalf("validation error leaked argument value: %s", validationError.Message)
			}
		})
	}
}

func TestRegistryExecutorRejectsSchemaMismatchBeforeRuntime(t *testing.T) {
	runtime := &argumentSchemaTestRuntime{}
	executor := RegistryExecutor{Registry: NewRegistry(runtime)}

	result, err := executor.Execute(t.Context(), Call{
		ID: "call_invalid", Identifier: "schema_test", APIName: "search",
		Arguments: json.RawMessage(`{"query":"ok","mode":"wrong"}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute invalid call: %v", err)
	}
	if result.Error == nil || result.Error.Type != "invalid_tool_arguments" || runtime.calls != 0 {
		t.Fatalf("schema mismatch reached runtime: result=%#v calls=%d", result, runtime.calls)
	}

	result, err = executor.Execute(t.Context(), Call{
		ID: "call_valid", Identifier: "schema_test", APIName: "search",
		Arguments: json.RawMessage(`{"query":"ok","mode":"safe"}`),
	}, ExecutionContext{})
	if err != nil || result.Error != nil || runtime.calls != 1 {
		t.Fatalf("valid call did not execute once: result=%#v err=%v calls=%d", result, err, runtime.calls)
	}
}

func TestDefaultRegistryToolSchemasCompile(t *testing.T) {
	registry := DefaultRegistry()
	for _, manifest := range registry.Manifests() {
		for _, api := range manifest.API {
			_, err := CompileJSONSchema(api.Parameters)
			if err != nil {
				t.Errorf("compile %s.%s parameters: %v", manifest.Identifier, api.Name, err)
			}
		}
	}
}

func TestRegistryRejectsInvalidOrExternalToolSchema(t *testing.T) {
	tests := []struct {
		name       string
		parameters string
	}{
		{name: "invalid JSON", parameters: `{"type":"object"`},
		{name: "external ref", parameters: `{"type":"object","properties":{"value":{"$ref":"https://example.com/value.json"}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &argumentSchemaTestRuntime{parameters: json.RawMessage(test.parameters)}
			registry := NewRegistry(runtime)
			validationError := registry.ValidateCallArguments(Call{Identifier: "schema_test", APIName: "search", Arguments: json.RawMessage(`{"query":"ok","mode":"safe"}`)})
			if validationError == nil || validationError.Type != "invalid_tool_schema" {
				t.Fatalf("expected invalid schema failure, got %#v", validationError)
			}
			result, err := (RegistryExecutor{Registry: registry}).Execute(t.Context(), Call{Identifier: "schema_test", APIName: "search", Arguments: json.RawMessage(`{"query":"ok","mode":"safe"}`)}, ExecutionContext{})
			if err != nil || result.Error == nil || result.Error.Type != "invalid_tool_schema" || runtime.calls != 0 {
				t.Fatalf("invalid schema reached runtime: result=%#v err=%v calls=%d", result, err, runtime.calls)
			}
		})
	}
}

type argumentSchemaTestRuntime struct {
	calls      int
	parameters json.RawMessage
}

func (runtime *argumentSchemaTestRuntime) Manifest() Manifest {
	parameters := runtime.parameters
	if len(parameters) == 0 {
		parameters = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string","minLength":1},"mode":{"type":"string","enum":["safe"]}},"required":["query","mode"]}`)
	}
	return Manifest{
		Identifier: "schema_test", Type: "builtin", Executors: []string{ExecutorServer},
		API: []API{{
			Name: "search", Description: "Search a deterministic fixture.", Risk: ToolRiskRead,
			Parameters: parameters,
		}},
	}
}

func (runtime *argumentSchemaTestRuntime) Execute(context.Context, Call, ExecutionContext) (ExecutionResult, error) {
	runtime.calls++
	return ExecutionResult{Identifier: "schema_test", APIName: "search", Content: "ok"}, nil
}
