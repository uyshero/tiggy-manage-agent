package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

const DemoProtocolVersion = "tma.agent_runtime.demo.v1"

var ErrPendingIntervention = errors.New("pending tool intervention")
var ErrPendingHumanInput = errors.New("pending human input")
var ErrVisionModelNotConfigured = errors.New("image attachments require a text+image model or a configured default vision model")

const (
	defaultCompactionPromptMaxChars  = 60000
	defaultCompactionSummaryMaxChars = 12000
	defaultExistingSummaryMaxChars   = 12000
	defaultToolResultTotalMaxChars   = 24000
	compactionPromptReservedMetadata = 2000
	defaultMaxLLMOutputTokens        = 16384
	maxInvalidToolArgumentRetries    = 2
)

type Step struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Private map[string]any `json:"-"`
}

// StreamEvent is transient presentation data. It must never be persisted as a runtime step.
type StreamEvent struct {
	Index     int
	ToolRound int
	Text      string
}

// TurnRequest 是 AgentRuntime 执行一轮对话所需的最小输入。
type TurnRequest struct {
	SessionID          string
	TurnID             string
	UserPayload        json.RawMessage
	History            []managedagents.ConversationMessage
	ImageParts         []llm.ContentPart
	ResumeIntervention *InterventionResume
	Config             Config
	EmitStep           func(context.Context, Step) error
	EmitStream         func(StreamEvent)
}

