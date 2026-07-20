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
