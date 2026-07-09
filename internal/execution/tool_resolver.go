package execution

import (
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workerselect"
)

type ToolExecutionRequest struct {
	Config           managedagents.AgentRuntimeConfig
	SessionID        string
	TurnID           string
	ProviderResolver ProviderResolver
	Store            WorkerBackedStore
	ArtifactRecorder tools.ArtifactRecorder
	Now              func() time.Time
}

type ToolExecution struct {
	Registry               tools.Registry
	Policy                 tools.ConfigPolicy
	Context                tools.ExecutionContext
	Provider               capability.Provider
	ProviderCapabilities   tools.AvailableCapabilities
	WorkerBacked           bool
	LocalSystemUnavailable bool
}

func ResolveToolExecution(request ToolExecutionRequest) ToolExecution {
	config := request.Config
	sessionID := request.SessionID
	if sessionID == "" {
		sessionID = config.SessionID
	}
	registry, toolPolicy := tools.DefaultRegistry().Configured(config.Tools)
	provider := resolveToolProvider(request.ProviderResolver, config, sessionID, toolPolicy)
	providerCapabilities := AvailableCapabilities(provider, toolPolicy)

	workerBacked := false
	localSystemUnavailable := false
	if workerRegistry, ok := availableWorkerRegistry(request.Store, config.WorkspaceID, providerCapabilities, registry, request.now()); ok {
		registry = workerRegistry
		provider = WorkerBackedProvider{
			Store:         request.Store,
			WorkspaceID:   config.WorkspaceID,
			SessionID:     sessionID,
			EnvironmentID: config.EnvironmentID,
			TurnID:        request.TurnID,
		}
		workerBacked = true
	} else if LocalSystemUnavailable(providerCapabilities, provider) {
		registry = tools.Registry{}
		localSystemUnavailable = true
	} else {
		registry = registry.Available(providerCapabilities)
	}

	return ToolExecution{
		Registry:               registry,
		Policy:                 toolPolicy,
		Provider:               provider,
		ProviderCapabilities:   providerCapabilities,
		WorkerBacked:           workerBacked,
		LocalSystemUnavailable: localSystemUnavailable,
		Context: tools.ExecutionContext{
			WorkspaceID:      config.WorkspaceID,
			SessionID:        sessionID,
			EnvironmentID:    config.EnvironmentID,
			TurnID:           request.TurnID,
			Provider:         provider,
			ArtifactRecorder: request.ArtifactRecorder,
		},
	}
}

func (request ToolExecutionRequest) now() time.Time {
	if request.Now != nil {
		return request.Now().UTC()
	}
	return time.Now().UTC()
}

func resolveToolProvider(resolver ProviderResolver, config managedagents.AgentRuntimeConfig, sessionID string, toolPolicy tools.ConfigPolicy) capability.Provider {
	providerRequest := ProviderRequest{
		WorkspaceID:   config.WorkspaceID,
		SessionID:     sessionID,
		EnvironmentID: config.EnvironmentID,
		ToolRuntime:   toolPolicy.Runtime,
	}
	if resolver != nil {
		if provider := resolver.ResolveProvider(providerRequest); provider != nil {
			return provider
		}
	}
	return SessionProviderResolver{}.ResolveProvider(providerRequest)
}

func AvailableCapabilities(provider capability.Provider, toolPolicy tools.ConfigPolicy) tools.AvailableCapabilities {
	runtime := toolPolicy.Runtime
	if runtime == "" {
		runtime = tools.ToolRuntimeAuto
	}
	capabilities := []string{
		tools.CapabilityFilesystemRead,
		tools.CapabilityFilesystemWrite,
		tools.CapabilityExec,
		tools.CapabilityCodeExecute,
	}
	if descriptor, ok := provider.(capability.CapabilityDescriptor); ok {
		if declaredRuntime := descriptor.ToolRuntime(); declaredRuntime != "" && runtime == tools.ToolRuntimeAuto {
			runtime = declaredRuntime
		}
		capabilities = descriptor.ToolCapabilities()
	}
	switch provider.(type) {
	case capability.LocalSystemProvider:
		if runtime == tools.ToolRuntimeAuto {
			runtime = tools.ToolRuntimeLocalSystem
		}
	case capability.OnlyboxesProvider:
		if runtime == tools.ToolRuntimeAuto {
			runtime = tools.ToolRuntimeCloudSandbox
		}
	}
	return tools.AvailableCapabilities{
		Runtime:      runtime,
		Capabilities: capabilities,
	}
}

func LocalSystemUnavailable(providerCapabilities tools.AvailableCapabilities, provider capability.Provider) bool {
	if providerCapabilities.Runtime != tools.ToolRuntimeLocalSystem {
		return false
	}
	if _, ok := provider.(capability.UnavailableProvider); ok {
		return true
	}
	return len(providerCapabilities.Capabilities) == 0
}

func availableWorkerRegistry(store workerselect.Store, workspaceID string, providerCapabilities tools.AvailableCapabilities, registry tools.Registry, now time.Time) (tools.Registry, bool) {
	if store == nil {
		return tools.Registry{}, false
	}
	runtime := providerCapabilities.Runtime
	if runtime == "" {
		runtime = tools.ToolRuntimeAuto
	}
	if runtime != tools.ToolRuntimeLocalSystem {
		return tools.Registry{}, false
	}
	workers, err := store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
		Status:      managedagents.WorkerStatusOnline,
	})
	if err != nil || len(workers) == 0 {
		return tools.Registry{}, false
	}
	available := workerselect.AvailableRegistryFromWorkers(registry, workers, runtime, now)
	if len(available.ModelTools()) == 0 {
		return tools.Registry{}, false
	}
	return available, true
}
