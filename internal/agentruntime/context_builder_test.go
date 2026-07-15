package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestDefaultContextBuilderInjectsCurrentDateContextBeforeConversation(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	currentDateContext := buildCurrentDateContext(time.Date(2026, 7, 13, 16, 58, 19, 0, location))

	result, err := (DefaultContextBuilder{}).Build(ContextBuildRequest{
		System:             "system",
		CurrentDateContext: currentDateContext,
		UserPayload:        json.RawMessage(`{"content":[{"type":"text","text":"今天有什么新闻"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.CurrentDateContextIncluded || result.Budget.CurrentDateContextTokens == 0 {
		t.Fatalf("expected current date context metadata, got %+v", result)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected system, current date context and user message, got %#v", result.Messages)
	}
	assertMessage(t, result.Messages[1], "system", "Today's date is 2026-07-13.")
	assertMessage(t, result.Messages[2], "user", "今天有什么新闻")
}

func TestDefaultContextBuilderBuildsSystemHistoryAndCurrentUser(t *testing.T) {
	builder := DefaultContextBuilder{}

	result, err := builder.Build(ContextBuildRequest{
		System: "remember user facts",
		History: []managedagents.ConversationMessage{
			{
				Seq:     1,
				Role:    "user",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"my name is Alice"}]}`),
			},
			{
				Seq:     2,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"Nice to meet you, Alice."}]}`),
			},
		},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"what is my name?"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if result.HistoryMessageCount != 2 {
		t.Fatalf("expected 2 history messages, got %d", result.HistoryMessageCount)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "system", "remember user facts")
	assertMessageContains(t, result.Messages[0], "system", "Latest user message policy:")
	assertMessage(t, result.Messages[1], "user", "my name is Alice")
	assertMessage(t, result.Messages[2], "assistant", "Nice to meet you, Alice.")
	assertMessage(t, result.Messages[3], "user", "what is my name?")
}

func TestDefaultContextBuilderSkipsUnsupportedOrEmptyHistory(t *testing.T) {
	builder := DefaultContextBuilder{}

	result, err := builder.Build(ContextBuildRequest{
		History: []managedagents.ConversationMessage{
			{
				Seq:     1,
				Role:    "tool",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"tool output"}]}`),
			},
			{
				Seq:     2,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[]}`),
			},
			{
				Seq:     3,
				Role:    "user",
				Payload: json.RawMessage(`{"content":[{"type":"image","text":"ignored"}]}`),
			},
		},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if result.HistoryMessageCount != 0 {
		t.Fatalf("expected no history messages, got %d", result.HistoryMessageCount)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected only current user message, got %#v", result.Messages)
	}
	assertMessage(t, result.Messages[0], "user", "current")
}

func TestDefaultContextBuilderAddsUploadedFilesToModelContext(t *testing.T) {
	builder := DefaultContextBuilder{}

	result, err := builder.Build(ContextBuildRequest{
		UserPayload: json.RawMessage(`{
			"content":[{"type":"text","text":"summarize this report"}],
			"attachments":[{
				"artifact_id":"art_000001",
				"name":"quarterly report.pdf",
				"content_type":"application/pdf",
				"size_bytes":2048,
				"workspace_path":"/mnt/data/uploads/art_000001/quarterly_report.pdf"
			}]
		}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected one user message, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "user", "summarize this report")
	assertMessageContains(t, result.Messages[0], "user", "quarterly report.pdf: artifact_id=art_000001, workspace_path=/workspace/uploads/art_000001/quarterly_report.pdf")
	if strings.Contains(messagesText(result.Messages), "/mnt/data/uploads") {
		t.Fatalf("legacy upload path was not normalized: %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "user", "application/pdf, 2048 bytes")
}

func TestDefaultContextBuilderAddsOfflineSkillZIPCoordinates(t *testing.T) {
	result, err := (DefaultContextBuilder{}).Build(ContextBuildRequest{
		UserPayload: json.RawMessage(`{
			"content":[{"type":"text","text":"install this skill"}],
			"attachments":[{
				"artifact_id":"art_skill_zip",
				"name":"review-skill.zip",
				"content_type":"application/zip",
				"size_bytes":4096,
				"workspace_path":"/workspace/uploads/art_skill_zip/review-skill.zip"
			}]
		}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected one user message, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "user", "artifact_id=art_skill_zip")
	assertMessageContains(t, result.Messages[0], "user", "call skills.preview with source.provider=artifact")
	assertMessageContains(t, result.Messages[0], "user", "Never pass workspace_path")
}

func TestDefaultContextBuilderKeepsRecentHistoryWithinBudget(t *testing.T) {
	builder := DefaultContextBuilder{MaxInputTokens: 12}

	result, err := builder.Build(ContextBuildRequest{
		History: []managedagents.ConversationMessage{
			{
				Seq:     1,
				Role:    "user",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"old"}]}`),
			},
			{
				Seq:     2,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"recent"}]}`),
			},
		},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.Truncated {
		t.Fatal("expected context to be truncated")
	}
	if result.HistoryMessageCount != 1 || result.OmittedHistoryMessageCount != 1 {
		t.Fatalf("unexpected history counts: included=%d omitted=%d", result.HistoryMessageCount, result.OmittedHistoryMessageCount)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected recent history and current user, got %#v", result.Messages)
	}
	assertMessage(t, result.Messages[0], "assistant", "recent")
	assertMessage(t, result.Messages[1], "user", "current")
	if result.EstimatedTokenCount > builder.MaxInputTokens {
		t.Fatalf("expected estimated tokens within budget, got %d > %d", result.EstimatedTokenCount, builder.MaxInputTokens)
	}
}

func TestDefaultContextBuilderKeepsTurnHistoryAtomicWhenBudgeted(t *testing.T) {
	currentText := "current"
	assistantText := "paired answer"
	builder := DefaultContextBuilder{
		MaxInputTokens: estimateMessageTokens(llm.Message{
			Role:    "user",
			Content: []llm.ContentPart{{Type: "text", Text: currentText}},
		}) + estimateMessageTokens(llm.Message{
			Role:    "assistant",
			Content: []llm.ContentPart{{Type: "text", Text: assistantText}},
		}) + 1,
	}

	result, err := builder.Build(ContextBuildRequest{
		History: []managedagents.ConversationMessage{
			{
				Seq:     1,
				Role:    "user",
				Payload: json.RawMessage(`{"turn_id":"turn_1","content":[{"type":"text","text":"` + strings.Repeat("paired question ", 8) + `"}]}`),
			},
			{
				Seq:     2,
				Role:    "assistant",
				Payload: json.RawMessage(`{"turn_id":"turn_1","content":[{"type":"text","text":"` + assistantText + `"}]}`),
			},
		},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"` + currentText + `"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.Truncated {
		t.Fatal("expected context to be truncated")
	}
	if result.HistoryMessageCount != 0 || result.OmittedHistoryMessageCount != 2 || result.OmittedHistoryUntilSeq != 2 {
		t.Fatalf("expected whole turn history to be omitted, got %+v", result)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected only current user message, got %#v", result.Messages)
	}
	assertMessage(t, result.Messages[0], "user", currentText)
}

func TestDefaultContextBuilderInjectsSummaryBeforeHistory(t *testing.T) {
	builder := DefaultContextBuilder{}

	result, err := builder.Build(ContextBuildRequest{
		System:      "system",
		SummaryText: "Earlier conversation established the repo layout.",
		History: []managedagents.ConversationMessage{{
			Seq:     1,
			Role:    "user",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"recent history"}]}`),
		}},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.SummaryMessageIncluded {
		t.Fatal("expected summary message to be included")
	}
	if len(result.Messages) != 4 {
		t.Fatalf("expected system, summary, history and current, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "system", "system")
	assertMessageContains(t, result.Messages[0], "system", "Latest user message policy:")
	assertMessage(t, result.Messages[1], "system", "Conversation summary:\nEarlier conversation established the repo layout.")
	assertMessage(t, result.Messages[2], "user", "recent history")
	assertMessage(t, result.Messages[3], "user", "current")
}

func TestDefaultContextBuilderKeepsPinnedContextOutsideHistoryBudget(t *testing.T) {
	builder := DefaultContextBuilder{MaxInputTokens: 1}

	result, err := builder.Build(ContextBuildRequest{
		System:        "system",
		PinnedContext: "repo root is /workspace/project\nnever discard approval policy",
		History: []managedagents.ConversationMessage{{
			Seq:     1,
			Role:    "assistant",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"history"}]}`),
		}},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.PinnedContextIncluded {
		t.Fatal("expected pinned context to be included")
	}
	if result.HistoryMessageCount != 0 || result.OmittedHistoryMessageCount != 1 {
		t.Fatalf("expected history to be omitted while pinned context remains, got %+v", result)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected system, pinned context and current user, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "system", "system")
	assertMessageContains(t, result.Messages[0], "system", "Latest user message policy:")
	assertMessage(t, result.Messages[1], "system", "Pinned context:\nrepo root is /workspace/project\nnever discard approval policy")
	assertMessage(t, result.Messages[2], "user", "current")
	if result.Budget.PinnedContextTokens == 0 {
		t.Fatalf("expected pinned context token breakdown, got %+v", result.Budget)
	}
}

func TestDefaultContextBuilderInjectsToolsAndSkillsBeforeHistory(t *testing.T) {
	builder := DefaultContextBuilder{}

	result, err := builder.Build(ContextBuildRequest{
		System: "system",
		Tools:  tools.DefaultRegistry().ModelContext(),
		Skills: json.RawMessage(`["code-review","search"]`),
		History: []managedagents.ConversationMessage{{
			Seq:     1,
			Role:    "assistant",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"history"}]}`),
		}},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if len(result.Messages) != 5 {
		t.Fatalf("expected system, tools, skills, history and current, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "system", "system")
	assertMessageContains(t, result.Messages[0], "system", "Latest user message policy:")
	assertMessageContains(t, result.Messages[1], "system", "Available tools:\n")
	assertMessageContains(t, result.Messages[1], "system", "\"identifier\": \"default\"")
	assertMessageContains(t, result.Messages[1], "system", "\"name\": \"tool namespace plus api name, for example default.run_command\"")
	assertMessage(t, result.Messages[2], "system", "Available skills:\n[\n  \"code-review\",\n  \"search\"\n]")
	assertMessage(t, result.Messages[3], "assistant", "history")
	assertMessage(t, result.Messages[4], "user", "current")
	if result.Budget.ToolsTokens == 0 || result.Budget.SkillsTokens == 0 {
		t.Fatalf("expected tools and skills token breakdown, got %+v", result.Budget)
	}
	if result.Budget.EstimatedTokenCount != result.EstimatedTokenCount {
		t.Fatalf("expected budget estimated tokens to match result, got %+v", result.Budget)
	}
}

func TestDefaultContextBuilderBudgetsNativeToolSchemas(t *testing.T) {
	currentText := "current"
	modelTools := []llm.Tool{{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "default_large_tool",
			Description: strings.Repeat("schema ", 80),
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		},
	}}
	currentTokens := estimateMessageTokens(llm.Message{
		Role:    "user",
		Content: []llm.ContentPart{{Type: "text", Text: currentText}},
	})
	toolSchemaTokens := estimateToolsTokens(modelTools)
	builder := DefaultContextBuilder{MaxInputTokens: currentTokens + toolSchemaTokens + 1}

	result, err := builder.Build(ContextBuildRequest{
		ModelTools: modelTools,
		History: []managedagents.ConversationMessage{{
			Seq:     1,
			Role:    "assistant",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"recent history"}]}`),
		}},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"` + currentText + `"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.Truncated || result.HistoryMessageCount != 0 || result.OmittedHistoryMessageCount != 1 {
		t.Fatalf("expected tool schema cost to force history truncation, got %+v", result)
	}
	if result.Budget.ToolSchemaCount != 1 || result.Budget.ToolSchemaTokens != toolSchemaTokens {
		t.Fatalf("expected tool schema budget breakdown, got %+v", result.Budget)
	}
	if result.Budget.MessageTokens+result.Budget.ToolSchemaTokens != result.EstimatedTokenCount {
		t.Fatalf("expected estimated token count to include messages and tool schema, got %+v", result.Budget)
	}
}

func TestEstimateTextTokensDoesNotUndercountChineseContext(t *testing.T) {
	if got := estimateTextTokens("上下文预算"); got != 5 {
		t.Fatalf("expected one estimated token per CJK rune, got %d", got)
	}
	if got := estimateTextTokens("abcdefgh"); got != 2 {
		t.Fatalf("expected ASCII heuristic to remain four characters per token, got %d", got)
	}
}

func TestDefaultContextBuilderAlwaysKeepsSystemAndCurrentUser(t *testing.T) {
	builder := DefaultContextBuilder{MaxInputTokens: 1}

	result, err := builder.Build(ContextBuildRequest{
		System: "system prompt that is already over budget",
		History: []managedagents.ConversationMessage{{
			Seq:     1,
			Role:    "user",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"history"}]}`),
		}},
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	if !result.Truncated || result.HistoryMessageCount != 0 || result.OmittedHistoryMessageCount != 1 {
		t.Fatalf("unexpected truncation metadata: %+v", result)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected system and current user, got %#v", result.Messages)
	}
	assertMessageContains(t, result.Messages[0], "system", "system prompt that is already over budget")
	assertMessageContains(t, result.Messages[0], "system", "Latest user message policy:")
	assertMessage(t, result.Messages[1], "user", "current")
	if result.EstimatedTokenCount <= builder.MaxInputTokens {
		t.Fatalf("expected base messages to exceed tiny budget, got %d", result.EstimatedTokenCount)
	}
	if result.Budget.OmittedHistoryTokens == 0 || result.Budget.HistoryTokens != 0 {
		t.Fatalf("expected omitted history token breakdown, got %+v", result.Budget)
	}
}

