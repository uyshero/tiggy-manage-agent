package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestDemoRuntimeSendsImagesDirectlyToVisionCapableCurrentModel(t *testing.T) {
	client := &visionRoutingClient{}
	runtime := DemoRuntime{Client: client}
	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"describe image"}]}`),
		ImageParts:  []llm.ContentPart{{Type: "image_url", ImageURL: &llm.ImageURL{URL: "data:image/png;base64,cG5n"}}},
		Config:      Config{LLMModel: "current-vision", LLMCapabilityType: managedagents.LLMModelCapabilityTextImage},
	})
	if err != nil {
		t.Fatalf("run vision turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "main response" {
		t.Fatalf("unexpected response %q", got)
	}
	if len(client.requests) != 1 || !messageHasImage(client.requests[0].Messages) {
		t.Fatalf("expected one multimodal request to current model, got %#v", client.requests)
	}
}

func TestDemoRuntimeUsesDefaultVisionModelForTextCurrentModel(t *testing.T) {
	client := &visionRoutingClient{}
	runtime := DemoRuntime{Client: client}
	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"read chart"}]}`),
		ImageParts:  []llm.ContentPart{{Type: "image_url", ImageURL: &llm.ImageURL{URL: "data:image/png;base64,cG5n"}}},
		Config: Config{
			LLMModel: "current-text", LLMCapabilityType: managedagents.LLMModelCapabilityText,
			VisionLLMProvider: "vision-provider", VisionLLMProviderType: llm.ProviderFake, VisionLLMModel: "fallback-vision",
		},
	})
	if err != nil {
		t.Fatalf("run fallback vision turn: %v", err)
	}
	if len(client.requests) != 2 || client.requests[0].Model != "fallback-vision" || client.requests[1].Model != "current-text" {
		t.Fatalf("unexpected vision routing: %#v", client.requests)
	}
	if !messageHasImage(client.requests[0].Messages) || messageHasImage(client.requests[1].Messages) {
		t.Fatalf("expected image only in fallback vision request: %#v", client.requests)
	}
	if !strings.Contains(messagesText(client.requests[1].Messages), "parsed image content") {
		t.Fatalf("expected vision analysis in current model context: %#v", client.requests[1].Messages)
	}
	if result.Usage.TotalTokens != 10 {
		t.Fatalf("expected combined usage, got %#v", result.Usage)
	}
}

