package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func Resolve(raw json.RawMessage) (ResolveResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return ResolveResult{}, nil
	}
	config, ok := NormalizeConfig(raw)
	if !ok {
		return ResolveResult{Rendered: cloneRaw(raw), LegacyPassthrough: true}, nil
	}
	sortEnabled(config.Enabled)
	rendered, err := RenderContextJSON(config)
	if err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{Config: config, Rendered: rendered}, nil
}

func ResolveRegistry(ctx context.Context, registry Registry, workspaceID string, raw json.RawMessage, maxTokens int) (ResolveResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	config, err := ValidateConfig(raw)
	if err != nil {
		return ResolveResult{}, err
	}
	if registry == nil && len(config.Enabled) > 0 {
		return ResolveResult{}, fmt.Errorf("skill registry is unavailable")
	}
	sortEnabled(config.Enabled)
	result := ResolveResult{Config: config}
	for index, enabled := range config.Enabled {
		var skill Skill
		var getErr error
		if enabled.SkillID != "" {
			skill, getErr = registry.GetSkill(ctx, enabled.SkillID)
		} else {
			skill, getErr = registry.GetSkillByIdentifier(ctx, workspaceID, enabled.Skill)
		}
		if getErr != nil {
			return ResolveResult{}, fmt.Errorf("resolve skill %s: %w", enabled.Skill, getErr)
		}
		if workspaceID == "" {
			workspaceID = skill.WorkspaceID
		}
		if skill.WorkspaceID != workspaceID || skill.Identifier != enabled.Skill {
			return ResolveResult{}, fmt.Errorf("resolve skill %s: pinned skill_id does not match workspace or identifier", enabled.Skill)
		}
		enabled.SkillID = skill.ID
		result.Config.Enabled[index].SkillID = skill.ID
		version, getErr := registry.GetSkillVersion(ctx, skill.ID, enabled.Version)
		if getErr != nil {
			return ResolveResult{}, fmt.Errorf("resolve skill %s version %d: %w", enabled.Skill, enabled.Version, getErr)
		}
		if validateErr := ValidateVersionInputs(version, enabled.Inputs); validateErr != nil {
			return ResolveResult{}, fmt.Errorf("resolve skill %s version %d: %w", enabled.Skill, enabled.Version, validateErr)
		}
		remainingTokens := -1
		if maxTokens > 0 {
			remainingTokens = maxTokens - result.EstimatedTokens
		}
		resolved, renderErr := renderWithinBudget(skill, version, enabled, remainingTokens)
		if renderErr != nil {
			return ResolveResult{}, fmt.Errorf("render skill %s: %w", enabled.Skill, renderErr)
		}
		if resolved.Status == UsageSkipped || resolved.Status == UsageDegraded {
			result.Truncated = true
		}
		result.EstimatedTokens += resolved.EstimatedTokens
		result.Skills = append(result.Skills, resolved)
	}
	var rendered []string
	for _, resolved := range result.Skills {
		if resolved.Rendered != "" {
			rendered = append(rendered, resolved.Rendered)
		}
	}
	if len(rendered) > 0 {
		result.Rendered, err = json.Marshal(map[string]any{
			"format":  "tma.skills.context.v1",
			"content": strings.Join(rendered, "\n\n"),
		})
		if err != nil {
			return ResolveResult{}, err
		}
	}
	return result, nil
}

func renderWithinBudget(skill Skill, version Version, enabled EnabledSkill, remainingTokens int) (ResolvedSkill, error) {
	resolved := ResolvedSkill{Skill: skill, Version: version, RequestedMode: enabled.Mode, Priority: enabled.Priority, Inputs: cloneRaw(enabled.Inputs)}
	modes := fallbackModes(enabled.Mode)
	for _, mode := range modes {
		rendered, err := RenderVersion(skill, version, mode, enabled.Inputs)
		if err != nil {
			return ResolvedSkill{}, err
		}
		tokens := estimateTokens(rendered)
		if remainingTokens < 0 || tokens <= remainingTokens {
			resolved.RenderedMode = mode
			resolved.Rendered = rendered
			resolved.EstimatedTokens = tokens
			resolved.Status = UsageResolved
			if mode != enabled.Mode {
				resolved.Status = UsageDegraded
			}
			return resolved, nil
		}
	}
	resolved.Status = UsageSkipped
	resolved.FailureReason = "skill context budget exceeded"
	return resolved, nil
}

func fallbackModes(mode string) []string {
	switch mode {
	case ModeFull:
		return []string{ModeFull, ModeSummary, ModeExamplesOnly}
	case ModeSummary:
		return []string{ModeSummary, ModeExamplesOnly}
	case ModeExamplesOnly:
		return []string{ModeExamplesOnly}
	default:
		return nil
	}
}

func sortEnabled(enabled []EnabledSkill) {
	sort.SliceStable(enabled, func(left int, right int) bool {
		if enabled[left].Priority == enabled[right].Priority {
			return enabled[left].Skill < enabled[right].Skill
		}
		return enabled[left].Priority > enabled[right].Priority
	})
}
