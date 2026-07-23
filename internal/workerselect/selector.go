package workerselect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type Store interface {
	ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error)
}

type Selector struct {
	Store Store
	Now   func() time.Time
}

type Request struct {
	WorkspaceID string
	Invocation  tools.WorkInvocation
}

type WorkerDiagnosis struct {
	Worker       managedagents.Worker
	Match        bool
	Reasons      []string
	Capabilities tools.WorkerCapabilities
}

func AvailableFromWorkers(workers []managedagents.Worker, runtime string, now time.Time) tools.AvailableCapabilities {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if normalized, ok := tools.NormalizeToolRuntime(runtime); ok {
		runtime = normalized
	}
	available := tools.AvailableCapabilities{Runtime: runtime}
	seenNamespaces := map[string]bool{}
	seenAPIs := map[string]bool{}
	seenCapabilities := map[string]bool{}
	for _, worker := range workers {
		if worker.Status != "" && worker.Status != managedagents.WorkerStatusOnline {
			continue
		}
		if worker.LeaseExpiresAt != nil && worker.LeaseExpiresAt.Before(now) {
			continue
		}
		capabilities, err := decodeCapabilities(worker)
		if err != nil {
			continue
		}
		if runtime != "" && runtime != tools.ToolRuntimeAuto && !containsString(capabilities.Runtimes, runtime) {
			continue
		}
		appendUnique(&available.Namespaces, seenNamespaces, capabilities.Namespaces)
		appendUnique(&available.APIs, seenAPIs, capabilities.APIs)
		appendUnique(&available.Capabilities, seenCapabilities, capabilities.Capabilities)
	}
	return available
}

func AvailableRegistryFromWorkers(registry tools.Registry, workers []managedagents.Worker, runtime string, now time.Time) tools.Registry {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if normalized, ok := tools.NormalizeToolRuntime(runtime); ok {
		runtime = normalized
	}
	registry = registryWithWorkerManifests(registry, workers, now)
	return registry.FilterAPIs(func(manifest tools.Manifest, api tools.API) bool {
		invocation := tools.WorkInvocationFromAPI(manifest, api, runtime, nil)
		for _, worker := range workers {
			if !workerAvailable(worker, now) {
				continue
			}
			if MatchesInvocation(worker, invocation) {
				return true
			}
		}
		return false
	})
}

func registryWithWorkerManifests(registry tools.Registry, workers []managedagents.Worker, now time.Time) tools.Registry {
	for _, worker := range workers {
		if !workerAvailable(worker, now) {
			continue
		}
		capabilities, err := decodeCapabilities(worker)
		if err != nil {
			continue
		}
		for _, manifest := range capabilities.Manifests {
			if _, exists := registry.Get(manifest.Identifier); exists {
				continue
			}
			registry.Register(tools.ManifestRuntime{ManifestData: manifest})
		}
	}
	return registry
}

func (s Selector) SelectWorkerID(input Request) (string, error) {
	return s.SelectWorkerIDContext(context.Background(), input)
}

func (s Selector) SelectWorkerIDContext(ctx context.Context, input Request) (string, error) {
	if s.Store == nil {
		return "", fmt.Errorf("%w: worker selector store is required", managedagents.ErrInvalid)
	}
	workspaceID := input.WorkspaceID
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := managedagents.ListWorkersWithContext(ctx, s.Store, managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
		Status:      managedagents.WorkerStatusOnline,
	})
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	for _, worker := range workers {
		if worker.LeaseExpiresAt != nil && worker.LeaseExpiresAt.Before(now) {
			continue
		}
		if MatchesInvocation(worker, input.Invocation) {
			return worker.ID, nil
		}
	}
	return "", fmt.Errorf(
		"%w: no online worker matches tool invocation %s runtime %s",
		managedagents.ErrConflict,
		tools.ModelToolName(input.Invocation.Namespace, input.Invocation.API),
		input.Invocation.Runtime,
	)
}

func MatchesInvocation(worker managedagents.Worker, invocation tools.WorkInvocation) bool {
	capabilities, err := decodeCapabilities(worker)
	if err != nil {
		return false
	}
	namespace, ok := tools.NormalizeToolNamespace(invocation.Namespace)
	if !ok {
		return false
	}
	apiName := strings.TrimSpace(invocation.API)
	if apiName == "" {
		return false
	}
	return capabilitySetMatches(capabilities, namespace, apiName, invocation.Runtime, invocation.Capabilities)
}

