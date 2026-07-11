package agentruntime

import (
	"encoding/json"
	"strings"
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
	System                  string                              // Agent 系统提示词
	PinnedContext           string                              // 固定上下文，不随历史截断或 summary 覆盖
	SummaryText             string                              // 历史对话摘要（由上游压缩产生）
	History                 []managedagents.ConversationMessage // 按 seq 升序排列的会话历史
	UserPayload             json.RawMessage                     // 当前轮用户消息的原始 JSON payload
	Tools                   json.RawMessage                     // AgentConfigVersion.tools
	ModelTools              []llm.Tool                          // 原生 function calling schema
	Skills                  json.RawMessage                     // AgentConfigVersion.skills
	ContextWindowTokens     int                                 // 模型总上下文窗口；仅用于上报预算
	InputBudgetRatioPercent int                                 // 输入预算比例；仅用于上报预算
	ReservedOutputTokens    int                                 // 输出预留 token；仅用于上报预算
}

// ContextBuildResult 描述组装后的 messages 及截断元信息，供运行时记录与调试。
type ContextBuildResult struct {
	Messages                   []llm.Message // 最终送入 LLM 的消息列表
	PinnedContextIncluded      bool          // 是否注入固定上下文
	SummaryMessageIncluded     bool          // 是否注入了摘要 system 消息
	HistoryMessageCount        int           // 实际保留的历史消息条数
	OmittedHistoryMessageCount int           // 因 token 预算被丢弃的历史消息条数
	OmittedHistoryUntilSeq     int64         // 被丢弃区间上界 seq（最早被省略的那条）
	EstimatedTokenCount        int           // 估算的总输入 token 数
	Budget                     ContextBudgetBreakdown
	Truncated                  bool // 是否发生了历史截断
}

// DefaultContextBuilder 是 ContextBuilder 的默认实现，支持按 MaxInputTokens 截断历史。
type DefaultContextBuilder struct {
	MaxInputTokens int // 输入 token 上限；<=0 表示不限制
}

type ContextBudgetBreakdown struct {
	ContextWindowTokens     int `json:"context_window_tokens,omitempty"`
	InputBudgetRatioPercent int `json:"input_budget_ratio_percent,omitempty"`
	MaxInputTokens          int `json:"max_input_tokens,omitempty"`
	ReservedOutputTokens    int `json:"reserved_output_tokens,omitempty"`
	EstimatedTokenCount     int `json:"estimated_token_count"`
	MessageTokens           int `json:"message_tokens,omitempty"`
	ToolSchemaTokens        int `json:"tool_schema_tokens,omitempty"`
	ToolSchemaCount         int `json:"tool_schema_count,omitempty"`
	SystemTokens            int `json:"system_tokens,omitempty"`
	PinnedContextTokens     int `json:"pinned_context_tokens,omitempty"`
	SummaryTokens           int `json:"summary_tokens,omitempty"`
	ToolsTokens             int `json:"tools_tokens,omitempty"`
	SkillsTokens            int `json:"skills_tokens,omitempty"`
	HistoryTokens           int `json:"history_tokens,omitempty"`
	CurrentUserTokens       int `json:"current_user_tokens,omitempty"`
	OmittedHistoryTokens    int `json:"omitted_history_tokens,omitempty"`
}

// historyContextMessage 在截断算法中同时保留 seq，便于上报被省略区间的边界。
type historyContextMessage struct {
	Seq     int64
	TurnID  string
	Message llm.Message
}

type historyContextGroup struct {
	StartSeq int64
	EndSeq   int64
	TurnID   string
	Messages []llm.Message
	Tokens   int
}

const latestUserMessagePolicy = `Latest user message policy:
- Treat the newest user message as the highest-priority instruction for this turn.
- If it changes the goal or asks a separate question, stop following earlier unfinished plans and respond to the newest request instead.
- Do not continue prior desktop, browser, file, or tool actions unless the newest user message clearly asks you to continue them.`

