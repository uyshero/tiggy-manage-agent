package agentruntime

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"tiggy-manage-agent/internal/llm"
)

const (
	skillMutationCompletionValidator = "builtin.skill_mutation_execution"
	skillsInstallToolName            = "skills_install"
	skillsEnableToolName             = "skills_enable"
	skillsDisableToolName            = "skills_disable"
)

var (
	skillInstallClaimPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:skill\s*)?(?:已|已经|成功)(?:安装|更新|升级|创建|发布)`),
		regexp.MustCompile(`(?i)(?:安装|更新|升级|创建|发布)(?:成功|完成)`),
		regexp.MustCompile(`(?i)\b(?:skill\s+)?(?:installed|updated|upgraded|created|published)\b`),
	}
	skillEnableClaimPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:skill\s*)?(?:已|已经|成功|当前)(?:启用|激活|生效)`),
		regexp.MustCompile(`(?i)(?:启用|激活)(?:成功|完成)`),
		regexp.MustCompile(`(?i)\b(?:skill\s+)?(?:enabled|activated)\b`),
	}
	skillDisableClaimPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:skill\s*)?(?:已|已经|成功|当前)(?:禁用|停用)`),
		regexp.MustCompile(`(?i)(?:禁用|停用)(?:成功|完成)`),
		regexp.MustCompile(`(?i)\b(?:skill\s+)?disabled\b`),
	}
)

// SkillMutationCompletionGate prevents a response from claiming a registry or
// binding mutation unless the matching tool succeeded during the current turn.
type SkillMutationCompletionGate struct{}

func (SkillMutationCompletionGate) Validate(_ context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	claims := claimedSkillMutationTools(visibleAssistantText(candidate.Response.Message))
	if len(claims) == 0 {
		return skillMutationCompletionPass(nil, nil), nil
	}

	succeeded := successfulSkillMutationTools(candidate.ToolExecutions)
	missing := make([]string, 0, len(claims))
	for _, name := range claims {
		if _, ok := succeeded[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return skillMutationCompletionPass(claims, mapKeysSorted(succeeded)), nil
	}

	return CompletionVerdict{
		Outcome:   CompletionOutcomeRetry,
		Validator: skillMutationCompletionValidator,
		Reason:    "the response claims a Skill mutation without a matching successful tool execution",
		Feedback:  "Do not claim that a Skill was installed, updated, enabled, or disabled unless the matching skills_install, skills_enable, or skills_disable call succeeded in this turn. Execute the required Skill workflow now if authorized and available; otherwise report the exact blocker or current state without claiming success. If the response is correcting an earlier false claim, state the correction plainly without repeating the old success claim as a standalone status. This is internal validation feedback: do not mention the completion gate, validator, or retry mechanism to the user.",
		Evidence: map[string]any{
			"claimed_skill_mutations":    claims,
			"successful_skill_mutations": mapKeysSorted(succeeded),
			"missing_skill_mutations":    missing,
		},
	}, nil
}

func skillMutationCompletionPass(claims, succeeded []string) CompletionVerdict {
	return CompletionVerdict{
		Outcome:   CompletionOutcomePass,
		Validator: skillMutationCompletionValidator,
		Evidence: map[string]any{
			"claimed_skill_mutations":    claims,
			"successful_skill_mutations": succeeded,
		},
	}
}

func visibleAssistantText(message llm.Message) string {
	parts := make([]string, 0, len(message.Content))
	for _, part := range message.Content {
		if (part.Type == "" || part.Type == "text") && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func claimedSkillMutationTools(text string) []string {
	claimed := map[string]struct{}{}
	segments := strings.FieldsFunc(text, func(value rune) bool {
		switch value {
		case '\n', '\r', '。', '！', '？', '；', ';', '，', ',':
			return true
		default:
			return false
		}
	})
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" || skillClaimSegmentIsNegated(segment) {
			continue
		}
		if matchesAnySkillClaim(segment, skillInstallClaimPatterns) {
			claimed[skillsInstallToolName] = struct{}{}
		}
		if matchesAnySkillClaim(segment, skillEnableClaimPatterns) {
			claimed[skillsEnableToolName] = struct{}{}
		}
		if matchesAnySkillClaim(segment, skillDisableClaimPatterns) {
			claimed[skillsDisableToolName] = struct{}{}
		}
	}
	return mapKeysSorted(claimed)
}

func matchesAnySkillClaim(segment string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(segment) {
			return true
		}
	}
	return false
}

func skillClaimSegmentIsNegated(segment string) bool {
	lower := strings.ToLower(segment)
	for _, marker := range []string{
		"没有", "并未", "并没有", "尚未", "未安装", "未更新", "未升级", "未创建", "未发布", "未启用", "未激活", "未禁用",
		"无法", "不能", "不存在", "错误", "声称", "口头", "之前回复", "我回复", "需要", "希望", "是否", "准备", "将会", "将要", "尝试",
		"用于", "用来", "可以用", "工具说明", "文档", "示例",
		"not installed", "not updated", "not upgraded", "not enabled", "not disabled", "didn't", "did not", "failed", "unable", "need to", "will ", "would ", "used to", "can be used", "example",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func successfulSkillMutationTools(executions []CompletionToolExecution) map[string]struct{} {
	succeeded := map[string]struct{}{}
	for _, execution := range executions {
		name := strings.ToLower(strings.TrimSpace(execution.Name))
		if execution.IsError || !strings.EqualFold(strings.TrimSpace(execution.Status), "succeeded") {
			continue
		}
		switch name {
		case skillsInstallToolName, skillsEnableToolName, skillsDisableToolName:
			succeeded[name] = struct{}{}
		}
	}
	return succeeded
}

func mapKeysSorted(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
