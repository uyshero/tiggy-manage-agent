package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

func commandSessionAttach(client *apiClient, args []string) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printSessionAttachUsage()
		return nil
	}

	flags := flag.NewFlagSet("session attach", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var sessionID string
	var afterSeq int64
	flags.StringVar(&sessionID, "session", "", "session id")
	flags.Int64Var(&afterSeq, "after", 0, "stream events after this seq")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("session attach requires --session")
	}

	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/events/stream"
	if afterSeq > 0 {
		path += "?after_seq=" + strconv.FormatInt(afterSeq, 10)
	}

	return client.streamInteractive(path, sessionID, os.Stdin, os.Stdout)
}

func (c *apiClient) streamInteractive(path string, sessionID string, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")

	response, err := c.streamHTTP.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBytes, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return fmt.Errorf("read error response: %w", readErr)
		}
		return fmt.Errorf("GET %s returned %s: %s", path, response.Status, strings.TrimSpace(string(responseBytes)))
	}

	lockedOutput := &lockedWriter{writer: output}
	state := &interactiveSessionState{}
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- streamSSEWithInterventions(response.Body, lockedOutput, func(event toolInterventionEvent) error {
			return handleInteractiveInterventionEvent(c, sessionID, lockedOutput, state, event)
		})
	}()

	if _, err := fmt.Fprintf(lockedOutput, "attached to %s\n", sessionID); err != nil {
		cancel()
		return fmt.Errorf("write attach hint: %w", err)
	}
	if err := printSessionVersionNotice(c, sessionID, lockedOutput); err != nil {
		fmt.Fprintf(lockedOutput, "session version notice unavailable: %v\n", err)
	}
	if _, err := fmt.Fprintln(lockedOutput, "type a message, /say MESSAGE, /interrupt, or /quit"); err != nil {
		cancel()
		return fmt.Errorf("write attach hint: %w", err)
	}
	if _, err := fmt.Fprintln(lockedOutput, "approval: a=approve, r REASON=reject, s=skip"); err != nil {
		cancel()
		return fmt.Errorf("write attach hint: %w", err)
	}
	if err := announceCurrentPendingInterventions(c, sessionID, lockedOutput, state); err != nil {
		cancel()
		return err
	}

	inputErr := make(chan error, 1)
	go func() {
		inputErr <- runInteractiveInputLoop(c, sessionID, input, lockedOutput, state)
	}()

	select {
	case err := <-streamErr:
		cancel()
		return err
	case err := <-inputErr:
		cancel()
		_ = response.Body.Close()
		if err != nil {
			return err
		}
		return nil
	}
}

type attachSessionInfo struct {
	ID                 string `json:"id"`
	AgentID            string `json:"agent_id"`
	AgentConfigVersion int    `json:"agent_config_version"`
}

type attachAgentInfo struct {
	ID                   string `json:"id"`
	CurrentConfigVersion int    `json:"current_config_version"`
}

func printSessionVersionNotice(client *apiClient, sessionID string, output io.Writer) error {
	var session attachSessionInfo
	if err := client.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID), nil, &session); err != nil {
		return err
	}
	if session.AgentID == "" || session.AgentConfigVersion <= 0 {
		return nil
	}
	var agent attachAgentInfo
	if err := client.do(http.MethodGet, "/v1/agents/"+url.PathEscape(session.AgentID), nil, &agent); err != nil {
		return err
	}
	if agent.CurrentConfigVersion <= 0 {
		return nil
	}
	if session.AgentConfigVersion < agent.CurrentConfigVersion {
		_, err := fmt.Fprintf(output, "notice: this session is pinned to agent config v%d; latest is v%d. Start a new session to use the latest config.\n", session.AgentConfigVersion, agent.CurrentConfigVersion)
		return err
	}
	_, err := fmt.Fprintf(output, "agent config: v%d\n", session.AgentConfigVersion)
	return err
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

type interactiveClient interface {
	sessionInterventionPending(sessionID string, turnID string, callID string) (bool, error)
	decideSessionIntervention(sessionID string, turnID string, callID string, action string, reason string) error
	sendUserMessage(sessionID string, text string) error
	sendUserInterrupt(sessionID string) error
}

func (c *apiClient) sessionInterventionPending(sessionID string, turnID string, callID string) (bool, error) {
	interventions, err := c.listPendingInterventions(sessionID)
	if err != nil {
		return false, err
	}
	for _, intervention := range interventions {
		if intervention.TurnID == turnID && intervention.CallID == callID {
			return true, nil
		}
	}
	return false, nil
}

