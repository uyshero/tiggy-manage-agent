package tools

import (
	"context"
	"fmt"
)

// WorkerToolProvider is implemented by providers that can execute arbitrary
// worker-backed tools described by a manifest.
type WorkerToolProvider interface {
	ExecuteWorkerTool(context.Context, Manifest, API, Call, ExecutionContext) (ExecutionResult, error)
}

// ManifestRuntime exposes a manifest whose implementation lives behind a
// WorkerToolProvider. It is used by the server to expose worker plugin tools to
// the model without loading the plugin process in the server.
type ManifestRuntime struct {
	ManifestData Manifest
}

func (r ManifestRuntime) Manifest() Manifest {
	return r.ManifestData
}

func (r ManifestRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	provider, ok := executionContext.Provider.(WorkerToolProvider)
	if !ok || provider == nil {
		return ExecutionResult{}, fmt.Errorf("worker-backed provider is required for tool %s.%s", call.Identifier, call.APIName)
	}
	manifest := r.Manifest()
	for _, api := range manifest.API {
		if api.Name == call.APIName || api.APIName == call.APIName {
			return provider.ExecuteWorkerTool(ctx, manifest, api, call, executionContext)
		}
	}
	return ExecutionResult{}, fmt.Errorf("unsupported manifest api %q", call.APIName)
}
