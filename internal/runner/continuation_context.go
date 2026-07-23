package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/managedagents"
)

const (
	maxContinuationToolResults  = 12
	maxContinuationFieldRunes   = 800
	maxContinuationContextRunes = 12000
)

func (e AgentRuntimeTurnExecutor) resolveContinuationContext(ctx context.Context, request TurnRequest) (string, error) {
	if e.Store == nil || request.UserEventSeq <= 0 || !isContinuationRequest(request.UserPayload) {
		return "", nil
	}
	events, err := managedagents.ListEventsWithContext(ctx, e.Store, request.SessionID, 0)
	if err != nil {
		return "", fmt.Errorf("list continuation events: %w", err)
	}
	return buildContinuationContext(events, request.TurnID, request.UserEventSeq), nil
}

func isContinuationRequest(payload json.RawMessage) bool {
	text := strings.ToLower(strings.TrimSpace(firstTextContent(payload)))
	if text == "" {
		return false
	}
	for _, prefix := range []string{"继续", "接着", "继续做", "继续执行", "继续完成"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	first := text
	if index := strings.IndexAny(first, " \t\r\n,，。.!！?？:："); index >= 0 {
		first = first[:index]
	}
	switch first {
	case "continue", "resume":
		return true
	}
	return strings.HasPrefix(text, "keep going") || strings.HasPrefix(text, "carry on")
}

type continuationToolCall struct {
	Name      string
	Arguments json.RawMessage
}

type continuationToolResult struct {
	Seq     int64
	CallID  string
	Name    string
	Summary string
}

func buildContinuationContext(events []managedagents.Event, currentTurnID string, beforeSeq int64) string {
	previousTurnID := ""
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Seq >= beforeSeq || event.TurnID == "" || event.TurnID == currentTurnID {
			continue
		}
		previousTurnID = event.TurnID
		break
	}
	if previousTurnID == "" {
		return ""
	}

	calls := map[string]continuationToolCall{}
	results := make([]continuationToolResult, 0)
	objective := ""
	finalMessage := ""
	terminalStatus := "unknown"
	for _, event := range events {
		if event.Seq >= beforeSeq || event.TurnID != previousTurnID {
			continue
		}
		switch event.Type {
		case managedagents.EventUserMessage:
			if text := strings.TrimSpace(firstTextContent(event.Payload)); text != "" {
				objective = text
			}
		case managedagents.EventAgentMessage:
			if text := strings.TrimSpace(firstTextContent(event.Payload)); text != "" {
				finalMessage = text
			}
		case string(agentcore.EventToolBatchPlanned):
			for callID, call := range continuationCallsFromEvent(event.Payload) {
				calls[callID] = call
			}
		case string(agentcore.EventToolCallResult):
			if result, ok := continuationResultFromEvent(event); ok {
				results = append(results, result)
			}
		case string(agentcore.EventRuntimeCompleted):
			terminalStatus = "completed"
		case string(agentcore.EventRuntimeFailed):
			terminalStatus = "failed"
		case string(agentcore.EventRuntimeCanceled):
			terminalStatus = "canceled"
		}
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Seq < results[j].Seq })
	if len(results) > maxContinuationToolResults {
		results = results[len(results)-maxContinuationToolResults:]
	}

	lines := []string{
		"Execution continuation from the immediately preceding turn (protected state):",
		fmt.Sprintf("Previous turn: %s; terminal status: %s.", previousTurnID, terminalStatus),
		"The user explicitly asked to continue. Preserve the original objective, reuse the existing Session workspace and artifacts, and continue from unfinished work. Do not restart completed steps merely because this is a new turn.",
	}
	if objective != "" {
		lines = append(lines, "Original objective: "+truncateContinuationText(objective, maxContinuationFieldRunes))
	}
	if len(results) > 0 {
		lines = append(lines, "Recent execution evidence (oldest to newest):")
		for _, result := range results {
			call := calls[result.CallID]
			name := firstNonemptyContinuation(result.Name, call.Name, "unknown_tool")
			line := "- " + name
			if arguments := compactContinuationArguments(call.Arguments); arguments != "" {
				line += " arguments=" + arguments
			}
			if result.Summary != "" {
				line += " => " + result.Summary
			}
			lines = append(lines, truncateContinuationText(line, maxContinuationFieldRunes))
		}
	}
	if finalMessage != "" {
		lines = append(lines, "Previous final output: "+truncateContinuationText(finalMessage, maxContinuationFieldRunes))
	}
	return truncateContinuationText(strings.Join(lines, "\n"), maxContinuationContextRunes)
}

