package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

const DemoProtocolVersion = "tma.agent_runtime.demo.v1"

var ErrPendingIntervention = errors.New("pending tool intervention")

const (
	defaultCompactionPromptMaxChars  = 60000
	defaultCompactionSummaryMaxChars = 12000
	defaultExistingSummaryMaxChars   = 12000
	compactionPromptReservedMetadata = 2000
)

type Step struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Private map[string]any `json:"-"`
}

// TurnRequest 是 AgentRuntime 执行一轮对话所需的最小输入。
type TurnRequest struct {
	SessionID          string
	TurnID             string
	UserPayload        json.RawMessage
	History            []managedagents.ConversationMessage
	ResumeIntervention *InterventionResume
	Config             Config
	EmitStep           func(context.Context, Step) error
}

type InterventionResume struct {
	Call              tools.Call
	Status            string
	DecisionReason    string
	Continuation      []llm.Message
	ContinuationRound int
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
	WorkspaceID           string
	EnvironmentID         string
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
	MaxToolRounds  int
}

func (runtime DemoRuntime) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	select {
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	default:
	}
	if request.ResumeIntervention != nil {
		return runtime.resumeIntervention(ctx, request)
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
	contextBudget := contextBudgetFromSettings(request.Config.ContextWindowTokens, request.Config.RuntimeSettings)
	pinnedContext := pinnedContextFromSettings(request.Config.RuntimeSettings)
	contextBuilder := runtime.ContextBuilder
	if contextBuilder == nil {
		contextBuilder = DefaultContextBuilder{
			MaxInputTokens: contextBudget.MaxInputTokens,
		}
	}
	contextResult, err := contextBuilder.Build(ContextBuildRequest{
		System:                  request.Config.System,
		PinnedContext:           pinnedContext,
		SummaryText:             request.Config.SummaryText,
		History:                 request.History,
		UserPayload:             request.UserPayload,
		Tools:                   request.Config.Tools,
		ModelTools:              request.Config.ModelTools,
		Skills:                  request.Config.Skills,
		ContextWindowTokens:     contextBudget.ContextWindowTokens,
		InputBudgetRatioPercent: contextBudget.InputBudgetRatioPercent,
		ReservedOutputTokens:    contextBudget.ReservedOutputTokens,
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
				System:                  request.Config.System,
				PinnedContext:           pinnedContext,
				SummaryText:             generatedSummaryText,
				History:                 rebuildHistory,
				UserPayload:             request.UserPayload,
				Tools:                   request.Config.Tools,
				ModelTools:              request.Config.ModelTools,
				Skills:                  request.Config.Skills,
				ContextWindowTokens:     contextBudget.ContextWindowTokens,
				InputBudgetRatioPercent: contextBudget.InputBudgetRatioPercent,
				ReservedOutputTokens:    contextBudget.ReservedOutputTokens,
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
	llmResponse, err := runtime.generateWithToolLoop(ctx, client, llmRequest, request, provider, model, contextResult, generatedSummaryText, generatedSummaryUntilSeq, 0)
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

func (runtime DemoRuntime) resumeIntervention(ctx context.Context, request TurnRequest) (TurnResult, error) {
	resume := request.ResumeIntervention
	if resume == nil {
		return TurnResult{}, errors.New("intervention resume is required")
	}

	client := runtime.Client
	if client == nil {
		client = llm.FakeClient{}
	}
	provider := currentProvider(client, request.Config.LLMProvider)
	model := currentModel(client, defaultString(request.Config.LLMModel, runtime.Model))
	call := tools.NormalizeCall(resume.Call)
	executionResult, err := runtime.resolveInterventionResult(ctx, request, call, resume.Status, resume.DecisionReason)
	if err != nil {
		return TurnResult{}, err
	}

	if len(resume.Continuation) == 0 {
		if resume.Status == managedagents.InterventionStatusRejected {
			return TurnResult{}, errors.New(executionResult.Error.Message)
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"protocol_version": DemoProtocolVersion,
			"content": []llm.ContentPart{{
				Type: "text",
				Text: tools.ContextResultMessage(executionResult, toolResultContextOptions(request.Config.RuntimeSettings)),
			}},
		})
		if marshalErr != nil {
			return TurnResult{}, marshalErr
		}
		if err := emitStep(ctx, request, Step{Type: managedagents.EventRuntimeCompleted, Message: "Intervention resume completed."}); err != nil {
			return TurnResult{AgentPayload: payload, Provider: provider, ProviderType: request.Config.LLMProviderType, Model: model}, err
		}
		return TurnResult{AgentPayload: payload, Provider: provider, ProviderType: request.Config.LLMProviderType, Model: model}, nil
	}

	messages := append([]llm.Message(nil), resume.Continuation...)
	messages = append(messages, llm.Message{
		Role:       "tool",
		ToolCallID: call.ID,
		Content: []llm.ContentPart{{
			Type: "text",
			Text: tools.ContextResultMessage(executionResult, toolResultContextOptions(request.Config.RuntimeSettings)),
		}},
	})
	llmResponse, err := runtime.generateWithToolLoop(ctx, client, llm.Request{
		Provider:     provider,
		ProviderType: request.Config.LLMProviderType,
		Model:        model,
		BaseURL:      request.Config.LLMBaseURL,
		APIKey:       request.Config.LLMAPIKey,
	}, request, provider, model, ContextBuildResult{Messages: messages}, "", 0, resume.ContinuationRound+1)
	if err != nil {
		return TurnResult{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"protocol_version": DemoProtocolVersion,
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return TurnResult{Usage: llmResponse.Usage, Provider: provider, ProviderType: request.Config.LLMProviderType, Model: model}, err
	}
	if err := emitStep(ctx, request, Step{Type: managedagents.EventRuntimeCompleted, Message: "Intervention resume completed."}); err != nil {
		return llmTurnResult(payload, llmResponse.Usage, provider, request.Config.LLMProviderType, model, "", 0), err
	}
	return llmTurnResult(payload, llmResponse.Usage, provider, request.Config.LLMProviderType, model, "", 0), nil
}

func (runtime DemoRuntime) resolveInterventionResult(ctx context.Context, request TurnRequest, call tools.Call, status string, decisionReason string) (tools.ExecutionResult, error) {
	if status == managedagents.InterventionStatusRejected {
		message := "Tool call rejected by user."
		if decisionReason != "" {
			message += " Reason: " + decisionReason
		}
		result := tools.ExecutionResult{
			ID:         call.ID,
			Identifier: call.Identifier,
			APIName:    call.APIName,
			Content:    message,
			State:      mustMarshalRaw(map[string]any{"rejected": true, "decision_reason": decisionReason}),
			Error:      &tools.ExecutionError{Type: "tool_rejected_by_user", Message: message},
		}
		if err := emitInterventionToolResult(ctx, request, call, result, decisionReason, 0); err != nil {
			return tools.ExecutionResult{}, err
		}
		return result, nil
	}
	if status != managedagents.InterventionStatusApproved {
		return tools.ExecutionResult{}, fmt.Errorf("unsupported intervention resume status %q", status)
	}
	if request.Config.ToolExecutor == nil {
		return tools.ExecutionResult{}, errors.New("approved intervention requires a tool executor")
	}
	executionContext := request.Config.ToolExecutionContext
	executionContext.WorkspaceID = defaultString(executionContext.WorkspaceID, request.Config.WorkspaceID)
	executionContext.SessionID = defaultString(executionContext.SessionID, request.SessionID)
	executionContext.EnvironmentID = defaultString(executionContext.EnvironmentID, request.Config.EnvironmentID)
	executionContext.TurnID = defaultString(executionContext.TurnID, request.TurnID)
	if executionContext.Deadline == nil {
		executionContext.Deadline = deadlineFromContext(ctx)
	}
	startedAt := time.Now()
	result, err := request.Config.ToolExecutor.Execute(ctx, call, executionContext)
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	if err := emitInterventionToolResult(ctx, request, call, result, decisionReason, time.Since(startedAt)); err != nil {
		return tools.ExecutionResult{}, err
	}
	if result.Error != nil {
		return result, fmt.Errorf("tool %s.%s failed: %s", call.Identifier, call.APIName, result.Error.Message)
	}
	return result, nil
}

func emitInterventionToolResult(ctx context.Context, request TurnRequest, call tools.Call, result tools.ExecutionResult, decisionReason string, duration time.Duration) error {
	data := tools.ObservableResultData(result, toolResultContextOptions(request.Config.RuntimeSettings))
	data["id"] = call.ID
	data["identifier"] = call.Identifier
	data["api_name"] = call.APIName
	data["approval_source"] = "user"
	data["duration_ms"] = duration.Milliseconds()
	if decisionReason != "" {
		data["decision_reason"] = decisionReason
	}
	return emitStep(ctx, request, Step{
		Type:    managedagents.EventRuntimeToolResult,
		Message: "Received decided tool result.",
		Data:    data,
	})
}

func mustMarshalRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
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

var (
	seedToolCallPattern  = regexp.MustCompile(`(?is)<seed:tool_call\b[^>]*>.*?</seed:tool_call>`)
	seedFunctionPattern  = regexp.MustCompile(`(?is)<function\b([^>]*)>(.*?)</function>`)
	seedParameterPattern = regexp.MustCompile(`(?is)<parameter\b([^>]*)>(.*?)</parameter>`)
	seedAttributePattern = regexp.MustCompile(`(?is)([a-zA-Z0-9_:-]+)\s*=\s*"([^"]*)"`)
	seedTagPattern       = regexp.MustCompile(`(?is)<[^>]+>`)
)

type contextCompactionResult struct {
	SummaryText    string
	SourceUntilSeq int64
}

type contextBudgetLimits struct {
	ContextWindowTokens     int
	InputBudgetRatioPercent int
	MaxInputTokens          int
	ReservedOutputTokens    int
}

func (runtime DemoRuntime) generateWithToolLoop(ctx context.Context, client llm.Client, baseRequest llm.Request, turnRequest TurnRequest, provider string, model string, contextResult ContextBuildResult, summaryText string, summaryUntilSeq int64, startRound int) (llm.Response, error) {
	requestForLLM := baseRequest
	requestForLLM.Messages = append([]llm.Message(nil), contextResult.Messages...)
	requestForLLM.Tools = append([]llm.Tool(nil), turnRequest.Config.ModelTools...)

	for round := startRound; ; round++ {
		if runtime.MaxToolRounds > 0 && round >= runtime.MaxToolRounds {
			return llm.Response{}, fmt.Errorf("tool loop exceeded maximum rounds")
		}
		messageTokens := estimateMessagesTokens(requestForLLM.Messages)
		toolSchemaTokens := estimateToolsTokens(requestForLLM.Tools)
		estimatedRequestTokens := messageTokens + toolSchemaTokens
		contextBudget := contextResult.Budget
		contextBudget.EstimatedTokenCount = estimatedRequestTokens
		contextBudget.MessageTokens = messageTokens
		contextBudget.ToolSchemaTokens = toolSchemaTokens
		contextBudget.ToolSchemaCount = len(requestForLLM.Tools)
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeLLMRequest,
			Message: "Sending request to LLM client.",
			Data: map[string]any{
				"provider":                      provider,
				"provider_type":                 turnRequest.Config.LLMProviderType,
				"model":                         model,
				"base_url":                      turnRequest.Config.LLMBaseURL,
				"message_count":                 len(requestForLLM.Messages),
				"history_count":                 contextResult.HistoryMessageCount,
				"omitted_history_count":         contextResult.OmittedHistoryMessageCount,
				"estimated_token_count":         estimatedRequestTokens,
				"estimated_message_tokens":      messageTokens,
				"estimated_tool_schema_tokens":  toolSchemaTokens,
				"tool_schema_count":             len(requestForLLM.Tools),
				"initial_estimated_token_count": contextResult.EstimatedTokenCount,
				"context_budget":                contextBudget,
				"context_truncated":             contextResult.Truncated,
				"pinned_context_included":       contextResult.PinnedContextIncluded,
				"summary_included":              contextResult.SummaryMessageIncluded,
				"summary_source_until_seq":      defaultInt64(summaryUntilSeq, turnRequest.Config.SummarySourceUntilSeq),
				"tool_round":                    round,
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
				Content:    []llm.ContentPart{{Type: "text", Text: resultTextForContext(result, turnRequest.Config.RuntimeSettings)}},
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
		executionContext.WorkspaceID = defaultString(executionContext.WorkspaceID, turnRequest.Config.WorkspaceID)
		executionContext.SessionID = defaultString(executionContext.SessionID, turnRequest.SessionID)
		executionContext.EnvironmentID = defaultString(executionContext.EnvironmentID, turnRequest.Config.EnvironmentID)
		executionContext.TurnID = defaultString(executionContext.TurnID, turnRequest.TurnID)
		if executionContext.Deadline == nil {
			executionContext.Deadline = deadlineFromContext(ctx)
		}

		if manifest, api, ok := registry.GetAPI(call.Identifier, call.APIName); ok {
			decision := policy.EvaluateCall(manifest, api, call, executionContext)
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
			Data:    tools.ObservableResultData(executionResult, toolResultContextOptions(turnRequest.Config.RuntimeSettings)),
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

func parseSeedToolCallEnvelope(parts []llm.ContentPart) (toolCallEnvelope, bool) {
	text := strings.TrimSpace(contentPartsText(parts))
	if text == "" || !strings.Contains(text, "<seed:tool_call") {
		return toolCallEnvelope{}, false
	}
	matches := seedToolCallPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return toolCallEnvelope{}, false
	}
	envelope := toolCallEnvelope{ProtocolVersion: tools.ToolCallProtocolVersion}
	for index, block := range matches {
		functionMatch := seedFunctionPattern.FindStringSubmatch(block)
		if len(functionMatch) < 3 {
			continue
		}
		functionAttrs := seedAttributes(functionMatch[1])
		name := strings.TrimSpace(functionAttrs["name"])
		if name == "" {
			continue
		}
		args := map[string]any{}
		for _, parameterMatch := range seedParameterPattern.FindAllStringSubmatch(functionMatch[2], -1) {
			if len(parameterMatch) < 3 {
				continue
			}
			parameterAttrs := seedAttributes(parameterMatch[1])
			parameterName := strings.TrimSpace(parameterAttrs["name"])
			if parameterName == "" {
				continue
			}
			args[parameterName] = seedParameterValue(parameterAttrs, parameterMatch[2])
		}
		rawArgs, err := json.Marshal(args)
		if err != nil {
			continue
		}
		envelope.ToolCalls = append(envelope.ToolCalls, toolCallEnvelopeCall{
			ID:   "call_seed_" + strconv.Itoa(index+1),
			Type: "function",
			Function: toolCallFunction{
				Name:      name,
				Arguments: rawArgs,
			},
		})
	}
	if len(envelope.ToolCalls) == 0 {
		return toolCallEnvelope{}, false
	}
	return envelope, true
}

func seedAttributes(text string) map[string]string {
	attrs := map[string]string{}
	for _, match := range seedAttributePattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		attrs[strings.ToLower(strings.TrimSpace(match[1]))] = html.UnescapeString(match[2])
	}
	return attrs
}

func seedParameterValue(attrs map[string]string, raw string) any {
	text := strings.TrimSpace(seedTagPattern.ReplaceAllString(raw, ""))
	text = html.UnescapeString(text)
	if seedAttrTrue(attrs, "json") {
		var value any
		if err := json.Unmarshal([]byte(text), &value); err == nil {
			return value
		}
		return text
	}
	if seedAttrTrue(attrs, "integer") || seedAttrTrue(attrs, "int") {
		if value, err := strconv.ParseInt(text, 10, 64); err == nil {
			return value
		}
		return text
	}
	if seedAttrTrue(attrs, "number") || seedAttrTrue(attrs, "float") {
		if value, err := strconv.ParseFloat(text, 64); err == nil {
			return value
		}
		return text
	}
	if seedAttrTrue(attrs, "boolean") || seedAttrTrue(attrs, "bool") {
		if value, err := strconv.ParseBool(strings.ToLower(text)); err == nil {
			return value
		}
		return text
	}
	return text
}

func seedAttrTrue(attrs map[string]string, key string) bool {
	value := strings.ToLower(strings.TrimSpace(attrs[key]))
	return value == "true" || value == "1" || value == "yes"
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
	if !ok {
		envelope, ok = parseSeedToolCallEnvelope(response.Message.Content)
	}
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

func resultTextForContext(result toolCallExecutionResult, runtimeSettings json.RawMessage) string {
	return tools.ContextResultMessage(result.Result, toolResultContextOptions(runtimeSettings))
}

func toolResultContextOptions(runtimeSettings json.RawMessage) tools.ResultContextOptions {
	return tools.ResultContextOptions{MaxContentChars: toolResultContextMaxChars(runtimeSettings)}
}

func toolResultContextMaxChars(runtimeSettings json.RawMessage) int {
	var settings struct {
		ToolResultContextMaxChars int `json:"tool_result_context_max_chars"`
		ToolResultMaxChars        int `json:"tool_result_max_chars"`
		ToolResultMaxLength       int `json:"tool_result_max_length"`
	}
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		switch {
		case settings.ToolResultContextMaxChars > 0:
			return settings.ToolResultContextMaxChars
		case settings.ToolResultMaxChars > 0:
			return settings.ToolResultMaxChars
		case settings.ToolResultMaxLength > 0:
			return settings.ToolResultMaxLength
		}
	}
	return tools.DefaultResultContextMaxChars
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
	promptText := compactionPrompt(turnRequest, contextResult.OmittedHistoryUntilSeq)
	promptMaxChars := compactionPromptMaxChars(turnRequest.Config.RuntimeSettings)
	summaryMaxChars := compactionSummaryMaxChars(turnRequest.Config.RuntimeSettings)
	if err := emitStep(ctx, turnRequest, Step{
		Type:    managedagents.EventRuntimeContextCompacting,
		Message: "Compacting conversation context before LLM request.",
		Data: map[string]any{
			"mode":                  "before_turn",
			"source_until_seq":      contextResult.OmittedHistoryUntilSeq,
			"omitted_history_count": contextResult.OmittedHistoryMessageCount,
			"prompt_chars":          len([]rune(promptText)),
			"prompt_max_chars":      promptMaxChars,
		},
	}); err != nil {
		return contextCompactionResult{}, err
	}

	baseRequest.Messages = []llm.Message{
		{
			Role: "system",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: "Summarize the older conversation context for a coding agent. Return a concise structured summary with these sections: Objective, User constraints, Completed work, Files touched, Commands and results, Open issues, Next steps, Pinned facts. Preserve concrete file paths, commands, failures, and fixes.",
			}},
		},
		{
			Role: "user",
			Content: []llm.ContentPart{{
				Type: "text",
				Text: promptText,
			}},
		},
	}
	response, err := client.Generate(ctx, baseRequest)
	if err != nil {
		return contextCompactionResult{}, err
	}
	rawSummaryText := strings.TrimSpace(contentPartsText(response.Message.Content))
	summaryText := normalizeCompactionSummaryWithLimit(rawSummaryText, summaryMaxChars)
	if summaryText == "" {
		return contextCompactionResult{}, nil
	}
	if err := emitStep(ctx, turnRequest, Step{
		Type:    managedagents.EventRuntimeContextCompacted,
		Message: "Compacted conversation context.",
		Data: map[string]any{
			"mode":              "before_turn",
			"source_until_seq":  contextResult.OmittedHistoryUntilSeq,
			"summary_chars":     len(summaryText),
			"raw_summary_chars": len(rawSummaryText),
			"summary_truncated": len([]rune(summaryText)) < len([]rune(rawSummaryText)),
			"summary_max_chars": summaryMaxChars,
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
	return compactionPromptWithLimit(request, sourceUntilSeq, compactionPromptMaxChars(request.Config.RuntimeSettings))
}

func compactionPromptWithLimit(request TurnRequest, sourceUntilSeq int64, maxChars int) string {
	if maxChars <= 0 {
		maxChars = defaultCompactionPromptMaxChars
	}
	var builder strings.Builder
	if request.Config.SummaryText != "" {
		summaryMaxChars := minInt(defaultExistingSummaryMaxChars, maxChars/3)
		builder.WriteString("Existing summary:\n")
		builder.WriteString(truncateForPrompt(request.Config.SummaryText, summaryMaxChars))
		builder.WriteString("\n\n")
	}
	builder.WriteString("Messages to summarize up to event seq ")
	builder.WriteString(strconv.FormatInt(sourceUntilSeq, 10))
	builder.WriteString(":\n")
	prefix := builder.String()

	lines := compactionMessageLines(request.History, sourceUntilSeq)
	remaining := maxChars - len([]rune(prefix)) - compactionPromptReservedMetadata
	if remaining < 0 {
		remaining = 0
	}
	selected, omitted := newestLinesWithinLimit(lines, remaining)
	builder.Reset()
	builder.WriteString(prefix)
	if omitted > 0 {
		builder.WriteString("[")
		builder.WriteString(strconv.Itoa(omitted))
		builder.WriteString(" older messages omitted from this compaction prompt because of prompt budget. Preserve the existing summary for earlier context.]\n")
	}
	for _, line := range selected {
		builder.WriteString(line)
	}
	return truncateForPrompt(builder.String(), maxChars)
}

func normalizeCompactionSummary(summaryText string) string {
	return normalizeCompactionSummaryWithLimit(summaryText, defaultCompactionSummaryMaxChars)
}

func normalizeCompactionSummaryWithLimit(summaryText string, maxChars int) string {
	summaryText = strings.TrimSpace(summaryText)
	if summaryText == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = defaultCompactionSummaryMaxChars
	}
	return strings.TrimSpace(truncateForPrompt(summaryText, maxChars))
}

func compactionMessageLines(history []managedagents.ConversationMessage, sourceUntilSeq int64) []string {
	lines := make([]string, 0, len(history))
	for _, message := range history {
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
		lines = append(lines, message.Role+": "+text+"\n")
	}
	return lines
}

func newestLinesWithinLimit(lines []string, maxChars int) ([]string, int) {
	if maxChars <= 0 {
		return nil, len(lines)
	}
	selectedNewestFirst := make([]string, 0, len(lines))
	used := 0
	for index := len(lines) - 1; index >= 0; index-- {
		lineChars := len([]rune(lines[index]))
		if used+lineChars > maxChars {
			return reverseStrings(selectedNewestFirst), index + 1
		}
		used += lineChars
		selectedNewestFirst = append(selectedNewestFirst, lines[index])
	}
	return reverseStrings(selectedNewestFirst), 0
}

func reverseStrings(values []string) []string {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
	return values
}

func truncateForPrompt(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars < 120 {
		return string(runes[:maxChars])
	}
	noticeTemplate := "\n[Text truncated for compaction prompt budget: %d characters omitted.]\n"
	notice := fmt.Sprintf(noticeTemplate, len(runes)-maxChars)
	available := maxChars - len([]rune(notice))
	if available <= 0 {
		return string(runes[:maxChars])
	}
	headChars := available * 2 / 3
	tailChars := available - headChars
	notice = fmt.Sprintf(noticeTemplate, len(runes)-headChars-tailChars)
	return string(runes[:headChars]) + notice + string(runes[len(runes)-tailChars:])
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
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

func contextInputBudgetTokens(contextWindowTokens int, runtimeSettings json.RawMessage) int {
	return contextBudgetFromSettings(contextWindowTokens, runtimeSettings).MaxInputTokens
}

func contextBudgetFromSettings(contextWindowTokens int, runtimeSettings json.RawMessage) contextBudgetLimits {
	if contextWindowTokens <= 0 {
		contextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	ratio := contextBudgetRatioPercent(runtimeSettings)
	maxInputTokens := contextWindowTokens * ratio / 100
	reservedOutputTokens := contextOutputReserveTokens(runtimeSettings)
	if reservedOutputTokens > 0 {
		if reservedOutputTokens >= contextWindowTokens {
			reservedOutputTokens = contextWindowTokens - 1
		}
		maxInputTokens = minInt(maxInputTokens, contextWindowTokens-reservedOutputTokens)
	} else {
		reservedOutputTokens = contextWindowTokens - maxInputTokens
	}
	if maxInputTokens < 1 {
		maxInputTokens = 1
	}
	return contextBudgetLimits{
		ContextWindowTokens:     contextWindowTokens,
		InputBudgetRatioPercent: ratio,
		MaxInputTokens:          maxInputTokens,
		ReservedOutputTokens:    reservedOutputTokens,
	}
}

func contextBudgetRatioPercent(runtimeSettings json.RawMessage) int {
	var settings struct {
		ContextInputBudgetRatioPercent int `json:"context_input_budget_ratio_percent"`
		ContextBudgetRatioPercent      int `json:"context_budget_ratio_percent"`
	}
	ratio := managedagents.ContextBudgetRatioPercent
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		switch {
		case settings.ContextInputBudgetRatioPercent > 0:
			ratio = settings.ContextInputBudgetRatioPercent
		case settings.ContextBudgetRatioPercent > 0:
			ratio = settings.ContextBudgetRatioPercent
		}
	}
	if ratio < 10 {
		return 10
	}
	if ratio > 95 {
		return 95
	}
	return ratio
}

func contextOutputReserveTokens(runtimeSettings json.RawMessage) int {
	var settings struct {
		ContextOutputReserveTokens int `json:"context_output_reserve_tokens"`
		OutputReserveTokens        int `json:"output_reserve_tokens"`
		OutputTokenReserve         int `json:"output_token_reserve"`
	}
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		switch {
		case settings.ContextOutputReserveTokens > 0:
			return settings.ContextOutputReserveTokens
		case settings.OutputReserveTokens > 0:
			return settings.OutputReserveTokens
		case settings.OutputTokenReserve > 0:
			return settings.OutputTokenReserve
		}
	}
	return 0
}

func pinnedContextFromSettings(runtimeSettings json.RawMessage) string {
	var settings struct {
		PinnedContext    any `json:"pinned_context"`
		ProtectedContext any `json:"protected_context"`
	}
	if len(runtimeSettings) == 0 || json.Unmarshal(runtimeSettings, &settings) != nil {
		return ""
	}
	return formatPinnedContext(defaultAny(settings.PinnedContext, settings.ProtectedContext))
}

func formatPinnedContext(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		lines := make([]string, 0, len(typed))
		for _, item := range typed {
			line := strings.TrimSpace(formatPinnedContext(item))
			if line != "" {
				lines = append(lines, "- "+line)
			}
		}
		return strings.Join(lines, "\n")
	default:
		encoded, err := json.MarshalIndent(typed, "", "  ")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(encoded))
	}
}

func defaultAny(value any, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

func compactionPromptMaxChars(runtimeSettings json.RawMessage) int {
	var settings struct {
		CompactionPromptMaxChars  int `json:"compaction_prompt_max_chars"`
		ContextCompactionMaxChars int `json:"context_compaction_max_chars"`
	}
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		switch {
		case settings.CompactionPromptMaxChars > 0:
			return settings.CompactionPromptMaxChars
		case settings.ContextCompactionMaxChars > 0:
			return settings.ContextCompactionMaxChars
		}
	}
	return defaultCompactionPromptMaxChars
}

func compactionSummaryMaxChars(runtimeSettings json.RawMessage) int {
	var settings struct {
		CompactionSummaryMaxChars int `json:"compaction_summary_max_chars"`
		SummaryMaxChars           int `json:"summary_max_chars"`
	}
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		switch {
		case settings.CompactionSummaryMaxChars > 0:
			return settings.CompactionSummaryMaxChars
		case settings.SummaryMaxChars > 0:
			return settings.SummaryMaxChars
		}
	}
	return defaultCompactionSummaryMaxChars
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
