package biographyvoice

import (
	"strings"
	"unicode/utf8"
)

const biographySpeechPaceInstruction = "整体语速约为正常对话的80%，咬字清楚，句间停顿稍长"

func withBiographySpeechPace(expression string) string {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return biographySpeechPaceInstruction
	}
	if strings.Contains(expression, biographySpeechPaceInstruction) {
		return expression
	}
	return expression + "；" + biographySpeechPaceInstruction
}

func stableSpeechChunks(text string, consumed int, flush bool) ([]string, int) {
	if consumed < 0 || consumed > len(text) || !utf8.ValidString(text[:consumed]) {
		return nil, consumed
	}
	suffix := text[consumed:]
	start := 0
	lastBoundary := 0
	runesSinceBoundary := 0
	chunks := make([]string, 0, 2)
	for offset, current := range suffix {
		runesSinceBoundary++
		end := offset + utf8.RuneLen(current)
		boundary := strings.ContainsRune("。！？!?；;\n", current)
		if !boundary && strings.ContainsRune("，,", current) && runesSinceBoundary >= 18 {
			boundary = true
		}
		if !boundary {
			continue
		}
		if chunk := strings.TrimSpace(suffix[start:end]); chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = end
		lastBoundary = end
		runesSinceBoundary = 0
	}
	if flush && start < len(suffix) {
		if chunk := strings.TrimSpace(suffix[start:]); chunk != "" {
			chunks = append(chunks, chunk)
		}
		lastBoundary = len(suffix)
	}
	return chunks, consumed + lastBoundary
}
