package execution

import (
	"encoding/json"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
)

func TestSessionProviderResolverReturnsUnavailableLocalSystemByDefault(t *testing.T) {
	store := fakeSessionStore{session: managedagents.Session{
		ID:              "sesn_000001",
		RuntimeSettings: json.RawMessage(`{"tool_runtime":"local_system"}`),
	}}
	provider := SessionProviderResolver{Store: store}.ResolveProvider(ProviderRequest{SessionID: "sesn_000001"})
	if unavailable, ok := provider.(capability.UnavailableProvider); !ok || unavailable.Runtime != ToolRuntimeLocalSystem {
		t.Fatalf("expected unavailable local_system provider, got %#v", provider)
	}
}

func TestSessionProviderResolverUsesCloudSandboxRuntime(t *testing.T) {
	store := fakeSessionStore{session: managedagents.Session{
		ID:              "sesn_000001",
		WorkspaceID:     "wksp_one",
		OwnerID:         "owner_one",
		RuntimeSettings: json.RawMessage(`{"tool_runtime":"cloud_sandbox","cloud_sandbox_image":"onlyboxes/test:latest","cloud_sandbox_allow_network":true}`),
	}}
	containerManager := capability.NewOnlyboxesContainerManager(capability.OnlyboxesContainerManagerConfig{CleanupInterval: time.Hour})
	t.Cleanup(containerManager.Close)
	provider := SessionProviderResolver{
		Store:                  store,
		CloudSandboxDataRoot:   "/tmp/tma-sandbox-data",
		CloudSandboxDataTTL:    30 * time.Minute,
		CloudSandboxContainers: containerManager,
	}.ResolveProvider(ProviderRequest{WorkspaceID: "wksp_one", OwnerID: "owner_one", SessionID: "sesn_000001"})
	onlyboxesProvider, ok := provider.(capability.OnlyboxesProvider)
	if !ok {
		t.Fatalf("expected onlyboxes provider, got %T", provider)
	}
	if onlyboxesProvider.Image != "onlyboxes/test:latest" {
		t.Fatalf("unexpected onlyboxes image %q", onlyboxesProvider.Image)
	}
	if onlyboxesProvider.DataRoot != "/tmp/tma-sandbox-data" {
		t.Fatalf("unexpected onlyboxes data root %q", onlyboxesProvider.DataRoot)
	}
	if onlyboxesProvider.DataDirTTL != 30*time.Minute {
		t.Fatalf("unexpected onlyboxes data ttl %s", onlyboxesProvider.DataDirTTL)
	}
	if onlyboxesProvider.DisableNetwork {
		t.Fatal("expected onlyboxes provider to allow network from session runtime settings")
	}
	if onlyboxesProvider.SessionID != "sesn_000001" {
		t.Fatalf("unexpected onlyboxes session id %q", onlyboxesProvider.SessionID)
	}
	if !onlyboxesProvider.IsolateWorkspace || onlyboxesProvider.WorkspaceID != "wksp_one" || onlyboxesProvider.OwnerID != "owner_one" {
		t.Fatalf("expected full isolated session scope, got %#v", onlyboxesProvider)
	}
	if onlyboxesProvider.ContainerManager != containerManager {
		t.Fatal("expected shared cloud sandbox container manager")
	}
}

func TestSessionProviderResolverDefaultsAutoToCloudSandbox(t *testing.T) {
	provider := SessionProviderResolver{}.ResolveProvider(ProviderRequest{})
	if _, ok := provider.(capability.OnlyboxesProvider); !ok {
		t.Fatalf("expected default cloud_sandbox onlyboxes provider, got %T", provider)
	}
}

func TestStaticProviderResolverDefaultsToCloudSandbox(t *testing.T) {
	provider := StaticProviderResolver{}.ResolveProvider(ProviderRequest{})
	if _, ok := provider.(capability.OnlyboxesProvider); !ok {
		t.Fatalf("expected default cloud_sandbox onlyboxes provider, got %T", provider)
	}
}

func TestSessionProviderResolverUsesDefaultCloudSandboxRuntime(t *testing.T) {
	store := fakeSessionStore{session: managedagents.Session{
		ID:              "sesn_000001",
		RuntimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
	}}
	provider := SessionProviderResolver{
		Store:             store,
		DefaultRuntime:    "cloud_sandbox",
		CloudSandboxRoot:  ".",
		CloudSandboxImage: "onlyboxes/default:latest",
	}.ResolveProvider(ProviderRequest{SessionID: "sesn_000001"})
	onlyboxesProvider, ok := provider.(capability.OnlyboxesProvider)
	if !ok {
		t.Fatalf("expected onlyboxes provider, got %T", provider)
	}
	if onlyboxesProvider.Image != "onlyboxes/default:latest" {
		t.Fatalf("unexpected onlyboxes image %q", onlyboxesProvider.Image)
	}
	if onlyboxesProvider.WorkspaceRoot != "." {
		t.Fatalf("unexpected cloud sandbox workspace root %q", onlyboxesProvider.WorkspaceRoot)
	}
	if onlyboxesProvider.DisableNetwork {
		t.Fatal("expected cloud sandbox network to stay enabled by default")
	}
}

func TestSessionProviderResolverUsesDefaultCloudSandboxNetworkDisabledSetting(t *testing.T) {
	store := fakeSessionStore{session: managedagents.Session{
		ID:              "sesn_000001",
		RuntimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
	}}
	provider := SessionProviderResolver{
		Store:                      store,
		DefaultRuntime:             "cloud_sandbox",
		CloudSandboxDisableNetwork: true,
	}.ResolveProvider(ProviderRequest{SessionID: "sesn_000001"})
	onlyboxesProvider, ok := provider.(capability.OnlyboxesProvider)
	if !ok {
		t.Fatalf("expected onlyboxes provider, got %T", provider)
	}
	if !onlyboxesProvider.DisableNetwork {
		t.Fatal("expected default cloud sandbox network disabled setting to propagate")
	}
}

func TestSessionProviderResolverAllowsServerLocalSystemWhenExplicitlyEnabled(t *testing.T) {
	store := fakeSessionStore{session: managedagents.Session{
		ID:              "sesn_000001",
		RuntimeSettings: json.RawMessage(`{"tool_runtime":"local_system"}`),
	}}
	provider := SessionProviderResolver{
		Store:            store,
		DefaultRuntime:   "cloud_sandbox",
		AllowLocalSystem: true,
	}.ResolveProvider(ProviderRequest{SessionID: "sesn_000001"})
	if _, ok := provider.(capability.LocalSystemProvider); !ok {
		t.Fatalf("expected local provider, got %T", provider)
	}
}

func TestSessionProviderResolverUsesExplicitToolRuntimePolicy(t *testing.T) {
	store := fakeSessionStore{session: managedagents.Session{
		ID:              "sesn_000001",
		RuntimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
	}}
	provider := SessionProviderResolver{
		Store:          store,
		DefaultRuntime: ToolRuntimeCloudSandbox,
	}.ResolveProvider(ProviderRequest{
		SessionID:   "sesn_000001",
		ToolRuntime: ToolRuntimeLocalSystem,
	})
	if unavailable, ok := provider.(capability.UnavailableProvider); !ok || unavailable.Runtime != ToolRuntimeLocalSystem {
		t.Fatalf("expected explicit local_system to be unavailable by default, got %#v", provider)
	}
}

type fakeSessionStore struct {
	session managedagents.Session
}

func (s fakeSessionStore) GetSession(string) (managedagents.Session, error) {
	return s.session, nil
}
