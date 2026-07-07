package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
)

const (
	ExecutorServer                = "server"
	ManifestProtocolVersion       = "tma.tools.manifest.v1"
	ToolCallProtocolVersion       = "tma.tool_call.v1"
	LegacyToolCallProtocolVersion = "tma.agent_runtime.demo.v1"
)

type Manifest struct {
	Identifier        string   `json:"identifier"`
	Type              string   `json:"type,omitempty"`
	Meta              Meta     `json:"meta"`
	SystemRole        string   `json:"system_role"`
	API               []API    `json:"api"`
	Executors         []string `json:"executors,omitempty"`
	HumanIntervention string   `json:"human_intervention,omitempty"`
}

type Meta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type API struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Parameters        json.RawMessage `json:"parameters,omitempty"`
	HumanIntervention string          `json:"human_intervention,omitempty"`
}

type Call struct {
	ID         string          `json:"id,omitempty"`
	Identifier string          `json:"identifier,omitempty"`
	APIName    string          `json:"api_name,omitempty"`
	Name       string          `json:"name,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
}

type ExecutionContext struct {
	SessionID string
	TurnID    string
	Deadline  *time.Time
	Provider  capability.Provider
}

type ExecutionResult struct {
	ID         string          `json:"id,omitempty"`
	Identifier string          `json:"identifier"`
	APIName    string          `json:"api_name"`
	Content    string          `json:"content"`
	State      json.RawMessage `json:"state,omitempty"`
	Error      *ExecutionError `json:"error,omitempty"`
}

type ExecutionError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type Runtime interface {
	Manifest() Manifest
	Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error)
}

type Executor interface {
	Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error)
}

type Registry struct {
	runtimes map[string]Runtime
}

func NewRegistry(runtimes ...Runtime) Registry {
	registry := Registry{runtimes: make(map[string]Runtime)}
	for _, runtime := range runtimes {
		registry.Register(runtime)
	}
	return registry
}

func DefaultRegistry() Registry {
	return NewRegistry(LocalSystemRuntime{})
}

func (r Registry) Register(runtime Runtime) {
	if runtime == nil {
		return
	}
	manifest := runtime.Manifest()
	if manifest.Identifier == "" {
		return
	}
	r.runtimes[manifest.Identifier] = runtime
}

func (r Registry) Get(identifier string) (Runtime, bool) {
	runtime, ok := r.runtimes[identifier]
	return runtime, ok
}

func (r Registry) Manifests() []Manifest {
	manifests := make([]Manifest, 0, len(r.runtimes))
	for _, runtime := range r.runtimes {
		manifests = append(manifests, runtime.Manifest())
	}
	return manifests
}

func (r Registry) ModelTools() []llm.Tool {
	manifests := r.Manifests()
	if len(manifests) == 0 {
		return nil
	}
	modelTools := make([]llm.Tool, 0)
	for _, manifest := range manifests {
		for _, api := range manifest.API {
			parameters := api.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			modelTools = append(modelTools, llm.Tool{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        manifest.Identifier + "." + api.Name,
					Description: api.Description,
					Parameters:  parameters,
				},
			})
		}
	}
	return modelTools
}

func (r Registry) ModelContext() json.RawMessage {
	manifests := r.Manifests()
	if len(manifests) == 0 {
		return nil
	}
	payload := map[string]any{
		"protocol_version": ManifestProtocolVersion,
		"tool_call_format": map[string]any{
			"protocol_version": ToolCallProtocolVersion,
			"shape": map[string]any{
				"tool_calls": []map[string]any{{
					"id":   "optional stable call id",
					"type": "function",
					"function": map[string]any{
						"name":      "tool identifier plus api name, for example tma.local_system.run_command",
						"arguments": "JSON object matching the API parameters",
					},
				}},
			},
			"legacy_compatibility": "identifier/api_name/name fields are still accepted during migration",
		},
		"tools": manifests,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func PrettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if !json.Valid(raw) {
		return string(raw)
	}

	var builder bytes.Buffer
	if err := json.Indent(&builder, raw, "", "  "); err != nil {
		return string(raw)
	}
	return builder.String()
}

type RegistryExecutor struct {
	Registry Registry
}

func NewDefaultExecutor() RegistryExecutor {
	return RegistryExecutor{Registry: DefaultRegistry()}
}

func (e RegistryExecutor) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	call = NormalizeCall(call)
	registry := e.Registry
	if registry.runtimes == nil {
		registry = DefaultRegistry()
	}
	runtime, ok := registry.Get(call.Identifier)
	if !ok {
		return failedResult(call, "unsupported_tool", fmt.Sprintf("unsupported tool %q", call.Identifier)), nil
	}

	result, err := runtime.Execute(ctx, call, executionContext)
	if err != nil {
		return failedResult(call, "tool_execution_failed", err.Error()), nil
	}
	if result.Identifier == "" {
		result.Identifier = call.Identifier
	}
	if result.APIName == "" {
		result.APIName = call.APIName
	}
	if result.ID == "" {
		result.ID = call.ID
	}
	return result, nil
}

func NormalizeCall(call Call) Call {
	if call.APIName == "" {
		call.APIName = call.Name
	}
	if call.Identifier == "" && strings.Contains(call.APIName, ".") {
		call.Identifier, call.APIName = splitFunctionName(call.APIName)
	}
	if call.Identifier == "" {
		call.Identifier = LocalSystemIdentifier
	}
	return call
}

func splitFunctionName(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, LocalSystemIdentifier+"."):
		return LocalSystemIdentifier, strings.TrimPrefix(name, LocalSystemIdentifier+".")
	default:
		parts := strings.Split(name, ".")
		if len(parts) == 1 {
			return "", name
		}
		return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
	}
}

func ResultMessage(result ExecutionResult) string {
	encoded, err := json.Marshal(map[string]any{
		"id":         result.ID,
		"identifier": result.Identifier,
		"api_name":   result.APIName,
		"content":    result.Content,
		"state":      rawJSONObject(result.State),
		"error":      result.Error,
		"success":    result.Error == nil,
	})
	if err != nil {
		return `{"success":false,"error":{"type":"encode_failed","message":"encode tool result failed"}}`
	}
	return string(encoded)
}

func failedResult(call Call, errorType string, message string) ExecutionResult {
	call = NormalizeCall(call)
	return ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Error: &ExecutionError{
			Type:    errorType,
			Message: message,
		},
	}
}

func rawJSONObject(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}
