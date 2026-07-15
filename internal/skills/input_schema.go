package skills

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
	MaxInputsSchemaBytes      = 32 * 1024
	MaxSkillInputsBytes       = 16 * 1024
	MaxInputsSchemaDepth      = 8
	MaxInputsSchemaNodes      = 512
	MaxInputsSchemaProperties = 64
	MaxSkillInputsDepth       = 8
	MaxSkillInputNodes        = 512
)

const inputsSchemaResourceURL = "https://tma.local/schemas/skill-inputs.json"

var compiledInputsSchemas sync.Map

// ValidateInputsSchema accepts an offline-only Draft 2020-12 object schema.
func ValidateInputsSchema(raw json.RawMessage) error {
	if isEmptyJSON(raw) {
		return nil
	}
	_, err := compileInputsSchema(raw)
	return err
}

// ValidateVersionInputs validates one binding's inputs against its immutable version manifest.
func ValidateVersionInputs(version Version, raw json.RawMessage) error {
	var manifest Manifest
	if !isEmptyJSON(version.Manifest) {
		if err := json.Unmarshal(version.Manifest, &manifest); err != nil {
			return fmt.Errorf("decode skill manifest: %w", err)
		}
	}
	return ValidateInputs(manifest.InputsSchema, raw)
}

// ValidateInputs preserves legacy object-only behavior when a version has no inputs_schema.
func ValidateInputs(schemaRaw json.RawMessage, inputsRaw json.RawMessage) error {
	instanceRaw := inputsRaw
	if isEmptyJSON(instanceRaw) {
		instanceRaw = json.RawMessage(`{}`)
	}
	if len(instanceRaw) > MaxSkillInputsBytes {
		return fmt.Errorf("skill inputs must not exceed %d bytes", MaxSkillInputsBytes)
	}
	instance, err := decodeJSONValue(instanceRaw)
	if err != nil {
		return fmt.Errorf("skill inputs must be valid JSON: %w", err)
	}
	if _, ok := instance.(map[string]any); !ok {
		return fmt.Errorf("skill inputs must be an object")
	}
	limits := jsonStructureLimits{maxDepth: MaxSkillInputsDepth, maxNodes: MaxSkillInputNodes}
	if err := inspectJSONStructure(instance, 1, &limits); err != nil {
		return fmt.Errorf("skill inputs exceed structural limits: %w", err)
	}
	if isEmptyJSON(schemaRaw) {
		return nil
	}
	compiled, err := compileInputsSchema(schemaRaw)
	if err != nil {
		return err
	}
	if err := compiled.Validate(instance); err != nil {
		return sanitizedInputsValidationError(err)
	}
	return nil
}

func compileInputsSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) > MaxInputsSchemaBytes {
		return nil, fmt.Errorf("inputs_schema must not exceed %d bytes", MaxInputsSchemaBytes)
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return nil, fmt.Errorf("inputs_schema must be valid JSON: %w", err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("inputs_schema must be an object")
	}
	if root["type"] != "object" {
		return nil, fmt.Errorf("inputs_schema root type must be object")
	}
	limits := jsonStructureLimits{
		maxDepth: MaxInputsSchemaDepth, maxNodes: MaxInputsSchemaNodes,
		maxProperties: MaxInputsSchemaProperties, schema: true,
	}
	if err := inspectJSONStructure(root, 1, &limits); err != nil {
		return nil, fmt.Errorf("inputs_schema exceeds structural limits: %w", err)
	}
	digest := sha256.Sum256(raw)
	cacheKey := hex.EncodeToString(digest[:])
	if cached, ok := compiledInputsSchemas.Load(cacheKey); ok {
		return cached.(*jsonschema.Schema), nil
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	compiler.AssertFormat = true
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("external schema reference %q is disabled", url)
	}
	if err := compiler.AddResource(inputsSchemaResourceURL, bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("compile inputs_schema: %w", err)
	}
	compiled, err := compiler.Compile(inputsSchemaResourceURL)
	if err != nil {
		return nil, fmt.Errorf("compile inputs_schema: %w", err)
	}
	actual, _ := compiledInputsSchemas.LoadOrStore(cacheKey, compiled)
	return actual.(*jsonschema.Schema), nil
}

