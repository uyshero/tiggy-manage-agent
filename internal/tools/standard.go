package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	WorkProtocolVersion = "tma.work.v1"

	NamespaceDefault     = "default"
	NamespaceArtifact    = "artifact"
	NamespaceBrowser     = "browser"
	NamespaceAgent       = "agent"
	NamespaceInteraction = "interaction"
	NamespaceTask        = "task"
	NamespaceSkills      = "skills"
	NamespaceWeb         = "web"

	ToolRuntimeAuto         = "auto"
	ToolRuntimeCloudSandbox = "cloud_sandbox"
	ToolRuntimeLocalSystem  = "local_system"

	ToolImplementationServerBuiltin    = "server_builtin"
	ToolImplementationWorkerCapability = "worker_capability"

	ToolRiskRead  = "read"
	ToolRiskWrite = "write"
	ToolRiskExec  = "exec"

	CapabilityExec            = "exec"
	CapabilityCodeExecute     = "code.execute"
	CapabilityNetworkHTTP     = "network.http"
	CapabilityBrowserOpen     = "browser.open"
	CapabilityBrowserRead     = "browser.read"
	CapabilityBrowserInteract = "browser.interact"
	CapabilityBrowserCapture  = "browser.capture"
	CapabilityBrowserTakeover = "browser.takeover"
	CapabilityBrowserClose    = "browser.close"
)

type RuntimePolicy struct {
	Allowed   []string `json:"allowed,omitempty"`
	Preferred string   `json:"preferred,omitempty"`
}

type WorkInvocation struct {
	ProtocolVersion     string          `json:"protocol_version"`
	Namespace           string          `json:"namespace"`
	API                 string          `json:"api"`
	Capabilities        []string        `json:"capabilities,omitempty"`
	Risk                string          `json:"risk,omitempty"`
	Runtime             string          `json:"runtime,omitempty"`
	Input               json.RawMessage `json:"input,omitempty"`
	EnvironmentEnvelope string          `json:"environment_envelope,omitempty"`
}

func NormalizeToolRuntime(value string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", ToolRuntimeAuto:
		return ToolRuntimeAuto, true
	case ToolRuntimeCloudSandbox:
		return ToolRuntimeCloudSandbox, true
	case ToolRuntimeLocalSystem:
		return ToolRuntimeLocalSystem, true
	default:
		return "", false
	}
}

func NormalizeToolRisk(value string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case ToolRiskRead:
		return ToolRiskRead, true
	case ToolRiskWrite:
		return ToolRiskWrite, true
	case ToolRiskExec:
		return ToolRiskExec, true
	default:
		return "", false
	}
}

func NormalizeToolNamespace(value string) (string, bool) {
	namespace := strings.TrimSpace(strings.ToLower(value))
	switch namespace {
	case NamespaceDefault:
		return NamespaceDefault, true
	case NamespaceArtifact:
		return NamespaceArtifact, true
	case NamespaceBrowser:
		return NamespaceBrowser, true
	case NamespaceAgent:
		return NamespaceAgent, true
	case NamespaceInteraction:
		return NamespaceInteraction, true
	case NamespaceTask:
		return NamespaceTask, true
	case NamespaceWeb:
		return NamespaceWeb, true
	case ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem:
		return "", false
	default:
		if isValidToolNamespace(namespace) {
			return namespace, true
		}
		return "", false
	}
}

func isValidToolNamespace(value string) bool {
	if value == "" {
		return false
	}
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if !isValidToolIdentifier(part) {
			return false
		}
	}
	return true
}

func isValidToolIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !valid {
			return false
		}
		if index == 0 && !((r >= 'a' && r <= 'z') || r == '_') {
			return false
		}
	}
	return true
}

func fallbackString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func NormalizeToolImplementation(value string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return "", true
	case ToolImplementationServerBuiltin:
		return ToolImplementationServerBuiltin, true
	case ToolImplementationWorkerCapability:
		return ToolImplementationWorkerCapability, true
	default:
		return "", false
	}
}

func NormalizeRuntimePolicy(policy *RuntimePolicy) RuntimePolicy {
	if policy == nil {
		return RuntimePolicy{
			Allowed:   []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem},
			Preferred: ToolRuntimeAuto,
		}
	}
	normalized := RuntimePolicy{}
	seen := map[string]bool{}
	for _, value := range policy.Allowed {
		runtime, ok := NormalizeToolRuntime(value)
		if !ok || seen[runtime] {
			continue
		}
		seen[runtime] = true
		normalized.Allowed = append(normalized.Allowed, runtime)
	}
	if len(normalized.Allowed) == 0 {
		normalized.Allowed = []string{ToolRuntimeAuto}
		seen[ToolRuntimeAuto] = true
	}
	preferred, ok := NormalizeToolRuntime(policy.Preferred)
	if !ok {
		preferred = ToolRuntimeAuto
	}
	if !seen[preferred] {
		normalized.Allowed = append(normalized.Allowed, preferred)
	}
	normalized.Preferred = preferred
	return normalized
}

func ValidateWorkInvocation(work WorkInvocation) error {
	if strings.TrimSpace(work.ProtocolVersion) == "" {
		work.ProtocolVersion = WorkProtocolVersion
	}
	if work.ProtocolVersion != WorkProtocolVersion {
		return fmt.Errorf("unsupported work protocol version %q", work.ProtocolVersion)
	}
	if _, ok := NormalizeToolNamespace(work.Namespace); !ok {
		return fmt.Errorf("unsupported tool namespace %q", work.Namespace)
	}
	if strings.TrimSpace(work.API) == "" {
		return fmt.Errorf("work api is required")
	}
	if work.Risk != "" {
		if _, ok := NormalizeToolRisk(work.Risk); !ok {
			return fmt.Errorf("unsupported tool risk %q", work.Risk)
		}
	}
	if _, ok := NormalizeToolRuntime(work.Runtime); !ok {
		return fmt.Errorf("unsupported tool runtime %q", work.Runtime)
	}
	return nil
}
