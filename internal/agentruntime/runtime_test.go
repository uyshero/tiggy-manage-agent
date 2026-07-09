package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestDemoRuntimeReturnsAgentPayload(t *testing.T) {
	runtime := DemoRuntime{}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "Agent runtime received: hello" {
		t.Fatalf("expected demo runtime payload, got %q", got)
	}
	if got := payloadString(result.AgentPayload, "protocol_version"); got != DemoProtocolVersion {
		t.Fatalf("expected protocol version %q, got %q", DemoProtocolVersion, got)
	}
}

func TestDemoRuntimeEmitsLLMDeltaForStreamingClient(t *testing.T) {
	runtime := DemoRuntime{Client: streamingTestClient{}}
	var steps []Step

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "streamed response" {
		t.Fatalf("expected streamed payload, got %q", got)
	}
	if !hasStepType(steps, managedagents.EventRuntimeLLMDelta) {
		t.Fatalf("expected runtime.llm_delta in steps: %#v", steps)
	}
}

func TestDemoRuntimeReturnsUsageWhenPostLLMStepFails(t *testing.T) {
	runtime := DemoRuntime{Client: usageLLMClient{}}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
		EmitStep: func(ctx context.Context, step Step) error {
			if step.Type == managedagents.EventRuntimeCompleted {
				return errors.New("write completed step failed")
			}
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected completed step error")
	}
	if got := payloadText(result.AgentPayload); got != "usage response" {
		t.Fatalf("expected partial agent payload, got %q", got)
	}
	if result.Usage.InputTokens != 13 || result.Usage.OutputTokens != 5 || result.Usage.TotalTokens != 18 {
		t.Fatalf("expected usage to survive post-LLM failure, got %#v", result.Usage)
	}
}

func TestDemoRuntimeExecutesCapabilityToolCalls(t *testing.T) {
	client := &toolLoopLLMClient{}
	provider := stubCapabilityProvider{}
	var steps []Step

	runtime := DemoRuntime{Client: client}
	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
		Config: Config{
			ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: provider,
			},
		},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "final answer" {
		t.Fatalf("expected final answer after tool loop, got %q", got)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two llm requests, got %#v", client.requests)
	}
	if len(client.requests[1].Messages) != 3 {
		t.Fatalf("expected assistant tool call plus tool result before final response, got %#v", client.requests[1].Messages)
	}
	assertLLMMessage(t, client.requests[1].Messages[0], "user", "please inspect")
	assertLLMMessage(t, client.requests[1].Messages[1], "assistant", `{"protocol_version":"tma.tool_call.v1","tool_calls":[{"id":"call_1","type":"function","function":{"name":"default.run_command","arguments":{"args":["-c","printf tool-output"],"command":"sh"}}}]}`)
	if client.requests[1].Messages[2].Role != "tool" {
		t.Fatalf("expected tool role, got %q", client.requests[1].Messages[2].Role)
	}
	var toolResult map[string]any
	if err := json.Unmarshal([]byte(llmMessageText(client.requests[1].Messages[2])), &toolResult); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if toolResult["identifier"] != tools.DefaultIdentifier || toolResult["api_name"] != "run_command" || toolResult["success"] != true {
		t.Fatalf("unexpected tool result: %#v", toolResult)
	}
	if toolResult["content"] != "tool-output" {
		t.Fatalf("unexpected tool content: %#v", toolResult["content"])
	}
	if state, ok := toolResult["state"].(map[string]any); !ok || state["stdout"] != "tool-output" {
		t.Fatalf("unexpected tool state: %#v", toolResult["state"])
	}
	if !hasStepType(steps, managedagents.EventRuntimeToolCall) || !hasStepType(steps, managedagents.EventRuntimeToolResult) {
		t.Fatalf("expected tool call/result steps, got %#v", steps)
	}
}

