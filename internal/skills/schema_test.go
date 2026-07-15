package skills

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResolveNormalizesLegacyStringArray(t *testing.T) {
	result, err := Resolve(json.RawMessage(`["code-review","search"]`))
	if err != nil {
		t.Fatalf("resolve skills: %v", err)
	}

	if result.LegacyPassthrough {
		t.Fatal("expected normalized result, got legacy passthrough")
	}
	if len(result.Config.Enabled) != 2 {
		t.Fatalf("expected two enabled skills, got %+v", result.Config.Enabled)
	}
	if result.Config.Enabled[0].Skill != "code-review" || result.Config.Enabled[0].Mode != DefaultMode || result.Config.Enabled[0].Priority != DefaultPriority {
		t.Fatalf("unexpected first enabled skill: %+v", result.Config.Enabled[0])
	}
	if string(result.Rendered) != `{"enabled":[{"skill":"code-review","mode":"summary","priority":100},{"skill":"search","mode":"summary","priority":100}]}` {
		t.Fatalf("unexpected rendered config: %s", string(result.Rendered))
	}
}

func TestValidateConfigRequiresExactVersionsAndKnownFields(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"enabled":[{"skill":"code-review"}]}`),
		json.RawMessage(`{"enabled":[{"skill":"Code Review","version":1}]}`),
		json.RawMessage(`{"enabled":[{"skill":"code-review","version":1,"unknown":true}]}`),
		json.RawMessage(`{"skills":["code-review"]}`),
		json.RawMessage(`{"enabled":[]} {"enabled":[]}`),
	} {
		if _, err := ValidateConfig(raw); err == nil {
			t.Fatalf("expected strict validation failure for %s", raw)
		}
	}
	config, err := ValidateConfig(json.RawMessage(`{"enabled":[{"skill":"code-review","version":2,"inputs":{"style":"strict"}}]}`))
	if err != nil {
		t.Fatalf("validate canonical config: %v", err)
	}
	if config.Enabled[0].Mode != ModeSummary || config.Enabled[0].Priority != DefaultPriority {
		t.Fatalf("expected canonical defaults, got %#v", config.Enabled[0])
	}
}

func TestResolveRegistrySummaryDefersFullSkillContentToInspect(t *testing.T) {
	registry := staticRegistry{
		skill: Skill{ID: "skl_1", WorkspaceID: "wksp_default", Identifier: "code-review", Title: "Code Review", Status: StatusActive},
		version: Version{ID: "sklv_1", SkillID: "skl_1", Version: 2, ContentText: "FULL-SKILL-BODY", Manifest: json.RawMessage(`{
			"system_role":"Review carefully.",
			"blocks":[{"type":"constraint","content":"Do not invent evidence."}]
		}`)},
	}
	result, err := ResolveRegistry(context.Background(), registry, "wksp_default", json.RawMessage(`{"enabled":[{"skill":"code-review","version":2}]}`), 1000)
	if err != nil {
		t.Fatalf("resolve summary skill: %v", err)
	}
	rendered := string(result.Rendered)
	if result.Skills[0].RenderedMode != ModeSummary || !strings.Contains(rendered, "Review carefully.") || !strings.Contains(rendered, "skills.inspect") {
		t.Fatalf("expected summary with on-demand guidance, got %#v rendered=%s", result.Skills[0], rendered)
	}
	if strings.Contains(rendered, "FULL-SKILL-BODY") {
		t.Fatalf("expected full SKILL.md body to stay out of summary context: %s", rendered)
	}
}

func TestResolveRegistryRendersFrozenVersionAndDegradesToBudget(t *testing.T) {
	registry := staticRegistry{
		skill: Skill{ID: "skl_1", WorkspaceID: "wksp_default", Identifier: "code-review", Title: "Code Review", Status: StatusActive},
		version: Version{ID: "sklv_1", SkillID: "skl_1", Version: 2, ContentText: "A long full instruction body that should not fit.", Manifest: json.RawMessage(`{
			"system_role":"Review carefully.",
			"blocks":[{"type":"example","title":"Example","content":"P1 file:line impact"}]
		}`)},
	}
	raw := json.RawMessage(`{"enabled":[{"skill":"code-review","version":2,"mode":"full","priority":200}]}`)
	full, err := ResolveRegistry(context.Background(), registry, "wksp_default", raw, 1000)
	if err != nil {
		t.Fatalf("resolve full: %v", err)
	}
	if len(full.Skills) != 1 || full.Skills[0].RenderedMode != ModeFull || full.Skills[0].Version.Version != 2 {
		t.Fatalf("unexpected full result: %#v", full)
	}
	limited, err := ResolveRegistry(context.Background(), registry, "wksp_default", raw, 1)
	if err != nil {
		t.Fatalf("resolve limited: %v", err)
	}
	if !limited.Truncated || limited.Skills[0].Status != UsageSkipped || len(limited.Rendered) != 0 {
		t.Fatalf("expected budget skip, got %#v", limited)
	}
}

type staticRegistry struct {
	skill   Skill
	version Version
}

func (registry staticRegistry) CreateSkill(context.Context, CreateSkillInput) (Skill, error) {
	return Skill{}, errors.New("unsupported")
}
func (registry staticRegistry) GetSkill(context.Context, string) (Skill, error) {
	return registry.skill, nil
}
func (registry staticRegistry) GetSkillByIdentifier(_ context.Context, _ string, identifier string) (Skill, error) {
	if identifier != registry.skill.Identifier {
		return Skill{}, errors.New("not found")
	}
	return registry.skill, nil
}
func (registry staticRegistry) ListSkills(context.Context, ListSkillsInput) ([]Skill, error) {
	return []Skill{registry.skill}, nil
}
func (registry staticRegistry) ArchiveSkill(context.Context, string) (Skill, error) {
	return Skill{}, errors.New("unsupported")
}
func (registry staticRegistry) CreateSkillVersion(context.Context, CreateVersionInput) (Version, error) {
	return Version{}, errors.New("unsupported")
}
func (registry staticRegistry) GetSkillVersion(_ context.Context, _ string, version int) (Version, error) {
	if version != registry.version.Version {
		return Version{}, errors.New("not found")
	}
	return registry.version, nil
}
func (registry staticRegistry) ListSkillVersions(context.Context, string) ([]Version, error) {
	return []Version{registry.version}, nil
}

func TestResolveNormalizesCanonicalConfigAndSortsByPriority(t *testing.T) {
	result, err := Resolve(json.RawMessage(`{
		"enabled": [
			{"skill":"repo-conventions","version":2,"mode":"summary","priority":50},
			{"skill":"code-review","version":3,"inputs":{"review_style":"strict"},"priority":200}
		]
	}`))
	if err != nil {
		t.Fatalf("resolve skills: %v", err)
	}

	if len(result.Config.Enabled) != 2 {
		t.Fatalf("expected two enabled skills, got %+v", result.Config.Enabled)
	}
	if result.Config.Enabled[0].Skill != "code-review" || result.Config.Enabled[0].Version != 3 || result.Config.Enabled[0].Mode != DefaultMode || result.Config.Enabled[0].Priority != 200 {
		t.Fatalf("unexpected first normalized skill: %+v", result.Config.Enabled[0])
	}
	if result.Config.Enabled[1].Skill != "repo-conventions" || result.Config.Enabled[1].Mode != ModeSummary {
		t.Fatalf("unexpected second normalized skill: %+v", result.Config.Enabled[1])
	}
}

func TestResolveFallsBackToLegacyPassthroughForUnsupportedObject(t *testing.T) {
	raw := json.RawMessage(`{"code_review":{"enabled":true}}`)
	result, err := Resolve(raw)
	if err != nil {
		t.Fatalf("resolve skills: %v", err)
	}

	if !result.LegacyPassthrough {
		t.Fatal("expected legacy passthrough")
	}
	if string(result.Rendered) != string(raw) {
		t.Fatalf("expected raw passthrough, got %s", string(result.Rendered))
	}
}

func TestResolveFallsBackToLegacyPassthroughForInvalidJSON(t *testing.T) {
	raw := json.RawMessage(`not-json`)
	result, err := Resolve(raw)
	if err != nil {
		t.Fatalf("resolve skills: %v", err)
	}

	if !result.LegacyPassthrough {
		t.Fatal("expected invalid json to passthrough")
	}
	if string(result.Rendered) != string(raw) {
		t.Fatalf("expected invalid raw passthrough, got %s", string(result.Rendered))
	}
}
