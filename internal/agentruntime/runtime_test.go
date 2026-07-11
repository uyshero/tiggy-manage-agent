package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func TestToolCallsFromSeedToolCallText(t *testing.T) {
	response := llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: `<seed:tool_call><function name="computer.bring_to_front"><parameter name="app" string="true">Google Chrome</parameter></function></seed:tool_call>`,
			}},
		},
	}

	calls, ok := toolCallsFromLLMResponse(response)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one seed tool call, got ok=%v calls=%#v", ok, calls)
	}
	if calls[0].Identifier != "computer" || calls[0].APIName != "bring_to_front" {
		t.Fatalf("unexpected seed call target: %#v", calls[0])
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("decode seed call args: %v", err)
	}
	if args["app"] != "Google Chrome" {
		t.Fatalf("unexpected seed call args: %#v", args)
	}
}

func TestDemoRuntimeExecutesSeedToolCallText(t *testing.T) {
	client := &seedToolLoopLLMClient{}
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
		t.Fatalf("expected final answer after seed tool loop, got %q", got)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two llm requests, got %#v", client.requests)
	}
	if !hasStepType(steps, managedagents.EventRuntimeToolCall) || !hasStepType(steps, managedagents.EventRuntimeToolResult) {
		t.Fatalf("expected tool call/result steps, got %#v", steps)
	}
}

func TestDemoRuntimeTruncatesLargeToolResultForModelContext(t *testing.T) {
	client := &toolLoopLLMClient{}
	provider := largeOutputCapabilityProvider{
		stdout: strings.Repeat("A", 120) + "middle-marker" + strings.Repeat("Z", 120),
	}
	var steps []Step

	runtime := DemoRuntime{Client: client}
	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
		Config: Config{
			RuntimeSettings: json.RawMessage(`{"tool_result_context_max_chars":150}`),
			ToolExecutor:    tools.NewDefaultExecutor(),
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
	var toolResult map[string]any
	if err := json.Unmarshal([]byte(llmMessageText(client.requests[1].Messages[2])), &toolResult); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	content, _ := toolResult["content"].(string)
	if strings.Contains(content, "middle-marker") {
		t.Fatalf("expected middle of large tool result to be omitted, got %q", content)
	}
	if !strings.Contains(content, "Tool result truncated for model context") {
		t.Fatalf("expected truncation notice in tool result, got %q", content)
	}
	contextMeta, ok := toolResult["context"].(map[string]any)
	if !ok || contextMeta["content_truncated"] != true {
		t.Fatalf("expected content_truncated context metadata, got %#v", toolResult["context"])
	}
	var toolResultStep *Step
	for index := range steps {
		if steps[index].Type == managedagents.EventRuntimeToolResult {
			toolResultStep = &steps[index]
			break
		}
	}
	if toolResultStep == nil {
		t.Fatalf("expected runtime tool result step, got %#v", steps)
	}
	stepContent, _ := toolResultStep.Data["content"].(string)
	if strings.Contains(stepContent, "middle-marker") {
		t.Fatalf("expected observable tool result event to omit middle content, got %q", stepContent)
	}
	stepContext, ok := toolResultStep.Data["context"].(map[string]any)
	if !ok || stepContext["content_truncated"] != true {
		t.Fatalf("expected observable tool result event truncation metadata, got %#v", toolResultStep.Data["context"])
	}
}

func TestDemoRuntimeExecutesNativeToolCalls(t *testing.T) {
	client := &nativeToolLoopLLMClient{}
	runtime := DemoRuntime{Client: client}
	var steps []Step

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
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
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
	llmRequestStep := firstStepType(steps, managedagents.EventRuntimeLLMRequest)
	if llmRequestStep.Data["tool_schema_count"] == nil || llmRequestStep.Data["estimated_tool_schema_tokens"] == nil {
		t.Fatalf("expected llm request step to include tool schema budget fields, got %#v", llmRequestStep.Data)
	}
	if count, ok := llmRequestStep.Data["tool_schema_count"].(int); !ok || count == 0 {
		t.Fatalf("expected positive tool schema count, got %#v", llmRequestStep.Data["tool_schema_count"])
	}
	if tokens, ok := llmRequestStep.Data["estimated_tool_schema_tokens"].(int); !ok || tokens == 0 {
		t.Fatalf("expected positive tool schema tokens, got %#v", llmRequestStep.Data["estimated_tool_schema_tokens"])
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

func TestDemoRuntimeUnlimitedToolRoundsAllowsLongerLoop(t *testing.T) {
	client := &multiRoundToolLoopLLMClient{finalAfter: 5}
	runtime := DemoRuntime{Client: client}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
		Config: Config{
			ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: stubCapabilityProvider{},
			},
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "multi-round final answer" {
		t.Fatalf("expected final answer after extended tool loop, got %q", got)
	}
	if len(client.requests) != 6 {
		t.Fatalf("expected five tool rounds plus final answer, got %d requests", len(client.requests))
	}
}

func TestDemoRuntimeMaxToolRoundsStillCapsLoopWhenConfigured(t *testing.T) {
	client := &multiRoundToolLoopLLMClient{finalAfter: 5}
	runtime := DemoRuntime{
		Client:        client,
		MaxToolRounds: 4,
	}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please inspect"}]}`),
		Config: Config{
			ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: stubCapabilityProvider{},
			},
		},
	})
	if err == nil || err.Error() != "tool loop exceeded maximum rounds" {
		t.Fatalf("expected max tool round error, got %v", err)
	}
}

