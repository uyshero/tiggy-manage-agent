package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

const (
	toolArgumentSchemaResourceURL = "https://tma.local/schemas/tool-arguments.json"
	maxToolArgumentSchemaBytes    = 256 * 1024
	maxToolArgumentsBytes         = 1 * 1024 * 1024
)

var compiledToolArgumentSchemas sync.Map

// ValidateCallArguments enforces the registered API schema before a tool call
// reaches policy evaluation or an executor. Validation errors describe schema
// locations without including argument values.
func (r Registry) ValidateCallArguments(call Call) *ExecutionError {
	call = NormalizeCall(call)
	_, api, ok := r.GetAPI(call.Identifier, call.APIName)
	if !ok {
		return &ExecutionError{Type: "unsupported_tool_api", Message: fmt.Sprintf("unsupported tool api %q", call.Identifier+"."+call.APIName)}
	}

	instance, err := decodeToolArguments(call.Arguments)
	if err != nil {
		return &ExecutionError{Type: "invalid_tool_arguments", Message: err.Error()}
	}
	schema, err := CompileJSONSchema(api.Parameters)
	if err != nil {
		return &ExecutionError{Type: "invalid_tool_schema", Message: fmt.Sprintf("tool argument schema is invalid for %s.%s: %v", call.Identifier, call.APIName, err)}
	}
	if err := schema.Validate(instance); err != nil {
		return &ExecutionError{Type: "invalid_tool_arguments", Message: schemaValidationMessage(err, "tool arguments")}
	}
	return nil
}

// CompileJSONSchema compiles an offline-only Draft 2020-12 schema and caches
// it by content. Callers use this to reject invalid persisted schemas before
// creating work that depends on them.
func CompileJSONSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	return compileToolArgumentSchema(raw)
}

// ValidateJSONSchemaInstance validates any JSON value against an offline-only
// Draft 2020-12 schema. Returned errors contain schema paths, not instance
// values.
func ValidateJSONSchemaInstance(schemaRaw, instanceRaw json.RawMessage) *ExecutionError {
	instance, err := decodeJSONInstance(instanceRaw)
	if err != nil {
		return &ExecutionError{Type: "invalid_json_instance", Message: err.Error()}
	}
	schema, err := CompileJSONSchema(schemaRaw)
	if err != nil {
		return &ExecutionError{Type: "invalid_json_schema", Message: fmt.Sprintf("JSON Schema is invalid: %v", err)}
	}
	if err := schema.Validate(instance); err != nil {
		return &ExecutionError{Type: "json_schema_validation_failed", Message: schemaValidationMessage(err, "JSON instance")}
	}
	return nil
}

func decodeToolArguments(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > maxToolArgumentsBytes {
		return nil, fmt.Errorf("tool arguments exceed %d bytes", maxToolArgumentsBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("tool arguments must be a valid JSON object")
	}
	if value == nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("tool arguments must contain exactly one JSON object")
	}
	return value, nil
}

func decodeJSONInstance(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("JSON instance is empty")
	}
	if len(raw) > maxToolArgumentsBytes {
		return nil, fmt.Errorf("JSON instance exceeds %d bytes", maxToolArgumentsBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("JSON instance must be valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("JSON instance must contain exactly one JSON value")
	}
	return value, nil
}

func compileToolArgumentSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if len(raw) > maxToolArgumentSchemaBytes {
		return nil, fmt.Errorf("schema exceeds %d bytes", maxToolArgumentSchemaBytes)
	}
	digest := sha256.Sum256(raw)
	cacheKey := hex.EncodeToString(digest[:])
	if cached, ok := compiledToolArgumentSchemas.Load(cacheKey); ok {
		return cached.(*jsonschema.Schema), nil
	}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	compiler.AssertFormat = true
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("external schema reference %q is disabled", url)
	}
	if err := compiler.AddResource(toolArgumentSchemaResourceURL, bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	compiled, err := compiler.Compile(toolArgumentSchemaResourceURL)
	if err != nil {
		return nil, err
	}
	actual, _ := compiledToolArgumentSchemas.LoadOrStore(cacheKey, compiled)
	return actual.(*jsonschema.Schema), nil
}

func schemaValidationMessage(err error, subject string) string {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return subject + " does not match the registered schema"
	}
	leaf := validationErr
	for len(leaf.Causes) > 0 {
		leaf = leaf.Causes[0]
	}
	instanceLocation := defaultSchemaLocation(leaf.InstanceLocation)
	keywordLocation := defaultSchemaLocation(leaf.KeywordLocation)
	detail := ""
	if strings.HasSuffix(keywordLocation, "/required") && strings.HasPrefix(leaf.Message, "missing properties:") {
		// Required-property names come from the registered schema, not from the
		// submitted values, so they are safe and make retries actionable.
		detail = ": " + leaf.Message
	}
	return fmt.Sprintf("%s does not match schema at instance %s (constraint %s)%s", subject, instanceLocation, keywordLocation, detail)
}

func defaultSchemaLocation(value string) string {
	if strings.TrimSpace(value) == "" {
		return "/"
	}
	return value
}
