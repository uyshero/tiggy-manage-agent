package toolruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"tiggy-manage-agent/internal/tools"
)

var middlewareIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,119})$`)

type MiddlewareDescriptor struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// ToolMiddleware wraps execution after schema and permission preflight. The
// ordered descriptor chain is part of the durable ToolBatchPlan contract.
type ToolMiddleware interface {
	Descriptor() MiddlewareDescriptor
	Wrap(next tools.Executor) tools.Executor
}

func freezeMiddleware(middlewares []ToolMiddleware) ([]ToolMiddleware, []MiddlewareDescriptor, string, error) {
	frozen := append([]ToolMiddleware(nil), middlewares...)
	descriptors := make([]MiddlewareDescriptor, len(frozen))
	seen := make(map[string]bool, len(frozen))
	for index, middleware := range frozen {
		if middleware == nil {
			return nil, nil, "", tools.NewToolContractError("invalid_tool_middleware", fmt.Errorf("tool middleware %d is nil", index))
		}
		descriptor := middleware.Descriptor()
		descriptor.ID = strings.TrimSpace(strings.ToLower(descriptor.ID))
		descriptor.Version = strings.TrimSpace(descriptor.Version)
		if !middlewareIDPattern.MatchString(descriptor.ID) {
			return nil, nil, "", tools.NewToolContractError("invalid_tool_middleware", fmt.Errorf("tool middleware %d has invalid id %q", index, descriptor.ID))
		}
		if descriptor.Version == "" || len(descriptor.Version) > 120 {
			return nil, nil, "", tools.NewToolContractError("invalid_tool_middleware", fmt.Errorf("tool middleware %q requires a 1-120 character version", descriptor.ID))
		}
		if seen[descriptor.ID] {
			return nil, nil, "", tools.NewToolContractError("invalid_tool_middleware", fmt.Errorf("duplicate tool middleware id %q", descriptor.ID))
		}
		seen[descriptor.ID] = true
		descriptors[index] = descriptor
	}
	encoded, err := json.Marshal(descriptors)
	if err != nil {
		return nil, nil, "", tools.NewToolContractError("invalid_tool_middleware", fmt.Errorf("encode tool middleware contract: %w", err))
	}
	sum := sha256.Sum256(encoded)
	return frozen, descriptors, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func wrapMiddlewareExecutor(middlewares []ToolMiddleware, executor tools.Executor) (tools.Executor, error) {
	if executor == nil {
		return nil, fmt.Errorf("tool executor is required")
	}
	wrapped := executor
	for index := len(middlewares) - 1; index >= 0; index-- {
		wrapped = middlewares[index].Wrap(wrapped)
		if wrapped == nil {
			return nil, fmt.Errorf("tool middleware %q returned a nil executor", middlewares[index].Descriptor().ID)
		}
	}
	return wrapped, nil
}