func TestBuildSystemPromptAppendsLatestMessagePolicyOnce(t *testing.T) {
	system := "be concise"

	prompt := buildSystemPrompt(system)
	if !strings.Contains(prompt, system) || !strings.Contains(prompt, "Latest user message policy:") || !strings.Contains(prompt, "Turn progress policy:") {
		t.Fatalf("expected appended policy, got %q", prompt)
	}

	again := buildSystemPrompt(prompt)
	if strings.Count(again, "Latest user message policy:") != 1 {
		t.Fatalf("expected latest-message policy once, got %q", again)
	}
	if strings.Count(again, "Turn progress policy:") != 1 {
		t.Fatalf("expected turn-progress policy once, got %q", again)
	}
}

func TestEstimateToolsTokensCountsNativeToolSchemas(t *testing.T) {
	if got := estimateToolsTokens(nil); got != 0 {
		t.Fatalf("expected nil tools to cost 0 tokens, got %d", got)
	}
	if got := estimateToolsTokens(tools.DefaultRegistry().ModelTools()); got == 0 {
		t.Fatal("expected default model tools to have a positive token estimate")
	}
}

func assertMessage(t *testing.T, message llm.Message, role string, text string) {
	t.Helper()
	if message.Role != role {
		t.Fatalf("expected role %q, got %q", role, message.Role)
	}
	if len(message.Content) != 1 || message.Content[0].Text != text {
		t.Fatalf("expected text %q, got %#v", text, message.Content)
	}
}

func assertMessageContains(t *testing.T, message llm.Message, role string, text string) {
	t.Helper()
	if message.Role != role {
		t.Fatalf("expected role %q, got %q", role, message.Role)
	}
	if len(message.Content) != 1 || !strings.Contains(message.Content[0].Text, text) {
		t.Fatalf("expected text containing %q, got %#v", text, message.Content)
	}
}
