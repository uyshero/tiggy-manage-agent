package toolruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/llm"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

// Snapshot is the immutable tool contract used for one Agent Loop run. It
// freezes model definitions, registry manifests, and permission policy before
// the first model request.
type Snapshot struct {
	registry           tools.Registry
	registryRevision   string
	policy             tools.InterventionPolicy
	policyRevision     string
	middlewares        []ToolMiddleware
	middleware         []MiddlewareDescriptor
	middlewareRevision string
	definitions        []coremodel.ToolDefinition
	modelContext       json.RawMessage
	modelTools         []llm.Tool
}

func NewSnapshot(registry tools.Registry, policy tools.InterventionPolicy) (Snapshot, error) {
	return NewSnapshotWithMiddleware(registry, policy, nil)
}

func NewSnapshotWithMiddleware(registry tools.Registry, policy tools.InterventionPolicy, middlewares []ToolMiddleware) (Snapshot, error) {
	frozenRegistry, registryRevision, err := registry.Snapshot()
	if err != nil {
		return Snapshot{}, err
	}
	frozenPolicy, policyRevision, err := freezePolicy(policy)
	if err != nil {
		return Snapshot{}, err
	}
	frozenMiddleware, middleware, middlewareRevision, err := freezeMiddleware(middlewares)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		registry: frozenRegistry, registryRevision: registryRevision,
		policy: frozenPolicy, policyRevision: policyRevision,
		middlewares: frozenMiddleware, middleware: middleware, middlewareRevision: middlewareRevision,
		definitions:  toolDefinitions(frozenRegistry),
		modelContext: frozenRegistry.ModelContext(), modelTools: frozenRegistry.ModelTools(),
	}, nil
}

func (s Snapshot) Definitions() []coremodel.ToolDefinition {
	return cloneToolDefinitions(s.definitions)
}

func (s Snapshot) ModelContext() json.RawMessage {
	return append(json.RawMessage(nil), s.modelContext...)
}

func (s Snapshot) ModelTools() []llm.Tool {
	if s.modelTools == nil {
		return nil
	}
	cloned := make([]llm.Tool, len(s.modelTools))
	for index, tool := range s.modelTools {
		cloned[index] = tool
		cloned[index].Function.Parameters = append(json.RawMessage(nil), tool.Function.Parameters...)
	}
	return cloned
}

func (s Snapshot) RegistryRevision() string {
	return s.registryRevision
}

func (s Snapshot) PolicyRevision() string {
	return s.policyRevision
}

func (s Snapshot) MiddlewareRevision() string {
	return s.middlewareRevision
}

func (s Snapshot) Middleware() []MiddlewareDescriptor {
	return append([]MiddlewareDescriptor(nil), s.middleware...)
}

func freezePolicy(policy tools.InterventionPolicy) (tools.InterventionPolicy, string, error) {
	mode := strings.TrimSpace(policy.Mode)
	if mode == "" {
		mode = tools.InterventionModeRequestApproval
	} else {
		var ok bool
		mode, ok = tools.NormalizeInterventionMode(mode)
		if !ok {
			return tools.InterventionPolicy{}, "", tools.NewToolContractError(
				"invalid_tool_policy", fmt.Errorf("invalid_tool_policy: unsupported intervention mode %q", policy.Mode),
			)
		}
	}
	rules := append([]tools.PermissionRule(nil), policy.Rules...)
	if err := tools.ValidatePermissionRules(rules); err != nil {
		return tools.InterventionPolicy{}, "", tools.NewToolContractError(
			"invalid_tool_policy", fmt.Errorf("invalid_tool_policy: %w", err),
		)
	}
	frozen := tools.InterventionPolicy{Mode: mode, Rules: rules}
	type revisionRule struct {
		ID       string `json:"id"`
		Tool     string `json:"tool"`
		Argument string `json:"argument"`
		Pattern  string `json:"pattern"`
		Behavior string `json:"behavior"`
		Reason   string `json:"reason,omitempty"`
		Source   string `json:"source,omitempty"`
	}
	contract := struct {
		Mode  string         `json:"mode"`
		Rules []revisionRule `json:"rules"`
	}{Mode: mode, Rules: make([]revisionRule, len(rules))}
	for index, rule := range rules {
		contract.Rules[index] = revisionRule{
			ID: rule.ID, Tool: rule.Tool, Argument: rule.Argument, Pattern: rule.Pattern,
			Behavior: rule.Behavior, Reason: rule.Reason, Source: rule.Source,
		}
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return tools.InterventionPolicy{}, "", tools.NewToolContractError(
			"invalid_tool_policy", fmt.Errorf("invalid_tool_policy: encode policy snapshot: %w", err),
		)
	}
	sum := sha256.Sum256(encoded)
	return frozen, "sha256:" + hex.EncodeToString(sum[:]), nil
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