// Build 将 system、摘要、历史与当前用户消息组装为 LLM messages。
// 消息顺序：system → pinned context → 摘要 system → 历史（user/assistant 交替）→ 当前 user。
// 历史中会跳过非 user/assistant 角色及空文本内容。
func (builder DefaultContextBuilder) Build(request ContextBuildRequest) (ContextBuildResult, error) {
	// 前缀固定包含 system 与可选的摘要，截断时始终保留。
	prefix := make([]llm.Message, 0, 1)
	systemTokens := 0
	pinnedContextTokens := 0
	summaryTokens := 0
	toolsTokens := 0
	skillsTokens := 0
	toolSchemaTokens := estimateToolsTokens(request.ModelTools)
	if request.System != "" {
		message := llm.Message{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: buildSystemPrompt(request.System),
			}},
		}
		systemTokens = estimateMessageTokens(message)
		prefix = append(prefix, message)
	}
	if request.PinnedContext != "" {
		message := llm.Message{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "Pinned context:\n" + request.PinnedContext,
			}},
		}
		pinnedContextTokens = estimateMessageTokens(message)
		prefix = append(prefix, message)
	}
	if request.SummaryText != "" {
		message := llm.Message{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "Conversation summary:\n" + request.SummaryText,
			}},
		}
		summaryTokens = estimateMessageTokens(message)
		prefix = append(prefix, message)
	}
	var appended bool
	prefix, appended = appendContextJSON(prefix, "Available tools", request.Tools)
	if appended {
		toolsTokens = estimateMessageTokens(prefix[len(prefix)-1])
	}
	prefix, appended = appendContextJSON(prefix, "Available skills", request.Skills)
	if appended {
		skillsTokens = estimateMessageTokens(prefix[len(prefix)-1])
	}

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
			Seq:    history.Seq,
			TurnID: messageTurnID(history.Payload),
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
	messages, omittedHistory, omittedUntilSeq := builder.applyBudget(prefix, historyMessages, currentUserMessage, toolSchemaTokens)
	includedHistoryTokens, omittedHistoryTokens := splitHistoryTokens(historyMessages, omittedHistory)
	messageTokens := estimateMessagesTokens(messages)
	estimatedTokenCount := messageTokens + toolSchemaTokens
	return ContextBuildResult{
		Messages:                   messages,
		PinnedContextIncluded:      request.PinnedContext != "",
		SummaryMessageIncluded:     request.SummaryText != "",
		HistoryMessageCount:        len(historyMessages) - omittedHistory,
		OmittedHistoryMessageCount: omittedHistory,
		OmittedHistoryUntilSeq:     omittedUntilSeq,
		EstimatedTokenCount:        estimatedTokenCount,
		Budget: ContextBudgetBreakdown{
			ContextWindowTokens:     request.ContextWindowTokens,
			InputBudgetRatioPercent: request.InputBudgetRatioPercent,
			MaxInputTokens:          builder.MaxInputTokens,
			ReservedOutputTokens:    request.ReservedOutputTokens,
			EstimatedTokenCount:     estimatedTokenCount,
			MessageTokens:           messageTokens,
			ToolSchemaTokens:        toolSchemaTokens,
			ToolSchemaCount:         len(request.ModelTools),
			SystemTokens:            systemTokens,
			PinnedContextTokens:     pinnedContextTokens,
			SummaryTokens:           summaryTokens,
			ToolsTokens:             toolsTokens,
			SkillsTokens:            skillsTokens,
			HistoryTokens:           includedHistoryTokens,
			CurrentUserTokens:       estimateMessageTokens(currentUserMessage),
			OmittedHistoryTokens:    omittedHistoryTokens,
		},
		Truncated: omittedHistory > 0,
	}, nil
}

func buildSystemPrompt(system string) string {
	trimmed := strings.TrimSpace(system)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, latestUserMessagePolicy) {
		return trimmed
	}
	return trimmed + "\n\n" + latestUserMessagePolicy
}

