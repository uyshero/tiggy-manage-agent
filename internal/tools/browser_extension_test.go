package tools

import (
	"encoding/json"
	"testing"
)

func TestDefaultRegistryExcludesBuiltInBrowser(t *testing.T) {
	if _, ok := DefaultRegistry().Get(NamespaceBrowser); ok {
		t.Fatal("browser must be supplied by a worker plugin, not the default registry")
	}
}

func TestConfiguredRegistryIgnoresUnregisteredBrowserTools(t *testing.T) {
	registry, policy := DefaultRegistry().Configured(json.RawMessage(`{"enabled_tools":["browser","browser_open"]}`))
	if !policy.Explicit {
		t.Fatal("expected an explicit tool policy")
	}
	if _, ok := registry.Get(NamespaceBrowser); ok {
		t.Fatal("unregistered browser namespace must not enter the configured registry")
	}
	for _, identifier := range platformDefaultToolIdentifiers {
		if _, ok := registry.Get(identifier); !ok {
			t.Fatalf("platform default namespace %q must remain enabled", identifier)
		}
	}
	for _, modelTool := range registry.ModelTools() {
		if modelTool.Function.Name == NamespaceBrowser+"_open" {
			t.Fatal("unregistered browser tool must not be callable")
		}
	}
}

func TestBrowserNamespaceIsAvailableToProcessPlugins(t *testing.T) {
	manifest := Manifest{
		Identifier: NamespaceBrowser,
		Type:       "process_plugin",
		API: []API{{
			Name:           "open",
			APIName:        "open",
			Description:    "Open a URL through an extension browser.",
			Parameters:     json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
			Risk:           ToolRiskRead,
			Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeLocalSystem}, Preferred: ToolRuntimeLocalSystem},
			Implementation: ToolImplementationWorkerCapability,
		}},
	}
	if err := validatePluginManifest(manifest); err != nil {
		t.Fatalf("browser process plugin manifest rejected: %v", err)
	}
}
