package agentcoreadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/llm"
	coremodel "tiggy-manage-agent/internal/model"
)

type ResolvedRoute struct {
	Provider     string
	ProviderType string
	Model        string
	BaseURL      string
	APIKey       string
}

type RouteResolver interface {
	Resolve(context.Context, coremodel.Route) (ResolvedRoute, error)
}

type RouteResolverFunc func(context.Context, coremodel.Route) (ResolvedRoute, error)

func (f RouteResolverFunc) Resolve(ctx context.Context, route coremodel.Route) (ResolvedRoute, error) {
	return f(ctx, route)
}

type LLMModel struct {
	Client        llm.Client
	RouteResolver RouteResolver
}

var _ agentcore.ModelPort = LLMModel{}

func (a LLMModel) Generate(ctx context.Context, request coremodel.Request, sink agentcore.DeltaSink) (coremodel.Response, error) {
	if a.Client == nil {
		return coremodel.Response{}, errors.New("llm client is required")
	}
	if err := request.Validate(); err != nil {
		return coremodel.Response{}, fmt.Errorf("invalid model request: %w", err)
	}
	resolved := ResolvedRoute{Provider: request.Route.ProviderInstanceID, Model: request.Route.ModelID}
	if a.RouteResolver != nil {
		var err error
		resolved, err = a.RouteResolver.Resolve(ctx, request.Route)
		if err != nil {
			return coremodel.Response{}, fmt.Errorf("resolve model route: %w", err)
		}
	}
	if strings.TrimSpace(resolved.Provider) == "" {
		resolved.Provider = request.Route.ProviderInstanceID
	}
	if strings.TrimSpace(resolved.Model) == "" {
		resolved.Model = request.Route.ModelID
	}
	llmRequest, err := toLLMRequest(request, resolved)
	if err != nil {
		return coremodel.Response{}, err
	}

	finishReason := ""
	var response llm.Response
	if streaming, ok := a.Client.(llm.StreamingClient); ok && sink != nil {
		response, err = streaming.GenerateStream(ctx, llmRequest, func(delta llm.Delta) error {
			converted, emit, stop := fromLLMDelta(delta)
			if stop != "" {
				finishReason = stop
			}
			if !emit {
				return nil
			}
			return sink(converted)
		})
	} else {
		response, err = a.Client.Generate(ctx, llmRequest)
	}
	if err != nil {
		return coremodel.Response{}, fromLLMError(err)
	}
	return fromLLMResponse(request.AttemptID, response, finishReason), nil
}

func toLLMRequest(request coremodel.Request, route ResolvedRoute) (llm.Request, error) {
	messages := make([]llm.Message, 0, len(request.Messages))
	for index, message := range request.Messages {
		converted, err := toLLMMessage(message)
		if err != nil {
			return llm.Request{}, fmt.Errorf("convert model message %d: %w", index, err)
		}
		messages = append(messages, converted)
	}
	tools := make([]llm.Tool, len(request.Tools))
	for index, tool := range request.Tools {
		tools[index] = llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  append(json.RawMessage(nil), tool.InputSchema...),
			},
		}
	}
	return llm.Request{
		Provider:        route.Provider,
		ProviderType:    route.ProviderType,
		Model:           route.Model,
		BaseURL:         route.BaseURL,
		APIKey:          route.APIKey,
		MaxOutputTokens: request.MaxOutputTokens,
		Messages:        messages,
		Tools:           tools,
	}, nil
}