func TestDemoRuntimeExecutesNativeToolCalls(t *testing.T) {
	client := &nativeToolLoopLLMClient{}
	runtime := DemoRuntime{Client: client}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
		Config: Config{
			ModelTools:   tools.DefaultRegistry().ModelTools(),
			ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: stubCapabilityProvider{},
			},
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "native final answer" {
		t.Fatalf("expected final answer after native tool loop, got %q", got)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two llm requests, got %#v", client.requests)
	}
	if len(client.requests[0].Tools) == 0 {
		t.Fatalf("expected model tools on first request")
	}
	assistantMessage := client.requests[1].Messages[1]
	if len(assistantMessage.ToolCalls) != 1 || assistantMessage.ToolCalls[0].ID != "call_native" {
		t.Fatalf("expected assistant native tool call to be preserved, got %#v", assistantMessage)
	}
	toolMessage := client.requests[1].Messages[2]
	if toolMessage.Role != "tool" || toolMessage.ToolCallID != "call_native" {
		t.Fatalf("expected tool result with tool_call_id, got %#v", toolMessage)
	}
}

func TestDemoRuntimeRequiresApprovalForSensitiveTools(t *testing.T) {
	client := &sensitiveToolLoopLLMClient{}
	provider := &countingCapabilityProvider{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
		Config: Config{
			ModelTools:       tools.DefaultRegistry().ModelTools(),
			ToolRegistry:     tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeRequestApproval,
			ToolExecutor:     tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: provider,
			},
		},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected pending intervention error, got %v", err)
	}
	if provider.editCalls != 0 {
		t.Fatalf("expected edit_file not to execute without approval, got %d calls", provider.editCalls)
	}
	if !hasStepType(steps, managedagents.EventRuntimeToolInterventionRequired) {
		t.Fatalf("expected intervention required step, got %#v", steps)
	}
	requiredStep := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	if len(requiredStep.Private) == 0 {
		t.Fatalf("expected intervention required step to include private continuation state")
	}
	if _, ok := requiredStep.Private["continuation_messages"].([]llm.Message); !ok {
		t.Fatalf("expected private continuation messages, got %#v", requiredStep.Private["continuation_messages"])
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected runtime to pause before second LLM request, got %d requests", len(client.requests))
	}
}

func TestDemoRuntimeApproveForMeExecutesSensitiveTools(t *testing.T) {
	client := &sensitiveToolLoopLLMClient{}
	provider := &countingCapabilityProvider{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please edit"}]}`),
		Config: Config{
			ModelTools:       tools.DefaultRegistry().ModelTools(),
			ToolRegistry:     tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeApproveForMe,
			ToolExecutor:     tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: provider,
			},
		},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "approval final answer" {
		t.Fatalf("expected final answer after auto-approval, got %q", got)
	}
	if provider.editCalls != 1 {
		t.Fatalf("expected edit_file to execute once, got %d", provider.editCalls)
	}
	if !hasStepType(steps, managedagents.EventRuntimeToolInterventionApproved) {
		t.Fatalf("expected intervention approved step, got %#v", steps)
	}
}