// applyBudget 在 token 预算内从最近的历史消息向前选取，保证 prefix 与当前 user 始终保留。
// 返回：最终 messages、被省略的历史条数、最早被省略消息的 seq（无截断时为 0）。
func (builder DefaultContextBuilder) applyBudget(prefix []llm.Message, history []historyContextMessage, current llm.Message, reservedInputTokens int) ([]llm.Message, int, int64) {
	if builder.MaxInputTokens <= 0 {
		messages := make([]llm.Message, 0, len(prefix)+len(history)+1)
		messages = append(messages, prefix...)
		for _, historyMessage := range history {
			messages = append(messages, historyMessage.Message)
		}
		messages = append(messages, current)
		return messages, 0, 0
	}

	groups := groupHistoryMessages(history)
	// 从最新 turn 组向旧 turn 组遍历，selected 暂存为「新→旧」顺序。
	selected := make([]historyContextGroup, 0, len(groups))
	currentBudget := estimateMessagesTokens(prefix) + estimateMessageTokens(current) + reservedInputTokens
	for index := len(groups) - 1; index >= 0; index-- {
		if currentBudget+groups[index].Tokens > builder.MaxInputTokens {
			omitted, omittedUntilSeq := omittedGroupMetadata(groups, index)
			return contextMessages(prefix, selected, current), omitted, omittedUntilSeq
		}
		currentBudget += groups[index].Tokens
		selected = append(selected, groups[index])
	}
	return contextMessages(prefix, selected, current), 0, 0
}

// contextMessages 拼接 prefix、历史与当前 user，并将历史恢复为时间正序。
func contextMessages(prefix []llm.Message, selectedNewestFirst []historyContextGroup, current llm.Message) []llm.Message {
	reverseGroups(selectedNewestFirst)
	historyCount := 0
	for _, group := range selectedNewestFirst {
		historyCount += len(group.Messages)
	}
	messages := make([]llm.Message, 0, len(prefix)+historyCount+1)
	messages = append(messages, prefix...)
	for _, group := range selectedNewestFirst {
		messages = append(messages, group.Messages...)
	}
	messages = append(messages, current)
	return messages
}

func groupHistoryMessages(history []historyContextMessage) []historyContextGroup {
	groups := make([]historyContextGroup, 0, len(history))
	for _, message := range history {
		if message.TurnID == "" || len(groups) == 0 || groups[len(groups)-1].TurnID != message.TurnID {
			groups = append(groups, historyContextGroup{
				StartSeq: message.Seq,
				EndSeq:   message.Seq,
				TurnID:   message.TurnID,
				Messages: []llm.Message{message.Message},
				Tokens:   estimateMessageTokens(message.Message),
			})
			continue
		}
		group := &groups[len(groups)-1]
		group.EndSeq = message.Seq
		group.Messages = append(group.Messages, message.Message)
		group.Tokens += estimateMessageTokens(message.Message)
	}
	return groups
}

func omittedGroupMetadata(groups []historyContextGroup, lastOmittedIndex int) (int, int64) {
	omitted := 0
	for index := 0; index <= lastOmittedIndex; index++ {
		omitted += len(groups[index].Messages)
	}
	return omitted, groups[lastOmittedIndex].EndSeq
}

func estimateMessagesTokens(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateMessageTokens(message)
	}
	return total
}

func estimateToolsTokens(modelTools []llm.Tool) int {
	if len(modelTools) == 0 {
		return 0
	}
	encoded, err := json.Marshal(modelTools)
	if err != nil {
		return 0
	}
	return estimateTextTokens(string(encoded))
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

func reverseGroups(groups []historyContextGroup) {
	for left, right := 0, len(groups)-1; left < right; left, right = left+1, right-1 {
		groups[left], groups[right] = groups[right], groups[left]
	}
}

func splitHistoryTokens(history []historyContextMessage, omitted int) (int, int) {
	includedTokens := 0
	omittedTokens := 0
	for index, historyMessage := range history {
		tokens := estimateMessageTokens(historyMessage.Message)
		if index < omitted {
			omittedTokens += tokens
			continue
		}
		includedTokens += tokens
	}
	return includedTokens, omittedTokens
}

// messageContent 从会话 payload JSON 中提取首个非空 text 片段，包装为 LLM ContentPart。
func messageContent(payload json.RawMessage) []llm.ContentPart {
	return []llm.ContentPart{{
		Type: "text",
		Text: firstTextContent(payload),
	}}
}

func messageTurnID(payload json.RawMessage) string {
	var object struct {
		TurnID string `json:"turn_id"`
	}
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	return object.TurnID
}

func appendContextJSON(messages []llm.Message, label string, raw json.RawMessage) ([]llm.Message, bool) {
	text := tools.PrettyJSON(raw)
	if text == "" {
		return messages, false
	}
	return append(messages, llm.Message{
		Role: "system",
		Content: []llm.ContentPart{{
			Type: "text",
			Text: label + ":\n" + text,
		}},
	}), true
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
