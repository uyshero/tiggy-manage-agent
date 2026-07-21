package agentcoreadapter

import (
	"context"
	"encoding/json"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

type FixedContext struct {
	Purpose         coremodel.RequestPurpose
	Route           coremodel.Route
	Tools           []coremodel.ToolDefinition
	MaxOutputTokens int
}

var _ agentcore.ContextPort = FixedContext{}

func (c FixedContext) Build(_ context.Context, state agentcore.State) (coremodel.Request, error) {
	return coremodel.Request{
		Purpose:         c.Purpose,
		Route:           cloneModelRoute(c.Route),
		Messages:        coremodel.CloneMessages(state.Messages),
		Tools:           cloneToolDefinitions(c.Tools),
		MaxOutputTokens: c.MaxOutputTokens,
	}, nil
}

func ToolDefinitions(registry tools.Registry) []coremodel.ToolDefinition {
	modelTools := registry.ModelTools()
	definitions := make([]coremodel.ToolDefinition, 0, len(modelTools))
	for _, tool := range modelTools {
		call := tools.NormalizeCall(tools.Call{Name: tool.Function.Name})
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

func cloneModelRoute(route coremodel.Route) coremodel.Route {
	cloned := route
	cloned.Parameters = append(json.RawMessage(nil), route.Parameters...)
	return cloned
}

func cloneToolDefinitions(definitions []coremodel.ToolDefinition) []coremodel.ToolDefinition {
	if definitions == nil {
		return nil
	}
	cloned := make([]coremodel.ToolDefinition, len(definitions))
	for index, definition := range definitions {
		cloned[index] = definition
		cloned[index].InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
		cloned[index].OutputSchema = append(json.RawMessage(nil), definition.OutputSchema...)
	}
	return cloned
}
