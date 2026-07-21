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
			if test.name == "required" && !strings.Contains(validationError.Message, "missing properties: 'query'") {
				t.Fatalf("required-property error is not actionable: %s", validationError.Message)
			}
		})
	}
}

func TestEditFileSchemaRequiresPathAndCompleteReplacement(t *testing.T) {
	registry := DefaultRegistry()
	for _, test := range []struct {
		name      string
		arguments string
		valid     bool
		want      string
	}{
		{name: "canonical", arguments: `{"path":"note.txt","old_string":"old","new_string":"new"}`, valid: true},
		{name: "noncanonical path field is rejected", arguments: `{"file_path":"note.txt","old_string":"old","new_string":"new"}`, want: "missing properties: 'path'"},
		{name: "missing replacement fields", arguments: `{"path":"note.txt"}`, want: "missing properties: 'old_string', 'new_string'"},
		{name: "missing path", arguments: `{"old_string":"old","new_string":"new"}`, want: "missing properties: 'path'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			validationError := registry.ValidateCallArguments(Call{Identifier: DefaultIdentifier, APIName: "edit_file", Arguments: json.RawMessage(test.arguments)})
			if test.valid {
				if validationError != nil {
					t.Fatalf("valid edit_file arguments rejected: %#v", validationError)
				}
				return
			}
			if validationError == nil || !strings.Contains(validationError.Message, test.want) {
				t.Fatalf("unexpected edit_file validation error: %#v", validationError)
			}
		})
	}
}

func TestEditFileModelSchemaExposesOnlyCanonicalFields(t *testing.T) {
	_, api, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "edit_file")
	if !ok {
		t.Fatal("expected edit_file API")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(api.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"path", "old_string", "new_string", "replace_all"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("edit_file schema is missing %s", field)
		}
	}
	if len(schema.Properties) != 4 {
		t.Fatalf("edit_file model schema must expose exactly four fields: %#v", schema.Properties)
	}
	for _, hidden := range []string{"work_dir", "expected_revision", "expected_match_count", "file_path"} {
		if _, ok := schema.Properties[hidden]; ok {
			t.Fatalf("internal field %s leaked into the model schema", hidden)
		}
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