func TestDemoRuntimeResumesApprovedInterventionThroughToolLoop(t *testing.T) {
	client := &captureLLMClient{}
	provider := &countingCapabilityProvider{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call: tools.Call{
				ID:         "call_edit",
				Identifier: "default",
				APIName:    "edit_file",
				Arguments:  json.RawMessage(`{"file_path":"/tmp/note.txt","old_string":"a","new_string":"b"}`),
			},
			Status: managedagents.InterventionStatusApproved,
			Continuation: []llm.Message{{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					ID:   "call_edit",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "default.edit_file",
						Arguments: json.RawMessage(`{"file_path":"/tmp/note.txt","old_string":"a","new_string":"b"}`),
					},
				}},
			}},
		},
		Config: Config{
			ModelTools:       tools.DefaultRegistry().ModelTools(),
			ToolRegistry:     tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeRequestApproval,
			ToolExecutor:     tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: provider,
			},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("resume approved intervention: %v", err)
	}
	if provider.editCalls != 1 {
		t.Fatalf("expected approved tool to execute once, got %d", provider.editCalls)
	}
	if got := payloadText(result.AgentPayload); got != "captured" {
		t.Fatalf("expected resumed final answer, got %q", got)
	}
	if !hasStepType(steps, managedagents.EventRuntimeToolResult) || !hasStepType(steps, managedagents.EventRuntimeCompleted) {
		t.Fatalf("expected resumed runtime steps, got %#v", steps)
	}
	if len(client.request.Messages) != 2 || client.request.Messages[1].Role != "tool" || client.request.Messages[1].ToolCallID != "call_edit" {
		t.Fatalf("expected approved tool observation in resumed LLM request, got %#v", client.request.Messages)
	}
}

