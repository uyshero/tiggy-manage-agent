package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

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
	assertMessage(t, result.Messages[0], "system", "remember user facts")
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
	assertMessage(t, result.Messages[0], "system", "system")
	assertMessage(t, result.Messages[1], "system", "Conversation summary:\nEarlier conversation established the repo layout.")
	assertMessage(t, result.Messages[2], "user", "recent history")
	assertMessage(t, result.Messages[3], "user", "current")
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
	assertMessage(t, result.Messages[0], "system", "system")
	assertMessageContains(t, result.Messages[1], "system", "Available tools:\n")
	assertMessageContains(t, result.Messages[1], "system", "\"identifier\": \"default\"")
	assertMessageContains(t, result.Messages[1], "system", "\"name\": \"tool namespace plus api name, for example default.run_command\"")
	assertMessage(t, result.Messages[2], "system", "Available skills:\n[\n  \"code-review\",\n  \"search\"\n]")
	assertMessage(t, result.Messages[3], "assistant", "history")
	assertMessage(t, result.Messages[4], "user", "current")
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
	assertMessage(t, result.Messages[0], "system", "system prompt that is already over budget")
	assertMessage(t, result.Messages[1], "user", "current")
	if result.EstimatedTokenCount <= builder.MaxInputTokens {
		t.Fatalf("expected base messages to exceed tiny budget, got %d", result.EstimatedTokenCount)
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
