package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/model"
)

func TestListToolPermissionAuditRejectsInvalidFilters(t *testing.T) {
	server := newTestServer()
	for _, target := range []string{
		"/v2/sessions/missing/tool-permission-audit?decision=maybe",
		"/v2/sessions/missing/tool-permission-audit?limit=201",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want %d; body=%s", target, response.Code, http.StatusBadRequest, response.Body.String())
		}
	}
}

func TestListToolPermissionAuditUsesStableCursor(t *testing.T) {
	store := newTestStore()
	store.sessions["session_1"] = managedagents.Session{ID: "session_1", WorkspaceID: "workspace_1"}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for index := 0; index < 3; index++ {
		callID := "call_" + string(rune('a'+index))
		plan := agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{{
			Call:            model.ToolCall{ID: callID, Name: "default_edit_file", Arguments: json.RawMessage(`{"path":"/workspace/src/main.go"}`)},
			Disposition:     agentcore.ToolDispositionExecute,
			ValidationState: agentcore.ToolValidationValid,
			ApprovalState:   agentcore.ToolApprovalNotRequired,
			Permission:      &agentcore.ToolPermissionDecision{Decision: "allow", Allowed: true, Mode: "request_approval", ApprovalPolicy: "never"},
		}}}
		store.events["session_1"] = append(store.events["session_1"], auditTestEvent(t, "session_1", "turn_"+callID, string(agentcore.EventToolBatchPlanned), plan, now.Add(time.Duration(index)*time.Second)))
	}
	server := &Server{store: store}
	first := getToolPermissionAuditPage(t, server, "/v2/sessions/session_1/tool-permission-audit?limit=2")
	if len(first.Records) != 2 || !first.HasMore || first.NextCursor == "" || first.Records[0].CallID != "call_c" || first.Records[1].CallID != "call_b" {
		t.Fatalf("first page = %#v", first)
	}
	second := getToolPermissionAuditPage(t, server, "/v2/sessions/session_1/tool-permission-audit?limit=2&cursor="+url.QueryEscape(first.NextCursor))
	if len(second.Records) != 1 || second.HasMore || second.NextCursor != "" || second.Records[0].CallID != "call_a" {
		t.Fatalf("second page = %#v", second)
	}
	request := httptest.NewRequest(http.MethodGet, "/v2/sessions/session_1/tool-permission-audit?decision=deny&limit=2&cursor="+url.QueryEscape(first.NextCursor), nil)
	request.SetPathValue("session_id", "session_1")
	response := httptest.NewRecorder()
	server.listSessionToolPermissionAudit(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("filter-mismatched cursor status = %d; body=%s", response.Code, response.Body.String())
	}
}

type toolPermissionAuditTestPage struct {
	Records    []managedagents.ToolPermissionAuditRecord `json:"records"`
	NextCursor string                                    `json:"next_cursor"`
	HasMore    bool                                      `json:"has_more"`
}

func getToolPermissionAuditPage(t *testing.T, server *Server, target string) toolPermissionAuditTestPage {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.SetPathValue("session_id", "session_1")
	response := httptest.NewRecorder()
	server.listSessionToolPermissionAudit(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d; body=%s", target, response.Code, response.Body.String())
	}
	var page toolPermissionAuditTestPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	return page
}

func TestProjectToolPermissionAuditCombinesPlanApprovalAndResult(t *testing.T) {
	now := time.Now().UTC()
	plan := agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{{
		Call:            model.ToolCall{ID: "call_ask", Name: "default_edit_file", Arguments: json.RawMessage(`{"path":"/workspace/src/main.go"}`)},
		Disposition:     agentcore.ToolDispositionExecute,
		ValidationState: agentcore.ToolValidationValid,
		ApprovalState:   agentcore.ToolApprovalPending,
		ApprovalSource:  agentcore.ToolApprovalSourceHuman,
		Permission: &agentcore.ToolPermissionDecision{
			Decision: "ask", Required: true, Mode: "request_approval",
			ApprovalPolicy: "conditional", Reason: "filesystem_write", Risk: "write",
			MatchedRuleID: "ask-src", RuleSource: "session",
		},
	}, {
		Call:            model.ToolCall{ID: "call_deny", Name: "default_edit_file", Arguments: json.RawMessage(`{"path":"/workspace/secrets/token"}`)},
		Disposition:     agentcore.ToolDispositionDenied,
		ValidationState: agentcore.ToolValidationValid,
		ApprovalState:   agentcore.ToolApprovalNotRequired,
		Permission: &agentcore.ToolPermissionDecision{
			Decision: "deny", Mode: "full_access", ApprovalPolicy: "conditional",
			Reason: "workspace_boundary", Risk: "write", MatchedRuleID: "deny-secrets", RuleSource: "workspace",
		},
	}}}
	result := agentcore.ToolCallJournalEntry{CallID: "call_ask", Name: "default_edit_file", Status: agentcore.ToolCallSucceeded}
	deniedResult := agentcore.ToolCallJournalEntry{CallID: "call_deny", Name: "default_edit_file", Status: agentcore.ToolCallFailed}
	events := []managedagents.Event{
		auditTestEvent(t, "session_1", "turn_1", string(agentcore.EventToolBatchPlanned), plan, now),
		auditTestEvent(t, "session_1", "turn_1", string(agentcore.EventInterventionResolved), []agentcore.InteractionDecision{{InteractionID: "tool_approval:call_ask", Status: "approved"}}, now.Add(time.Second)),
		auditTestEvent(t, "session_1", "turn_1", string(agentcore.EventToolCallResult), result, now.Add(2*time.Second)),
		auditTestEvent(t, "session_1", "turn_1", string(agentcore.EventToolCallResult), deniedResult, now.Add(3*time.Second)),
	}

	records := projectToolPermissionAudit(events)
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	byCall := map[string]toolPermissionAuditRecord{}
	for _, record := range records {
		byCall[record.CallID] = record
	}
	ask := byCall["call_ask"]
	if ask.Decision != "ask" || ask.ApprovalStatus != "approved" || ask.ExecutionStatus != "succeeded" || ask.Path != "/workspace/src/main.go" || ask.RuleSource != "session" {
		t.Fatalf("ask record = %#v", ask)
	}
	deny := byCall["call_deny"]
	if deny.Decision != "deny" || deny.ApprovalStatus != "not_required" || deny.ExecutionStatus != "denied" || deny.MatchedRuleID != "deny-secrets" {
		t.Fatalf("deny record = %#v", deny)
	}
}

func auditTestEvent(t *testing.T, sessionID string, turnID string, eventType string, data any, createdAt time.Time) managedagents.Event {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"turn_id": turnID, "data": data})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return managedagents.Event{SessionID: sessionID, TurnID: turnID, Type: eventType, Payload: payload, CreatedAt: createdAt}
}
