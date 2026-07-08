package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

const DemoProtocolVersion = "tma.agent_runtime.demo.v1"

var ErrPendingIntervention = errors.New("pending tool intervention")

type Step struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Private map[string]any `json:"-"`
}

// TurnRequest 是 AgentRuntime 执行一轮对话所需的最小输入。
type TurnRequest struct {
	SessionID   string
	TurnID      string
	UserPayload json.RawMessage
	History     []managedagents.ConversationMessage
	Config      Config
	EmitStep    func(context.Context, Step) error
}

type TurnResult struct {
	AgentPayload          json.RawMessage
	Usage                 llm.Usage
	Provider              string
	ProviderType          string
	Model                 string
	SummaryText           string
	SummarySourceUntilSeq int64
}

type Config struct {
	LLMProvider           string
	LLMProviderType       string
	LLMModel              string
	LLMBaseURL            string
	LLMAPIKey             string
	ContextWindowTokens   int
	SummaryText           string
	SummarySourceUntilSeq int64
	System                string
	RuntimeSettings       json.RawMessage
	Tools                 json.RawMessage
	ModelTools            []llm.Tool
	Skills                json.RawMessage
	InterventionMode      string
	ToolRegistry          tools.Registry
	ToolExecutor          tools.Executor
	ToolExecutionContext  tools.ExecutionContext
}

// Runtime 负责把一次 user.message 转换为 agent.message payload。
// 后续 LLM loop、tool calling 和 sandbox 编排都会收敛到这一层。
type Runtime interface {
	RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error)
}

// DemoRuntime 是当前内置的最小 AgentRuntime。
// 它通过可替换的 LLM Client 生成回复；默认使用 FakeClient，不调用外部模型。
type DemoRuntime struct {
	Client         llm.Client
	Model          string
	ContextBuilder ContextBuilder
}

func (runtime DemoRuntime) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	default:
	}

	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeStarted,
		Message: "Demo runtime started.",
	}); err != nil {
		return TurnResult{}, err
	}

	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeThinking,
		Message: "Reading user message.",
	}); err != nil {
		return TurnResult{}, err
	}

	client := runtime.Client
	if client == nil {
		client = llm.FakeClient{}
	}
	provider := currentProvider(client, request.Config.LLMProvider)
	model := currentModel(client, defaultString(request.Config.LLMModel, runtime.Model))
	contextBuilder := runtime.ContextBuilder
	if contextBuilder == nil {
		contextBuilder = DefaultContextBuilder{
			MaxInputTokens: contextInputBudgetTokens(request.Config.ContextWindowTokens),
		}
	}
	contextResult, err := contextBuilder.Build(ContextBuildRequest{
		System:      request.Config.System,
		SummaryText: request.Config.SummaryText,
		History:     request.History,
		UserPayload: request.UserPayload,
		Tools:       request.Config.Tools,
		Skills:      request.Config.Skills,
	})
	if err != nil {
		return TurnResult{}, err
	}
	generatedSummaryText := ""
	generatedSummaryUntilSeq := int64(0)
	if contextResult.Truncated && contextResult.OmittedHistoryUntilSeq > request.Config.SummarySourceUntilSeq {
		compactResult, compactErr := compactContext(ctx, client, llm.Request{
			Provider:     provider,
			ProviderType: request.Config.LLMProviderType,
			Model:        model,
			BaseURL:      request.Config.LLMBaseURL,
			APIKey:       request.Config.LLMAPIKey,
		}, request, contextResult)
		if compactErr != nil {
			_ = emitStep(ctx, request, Step{
				Type:    managedagents.EventRuntimeContextCompactionFailed,
				Message: compactErr.Error(),
				Data: map[string]any{
					"mode":             "before_turn",
					"source_until_seq": contextResult.OmittedHistoryUntilSeq,
				},
			})
		} else if compactResult.SummaryText != "" {
			generatedSummaryText = compactResult.SummaryText
			generatedSummaryUntilSeq = compactResult.SourceUntilSeq
			rebuildHistory := historyAfterSeq(request.History, generatedSummaryUntilSeq)
			contextResult, err = contextBuilder.Build(ContextBuildRequest{
				System:      request.Config.System,
				SummaryText: generatedSummaryText,
				History:     rebuildHistory,
				UserPayload: request.UserPayload,
				Tools:       request.Config.Tools,
				Skills:      request.Config.Skills,
			})
			if err != nil {
				return TurnResult{}, err
			}
		}
	}

	llmRequest := llm.Request{
		Provider:     provider,
		ProviderType: request.Config.LLMProviderType,
		Model:        model,
		BaseURL:      request.Config.LLMBaseURL,
		APIKey:       request.Config.LLMAPIKey,
	}
	llmResponse, err := runtime.generateWithToolLoop(ctx, client, llmRequest, request, provider, model, contextResult, generatedSummaryText, generatedSummaryUntilSeq)
	if err != nil {
		return TurnResult{}, err
	}
	encoded, err := json.Marshal(map[string]any{
		"protocol_version": DemoProtocolVersion,
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return TurnResult{
			AgentPayload:          json.RawMessage(`{"protocol_version":"tma.agent_runtime.demo.v1","content":[{"type":"text","text":"Agent runtime received your message."}]}`),
			Usage:                 llmResponse.Usage,
			Provider:              provider,
			ProviderType:          request.Config.LLMProviderType,
			Model:                 model,
			SummaryText:           generatedSummaryText,
			SummarySourceUntilSeq: generatedSummaryUntilSeq,
		}, nil
	}
	if err := emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeCompleted,
		Message: "Demo runtime completed.",
	}); err != nil {
		return llmTurnResult(encoded, llmResponse.Usage, provider, request.Config.LLMProviderType, model, generatedSummaryText, generatedSummaryUntilSeq), err
	}
	return llmTurnResult(encoded, llmResponse.Usage, provider, request.Config.LLMProviderType, model, generatedSummaryText, generatedSummaryUntilSeq), nil
}