func TestDemoRuntimePassesProviderConfigToLLMClient(t *testing.T) {
	client := &captureLLMClient{}
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
		Config: Config{
			LLMProvider:     "volcengine-agent-plan",
			LLMProviderType: llm.ProviderTypeOpenAI,
			LLMModel:        "doubao-test",
			LLMBaseURL:      "http://llm.example/v1",
			LLMAPIKey:       "test-key",
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if client.request.Provider != "volcengine-agent-plan" {
		t.Fatalf("expected provider from config, got %q", client.request.Provider)
	}
	if client.request.ProviderType != llm.ProviderTypeOpenAI {
		t.Fatalf("expected provider type from config, got %q", client.request.ProviderType)
	}
	if client.request.Model != "doubao-test" {
		t.Fatalf("expected model from config, got %q", client.request.Model)
	}
	if client.request.BaseURL != "http://llm.example/v1" || client.request.APIKey != "test-key" {
		t.Fatalf("expected transport config, got base_url=%q api_key=%q", client.request.BaseURL, client.request.APIKey)
	}
}

func TestDemoRuntimePassesConversationHistoryToLLMClient(t *testing.T) {
	client := &captureLLMClient{}
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000002",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"what is my name?"}]}`),
		History: []managedagents.ConversationMessage{
			{
				Seq:     3,
				Role:    "user",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"my name is Alice"}],"turn_id":"turn_000001"}`),
			},
			{
				Seq:     4,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"Nice to meet you, Alice."}],"turn_id":"turn_000001"}`),
			},
		},
		Config: Config{System: "remember user facts"},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 4 {
		t.Fatalf("expected 4 LLM messages, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[0], "system", "remember user facts")
	assertLLMMessage(t, client.request.Messages[1], "user", "my name is Alice")
	assertLLMMessage(t, client.request.Messages[2], "assistant", "Nice to meet you, Alice.")
	assertLLMMessage(t, client.request.Messages[3], "user", "what is my name?")
}

func TestDemoRuntimeUsesContextWindowBudget(t *testing.T) {
	client := &captureLLMClient{}
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000002",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
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
		Config: Config{ContextWindowTokens: 200},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 3 {
		t.Fatalf("expected two history messages plus current user, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[0], "user", "old")
	assertLLMMessage(t, client.request.Messages[1], "assistant", "recent")
	assertLLMMessage(t, client.request.Messages[2], "user", "current")
}

func TestDemoRuntimePerformsJustInTimeCompaction(t *testing.T) {
	client := &compactionLLMClient{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000003",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
		History: []managedagents.ConversationMessage{
			{
				Seq:     1,
				Role:    "user",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"old old old old old old old old old old"}]}`),
			},
			{
				Seq:     2,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"recent"}]}`),
			},
		},
		Config: Config{ContextWindowTokens: 42},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if result.SummaryText == "" || result.SummarySourceUntilSeq != 1 {
		t.Fatalf("expected generated summary and source seq 1, got %#v", result)
	}
	if got := payloadText(result.AgentPayload); got != "final answer" {
		t.Fatalf("expected final runtime payload, got %q", got)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected compaction and final LLM requests, got %#v", client.requests)
	}
	assertLLMMessage(t, client.requests[0].Messages[0], "system", "Summarize the older conversation context for a coding agent. Preserve user requirements, architecture decisions, file paths, commands, failures, and fixes. Be concise.")
	assertLLMMessage(t, client.requests[1].Messages[0], "system", "Conversation summary:\nbrief summary")
	assertLLMMessage(t, client.requests[1].Messages[1], "assistant", "recent")
	assertLLMMessage(t, client.requests[1].Messages[2], "user", "current")
	if !hasStepType(steps, managedagents.EventRuntimeContextCompacting) || !hasStepType(steps, managedagents.EventRuntimeContextCompacted) {
		t.Fatalf("expected compaction steps, got %#v", steps)
	}
}

func assertLLMMessage(t *testing.T, message llm.Message, role string, text string) {
	t.Helper()
	if message.Role != role {
		t.Fatalf("expected role %q, got %q", role, message.Role)
	}
	if len(message.Content) != 1 || message.Content[0].Text != text {
		t.Fatalf("expected text %q, got %#v", text, message.Content)
	}
}

func llmMessageText(message llm.Message) string {
	if len(message.Content) == 0 {
		return ""
	}
	return message.Content[0].Text
}

type captureLLMClient struct {
	request llm.Request
}

func (c *captureLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.request = request
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "captured",
			}},
		},
	}, nil
}

type compactionLLMClient struct {
	requests []llm.Request
}

func (c *compactionLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				Content: []llm.ContentPart{{
					Type: "text",
					Text: "brief summary",
				}},
			},
		}, nil
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "final answer",
			}},
		},
	}, nil
}

type usageLLMClient struct{}

