package toolruntime

import (
	"encoding/json"
	"strings"

	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

func toolDefinitions(registry tools.Registry) []coremodel.ToolDefinition {
	modelTools := registry.ModelTools()
	definitions := make([]coremodel.ToolDefinition, 0, len(modelTools))
	for _, tool := range modelTools {
		call := registry.ResolveCall(tools.Call{Name: tool.Function.Name})
		_, api, _ := registry.GetAPI(call.Identifier, call.APIName)
		concurrencyClass := toolExecutionMode(api)
		definitions = append(definitions, coremodel.ToolDefinition{
			Name:             tool.Function.Name,
			Description:      tool.Function.Description,
			InputSchema:      append(json.RawMessage(nil), tool.Function.Parameters...),
			SideEffect:       toolSideEffect(api),
			Idempotency:      toolIdempotency(api),
			ConcurrencyClass: concurrencyClass,
			LockKeyTemplate:  strings.TrimSpace(api.LockKey),
		})
	}
	return definitions
}