func toLLMMessage(message coremodel.Message) (llm.Message, error) {
	converted := llm.Message{Role: string(message.Role)}
	for _, content := range message.Content {
		switch content.Type {
		case coremodel.ContentText:
			converted.Content = append(converted.Content, llm.ContentPart{Type: "text", Text: content.Text})
		case coremodel.ContentImage:
			if content.Image == nil || strings.TrimSpace(content.Image.URL) == "" {
				return llm.Message{}, errors.New("llm adapter requires image content to contain a resolved URL")
			}
			converted.Content = append(converted.Content, llm.ContentPart{
				Type: "image_url",
				ImageURL: &llm.ImageURL{
					URL:    content.Image.URL,
					Detail: content.Image.Detail,
				},
			})
		case coremodel.ContentThinking:
			// The legacy llm protocol cannot safely replay signed reasoning blocks.
			continue
		case coremodel.ContentToolCall:
			if content.ToolCall == nil {
				return llm.Message{}, errors.New("tool call content is missing")
			}
			converted.ToolCalls = append(converted.ToolCalls, llm.ToolCall{
				ID:   content.ToolCall.ID,
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      content.ToolCall.Name,
					Arguments: append(json.RawMessage(nil), content.ToolCall.Arguments...),
				},
			})
		case coremodel.ContentToolResult:
			if content.ToolResult == nil {
				return llm.Message{}, errors.New("tool result content is missing")
			}
			if converted.ToolCallID != "" && converted.ToolCallID != content.ToolResult.CallID {
				return llm.Message{}, errors.New("legacy llm protocol allows one tool result per message")
			}
			converted.ToolCallID = content.ToolResult.CallID
			text := flattenModelContent(content.ToolResult.Content)
			if text == "" {
				text = "{}"
			}
			converted.Content = append(converted.Content, llm.ContentPart{Type: "text", Text: text})
		default:
			return llm.Message{}, fmt.Errorf("unsupported content type %q", content.Type)
		}
	}
	return converted, nil
}

func flattenModelContent(content []coremodel.Content) string {
	parts := make([]string, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case coremodel.ContentText:
			parts = append(parts, part.Text)
		case coremodel.ContentToolResult:
			if part.ToolResult != nil {
				parts = append(parts, flattenModelContent(part.ToolResult.Content))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func fromLLMResponse(attemptID string, response llm.Response, finishReason string) coremodel.Response {
	content := make([]coremodel.Content, 0, len(response.Reasoning)+len(response.Message.Content)+len(response.Message.ToolCalls))
	for _, reasoning := range response.Reasoning {
		if reasoning.Text != "" {
			content = append(content, coremodel.Content{Type: coremodel.ContentThinking, Thinking: &coremodel.ThinkingBlock{Text: reasoning.Text}})
		}
	}
	for _, part := range response.Message.Content {
		switch part.Type {
		case "text", "":
			if part.Text != "" {
				content = append(content, coremodel.Content{Type: coremodel.ContentText, Text: part.Text})
			}
		case "image_url":
			if part.ImageURL != nil && part.ImageURL.URL != "" {
				content = append(content, coremodel.Content{Type: coremodel.ContentImage, Image: &coremodel.ImageReference{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}})
			}
		}
	}
	for index, call := range response.Message.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("%s_tool_%d", defaultAdapterString(attemptID, "attempt"), index+1)
		}
		content = append(content, coremodel.Content{Type: coremodel.ContentToolCall, ToolCall: &coremodel.ToolCall{
			ID:        id,
			Name:      call.Function.Name,
			Arguments: append(json.RawMessage(nil), call.Function.Arguments...),
		}})
	}
	return coremodel.Response{
		Message: coremodel.Message{
			Role:       coremodel.RoleAssistant,
			Visibility: coremodel.VisibilityInternal,
			Content:    content,
		},
		StopReason: normalizedStopReason(finishReason, len(response.Message.ToolCalls) > 0),
		Usage:      fromLLMUsage(response.Usage),
	}
}

func fromLLMDelta(delta llm.Delta) (coremodel.Delta, bool, string) {
	converted := coremodel.Delta{Index: delta.Index}
	finishReason := ""
	switch delta.Kind {
	case llm.DeltaKindText:
		converted.Type = coremodel.DeltaText
		converted.Text = delta.Text
	case llm.DeltaKindReasoning:
		converted.Type = coremodel.DeltaThinking
		converted.Text = delta.Text
	case llm.DeltaKindToolCall:
		converted.Type = coremodel.DeltaToolCall
		if delta.ToolCall != nil {
			converted.ToolCall = &coremodel.ToolCallDelta{
				Index:             delta.ToolCall.Index,
				ID:                delta.ToolCall.ID,
				Name:              delta.ToolCall.Name,
				ArgumentsFragment: delta.ToolCall.Arguments,
			}
		}
	case llm.DeltaKindUsage:
		converted.Type = coremodel.DeltaUsage
		if delta.Usage != nil {
			usage := fromLLMUsage(*delta.Usage)
			converted.Usage = &usage
		}
	case llm.DeltaKindStop:
		converted.Type = coremodel.DeltaStopped
		converted.StopReason = normalizedStopReason(delta.FinishReason, false)
		finishReason = delta.FinishReason
	case llm.DeltaKindError:
		converted.Type = coremodel.DeltaError
		if delta.Error != nil {
			converted.ProviderError = &coremodel.ProviderError{
				Class:      fromLLMErrorClass(delta.Error.Class),
				Code:       fmt.Sprintf("http_%d", delta.Error.StatusCode),
				Retryable:  delta.Error.Retryable,
				SafeDetail: delta.Error.Message,
			}
		}
	default:
		return coremodel.Delta{}, false, ""
	}
	return converted, true, finishReason
}

func normalizedStopReason(reason string, hasTools bool) coremodel.StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "tool_calls", "tool_call", "tool_use":
		return coremodel.StopReasonToolCall
	case "length", "max_tokens":
		return coremodel.StopReasonLength
	case "cancelled", "canceled":
		return coremodel.StopReasonCanceled
	case "error":
		return coremodel.StopReasonError
	case "stop", "end_turn", "done", "":
		if hasTools {
			return coremodel.StopReasonToolCall
		}
		return coremodel.StopReasonComplete
	default:
		if hasTools {
			return coremodel.StopReasonToolCall
		}
		return coremodel.StopReason(reason)
	}
}