func TestDemoRuntimeRejectsImagesWithoutVisionModel(t *testing.T) {
	runtime := DemoRuntime{Client: &visionRoutingClient{}}
	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"describe"}]}`),
		ImageParts:  []llm.ContentPart{{Type: "image_url", ImageURL: &llm.ImageURL{URL: "data:image/png;base64,cG5n"}}},
		Config:      Config{LLMModel: "text-only", LLMCapabilityType: managedagents.LLMModelCapabilityText},
	})
	if !errors.Is(err, ErrVisionModelNotConfigured) {
		t.Fatalf("expected vision configuration error, got %v", err)
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
	var payloadEnvelope struct {
		ContentFormat string `json:"content_format"`
	}
	if err := json.Unmarshal(result.AgentPayload, &payloadEnvelope); err != nil || payloadEnvelope.ContentFormat != "markdown" {
		t.Fatalf("expected markdown final payload, envelope=%#v err=%v", payloadEnvelope, err)
	}
	if !hasStepType(steps, managedagents.EventRuntimeLLMDelta) {
		t.Fatalf("expected runtime.llm_delta in steps: %#v", steps)
	}
	if !hasStepType(steps, managedagents.EventRuntimeLLMChunk) {
		t.Fatalf("expected runtime.llm_chunk in steps: %#v", steps)
	}
	reasoningChunks := 0
	chunkKinds := map[string]int{}
	for _, step := range steps {
		if step.Type == managedagents.EventRuntimeLLMChunk {
			kind, _ := step.Data["type"].(string)
			chunkKinds[kind]++
			if step.Data["type"] == llm.DeltaKindReasoning {
				reasoningChunks++
				if step.Data["text"] != "checking context" {
					t.Fatalf("unexpected reasoning chunk: %#v", step.Data)
				}
			}
		}
		if step.Type != managedagents.EventRuntimeLLMDelta {
			continue
		}
		if step.Data["content_format"] != "markdown" || step.Data["operation"] != "append" {
			t.Fatalf("expected append-only markdown delta, got %#v", step.Data)
		}
	}
	if reasoningChunks != 1 {
		t.Fatalf("expected one reasoning chunk, got %d", reasoningChunks)
	}
	for _, kind := range []string{llm.DeltaKindText, llm.DeltaKindReasoning, llm.DeltaKindToolCall, llm.DeltaKindUsage, llm.DeltaKindStop} {
		if chunkKinds[kind] == 0 {
			t.Fatalf("expected %s chunk, got %#v", kind, chunkKinds)
		}
	}
}

func TestEmitLLMChunkPreservesStructuredStreamError(t *testing.T) {
	var emitted Step
	err := emitLLMChunk(t.Context(), TurnRequest{EmitStep: func(_ context.Context, step Step) error {
		emitted = step
		return nil
	}}, llm.Delta{
		Index: 7,
		Kind:  llm.DeltaKindError,
		Error: &llm.StreamError{Class: llm.ErrorClassServer, Retryable: true, Message: "stream failed"},
	}, 2)
	if err != nil {
		t.Fatalf("emit stream error chunk: %v", err)
	}
	streamError, ok := emitted.Data["error"].(*llm.StreamError)
	if emitted.Type != managedagents.EventRuntimeLLMChunk || emitted.Data["type"] != llm.DeltaKindError || emitted.Data["tool_round"] != 2 || !ok || streamError.Message != "stream failed" {
		t.Fatalf("unexpected emitted stream error: %#v", emitted)
	}
}

func TestGenerateLLMStreamsRequestsWithTools(t *testing.T) {
	client := &toolStreamingSelectionClient{}
	response, err := generateLLM(t.Context(), client, llm.Request{
		Tools: []llm.Tool{{
			Type:     "function",
			Function: llm.ToolFunction{Name: "default.read_file"},
		}},
	}, TurnRequest{}, 0, tools.FileMutationLimits{})
	if err != nil {
		t.Fatalf("generate llm: %v", err)
	}
	if client.generateCalled || !client.streamCalled {
		t.Fatalf("expected streaming path for tool request, got generate=%v stream=%v", client.generateCalled, client.streamCalled)
	}
	if got := contentPartsText(response.Message.Content); got != "streamed with tools" {
		t.Fatalf("unexpected streamed response %q", got)
	}
}

func TestGenerateLLMStopsLargeFileMutationAtRecommendedStreamLimit(t *testing.T) {
	client := &oversizedFileMutationStreamingClient{}
	emittedToolChunks := 0
	_, err := generateLLM(t.Context(), client, llm.Request{}, TurnRequest{EmitStep: func(_ context.Context, step Step) error {
		if step.Type == managedagents.EventRuntimeLLMChunk && step.Data["type"] == llm.DeltaKindToolCall {
			emittedToolChunks++
		}
		if step.Type == managedagents.EventRuntimeToolResult {
			t.Fatal("proactive stream limit must not emit a tool failure result")
		}
		return nil
	}}, 0, tools.FileMutationLimits{RecommendedTokens: 50, MaxTokens: 100})
	var limitError *fileMutationStreamLimitError
	if !errors.As(err, &limitError) {
		t.Fatalf("expected proactive file mutation stream limit, got %v", err)
	}
	if limitError.APIName != "write_file" || limitError.EstimatedTokens <= 50 {
		t.Fatalf("unexpected stream limit details: %#v", limitError)
	}
	if emittedToolChunks != 1 {
		t.Fatalf("expected only the initial safe tool chunk to be emitted, got %d", emittedToolChunks)
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
	if len(client.requests[1].Messages) != 4 {
		t.Fatalf("expected assistant tool call plus tool result before final response, got %#v", client.requests[1].Messages)
	}
	assertLLMMessage(t, client.requests[1].Messages[1], "user", "please inspect")
	assertLLMMessage(t, client.requests[1].Messages[2], "assistant", `{"protocol_version":"tma.tool_call.v1","tool_calls":[{"id":"call_1","type":"function","function":{"name":"default.run_command","arguments":{"args":["-c","printf tool-output"],"command":"sh"}}}]}`)
	if client.requests[1].Messages[3].Role != "tool" {
		t.Fatalf("expected tool role, got %q", client.requests[1].Messages[3].Role)
	}
	var toolResult map[string]any
	if err := json.Unmarshal([]byte(llmMessageText(client.requests[1].Messages[3])), &toolResult); err != nil {
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
	if hasStepType(steps, managedagents.EventRuntimeProgressMessage) {
		t.Fatalf("expected protocol tool envelope to stay hidden from progress messages, got %#v", steps)
	}
}

func TestDemoRuntimeEmitsMCPToolMetadata(t *testing.T) {
	client := &mcpToolLoopLLMClient{}
	mcpRuntime := testMCPRuntime{}
	registry := tools.NewRegistry(mcpRuntime)
	var steps []Step

	runtime := DemoRuntime{Client: client}
	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"ping mcp"}]}`),
		Config: Config{
			ModelTools:      registry.ModelTools(),
			ToolRegistry:    registry,
			ToolExecutor:    tools.RegistryExecutor{Registry: registry},
			RuntimeSettings: json.RawMessage(`{"tool_result_context_max_chars":12000}`),
		},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn with mcp tool: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "mcp final answer" {
		t.Fatalf("expected final answer after mcp tool loop, got %q", got)
	}

	toolCall := firstStepType(steps, managedagents.EventRuntimeToolCall)
	assertMCPEventMetadata(t, toolCall)
	toolResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	assertMCPEventMetadata(t, toolResult)
	if toolResult.Data["success"] != true {
		t.Fatalf("expected successful mcp tool result, got %#v", toolResult.Data)
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
	if err := json.Unmarshal([]byte(llmMessageText(client.requests[1].Messages[3])), &toolResult); err != nil {
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

func TestMicrocompactToolResultMessagesBoundsAccumulatedContext(t *testing.T) {
	toolMessage := func(id string, marker string) llm.Message {
		payload := fmt.Sprintf(`{"protocol_version":"tma.tool_result.v1","id":%q,"identifier":"default","api_name":"read_file","content":%q,"success":true,"artifacts":[{"artifact_id":%q}]}`, id, strings.Repeat(marker, 1000), "art_"+id)
		return llm.Message{Role: "tool", ToolCallID: id, Content: []llm.ContentPart{{Type: "text", Text: payload}}}
	}
	messages := []llm.Message{
		{Role: "user", Content: []llm.ContentPart{{Type: "text", Text: "inspect"}}},
		toolMessage("call_1", "A"),
		toolMessage("call_2", "B"),
		toolMessage("call_3", "C"),
	}
	originalLatest := llmMessageText(messages[3])
	compacted, stats := microcompactToolResultMessages(messages, 1900)
	if stats.CompactedCount == 0 || stats.AfterChars >= stats.BeforeChars || stats.AfterChars > stats.MaxChars {
		t.Fatalf("expected bounded cumulative tool context, got %+v", stats)
	}
	if llmMessageText(compacted[3]) != originalLatest {
		t.Fatal("expected newest tool result to remain intact")
	}
	if compacted[1].ToolCallID != "call_1" || !strings.Contains(llmMessageText(compacted[1]), `"micro_compacted":true`) || !strings.Contains(llmMessageText(compacted[1]), `"artifact_id":"art_call_1"`) {
		t.Fatalf("expected oldest result to retain pairing and artifact metadata: %#v", compacted[1])
	}
	if strings.Contains(llmMessageText(compacted[1]), strings.Repeat("A", 100)) {
		t.Fatal("expected bulky old tool content to be removed")
	}
	if strings.Contains(llmMessageText(messages[1]), `"micro_compacted":true`) {
		t.Fatal("expected input messages to remain unchanged")
	}
}

func TestToolResultContextTotalMaxCharsUsesPerResultFloor(t *testing.T) {
	settings := json.RawMessage(`{"tool_result_context_max_chars":500,"tool_result_context_total_max_chars":200}`)
	if got := toolResultContextTotalMaxChars(settings); got != 500 {
		t.Fatalf("expected total cap to preserve one complete recent result, got %d", got)
	}
	if got := toolResultContextTotalMaxChars(json.RawMessage(`{"tool_result_context_max_chars":500}`)); got != 1000 {
		t.Fatalf("expected default total cap to retain two results, got %d", got)
	}
}

func TestTokenEstimateCalibrationOnlyRaisesFutureEstimates(t *testing.T) {
	calibration := tokenEstimateCalibration{}
	calibration.observe(100, 175)
	if got := calibration.apply(200); got != 350 || calibration.multiplier() != 1.75 {
		t.Fatalf("expected provider usage to raise later estimates, got=%d multiplier=%v", got, calibration.multiplier())
	}
	calibration.observe(100, 80)
	if got := calibration.apply(200); got != 350 {
		t.Fatalf("lower provider usage must not reduce conservative estimate, got %d", got)
	}
	calibration.observe(100, 200)
	if got := calibration.apply(200); got != 400 {
		t.Fatalf("expected highest observed multiplier to win, got %d", got)
	}
}

func TestFitRoundOutputBudgetShrinksOutputAndRejectsExhaustedInput(t *testing.T) {
	request := llm.Request{MaxOutputTokens: 400}
	available, err := fitRoundOutputBudget(&request, ContextBudgetBreakdown{ContextWindowTokens: 1000, ReservedOutputTokens: 400}, 850)
	if err != nil || available != 150 || request.MaxOutputTokens != 150 {
		t.Fatalf("expected output budget to shrink to remaining context: request=%#v available=%d err=%v", request, available, err)
	}
	_, err = fitRoundOutputBudget(&request, ContextBudgetBreakdown{ContextWindowTokens: 1000}, 1000)
	var providerError *llm.ProviderError
	if !errors.As(err, &providerError) || providerError.Class != llm.ErrorClassContextLength || providerError.Retryable {
		t.Fatalf("expected non-retryable context length error before provider call, got %v", err)
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
	assistantMessage := client.requests[1].Messages[2]
	if len(assistantMessage.ToolCalls) != 1 || assistantMessage.ToolCalls[0].ID != "call_native" {
		t.Fatalf("expected assistant native tool call to be preserved, got %#v", assistantMessage)
	}
	toolMessage := client.requests[1].Messages[3]
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
	progressStep := firstStepType(steps, managedagents.EventRuntimeProgressMessage)
	if progressStep.Data["text"] != "I found the relevant command. I will verify its output next." {
		t.Fatalf("expected native tool preamble progress event, got %#v", progressStep)
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

func TestDemoRuntimeParksAndResumesForHumanInput(t *testing.T) {
	client := &askUserThenFinalClient{}
	registry := tools.NewRegistry(tools.InteractionRuntime{})
	var steps []Step
	runtime := DemoRuntime{Client: client}
	config := Config{
		ModelTools: tools.DefaultRegistry().ModelTools(), ToolRegistry: registry,
		ToolExecutor: tools.RegistryExecutor{Registry: registry},
	}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"deploy it"}]}`),
		Config:      config,
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingHumanInput) {
		t.Fatalf("expected human input pause, got %v", err)
	}
	required := firstStepType(steps, managedagents.EventRuntimeHumanInputRequired)
	if required.Data["kind"] != managedagents.InterventionKindClarification {
		t.Fatalf("unexpected human input step: %#v", required)
	}
	continuation, ok := required.Private["continuation_messages"].([]llm.Message)
	if !ok || len(continuation) == 0 {
		t.Fatalf("expected persisted continuation: %#v", required.Private)
	}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call: tools.Call{ID: "call_ask", Identifier: tools.InteractionIdentifier, APIName: tools.InteractionAPIAskUser},
			Kind: managedagents.InterventionKindClarification, Status: managedagents.InterventionStatusAnswered,
			Response: json.RawMessage(`{"deployment":"private"}`), Continuation: continuation,
		},
		Config: config,
	})
	if err != nil {
		t.Fatalf("resume human input: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "continuing with private deployment" {
		t.Fatalf("unexpected resumed answer %q", got)
	}
	if client.calls != 2 || !strings.Contains(messagesText(client.lastRequest.Messages), `"deployment":"private"`) {
		t.Fatalf("expected answered input as tool result, calls=%d request=%#v", client.calls, client.lastRequest)
	}
}

func TestDemoRuntimeParksAndResumesForUploadRequest(t *testing.T) {
	client := &uploadRequestThenFinalClient{}
	registry := tools.NewRegistry(tools.InteractionRuntime{})
	var steps []Step
	runtime := DemoRuntime{Client: client}
	config := Config{
		ModelTools: registry.ModelTools(), ToolRegistry: registry,
		ToolExecutor: tools.RegistryExecutor{Registry: registry},
	}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"analyze my contract"}]}`),
		Config:      config,
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingHumanInput) {
		t.Fatalf("expected upload request pause, got %v", err)
	}
	required := firstStepType(steps, managedagents.EventRuntimeHumanInputRequired)
	if required.Data["kind"] != managedagents.InterventionKindUploadRequest || required.Data["intervention_mode"] != "request_upload" {
		t.Fatalf("unexpected upload request step: %#v", required)
	}
	var uploadRequest tools.UploadRequest
	if raw, ok := required.Private["request"].(json.RawMessage); !ok || json.Unmarshal(raw, &uploadRequest) != nil || uploadRequest.MaxFiles != 2 || uploadRequest.Accept[0] != ".pdf" {
		t.Fatalf("expected persisted upload request, got %#v", required.Private["request"])
	}
	continuation, ok := required.Private["continuation_messages"].([]llm.Message)
	if !ok || len(continuation) == 0 {
		t.Fatalf("expected persisted continuation: %#v", required.Private)
	}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call: tools.Call{ID: "call_upload", Identifier: tools.InteractionIdentifier, APIName: tools.InteractionAPIRequestUpload},
			Kind: managedagents.InterventionKindUploadRequest, Status: managedagents.InterventionStatusAnswered,
			Response:     json.RawMessage(`{"artifacts":[{"artifact_id":"art_1","object_ref_id":"obj_1","name":"contract.pdf"}]}`),
			Continuation: continuation,
		},
		Config: config,
	})
	if err != nil {
		t.Fatalf("resume upload request: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "analyzing uploaded contract" {
		t.Fatalf("unexpected resumed answer %q", got)
	}
	if client.calls != 2 || !strings.Contains(messagesText(client.lastRequest.Messages), `"artifact_id":"art_1"`) {
		t.Fatalf("expected uploaded artifact response as tool result, calls=%d request=%#v", client.calls, client.lastRequest)
	}
}