func llmTurnResult(payload json.RawMessage, usage llm.Usage, provider string, providerType string, model string, summaryText string, summaryUntilSeq int64) TurnResult {
	return TurnResult{
		AgentPayload:          append(json.RawMessage(nil), payload...),
		Usage:                 usage,
		Provider:              provider,
		ProviderType:          providerType,
		Model:                 model,
		SummaryText:           summaryText,
		SummarySourceUntilSeq: summaryUntilSeq,
	}
}

type toolCallEnvelope struct {
	ProtocolVersion string                 `json:"protocol_version"`
	ToolCalls       []toolCallEnvelopeCall `json:"tool_calls"`
}

type toolCallEnvelopeCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function toolCallFunction `json:"function,omitempty"`
}

type toolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolCallExecutionResult struct {
	Call   tools.Call
	Result tools.ExecutionResult
}

type contextCompactionResult struct {
	SummaryText    string
	SourceUntilSeq int64
}

func (runtime DemoRuntime) generateWithToolLoop(ctx context.Context, client llm.Client, baseRequest llm.Request, turnRequest TurnRequest, provider string, model string, contextResult ContextBuildResult, summaryText string, summaryUntilSeq int64) (llm.Response, error) {
	requestForLLM := baseRequest
	requestForLLM.Messages = append([]llm.Message(nil), contextResult.Messages...)
	requestForLLM.Tools = append([]llm.Tool(nil), turnRequest.Config.ModelTools...)

	for round := 0; round < 4; round++ {
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeLLMRequest,
			Message: "Sending request to LLM client.",
			Data: map[string]any{
				"provider":                 provider,
				"provider_type":            turnRequest.Config.LLMProviderType,
				"model":                    model,
				"base_url":                 turnRequest.Config.LLMBaseURL,
				"message_count":            len(requestForLLM.Messages),
				"history_count":            contextResult.HistoryMessageCount,
				"omitted_history_count":    contextResult.OmittedHistoryMessageCount,
				"estimated_token_count":    contextResult.EstimatedTokenCount,
				"context_truncated":        contextResult.Truncated,
				"summary_included":         contextResult.SummaryMessageIncluded,
				"summary_source_until_seq": defaultInt64(summaryUntilSeq, turnRequest.Config.SummarySourceUntilSeq),
				"tool_round":               round,
			},
		}); err != nil {
			return llm.Response{}, err
		}

		llmResponse, err := generateLLM(ctx, client, requestForLLM, turnRequest)
		if err != nil {
			return llm.Response{}, err
		}
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeLLMResponse,
			Message: "Received response from LLM client.",
			Data: map[string]any{
				"role":          llmResponse.Message.Role,
				"content_count": len(llmResponse.Message.Content),
				"usage":         llmResponse.Usage,
				"tool_round":    round,
			},
		}); err != nil {
			return llm.Response{}, err
		}

		toolCalls, ok := toolCallsFromLLMResponse(llmResponse)
		if !ok || len(toolCalls) == 0 {
			return llmResponse, nil
		}
		toolExecutor := turnRequest.Config.ToolExecutor
		if toolExecutor == nil {
			return llm.Response{}, fmt.Errorf("tool calls requested but tool executor is not configured")
		}

		assistantMessage := llm.Message{
			Role:      "assistant",
			Content:   []llm.ContentPart{{Type: "text", Text: contentPartsText(llmResponse.Message.Content)}},
			ToolCalls: append([]llm.ToolCall(nil), llmResponse.Message.ToolCalls...),
		}
		continuationMessages := append([]llm.Message(nil), requestForLLM.Messages...)
		continuationMessages = append(continuationMessages, assistantMessage)

		toolResults, err := runtime.executeToolCalls(ctx, turnRequest, toolExecutor, toolCalls, continuationMessages, round)
		if err != nil {
			return llm.Response{}, err
		}

		requestForLLM.Messages = append(requestForLLM.Messages, assistantMessage)
		for _, result := range toolResults {
			requestForLLM.Messages = append(requestForLLM.Messages, llm.Message{
				Role:       "tool",
				ToolCallID: result.Call.ID,
				Content:    []llm.ContentPart{{Type: "text", Text: resultText(result)}},
			})
		}
	}

	return llm.Response{}, fmt.Errorf("tool loop exceeded maximum rounds")
}

