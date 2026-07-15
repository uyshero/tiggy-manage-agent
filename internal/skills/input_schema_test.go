package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

var reviewInputsSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "additionalProperties":false,
  "properties":{
    "style":{"type":"string","enum":["strict","balanced"]},
    "max_findings":{"type":"integer","minimum":1,"maximum":20},
    "include_tests":{"type":"boolean"}
  },
  "required":["style"]
}`)

func TestValidateInputsSchemaAndInputs(t *testing.T) {
	if err := ValidateInputsSchema(reviewInputsSchema); err != nil {
		t.Fatalf("validate inputs schema: %v", err)
	}
	if err := ValidateInputs(reviewInputsSchema, json.RawMessage(`{"style":"strict","max_findings":5,"include_tests":true}`)); err != nil {
		t.Fatalf("validate skill inputs: %v", err)
	}
	err := ValidateInputs(reviewInputsSchema, json.RawMessage(`{"style":"do-not-log-this-value"}`))
	if err == nil || !strings.Contains(err.Error(), "/style") || !strings.Contains(err.Error(), "enum") {
		t.Fatalf("expected path-only validation error, got %v", err)
	}
	if strings.Contains(err.Error(), "do-not-log-this-value") {
		t.Fatalf("validation error leaked input value: %v", err)
	}
}

func TestValidateInputsSchemaRejectsNetworkSecretsAndOpenObjects(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name:   "remote ref",
			schema: `{"type":"object","additionalProperties":false,"properties":{"profile":{"$ref":"https://schemas.example/profile.json"}}}`,
			want:   "local fragment",
		},
		{
			name:   "absolute id",
			schema: `{"$id":"https://schemas.example/skill.json","type":"object","additionalProperties":false}`,
			want:   "must not declare $id",
		},
		{
			name:   "dynamic ref",
			schema: `{"type":"object","additionalProperties":false,"properties":{"profile":{"$dynamicRef":"#profile"}}}`,
			want:   "must not declare $dynamicRef",
		},
		{
			name:   "secret field",
			schema: `{"type":"object","additionalProperties":false,"properties":{"token":{"type":"string","writeOnly":true}}}`,
			want:   "managed environment variables",
		},
		{
			name:   "sensitive extension",
			schema: `{"type":"object","additionalProperties":false,"properties":{"token":{"type":"string","x-tma-sensitive":true}}}`,
			want:   "managed environment variables",
		},
		{
			name:   "password format",
			schema: `{"type":"object","additionalProperties":false,"properties":{"token":{"type":"string","format":"password"}}}`,
			want:   "managed environment variables",
		},
		{
			name:   "open object",
			schema: `{"type":"object","properties":{"style":{"type":"string"}}}`,
			want:   "additionalProperties",
		},
		{
			name:   "open nullable object",
			schema: `{"type":"object","additionalProperties":false,"properties":{"profile":{"type":["object","null"],"properties":{"name":{"type":"string"}}}}}`,
			want:   "additionalProperties",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateInputsSchema(json.RawMessage(test.schema))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q rejection, got %v", test.want, err)
			}
		})
	}
}

func TestValidateInputsSchemaAllowsLocalReferences(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"$defs":{"style":{"type":"string","enum":["strict","balanced"]}},
		"properties":{"style":{"$ref":"#/$defs/style"}},
		"required":["style"]
	}`)
	if err := ValidateInputs(schema, json.RawMessage(`{"style":"strict"}`)); err != nil {
		t.Fatalf("validate local schema reference: %v", err)
	}
}

func TestValidateInputsSchemaAndInputsEnforceByteLimits(t *testing.T) {
	oversizedSchema := json.RawMessage(`{"type":"object","additionalProperties":false,"description":"` + strings.Repeat("x", MaxInputsSchemaBytes) + `"}`)
	if err := ValidateInputsSchema(oversizedSchema); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("expected schema byte limit rejection, got %v", err)
	}
	oversizedInputs := json.RawMessage(`{"value":"` + strings.Repeat("x", MaxSkillInputsBytes) + `"}`)
	if err := ValidateInputs(nil, oversizedInputs); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("expected inputs byte limit rejection, got %v", err)
	}
}

func TestValidateInputsPreservesLegacyObjectBehavior(t *testing.T) {
	if err := ValidateInputs(nil, json.RawMessage(`{"style":"custom"}`)); err != nil {
		t.Fatalf("legacy object inputs should remain valid: %v", err)
	}
	if err := ValidateInputs(nil, json.RawMessage(`["not-an-object"]`)); err == nil {
		t.Fatal("expected legacy non-object inputs rejection")
	}
}

func TestResolveRegistryValidatesFrozenVersionInputs(t *testing.T) {
	registry := staticRegistry{
		skill: Skill{ID: "skl_schema", WorkspaceID: "wksp_default", Identifier: "review-schema", Title: "Review Schema", Status: StatusActive},
		version: Version{
			ID: "sklv_schema", SkillID: "skl_schema", Version: 1, ContentText: "Review carefully.",
			Manifest: json.RawMessage(`{"inputs_schema":` + string(reviewInputsSchema) + `}`),
		},
	}
	invalid := json.RawMessage(`{"enabled":[{"skill":"review-schema","version":1,"inputs":{"style":"unknown"}}]}`)
	if _, err := ResolveRegistry(context.Background(), registry, "wksp_default", invalid, 1000); err == nil || !strings.Contains(err.Error(), "/style") {
		t.Fatalf("expected resolver input validation failure, got %v", err)
	}
	valid := json.RawMessage(`{"enabled":[{"skill":"review-schema","version":1,"inputs":{"style":"balanced"}}]}`)
	result, err := ResolveRegistry(context.Background(), registry, "wksp_default", valid, 1000)
	if err != nil || len(result.Skills) != 1 || result.Skills[0].Status != UsageResolved {
		t.Fatalf("expected resolved schema-bound skill, result=%#v err=%v", result, err)
	}
}