func TestDemoRuntimeParksAndResumesForPlanApproval(t *testing.T) {
	client := &planApprovalThenFinalClient{}
	registry := tools.NewRegistry(tools.InteractionRuntime{})
	planService := &runtimeTaskService{plan: managedagents.SessionTaskPlan{
		ID: "plan_000001", SessionID: "sesn_000001", Title: "Production rollout", Goal: "Deploy safely",
		HandlingMode: managedagents.TaskPlanModePlanned, Status: managedagents.TaskPlanStatusActive,
		Items: []managedagents.SessionTaskItem{
			{ID: "item_1", PlanID: "plan_000001", Index: 0, Description: "Prepare", Status: managedagents.TaskItemStatusCompleted, Evidence: "config ready"},
			{ID: "item_2", PlanID: "plan_000001", Index: 1, Description: "Deploy", Status: managedagents.TaskItemStatusPending},
			{ID: "item_3", PlanID: "plan_000001", Index: 2, Description: "Verify", Status: managedagents.TaskItemStatusPending},
		},
	}}
	config := Config{
		ModelTools: registry.ModelTools(), ToolRegistry: registry,
		ToolExecutor:         tools.RegistryExecutor{Registry: registry},
		ToolExecutionContext: tools.ExecutionContext{TaskService: planService},
	}
	var steps []Step
	runtime := DemoRuntime{Client: client}
	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"plan the rollout"}]}`), Config: config,
		EmitStep: func(_ context.Context, step Step) error { steps = append(steps, step); return nil },
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected plan approval pause, got %v", err)
	}
	required := firstStepType(steps, managedagents.EventRuntimePlanApprovalRequired)
	if required.Data["kind"] != managedagents.InterventionKindPlanApproval || required.Data["plan_id"] != "plan_000001" {
		t.Fatalf("unexpected plan approval event: %#v", required)
	}
	var snapshot tools.PlanApprovalSnapshot
	if raw, ok := required.Private["request"].(json.RawMessage); !ok || json.Unmarshal(raw, &snapshot) != nil || snapshot.Plan.ID != "plan_000001" || len(snapshot.Plan.Items) != 3 {
		t.Fatalf("expected full plan snapshot, got %#v", required.Private["request"])
	}
	continuation, ok := required.Private["continuation_messages"].([]llm.Message)
	if !ok || len(continuation) == 0 {
		t.Fatalf("expected resumable continuation: %#v", required.Private)
	}

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call: tools.Call{ID: "call_plan_approval", Identifier: tools.InteractionIdentifier, APIName: tools.InteractionAPIRequestPlanApproval, Arguments: json.RawMessage(`{"plan_id":"plan_000001"}`)},
			Kind: managedagents.InterventionKindPlanApproval, Status: managedagents.InterventionStatusApproved,
			DecisionReason: "Proceed", Continuation: continuation,
		},
		Config: config,
	})
	if err != nil {
		t.Fatalf("resume plan approval: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "executing approved plan" {
		t.Fatalf("unexpected resumed result %q", got)
	}
	if client.calls != 2 || !strings.Contains(messagesText(client.lastRequest.Messages), "does not approve any later tool call") {
		t.Fatalf("expected synthetic plan decision tool result, calls=%d request=%#v", client.calls, client.lastRequest)
	}
}

func TestDemoRuntimePlanRejectionReturnsToModelWithoutExecutingTool(t *testing.T) {
	client := &planApprovalThenFinalClient{calls: 1}
	registry := tools.NewRegistry(tools.InteractionRuntime{})
	result, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call: tools.Call{ID: "call_plan_approval", Identifier: tools.InteractionIdentifier, APIName: tools.InteractionAPIRequestPlanApproval, Arguments: json.RawMessage(`{"plan_id":"plan_000001"}`)},
			Kind: managedagents.InterventionKindPlanApproval, Status: managedagents.InterventionStatusRejected,
			DecisionReason: "Reduce scope", Continuation: []llm.Message{{Role: "assistant"}},
		},
		Config: Config{ToolRegistry: registry, ToolExecutor: tools.RegistryExecutor{Registry: registry}},
	})
	if err != nil {
		t.Fatalf("resume rejected plan: %v", err)
	}
	if payloadText(result.AgentPayload) != "executing approved plan" || !strings.Contains(messagesText(client.lastRequest.Messages), "Revise or cancel the plan") {
		t.Fatalf("expected rejection observation to return to model: result=%s request=%#v", payloadText(result.AgentPayload), client.lastRequest)
	}
}

func TestDemoRuntimeRejectsMalformedToolArgumentsBeforeApproval(t *testing.T) {
	client := &malformedThenSensitiveToolClient{}
	provider := &countingCapabilityProvider{}
	var steps []Step
	runtime := DemoRuntime{Client: client, MaxToolRounds: 4}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"write the report"}]}`),
		Config: Config{
			ModelTools: tools.DefaultRegistry().ModelTools(), ToolRegistry: tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeRequestApproval, ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{Provider: provider},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected valid retry to require approval, got %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected model retry after malformed arguments, got %d requests", len(client.requests))
	}
	if client.requests[0].MaxOutputTokens != defaultMaxLLMOutputTokens || client.requests[1].MaxOutputTokens != defaultMaxLLMOutputTokens {
		t.Fatalf("expected output budget on every model request, got %#v", client.requests)
	}
	invalidResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	executionError, ok := invalidResult.Data["error"].(*tools.ExecutionError)
	if !ok || executionError.Type != "invalid_tool_arguments" {
		t.Fatalf("expected invalid_tool_arguments result, got %#v", invalidResult.Data)
	}
	required := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	if required.Data["id"] != "call_valid_write" {
		t.Fatalf("only the valid retry should reach approval, got %#v", required.Data)
	}
	continuation, ok := required.Private["continuation_messages"].([]llm.Message)
	if !ok {
		t.Fatalf("expected continuation messages, got %#v", required.Private)
	}
	encoded, err := json.Marshal(continuation)
	if err != nil {
		t.Fatalf("continuation must remain valid JSON: %v", err)
	}
	var decoded []llm.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode continuation: %v", err)
	}
	for _, message := range decoded {
		for _, call := range message.ToolCalls {
			if call.ID == "call_truncated_write" && string(call.Function.Arguments) != `{}` {
				t.Fatalf("malformed arguments were not normalized: %s", call.Function.Arguments)
			}
		}
	}
}

