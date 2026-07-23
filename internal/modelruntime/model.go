package modelruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
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
	return fromLLMResponse(request.AttemptID, response, finishReason, request.Tools), nil
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

func fromLLMResponse(attemptID string, response llm.Response, finishReason string, activeTools []coremodel.ToolDefinition) coremodel.Response {
	toolCalls := append([]llm.ToolCall(nil), response.Message.ToolCalls...)
	content := make([]coremodel.Content, 0, len(response.Reasoning)+len(response.Message.Content)+len(toolCalls))
	for _, reasoning := range response.Reasoning {
		if reasoning.Text != "" {
			content = append(content, coremodel.Content{Type: coremodel.ContentThinking, Thinking: &coremodel.ThinkingBlock{Text: reasoning.Text}})
		}
	}
	for _, part := range response.Message.Content {
		switch part.Type {
		case "text", "":
			text := part.Text
			if len(response.Message.ToolCalls) == 0 {
				if remaining, parsed, ok := parseSeedTextToolCalls(text); ok {
					text = remaining
					toolCalls = append(toolCalls, parsed...)
				}
			}
			if text != "" {
				content = append(content, coremodel.Content{Type: coremodel.ContentText, Text: text})
			}
		case "image_url":
			if part.ImageURL != nil && part.ImageURL.URL != "" {
				content = append(content, coremodel.Content{Type: coremodel.ContentImage, Image: &coremodel.ImageReference{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}})
			}
		}
	}
	for index, call := range toolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("%s_tool_%d", defaultAdapterString(attemptID, "attempt"), index+1)
		}
		arguments, argumentsError := normalizeToolCallArguments(call.Function.Arguments)
		content = append(content, coremodel.Content{Type: coremodel.ContentToolCall, ToolCall: &coremodel.ToolCall{
			ID:             id,
			Name:           canonicalActiveToolName(call.Function.Name, activeTools),
			Arguments:      arguments,
			ArgumentsError: argumentsError,
		}})
	}
	return coremodel.Response{
		Message: coremodel.Message{
			Role:       coremodel.RoleAssistant,
			Visibility: coremodel.VisibilityInternal,
			Content:    content,
		},
		StopReason: normalizedStopReason(finishReason, len(toolCalls) > 0),
		Usage:      fromLLMUsage(response.Usage),
	}
}

const (
	seedToolCallOpen  = "<seed:tool_call>"
	seedToolCallClose = "</seed:tool_call>"
)

type seedTextToolCall struct {
	Function seedTextFunction `xml:"function"`
}

type seedTextFunction struct {
	Name       string              `xml:"name,attr"`
	Parameters []seedTextParameter `xml:"parameter"`
}

type seedTextParameter struct {
	Name   string `xml:"name,attr"`
	String string `xml:"string,attr"`
	Value  string `xml:",chardata"`
}

// Some Ark Agent Plan responses serialize a tool call into assistant text
// instead of the OpenAI-compatible tool_calls field. Decode only complete,
// structurally valid blocks; malformed text remains ordinary model output.
func parseSeedTextToolCalls(text string) (string, []llm.ToolCall, bool) {
	if !strings.Contains(text, seedToolCallOpen) {
		return text, nil, false
	}
	rest := text
	var visible strings.Builder
	calls := make([]llm.ToolCall, 0, 1)
	for {
		start := strings.Index(rest, seedToolCallOpen)
		if start < 0 {
			visible.WriteString(rest)
			break
		}
		visible.WriteString(rest[:start])
		closeOffset := strings.Index(rest[start+len(seedToolCallOpen):], seedToolCallClose)
		if closeOffset < 0 {
			return text, nil, false
		}
		end := start + len(seedToolCallOpen) + closeOffset + len(seedToolCallClose)
		block := rest[start:end]
		call, err := decodeSeedTextToolCall(block)
		if err != nil {
			return text, nil, false
		}
		calls = append(calls, call)
		rest = rest[end:]
	}
	if len(calls) == 0 {
		return text, nil, false
	}
	return strings.TrimSpace(visible.String()), calls, true
}

func decodeSeedTextToolCall(block string) (llm.ToolCall, error) {
	var decoded seedTextToolCall
	if err := xml.Unmarshal([]byte(block), &decoded); err != nil {
		return llm.ToolCall{}, err
	}
	name := strings.TrimSpace(decoded.Function.Name)
	if name == "" {
		return llm.ToolCall{}, errors.New("seed tool call function name is required")
	}
	arguments := make(map[string]json.RawMessage, len(decoded.Function.Parameters))
	for _, parameter := range decoded.Function.Parameters {
		parameterName := strings.TrimSpace(parameter.Name)
		if parameterName == "" {
			return llm.ToolCall{}, errors.New("seed tool call parameter name is required")
		}
		if _, exists := arguments[parameterName]; exists {
			return llm.ToolCall{}, fmt.Errorf("duplicate seed tool call parameter %q", parameterName)
		}
		value := strings.TrimSpace(parameter.Value)
		isString, err := strconv.ParseBool(strings.TrimSpace(parameter.String))
		if err != nil && strings.TrimSpace(parameter.String) != "" {
			return llm.ToolCall{}, fmt.Errorf("invalid seed tool call string flag for %q", parameterName)
		}
		if isString || strings.TrimSpace(parameter.String) == "" && !json.Valid([]byte(value)) {
			encoded, err := json.Marshal(value)
			if err != nil {
				return llm.ToolCall{}, err
			}
			arguments[parameterName] = encoded
			continue
		}
		if !json.Valid([]byte(value)) {
			return llm.ToolCall{}, fmt.Errorf("seed tool call parameter %q is not valid JSON", parameterName)
		}
		arguments[parameterName] = json.RawMessage(value)
	}
	encodedArguments, err := json.Marshal(arguments)
	if err != nil {
		return llm.ToolCall{}, err
	}
	return llm.ToolCall{
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      name,
			Arguments: encodedArguments,
		},
	}, nil
}

func canonicalActiveToolName(name string, activeTools []coremodel.ToolDefinition) string {
	trimmed := strings.TrimSpace(name)
	for _, tool := range activeTools {
		if tool.Name == trimmed {
			return trimmed
		}
	}
	var compatible strings.Builder
	for _, char := range trimmed {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '_':
			compatible.WriteRune(char)
		default:
			compatible.WriteByte('_')
		}
	}
	candidate := compatible.String()
	for _, tool := range activeTools {
		if tool.Name == candidate {
			return candidate
		}
	}
	return trimmed
}

func normalizeToolCallArguments(raw json.RawMessage) (json.RawMessage, string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`), ""
	}
	if !json.Valid(trimmed) {
		return json.RawMessage(`{}`), "tool arguments must be a valid JSON object; re-issue the tool call with complete arguments"
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return json.RawMessage(`{}`), "tool arguments must be a JSON object; re-issue the tool call with object arguments"
	}
	return append(json.RawMessage(nil), trimmed...), ""
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
