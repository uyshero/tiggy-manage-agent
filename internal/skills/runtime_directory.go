package skills

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BindRuntimeDirectories makes package-backed instructions executable in a
// sandbox where tool arguments are passed directly instead of through a shell.
func BindRuntimeDirectories(result ResolveResult, directories map[string]string) (ResolveResult, error) {
	if len(directories) == 0 || len(result.Skills) == 0 {
		return result, nil
	}
	result.EstimatedTokens = 0
	rendered := make([]string, 0, len(result.Skills))
	for index := range result.Skills {
		resolved := &result.Skills[index]
		directory := strings.TrimSpace(directories[resolved.Skill.ID])
		if directory == "" {
			directory = strings.TrimSpace(directories[resolved.Skill.Identifier])
		}
		if directory != "" && resolved.Rendered != "" {
			replacer := strings.NewReplacer(
				"${CLAUDE_SKILL_DIR}", directory,
				"$CLAUDE_SKILL_DIR", directory,
				"${TMA_SKILL_DIR}", directory,
				"$TMA_SKILL_DIR", directory,
			)
			runtimeNote := ""
			if requiresNativeBrowserAdapter(resolved.Version.ContentText) {
				runtimeNote = "TMA browser compatibility: this sandbox has no desktop Chrome/Edge binary. Do not run host-browser discovery or CDP proxy scripts and do not search ~/.claude or server directories. Use the registered browser_* tools for browser navigation, reading, interaction, and capture; use web_* tools for search and crawl.\n"
			}
			resolved.Rendered = fmt.Sprintf(
				"%s\nRuntime package directory: %s\nUse this exact directory for package files; do not search server or home directories.\n%s",
				replacer.Replace(resolved.Rendered), directory, runtimeNote,
			)
			resolved.EstimatedTokens = estimateTokens(resolved.Rendered)
		}
		result.EstimatedTokens += resolved.EstimatedTokens
		if resolved.Rendered != "" {
			rendered = append(rendered, resolved.Rendered)
		}
	}
	if len(rendered) == 0 {
		result.Rendered = nil
		return result, nil
	}
	encoded, err := json.Marshal(map[string]any{
		"format":  "tma.skills.context.v1",
		"content": strings.Join(rendered, "\n\n"),
	})
	if err != nil {
		return ResolveResult{}, err
	}
	result.Rendered = encoded
	return result, nil
}

func requiresNativeBrowserAdapter(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "check-deps.mjs") &&
		(strings.Contains(lower, "cdp-proxy") || strings.Contains(lower, "desktop browser"))
}