func TestDemoRuntimeStopsAfterRepeatedMalformedToolArguments(t *testing.T) {
	client := &alwaysMalformedToolClient{}
	runtime := DemoRuntime{Client: client, MaxToolRounds: 8}
	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"write the report"}]}`),
		Config: Config{
			ModelTools: tools.DefaultRegistry().ModelTools(), ToolRegistry: tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeRequestApproval, ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{Provider: &countingCapabilityProvider{}},
		},
	})
	if err == nil || err.Error() != "model repeatedly returned invalid or oversized tool arguments" {
		t.Fatalf("expected repeated malformed arguments error, got %v", err)
	}
	if client.calls != maxInvalidToolArgumentRetries {
		t.Fatalf("expected %d model calls before stopping, got %d", maxInvalidToolArgumentRetries, client.calls)
	}
}

func TestDemoRuntimeCorrectsSchemaInvalidToolArgumentsBeforeExecution(t *testing.T) {
	client := &schemaInvalidThenValidToolClient{}
	runtimeTool := &schemaValidationRuntime{}
	registry := tools.NewRegistry(runtimeTool)
	var steps []Step

	result, err := (DemoRuntime{Client: client, MaxToolRounds: 5}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_schema", TurnID: "turn_schema",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"run schema tool"}]}`),
		Config: Config{
			ModelTools: registry.ModelTools(), ToolRegistry: registry,
			ToolExecutor: tools.RegistryExecutor{Registry: registry},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run schema correction loop: %v", err)
	}
	if payloadText(result.AgentPayload) != "schema-valid final" || client.calls != 3 || runtimeTool.calls != 1 {
		t.Fatalf("unexpected correction result=%q model_calls=%d tool_calls=%d", payloadText(result.AgentPayload), client.calls, runtimeTool.calls)
	}
	firstResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	executionError, ok := firstResult.Data["error"].(*tools.ExecutionError)
	if !ok || executionError.Type != "invalid_tool_arguments" || !strings.Contains(executionError.Message, "/required") {
		t.Fatalf("expected schema validation result, got %#v", firstResult.Data)
	}
	if !strings.Contains(messagesText(client.requests[1].Messages), "registered JSON Schema validation") || !strings.Contains(messagesText(client.requests[1].Messages), "Do not retry the unchanged payload") {
		t.Fatalf("schema recovery feedback missing from retry: %#v", client.requests[1].Messages)
	}
}

func TestDemoRuntimeStopsAfterRepeatedSchemaInvalidToolArguments(t *testing.T) {
	client := &alwaysSchemaInvalidToolClient{}
	runtimeTool := &schemaValidationRuntime{}
	registry := tools.NewRegistry(runtimeTool)

	_, err := (DemoRuntime{Client: client, MaxToolRounds: 8}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_schema", TurnID: "turn_schema",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"run schema tool"}]}`),
		Config: Config{
			ModelTools: registry.ModelTools(), ToolRegistry: registry,
			ToolExecutor: tools.RegistryExecutor{Registry: registry},
		},
	})
	if err == nil || err.Error() != "model repeatedly returned invalid or oversized tool arguments" {
		t.Fatalf("expected schema retry circuit breaker, got %v", err)
	}
	if client.calls != maxInvalidToolArgumentRetries || runtimeTool.calls != 0 {
		t.Fatalf("invalid schema calls escaped guard: model_calls=%d tool_calls=%d", client.calls, runtimeTool.calls)
	}
}

func TestDemoRuntimeFailsClosedForInvalidRegisteredToolSchema(t *testing.T) {
	runtimeTool := &invalidSchemaRuntime{}
	registry := tools.NewRegistry(runtimeTool)
	client := &completionScriptClient{responses: []llm.Response{schemaToolResponse("call_invalid_schema", `{"value":"candidate"}`)}}

	_, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_schema", TurnID: "turn_schema",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"run invalid schema tool"}]}`),
		Config: Config{
			ModelTools: registry.ModelTools(), ToolRegistry: registry,
			ToolExecutor: tools.RegistryExecutor{Registry: registry},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "registered tool schema validation failed") || runtimeTool.calls != 0 {
		t.Fatalf("invalid schema did not fail closed: err=%v calls=%d", err, runtimeTool.calls)
	}
}