func (c *apiClient) listPendingInterventions(sessionID string) ([]toolInterventionEvent, error) {
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/interventions?status=pending"
	var response struct {
		Interventions []struct {
			TurnID           string `json:"turn_id"`
			CallID           string `json:"call_id"`
			ToolIdentifier   string `json:"tool_identifier"`
			APIName          string `json:"api_name"`
			InterventionMode string `json:"intervention_mode"`
			Reason           string `json:"reason"`
		} `json:"interventions"`
	}
	if err := c.do(http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	events := make([]toolInterventionEvent, 0, len(response.Interventions))
	for _, intervention := range response.Interventions {
		events = append(events, toolInterventionEvent{
			Type:             "runtime.tool_intervention_required",
			TurnID:           intervention.TurnID,
			CallID:           intervention.CallID,
			Identifier:       intervention.ToolIdentifier,
			APIName:          intervention.APIName,
			InterventionMode: intervention.InterventionMode,
			Reason:           intervention.Reason,
			Message:          "Pending tool call requires approval before execution.",
		})
	}
	return events, nil
}

func (c *apiClient) decideSessionIntervention(sessionID string, turnID string, callID string, action string, reason string) error {
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/interventions/" + url.PathEscape(turnID) + "/" + url.PathEscape(callID) + "/" + action
	var response any
	if err := c.do(http.MethodPost, path, map[string]any{"reason": reason}, &response); err != nil {
		return err
	}
	return nil
}

func (c *apiClient) sendUserMessage(sessionID string, text string) error {
	request := map[string]any{
		"events": []map[string]any{
			{
				"type": "user.message",
				"payload": map[string]any{
					"content": []map[string]string{
						{"type": "text", "text": text},
					},
				},
			},
		},
	}
	var response any
	return c.do(http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/events", request, &response)
}

func (c *apiClient) sendUserInterrupt(sessionID string) error {
	request := map[string]any{
		"events": []map[string]any{
			{"type": "user.interrupt"},
		},
	}
	var response any
	return c.do(http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/events", request, &response)
}

type interactiveSessionState struct {
	mu      sync.Mutex
	pending *toolInterventionEvent
}

func (s *interactiveSessionState) setPending(event toolInterventionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := event
	s.pending = &copied
}

func (s *interactiveSessionState) setPendingIfChanged(event toolInterventionEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil && s.pending.TurnID == event.TurnID && s.pending.CallID == event.CallID {
		return false
	}
	copied := event
	s.pending = &copied
	return true
}

func (s *interactiveSessionState) clearPending(turnID string, callID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return
	}
	if s.pending.TurnID == turnID && s.pending.CallID == callID {
		s.pending = nil
	}
}

func (s *interactiveSessionState) takePending() (toolInterventionEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return toolInterventionEvent{}, false
	}
	event := *s.pending
	return event, true
}

func announceCurrentPendingInterventions(client *apiClient, sessionID string, output io.Writer, state *interactiveSessionState) error {
	pending, err := client.listPendingInterventions(sessionID)
	if err != nil {
		return err
	}
	return announcePendingInterventions(output, state, pending)
}

func announcePendingInterventions(output io.Writer, state *interactiveSessionState, pending []toolInterventionEvent) error {
	for _, event := range pending {
		if !state.setPendingIfChanged(event) {
			continue
		}
		if _, err := fmt.Fprintf(output, "pending approval recovered: %s.%s call=%s\n", event.Identifier, event.APIName, event.CallID); err != nil {
			return fmt.Errorf("write pending approval output: %w", err)
		}
		if _, err := fmt.Fprintln(output, "approval action: a=approve, r [reason]=reject, s=skip"); err != nil {
			return fmt.Errorf("write approval action output: %w", err)
		}
	}
	return nil
}

func handleInteractiveInterventionEvent(client interactiveClient, sessionID string, output io.Writer, state *interactiveSessionState, event toolInterventionEvent) error {
	switch event.Type {
	case "runtime.tool_intervention_required":
		pending, err := client.sessionInterventionPending(sessionID, event.TurnID, event.CallID)
		if err != nil {
			return err
		}
		if !pending {
			_, err := fmt.Fprintln(output, "approval already decided; skipping prompt")
			return err
		}
		if !state.setPendingIfChanged(event) {
			return nil
		}
		_, err = fmt.Fprintln(output, "approval action: a=approve, r [reason]=reject, s=skip")
		return err
	case "runtime.tool_intervention_approved", "runtime.tool_intervention_rejected":
		state.clearPending(event.TurnID, event.CallID)
	}
	return nil
}

func runInteractiveInputLoop(client interactiveClient, sessionID string, input io.Reader, output io.Writer, state *interactiveSessionState) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return nil
		}
		if line == "/interrupt" {
			if err := client.sendUserInterrupt(sessionID); err != nil {
				if writeErr := writeInteractiveError(output, "send interrupt", err); writeErr != nil {
					return writeErr
				}
				continue
			}
			if _, err := fmt.Fprintln(output, "interrupt sent"); err != nil {
				return fmt.Errorf("write interrupt output: %w", err)
			}
			continue
		}
		if strings.HasPrefix(line, "/say ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "/say "))
			if line == "" {
				continue
			}
			if err := client.sendUserMessage(sessionID, line); err != nil {
				if writeErr := writeInteractiveError(output, "send user message", err); writeErr != nil {
					return writeErr
				}
				continue
			}
			if _, err := fmt.Fprintln(output, "message sent"); err != nil {
				return fmt.Errorf("write message sent output: %w", err)
			}
			continue
		}

		if pending, ok := state.takePending(); ok {
			handled, err := handleInteractiveApprovalInput(client, sessionID, output, state, pending, line)
			if err != nil {
				if writeErr := writeInteractiveError(output, "handle approval", err); writeErr != nil {
					return writeErr
				}
				continue
			}
			if handled {
				continue
			}
			if !strings.HasPrefix(line, "/say ") {
				if _, err := fmt.Fprintln(output, "approval pending; enter a, r [reason], s, or /say MESSAGE"); err != nil {
					return fmt.Errorf("write approval pending output: %w", err)
				}
				continue
			}
		}

		if err := client.sendUserMessage(sessionID, line); err != nil {
			if writeErr := writeInteractiveError(output, "send user message", err); writeErr != nil {
				return writeErr
			}
			continue
		}
		if _, err := fmt.Fprintln(output, "message sent"); err != nil {
			return fmt.Errorf("write message sent output: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	return nil
}

func writeInteractiveError(output io.Writer, action string, err error) error {
	if _, writeErr := fmt.Fprintf(output, "%s failed: %v\n", action, err); writeErr != nil {
		return fmt.Errorf("write interactive error: %w", writeErr)
	}
	return nil
}

func handleInteractiveApprovalInput(client interactiveClient, sessionID string, output io.Writer, state *interactiveSessionState, pending toolInterventionEvent, line string) (bool, error) {
	action, reason, ok := parseApprovalInput(line)
	if !ok {
		return false, nil
	}
	if action == "skip" {
		state.clearPending(pending.TurnID, pending.CallID)
		_, err := fmt.Fprintln(output, "approval skipped")
		return true, err
	}
	if err := client.decideSessionIntervention(sessionID, pending.TurnID, pending.CallID, action, reason); err != nil {
		return true, fmt.Errorf("%s intervention %s: %w", action, pending.CallID, err)
	}
	state.clearPending(pending.TurnID, pending.CallID)
	_, err := fmt.Fprintf(output, "approval submitted: %s\n", action)
	return true, err
}

func parseApprovalInput(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	fields := strings.Fields(line)
	command := strings.ToLower(fields[0])
	switch command {
	case "a", "approve", "/approve":
		return "approve", "", true
	case "r", "reject", "/reject":
		reason := ""
		if len(fields) > 1 {
			reason = strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		}
		return "reject", reason, true
	case "s", "skip", "/skip":
		return "skip", "", true
	default:
		return "", "", false
	}
}

func printSessionAttachUsage() {
	fmt.Fprint(os.Stderr, sessionAttachUsage())
}

func sessionAttachUsage() string {
	return `Usage:
  tma [--base-url URL] session attach --session SESSION_ID [--after SEQ]

Interactive input:
  message text        send user.message
  /say MESSAGE        send user.message even when a local approval prompt is active
  /interrupt          interrupt the running turn
  /quit               exit attach

Approval input:
  a, approve          approve the pending tool call
  r REASON, reject REASON
                      reject the pending tool call with an optional reason
  s, skip             hide the local prompt and keep the approval pending

Notes:
  session attach is the recommended human CLI entrypoint. It streams session
  events, sends user messages, recovers pending approvals, and lets you decide
  approve/reject/skip in the same command.
`
}
