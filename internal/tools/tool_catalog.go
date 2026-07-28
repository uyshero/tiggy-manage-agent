package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const ToolCatalogIdentifier = NamespaceToolCatalog

const ToolCatalogAPIInspect = "inspect"

type ToolCatalogRuntime struct{}

type ToolCatalogInspectRequest struct {
	FunctionName string `json:"function_name,omitempty"`
	Identifier   string `json:"identifier,omitempty"`
	APIName      string `json:"api_name,omitempty"`
}

type ToolCatalogInspectResponse struct {
	ProtocolVersion string          `json:"protocol_version"`
	FunctionName    string          `json:"function_name"`
	Identifier      string          `json:"identifier"`
	APIName         string          `json:"api_name"`
	Manifest        Manifest        `json:"manifest"`
	API             API             `json:"api"`
	Parameters      json.RawMessage `json:"parameters,omitempty"`
}

func (ToolCatalogRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: ToolCatalogIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Tool Catalog",
			Description: "Inspect full details for one currently available model tool on demand.",
		},
		SystemRole:     "Use tool_catalog_inspect only when the compact tool catalog and native function schema are insufficient and you need the full tool workflow, manifest metadata, runtime policy, or long description before calling another tool.",
		Executors:      []string{ExecutorServer},
		ApprovalPolicy: ApprovalPolicyNever,
		API: []API{{
			Name:           ToolCatalogAPIInspect,
			Namespace:      NamespaceToolCatalog,
			APIName:        ToolCatalogAPIInspect,
			Description:    "Read the full manifest and API metadata for one currently available, model-visible tool. Prefer function_name such as default_edit_file.",
			Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"function_name":{"type":"string","minLength":1,"maxLength":160},"identifier":{"type":"string","maxLength":120},"api_name":{"type":"string","maxLength":120}},"anyOf":[{"required":["function_name"]},{"required":["identifier","api_name"]}]}`),
			Risk:           ToolRiskRead,
			Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
			Implementation: ToolImplementationServerBuiltin,
		}},
	}
}

func (ToolCatalogRuntime) Execute(_ context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	var request ToolCatalogInspectRequest
	if err := json.Unmarshal(call.Arguments, &request); err != nil {
		return failedResult(call, "invalid_arguments", fmt.Sprintf("decode inspect arguments: %v", err)), nil
	}
	registry := executionContext.ToolRegistry
	if len(registry.runtimes) == 0 {
		registry = DefaultRegistry()
	}
	resolved := resolveToolCatalogInspectCall(registry, request)
	manifest, api, ok := registry.GetAPI(resolved.Identifier, resolved.APIName)
	if !ok {
		return failedResult(call, "tool_not_found", "tool_catalog_inspect can only inspect a currently available tool"), nil
	}
	if api.HiddenFromModel {
		return failedResult(call, "tool_not_visible", "tool_catalog_inspect cannot inspect a tool API that is hidden from the model in the current registry"), nil
	}
	detailManifest := manifest
	detailManifest.API = []API{api}
	response := ToolCatalogInspectResponse{
		ProtocolVersion: ManifestProtocolVersion,
		FunctionName:    ModelToolName(manifest.Identifier, api.Name),
		Identifier:      manifest.Identifier,
		APIName:         api.Name,
		Manifest:        detailManifest,
		API:             api,
		Parameters:      append(json.RawMessage(nil), api.Parameters...),
	}
	state, err := json.Marshal(response)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Content:    fmt.Sprintf("Loaded tool details for %s.", response.FunctionName),
		State:      state,
	}, nil
}

func resolveToolCatalogInspectCall(registry Registry, request ToolCatalogInspectRequest) Call {
	if functionName := strings.TrimSpace(request.FunctionName); functionName != "" {
		return registry.ResolveCall(Call{Name: functionName})
	}
	return registry.ResolveCall(Call{
		Identifier: strings.TrimSpace(request.Identifier),
		APIName:    strings.TrimSpace(request.APIName),
	})
}
