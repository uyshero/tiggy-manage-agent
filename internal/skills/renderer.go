package skills

import (
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/tokenestimate"
)

func RenderContextJSON(config Config) (json.RawMessage, error) {
	if len(config.Enabled) == 0 {
		return nil, nil
	}
	return json.Marshal(config)
}

func RenderVersion(skill Skill, version Version, mode string, inputs json.RawMessage) (string, error) {
	manifest := Manifest{}
	if len(version.Manifest) > 0 && string(version.Manifest) != "null" {
		if err := json.Unmarshal(version.Manifest, &manifest); err != nil {
			return "", fmt.Errorf("decode skill manifest: %w", err)
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "## Skill: %s (version %d)\n", skill.Identifier, version.Version)
	if skill.Title != "" {
		fmt.Fprintf(&builder, "Title: %s\n", skill.Title)
	}
	if skill.Description != "" {
		fmt.Fprintf(&builder, "Description: %s\n", skill.Description)
	}
	if mode != ModeExamplesOnly {
		fmt.Fprintln(&builder, "\nSkill compliance policy: When the user's request matches this enabled Skill, its frozen-version instructions are binding, not optional guidance. Follow every required tool, step, validation, prohibition, and delivery contract. Do not replace a mandated workflow or tool with an alternative unless the Skill explicitly permits it or the user approves the deviation after you explain the blocker. If a required step cannot run, report the exact blocker and stop; never claim Skill-compliant success.")
	}
	if mode != ModeExamplesOnly && manifest.SystemRole != "" {
		fmt.Fprintf(&builder, "\n%s\n", manifest.SystemRole)
	}
	if mode == ModeFull && strings.TrimSpace(version.ContentText) != "" {
		fmt.Fprintf(&builder, "\n%s\n", strings.TrimSpace(version.ContentText))
	}
	if mode == ModeSummary && strings.TrimSpace(version.ContentText) != "" {
		fmt.Fprintf(&builder, "\nFull SKILL.md instructions are available on demand. Before taking any task action governed by this Skill, you MUST call skills_inspect with identifier %q and version %d and read every page until has_more is false.\n", skill.Identifier, version.Version)
	}
	for _, block := range manifest.Blocks {
		if !includeBlock(mode, block.Type) {
			continue
		}
		if block.Title != "" {
			fmt.Fprintf(&builder, "\n### %s\n", block.Title)
		}
		if block.Content != "" {
			fmt.Fprintf(&builder, "%s\n", block.Content)
		}
		for _, item := range block.Items {
			fmt.Fprintf(&builder, "- %s\n", item)
		}
	}
	if bundle, err := DecodeAssetBundle(version.Assets); err == nil && len(bundle.Files) > 0 {
		fmt.Fprintln(&builder, "\nPackage assets available through skills_read_asset:")
		for _, file := range bundle.Files {
			if file.Executable {
				fmt.Fprintf(&builder, "- %s (%d bytes; executable package script, requires a separate approved execution call)\n", file.Path, file.Size)
				continue
			}
			fmt.Fprintf(&builder, "- %s (%d bytes)\n", file.Path, file.Size)
		}
	}
	if len(inputs) > 0 && string(inputs) != "null" && mode != ModeExamplesOnly {
		fmt.Fprintf(&builder, "\nInputs: %s\n", inputs)
	}
	return strings.TrimSpace(builder.String()), nil
}

func includeBlock(mode string, blockType string) bool {
	switch mode {
	case ModeFull:
		return true
	case ModeSummary:
		return blockType == "instruction" || blockType == "constraint" || blockType == "checklist"
	case ModeExamplesOnly:
		return blockType == "example"
	default:
		return false
	}
}

func estimateTokens(value string) int {
	return tokenestimate.Text(value)
}
