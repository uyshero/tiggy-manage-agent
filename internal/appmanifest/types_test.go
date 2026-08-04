package appmanifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateManifest(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: SchemaVersionV1,
		Revision:      "2026-07-31.1",
		Environments:  []EnvironmentSpec{{ExternalRef: "environment/default", Name: "Default", Config: json.RawMessage(`{}`)}},
		Agents: []AgentSpec{{
			ExternalRef: "agent/main", EnvironmentRef: "environment/default", Name: "Main",
			LLMModel: "fake-demo", System: "Assist", Tools: json.RawMessage(`{}`),
		}},
	}
	if err := Validate(manifest); err != nil {
		t.Fatal(err)
	}
	first, err := Checksum(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Environments[0].Config = json.RawMessage(`{ }`)
	second, err := Checksum(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("checksum changed for equivalent JSON: %s != %s", first, second)
	}
}

func TestValidateManifestRejectsDuplicateReferences(t *testing.T) {
	err := Validate(Manifest{
		SchemaVersion: SchemaVersionV1, Revision: "1",
		Environments: []EnvironmentSpec{
			{ExternalRef: "environment/default", Name: "One"},
			{ExternalRef: "environment/default", Name: "Two"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate external_ref error = %v", err)
	}
}