func (runtime DemoRuntime) executeToolCalls(ctx context.Context, turnRequest TurnRequest, executor tools.Executor, calls []tools.Call, continuationMessages []llm.Message, continuationRound int) ([]toolCallExecutionResult, error) {
	results := make([]toolCallExecutionResult, 0, len(calls))
	registry := turnRequest.Config.ToolRegistry
	policy := tools.InterventionPolicy{Mode: turnRequest.Config.InterventionMode}
	for _, toolCall := range calls {
		call := tools.NormalizeCall(toolCall)
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeToolCall,
			Message: "Received tool call request.",
			Data: map[string]any{
				"id":         call.ID,
				"identifier": call.Identifier,
				"api_name":   call.APIName,
				"arguments":  rawJSONObject(call.Arguments),
			},
		}); err != nil {
			return nil, err
		}

		executionContext := turnRequest.Config.ToolExecutionContext
		executionContext.SessionID = defaultString(executionContext.SessionID, turnRequest.SessionID)
		executionContext.TurnID = defaultString(executionContext.TurnID, turnRequest.TurnID)
		if executionContext.Deadline == nil {
			executionContext.Deadline = deadlineFromContext(ctx)
		}

		if manifest, api, ok := registry.GetAPI(call.Identifier, call.APIName); ok {
			decision := policy.Evaluate(manifest, api)
			if decision.Required && !decision.Allowed {
				if err := emitStep(ctx, turnRequest, Step{
					Type:    managedagents.EventRuntimeToolInterventionRequired,
					Message: "Tool call requires approval before execution.",
					Data: map[string]any{
						"id":                call.ID,
						"identifier":        call.Identifier,
						"api_name":          call.APIName,
						"arguments":         rawJSONObject(call.Arguments),
						"intervention_mode": decision.Mode,
						"reason":            decision.Reason,
					},
					Private: map[string]any{
						"continuation_messages": continuationMessages,
						"continuation_round":    continuationRound,
					},
				}); err != nil {
					return nil, err
				}
				return nil, ErrPendingIntervention
			}
			if decision.Required && decision.Allowed && decision.Mode == tools.InterventionModeApproveForMe {
				if err := emitStep(ctx, turnRequest, Step{
					Type:    managedagents.EventRuntimeToolInterventionApproved,
					Message: "Tool call auto-approved for execution.",
					Data: map[string]any{
						"id":                call.ID,
						"identifier":        call.Identifier,
						"api_name":          call.APIName,
						"arguments":         rawJSONObject(call.Arguments),
						"intervention_mode": decision.Mode,
						"reason":            decision.Reason,
						"approval_source":   "auto",
					},
				}); err != nil {
					return nil, err
				}
			}
		}

		executionResult, err := executor.Execute(ctx, call, executionContext)
		if err != nil {
			return nil, err
		}
		result := toolCallExecutionResult{Call: call, Result: executionResult}

		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeToolResult,
			Message: "Received tool result.",
			Data: map[string]any{
				"id":                   call.ID,
				"identifier":           call.Identifier,
				"api_name":             call.APIName,
				"content":              executionResult.Content,
				"state":                rawJSONObject(executionResult.State),
				"pending_intervention": executionResult.PendingIntervention,
				"error":                executionResult.Error,
				"success":              executionResult.Error == nil,
			},
		}); err != nil {
			return nil, err
		}
		results = append(results, result)
		if executionResult.Error != nil && executionResult.Error.Type != "human_intervention_required" {
			return results, fmt.Errorf("tool %s.%s failed: %s", call.Identifier, call.APIName, executionResult.Error.Message)
		}
	}
	return results, nil
}