type jsonStructureLimits struct {
	maxDepth      int
	maxNodes      int
	maxProperties int
	nodes         int
	properties    int
	schema        bool
}

func inspectJSONStructure(value any, depth int, limits *jsonStructureLimits) error {
	if depth > limits.maxDepth {
		return fmt.Errorf("maximum depth is %d", limits.maxDepth)
	}
	limits.nodes++
	if limits.nodes > limits.maxNodes {
		return fmt.Errorf("maximum node count is %d", limits.maxNodes)
	}
	switch typed := value.(type) {
	case map[string]any:
		if limits.schema {
			if err := validateOfflineSchemaObject(typed, limits); err != nil {
				return err
			}
		}
		for _, child := range typed {
			if err := inspectJSONStructure(child, depth+1, limits); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := inspectJSONStructure(child, depth+1, limits); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOfflineSchemaObject(object map[string]any, limits *jsonStructureLimits) error {
	if schemaURI, ok := object["$schema"].(string); ok && schemaURI != "https://json-schema.org/draft/2020-12/schema" && schemaURI != "https://json-schema.org/draft/2020-12/schema#" {
		return fmt.Errorf("only JSON Schema Draft 2020-12 is supported")
	}
	if _, ok := object["$id"]; ok {
		return fmt.Errorf("inputs_schema must not declare $id")
	}
	if _, ok := object["$dynamicRef"]; ok {
		return fmt.Errorf("inputs_schema must not declare $dynamicRef")
	}
	if ref, ok := object["$ref"]; ok {
		value, stringOK := ref.(string)
		if !stringOK || !strings.HasPrefix(value, "#") {
			return fmt.Errorf("inputs_schema $ref must be a local fragment")
		}
	}
	if value, _ := object["writeOnly"].(bool); value {
		return fmt.Errorf("secret inputs are not supported; use managed environment variables")
	}
	if value, _ := object["x-tma-sensitive"].(bool); value {
		return fmt.Errorf("secret inputs are not supported; use managed environment variables")
	}
	if format, _ := object["format"].(string); strings.EqualFold(format, "password") {
		return fmt.Errorf("secret inputs are not supported; use managed environment variables")
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		limits.properties += len(properties)
		if limits.properties > limits.maxProperties {
			return fmt.Errorf("maximum property count is %d", limits.maxProperties)
		}
	}
	if isObjectSchema(object) {
		if additional, ok := object["additionalProperties"]; !ok || additional != false {
			return fmt.Errorf("object schemas must set additionalProperties to false")
		}
	}
	return nil
}

func isObjectSchema(schema map[string]any) bool {
	switch value := schema["type"].(type) {
	case string:
		return value == "object"
	case []any:
		for _, item := range value {
			if item == "object" {
				return true
			}
		}
	}
	_, hasProperties := schema["properties"]
	return hasProperties
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func sanitizedInputsValidationError(err error) error {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return fmt.Errorf("skill inputs do not match inputs_schema")
	}
	leaf := validationErr
	for len(leaf.Causes) > 0 {
		leaf = leaf.Causes[0]
	}
	instancePath := leaf.InstanceLocation
	if instancePath == "" {
		instancePath = "/"
	}
	keyword := strings.TrimPrefix(leaf.KeywordLocation, "/")
	if index := strings.LastIndex(keyword, "/"); index >= 0 {
		keyword = keyword[index+1:]
	}
	if keyword == "" {
		keyword = "schema"
	}
	return fmt.Errorf("skill inputs do not match inputs_schema at %s (%s)", instancePath, keyword)
}

func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}
