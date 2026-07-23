package workerselect

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestMatchesInvocationRequiresNamespaceAPIAndCapabilities(t *testing.T) {
	worker := managedagents.Worker{
		ID: "wrk_000001",
		Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
			Namespaces:   []string{"default"},
			APIs:         []string{"default_run_command"},
			Runtimes:     []string{"local_system"},
			Capabilities: []string{"exec"},
		}),
	}

	if !MatchesInvocation(worker, tools.WorkInvocation{
		Namespace:    "default",
		API:          "run_command",
		Runtime:      "local_system",
		Capabilities: []string{"exec"},
	}) {
		t.Fatal("expected matching worker")
	}
	if MatchesInvocation(worker, tools.WorkInvocation{
		Namespace:    "default",
		API:          "run_command",
		Runtime:      "local_system",
		Capabilities: []string{"filesystem.read"},
	}) {
		t.Fatal("expected capability mismatch")
	}
}

func TestSelectorSkipsExpiredWorkersAndUsesWorkspaceStatus(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	valid := now.Add(time.Minute)
	store := &fakeStore{workers: []managedagents.Worker{
		{
			ID:             "wrk_expired",
			WorkspaceID:    "wksp_1",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &expired,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"exec"},
			}),
		},
		{
			ID:             "wrk_valid",
			WorkspaceID:    "wksp_1",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"exec"},
			}),
		},
	}}

	workerID, err := (Selector{Store: store, Now: func() time.Time { return now }}).SelectWorkerID(Request{
		WorkspaceID: "wksp_1",
		Invocation: tools.WorkInvocation{
			Namespace:    "default",
			API:          "run_command",
			Runtime:      "local_system",
			Capabilities: []string{"exec"},
		},
	})
	if err != nil {
		t.Fatalf("select worker: %v", err)
	}
	if workerID != "wrk_valid" {
		t.Fatalf("expected valid worker, got %q", workerID)
	}
	if store.input.WorkspaceID != "wksp_1" || store.input.Status != managedagents.WorkerStatusOnline {
		t.Fatalf("unexpected list workers input: %#v", store.input)
	}
}

func TestSelectorReturnsConflictWhenNoWorkerMatches(t *testing.T) {
	_, err := (Selector{Store: &fakeStore{}}).SelectWorkerID(Request{
		Invocation: tools.WorkInvocation{Namespace: "default", API: "run_command", Runtime: "local_system"},
	})
	if !errors.Is(err, managedagents.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestAvailableFromWorkersAggregatesMatchingWorkerCapabilities(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	valid := now.Add(time.Minute)
	available := AvailableFromWorkers([]managedagents.Worker{
		{
			ID:             "wrk_expired",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &expired,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"exec"},
			}),
		},
		{
			ID:             "wrk_reader",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_read_file"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"filesystem.read"},
			}),
		},
		{
			ID:             "wrk_cloud",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_execute_code"},
				Runtimes:     []string{"cloud_sandbox"},
				Capabilities: []string{"code.execute", "exec"},
			}),
		},
	}, tools.ToolRuntimeLocalSystem, now)

	if available.Runtime != tools.ToolRuntimeLocalSystem {
		t.Fatalf("unexpected runtime: %#v", available)
	}
	if len(available.APIs) != 1 || available.APIs[0] != "default_read_file" {
		t.Fatalf("expected only local reader API, got %#v", available.APIs)
	}
	if len(available.Capabilities) != 1 || available.Capabilities[0] != "filesystem.read" {
		t.Fatalf("expected reader capability, got %#v", available.Capabilities)
	}
}

func TestAvailableRegistryFromWorkersRequiresSingleWorkerToMatchAPI(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	valid := now.Add(time.Minute)
	registry := AvailableRegistryFromWorkers(tools.DefaultRegistry(), []managedagents.Worker{
		{
			ID:             "wrk_api_without_capability",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"filesystem.read"},
			}),
		},
		{
			ID:             "wrk_capability_without_api",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_read_file"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"exec"},
			}),
		},
		{
			ID:             "wrk_reader",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_read_file"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"filesystem.read"},
			}),
		},
	}, tools.ToolRuntimeLocalSystem, now)

	modelTools := registry.ModelTools()
	if len(modelTools) != 1 || modelTools[0].Function.Name != "default_read_file" {
		t.Fatalf("expected only read_file to be executable by one worker, got %#v", modelTools)
	}
	if _, _, ok := registry.GetAPI("default", "run_command"); ok {
		t.Fatal("expected run_command to be unavailable when API and exec capability are split across workers")
	}
}

func TestDiagnoseInvocationExplainsWorkerMismatches(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	valid := now.Add(time.Minute)
	diagnostics := DiagnoseInvocation([]managedagents.Worker{
		{
			ID:             "wrk_expired",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &expired,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"exec"},
			}),
		},
		{
			ID:             "wrk_missing_capability",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"filesystem.read"},
			}),
		},
		{
			ID:             "wrk_match",
			Status:         managedagents.WorkerStatusOnline,
			LeaseExpiresAt: &valid,
			Capabilities: rawCapabilities(t, tools.WorkerCapabilities{
				Namespaces:   []string{"default"},
				APIs:         []string{"default_run_command"},
				Runtimes:     []string{"local_system"},
				Capabilities: []string{"exec"},
			}),
		},
	}, tools.WorkInvocation{
		Namespace:    "default",
		API:          "run_command",
		Runtime:      "local_system",
		Capabilities: []string{"exec"},
	}, now)

	if len(diagnostics) != 3 {
		t.Fatalf("expected 3 diagnostics, got %#v", diagnostics)
	}
	if diagnostics[0].Match || !containsString(diagnostics[0].Reasons, "lease expired at 2026-07-09T11:59:00Z") {
		t.Fatalf("expected expired worker reason, got %#v", diagnostics[0])
	}
	if diagnostics[1].Match || !containsString(diagnostics[1].Reasons, "missing capability exec") {
		t.Fatalf("expected missing capability reason, got %#v", diagnostics[1])
	}
	if !diagnostics[2].Match || len(diagnostics[2].Reasons) != 0 {
		t.Fatalf("expected matching worker, got %#v", diagnostics[2])
	}
}

type fakeStore struct {
	input   managedagents.ListWorkersInput
	workers []managedagents.Worker
}

func (s *fakeStore) ListWorkers(input managedagents.ListWorkersInput) ([]managedagents.Worker, error) {
	s.input = input
	return append([]managedagents.Worker(nil), s.workers...), nil
}

func rawCapabilities(t *testing.T, capabilities tools.WorkerCapabilities) json.RawMessage {
	t.Helper()

	encoded, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	return encoded
}
