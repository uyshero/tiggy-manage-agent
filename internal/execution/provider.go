package execution

import (
	"encoding/json"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/tools"
)

const (
	ToolRuntimeAuto         = tools.ToolRuntimeAuto
	ToolRuntimeCloudSandbox = tools.ToolRuntimeCloudSandbox
	ToolRuntimeLocalSystem  = tools.ToolRuntimeLocalSystem
)

// ProviderResolver 负责按 session 选择最终执行能力面。
// 现在先支持静态本地 provider，后续可替换成 worker pool / remote dispatcher。
type ProviderResolver interface {
	ResolveProvider(request ProviderRequest) capability.Provider
}

type ProviderRequest struct {
	WorkspaceID   string
	OwnerID       string
	SessionID     string
	EnvironmentID string
	ToolRuntime   string
}

// StaticProviderResolver 固定返回同一个 provider。
type StaticProviderResolver struct {
	Provider capability.Provider
}

func (r StaticProviderResolver) ResolveProvider(ProviderRequest) capability.Provider {
	if r.Provider != nil {
		return r.Provider
	}
	return SessionProviderResolver{}.ResolveProvider(ProviderRequest{})
}

type SessionStore interface {
	GetSession(id string) (managedagents.Session, error)
}

type scopedSessionStore interface {
	GetSessionScoped(id string, scope managedagents.AccessScope) (managedagents.Session, error)
}

type SessionProviderResolver struct {
	Store                      SessionStore
	SessionDataStore           capability.SessionDataStore
	ObjectStore                objectstore.Client
	DefaultRuntime             string
	CloudSandboxRoot           string
	CloudSandboxImage          string
	CloudSandboxDataRoot       string
	CloudSandboxDataTTL        time.Duration
	CloudSandboxDisableNetwork bool
	CloudSandboxContainers     *capability.OnlyboxesContainerManager
	AllowLocalSystem           bool
}

func (r SessionProviderResolver) ResolveProvider(request ProviderRequest) capability.Provider {
	settings := r.defaultRuntimeSettings()
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	ownerID := strings.TrimSpace(request.OwnerID)
	if r.Store != nil && request.SessionID != "" {
		var session managedagents.Session
		var err error
		if scoped, ok := r.Store.(scopedSessionStore); ok && workspaceID != "" {
			session, err = scoped.GetSessionScoped(request.SessionID, managedagents.AccessScope{WorkspaceID: workspaceID, OwnerID: ownerID})
		} else {
			session, err = r.Store.GetSession(request.SessionID)
		}
		if err == nil {
			settings = MergeRuntimeSettings(settings, session.RuntimeSettings)
			workspaceID = strings.TrimSpace(session.WorkspaceID)
			ownerID = strings.TrimSpace(session.OwnerID)
		}
	}
	settings = MergeRuntimePolicy(settings, RuntimePolicy{Runtime: request.ToolRuntime})
	if settings.Runtime == ToolRuntimeLocalSystem {
		if !r.AllowLocalSystem {
			return capability.UnavailableProvider{
				Runtime: ToolRuntimeLocalSystem,
				Reason:  "no matching local_system worker and server-local fallback is disabled",
			}
		}
		return capability.LocalSystemProvider{}
	}
	root := settings.Root
	if root == "" {
		root = r.CloudSandboxRoot
	}
	return capability.OnlyboxesProvider{
		Image:            settings.Image,
		WorkspaceRoot:    root,
		IsolateWorkspace: true,
		DataRoot:         r.CloudSandboxDataRoot,
		DataDirTTL:       r.CloudSandboxDataTTL,
		DisableNetwork:   !settings.Network,
		WorkspaceID:      workspaceID,
		OwnerID:          ownerID,
		SessionID:        request.SessionID,
		Store:            r.sessionDataStore(),
		ObjectStore:      r.ObjectStore,
		ContainerManager: r.CloudSandboxContainers,
	}
}

func (r SessionProviderResolver) sessionDataStore() capability.SessionDataStore {
	if r.SessionDataStore != nil {
		return r.SessionDataStore
	}
	if store, ok := r.Store.(capability.SessionDataStore); ok {
		return store
	}
	return nil
}

func (r SessionProviderResolver) defaultRuntimeSettings() RuntimeSettings {
	runtime, ok := tools.NormalizeToolRuntime(r.DefaultRuntime)
	if !ok {
		runtime = ToolRuntimeAuto
	}
	if runtime == ToolRuntimeAuto {
		runtime = ToolRuntimeCloudSandbox
	}
	return RuntimeSettings{
		Runtime: runtime,
		Root:    strings.TrimSpace(r.CloudSandboxRoot),
		Image:   strings.TrimSpace(r.CloudSandboxImage),
		Network: !r.CloudSandboxDisableNetwork,
	}
}

type RuntimeSettings struct {
	Runtime string
	Root    string
	Image   string
	Network bool
}

type RuntimePolicy struct {
	Runtime string
}

func MergeRuntimePolicy(base RuntimeSettings, policy RuntimePolicy) RuntimeSettings {
	settings := base
	if runtime, ok := tools.NormalizeToolRuntime(policy.Runtime); ok && strings.TrimSpace(policy.Runtime) != "" {
		settings.Runtime = runtime
		if settings.Runtime == ToolRuntimeAuto {
			settings.Runtime = ToolRuntimeCloudSandbox
		}
	}
	return settings
}

func ParseRuntimeSettings(raw json.RawMessage) RuntimeSettings {
	return MergeRuntimeSettings(RuntimeSettings{Runtime: ToolRuntimeCloudSandbox, Network: true}, raw)
}

func MergeRuntimeSettings(base RuntimeSettings, raw json.RawMessage) RuntimeSettings {
	settings := base
	if settings.Runtime == "" {
		settings.Runtime = ToolRuntimeCloudSandbox
	}
	if len(raw) == 0 || string(raw) == "null" {
		return settings
	}
	var decoded struct {
		Runtime *string `json:"tool_runtime"`
		Root    *string `json:"cloud_sandbox_root"`
		Image   *string `json:"cloud_sandbox_image"`
		Network *bool   `json:"cloud_sandbox_allow_network"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return settings
	}
	if decoded.Runtime != nil {
		if runtime, ok := tools.NormalizeToolRuntime(*decoded.Runtime); ok {
			settings.Runtime = runtime
			if settings.Runtime == ToolRuntimeAuto {
				settings.Runtime = ToolRuntimeCloudSandbox
			}
		}
	}
	if decoded.Root != nil {
		settings.Root = strings.TrimSpace(*decoded.Root)
	}
	if decoded.Image != nil {
		settings.Image = strings.TrimSpace(*decoded.Image)
	}
	if decoded.Network != nil {
		settings.Network = *decoded.Network
	}
	return settings
}