func TestDemoRuntimeRejectsOversizedWriteBeforeApproval(t *testing.T) {
	client := &oversizedThenSkeletonWriteClient{}
	var steps []Step
	runtime := DemoRuntime{Client: client, MaxToolRounds: 4}
	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"write a large report"}]}`),
		Config: Config{
			ModelTools: tools.DefaultRegistry().ModelTools(), ToolRegistry: tools.DefaultRegistry(),
			InterventionMode: tools.InterventionModeRequestApproval, ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{Provider: &countingCapabilityProvider{}},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected skeleton retry to reach approval, got %v", err)
	}
	firstResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	executionError, ok := firstResult.Data["error"].(*tools.ExecutionError)
	if !ok || executionError.Type != "file_content_too_large" {
		t.Fatalf("expected oversized write rejection, got %#v", firstResult.Data)
	}
	required := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	if required.Data["id"] != "call_skeleton_write" {
		t.Fatalf("only skeleton write should reach approval, got %#v", required.Data)
	}
}

func TestDemoRuntimeRequiresApprovalBeforeInstallingSkill(t *testing.T) {
	client := &skillsInstallLLMClient{}
	service := &countingSkillsToolService{}
	registry := tools.NewRegistry(tools.SkillsRuntime{Service: service})
	var steps []Step
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"install the review skill"}]}`),
		Config: Config{
			ModelTools:       registry.ModelTools(),
			ToolRegistry:     registry,
			InterventionMode: tools.InterventionModeRequestApproval,
			ToolExecutor:     tools.RegistryExecutor{Registry: registry},
		},
		EmitStep: func(ctx context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected pending intervention error, got %v", err)
	}
	if service.installCalls != 0 {
		t.Fatalf("expected skills.install not to execute without approval, got %d calls", service.installCalls)
	}
	requiredStep := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	if requiredStep.Data["identifier"] != tools.SkillsIdentifier || requiredStep.Data["api_name"] != "install" {
		t.Fatalf("expected skills.install approval step, got %#v", requiredStep)
	}
}

func TestDemoRuntimeSummarizesSkillsPreviewFailure(t *testing.T) {
	client := &recoverableSkillsFailureClient{
		apiName:   "preview",
		arguments: json.RawMessage(`{"source":{"provider":"github","repository":"missing/repository"}}`),
		finalText: "Skill preview failed, so installation was not attempted.",
	}
	service := &countingSkillsToolService{previewErr: errors.New("preview skill package: github API returned 404: Not Found")}
	registry := tools.NewRegistry(tools.SkillsRuntime{Service: service})
	var steps []Step

	result, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"install the GitHub skill"}]}`),
		Config: Config{
			ModelTools: registry.ModelTools(), ToolRegistry: registry,
			ToolExecutor: tools.RegistryExecutor{Registry: registry},
		},
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run turn after preview failure: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != client.finalText {
		t.Fatalf("expected failure summary, got %q", got)
	}
	if service.previewCalls != 1 || len(client.requests) != 2 {
		t.Fatalf("expected one failed preview followed by one summary request, preview=%d requests=%d", service.previewCalls, len(client.requests))
	}
	if summaryContext := messagesText(client.requests[1].Messages); !strings.Contains(summaryContext, `"success":false`) || !strings.Contains(summaryContext, "github API returned 404") {
		t.Fatalf("expected failed tool result in summary context: %s", summaryContext)
	}
	toolResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	executionError, ok := toolResult.Data["error"].(*tools.ExecutionError)
	if !ok || executionError.Type != "tool_execution_failed" {
		t.Fatalf("expected observable tool failure, got %#v", toolResult.Data)
	}
	if !hasStepType(steps, managedagents.EventRuntimeCompleted) {
		t.Fatalf("expected turn to complete after summarizing tool failure, got %#v", steps)
	}
}

func TestDemoRuntimeSummarizesApprovedSkillsInstallFailure(t *testing.T) {
	client := &recoverableSkillsFailureClient{
		apiName:   "install",
		arguments: json.RawMessage(`{"identifier":"missing-skill","title":"Missing Skill","content_text":"instructions"}`),
		finalText: "Skill installation failed; no skill was installed.",
	}
	service := &countingSkillsToolService{installErr: errors.New("fetch remote skill: github API returned 404: Not Found")}
	registry := tools.NewRegistry(tools.SkillsRuntime{Service: service})
	config := Config{
		ModelTools: registry.ModelTools(), ToolRegistry: registry,
		InterventionMode: tools.InterventionModeRequestApproval,
		ToolExecutor:     tools.RegistryExecutor{Registry: registry},
	}
	var steps []Step
	emitStep := func(_ context.Context, step Step) error {
		steps = append(steps, step)
		return nil
	}

	_, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"install the skill"}]}`),
		Config:      config, EmitStep: emitStep,
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected install approval, got %v", err)
	}
	required := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	continuation, ok := required.Private["continuation_messages"].([]llm.Message)
	if !ok || len(continuation) == 0 {
		t.Fatalf("expected resumable continuation, got %#v", required.Private)
	}
	continuationRound, _ := required.Private["continuation_round"].(int)

	result, err := (DemoRuntime{Client: client}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call:   tools.Call{ID: "call_recoverable_skill_failure", Identifier: tools.SkillsIdentifier, APIName: "install", Arguments: client.arguments},
			Status: managedagents.InterventionStatusApproved, Continuation: continuation, ContinuationRound: continuationRound,
		},
		Config: config, EmitStep: emitStep,
	})
	if err != nil {
		t.Fatalf("resume after approved install failure: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != client.finalText {
		t.Fatalf("expected install failure summary, got %q", got)
	}
	if service.installCalls != 1 || len(client.requests) != 2 {
		t.Fatalf("expected one failed install followed by one summary request, install=%d requests=%d", service.installCalls, len(client.requests))
	}
	if summaryContext := messagesText(client.requests[1].Messages); !strings.Contains(summaryContext, `"success":false`) || !strings.Contains(summaryContext, "github API returned 404") {
		t.Fatalf("expected approved tool failure in summary context: %s", summaryContext)
	}
}