func TestDemoRuntimeResumesRejectedInterventionAsObservation(t *testing.T) {
	client := &captureLLMClient{}
	provider := &countingCapabilityProvider{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call:           tools.Call{ID: "call_edit", Identifier: "default", APIName: "edit_file"},
			Status:         managedagents.InterventionStatusRejected,
			DecisionReason: "unsafe edit",
			Continuation:   []llm.Message{{Role: "assistant"}},
		},
		Config: Config{
			ToolRegistry: tools.DefaultRegistry(),
			ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: provider,
			},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("resume rejected intervention: %v", err)
	}
	if provider.editCalls != 0 {
		t.Fatalf("expected rejected tool not to execute, got %d calls", provider.editCalls)
	}
	if len(client.request.Messages) != 2 || !strings.Contains(contentPartsText(client.request.Messages[1].Content), "unsafe edit") {
		t.Fatalf("expected rejection observation in LLM request, got %#v", client.request.Messages)
	}
	toolResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	if toolResult.Data["success"] != false || toolResult.Data["decision_reason"] != "unsafe edit" {
		t.Fatalf("unexpected rejected tool result step: %#v", toolResult)
	}
}

func TestDemoRuntimeResumedLoopCanRequireAnotherApproval(t *testing.T) {
	client := &sensitiveToolLoopLLMClient{}
	provider := &countingCapabilityProvider{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call:         tools.Call{ID: "call_read", Identifier: "default", APIName: "read_file", Arguments: json.RawMessage(`{"file_path":"README.md"}`)},
			Status:       managedagents.InterventionStatusApproved,
			Continuation: []llm.Message{{Role: "assistant"}},
		},
		Config: Config{
			ModelTools:       tools.DefaultRegistry().ModelTools(),
			ToolRegistry:     tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeRequestApproval,
			ToolExecutor:     tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{
				Provider: provider,
			},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected second approval pause, got %v", err)
	}
	if provider.editCalls != 0 {
		t.Fatalf("expected second sensitive tool not to execute, got %d calls", provider.editCalls)
	}
	required := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	if required.Data["id"] != "call_edit" || required.Private["continuation_messages"] == nil {
		t.Fatalf("expected persisted second continuation, got %#v", required)
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
	assertLLMMessageContains(t, client.request.Messages[0], "system", "remember user facts")
	assertLLMMessageContains(t, client.request.Messages[0], "system", "Latest user message policy:")
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

func TestDemoRuntimeUsesRuntimeSettingsContextBudgetRatio(t *testing.T) {
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
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"old old old old old old old old old old old old old old old old"}]}`),
			},
			{
				Seq:     2,
				Role:    "assistant",
				Payload: json.RawMessage(`{"content":[{"type":"text","text":"recent"}]}`),
			},
		},
		Config: Config{
			ContextWindowTokens:   200,
			SummarySourceUntilSeq: 2,
			RuntimeSettings:       json.RawMessage(`{"context_input_budget_ratio_percent":10}`),
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 2 {
		t.Fatalf("expected tight budget to keep only recent history plus current, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[0], "assistant", "recent")
	assertLLMMessage(t, client.request.Messages[1], "user", "current")
}

func TestContextBudgetRatioPercentClampsRuntimeSettings(t *testing.T) {
	if got := contextBudgetRatioPercent(json.RawMessage(`{"context_input_budget_ratio_percent":1}`)); got != 10 {
		t.Fatalf("expected low ratio to clamp to 10, got %d", got)
	}
	if got := contextBudgetRatioPercent(json.RawMessage(`{"context_input_budget_ratio_percent":99}`)); got != 95 {
		t.Fatalf("expected high ratio to clamp to 95, got %d", got)
	}
	if got := contextBudgetRatioPercent(json.RawMessage(`{"context_budget_ratio_percent":75}`)); got != 75 {
		t.Fatalf("expected legacy ratio key to be honored, got %d", got)
	}
}

func TestContextBudgetFromSettingsReservesOutputTokens(t *testing.T) {
	limits := contextBudgetFromSettings(1000, json.RawMessage(`{"context_input_budget_ratio_percent":80,"context_output_reserve_tokens":400}`))
	if limits.MaxInputTokens != 600 {
		t.Fatalf("expected explicit output reserve to reduce input budget to 600, got %+v", limits)
	}
	if limits.ReservedOutputTokens != 400 {
		t.Fatalf("expected output reserve to be reported, got %+v", limits)
	}

	defaultLimits := contextBudgetFromSettings(1000, json.RawMessage(`{"context_input_budget_ratio_percent":70}`))
	if defaultLimits.MaxInputTokens != 700 || defaultLimits.ReservedOutputTokens != 300 {
		t.Fatalf("expected default reserve to reflect unused window, got %+v", defaultLimits)
	}
}

func TestDemoRuntimeInjectsPinnedContextFromRuntimeSettings(t *testing.T) {
	client := &captureLLMClient{}
	var steps []Step
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
		Config: Config{
			RuntimeSettings: json.RawMessage(`{"pinned_context":["repo=/workspace/project","approval policy is fixed"]}`),
		},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 2 {
		t.Fatalf("expected pinned context and current user, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[0], "system", "Pinned context:\n- repo=/workspace/project\n- approval policy is fixed")
	assertLLMMessage(t, client.request.Messages[1], "user", "current")
	llmRequestStep := firstStepType(steps, managedagents.EventRuntimeLLMRequest)
	if included, ok := llmRequestStep.Data["pinned_context_included"].(bool); !ok || !included {
		t.Fatalf("expected llm request to report pinned context, got %#v", llmRequestStep.Data)
	}
	budget, ok := llmRequestStep.Data["context_budget"].(ContextBudgetBreakdown)
	if !ok || budget.PinnedContextTokens == 0 {
		t.Fatalf("expected pinned context budget tokens, got %#v", llmRequestStep.Data["context_budget"])
	}
}

func TestPinnedContextFromSettingsSupportsProtectedContextAlias(t *testing.T) {
	got := pinnedContextFromSettings(json.RawMessage(`{"protected_context":"keep this fact"}`))
	if got != "keep this fact" {
		t.Fatalf("expected protected_context alias to be honored, got %q", got)
	}
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
	assertLLMMessageContains(t, client.requests[0].Messages[0], "system", "Return a concise structured summary")
	assertLLMMessageContains(t, client.requests[0].Messages[0], "system", "Objective")
	assertLLMMessageContains(t, client.requests[0].Messages[0], "system", "Pinned facts")
	assertLLMMessage(t, client.requests[1].Messages[0], "system", "Conversation summary:\nbrief summary")
	assertLLMMessage(t, client.requests[1].Messages[1], "assistant", "recent")
	assertLLMMessage(t, client.requests[1].Messages[2], "user", "current")
	if !hasStepType(steps, managedagents.EventRuntimeContextCompacting) || !hasStepType(steps, managedagents.EventRuntimeContextCompacted) {
		t.Fatalf("expected compaction steps, got %#v", steps)
	}
	llmRequestStep := firstStepType(steps, managedagents.EventRuntimeLLMRequest)
	if _, ok := llmRequestStep.Data["context_budget"].(ContextBudgetBreakdown); !ok {
		t.Fatalf("expected llm request step to include context budget breakdown, got %#v", llmRequestStep.Data["context_budget"])
	}
}

func TestCompactionPromptCapsHistoryAndKeepsRecentMessages(t *testing.T) {
	history := make([]managedagents.ConversationMessage, 0, 20)
	for index := 1; index <= 20; index++ {
		marker := "middle"
		if index == 1 {
			marker = "old-marker"
		}
		if index == 20 {
			marker = "new-marker"
		}
		history = append(history, managedagents.ConversationMessage{
			Seq:  int64(index),
			Role: "user",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"` +
				strings.Repeat("x", 180) + marker +
				`"}]}`),
		})
	}
	prompt := compactionPromptWithLimit(TurnRequest{
		History: history,
		Config: Config{
			SummaryText: strings.Repeat("summary ", 20),
		},
	}, 20, 3000)

	if len([]rune(prompt)) > 3000 {
		t.Fatalf("expected prompt to stay within cap, got %d chars", len([]rune(prompt)))
	}
	if strings.Contains(prompt, "old-marker") {
		t.Fatalf("expected oldest message to be omitted from capped prompt")
	}
	if !strings.Contains(prompt, "new-marker") {
		t.Fatalf("expected newest message to be retained in capped prompt")
	}
	if !strings.Contains(prompt, "older messages omitted") {
		t.Fatalf("expected prompt to include omission notice, got %q", prompt)
	}
}

