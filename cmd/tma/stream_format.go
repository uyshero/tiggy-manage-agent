package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func streamSSE(reader io.Reader, output io.Writer) error {
	return streamSSEWithInterventions(reader, output, nil)
}

func streamSSEWithInterventions(reader io.Reader, output io.Writer, onIntervention func(toolInterventionEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(lines) != 0 {
				if _, err := io.WriteString(output, formatSSEChunk(lines)); err != nil {
					return fmt.Errorf("write stream: %w", err)
				}
				if err := handleInterventionChunk(lines, onIntervention); err != nil {
					return err
				}
				lines = lines[:0]
			} else {
				if _, err := io.WriteString(output, "\n"); err != nil {
					return fmt.Errorf("write stream: %w", err)
				}
			}
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	if len(lines) != 0 {
		if _, err := io.WriteString(output, formatSSEChunk(lines)); err != nil {
			return fmt.Errorf("write stream: %w", err)
		}
		if err := handleInterventionChunk(lines, onIntervention); err != nil {
			return err
		}
	}
	return nil
}

func handleInterventionChunk(lines []string, onIntervention func(toolInterventionEvent) error) error {
	if onIntervention == nil {
		return nil
	}
	eventType, eventData := parseSSEChunk(lines)
	switch eventType {
	case "runtime.tool_intervention_required", "runtime.tool_intervention_approved", "runtime.tool_intervention_rejected":
	default:
		return nil
	}
	event, ok := parseToolInterventionEvent(eventType, eventData)
	if !ok {
		return nil
	}
	return onIntervention(event)
}

func formatSSEChunk(lines []string) string {
	chunk := strings.Join(lines, "\n") + "\n\n"
	eventType, eventData := parseSSEChunk(lines)
	switch eventType {
	case "user.message", "agent.message":
		formatted, ok := formatChatMessageEvent(eventType, eventData)
		if !ok {
			return chunk
		}
		return formatted + "\n"
	case "session.status_provisioning", "session.status_idle", "session.status_running", "session.status_interrupting", "session.status_compacting", "session.status_failed", "session.status_terminated":
		formatted, ok := formatSessionStatusEvent(eventType, eventData)
		if !ok {
			return chunk
		}
		return formatted + "\n"
	case "runtime.llm_delta":
		formatted, ok := formatLLMDeltaEvent(eventData)
		if !ok {
			return chunk
		}
		return formatted + "\n"
	case "runtime.tool_intervention_required", "runtime.tool_intervention_approved", "runtime.tool_intervention_rejected":
		formatted, ok := formatToolInterventionEvent(eventType, eventData)
		if !ok {
			return chunk
		}
		return formatted + "\n"
	case "runtime.tool_result":
		formatted, ok := formatToolResultEvent(eventData)
		if !ok {
			return chunk
		}
		return formatted + "\n"
	default:
		return chunk
	}
}

func parseSSEChunk(lines []string) (string, string) {
	var eventType string
	var dataLines []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return eventType, strings.Join(dataLines, "\n")
}

func formatChatMessageEvent(eventType string, raw string) (string, bool) {
	var event struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return "", false
	}
	text, ok := payloadText(event.Payload)
	if !ok {
		return "", false
	}
	label := "user"
	if eventType == "agent.message" {
		label = "agent"
	}
	return formatSpeakerText(label, text), true
}

func formatSessionStatusEvent(eventType string, raw string) (string, bool) {
	var event struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return "", false
	}
	var payload struct {
		Status         string `json:"status"`
		TurnID         string `json:"turn_id"`
		LastTurnStatus string `json:"last_turn_status"`
		Reason         string `json:"reason"`
	}
	if len(event.Payload) != 0 {
		_ = json.Unmarshal(event.Payload, &payload)
	}
	if payload.Status == "" {
		payload.Status = strings.TrimPrefix(eventType, "session.status_")
	}

	var builder strings.Builder
	builder.WriteString("status: ")
	builder.WriteString(payload.Status)
	if payload.TurnID != "" {
		builder.WriteString(" (turn ")
		builder.WriteString(payload.TurnID)
		builder.WriteString(")")
	}
	builder.WriteString("\n")
	if payload.LastTurnStatus != "" {
		builder.WriteString("  last_turn_status: ")
		builder.WriteString(payload.LastTurnStatus)
		builder.WriteString("\n")
	}
	if payload.Reason != "" {
		builder.WriteString("  reason: ")
		builder.WriteString(payload.Reason)
		builder.WriteString("\n")
	}
	return builder.String(), true
}

