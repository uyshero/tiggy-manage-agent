package tools

import (
	"encoding/json"
	"testing"
)

func TestDefaultRegistryModelToolParametersAreValidJSON(t *testing.T) {
	for _, tool := range DefaultRegistry().ModelTools() {
		if !json.Valid(tool.Function.Parameters) {
			t.Errorf("tool %s has invalid parameters JSON: %s", tool.Function.Name, tool.Function.Parameters)
		}
	}
}