func parseToolCallEnvelope(parts []llm.ContentPart) (toolCallEnvelope, bool) {
	text := strings.TrimSpace(contentPartsText(parts))
	if text == "" || !json.Valid([]byte(text)) {
		return toolCallEnvelope{}, false
	}
	var envelope toolCallEnvelope
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return toolCallEnvelope{}, false
	}
	if envelope.ProtocolVersion != tools.ToolCallProtocolVersion {
		return toolCallEnvelope{}, false
	}
	if len(envelope.ToolCalls) == 0 {
		return toolCallEnvelope{}, false
	}
	return envelope, true
}

func toolCallsFromLLMResponse(response llm.Response) ([]tools.Call, bool) {
	if len(response.Message.ToolCalls) > 0 {
		calls := make([]tools.Call, 0, len(response.Message.ToolCalls))
		for _, toolCall := range response.Message.ToolCalls {
			calls = append(calls, tools.NormalizeCall(tools.Call{
				ID:        toolCall.ID,
				APIName:   toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			}))
		}
		return calls, true
	}

	envelope, ok := parseToolCallEnvelope(response.Message.Content)
	if !ok || len(envelope.ToolCalls) == 0 {
		return nil, false
	}
	calls := make([]tools.Call, 0, len(envelope.ToolCalls))
	for _, envelopeCall := range envelope.ToolCalls {
		calls = append(calls, tools.NormalizeCall(tools.Call{
			ID:        envelopeCall.ID,
			APIName:   envelopeCall.Function.Name,
			Arguments: envelopeCall.Function.Arguments,
		}))
	}
	return calls, true
}

func resultText(result toolCallExecutionResult) string {
	return tools.ResultMessage(result.Result)
}

