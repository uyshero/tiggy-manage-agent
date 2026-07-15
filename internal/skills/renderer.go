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
	if mode != ModeExamplesOnly && manifest.SystemRole != "" {
		fmt.Fprintf(&builder, "\n%s\n", manifest.SystemRole)
	}
	if mode == ModeFull && strings.TrimSpace(version.ContentText) != "" {
		fmt.Fprintf(&builder, "\n%s\n", strings.TrimSpace(version.ContentText))
	}
	if mode == ModeSummary && strings.TrimSpace(version.ContentText) != "" {
		fmt.Fprintf(&builder, "\nFull SKILL.md instructions are available on demand. Call skills.inspect with identifier %q and version %d before applying details not covered by this summary.\n", skill.Identifier, version.Version)
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
		fmt.Fprintln(&builder, "\nPackage assets available through skills.read_asset:")
		for _, file := range bundle.Files {
			if file.Executable {
				fmt.Fprintf(&builder, "- %s (%d bytes; script reference text, not auto-executable)\n", file.Path, file.Size)
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
