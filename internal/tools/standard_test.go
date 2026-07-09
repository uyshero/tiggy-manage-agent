package tools

import (
	"encoding/json"
	"testing"
)

func TestNormalizeToolRuntime(t *testing.T) {
	cases := map[string]string{
		"":              ToolRuntimeAuto,
		"AUTO":          ToolRuntimeAuto,
		"cloud_sandbox": ToolRuntimeCloudSandbox,
		"local_system":  ToolRuntimeLocalSystem,
	}
	for input, expected := range cases {
		got, ok := NormalizeToolRuntime(input)
		if !ok || got != expected {
			t.Fatalf("NormalizeToolRuntime(%q) = %q, %v; want %q, true", input, got, ok, expected)
		}
	}
	if got, ok := NormalizeToolRuntime("server"); ok || got != "" {
		t.Fatalf("expected server to be rejected as a first-version runtime, got %q, %v", got, ok)
	}
}

func TestNormalizeRuntimePolicyDefaultsAndFilters(t *testing.T) {
	defaulted := NormalizeRuntimePolicy(nil)
	if defaulted.Preferred != ToolRuntimeAuto || len(defaulted.Allowed) != 3 {
		t.Fatalf("unexpected default runtime policy: %#v", defaulted)
	}

	normalized := NormalizeRuntimePolicy(&RuntimePolicy{
		Allowed:   []string{"cloud_sandbox", "server", "cloud_sandbox"},
		Preferred: "local_system",
	})
	if normalized.Preferred != ToolRuntimeLocalSystem {
		t.Fatalf("unexpected preferred runtime: %#v", normalized)
	}
	if len(normalized.Allowed) != 2 || normalized.Allowed[0] != ToolRuntimeCloudSandbox || normalized.Allowed[1] != ToolRuntimeLocalSystem {
		t.Fatalf("unexpected allowed runtimes: %#v", normalized)
	}
}

func TestValidateWorkInvocation(t *testing.T) {
	work := WorkInvocation{
		ProtocolVersion: WorkProtocolVersion,
		Namespace:       NamespaceBrowser,
		API:             "screenshot",
		Capabilities:    []string{"browser.read", "browser.capture"},
		Risk:            ToolRiskRead,
		Runtime:         ToolRuntimeAuto,
		Input:           json.RawMessage(`{"url":"https://example.com"}`),
	}
	if err := ValidateWorkInvocation(work); err != nil {
		t.Fatalf("expected valid work invocation: %v", err)
	}

	work.Runtime = "server"
	if err := ValidateWorkInvocation(work); err == nil {
		t.Fatal("expected server runtime to be rejected")
	}

	work.Runtime = ToolRuntimeAuto
	work.Namespace = "local_system"
	if err := ValidateWorkInvocation(work); err == nil {
		t.Fatal("expected implementation namespace to be rejected")
	}
}

func TestRegistryWorkInvocationUsesManifestMetadata(t *testing.T) {
	input := json.RawMessage(`{"path":"README.md"}`)
	invocation, ok := DefaultRegistry().WorkInvocation(DefaultIdentifier, "read_file", ToolRuntimeLocalSystem, input)
	if !ok {
		t.Fatal("expected read_file invocation")
	}
	if invocation.ProtocolVersion != WorkProtocolVersion || invocation.Namespace != NamespaceDefault || invocation.API != "read_file" {
		t.Fatalf("unexpected invocation identity: %#v", invocation)
	}
	if invocation.Runtime != ToolRuntimeLocalSystem || invocation.Risk != ToolRiskRead {
		t.Fatalf("expected runtime/risk from request and manifest, got %#v", invocation)
	}
	if len(invocation.Capabilities) != 1 || invocation.Capabilities[0] != CapabilityFilesystemRead {
		t.Fatalf("expected manifest capabilities, got %#v", invocation.Capabilities)
	}
	if string(invocation.Input) != string(input) {
		t.Fatalf("expected input to be preserved, got %s", string(invocation.Input))
	}
}

func TestDecodeWorkerCapabilities(t *testing.T) {
	capabilities, err := DecodeWorkerCapabilities(json.RawMessage(`{
		"namespaces": ["default"],
		"apis": ["default.read_file"],
		"runtimes": ["local_system"],
		"capabilities": ["filesystem.read"],
		"constraints": {"network": "disabled"}
	}`))
	if err != nil {
		t.Fatalf("decode worker capabilities: %v", err)
	}
	if len(capabilities.Namespaces) != 1 || capabilities.Namespaces[0] != NamespaceDefault {
		t.Fatalf("unexpected namespaces: %#v", capabilities.Namespaces)
	}
	if len(capabilities.APIs) != 1 || capabilities.APIs[0] != "default.read_file" {
		t.Fatalf("unexpected apis: %#v", capabilities.APIs)
	}
	if len(capabilities.Runtimes) != 1 || capabilities.Runtimes[0] != ToolRuntimeLocalSystem {
		t.Fatalf("unexpected runtimes: %#v", capabilities.Runtimes)
	}
	if capabilities.Constraints["network"] != "disabled" {
		t.Fatalf("unexpected constraints: %#v", capabilities.Constraints)
	}
	if _, err := DecodeWorkerCapabilities(nil); err == nil {
		t.Fatal("expected empty worker capabilities to fail")
	}
}

func TestAPISupportsStandardManifestFields(t *testing.T) {
	api := API{
		Namespace:      NamespaceArtifact,
		APIName:        "create",
		Name:           "create",
		Capabilities:   []string{"artifact.metadata.write"},
		Risk:           ToolRiskWrite,
		Runtime:        &RuntimePolicy{Preferred: ToolRuntimeAuto, Allowed: []string{ToolRuntimeAuto}},
		Implementation: ToolImplementationServerBuiltin,
	}
	encoded, err := json.Marshal(api)
	if err != nil {
		t.Fatalf("marshal api: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode api: %v", err)
	}
	if decoded["namespace"] != NamespaceArtifact || decoded["api"] != "create" || decoded["implementation"] != ToolImplementationServerBuiltin {
		t.Fatalf("standard manifest fields missing from JSON: %s", encoded)
	}
}
