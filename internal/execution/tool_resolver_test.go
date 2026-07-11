package execution

import (
	"encoding/json"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestResolveToolExecutionDefaultsToCloudSandbox(t *testing.T) {
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
		},
		TurnID: "turn_000001",
	})

	if _, ok := resolved.Context.Provider.(capability.OnlyboxesProvider); !ok {
		t.Fatalf("expected cloud_sandbox provider, got %T", resolved.Context.Provider)
	}
	if resolved.ProviderCapabilities.Runtime != tools.ToolRuntimeCloudSandbox {
		t.Fatalf("expected cloud_sandbox runtime, got %#v", resolved.ProviderCapabilities)
	}
	if len(resolved.Registry.ModelTools()) == 0 {
		t.Fatal("expected default tools to remain visible for cloud_sandbox")
	}
}

func TestResolveToolExecutionHidesLocalSystemWithoutWorker(t *testing.T) {
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime: ToolRuntimeCloudSandbox,
		},
		Store: &toolResolverStore{},
	})

	if !resolved.LocalSystemUnavailable {
		t.Fatal("expected local_system unavailable without worker")
	}
	if len(resolved.Registry.ModelTools()) != 0 {
		t.Fatalf("expected no model tools without local_system runtime, got %#v", resolved.Registry.ModelTools())
	}
	if _, ok := resolved.Context.Provider.(capability.UnavailableProvider); !ok {
		t.Fatalf("expected unavailable provider, got %T", resolved.Context.Provider)
	}
}

func TestResolveToolExecutionUsesWorkerBackedProviderForMatchingWorker(t *testing.T) {
	expiresAt := time.Date(2026, 7, 9, 12, 5, 0, 0, time.UTC)
	store := &toolResolverStore{workers: []managedagents.Worker{{
		ID:             "wrk_000001",
		WorkspaceID:    "wksp_default",
		Status:         managedagents.WorkerStatusOnline,
		LeaseExpiresAt: &expiresAt,
		Capabilities: rawWorkerCapabilities(t, tools.WorkerCapabilities{
			Namespaces:   []string{"default"},
			APIs:         []string{"default.run_command"},
			Runtimes:     []string{"local_system"},
			Capabilities: []string{"exec"},
		}),
	}}}
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime: ToolRuntimeCloudSandbox,
		},
		Store: store,
		Now:   func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) },
	})

	if !resolved.WorkerBacked {
		t.Fatal("expected worker-backed local_system")
	}
	if _, ok := resolved.Context.Provider.(WorkerBackedProvider); !ok {
		t.Fatalf("expected worker-backed provider, got %T", resolved.Context.Provider)
	}
	if got := len(resolved.Registry.ModelTools()); got != 1 {
		t.Fatalf("expected only matching worker tool to be visible, got %d", got)
	}
	if store.listInput.WorkspaceID != "wksp_default" || store.listInput.Status != managedagents.WorkerStatusOnline {
		t.Fatalf("unexpected worker list input: %#v", store.listInput)
	}
}

func TestResolveToolExecutionExposesWorkerPluginManifest(t *testing.T) {
	expiresAt := time.Date(2026, 7, 9, 12, 5, 0, 0, time.UTC)
	store := &toolResolverStore{workers: []managedagents.Worker{{
		ID:             "wrk_robot",
		WorkspaceID:    "wksp_default",
		Status:         managedagents.WorkerStatusOnline,
		LeaseExpiresAt: &expiresAt,
		Capabilities: rawWorkerCapabilities(t, tools.WorkerCapabilities{
			Namespaces:   []string{"robot"},
			APIs:         []string{"robot.get_state"},
			Runtimes:     []string{"local_system"},
			Capabilities: []string{"robot.state"},
			Manifests:    []tools.Manifest{robotManifest()},
		}),
	}}}
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["robot"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime: ToolRuntimeCloudSandbox,
		},
		Store: store,
		Now:   func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) },
	})

	if !resolved.WorkerBacked {
		t.Fatal("expected plugin tool to use worker-backed provider")
	}
	modelTools := resolved.Registry.ModelTools()
	if len(modelTools) != 1 || modelTools[0].Function.Name != "robot.get_state" {
		t.Fatalf("expected plugin model tool, got %#v", modelTools)
	}
	if _, ok := resolved.Registry.Get("robot"); !ok {
		t.Fatal("expected plugin manifest runtime in registry")
	}
}

func TestResolveToolExecutionAllowsExplicitServerLocalFallback(t *testing.T) {
	resolved := ResolveToolExecution(ToolExecutionRequest{
		Config: managedagents.AgentRuntimeConfig{
			WorkspaceID: "wksp_default",
			SessionID:   "sesn_000001",
			Tools:       json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		},
		TurnID: "turn_000001",
		ProviderResolver: SessionProviderResolver{
			DefaultRuntime:   ToolRuntimeCloudSandbox,
			AllowLocalSystem: true,
		},
		Store: &toolResolverStore{},
	})

	if resolved.LocalSystemUnavailable || resolved.WorkerBacked {
		t.Fatalf("unexpected resolver flags: %#v", resolved)
	}
	if _, ok := resolved.Context.Provider.(capability.LocalSystemProvider); !ok {
		t.Fatalf("expected server-local provider, got %T", resolved.Context.Provider)
	}
	if len(resolved.Registry.ModelTools()) == 0 {
		t.Fatal("expected local_system tools when dev fallback is enabled")
	}
}

type toolResolverStore struct {
	listInput managedagents.ListWorkersInput
	workers   []managedagents.Worker
}

func (s *toolResolverStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	s.listInput = input
	return append([]managedagents.Worker(nil), s.workers...), nil
}

func (s *toolResolverStore) EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func (s *toolResolverStore) GetWorkerWork(id string) (managedagents.WorkerWork, error) {
	return managedagents.WorkerWork{}, nil
}

func robotManifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "robot",
		Type:       "process_plugin",
		Meta: tools.Meta{
			Title:       "Robot",
			Description: "Robot control plugin.",
		},
		SystemRole: "Use robot.* tools only for robot control tasks.",
		API: []tools.API{{
			Name:           "get_state",
			Namespace:      "robot",
			APIName:        "get_state",
			Description:    "Read robot state.",
			Parameters:     json.RawMessage(`{"type":"object","properties":{}}`),
			Capabilities:   []string{"robot.state"},
			Risk:           tools.ToolRiskRead,
			Runtime:        &tools.RuntimePolicy{Allowed: []string{tools.ToolRuntimeLocalSystem}, Preferred: tools.ToolRuntimeLocalSystem},
			Implementation: tools.ToolImplementationWorkerCapability,
		}},
	}
}