func TestCompactionLimitsHonorRuntimeSettings(t *testing.T) {
	history := make([]managedagents.ConversationMessage, 0, 8)
	for index := 1; index <= 8; index++ {
		history = append(history, managedagents.ConversationMessage{
			Seq:  int64(index),
			Role: "user",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"` +
				strings.Repeat("x", 220) +
				`"}]}`),
		})
	}
	settings := json.RawMessage(`{"compaction_prompt_max_chars":900,"compaction_summary_max_chars":400}`)
	prompt := compactionPrompt(TurnRequest{
		History: history,
		Config: Config{
			RuntimeSettings: settings,
		},
	}, 8)
	if len([]rune(prompt)) > 900 {
		t.Fatalf("expected runtime setting to cap compaction prompt, got %d chars", len([]rune(prompt)))
	}

	summary := normalizeCompactionSummaryWithLimit(strings.Repeat("summary-", 200), compactionSummaryMaxChars(settings))
	if len([]rune(summary)) > 400 {
		t.Fatalf("expected runtime setting to cap compaction summary, got %d chars", len([]rune(summary)))
	}
}

func TestNormalizeCompactionSummaryCapsLargeSummary(t *testing.T) {
	summary := normalizeCompactionSummary(strings.Repeat("summary-", 3000))
	if len([]rune(summary)) > defaultCompactionSummaryMaxChars {
		t.Fatalf("expected summary to be capped at %d chars, got %d", defaultCompactionSummaryMaxChars, len([]rune(summary)))
	}
	if !strings.Contains(summary, "Text truncated for compaction prompt budget") {
		t.Fatalf("expected truncated summary to include notice, got %q", summary)
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

func assertLLMMessageContains(t *testing.T, message llm.Message, role string, text string) {
	t.Helper()
	if message.Role != role {
		t.Fatalf("expected role %q, got %q", role, message.Role)
	}
	if len(message.Content) != 1 || !strings.Contains(message.Content[0].Text, text) {
		t.Fatalf("expected text containing %q, got %#v", text, message.Content)
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

type seedToolLoopLLMClient struct {
	requests []llm.Request
}

func (c *seedToolLoopLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				Content: []llm.ContentPart{{
					Type: "text",
					Text: `<seed:tool_call><function name="default.run_command"><parameter name="command" string="true">sh</parameter><parameter name="args" json="true">["-c","printf tool-output"]</parameter></function></seed:tool_call>`,
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

type multiRoundToolLoopLLMClient struct {
	requests   []llm.Request
	finalAfter int
}

func (c *multiRoundToolLoopLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) <= c.finalAfter {
		callID := fmt.Sprintf("call_%d", len(c.requests))
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					ID:   callID,
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
				Text: "multi-round final answer",
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

type largeOutputCapabilityProvider struct {
	stdout string
}

func (p largeOutputCapabilityProvider) RunCommand(context.Context, capability.RunCommandRequest) (capability.CommandResult, error) {
	return capability.CommandResult{
		ExitCode: 0,
		Stdout:   p.stdout,
	}, nil
}

func (p largeOutputCapabilityProvider) ExecuteCode(context.Context, capability.ExecuteCodeRequest) (capability.CommandResult, error) {
	return capability.CommandResult{}, nil
}

func (p largeOutputCapabilityProvider) ReadFile(context.Context, capability.ReadFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (p largeOutputCapabilityProvider) WriteFile(context.Context, capability.WriteFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (p largeOutputCapabilityProvider) EditFile(context.Context, capability.EditFileRequest) (capability.EditFileResult, error) {
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