type InterventionResume struct {
	Call              tools.Call
	Kind              string
	Status            string
	DecisionReason    string
	Response          json.RawMessage
	Continuation      []llm.Message
	ContinuationRound int
	ContinuationState json.RawMessage
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
	LLMCapabilityType     string
	VisionLLMProvider     string
	VisionLLMProviderType string
	VisionLLMModel        string
	VisionLLMBaseURL      string
	VisionLLMAPIKey       string
	ContextWindowTokens   int
	SummaryText           string
	SummarySourceUntilSeq int64
	TaskPlanContext       string
	System                string
	RuntimeSettings       json.RawMessage
	Tools                 json.RawMessage
	ModelTools            []llm.Tool
	Skills                json.RawMessage
	SkillsResolved        bool
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
	CompletionGate CompletionGate
	MaxToolRounds  int
	Now            func() time.Time
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
	pinnedContext := combinePinnedContext(pinnedContextFromSettings(request.Config.RuntimeSettings), request.Config.TaskPlanContext)
	currentDateContext := buildCurrentDateContext(runtime.currentTime())
	currentUserImages := request.ImageParts
	currentUserSupplement := ""
	visionUsage := llm.Usage{}
	if len(currentUserImages) > 0 && !managedagents.LLMModelSupportsVision(request.Config.LLMCapabilityType) {
		if request.Config.VisionLLMProvider == "" || request.Config.VisionLLMModel == "" {
			return TurnResult{}, ErrVisionModelNotConfigured
		}
		analysis, usage, visionErr := runtime.analyzeImages(ctx, client, request)
		if visionErr != nil {
			return TurnResult{}, visionErr
		}
		currentUserSupplement = "Vision model analysis of the uploaded image(s):\n" + analysis
		currentUserImages = nil
		visionUsage = usage
	}
	contextBuilder := runtime.ContextBuilder
	if contextBuilder == nil {
		contextBuilder = DefaultContextBuilder{
			MaxInputTokens: contextBudget.MaxInputTokens,
		}
	}
	renderedSkills := request.Config.Skills
	if !request.Config.SkillsResolved {
		skillsResult, err := skills.Resolve(request.Config.Skills)
		if err != nil {
			return TurnResult{}, fmt.Errorf("resolve skills: %w", err)
		}
		renderedSkills = skillsResult.Rendered
	}
	contextResult, err := contextBuilder.Build(ContextBuildRequest{
		System:                  request.Config.System,
		CurrentDateContext:      currentDateContext,
		PinnedContext:           pinnedContext,
		SummaryText:             request.Config.SummaryText,
		History:                 request.History,
		UserPayload:             request.UserPayload,
		CurrentUserImages:       currentUserImages,
		CurrentUserSupplement:   currentUserSupplement,
		Tools:                   request.Config.Tools,
		ModelTools:              request.Config.ModelTools,
		Skills:                  renderedSkills,
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
				CurrentDateContext:      currentDateContext,
				PinnedContext:           pinnedContext,
				SummaryText:             generatedSummaryText,
				History:                 rebuildHistory,
				UserPayload:             request.UserPayload,
				CurrentUserImages:       currentUserImages,
				CurrentUserSupplement:   currentUserSupplement,
				Tools:                   request.Config.Tools,
				ModelTools:              request.Config.ModelTools,
				Skills:                  renderedSkills,
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
		Provider:        provider,
		ProviderType:    request.Config.LLMProviderType,
		Model:           model,
		BaseURL:         request.Config.LLMBaseURL,
		APIKey:          request.Config.LLMAPIKey,
		MaxOutputTokens: maxLLMOutputTokens(contextBudget.ReservedOutputTokens),
	}
	llmResponse, err := runtime.generateWithToolLoop(ctx, client, llmRequest, request, provider, model, contextResult, generatedSummaryText, generatedSummaryUntilSeq, 0, nil)
	if err != nil {
		return TurnResult{}, err
	}
	llmResponse.Usage = combineUsage(visionUsage, llmResponse.Usage)
	encoded, err := json.Marshal(map[string]any{
		"protocol_version": DemoProtocolVersion,
		"content_format":   "markdown",
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return TurnResult{
			AgentPayload:          json.RawMessage(`{"protocol_version":"tma.agent_runtime.demo.v1","content_format":"markdown","content":[{"type":"text","text":"Agent runtime received your message."}]}`),
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

func (runtime DemoRuntime) analyzeImages(ctx context.Context, client llm.Client, request TurnRequest) (string, llm.Usage, error) {
	prompt := "Analyze the uploaded image or images accurately. Describe visible content, extract readable text, and identify details relevant to the user's request. Return analysis text only."
	if userText := strings.TrimSpace(firstTextContent(request.UserPayload)); userText != "" {
		prompt += "\n\nUser request:\n" + userText
	}
	content := []llm.ContentPart{{Type: "text", Text: prompt}}
	content = append(content, request.ImageParts...)
	if err := emitStep(ctx, request, Step{
		Type: managedagents.EventRuntimeLLMRequest, Message: "Sending images to the configured vision model.",
		Data: map[string]any{"phase": "vision_analysis", "provider": request.Config.VisionLLMProvider, "model": request.Config.VisionLLMModel, "image_count": len(request.ImageParts)},
	}); err != nil {
		return "", llm.Usage{}, err
	}
	response, err := client.Generate(ctx, llm.Request{
		Provider: request.Config.VisionLLMProvider, ProviderType: request.Config.VisionLLMProviderType,
		Model: request.Config.VisionLLMModel, BaseURL: request.Config.VisionLLMBaseURL, APIKey: request.Config.VisionLLMAPIKey,
		Messages: []llm.Message{{Role: "user", Content: content}},
	})
	if err != nil {
		return "", llm.Usage{}, fmt.Errorf("vision model analysis failed: %w", err)
	}
	analysis := strings.TrimSpace(contentPartsText(response.Message.Content))
	if analysis == "" {
		return "", response.Usage, errors.New("vision model returned empty analysis")
	}
	if err := emitStep(ctx, request, Step{
		Type: managedagents.EventRuntimeLLMResponse, Message: "Vision model analysis completed.",
		Data: map[string]any{"phase": "vision_analysis", "provider": request.Config.VisionLLMProvider, "model": request.Config.VisionLLMModel, "usage": response.Usage},
	}); err != nil {
		return "", response.Usage, err
	}
	return analysis, response.Usage, nil
}

func combineUsage(left llm.Usage, right llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens: left.InputTokens + right.InputTokens, OutputTokens: left.OutputTokens + right.OutputTokens,
		TotalTokens: left.TotalTokens + right.TotalTokens, CachedInputTokens: left.CachedInputTokens + right.CachedInputTokens,
		ReasoningTokens: left.ReasoningTokens + right.ReasoningTokens,
	}
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
	fileGenerationState := segmentedFileGenerationStateFromRaw(resume.ContinuationState)
	var executionResult tools.ExecutionResult
	var err error
	if resume.Kind == managedagents.InterventionKindClarification || resume.Kind == managedagents.InterventionKindUploadRequest {
		executionResult, err = runtime.resolveHumanInputResult(call, resume.Status, resume.Response, resume.DecisionReason)
		if err == nil {
			err = emitInterventionToolResult(ctx, request, call, executionResult, resume.DecisionReason, 0)
		}
	} else if resume.Kind == managedagents.InterventionKindPlanApproval {
		executionResult, err = runtime.resolvePlanApprovalResult(call, resume.Status, resume.DecisionReason)
		if err == nil {
			err = emitInterventionToolResult(ctx, request, call, executionResult, resume.DecisionReason, 0)
		}
	} else {
		executionResult, err = runtime.resolveInterventionResult(ctx, request, call, resume.Status, resume.DecisionReason, fileGenerationState)
	}
	if err != nil {
		return TurnResult{}, err
	}
	if resume.Kind != managedagents.InterventionKindClarification && resume.Kind != managedagents.InterventionKindUploadRequest && resume.Kind != managedagents.InterventionKindPlanApproval && resume.Status == managedagents.InterventionStatusApproved {
		fileGenerationState.observe(call, executionResult, true)
	}

	if len(resume.Continuation) == 0 {
		if resume.Status == managedagents.InterventionStatusRejected && resume.Kind != managedagents.InterventionKindPlanApproval {
			if executionResult.Error != nil {
				return TurnResult{}, errors.New(executionResult.Error.Message)
			}
			return TurnResult{}, errors.New("intervention rejected without resumable continuation")
		}
		fileExecutionContext := request.Config.ToolExecutionContext
		fileExecutionContext.SessionID = defaultString(fileExecutionContext.SessionID, request.SessionID)
		fileExecutionContext.TurnID = defaultString(fileExecutionContext.TurnID, request.TurnID)
		if blocker, verifyErr := fileGenerationState.completionBlock(ctx, fileExecutionContext); verifyErr != nil {
			return TurnResult{}, verifyErr
		} else if blocker != "" {
			return TurnResult{}, errors.New("segmented file generation cannot complete without resumable continuation: " + blocker)
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"protocol_version": DemoProtocolVersion,
			"content_format":   "markdown",
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
		Provider:        provider,
		ProviderType:    request.Config.LLMProviderType,
		Model:           model,
		BaseURL:         request.Config.LLMBaseURL,
		APIKey:          request.Config.LLMAPIKey,
		MaxOutputTokens: maxLLMOutputTokens(contextBudgetFromSettings(request.Config.ContextWindowTokens, request.Config.RuntimeSettings).ReservedOutputTokens),
	}, request, provider, model, ContextBuildResult{Messages: messages}, "", 0, resume.ContinuationRound+1, fileGenerationState)
	if err != nil {
		return TurnResult{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"protocol_version": DemoProtocolVersion,
		"content_format":   "markdown",
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

func (runtime DemoRuntime) resolvePlanApprovalResult(call tools.Call, status string, reason string) (tools.ExecutionResult, error) {
	request, err := tools.ParsePlanApprovalRequest(call.Arguments)
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	state := map[string]any{
		"approved": false,
		"plan_id":  request.PlanID,
	}
	if strings.TrimSpace(reason) != "" {
		state["decision_reason"] = reason
	}
	content := "The user rejected the plan. Revise or cancel the plan before continuing."
	switch status {
	case managedagents.InterventionStatusApproved:
		state["approved"] = true
		content = "The user approved the plan direction. Continue with the plan. This decision does not approve any later tool call or side effect."
	case managedagents.InterventionStatusRejected:
	default:
		return tools.ExecutionResult{}, fmt.Errorf("unsupported plan approval result %q", status)
	}
	if strings.TrimSpace(reason) != "" {
		content += " Decision reason: " + reason
	}
	return tools.ExecutionResult{
		ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
		Content: content, State: mustMarshalRaw(state),
	}, nil
}

func (runtime DemoRuntime) resolveHumanInputResult(call tools.Call, status string, response json.RawMessage, reason string) (tools.ExecutionResult, error) {
	state := map[string]any{"status": status}
	content := "User input request " + status + "."
	switch status {
	case managedagents.InterventionStatusAnswered:
		if len(response) == 0 || !json.Valid(response) {
			return tools.ExecutionResult{}, errors.New("answered human input requires a valid response")
		}
		var value any
		if err := json.Unmarshal(response, &value); err != nil {
			return tools.ExecutionResult{}, fmt.Errorf("decode human input response: %w", err)
		}
		state["response"] = value
		content = "User submitted the requested information: " + string(response)
	case managedagents.InterventionStatusSkipped, managedagents.InterventionStatusCanceled, managedagents.InterventionStatusExpired:
		if strings.TrimSpace(reason) != "" {
			state["reason"] = reason
			content += " Reason: " + reason
		}
	default:
		return tools.ExecutionResult{}, fmt.Errorf("unsupported human input result %q", status)
	}
	return tools.ExecutionResult{
		ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
		Content: content, State: mustMarshalRaw(state),
	}, nil
}

func (runtime DemoRuntime) resolveInterventionResult(ctx context.Context, request TurnRequest, call tools.Call, status string, decisionReason string, fileGenerationState *segmentedFileGenerationState) (tools.ExecutionResult, error) {
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
	executionContext.DeferArtifacts = fileGenerationState.shouldDeferArtifacts(call)
	startedAt := time.Now()
	result, err := request.Config.ToolExecutor.Execute(ctx, call, executionContext)
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	if err := emitInterventionToolResult(ctx, request, call, result, decisionReason, time.Since(startedAt)); err != nil {
		return tools.ExecutionResult{}, err
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
	mergeToolEventMetadata(data, request.Config.ToolRegistry, call)
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

func (runtime DemoRuntime) currentTime() time.Time {
	if runtime.Now != nil {
		return runtime.Now()
	}
	return time.Now()
}

func buildCurrentDateContext(now time.Time) string {
	if now.IsZero() {
		return ""
	}
	return fmt.Sprintf("Today's date is %s.", now.Format("2006-01-02"))
}

func (runtime DemoRuntime) generateWithToolLoop(ctx context.Context, client llm.Client, baseRequest llm.Request, turnRequest TurnRequest, provider string, model string, contextResult ContextBuildResult, summaryText string, summaryUntilSeq int64, startRound int, fileGenerationState *segmentedFileGenerationState) (llm.Response, error) {
	requestForLLM := baseRequest
	requestForLLM.Messages = append([]llm.Message(nil), contextResult.Messages...)
	requestForLLM.Tools = append([]llm.Tool(nil), turnRequest.Config.ModelTools...)
	invalidToolArgumentRetries := 0
	completionGateRetries := 0
	completionAttempt := 0
	completionMaxRetries := completionGateMaxRetries(turnRequest.Config.RuntimeSettings)
	calibration := tokenEstimateCalibration{}
	if fileGenerationState == nil {
		fileGenerationState = newSegmentedFileGenerationState()
	}

	for round := startRound; ; round++ {
		if runtime.MaxToolRounds > 0 && round >= runtime.MaxToolRounds {
			return llm.Response{}, fmt.Errorf("tool loop exceeded maximum rounds")
		}
		var toolResultCompaction toolResultMicrocompaction
		requestForLLM.Messages, toolResultCompaction = microcompactToolResultMessages(
			requestForLLM.Messages,
			toolResultContextTotalMaxChars(turnRequest.Config.RuntimeSettings),
		)
		messageTokens := estimateMessagesTokens(requestForLLM.Messages)
		toolSchemaTokens := estimateToolsTokens(requestForLLM.Tools)
		estimatedRequestTokens := messageTokens + toolSchemaTokens
		budgetedRequestTokens := calibration.apply(estimatedRequestTokens)
		contextBudget := effectiveContextBudget(contextResult.Budget, turnRequest.Config.ContextWindowTokens, turnRequest.Config.RuntimeSettings)
		availableOutputTokens, budgetErr := fitRoundOutputBudget(&requestForLLM, contextBudget, budgetedRequestTokens)
		if budgetErr != nil {
			return llm.Response{}, budgetErr
		}
		contextBudget.EstimatedTokenCount = budgetedRequestTokens
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
				"budgeted_token_count":          budgetedRequestTokens,
				"token_estimate_multiplier":     calibration.multiplier(),
				"estimated_message_tokens":      messageTokens,
				"estimated_tool_schema_tokens":  toolSchemaTokens,
				"tool_schema_count":             len(requestForLLM.Tools),
				"initial_estimated_token_count": contextResult.EstimatedTokenCount,
				"context_budget":                contextBudget,
				"context_truncated":             contextResult.Truncated,
				"current_date_context_included": contextResult.CurrentDateContextIncluded,
				"pinned_context_included":       contextResult.PinnedContextIncluded,
				"summary_included":              contextResult.SummaryMessageIncluded,
				"summary_source_until_seq":      defaultInt64(summaryUntilSeq, turnRequest.Config.SummarySourceUntilSeq),
				"tool_round":                    round,
				"max_output_tokens":             requestForLLM.MaxOutputTokens,
				"available_output_tokens":       availableOutputTokens,
				"tool_result_context_chars":     toolResultCompaction.AfterChars,
				"tool_result_context_max_chars": toolResultCompaction.MaxChars,
				"tool_result_compacted_count":   toolResultCompaction.CompactedCount,
				"tool_result_compacted_chars":   toolResultCompaction.CompactedChars,
			},
		}); err != nil {
			return llm.Response{}, err
		}

		mutationLimits := fileMutationLimits(turnRequest.Config.RuntimeSettings, provider, model)
		llmResponse, streamStats, err := generateLLM(ctx, client, requestForLLM, turnRequest, round, mutationLimits)
		if err != nil {
			var streamLimitError *fileMutationStreamLimitError
			if errors.As(err, &streamLimitError) {
				invalidToolArgumentRetries++
				if invalidToolArgumentRetries >= maxInvalidToolArgumentRetries {
					return llm.Response{}, errors.New("model repeatedly streamed an oversized file mutation instead of planning segmented generation")
				}
				requestForLLM.Messages = append(requestForLLM.Messages, llm.Message{
					Role:    "user",
					Content: []llm.ContentPart{{Type: "text", Text: streamLimitError.recoveryMessage()}},
				})
				continue
			}
			return llm.Response{}, err
		}
		llmResponse = redactLLMResponseEnvironment(llmResponse, turnRequest.Config.ToolExecutionContext.Environment)
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeLLMResponse,
			Message: "Received response from LLM client.",
			Data: map[string]any{
				"role":          llmResponse.Message.Role,
				"content_count": len(llmResponse.Message.Content),
				"usage":         llmResponse.Usage,
				"tool_round":    round,
				"stream":        streamStats.data(),
			},
		}); err != nil {
			return llm.Response{}, err
		}
		calibration.observe(estimatedRequestTokens, llmResponse.Usage.InputTokens)

		toolCalls, ok := toolCallsFromLLMResponse(llmResponse)
		if !ok || len(toolCalls) == 0 {
			completionAttempt++
			if err := emitStep(ctx, turnRequest, Step{
				Type:    managedagents.EventRuntimeTurnCompleting,
				Message: "Validating candidate response before completion.",
				Data: map[string]any{
					"attempt":     completionAttempt,
					"tool_round":  round,
					"max_retries": completionMaxRetries,
				},
			}); err != nil {
				return llm.Response{}, err
			}
			fileExecutionContext := turnRequest.Config.ToolExecutionContext
			fileExecutionContext.WorkspaceID = defaultString(fileExecutionContext.WorkspaceID, turnRequest.Config.WorkspaceID)
			fileExecutionContext.SessionID = defaultString(fileExecutionContext.SessionID, turnRequest.SessionID)
			fileExecutionContext.EnvironmentID = defaultString(fileExecutionContext.EnvironmentID, turnRequest.Config.EnvironmentID)
			fileExecutionContext.TurnID = defaultString(fileExecutionContext.TurnID, turnRequest.TurnID)
			if fileExecutionContext.Deadline == nil {
				fileExecutionContext.Deadline = deadlineFromContext(ctx)
			}
			verdict := CompletionVerdict{Outcome: CompletionOutcomePass, Validator: "builtin.deterministic"}
			if strings.TrimSpace(contentPartsText(llmResponse.Message.Content)) == "" {
				verdict = CompletionVerdict{
					Outcome:   CompletionOutcomeRetry,
					Validator: "builtin.non_empty_response",
					Reason:    "model returned an empty final response",
					Feedback:  "Runtime completion gate blocked this response because it was empty. Continue the task and return a non-empty final response.",
				}
			}
			blocker, verifyErr := fileGenerationState.completionBlock(ctx, fileExecutionContext)
			if verifyErr != nil {
				verdict = CompletionVerdict{Outcome: CompletionOutcomeFail, Validator: "builtin.segmented_file_generation", Reason: verifyErr.Error()}
				if emitErr := emitStep(ctx, turnRequest, Step{Type: managedagents.EventRuntimeCompletionFailed, Message: "Completion validation failed.", Data: completionVerdictEventData(verdict, completionAttempt, round, completionMaxRetries)}); emitErr != nil {
					return llm.Response{}, emitErr
				}
				return llm.Response{}, fmt.Errorf("completion validation failed: %w", verifyErr)
			}
			if verdict.Outcome == CompletionOutcomePass && blocker != "" {
				verdict = CompletionVerdict{Outcome: CompletionOutcomeRetry, Validator: "builtin.segmented_file_generation", Reason: "segmented file verification is incomplete", Feedback: blocker}
			}
			if verdict.Outcome == CompletionOutcomePass && runtime.CompletionGate != nil {
				candidate := CompletionCandidate{
					SessionID: turnRequest.SessionID, TurnID: turnRequest.TurnID, ToolRound: round, Attempt: completionAttempt,
					Response: llmResponse, Messages: append([]llm.Message(nil), requestForLLM.Messages...),
				}
				customVerdict, gateErr := runtime.CompletionGate.Validate(ctx, candidate)
				if gateErr != nil {
					if ctx.Err() != nil {
						return llm.Response{}, ctx.Err()
					}
					verdict = CompletionVerdict{Outcome: CompletionOutcomeFail, Validator: defaultString(strings.TrimSpace(customVerdict.Validator), "custom"), Reason: gateErr.Error()}
				} else if normalized, normalizeErr := normalizeCompletionVerdict(customVerdict, "custom"); normalizeErr != nil {
					verdict = CompletionVerdict{Outcome: CompletionOutcomeFail, Validator: defaultString(strings.TrimSpace(customVerdict.Validator), "custom"), Reason: normalizeErr.Error()}
				} else {
					verdict = normalized
				}
			}

			switch verdict.Outcome {
			case CompletionOutcomeRetry:
				completionGateRetries++
				if completionGateRetries > completionMaxRetries {
					failedVerdict := verdict
					failedVerdict.Outcome = CompletionOutcomeFail
					failedVerdict.Reason = fmt.Sprintf("completion validation retry limit reached (max_retries=%d): %s", completionMaxRetries, defaultString(verdict.Reason, "candidate did not pass"))
					if err := emitStep(ctx, turnRequest, Step{Type: managedagents.EventRuntimeCompletionFailed, Message: "Completion validation retry limit reached.", Data: completionVerdictEventData(failedVerdict, completionAttempt, round, completionMaxRetries)}); err != nil {
						return llm.Response{}, err
					}
					return llm.Response{}, errors.New(failedVerdict.Reason)
				}
				if err := emitStep(ctx, turnRequest, Step{Type: managedagents.EventRuntimeCompletionBlocked, Message: "Candidate response was blocked by completion validation.", Data: completionVerdictEventData(verdict, completionAttempt, round, completionMaxRetries)}); err != nil {
					return llm.Response{}, err
				}
				requestForLLM.Messages = append(requestForLLM.Messages,
					llm.Message{Role: "assistant", Content: llmResponse.Message.Content},
					llm.Message{Role: "user", Content: []llm.ContentPart{{Type: "text", Text: verdict.Feedback}}},
				)
				continue
			case CompletionOutcomeFail:
				if err := emitStep(ctx, turnRequest, Step{Type: managedagents.EventRuntimeCompletionFailed, Message: "Completion validation failed.", Data: completionVerdictEventData(verdict, completionAttempt, round, completionMaxRetries)}); err != nil {
					return llm.Response{}, err
				}
				return llm.Response{}, fmt.Errorf("completion validation failed (%s): %s", verdict.Validator, defaultString(verdict.Reason, "validator rejected candidate"))
			case CompletionOutcomePass:
				if err := emitStep(ctx, turnRequest, Step{Type: managedagents.EventRuntimeCompletionValidated, Message: "Candidate response passed completion validation.", Data: completionVerdictEventData(verdict, completionAttempt, round, completionMaxRetries)}); err != nil {
					return llm.Response{}, err
				}
			}
			if err := fileGenerationState.publishFinalArtifacts(ctx, fileExecutionContext); err != nil {
				return llm.Response{}, fmt.Errorf("publish validated segmented file artifact: %w", err)
			}
			if err := fileGenerationState.publishReferencedFinalArtifacts(ctx, fileExecutionContext, contentPartsText(llmResponse.Message.Content)); err != nil {
				return llm.Response{}, fmt.Errorf("publish referenced final file artifact: %w", err)
			}
			return llmResponse, nil
		}
		progressText := strings.TrimSpace(contentPartsText(llmResponse.Message.Content))
		if len(llmResponse.Message.ToolCalls) > 0 && progressText != "" {
			if err := emitStep(ctx, turnRequest, Step{
				Type:    managedagents.EventRuntimeProgressMessage,
				Message: "Agent shared a progress update.",
				Data: map[string]any{
					"text":       progressText,
					"tool_round": round,
				},
			}); err != nil {
				return llm.Response{}, err
			}
		}
		toolExecutor := turnRequest.Config.ToolExecutor
		if toolExecutor == nil {
			return llm.Response{}, fmt.Errorf("tool calls requested but tool executor is not configured")
		}

		assistantMessage := llm.Message{
			Role:      "assistant",
			Content:   []llm.ContentPart{{Type: "text", Text: contentPartsText(llmResponse.Message.Content)}},
			ToolCalls: continuationSafeToolCalls(llmResponse.Message.ToolCalls),
		}
		continuationMessages := append([]llm.Message(nil), requestForLLM.Messages...)
		continuationMessages = append(continuationMessages, assistantMessage)

		toolResults, err := runtime.executeToolCalls(ctx, turnRequest, toolExecutor, toolCalls, continuationMessages, round, fileGenerationState, provider, model)
		if err != nil {
			return llm.Response{}, err
		}
		if hasRetryableToolCallError(toolResults) {
			invalidToolArgumentRetries++
			if invalidToolArgumentRetries >= maxInvalidToolArgumentRetries {
				return llm.Response{}, errors.New("model repeatedly returned invalid or oversized tool arguments")
			}
		} else {
			invalidToolArgumentRetries = 0
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

}

type tokenEstimateCalibration struct {
	estimated int
	actual    int64
}

func (calibration tokenEstimateCalibration) apply(estimated int) int {
	if estimated <= 0 || calibration.estimated <= 0 || calibration.actual <= int64(calibration.estimated) {
		return estimated
	}
	return int((int64(estimated)*calibration.actual + int64(calibration.estimated) - 1) / int64(calibration.estimated))
}

func (calibration tokenEstimateCalibration) multiplier() float64 {
	if calibration.estimated <= 0 || calibration.actual <= int64(calibration.estimated) {
		return 1
	}
	return float64(calibration.actual) / float64(calibration.estimated)
}

func (calibration *tokenEstimateCalibration) observe(estimated int, actual int64) {
	if calibration == nil || estimated <= 0 || actual <= int64(estimated) {
		return
	}
	if calibration.estimated == 0 || actual*int64(calibration.estimated) > calibration.actual*int64(estimated) {
		calibration.estimated = estimated
		calibration.actual = actual
	}
}

func effectiveContextBudget(current ContextBudgetBreakdown, contextWindowTokens int, runtimeSettings json.RawMessage) ContextBudgetBreakdown {
	if current.ContextWindowTokens > 0 && current.MaxInputTokens > 0 {
		return current
	}
	limits := contextBudgetFromSettings(contextWindowTokens, runtimeSettings)
	current.ContextWindowTokens = limits.ContextWindowTokens
	current.InputBudgetRatioPercent = limits.InputBudgetRatioPercent
	current.MaxInputTokens = limits.MaxInputTokens
	current.ReservedOutputTokens = limits.ReservedOutputTokens
	return current
}

func fitRoundOutputBudget(request *llm.Request, budget ContextBudgetBreakdown, estimatedInputTokens int) (int, error) {
	contextWindowTokens := budget.ContextWindowTokens
	if contextWindowTokens <= 0 {
		contextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	availableOutputTokens := contextWindowTokens - estimatedInputTokens
	if availableOutputTokens <= 0 {
		return 0, &llm.ProviderError{
			Class: llm.ErrorClassContextLength, Retryable: false,
			Message: fmt.Sprintf("tool loop context budget exhausted before provider request: estimated input %d tokens exceeds context window %d", estimatedInputTokens, contextWindowTokens),
		}
	}
	desiredOutputTokens := request.MaxOutputTokens
	if desiredOutputTokens <= 0 {
		desiredOutputTokens = maxLLMOutputTokens(budget.ReservedOutputTokens)
	}
	if desiredOutputTokens <= 0 {
		desiredOutputTokens = minInt(defaultMaxLLMOutputTokens, availableOutputTokens)
	}
	request.MaxOutputTokens = minInt(desiredOutputTokens, availableOutputTokens)
	return availableOutputTokens, nil
}

func (runtime DemoRuntime) executeToolCalls(ctx context.Context, turnRequest TurnRequest, executor tools.Executor, calls []tools.Call, continuationMessages []llm.Message, continuationRound int, fileGenerationState *segmentedFileGenerationState, provider string, model string) ([]toolCallExecutionResult, error) {
	for _, candidate := range calls {
		if tools.IsParkingInteractionCall(candidate) && len(calls) != 1 {
			return nil, fmt.Errorf("interaction.%s must be the only tool call in a model response", tools.NormalizeCall(candidate).APIName)
		}
	}
	results := make([]toolCallExecutionResult, 0, len(calls))
	registry := turnRequest.Config.ToolRegistry
	policy := tools.InterventionPolicy{Mode: turnRequest.Config.InterventionMode}
	batchPreflightError := tools.ValidateFileMutationBatch(calls)
	mutationLimits := fileMutationLimits(turnRequest.Config.RuntimeSettings, provider, model)
	for _, toolCall := range calls {
		call := tools.NormalizeCall(toolCall)
		normalizedArguments, argumentsErr := normalizeToolCallArguments(call.Arguments)
		if argumentsErr == nil {
			call.Arguments = normalizedArguments
		}
		toolMetadata := toolEventMetadata(registry, call)
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeToolCall,
			Message: "Received tool call request.",
			Data: withToolEventMetadata(map[string]any{
				"id":         call.ID,
				"identifier": call.Identifier,
				"api_name":   call.APIName,
				"arguments":  observableToolArguments(call),
			}, toolMetadata),
		}); err != nil {
			return nil, err
		}
		var preflightError *tools.ExecutionError
		if argumentsErr != nil {
			preflightError = &tools.ExecutionError{Type: "invalid_tool_arguments", Message: invalidToolArgumentsMessage(call)}
		} else {
			if batchPreflightError != nil && isFileMutationCall(call) {
				preflightError = batchPreflightError
			} else {
				preflightError = tools.ValidateFileMutationCallWithLimits(call, mutationLimits)
			}
			if preflightError == nil {
				_, _, registered := registry.GetAPI(call.Identifier, call.APIName)
				if registered {
					if schemaError := registry.ValidateCallArguments(call); schemaError != nil {
						preflightError = schemaError
						if schemaError.Type == "invalid_tool_arguments" {
							preflightError = &tools.ExecutionError{Type: schemaError.Type, Message: invalidToolSchemaRecoveryMessage(schemaError.Message)}
						}
					}
				}
			}
		}
		if preflightError != nil {
			if preflightError.Type == "invalid_tool_schema" {
				return nil, fmt.Errorf("registered tool schema validation failed for %s.%s: %s", call.Identifier, call.APIName, preflightError.Message)
			}
			if preflightError.Type == "file_content_too_large" || preflightError.Type == "edit_replacement_too_large" {
				fileGenerationState.OversizedCalls++
			}
			executionResult := tools.ExecutionResult{
				ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: preflightError.Message,
				Error: preflightError,
			}
			result := toolCallExecutionResult{Call: call, Result: executionResult}
			resultData := tools.ObservableResultData(executionResult, toolResultContextOptions(turnRequest.Config.RuntimeSettings))
			resultData["file_generation"] = fileGenerationMetricsData(fileGenerationState)
			if err := emitStep(ctx, turnRequest, Step{
				Type:    managedagents.EventRuntimeToolResult,
				Message: "Rejected invalid or oversized tool arguments before policy evaluation.",
				Data:    withToolEventMetadata(resultData, toolMetadata),
			}); err != nil {
				return nil, err
			}
			results = append(results, result)
			continue
		}

		executionContext := turnRequest.Config.ToolExecutionContext
		executionContext.WorkspaceID = defaultString(executionContext.WorkspaceID, turnRequest.Config.WorkspaceID)
		executionContext.SessionID = defaultString(executionContext.SessionID, turnRequest.SessionID)
		executionContext.EnvironmentID = defaultString(executionContext.EnvironmentID, turnRequest.Config.EnvironmentID)
		executionContext.TurnID = defaultString(executionContext.TurnID, turnRequest.TurnID)
		if executionContext.Deadline == nil {
			executionContext.Deadline = deadlineFromContext(ctx)
		}
		executionContext.DeferArtifacts = fileGenerationState.shouldDeferArtifacts(call)

		if tools.IsAskUserCall(call) {
			interactionRequest, parseErr := tools.ParseAskUserRequest(call.Arguments)
			if parseErr != nil {
				return nil, parseErr
			}
			requestData := mustMarshalRaw(interactionRequest)
			if err := emitStep(ctx, turnRequest, Step{
				Type:    managedagents.EventRuntimeHumanInputRequired,
				Message: "Agent requested additional user input.",
				Data: withToolEventMetadata(map[string]any{
					"id":                call.ID,
					"identifier":        call.Identifier,
					"api_name":          call.APIName,
					"arguments":         interactionRequest,
					"kind":              managedagents.InterventionKindClarification,
					"intervention_mode": "request_user_input",
					"reason":            "clarification",
				}, toolMetadata),
				Private: map[string]any{
					"continuation_messages": continuationMessages,
					"continuation_round":    continuationRound,
					"continuation_state":    fileGenerationState.raw(),
					"arguments":             append(json.RawMessage(nil), call.Arguments...),
					"request":               requestData,
				},
			}); err != nil {
				return nil, err
			}
			return nil, ErrPendingHumanInput
		}

		if tools.IsUploadRequestCall(call) {
			uploadRequest, parseErr := tools.ParseUploadRequest(call.Arguments)
			if parseErr != nil {
				return nil, parseErr
			}
			requestData := mustMarshalRaw(uploadRequest)
			if err := emitStep(ctx, turnRequest, Step{
				Type:    managedagents.EventRuntimeHumanInputRequired,
				Message: "Agent requested file upload from the user.",
				Data: withToolEventMetadata(map[string]any{
					"id":                call.ID,
					"identifier":        call.Identifier,
					"api_name":          call.APIName,
					"arguments":         uploadRequest,
					"kind":              managedagents.InterventionKindUploadRequest,
					"intervention_mode": "request_upload",
					"reason":            "upload_request",
				}, toolMetadata),
				Private: map[string]any{
					"continuation_messages": continuationMessages,
					"continuation_round":    continuationRound,
					"continuation_state":    fileGenerationState.raw(),
					"arguments":             append(json.RawMessage(nil), call.Arguments...),
					"request":               requestData,
				},
			}); err != nil {
				return nil, err
			}
			return nil, ErrPendingHumanInput
		}

		if tools.IsPlanApprovalCall(call) {
			approvalRequest, parseErr := tools.ParsePlanApprovalRequest(call.Arguments)
			if parseErr != nil {
				return nil, parseErr
			}
			if executionContext.TaskService == nil {
				return nil, errors.New("request_plan_approval requires task planning in this runtime")
			}
			plan, loadErr := executionContext.TaskService.GetPlan(ctx, executionContext.SessionID)
			if loadErr != nil {
				return nil, fmt.Errorf("load active task plan for approval: %w", loadErr)
			}
			if approvalRequest.PlanID != "" && approvalRequest.PlanID != plan.ID {
				return nil, fmt.Errorf("request_plan_approval plan_id %q does not match current active plan %q", approvalRequest.PlanID, plan.ID)
			}
			approvalRequest.PlanID = plan.ID
			call.Arguments = mustMarshalRaw(approvalRequest)
			requestData := mustMarshalRaw(tools.PlanApprovalSnapshot{Plan: plan, Summary: approvalRequest.Summary})
			if err := emitStep(ctx, turnRequest, Step{
				Type:    managedagents.EventRuntimePlanApprovalRequired,
				Message: "Agent requested approval for the current task plan.",
				Data: withToolEventMetadata(map[string]any{
					"id":                call.ID,
					"identifier":        call.Identifier,
					"api_name":          call.APIName,
					"arguments":         approvalRequest,
					"kind":              managedagents.InterventionKindPlanApproval,
					"intervention_mode": "request_plan_approval",
					"reason":            "plan_review",
					"plan_id":           plan.ID,
				}, toolMetadata),
				Private: map[string]any{
					"continuation_messages": continuationMessages,
					"continuation_round":    continuationRound,
					"continuation_state":    fileGenerationState.raw(),
					"arguments":             append(json.RawMessage(nil), call.Arguments...),
					"request":               requestData,
				},
			}); err != nil {
				return nil, err
			}
			return nil, ErrPendingIntervention
		}

		if manifest, api, ok := registry.GetAPI(call.Identifier, call.APIName); ok && !tools.IsTaskCall(call) {
			decision := policy.EvaluateCall(manifest, api, call, executionContext)
			planApproved := fileGenerationState.planApproves(call)
			if planApproved {
				decision = tools.InterventionDecision{Allowed: true, Required: true, Mode: decision.Mode, Reason: "approved_segmented_file_plan"}
			}
			if decision.Required && !decision.Allowed {
				if err := emitStep(ctx, turnRequest, Step{
					Type:    managedagents.EventRuntimeToolInterventionRequired,
					Message: "Tool call requires approval before execution.",
					Data: withToolEventMetadata(map[string]any{
						"id":                call.ID,
						"identifier":        call.Identifier,
						"api_name":          call.APIName,
						"arguments":         observableToolArguments(call),
						"intervention_mode": decision.Mode,
						"reason":            decision.Reason,
					}, toolMetadata),
					Private: map[string]any{
						"continuation_messages": continuationMessages,
						"continuation_round":    continuationRound,
						"continuation_state":    fileGenerationState.raw(),
						"arguments":             append(json.RawMessage(nil), call.Arguments...),
					},
				}); err != nil {
					return nil, err
				}
				return nil, ErrPendingIntervention
			}
			if decision.Required && decision.Allowed && (decision.Mode == tools.InterventionModeApproveForMe || planApproved) {
				approvalSource := "auto"
				if planApproved {
					approvalSource = "segmented_plan"
				}
				if err := emitStep(ctx, turnRequest, Step{
					Type:    managedagents.EventRuntimeToolInterventionApproved,
					Message: "Tool call auto-approved for execution.",
					Data: withToolEventMetadata(map[string]any{
						"id":                call.ID,
						"identifier":        call.Identifier,
						"api_name":          call.APIName,
						"arguments":         observableToolArguments(call),
						"intervention_mode": decision.Mode,
						"reason":            decision.Reason,
						"approval_source":   approvalSource,
					}, toolMetadata),
				}); err != nil {
					return nil, err
				}
			}
		}

		executionResult, replayed := fileGenerationState.idempotentReplay(call)
		if !replayed {
			var err error
			executionResult, err = executor.Execute(ctx, call, executionContext)
			if err != nil {
				return nil, err
			}
		}
		fileGenerationState.observe(call, executionResult, false)
		result := toolCallExecutionResult{Call: call, Result: executionResult}

		resultData := tools.ObservableResultData(executionResult, toolResultContextOptions(turnRequest.Config.RuntimeSettings))
		resultData["file_generation"] = fileGenerationMetricsData(fileGenerationState)
		if err := emitStep(ctx, turnRequest, Step{
			Type:    managedagents.EventRuntimeToolResult,
			Message: "Received tool result.",
			Data:    withToolEventMetadata(resultData, toolMetadata),
		}); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func fileGenerationMetricsData(state *segmentedFileGenerationState) map[string]any {
	return map[string]any{
		"oversized_call_count":             state.OversizedCalls,
		"segment_count":                    state.SegmentCount,
		"idempotent_replay_count":          state.IdempotentReplays,
		"remaining_placeholder_count":      state.remainingCount(),
		"generation_duration_milliseconds": state.durationMillis(),
	}
}

func normalizeToolCallArguments(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, errors.New("tool arguments are not valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil || object == nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	return json.RawMessage(trimmed), nil
}

func continuationSafeToolCalls(calls []llm.ToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	normalized := append([]llm.ToolCall(nil), calls...)
	for index := range normalized {
		arguments, err := normalizeToolCallArguments(normalized[index].Function.Arguments)
		call := tools.NormalizeCall(tools.Call{
			ID: normalized[index].ID, APIName: normalized[index].Function.Name, Arguments: arguments,
		})
		if err != nil || tools.ValidateFileMutationCall(call) != nil {
			arguments = json.RawMessage(`{}`)
		}
		normalized[index].Function.Arguments = arguments
	}
	return normalized
}

func hasRetryableToolCallError(results []toolCallExecutionResult) bool {
	for _, result := range results {
		if result.Result.Error != nil && (result.Result.Error.Type == "invalid_tool_arguments" || tools.IsRecoverableFileGenerationError(result.Result.Error.Type)) {
			return true
		}
	}
	return false
}

func invalidToolArgumentsMessage(call tools.Call) string {
	switch normalizeToolAPIName(call.APIName) {
	case "write_file", "edit_file":
		return "Tool arguments were incomplete or invalid JSON. Do not retry the same payload. For a large file, use write_file to create a small skeleton with unique numbered placeholders such as __TMA_PLACEHOLDER_REPORT_001__, then use edit_file to replace one placeholder with one complete semantic segment at a time. Keep each content/new_string at or below 6000 tokens when possible and always below 8000. Before finishing, read the file to confirm no placeholders remain and run the appropriate syntax check or test."
	default:
		return "Tool arguments were incomplete or invalid JSON. Regenerate a complete, smaller JSON object and retry the tool call."
	}
}

func invalidToolSchemaRecoveryMessage(validationMessage string) string {
	return "Tool arguments failed the registered JSON Schema validation: " + validationMessage + ". Regenerate the arguments to match the tool schema. Do not retry the unchanged payload."
}

func normalizeToolAPIName(apiName string) string {
	parts := strings.Split(strings.TrimSpace(apiName), ".")
	return parts[len(parts)-1]
}

func isFileMutationCall(call tools.Call) bool {
	switch normalizeToolAPIName(call.APIName) {
	case "write_file", "edit_file":
		return true
	default:
		return false
	}
}

func fileMutationLimits(runtimeSettings json.RawMessage, provider, model string) tools.FileMutationLimits {
	type limits struct {
		RecommendedTokens int `json:"recommended_tokens"`
		MaxTokens         int `json:"max_tokens"`
	}
	var settings struct {
		FileMutationRecommendedTokens int               `json:"file_mutation_recommended_tokens"`
		FileMutationMaxTokens         int               `json:"file_mutation_max_tokens"`
		FileMutationLimits            map[string]limits `json:"file_mutation_limits"`
	}
	_ = json.Unmarshal(runtimeSettings, &settings)
	result := tools.FileMutationLimits{
		RecommendedTokens: settings.FileMutationRecommendedTokens,
		MaxTokens:         settings.FileMutationMaxTokens,
	}
	if configured, ok := settings.FileMutationLimits[strings.TrimSpace(provider)+"/"+strings.TrimSpace(model)]; ok {
		result.RecommendedTokens = configured.RecommendedTokens
		result.MaxTokens = configured.MaxTokens
	}
	return result
}

func observableToolArguments(call tools.Call) any {
	call = tools.NormalizeCall(call)
	if !isFileMutationCall(call) {
		return rawJSONObject(call.Arguments)
	}
	var arguments map[string]any
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return map[string]any{"redacted": true, "valid_json": false, "size_bytes": len(call.Arguments)}
	}
	for _, field := range []string{"content", "content_base64", "new_string"} {
		value, ok := arguments[field]
		if !ok {
			continue
		}
		text, _ := value.(string)
		hash := sha256.Sum256([]byte(text))
		arguments[field] = map[string]any{
			"redacted":         true,
			"character_count":  len(text),
			"estimated_tokens": tools.EstimateFileMutationTokens(text),
			"sha256":           hex.EncodeToString(hash[:]),
		}
	}
	return arguments
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

func toolResultContextTotalMaxChars(runtimeSettings json.RawMessage) int {
	perResultMax := toolResultContextMaxChars(runtimeSettings)
	var settings struct {
		ToolResultContextTotalMaxChars int `json:"tool_result_context_total_max_chars"`
		ToolResultsContextMaxChars     int `json:"tool_results_context_max_chars"`
	}
	if len(runtimeSettings) > 0 && json.Unmarshal(runtimeSettings, &settings) == nil {
		configured := settings.ToolResultContextTotalMaxChars
		if configured <= 0 {
			configured = settings.ToolResultsContextMaxChars
		}
		if configured > 0 {
			return maxInt(configured, perResultMax)
		}
	}
	return maxInt(perResultMax, minInt(perResultMax*2, defaultToolResultTotalMaxChars))
}

type toolResultMicrocompaction struct {
	MaxChars       int
	BeforeChars    int
	AfterChars     int
	CompactedCount int
	CompactedChars int
}

func microcompactToolResultMessages(messages []llm.Message, maxChars int) ([]llm.Message, toolResultMicrocompaction) {
	stats := toolResultMicrocompaction{MaxChars: maxChars}
	toolIndexes := make([]int, 0)
	for index, message := range messages {
		if message.Role != "tool" {
			continue
		}
		stats.BeforeChars += messageContentChars(message)
		toolIndexes = append(toolIndexes, index)
	}
	stats.AfterChars = stats.BeforeChars
	if maxChars <= 0 || stats.BeforeChars <= maxChars || len(toolIndexes) < 2 {
		return messages, stats
	}

	compacted := append([]llm.Message(nil), messages...)
	// Keep the newest tool result intact. Older calls remain paired with their
	// assistant tool calls, but their bulky content becomes a structured stub.
	for _, messageIndex := range toolIndexes[:len(toolIndexes)-1] {
		if stats.AfterChars <= maxChars {
			break
		}
		originalChars := messageContentChars(compacted[messageIndex])
		text := contentPartsText(compacted[messageIndex].Content)
		if originalChars == 0 || toolResultAlreadyMicrocompacted(text) {
			continue
		}
		stub := microcompactedToolResultText(text, originalChars)
		stubChars := len([]rune(stub))
		if stubChars >= originalChars {
			continue
		}
		compacted[messageIndex].Content = []llm.ContentPart{{Type: "text", Text: stub}}
		stats.AfterChars -= originalChars - stubChars
		stats.CompactedChars += originalChars - stubChars
		stats.CompactedCount++
	}
	return compacted, stats
}

func messageContentChars(message llm.Message) int {
	total := 0
	for _, part := range message.Content {
		if part.Type == "text" {
			total += len([]rune(part.Text))
		}
	}
	return total
}

func toolResultAlreadyMicrocompacted(text string) bool {
	var payload struct {
		Context struct {
			Microcompacted bool `json:"micro_compacted"`
		} `json:"context"`
	}
	return json.Unmarshal([]byte(text), &payload) == nil && payload.Context.Microcompacted
}

func microcompactedToolResultText(text string, originalChars int) string {
	var source map[string]any
	if json.Unmarshal([]byte(text), &source) != nil {
		source = map[string]any{}
	}
	result := map[string]any{
		"content": "[Older tool result compacted. Re-run the tool or inspect its artifact for full detail.]",
		"context": map[string]any{
			"micro_compacted":       true,
			"original_result_chars": originalChars,
		},
	}
	for _, key := range []string{"protocol_version", "id", "identifier", "api_name", "success", "error", "artifacts", "artifact_error"} {
		if value, exists := source[key]; exists && value != nil {
			result[key] = value
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return `{"success":false,"content":"[Older tool result compacted for the current turn.]","context":{"micro_compacted":true}}`
	}
	return string(encoded)
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

func withToolEventMetadata(data map[string]any, metadata map[string]any) map[string]any {
	mergeToolEventMetadataMap(data, metadata)
	return data
}

func mergeToolEventMetadata(data map[string]any, registry tools.Registry, call tools.Call) {
	mergeToolEventMetadataMap(data, toolEventMetadata(registry, call))
}

func mergeToolEventMetadataMap(data map[string]any, metadata map[string]any) {
	if len(data) == 0 || len(metadata) == 0 {
		return
	}
	for key, value := range metadata {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		data[key] = value
	}
}

func toolEventMetadata(registry tools.Registry, call tools.Call) map[string]any {
	call = tools.NormalizeCall(call)
	manifest, _, ok := registry.GetAPI(call.Identifier, call.APIName)
	if !ok {
		return nil
	}
	metadata := map[string]any{
		"tool_source":    toolSourceFromManifest(manifest),
		"manifest_type":  manifest.Type,
		"manifest_title": strings.TrimSpace(manifest.Meta.Title),
	}
	for key, value := range manifest.Metadata {
		if strings.HasPrefix(key, "mcp_") {
			metadata[key] = value
		}
	}
	return metadata
}

func toolSourceFromManifest(manifest tools.Manifest) string {
	switch strings.TrimSpace(strings.ToLower(manifest.Type)) {
	case "mcp_server":
		return "mcp"
	case "process_plugin":
		return "worker_plugin"
	case "builtin":
		return "builtin"
	default:
		return "tool"
	}
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

func maxInt(left int, right int) int {
	if left > right {
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

func redactLLMResponseEnvironment(response llm.Response, environment map[string]string) llm.Response {
	if !tools.HasSensitiveEnvironment(environment) {
		return response
	}
	response.Message.Content = append([]llm.ContentPart(nil), response.Message.Content...)
	for index := range response.Message.Content {
		response.Message.Content[index].Text = tools.RedactEnvironmentText(response.Message.Content[index].Text, environment)
		if response.Message.Content[index].ImageURL != nil {
			imageURL := *response.Message.Content[index].ImageURL
			imageURL.URL = tools.RedactEnvironmentText(imageURL.URL, environment)
			response.Message.Content[index].ImageURL = &imageURL
		}
	}
	response.Reasoning = append([]llm.ReasoningPart(nil), response.Reasoning...)
	for index := range response.Reasoning {
		response.Reasoning[index].Text = tools.RedactEnvironmentText(response.Reasoning[index].Text, environment)
	}
	return response
}

func defaultInt64(value int64, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
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

func maxLLMOutputTokens(reservedOutputTokens int) int {
	if reservedOutputTokens <= 0 {
		return 0
	}
	return minInt(reservedOutputTokens, defaultMaxLLMOutputTokens)
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

func combinePinnedContext(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n\n")
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

type fileMutationStreamLimitError struct {
	APIName           string
	EstimatedTokens   int
	RecommendedTokens int
	MaxTokens         int
}

func (err *fileMutationStreamLimitError) Error() string {
	return fmt.Sprintf("streamed %s arguments reached %d estimated tokens", err.APIName, err.EstimatedTokens)
}

func (err *fileMutationStreamLimitError) recoveryMessage() string {
	return fmt.Sprintf("Stop generating the large %s payload. Its streamed arguments already reached the recommended limit of %d estimated tokens. Regenerate this step as exactly one small write_file skeleton with unique numbered placeholders such as __TMA_PLACEHOLDER_REPORT_001__, then replace one placeholder per later edit_file call. Keep every segment below %d estimated tokens.", err.APIName, err.RecommendedTokens, err.MaxTokens)
}

type llmStreamStats struct {
	Streamed            bool
	ChunkCount          int
	TextChunkCount      int
	ReasoningChunkCount int
	ToolCallChunkCount  int
	UsageChunkCount     int
	StopChunkCount      int
	ErrorChunkCount     int
	OutputChars         int
	ReasoningChars      int
	TTFTMillis          int64
	FinishReason        string
	firstTextSeen       bool
}

func (stats *llmStreamStats) observe(delta llm.Delta, kind string, elapsed time.Duration) {
	stats.ChunkCount++
	switch kind {
	case llm.DeltaKindText:
		stats.TextChunkCount++
		stats.OutputChars += utf8.RuneCountInString(delta.Text)
		if !stats.firstTextSeen {
			stats.firstTextSeen = true
			stats.TTFTMillis = elapsed.Milliseconds()
		}
	case llm.DeltaKindReasoning:
		stats.ReasoningChunkCount++
		stats.ReasoningChars += utf8.RuneCountInString(delta.Text)
	case llm.DeltaKindToolCall:
		stats.ToolCallChunkCount++
	case llm.DeltaKindUsage:
		stats.UsageChunkCount++
	case llm.DeltaKindStop:
		stats.StopChunkCount++
		stats.FinishReason = delta.FinishReason
	case llm.DeltaKindError:
		stats.ErrorChunkCount++
	}
}

func (stats llmStreamStats) data() map[string]any {
	if !stats.Streamed {
		return map[string]any{"streamed": false}
	}
	return map[string]any{
		"streamed":              true,
		"chunk_count":           stats.ChunkCount,
		"text_chunk_count":      stats.TextChunkCount,
		"reasoning_chunk_count": stats.ReasoningChunkCount,
		"tool_call_chunk_count": stats.ToolCallChunkCount,
		"usage_chunk_count":     stats.UsageChunkCount,
		"stop_chunk_count":      stats.StopChunkCount,
		"error_chunk_count":     stats.ErrorChunkCount,
		"output_chars":          stats.OutputChars,
		"reasoning_chars":       stats.ReasoningChars,
		"ttft_ms":               stats.TTFTMillis,
		"finish_reason":         stats.FinishReason,
	}
}

func generateLLM(ctx context.Context, client llm.Client, llmRequest llm.Request, turnRequest TurnRequest, toolRound int, mutationLimits tools.FileMutationLimits) (llm.Response, llmStreamStats, error) {
	streamingClient, ok := client.(llm.StreamingClient)
	if !ok {
		response, err := client.Generate(ctx, llmRequest)
		if err != nil {
			return llm.Response{}, llmStreamStats{}, err
		}
		return response, llmStreamStats{}, nil
	}

	streamLimits := normalizedStreamMutationLimits(mutationLimits)
	type streamedToolCall struct {
		name      strings.Builder
		arguments strings.Builder
	}
	streamedToolCalls := map[int]*streamedToolCall{}
	bufferSensitiveText := tools.HasSensitiveEnvironment(turnRequest.Config.ToolExecutionContext.Environment)
	var sensitiveText strings.Builder
	startedAt := time.Now()
	stats := llmStreamStats{Streamed: true}

	response, err := streamingClient.GenerateStream(ctx, llmRequest, func(delta llm.Delta) error {
		kind := defaultString(delta.Kind, llm.DeltaKindText)
		if !llmDeltaHasPayload(delta, kind) {
			return nil
		}
		stats.observe(delta, kind, time.Since(startedAt))
		if kind == llm.DeltaKindToolCall && delta.ToolCall != nil {
			partial := delta.ToolCall
			call := streamedToolCalls[partial.Index]
			if call == nil {
				call = &streamedToolCall{}
				streamedToolCalls[partial.Index] = call
			}
			call.name.WriteString(partial.Name)
			call.arguments.WriteString(partial.Arguments)
			apiName := normalizeToolAPIName(call.name.String())
			if apiName == "write_file" || apiName == "edit_file" {
				estimated := tools.EstimateSerializedFileMutationTokens(call.arguments.String())
				if estimated > streamLimits.RecommendedTokens {
					return &fileMutationStreamLimitError{
						APIName: apiName, EstimatedTokens: estimated,
						RecommendedTokens: streamLimits.RecommendedTokens, MaxTokens: streamLimits.MaxTokens,
					}
				}
			}
		}
		if kind == llm.DeltaKindText && turnRequest.EmitStream != nil {
			if bufferSensitiveText {
				sensitiveText.WriteString(delta.Text)
			} else {
				turnRequest.EmitStream(StreamEvent{Index: delta.Index, ToolRound: toolRound, Text: delta.Text})
			}
		}
		return nil
	})
	if err == nil && bufferSensitiveText && turnRequest.EmitStream != nil && sensitiveText.Len() > 0 {
		turnRequest.EmitStream(StreamEvent{
			Index: 0, ToolRound: toolRound,
			Text: tools.RedactEnvironmentText(sensitiveText.String(), turnRequest.Config.ToolExecutionContext.Environment),
		})
	}
	return response, stats, err
}

func normalizedStreamMutationLimits(limits tools.FileMutationLimits) tools.FileMutationLimits {
	if limits.MaxTokens <= 0 || limits.MaxTokens > tools.MaxFileMutationTokens {
		limits.MaxTokens = tools.MaxFileMutationTokens
	}
	if limits.RecommendedTokens <= 0 || limits.RecommendedTokens > limits.MaxTokens {
		limits.RecommendedTokens = minInt(tools.RecommendedFileMutationTokens, limits.MaxTokens)
	}
	return limits
}

func llmDeltaHasPayload(delta llm.Delta, kind string) bool {
	switch kind {
	case llm.DeltaKindText, llm.DeltaKindReasoning:
		return delta.Text != ""
	case llm.DeltaKindToolCall:
		return delta.ToolCall != nil
	case llm.DeltaKindUsage:
		return delta.Usage != nil
	case llm.DeltaKindStop:
		return delta.FinishReason != ""
	case llm.DeltaKindError:
		return delta.Error != nil
	default:
		return false
	}
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
