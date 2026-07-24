package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/capability"
)

const fileReferenceSchemaKeyword = "x-tma-file-ref"

var allowedFileReferenceDeclarations = map[string]bool{
	"read": true, "write": true, "read_write": true, "directory": true, "output": true,
}

// ResolveCallFileReferences centralizes FileRef URI handling for every tool
// whose schema marks file-valued strings with x-tma-file-ref.
func (r Registry) ResolveCallFileReferences(ctx context.Context, call Call, executionContext ExecutionContext) (Call, *ExecutionError) {
	call = r.ResolveCall(call)
	_, api, ok := r.GetAPI(call.Identifier, call.APIName)
	if !ok || len(api.Parameters) == 0 {
		return call, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(api.Parameters, &schema); err != nil {
		return call, &ExecutionError{Type: "invalid_tool_schema", Message: fmt.Sprintf("decode file reference schema: %v", err)}
	}
	var arguments map[string]any
	if len(call.Arguments) == 0 {
		return call, nil
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return call, nil
	}
	resolvedArguments, changed, err := resolveFileReferenceValue(ctx, schema, arguments, executionContext.Provider)
	if err != nil {
		return call, &ExecutionError{Type: "invalid_file_reference", Message: err.Error()}
	}
	if !changed {
		return call, nil
	}
	encoded, err := json.Marshal(resolvedArguments)
	if err != nil {
		return call, &ExecutionError{Type: "invalid_file_reference", Message: fmt.Sprintf("encode resolved file references: %v", err)}
	}
	call.Arguments = encoded
	return call, nil
}

func resolveFileReferenceValue(ctx context.Context, schema map[string]any, value any, provider capability.Provider) (any, bool, error) {
	if annotation, exists := schema[fileReferenceSchemaKeyword]; exists && annotation != false {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return value, false, nil
		}
		resolved, changed, err := resolveFileReferenceString(ctx, provider, text)
		if err != nil {
			return value, false, err
		}
		return resolved, changed, nil
	}
	changed := false
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		for name, childValue := range object {
			childSchema, _ := properties[name].(map[string]any)
			if childSchema == nil {
				continue
			}
			resolvedChild, childChanged, err := resolveFileReferenceValue(ctx, childSchema, childValue, provider)
			if err != nil {
				return value, false, fmt.Errorf("file argument %s: %w", name, err)
			}
			if childChanged {
				object[name] = resolvedChild
				changed = true
			}
		}
	}
	if array, ok := value.([]any); ok {
		items, _ := schema["items"].(map[string]any)
		for index, childValue := range array {
			resolvedChild, childChanged, err := resolveFileReferenceValue(ctx, items, childValue, provider)
			if err != nil {
				return value, false, fmt.Errorf("file argument item %d: %w", index, err)
			}
			if childChanged {
				array[index] = resolvedChild
				changed = true
			}
		}
	}
	return value, changed, nil
}

func resolveFileReferenceString(ctx context.Context, provider capability.Provider, value string) (string, bool, error) {
	if resolver, ok := provider.(capability.FileReferenceResolver); ok {
		resolved, err := resolver.ResolveFileReference(ctx, value)
		return resolved, resolved != value, err
	}
	resolved, recognized, err := capability.PortableFileReferencePath(value)
	if err != nil {
		return value, false, err
	}
	if recognized && strings.HasPrefix(strings.ToLower(value), capability.FileReferenceScheme+"://artifact/") {
		return value, false, fmt.Errorf("artifact file reference requires a session-aware provider")
	}
	return resolved, recognized && resolved != value, nil
}

func validateBuiltinFileReferenceDeclarations(raw json.RawMessage) error {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	return walkBuiltinFileReferenceDeclarations(schema, "$")
}

func walkBuiltinFileReferenceDeclarations(schema map[string]any, location string) error {
	if value, exists := schema[fileReferenceSchemaKeyword]; exists {
		switch typed := value.(type) {
		case bool:
			if typed {
				return fmt.Errorf("%s.%s must be false or a supported declaration string", location, fileReferenceSchemaKeyword)
			}
		case string:
			if !allowedFileReferenceDeclarations[typed] {
				return fmt.Errorf("%s.%s has unsupported value %q", location, fileReferenceSchemaKeyword, typed)
			}
		default:
			return fmt.Errorf("%s.%s must be false or a declaration string", location, fileReferenceSchemaKeyword)
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, rawChild := range properties {
		child, _ := rawChild.(map[string]any)
		if child == nil {
			continue
		}
		if fileLikeSchemaProperty(name) && !hasFileReferenceDeclaration(child) {
			return fmt.Errorf("%s.properties.%s must declare %s (use false for non-filesystem values)", location, name, fileReferenceSchemaKeyword)
		}
		if err := walkBuiltinFileReferenceDeclarations(child, location+".properties."+name); err != nil {
			return err
		}
	}
	if items, _ := schema["items"].(map[string]any); items != nil {
		if err := walkBuiltinFileReferenceDeclarations(items, location+".items"); err != nil {
			return err
		}
	}
	return nil
}

func hasFileReferenceDeclaration(schema map[string]any) bool {
	if _, exists := schema[fileReferenceSchemaKeyword]; exists {
		return true
	}
	items, _ := schema["items"].(map[string]any)
	if items == nil {
		return false
	}
	_, exists := items[fileReferenceSchemaKeyword]
	return exists
}

func fileLikeSchemaProperty(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "path" || name == "paths" || name == "root" || name == "work_dir" || name == "output_paths" || name == "mask_path"
}