func rawJSONObject(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func deadlineFromContext(ctx context.Context) *time.Time {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	return &deadline
}

func compactContext(ctx context.Context, client llm.Client, baseRequest llm.Request, turnRequest TurnRequest, contextResult ContextBuildResult) (contextCompactionResult, error) {
	if err := emitStep(ctx, turnRequest, Step{
		Type:    managedagents.EventRuntimeContextCompacting,
		Message: "Compacting conversation context before LLM request.",
		Data: map[string]any{
			"mode":                  "before_turn",
			"source_until_seq":      contextResult.OmittedHistoryUntilSeq,
			"omitted_history_count": contextResult.OmittedHistoryMessageCount,
		},
	}); err != nil {
		return contextCompactionResult{}, err
	}

	baseRequest.Messages = []llm.Message{
		{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "Summarize the older conversation context for a coding agent. Preserve user requirements, architecture decisions, file paths, commands, failures, and fixes. Be concise.",
			}},
		},
		{
			Role: "user",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: compactionPrompt(turnRequest, contextResult.OmittedHistoryUntilSeq),
			}},
		},
	}
	response, err := client.Generate(ctx, baseRequest)
	if err != nil {
		return contextCompactionResult{}, err
	}
	summaryText := strings.TrimSpace(contentPartsText(response.Message.Content))
	if summaryText == "" {
		return contextCompactionResult{}, nil
	}
	if err := emitStep(ctx, turnRequest, Step{
		Type:    managedagents.EventRuntimeContextCompacted,
		Message: "Compacted conversation context.",
		Data: map[string]any{
			"mode":             "before_turn",
			"source_until_seq": contextResult.OmittedHistoryUntilSeq,
			"summary_chars":    len(summaryText),
		},
	}); err != nil {
		return contextCompactionResult{}, err
	}
	return contextCompactionResult{
		SummaryText:    summaryText,
		SourceUntilSeq: contextResult.OmittedHistoryUntilSeq,
	}, nil
}

func compactionPrompt(request TurnRequest, sourceUntilSeq int64) string {
	var builder strings.Builder
	if request.Config.SummaryText != "" {
		builder.WriteString("Existing summary:\n")
		builder.WriteString(request.Config.SummaryText)
		builder.WriteString("\n\n")
	}
	builder.WriteString("Messages to summarize up to event seq ")
	builder.WriteString(strconv.FormatInt(sourceUntilSeq, 10))
	builder.WriteString(":\n")
	for _, message := range request.History {
		if message.Seq > sourceUntilSeq {
			continue
		}
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		text := firstTextContent(message.Payload)
		if text == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(text)
		builder.WriteString("\n")
	}
	return builder.String()
}

func historyAfterSeq(history []managedagents.ConversationMessage, seq int64) []managedagents.ConversationMessage {
	filtered := make([]managedagents.ConversationMessage, 0, len(history))
	for _, message := range history {
		if message.Seq > seq {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func contentPartsText(parts []llm.ContentPart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

func defaultInt64(value int64, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
}

func contextInputBudgetTokens(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		contextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	return contextWindowTokens * managedagents.ContextBudgetRatioPercent / 100
}

func generateLLM(ctx context.Context, client llm.Client, llmRequest llm.Request, turnRequest TurnRequest) (llm.Response, error) {
	if len(llmRequest.Tools) > 0 {
		return client.Generate(ctx, llmRequest)
	}
	streamingClient, ok := client.(llm.StreamingClient)
	if !ok {
		return client.Generate(ctx, llmRequest)
	}

	return streamingClient.GenerateStream(ctx, llmRequest, func(delta llm.Delta) error {
		if delta.Text == "" {
			return nil
		}
		return emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeLLMDelta,
			Message: "Received streamed LLM text.",
			Data: map[string]any{
				"index": delta.Index,
				"text":  delta.Text,
			},
		})
	})
}

func emitStep(ctx context.Context, request TurnRequest, step Step) error {
	if request.EmitStep == nil {
		return nil
	}
	return request.EmitStep(ctx, step)
}

type llmConfigSource interface {
	CurrentConfig() (string, string)
}

// currentModel 优先使用显式 Runtime 配置；没有显式配置时读取 LLM Manager 当前模型。
func currentModel(client llm.Client, fallback string) string {
	if fallback != "" {
		return fallback
	}
	if source, ok := client.(llmConfigSource); ok {
		_, model := source.CurrentConfig()
		if model != "" {
			return model
		}
	}
	return llm.DefaultModel
}

func currentProvider(client llm.Client, fallback string) string {
	if fallback != "" {
		return fallback
	}
	if source, ok := client.(llmConfigSource); ok {
		provider, _ := source.CurrentConfig()
		if provider != "" {
			return provider
		}
	}
	return llm.ProviderFake
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