func TestDemoRuntimeOfflineSkillZIPPreviewInstallAndApproval(t *testing.T) {
	client := &offlineSkillsFlowLLMClient{}
	service := &countingSkillsToolService{}
	registry := tools.NewRegistry(tools.SkillsRuntime{Service: service})
	var steps []Step
	runtime := DemoRuntime{Client: client}
	config := Config{
		WorkspaceID:      "wksp_000001",
		ModelTools:       registry.ModelTools(),
		ToolRegistry:     registry,
		ToolExecutor:     tools.RegistryExecutor{Registry: registry},
		InterventionMode: tools.InterventionModeRequestApproval,
	}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		UserPayload: json.RawMessage(`{
			"content":[{"type":"text","text":"install this offline skill"}],
			"attachments":[{
				"artifact_id":"art_skill_zip",
				"name":"offline-review.zip",
				"content_type":"application/zip",
				"size_bytes":4096,
				"workspace_path":"/workspace/uploads/art_skill_zip/offline-review.zip"
			}]
		}`),
		Config: config,
		EmitStep: func(_ context.Context, step Step) error {
			steps = append(steps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected offline install approval, got %v", err)
	}
	if service.previewCalls != 1 || service.installCalls != 0 {
		t.Fatalf("expected preview before pending install, preview=%d install=%d", service.previewCalls, service.installCalls)
	}
	if service.previewRequest.SessionID != "sesn_000001" || service.previewRequest.WorkspaceID != "wksp_000001" || service.previewRequest.Source.ArtifactID != "art_skill_zip" {
		t.Fatalf("unexpected offline preview request: %#v", service.previewRequest)
	}
	if len(client.requests) != 2 || !strings.Contains(messagesText(client.requests[0].Messages), "artifact_id=art_skill_zip") ||
		!strings.Contains(messagesText(client.requests[0].Messages), "call skills.preview with source.provider=artifact") {
		t.Fatalf("expected artifact coordinate and preview guidance in model context: %#v", client.requests)
	}
	requiredStep := firstStepType(steps, managedagents.EventRuntimeToolInterventionRequired)
	arguments, ok := requiredStep.Data["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("expected install arguments in intervention: %#v", requiredStep)
	}
	source, ok := arguments["source"].(map[string]any)
	if !ok || source["provider"] != "artifact" || source["artifact_id"] != "art_skill_zip" {
		t.Fatalf("expected artifact install source in intervention: %#v", arguments)
	}
	continuation, ok := requiredStep.Private["continuation_messages"].([]llm.Message)
	if !ok || len(continuation) == 0 {
		t.Fatalf("expected resumable offline install continuation: %#v", requiredStep.Private)
	}
	continuationRound, _ := requiredStep.Private["continuation_round"].(int)

	result, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_000001", TurnID: "turn_000001",
		ResumeIntervention: &InterventionResume{
			Call: tools.Call{
				ID: "call_install_offline", Identifier: tools.SkillsIdentifier, APIName: "install",
				Arguments: json.RawMessage(`{"identifier":"offline-review","source":{"provider":"artifact","artifact_id":"art_skill_zip"},"policy_revision":"policy-revision"}`),
			},
			Status: managedagents.InterventionStatusApproved, Continuation: continuation, ContinuationRound: continuationRound,
		},
		Config: config,
	})
	if err != nil {
		t.Fatalf("resume approved offline install: %v", err)
	}
	if service.installCalls != 1 || service.installRequest.Source == nil || service.installRequest.Source.ArtifactID != "art_skill_zip" || service.installRequest.TurnID != "turn_000001" {
		t.Fatalf("unexpected approved offline install request: calls=%d request=%#v", service.installCalls, service.installRequest)
	}
	if got := payloadText(result.AgentPayload); got != "offline skill installed; offer enable" {
		t.Fatalf("unexpected offline install completion %q", got)
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
	if !hasStepType(steps, managedagents.EventRuntimeToolResult) || !hasStepType(steps, managedagents.EventRuntimeCompletionValidated) || !hasStepType(steps, managedagents.EventRuntimeCompleted) {
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
	runtime := DemoRuntime{
		Client: client,
		Now: func() time.Time {
			return time.Date(2026, 7, 13, 16, 58, 19, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
		},
	}

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

	if len(client.request.Messages) != 5 {
		t.Fatalf("expected 5 LLM messages, got %#v", client.request.Messages)
	}
	assertLLMMessageContains(t, client.request.Messages[0], "system", "remember user facts")
	assertLLMMessageContains(t, client.request.Messages[0], "system", "Latest user message policy:")
	assertLLMMessage(t, client.request.Messages[1], "system", "Today's date is 2026-07-13.")
	assertLLMMessage(t, client.request.Messages[2], "user", "my name is Alice")
	assertLLMMessage(t, client.request.Messages[3], "assistant", "Nice to meet you, Alice.")
	assertLLMMessage(t, client.request.Messages[4], "user", "what is my name?")
}

func TestDemoRuntimeNormalizesSkillsConfigBeforeLLMRequest(t *testing.T) {
	client := &captureLLMClient{}
	runtime := DemoRuntime{Client: client}

	_, err := runtime.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"current"}]}`),
		Config: Config{
			Skills: json.RawMessage(`["code-review","search"]`),
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 3 {
		t.Fatalf("expected skills context plus current user, got %#v", client.request.Messages)
	}
	assertLLMMessageContains(t, client.request.Messages[1], "system", "Available skills:\n")
	assertLLMMessageContains(t, client.request.Messages[1], "system", "\"enabled\": [")
	assertLLMMessageContains(t, client.request.Messages[1], "system", "\"skill\": \"code-review\"")
	assertLLMMessageContains(t, client.request.Messages[1], "system", "\"priority\": 100")
	assertLLMMessage(t, client.request.Messages[2], "user", "current")
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

	if len(client.request.Messages) != 4 {
		t.Fatalf("expected two history messages plus current user, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[1], "user", "old")
	assertLLMMessage(t, client.request.Messages[2], "assistant", "recent")
	assertLLMMessage(t, client.request.Messages[3], "user", "current")
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
			RuntimeSettings:       json.RawMessage(`{"context_input_budget_ratio_percent":20}`),
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(client.request.Messages) != 3 {
		t.Fatalf("expected tight budget to keep only recent history plus current, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[1], "assistant", "recent")
	assertLLMMessage(t, client.request.Messages[2], "user", "current")
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

	if len(client.request.Messages) != 3 {
		t.Fatalf("expected pinned context and current user, got %#v", client.request.Messages)
	}
	assertLLMMessage(t, client.request.Messages[1], "system", "Pinned context:\n- repo=/workspace/project\n- approval policy is fixed")
	assertLLMMessage(t, client.request.Messages[2], "user", "current")
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
		Config: Config{ContextWindowTokens: 60},
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
	assertLLMMessage(t, client.requests[1].Messages[1], "system", "Conversation summary:\nbrief summary")
	assertLLMMessage(t, client.requests[1].Messages[2], "assistant", "recent")
	assertLLMMessage(t, client.requests[1].Messages[3], "user", "current")
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
	if err := onDelta(llm.Delta{Index: 1, Kind: llm.DeltaKindReasoning, Text: "checking context"}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 2, Kind: llm.DeltaKindText, Text: "streamed "}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 3, Kind: llm.DeltaKindText, Text: "response"}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 4, Kind: llm.DeltaKindToolCall, ToolCall: &llm.ToolCallDelta{Index: 0, ID: "call_preview", Name: "default.read_file", Arguments: "{"}}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 5, Kind: llm.DeltaKindUsage, Usage: &llm.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Index: 6, Kind: llm.DeltaKindStop, FinishReason: "stop"}); err != nil {
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

type toolStreamingSelectionClient struct {
	generateCalled bool
	streamCalled   bool
}

type oversizedFileMutationStreamingClient struct{}

func (*oversizedFileMutationStreamingClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("non-streaming generation should not be used")
}

func (*oversizedFileMutationStreamingClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	if err := onDelta(llm.Delta{Kind: llm.DeltaKindToolCall, ToolCall: &llm.ToolCallDelta{
		Index: 0, ID: "call_write", Name: "default.write_file", Arguments: `{"path":"report.html","content":"`,
	}}); err != nil {
		return llm.Response{}, err
	}
	if err := onDelta(llm.Delta{Kind: llm.DeltaKindToolCall, ToolCall: &llm.ToolCallDelta{
		Index: 0, Arguments: strings.Repeat("complete semantic report section ", 40),
	}}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{}, errors.New("expected stream limit callback to stop generation")
}

