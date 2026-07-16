package skills

import (
	"strings"
	"testing"
)

func TestBindRuntimeDirectoriesRewritesDirectExecPaths(t *testing.T) {
	result := ResolveResult{Skills: []ResolvedSkill{{
		Skill: Skill{Identifier: "web-access"}, Version: Version{Version: 2},
		Rendered:        "Run node ${CLAUDE_SKILL_DIR}/scripts/check-deps.mjs then read $TMA_SKILL_DIR/config.env.",
		EstimatedTokens: 10,
	}}}
	bound, err := BindRuntimeDirectories(result, map[string]string{"web-access": "/workspace/.tma/skills/web-access/2"})
	if err != nil {
		t.Fatalf("bind runtime directories: %v", err)
	}
	content := string(bound.Rendered)
	if strings.Contains(content, "SKILL_DIR") || !strings.Contains(content, "/workspace/.tma/skills/web-access/2/scripts/check-deps.mjs") || !strings.Contains(content, "do not search server or home directories") {
		t.Fatalf("unexpected bound skill context: %s", content)
	}
	if bound.EstimatedTokens <= 0 || bound.Skills[0].EstimatedTokens <= 0 {
		t.Fatalf("expected rebound token estimates, got %#v", bound)
	}
}

func TestBindRuntimeDirectoriesAdaptsDesktopBrowserSkills(t *testing.T) {
	result := ResolveResult{Skills: []ResolvedSkill{{
		Skill: Skill{Identifier: "web-access"}, Version: Version{Version: 2, ContentText: "Run check-deps.mjs before cdp-proxy.mjs."},
		Rendered: "Run check-deps.mjs before cdp-proxy.mjs.", EstimatedTokens: 10,
	}}}
	bound, err := BindRuntimeDirectories(result, map[string]string{"web-access": "/workspace/.tma/skills/web-access/2"})
	if err != nil {
		t.Fatalf("bind runtime directories: %v", err)
	}
	content := string(bound.Rendered)
	if !strings.Contains(content, "Use the registered browser.* tools") || !strings.Contains(content, "Do not run host-browser discovery") {
		t.Fatalf("expected native browser compatibility instructions, got %s", content)
	}
}