func continuationCallsFromEvent(payload json.RawMessage) map[string]continuationToolCall {
	var decoded struct {
		Data struct {
			Calls []struct {
				Call struct {
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"call"`
			} `json:"calls"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &decoded) != nil {
		return nil
	}
	result := make(map[string]continuationToolCall, len(decoded.Data.Calls))
	for _, item := range decoded.Data.Calls {
		if callID := strings.TrimSpace(item.Call.ID); callID != "" {
			result[callID] = continuationToolCall{Name: strings.TrimSpace(item.Call.Name), Arguments: append(json.RawMessage(nil), item.Call.Arguments...)}
		}
	}
	return result
}

func continuationResultFromEvent(event managedagents.Event) (continuationToolResult, bool) {
	var decoded struct {
		Data struct {
			Name   string `json:"name"`
			CallID string `json:"call_id"`
			Result struct {
				Status  string          `json:"status"`
				State   json.RawMessage `json:"state"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(event.Payload, &decoded) != nil {
		return continuationToolResult{}, false
	}
	callID := strings.TrimSpace(decoded.Data.CallID)
	if callID == "" {
		return continuationToolResult{}, false
	}
	parts := make([]string, 0, 3)
	if status := strings.TrimSpace(decoded.Data.Result.Status); status != "" {
		parts = append(parts, "status="+status)
	}
	if state := compactContinuationState(decoded.Data.Result.State); state != "" {
		parts = append(parts, state)
	}
	if len(parts) < 2 && len(decoded.Data.Result.Content) > 0 {
		if content := compactContinuationResultContent(decoded.Data.Result.Content[0].Text); content != "" {
			parts = append(parts, content)
		}
	}
	return continuationToolResult{
		Seq: event.Seq, CallID: callID, Name: strings.TrimSpace(decoded.Data.Name),
		Summary: truncateContinuationText(strings.Join(parts, "; "), maxContinuationFieldRunes),
	}, true
}

func compactContinuationArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return truncateContinuationText(string(raw), maxContinuationFieldRunes)
	}
	for _, key := range []string{"content", "new_string", "old_string", "data", "base64"} {
		if value, exists := object[key]; exists {
			encoded, _ := json.Marshal(value)
			object[key] = fmt.Sprintf("<omitted %d bytes>", len(encoded))
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return ""
	}
	return truncateContinuationText(string(encoded), maxContinuationFieldRunes)
}

func compactContinuationState(raw json.RawMessage) string {
	var state map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &state) != nil {
		return ""
	}
	parts := make([]string, 0, 6)
	for _, key := range []string{"status", "exit_code", "path", "stdout", "stderr", "error", "size_bytes"} {
		value, ok := state[key]
		if !ok {
			continue
		}
		text := truncateContinuationText(strings.TrimSpace(fmt.Sprint(value)), maxContinuationFieldRunes/2)
		if text != "" && text != "<nil>" {
			parts = append(parts, key+"="+text)
		}
	}
	return strings.Join(parts, ", ")
}

func compactContinuationResultContent(text string) string {
	var payload struct {
		Content   string `json:"content"`
		Artifacts []struct {
			Name string `json:"name"`
		} `json:"artifacts"`
	}
	if json.Unmarshal([]byte(text), &payload) == nil {
		parts := make([]string, 0, 2)
		if content := strings.TrimSpace(payload.Content); content != "" {
			parts = append(parts, truncateContinuationText(content, maxContinuationFieldRunes/2))
		}
		artifactNames := make([]string, 0, len(payload.Artifacts))
		for _, artifact := range payload.Artifacts {
			if name := strings.TrimSpace(artifact.Name); name != "" {
				artifactNames = append(artifactNames, name)
			}
		}
		if len(artifactNames) > 0 {
			parts = append(parts, "artifacts="+strings.Join(artifactNames, ","))
		}
		return strings.Join(parts, "; ")
	}
	return truncateContinuationText(strings.TrimSpace(text), maxContinuationFieldRunes/2)
}

func truncateContinuationText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

func firstNonemptyContinuation(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