func (client *toolStreamingSelectionClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	client.generateCalled = true
	return llm.Response{}, nil
}

func (client *toolStreamingSelectionClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	client.streamCalled = true
	if err := onDelta(llm.Delta{Index: 1, Text: "streamed with tools"}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{
		Message: llm.Message{
			Role:    "assistant",
			Content: []llm.ContentPart{{Type: "text", Text: "streamed with tools"}},
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

type mcpToolLoopLLMClient struct {
	requests []llm.Request
}

func (c *mcpToolLoopLLMClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{
			Message: llm.Message{
				Role: "assistant",
				Content: []llm.ContentPart{{
					Type: "text",
					Text: `{"protocol_version":"tma.tool_call.v1","tool_calls":[{"id":"call_mcp","type":"function","function":{"name":"remote.ping","arguments":{}}}]}`,
				}},
			},
		}, nil
	}
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "mcp final answer",
			}},
		},
	}, nil
}

type testMCPRuntime struct{}

func (testMCPRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "remote",
		Type:       "mcp_server",
		Meta: tools.Meta{
			Title:       "Remote MCP",
			Description: "Test MCP server.",
		},
		Metadata: map[string]any{
			"mcp_transport":        "streamable_http",
			"mcp_protocol_version": "2025-06-18",
			"mcp_capabilities":     []string{"resources", "tools"},
			"mcp_tool_count":       1,
			"mcp_oauth":            true,
		},
		API: []tools.API{{
			Name:           "ping",
			APIName:        "ping",
			Description:    "Ping MCP server.",
			Parameters:     json.RawMessage(`{"type":"object","properties":{}}`),
			Risk:           tools.ToolRiskRead,
			Implementation: tools.ToolImplementationServerBuiltin,
		}},
	}
}

func (testMCPRuntime) Execute(ctx context.Context, call tools.Call, executionContext tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Content:    "mcp pong",
		State:      json.RawMessage(`{"protocol_version":"tma.mcp_result.v1","tool_name":"ping"}`),
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
				Content: []llm.ContentPart{{
					Type: "text",
					Text: "I found the relevant command. I will verify its output next.",
				}},
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

type askUserThenFinalClient struct {
	calls       int
	lastRequest llm.Request
}

type uploadRequestThenFinalClient struct {
	calls       int
	lastRequest llm.Request
}

type planApprovalThenFinalClient struct {
	calls       int
	lastRequest llm.Request
}

func (client *planApprovalThenFinalClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.calls++
	client.lastRequest = request
	if client.calls == 1 {
		return llm.Response{Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "call_plan_approval", Type: "function",
				Function: llm.ToolCallFunction{
					Name:      tools.InteractionIdentifier + "." + tools.InteractionAPIRequestPlanApproval,
					Arguments: json.RawMessage(`{"plan_id":"plan_000001","summary":"Review production rollout"}`),
				},
			}},
		}}, nil
	}
	return llm.Response{Message: llm.Message{
		Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "executing approved plan"}},
	}}, nil
}

type runtimeTaskService struct {
	plan managedagents.SessionTaskPlan
}

func (service *runtimeTaskService) CreatePlan(context.Context, string, managedagents.CreateSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: service.plan}, nil
}

func (service *runtimeTaskService) GetPlan(context.Context, string) (managedagents.SessionTaskPlan, error) {
	return service.plan, nil
}

func (service *runtimeTaskService) UpdateItems(context.Context, string, managedagents.UpdateSessionTaskItemsInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: service.plan}, nil
}

func (service *runtimeTaskService) CompletePlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: service.plan}, nil
}

func (service *runtimeTaskService) CancelPlan(context.Context, string, managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return managedagents.SessionTaskPlanResult{Plan: service.plan}, nil
}

func (client *askUserThenFinalClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.calls++
	client.lastRequest = request
	if client.calls == 1 {
		return llm.Response{Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "call_ask", Type: "function",
				Function: llm.ToolCallFunction{
					Name:      tools.InteractionIdentifier + "." + tools.InteractionAPIAskUser,
					Arguments: json.RawMessage(`{"question":"Which deployment model should be used?","mode":"select","choices":[{"id":"private","label":"Private"},{"id":"saas","label":"SaaS"}]}`),
				},
			}},
		}}, nil
	}
	return llm.Response{Message: llm.Message{
		Role:    "assistant",
		Content: []llm.ContentPart{{Type: "text", Text: "continuing with private deployment"}},
	}}, nil
}

func (client *uploadRequestThenFinalClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.calls++
	client.lastRequest = request
	if client.calls == 1 {
		return llm.Response{Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "call_upload", Type: "function",
				Function: llm.ToolCallFunction{
					Name:      tools.InteractionIdentifier + "." + tools.InteractionAPIRequestUpload,
					Arguments: json.RawMessage(`{"prompt":"Upload the contract PDF","accept":[".pdf","application/pdf"],"max_files":2,"max_bytes":10485760,"required":true}`),
				},
			}},
		}}, nil
	}
	return llm.Response{Message: llm.Message{
		Role:    "assistant",
		Content: []llm.ContentPart{{Type: "text", Text: "analyzing uploaded contract"}},
	}}, nil
}

type malformedThenSensitiveToolClient struct {
	requests []llm.Request
}

type alwaysMalformedToolClient struct {
	calls int
}

type schemaInvalidThenValidToolClient struct {
	calls    int
	requests []llm.Request
}

type alwaysSchemaInvalidToolClient struct {
	calls int
}

type schemaValidationRuntime struct {
	calls int
}

type invalidSchemaRuntime struct {
	calls int
}

func (runtime *invalidSchemaRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "schema_test", Type: "builtin", Executors: []string{tools.ExecutorServer},
		API: []tools.API{{Name: "check", Description: "Invalid schema fixture.", Risk: tools.ToolRiskRead, Parameters: json.RawMessage(`{"type":"object"`)}},
	}
}

func (runtime *invalidSchemaRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	runtime.calls++
	return tools.ExecutionResult{Identifier: "schema_test", APIName: "check", Content: "must not execute"}, nil
}

