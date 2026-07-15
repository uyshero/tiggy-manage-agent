package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestSessionAttachUsageDescribesInteractiveInput(t *testing.T) {
	usage := sessionAttachUsage()
	for _, expected := range []string{
		"session attach --session SESSION_ID",
		"message text",
		"/say MESSAGE",
		"/interrupt",
		"a, approve",
		"r REASON",
		"s, skip",
	} {
		if !strings.Contains(usage, expected) {
			t.Fatalf("expected usage to contain %q, got %q", expected, usage)
		}
	}
}

func TestCommandSessionAttachHelpDoesNotRequireSession(t *testing.T) {
	stderr := captureStderr(t, func() {
		if err := commandSessionAttach(&apiClient{}, []string{"--help"}); err != nil {
			t.Fatalf("attach help: %v", err)
		}
	})
	if !strings.Contains(stderr, "Interactive input:") {
		t.Fatalf("expected attach help output, got %q", stderr)
	}
}

func TestPrintSessionVersionNoticeShowsCurrentVersion(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/sessions/sesn_000001":
			return jsonResponse(`{"id":"sesn_000001","agent_id":"agt_000001","agent_config_version":2}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v2/agents/agt_000001":
			return jsonResponse(`{"id":"agt_000001","current_config_version":2}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})

	var output bytes.Buffer
	if err := printSessionVersionNotice(client, "sesn_000001", &output); err != nil {
		t.Fatalf("print notice: %v", err)
	}
	if !strings.Contains(output.String(), "agent config: v2") {
		t.Fatalf("expected current version notice, got %q", output.String())
	}
}

func TestPrintSessionVersionNoticeWarnsOutdatedSession(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/sessions/sesn_000001":
			return jsonResponse(`{"id":"sesn_000001","agent_id":"agt_000001","agent_config_version":1}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/v2/agents/agt_000001":
			return jsonResponse(`{"id":"agt_000001","current_config_version":3}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})

	var output bytes.Buffer
	if err := printSessionVersionNotice(client, "sesn_000001", &output); err != nil {
		t.Fatalf("print notice: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "pinned to agent config v1") || !strings.Contains(text, "latest is v3") {
		t.Fatalf("expected outdated version notice, got %q", text)
	}
}

func TestParseApprovalInput(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantAction string
		wantReason string
		wantOK     bool
	}{
		{name: "short approve", line: "a", wantAction: "approve", wantOK: true},
		{name: "slash approve", line: "/approve", wantAction: "approve", wantOK: true},
		{name: "reject reason", line: "r not safe now", wantAction: "reject", wantReason: "not safe now", wantOK: true},
		{name: "slash reject reason", line: "/reject not safe now", wantAction: "reject", wantReason: "not safe now", wantOK: true},
		{name: "skip", line: "s", wantAction: "skip", wantOK: true},
		{name: "slash skip", line: "/skip", wantAction: "skip", wantOK: true},
		{name: "message", line: "hello agent", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, reason, ok := parseApprovalInput(test.line)
			if action != test.wantAction || reason != test.wantReason || ok != test.wantOK {
				t.Fatalf("parse approval input = action %q reason %q ok %v", action, reason, ok)
			}
		})
	}
}

func TestInteractiveInputLoopContinuesAfterSendFailure(t *testing.T) {
	client := &fakeInteractiveClient{
		sendMessageErrors: []error{errors.New("session busy"), nil},
	}
	var output bytes.Buffer
	err := runInteractiveInputLoop(client, "sesn_123", strings.NewReader("hello\nagain\n/quit\n"), &output, &interactiveSessionState{})
	if err != nil {
		t.Fatalf("input loop: %v", err)
	}

	if len(client.messages) != 2 || client.messages[0] != "hello" || client.messages[1] != "again" {
		t.Fatalf("unexpected sent messages: %#v", client.messages)
	}
	text := output.String()
	if !strings.Contains(text, "send user message failed: session busy") {
		t.Fatalf("expected send failure output, got %q", text)
	}
	if !strings.Contains(text, "message sent") {
		t.Fatalf("expected later success output, got %q", text)
	}
}