func formatLLMDeltaEvent(raw string) (string, bool) {
	var event struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return "", false
	}
	var payload struct {
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return "", false
	}
	if payload.Data.Text == "" {
		return "", false
	}
	return formatSpeakerText("delta", payload.Data.Text), true
}

func payloadText(raw json.RawMessage) (string, bool) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false
	}
	if payload.Text != "" {
		return payload.Text, true
	}
	var parts []string
	for _, content := range payload.Content {
		if content.Type == "text" && content.Text != "" {
			parts = append(parts, content.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

func formatSpeakerText(label string, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return label + ">\n"
	}
	if !strings.Contains(text, "\n") {
		return label + "> " + text + "\n"
	}
	var builder strings.Builder
	builder.WriteString(label)
	builder.WriteString(">\n")
	for _, line := range strings.Split(text, "\n") {
		builder.WriteString("  ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return builder.String()
}

type toolInterventionEvent struct {
	Type             string
	Seq              int64
	TurnID           string
	CallID           string
	Identifier       string
	APIName          string
	InterventionMode string
	Reason           string
	Message          string
}

func parseToolInterventionEvent(eventType string, raw string) (toolInterventionEvent, bool) {
	var event struct {
		Seq     int64           `json:"seq"`
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return toolInterventionEvent{}, false
	}
	var payload struct {
		TurnID  string `json:"turn_id"`
		Message string `json:"message"`
		Data    struct {
			ID               string `json:"id"`
			Identifier       string `json:"identifier"`
			APIName          string `json:"api_name"`
			InterventionMode string `json:"intervention_mode"`
			Reason           string `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return toolInterventionEvent{}, false
	}
	if eventType == "" {
		eventType = event.Type
	}
	return toolInterventionEvent{
		Type:             eventType,
		Seq:              event.Seq,
		TurnID:           payload.TurnID,
		CallID:           payload.Data.ID,
		Identifier:       payload.Data.Identifier,
		APIName:          payload.Data.APIName,
		InterventionMode: payload.Data.InterventionMode,
		Reason:           payload.Data.Reason,
		Message:          payload.Message,
	}, true
}

func formatToolInterventionEvent(eventType string, raw string) (string, bool) {
	event, ok := parseToolInterventionEvent(eventType, raw)
	if !ok {
		return "", false
	}

	var builder strings.Builder
	builder.WriteString(toolInterventionHeader(eventType))
	builder.WriteString("\n")
	writeSeqTurnCallTool(&builder, event.Seq, event.TurnID, event.CallID, event.Identifier, event.APIName)
	if event.InterventionMode != "" {
		builder.WriteString("  mode: ")
		builder.WriteString(event.InterventionMode)
		builder.WriteString("\n")
	}
	if event.Message != "" {
		builder.WriteString("  message: ")
		builder.WriteString(event.Message)
		builder.WriteString("\n")
	}
	if event.Reason != "" {
		builder.WriteString("  policy: ")
		builder.WriteString(event.Reason)
		builder.WriteString("\n")
	}
	return builder.String(), true
}

func promptForToolIntervention(input *bufio.Scanner, output io.Writer, event toolInterventionEvent, decide func(action string, reason string) error) error {
	for {
		if _, err := fmt.Fprint(output, "approval action [a=approve, r=reject, s=skip]: "); err != nil {
			return fmt.Errorf("write approval prompt: %w", err)
		}
		if !input.Scan() {
			if err := input.Err(); err != nil {
				return fmt.Errorf("read approval action: %w", err)
			}
			return io.EOF
		}

		switch strings.ToLower(strings.TrimSpace(input.Text())) {
		case "a", "approve":
			if err := decide("approve", ""); err != nil {
				return fmt.Errorf("approve intervention %s: %w", event.CallID, err)
			}
			_, err := fmt.Fprintln(output, "approval submitted: approve")
			return err
		case "r", "reject":
			reason, err := readOptionalDecisionReason(input, output)
			if err != nil {
				return err
			}
			if err := decide("reject", reason); err != nil {
				return fmt.Errorf("reject intervention %s: %w", event.CallID, err)
			}
			_, err = fmt.Fprintln(output, "approval submitted: reject")
			return err
		case "s", "skip":
			_, err := fmt.Fprintln(output, "approval skipped")
			return err
		default:
			if _, err := fmt.Fprintln(output, "enter a, r, or s"); err != nil {
				return fmt.Errorf("write approval prompt: %w", err)
			}
		}
	}
}

func readOptionalDecisionReason(input *bufio.Scanner, output io.Writer) (string, error) {
	if _, err := fmt.Fprint(output, "rejection reason (optional): "); err != nil {
		return "", fmt.Errorf("write rejection reason prompt: %w", err)
	}
	if !input.Scan() {
		if err := input.Err(); err != nil {
			return "", fmt.Errorf("read rejection reason: %w", err)
		}
		return "", io.EOF
	}
	return strings.TrimSpace(input.Text()), nil
}

func formatToolResultEvent(raw string) (string, bool) {
	var event struct {
		Seq     int64           `json:"seq"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return "", false
	}
	var payload struct {
		TurnID  string `json:"turn_id"`
		Message string `json:"message"`
		Data    struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			APIName    string `json:"api_name"`
			Content    string `json:"content"`
			Success    bool   `json:"success"`
			Error      *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return "", false
	}

	var builder strings.Builder
	builder.WriteString("tool result\n")
	writeSeqTurnCallTool(&builder, event.Seq, payload.TurnID, payload.Data.ID, payload.Data.Identifier, payload.Data.APIName)
	builder.WriteString("  success: ")
	builder.WriteString(strconv.FormatBool(payload.Data.Success))
	builder.WriteString("\n")
	if payload.Message != "" {
		builder.WriteString("  message: ")
		builder.WriteString(payload.Message)
		builder.WriteString("\n")
	}
	if payload.Data.Error != nil {
		builder.WriteString("  error: ")
		if payload.Data.Error.Type != "" {
			builder.WriteString(payload.Data.Error.Type)
			builder.WriteString(": ")
		}
		builder.WriteString(payload.Data.Error.Message)
		builder.WriteString("\n")
	}
	if payload.Data.Content != "" {
		builder.WriteString("  content: ")
		builder.WriteString(truncateValue(strings.ReplaceAll(payload.Data.Content, "\n", "\\n"), 500))
		builder.WriteString("\n")
	}
	return builder.String(), true
}

func toolInterventionHeader(eventType string) string {
	switch eventType {
	case "runtime.tool_intervention_approved":
		return "approval approved"
	case "runtime.tool_intervention_rejected":
		return "approval rejected"
	default:
		return "approval required"
	}
}

func writeSeqTurnCallTool(builder *strings.Builder, seq int64, turnID string, callID string, identifier string, apiName string) {
	if seq > 0 {
		builder.WriteString("  seq: ")
		builder.WriteString(strconv.FormatInt(seq, 10))
		builder.WriteString("\n")
	}
	if turnID != "" {
		builder.WriteString("  turn: ")
		builder.WriteString(turnID)
		builder.WriteString("\n")
	}
	if callID != "" {
		builder.WriteString("  call: ")
		builder.WriteString(callID)
		builder.WriteString("\n")
	}
	if identifier != "" || apiName != "" {
		builder.WriteString("  tool: ")
		builder.WriteString(identifier)
		if apiName != "" {
			builder.WriteString(".")
			builder.WriteString(apiName)
		}
		builder.WriteString("\n")
	}
}

func truncateValue(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