func (runtime *schemaValidationRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "schema_test", Type: "builtin", Executors: []string{tools.ExecutorServer},
		API: []tools.API{{
			Name: "check", Description: "Validate a deterministic value.", Risk: tools.ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"value":{"type":"string","minLength":1},"mode":{"type":"string","enum":["strict"]}},"required":["value","mode"]}`),
		}},
	}
}

func (runtime *schemaValidationRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	runtime.calls++
	return tools.ExecutionResult{Identifier: "schema_test", APIName: "check", Content: "schema check passed"}, nil
}

func (client *schemaInvalidThenValidToolClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.calls++
	client.requests = append(client.requests, request)
	switch client.calls {
	case 1:
		return schemaToolResponse("call_schema_invalid", `{"value":"candidate"}`), nil
	case 2:
		return schemaToolResponse("call_schema_valid", `{"value":"candidate","mode":"strict"}`), nil
	default:
		return textResponse("schema-valid final"), nil
	}
}

func (client *alwaysSchemaInvalidToolClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	client.calls++
	return schemaToolResponse(fmt.Sprintf("call_schema_invalid_%d", client.calls), `{"value":"candidate","mode":"unsupported"}`), nil
}

func schemaToolResponse(callID, arguments string) llm.Response {
	return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: callID, Type: "function",
		Function: llm.ToolCallFunction{Name: "schema_test.check", Arguments: json.RawMessage(arguments)},
	}}}}
}

type oversizedThenSkeletonWriteClient struct {
	calls int
}

func (c *oversizedThenSkeletonWriteClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	c.calls++
	content := strings.Repeat("complete semantic section with markup <div>value</div>\n", 1200)
	callID := "call_oversized_write"
	if c.calls > 1 {
		content = "<html><body>__TMA_PLACEHOLDER_REPORT_001__</body></html>"
		callID = "call_skeleton_write"
	}
	arguments, _ := json.Marshal(map[string]string{"path": "report.html", "content": content})
	return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: callID, Type: "function", Function: llm.ToolCallFunction{Name: "default.write_file", Arguments: arguments},
	}}}}, nil
}

func (c *alwaysMalformedToolClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	c.calls++
	return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: "call_truncated_write", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.write_file", Arguments: json.RawMessage(`{"path":"report.html","content":"incomplete`),
		},
	}}}}, nil
}

func (c *malformedThenSensitiveToolClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_truncated_write", Type: "function", Function: llm.ToolCallFunction{
				Name: "default.write_file", Arguments: json.RawMessage(`{"path":"report.html","content":"incomplete`),
			},
		}}}}, nil
	}
	return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: "call_valid_write", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.write_file", Arguments: json.RawMessage(`{"path":"report.html","content":"short report"}`),
		},
	}}}}, nil
}

type skillsInstallLLMClient struct{}

func (*skillsInstallLLMClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{
		Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID:   "call_install_skill",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "skills.install",
					Arguments: json.RawMessage(`{"identifier":"code-review","title":"Code Review","content_text":"Review carefully."}`),
				},
			}},
		},
	}, nil
}

type offlineSkillsFlowLLMClient struct {
	requests []llm.Request
}

func (c *offlineSkillsFlowLLMClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	switch len(c.requests) {
	case 1:
		return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_preview_offline", Type: "function", Function: llm.ToolCallFunction{
				Name: "skills.preview", Arguments: json.RawMessage(`{"source":{"provider":"artifact","artifact_id":"art_skill_zip"}}`),
			},
		}}}}, nil
	case 2:
		return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_install_offline", Type: "function", Function: llm.ToolCallFunction{
				Name: "skills.install", Arguments: json.RawMessage(`{"identifier":"offline-review","source":{"provider":"artifact","artifact_id":"art_skill_zip"},"policy_revision":"policy-revision"}`),
			},
		}}}}, nil
	default:
		return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{
			Type: "text", Text: "offline skill installed; offer enable",
		}}}}, nil
	}
}

type recoverableSkillsFailureClient struct {
	requests  []llm.Request
	apiName   string
	arguments json.RawMessage
	finalText string
}

func (c *recoverableSkillsFailureClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_recoverable_skill_failure", Type: "function", Function: llm.ToolCallFunction{
				Name: "skills." + c.apiName, Arguments: c.arguments,
			},
		}}}}, nil
	}
	return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{
		Type: "text", Text: c.finalText,
	}}}}, nil
}

type countingSkillsToolService struct {
	previewCalls   int
	installCalls   int
	previewRequest tools.SkillsPreviewRequest
	installRequest tools.SkillsInstallRequest
	previewErr     error
	installErr     error
}

func (*countingSkillsToolService) Search(context.Context, tools.SkillsSearchRequest) (tools.SkillsSearchResponse, error) {
	return tools.SkillsSearchResponse{}, nil
}

func (*countingSkillsToolService) Inspect(context.Context, tools.SkillsInspectRequest) (tools.SkillsInspectResponse, error) {
	return tools.SkillsInspectResponse{}, nil
}

func (*countingSkillsToolService) Discover(context.Context, tools.SkillsDiscoverRequest) (tools.SkillsDiscoverResponse, error) {
	return tools.SkillsDiscoverResponse{}, nil
}

func (s *countingSkillsToolService) Preview(_ context.Context, request tools.SkillsPreviewRequest) (tools.SkillsPreviewResponse, error) {
	s.previewCalls++
	s.previewRequest = request
	if s.previewErr != nil {
		return tools.SkillsPreviewResponse{}, s.previewErr
	}
	return tools.SkillsPreviewResponse{
		Identifier: "offline-review", Source: request.Source, Revision: "zip-revision", InstallState: "new_install",
	}, nil
}

func (*countingSkillsToolService) ReadAsset(context.Context, tools.SkillsReadAssetRequest) (tools.SkillsReadAssetResponse, error) {
	return tools.SkillsReadAssetResponse{}, nil
}

func (s *countingSkillsToolService) Install(_ context.Context, request tools.SkillsInstallRequest) (tools.SkillsInstallResponse, error) {
	s.installCalls++
	s.installRequest = request
	if s.installErr != nil {
		return tools.SkillsInstallResponse{}, s.installErr
	}
	return tools.SkillsInstallResponse{}, nil
}

func (*countingSkillsToolService) Enable(context.Context, tools.SkillsEnableRequest) (tools.SkillsEnableResponse, error) {
	return tools.SkillsEnableResponse{}, nil
}

func (*countingSkillsToolService) Disable(context.Context, tools.SkillsDisableRequest) (tools.SkillsDisableResponse, error) {
	return tools.SkillsDisableResponse{}, nil
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

type visionRoutingClient struct {
	requests []llm.Request
}

func (c *visionRoutingClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, request)
	if request.Model == "fallback-vision" {
		return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "parsed image content"}}}, Usage: llm.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}}, nil
	}
	return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "main response"}}}, Usage: llm.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}}, nil
}

func messageHasImage(messages []llm.Message) bool {
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Type == "image_url" && part.ImageURL != nil {
				return true
			}
		}
	}
	return false
}

func messagesText(messages []llm.Message) string {
	var values []string
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Type == "text" {
				values = append(values, part.Text)
			}
		}
	}
	return strings.Join(values, "\n")
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

func assertMCPEventMetadata(t *testing.T, step Step) {
	t.Helper()
	if step.Data["tool_source"] != "mcp" || step.Data["manifest_type"] != "mcp_server" || step.Data["manifest_title"] != "Remote MCP" {
		t.Fatalf("expected mcp tool metadata, got %#v", step.Data)
	}
	if step.Data["mcp_transport"] != "streamable_http" || step.Data["mcp_protocol_version"] != "2025-06-18" || step.Data["mcp_oauth"] != true {
		t.Fatalf("expected mcp transport/protocol/oauth metadata, got %#v", step.Data)
	}
	if step.Data["mcp_tool_count"] != 1 {
		t.Fatalf("expected mcp tool count metadata, got %#v", step.Data)
	}
	capabilities, ok := step.Data["mcp_capabilities"].([]string)
	if !ok || strings.Join(capabilities, ",") != "resources,tools" {
		t.Fatalf("expected mcp capabilities metadata, got %#v", step.Data["mcp_capabilities"])
	}
	if _, exists := step.Data["mcp_url"]; exists {
		t.Fatalf("mcp event metadata should not include endpoint URL: %#v", step.Data)
	}
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