func fromLLMUsage(usage llm.Usage) coremodel.Usage {
	return coremodel.Usage{
		InputTokens:       usage.InputTokens,
		OutputTokens:      usage.OutputTokens,
		TotalTokens:       usage.TotalTokens,
		CachedInputTokens: usage.CachedInputTokens,
		ReasoningTokens:   usage.ReasoningTokens,
		Source:            coremodel.UsageSourceProvider,
	}
}

func fromLLMError(err error) error {
	var providerError *llm.ProviderError
	if !errors.As(err, &providerError) {
		return err
	}
	code := ""
	if providerError.StatusCode > 0 {
		code = fmt.Sprintf("http_%d", providerError.StatusCode)
	}
	return &coremodel.ProviderError{
		Class:      fromLLMErrorClass(providerError.Class),
		Code:       code,
		Retryable:  providerError.Retryable,
		RetryAfter: providerError.RetryAfter,
		Attempt:    providerError.Attempts,
		SafeDetail: providerError.Message,
		Cause:      err,
	}
}

func fromLLMErrorClass(class llm.ErrorClass) coremodel.ErrorClass {
	switch class {
	case llm.ErrorClassAuth:
		return coremodel.ErrorAuth
	case llm.ErrorClassRateLimit:
		return coremodel.ErrorRateLimit
	case llm.ErrorClassContextLength:
		return coremodel.ErrorContextLength
	case llm.ErrorClassTimeout:
		return coremodel.ErrorTimeout
	case llm.ErrorClassServer:
		return coremodel.ErrorServer
	case llm.ErrorClassInvalidRequest:
		return coremodel.ErrorInvalidRequest
	default:
		return coremodel.ErrorUnknown
	}
}

func defaultAdapterString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