func DiagnoseInvocation(workers []managedagents.Worker, invocation tools.WorkInvocation, now time.Time) []WorkerDiagnosis {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	diagnostics := make([]WorkerDiagnosis, 0, len(workers))
	for _, worker := range workers {
		diagnosis := WorkerDiagnosis{Worker: worker}
		if worker.Status != "" && worker.Status != managedagents.WorkerStatusOnline {
			diagnosis.Reasons = append(diagnosis.Reasons, "status is "+worker.Status)
		}
		if worker.LeaseExpiresAt != nil && worker.LeaseExpiresAt.Before(now) {
			diagnosis.Reasons = append(diagnosis.Reasons, "lease expired at "+worker.LeaseExpiresAt.UTC().Format(time.RFC3339))
		}

		capabilities, err := decodeCapabilities(worker)
		if err != nil {
			diagnosis.Reasons = append(diagnosis.Reasons, "invalid capabilities: "+err.Error())
			diagnostics = append(diagnostics, diagnosis)
			continue
		}
		diagnosis.Capabilities = capabilities
		diagnosis.Reasons = append(diagnosis.Reasons, invocationMismatchReasons(capabilities, invocation)...)
		diagnosis.Match = len(diagnosis.Reasons) == 0
		diagnostics = append(diagnostics, diagnosis)
	}
	return diagnostics
}

func workerAvailable(worker managedagents.Worker, now time.Time) bool {
	if worker.Status != "" && worker.Status != managedagents.WorkerStatusOnline {
		return false
	}
	if worker.LeaseExpiresAt != nil && worker.LeaseExpiresAt.Before(now) {
		return false
	}
	return true
}

func decodeCapabilities(worker managedagents.Worker) (tools.WorkerCapabilities, error) {
	return tools.DecodeWorkerCapabilities(worker.Capabilities)
}

func invocationMismatchReasons(capabilities tools.WorkerCapabilities, invocation tools.WorkInvocation) []string {
	var reasons []string
	namespace, ok := tools.NormalizeToolNamespace(invocation.Namespace)
	if !ok {
		return []string{"invalid namespace " + strings.TrimSpace(invocation.Namespace)}
	}
	apiName := strings.TrimSpace(invocation.API)
	if apiName == "" {
		return []string{"missing api"}
	}
	if !containsString(capabilities.Namespaces, namespace) {
		reasons = append(reasons, "missing namespace "+namespace)
	}
	canonicalName := tools.ModelToolName(namespace, apiName)
	if !containsString(capabilities.APIs, canonicalName) {
		reasons = append(reasons, "missing api "+canonicalName)
	}
	runtime, ok := tools.NormalizeToolRuntime(invocation.Runtime)
	if !ok {
		reasons = append(reasons, "invalid runtime "+strings.TrimSpace(invocation.Runtime))
	} else if runtime == tools.ToolRuntimeAuto && len(capabilities.Runtimes) == 0 {
		reasons = append(reasons, "no runtimes declared for auto")
	} else if runtime != tools.ToolRuntimeAuto && !containsString(capabilities.Runtimes, runtime) {
		reasons = append(reasons, "missing runtime "+runtime)
	}
	for _, capability := range invocation.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability != "" && !containsString(capabilities.Capabilities, capability) {
			reasons = append(reasons, "missing capability "+capability)
		}
	}
	return reasons
}

func capabilitySetMatches(capabilities tools.WorkerCapabilities, namespace string, apiName string, runtimeValue string, requiredCapabilities []string) bool {
	namespace, ok := tools.NormalizeToolNamespace(namespace)
	if !ok {
		return false
	}
	apiName = strings.TrimSpace(apiName)
	if apiName == "" {
		return false
	}
	if !containsString(capabilities.Namespaces, namespace) {
		return false
	}
	if !containsString(capabilities.APIs, tools.ModelToolName(namespace, apiName)) {
		return false
	}
	runtime, ok := tools.NormalizeToolRuntime(runtimeValue)
	if !ok {
		return false
	}
	if runtime == tools.ToolRuntimeAuto && len(capabilities.Runtimes) == 0 {
		return false
	}
	if runtime != tools.ToolRuntimeAuto && !containsString(capabilities.Runtimes, runtime) {
		return false
	}
	for _, capability := range requiredCapabilities {
		if !containsString(capabilities.Capabilities, capability) {
			return false
		}
	}
	return true
}

func appendUnique(target *[]string, seen map[string]bool, values []string) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		*target = append(*target, value)
	}
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