func TestInteractiveInputLoopContinuesAfterApprovalFailure(t *testing.T) {
	client := &fakeInteractiveClient{
		decisionErrors: []error{errors.New("already decided"), nil},
	}
	state := &interactiveSessionState{}
	state.setPending(toolInterventionEvent{TurnID: "turn_123", CallID: "call_123"})

	var output bytes.Buffer
	err := runInteractiveInputLoop(client, "sesn_123", strings.NewReader("a\na\n/quit\n"), &output, state)
	if err != nil {
		t.Fatalf("input loop: %v", err)
	}

	if len(client.decisions) != 2 {
		t.Fatalf("expected 2 decision attempts, got %#v", client.decisions)
	}
	text := output.String()
	if !strings.Contains(text, "handle approval failed") {
		t.Fatalf("expected approval failure output, got %q", text)
	}
	if !strings.Contains(text, "approval submitted: approve") {
		t.Fatalf("expected later approval success output, got %q", text)
	}
}

func TestAnnouncePendingInterventionsRecoversApproval(t *testing.T) {
	state := &interactiveSessionState{}
	var output bytes.Buffer
	err := announcePendingInterventions(&output, state, []toolInterventionEvent{{
		TurnID:     "turn_123",
		CallID:     "call_123",
		Identifier: "default",
		APIName:    "edit_file",
	}})
	if err != nil {
		t.Fatalf("announce pending: %v", err)
	}

	pending, ok := state.takePending()
	if !ok || pending.TurnID != "turn_123" || pending.CallID != "call_123" {
		t.Fatalf("expected recovered pending approval, got %#v ok=%v", pending, ok)
	}
	text := output.String()
	if !strings.Contains(text, "pending approval recovered: default.edit_file call=call_123") {
		t.Fatalf("expected recovered approval output, got %q", text)
	}
	if !strings.Contains(text, "approval action: a=approve") {
		t.Fatalf("expected approval action output, got %q", text)
	}
}

func TestAnnouncePendingInterventionsSkipsDuplicate(t *testing.T) {
	state := &interactiveSessionState{}
	state.setPending(toolInterventionEvent{TurnID: "turn_123", CallID: "call_123"})
	var output bytes.Buffer
	err := announcePendingInterventions(&output, state, []toolInterventionEvent{{
		TurnID:     "turn_123",
		CallID:     "call_123",
		Identifier: "default",
		APIName:    "edit_file",
	}})
	if err != nil {
		t.Fatalf("announce pending: %v", err)
	}
	if output.String() != "" {
		t.Fatalf("expected duplicate pending approval not to be announced, got %q", output.String())
	}
}

type fakeInteractiveClient struct {
	messages          []string
	interrupts        int
	decisions         []string
	sendMessageErrors []error
	decisionErrors    []error
}

func (c *fakeInteractiveClient) sessionInterventionPending(string, string, string) (bool, error) {
	return true, nil
}

func (c *fakeInteractiveClient) decideSessionIntervention(_ string, _ string, _ string, action string, reason string) error {
	c.decisions = append(c.decisions, action+":"+reason)
	if len(c.decisionErrors) == 0 {
		return nil
	}
	err := c.decisionErrors[0]
	c.decisionErrors = c.decisionErrors[1:]
	return err
}

func (c *fakeInteractiveClient) sendUserMessage(_ string, text string) error {
	c.messages = append(c.messages, text)
	if len(c.sendMessageErrors) == 0 {
		return nil
	}
	err := c.sendMessageErrors[0]
	c.sendMessageErrors = c.sendMessageErrors[1:]
	return err
}

func (c *fakeInteractiveClient) sendUserInterrupt(string) error {
	c.interrupts++
	return nil
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = writer

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	os.Stderr = old
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return string(out)
}