func (usageLLMClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "usage response",
			}},
		},
		Usage: llm.Usage{
			InputTokens:  13,
			OutputTokens: 5,
			TotalTokens:  18,
		},
	}, nil
}

type streamingTestClient struct{}

func (streamingTestClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (streamingTestClient) GenerateStream(ctx context.Context, request llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	if err := onDelta(llm.Delta{Index: 1, Text: "streamed "}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 2, Text: "response"}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "streamed response",
			}},
		},
	}, nil
}

type toolLoopLLMClient struct {
	requests []llm.Request
}

func (c *toolLoopLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				Content: []llm.ContentPart{{
					Type: "text",
					Text: `{"protocol_version":"tma.tool_call.v1","tool_calls":[{"id":"call_1","type":"function","function":{"name":"default.run_command","arguments":{"args":["-c","printf tool-output"],"command":"sh"}}}]}`,
				}},
			},
		}, nil
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "final answer",
			}},
		},
	}, nil
}

type nativeToolLoopLLMClient struct {
	requests []llm.Request
}

func (c *nativeToolLoopLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					ID:   "call_native",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "default.run_command",
						Arguments: json.RawMessage(`{"command":"sh","args":["-c","printf tool-output"]}`),
					},
				}},
			},
		}, nil
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "native final answer",
			}},
		},
	}, nil
}

type sensitiveToolLoopLLMClient struct {
	requests []llm.Request
}

func (c *sensitiveToolLoopLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					ID:   "call_edit",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "default.edit_file",
						Arguments: json.RawMessage(`{"file_path":"/tmp/note.txt","old_string":"a","new_string":"b"}`),
					},
				}},
			},
		}, nil
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "approval final answer",
			}},
		},
	}, nil
}

type stubCapabilityProvider struct{}

func (stubCapabilityProvider) RunCommand(context.Context, capability.RunCommandRequest) (capability.CommandResult, error) {
	return capability.CommandResult{
		ExitCode: 0,
		Stdout:   "tool-output",
	}, nil
}

func (stubCapabilityProvider) ExecuteCode(context.Context, capability.ExecuteCodeRequest) (capability.CommandResult, error) {
	return capability.CommandResult{}, nil
}

func (stubCapabilityProvider) ReadFile(context.Context, capability.ReadFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (stubCapabilityProvider) WriteFile(context.Context, capability.WriteFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (stubCapabilityProvider) EditFile(context.Context, capability.EditFileRequest) (capability.EditFileResult, error) {
	return capability.EditFileResult{Success: true, Replacements: 1}, nil
}

type countingCapabilityProvider struct {
	editCalls int
}

func (p *countingCapabilityProvider) RunCommand(context.Context, capability.RunCommandRequest) (capability.CommandResult, error) {
	return capability.CommandResult{ExitCode: 0, Stdout: "tool-output"}, nil
}

func (p *countingCapabilityProvider) ExecuteCode(context.Context, capability.ExecuteCodeRequest) (capability.CommandResult, error) {
	return capability.CommandResult{}, nil
}

func (p *countingCapabilityProvider) ReadFile(context.Context, capability.ReadFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (p *countingCapabilityProvider) WriteFile(context.Context, capability.WriteFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (p *countingCapabilityProvider) EditFile(context.Context, capability.EditFileRequest) (capability.EditFileResult, error) {
	p.editCalls++
	return capability.EditFileResult{Success: true, Replacements: 1}, nil
}

func hasStepType(steps []Step, stepType string) bool {
	for _, step := range steps {
		if step.Type == stepType {
			return true
		}
	}
	return false
}

func firstStepType(steps []Step, stepType string) Step {
	for _, step := range steps {
		if step.Type == stepType {
			return step
		}
	}
	return Step{}
}

func payloadText(payload json.RawMessage) string {
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
		if content.Type == "text" {
			return content.Text
		}
	}
	return ""
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	var value string
	if err := json.Unmarshal(object[key], &value); err != nil {
		return ""
	}
	return value
}
