package agentruntime

import (
	"context"
	"regexp"
	"strings"

	"tiggy-manage-agent/internal/llm"
)

const (
	imageGenerationCompletionValidator = "builtin.image_generation_execution"
	imageGenerateToolName              = "image_generate"
)

var (
	chineseImageGenerationPattern = regexp.MustCompile(`(?i)(帮我|给我|请|替我|为我|现在|马上|立即)?(画|绘制|生成|制作|创建|设计|重绘|修图|出图)(一|个|只|张|幅|套|些|下|一下|这|那|图片|图像|插画|海报|照片|头像|封面|logo|图标)`) //nolint:lll
	chineseDirectImagePattern     = regexp.MustCompile(`(?i)^(请)?(帮我|给我|替我|为我)?(现在|马上|立即)?(画|绘制|生成|制作|创建|设计|重绘|修图|出图)`)                                                 //nolint:lll
	englishImageGenerationPattern = regexp.MustCompile(`(?i)\b(draw|generate|create|make|render|redraw|edit)\b.{0,80}\b(image|picture|photo|illustration|poster|logo|icon|artwork|elephant)\b`)
)

// ImageGenerationCompletionGate prevents a model from promising image work
// without attempting the image tool during the current user turn.
type ImageGenerationCompletionGate struct{}

func (ImageGenerationCompletionGate) Validate(_ context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	if !containsString(candidate.ActiveTools, imageGenerateToolName) {
		return imageGenerationCompletionPass(false, false), nil
	}
	userIndex, userText := latestUserMessage(candidate.Messages)
	if userIndex < 0 || !requestsImageGeneration(userText) {
		return imageGenerationCompletionPass(false, false), nil
	}
	attempted := hasImageGenerateCall(candidate.Messages[userIndex+1:]) || hasImageGenerateCall([]llm.Message{candidate.Response.Message})
	if attempted {
		return imageGenerationCompletionPass(true, true), nil
	}
	return CompletionVerdict{
		Outcome:   CompletionOutcomeRetry,
		Validator: imageGenerationCompletionValidator,
		Reason:    "the user requested image generation but the model completed without calling image_generate",
		Feedback:  "The user requested an image generation or edit. Call the available image_generate function now with a suitable exact use_case and the user's request. Do not promise future action, claim that an image was created, or produce a text-only completion before the tool has been attempted. After the tool result, report only the actual result or blocker. This is internal validation feedback: do not mention the completion gate, validator, or retry mechanism to the user.",
		Evidence: map[string]any{
			"image_generation_intent":  true,
			"image_generate_attempted": false,
		},
	}, nil
}

func imageGenerationCompletionPass(intent bool, attempted bool) CompletionVerdict {
	return CompletionVerdict{
		Outcome:   CompletionOutcomePass,
		Validator: imageGenerationCompletionValidator,
		Evidence: map[string]any{
			"image_generation_intent":  intent,
			"image_generate_attempted": attempted,
		},
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}

func latestUserMessage(messages []llm.Message) (int, string) {
	for index := len(messages) - 1; index >= 0; index-- {
		if !strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			continue
		}
		parts := make([]string, 0, len(messages[index].Content))
		for _, part := range messages[index].Content {
			if (part.Type == "" || part.Type == "text") && strings.TrimSpace(part.Text) != "" {
				parts = append(parts, strings.TrimSpace(part.Text))
			}
		}
		return index, strings.Join(parts, "\n")
	}
	return -1, ""
}

func hasImageGenerateCall(messages []llm.Message) bool {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if strings.EqualFold(strings.TrimSpace(call.Function.Name), imageGenerateToolName) {
				return true
			}
		}
	}
	return false
}

func requestsImageGeneration(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	for _, informational := range []string{
		"画图工具", "图片工具", "图像工具", "会画图", "能画图", "怎么画", "如何画", "怎样画",
		"是否支持画", "支持画图吗", "画图叫什么", "画图叫啥", "draw a conclusion",
	} {
		if strings.Contains(lower, informational) {
			return false
		}
	}
	for _, followUp := range []string{"画了吗", "画了没", "生成了吗", "生成了没", "开始画", "现在画", "马上画", "立即画"} {
		if strings.Contains(lower, followUp) {
			return true
		}
	}
	if chineseImageGenerationPattern.MatchString(value) || chineseDirectImagePattern.MatchString(value) || englishImageGenerationPattern.MatchString(value) {
		return true
	}
	return (strings.Contains(lower, "来一张") || strings.Contains(lower, "来张")) &&
		(strings.Contains(lower, "图") || strings.Contains(lower, "照片") || strings.Contains(lower, "海报"))
}
