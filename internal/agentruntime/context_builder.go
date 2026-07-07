package agentruntime

import (
	"encoding/json"
	"unicode/utf8"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

// ContextBuilder 负责把 Session 上下文转换成 LLM messages。
// 后续 token budget、历史截断、summary、多模态上下文都应该收敛到这里。
type ContextBuilder interface {
	Build(request ContextBuildRequest) (ContextBuildResult, error)
}

// ContextBuildRequest 是组装 LLM 上下文所需的原始输入。
type ContextBuildRequest struct {
	System      string                              // Agent 系统提示词
	SummaryText string                              // 历史对话摘要（由上游压缩产生）
	History     []managedagents.ConversationMessage // 按 seq 升序排列的会话历史
	UserPayload json.RawMessage                     // 当前轮用户消息的原始 JSON payload
	Tools       json.RawMessage                     // AgentConfigVersion.tools
	Skills      json.RawMessage                     // AgentConfigVersion.skills
}

// ContextBuildResult 描述组装后的 messages 及截断元信息，供运行时记录与调试。
type ContextBuildResult struct {
	Messages                   []llm.Message // 最终送入 LLM 的消息列表
	SummaryMessageIncluded     bool          // 是否注入了摘要 system 消息
	HistoryMessageCount        int           // 实际保留的历史消息条数
	OmittedHistoryMessageCount int           // 因 token 预算被丢弃的历史消息条数
	OmittedHistoryUntilSeq     int64         // 被丢弃区间上界 seq（最早被省略的那条）
	EstimatedTokenCount        int           // 估算的总输入 token 数
	Truncated                  bool          // 是否发生了历史截断
}

// DefaultContextBuilder 是 ContextBuilder 的默认实现，支持按 MaxInputTokens 截断历史。
type DefaultContextBuilder struct {
	MaxInputTokens int // 输入 token 上限；<=0 表示不限制
}

// historyContextMessage 在截断算法中同时保留 seq，便于上报被省略区间的边界。
type historyContextMessage struct {
	Seq     int64
	Message llm.Message
}

// Build 将 system、摘要、历史与当前用户消息组装为 LLM messages。
// 消息顺序：system → 摘要 system → 历史（user/assistant 交替）→ 当前 user。
// 历史中会跳过非 user/assistant 角色及空文本内容。
func (builder DefaultContextBuilder) Build(request ContextBuildRequest) (ContextBuildResult, error) {
	// 前缀固定包含 system 与可选的摘要，截断时始终保留。
	prefix := make([]llm.Message, 0, 1)
	if request.System != "" {
		prefix = append(prefix, llm.Message{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: request.System,
			}},
		})
	}
	if request.SummaryText != "" {
		prefix = append(prefix, llm.Message{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "Conversation summary:\n" + request.SummaryText,
			}},
		})
	}
	prefix = appendContextJSON(prefix, "Available tools", request.Tools)
	prefix = appendContextJSON(prefix, "Available skills", request.Skills)

	historyMessages := make([]historyContextMessage, 0, len(request.History))
	for _, history := range request.History {
		if history.Role != "user" && history.Role != "assistant" {
			continue
		}
		content := messageContent(history.Payload)
		if len(content) == 0 || content[0].Text == "" {
			continue
		}
		historyMessages = append(historyMessages, historyContextMessage{
			Seq: history.Seq,
			Message: llm.Message{
				Role:    history.Role,
				Content: content,
			},
		})
	}

	currentUserMessage := llm.Message{
		Role:    "user",
		Content: messageContent(request.UserPayload),
	}
	messages, omittedHistory, omittedUntilSeq := builder.applyBudget(prefix, historyMessages, currentUserMessage)
	return ContextBuildResult{
		Messages:                   messages,
		SummaryMessageIncluded:     request.SummaryText != "",
		HistoryMessageCount:        len(historyMessages) - omittedHistory,
		OmittedHistoryMessageCount: omittedHistory,
		OmittedHistoryUntilSeq:     omittedUntilSeq,
		EstimatedTokenCount:        estimateMessagesTokens(messages),
		Truncated:                  omittedHistory > 0,
	}, nil
}

// applyBudget 在 token 预算内从最近的历史消息向前选取，保证 prefix 与当前 user 始终保留。
// 返回：最终 messages、被省略的历史条数、最早被省略消息的 seq（无截断时为 0）。
func (builder DefaultContextBuilder) applyBudget(prefix []llm.Message, history []historyContextMessage, current llm.Message) ([]llm.Message, int, int64) {
	if builder.MaxInputTokens <= 0 {
		messages := make([]llm.Message, 0, len(prefix)+len(history)+1)
		messages = append(messages, prefix...)
		for _, historyMessage := range history {
			messages = append(messages, historyMessage.Message)
		}
		messages = append(messages, current)
		return messages, 0, 0
	}

	// 从最新消息向旧消息遍历，selected 暂存为「新→旧」顺序。
	selected := make([]llm.Message, 0, len(history))
	currentBudget := estimateMessagesTokens(prefix) + estimateMessageTokens(current)
	for index := len(history) - 1; index >= 0; index-- {
		messageTokens := estimateMessageTokens(history[index].Message)
		if currentBudget+messageTokens > builder.MaxInputTokens {
			omitted := index + 1
			return contextMessages(prefix, selected, current), omitted, history[index].Seq
		}
		currentBudget += messageTokens
		selected = append(selected, history[index].Message)
	}
	return contextMessages(prefix, selected, current), 0, 0
}

// contextMessages 拼接 prefix、历史与当前 user，并将历史恢复为时间正序。
func contextMessages(prefix []llm.Message, selectedNewestFirst []llm.Message, current llm.Message) []llm.Message {
	reverseMessages(selectedNewestFirst)
	messages := make([]llm.Message, 0, len(prefix)+len(selectedNewestFirst)+1)
	messages = append(messages, prefix...)
	messages = append(messages, selectedNewestFirst...)
	messages = append(messages, current)
	return messages
}

func estimateMessagesTokens(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateMessageTokens(message)
	}
	return total
}

// estimateMessageTokens 用粗粒度启发式估算单条消息的 token 数（非精确 tokenizer）。
// 固定开销 4 覆盖 role/结构开销，文本按 rune 数 ÷4 向上取整。
func estimateMessageTokens(message llm.Message) int {
	total := 4
	for _, part := range message.Content {
		if part.Type != "text" {
			continue
		}
		total += estimateTextTokens(part.Text)
	}
	return total
}

func estimateTextTokens(text string) int {
	runes := utf8.RuneCountInString(text)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func reverseMessages(messages []llm.Message) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

// messageContent 从会话 payload JSON 中提取首个非空 text 片段，包装为 LLM ContentPart。
func messageContent(payload json.RawMessage) []llm.ContentPart {
	return []llm.ContentPart{{
		Type: "text",
		Text: firstTextContent(payload),
	}}
}

func appendContextJSON(messages []llm.Message, label string, raw json.RawMessage) []llm.Message {
	text := tools.PrettyJSON(raw)
	if text == "" {
		return messages
	}
	return append(messages, llm.Message{
		Role: "system",
		Content: []llm.ContentPart{{
			Type: "text",
			Text: label + ":\n" + text,
		}},
	})
}

// firstTextContent 解析 {"content":[{"type":"text","text":"..."}]} 结构，返回第一个有效文本。
func firstTextContent(payload json.RawMessage) string {
	var object struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	for _, content := range object.Content {
		if content.Type == "text" && content.Text != "" {
			return content.Text
		}
	}
	return ""
}
