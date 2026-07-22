package modelruntime

import (
	"context"
	"encoding/json"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
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
