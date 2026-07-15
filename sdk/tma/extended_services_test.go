package tma

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtendedTypedServices(t *testing.T) {
	type fixture struct {
		status int
		body   string
		check  func(*testing.T, []byte)
	}
	expected := map[string]fixture{
		"GET /v2/agents/default": {body: `{"id":"agt_default","name":"Default"}`},
		"POST /v2/agents/import": {status: http.StatusCreated, body: `{"id":"agt_imported","name":"Imported"}`, check: func(t *testing.T, raw []byte) {
			var request AgentImportRequest
			if err := json.Unmarshal(raw, &request); err != nil || !strings.Contains(string(request.Agent.Tools), `"custom"`) {
				t.Fatalf("agent import request=%s err=%v", raw, err)
			}
		}},
		"GET /v2/agents/agt%2F1/export":                                                   {body: `{"format":"tma.agent","schema_version":1,"exported_at":"2026-07-15T00:00:00Z","agent":{"name":"A","llm_provider":"fake","llm_model":"fake","system":"S","tools":{"custom":true}}}`},
		"POST /v2/agents/agt%2F1/config-versions/7/rollback":                              {status: http.StatusCreated, body: `{"agent":{"id":"agt/1"},"source_version":7,"previous_version":8,"new_version":9}`},
		"POST /v2/agents/agt%2F1/tooling-health":                                          {body: `{"agent_id":"agt/1","checked_at":"2026-07-15T00:00:00Z","mcp":[],"skills":[]}`},
		"PATCH /v2/sessions/sesn%2F1":                                                     {body: `{"id":"sesn/1","status":"active","tags":["sdk"]}`},
		"DELETE /v2/sessions/sesn%2Fdelete":                                               {status: http.StatusNoContent},
		"POST /v2/sessions/sesn%2F1/rerun":                                                {status: http.StatusCreated, body: `{"session":{"id":"sesn/rerun","status":"active"},"source_session_id":"sesn/1","source_event_seq":4,"events":[]}`},
		"GET /v2/session-comparisons?left_session_id=left%2F1&right_session_id=right%2F1": {body: `{"left":{"session":{"id":"left/1"},"usage":{"session_id":"left/1","records":[],"summary":{}},"artifacts":[]},"right":{"session":{"id":"right/1"},"usage":{"session_id":"right/1","records":[],"summary":{}},"artifacts":[]}}`},
		"GET /v2/sessions/sesn%2F1/runtime-config":                                        {body: `{"session_id":"sesn/1","workspace_id":"wksp","owner_id":"user","agent_id":"agt","agent_config_version":1,"environment_id":"env","llm_provider":"fake","llm_model":"fake","llm_capability_type":"text","context_window_tokens":1000,"system":"S","tools":{"custom":{"enabled":true}}}`},
		"GET /v2/sessions/sesn%2F1/runtime-capabilities":                                  {body: `{"default_runtime":"cloud_sandbox","available_runtimes":["cloud_sandbox","future_runtime"]}`},
		"GET /v2/sessions/sesn%2F1/deliberations":                                         {body: `{"deliberations":[]}`},
		"GET /v2/sessions/sesn%2F1/deliberations/del%2F1":                                 {body: deliberationFixture("future_state")},
		"POST /v2/sessions/sesn%2F1/deliberations/del%2F1/cancel":                         {body: deliberationFixture("canceled")},
		"POST /v2/sessions/sesn%2F1/deliberations/del%2F1/participants/2/retry":           {body: deliberationFixture("running")},
		"GET /v2/sessions/sesn%2F1/task-groups":                                           {body: `{"task_groups":[]}`},
		"GET /v2/sessions/sesn%2F1/task-group-tree":                                       {body: `{"root":{"session":{"id":"sesn/1"},"task_groups":[],"children":[]},"summary":{}}`},
		"GET /v2/sessions/sesn%2F1/task-groups/grp%2F1":                                   {body: `{"state":{"group":{"id":"grp/1"},"status":"future_state","summary":{},"aggregate":{},"items":[]}}`},
		"POST /v2/sessions/sesn%2F1/task-groups/grp%2F1/cancel":                           {body: taskGroupFixture("canceled")},
		"POST /v2/sessions/sesn%2F1/task-groups/grp%2F1/retry":                            {body: taskGroupFixture("running")},
		"POST /v2/sessions/sesn%2F1/task-groups/grp%2F1/items/3/retry":                    {body: taskGroupFixture("running")},
		"POST /v2/subagents/reap-orphans":                                                 {body: `{"count":0,"reaped":[]}`},
		"GET /v2/traces?cursor=next%2Fcursor&include_archived=true&limit=25&session_id=sesn%2F1&session_status=active&turn_id=turn%2F1&workspace_id=wksp%2F1": {body: `{"items":[{"trace_id":"trace/1","session_id":"sesn/1","turn_id":"turn/1","turn_status":"future_state","duration_ms":1,"step_count":1,"span_count":1,"tool_calls":0,"errors":0}],"next_cursor":"opaque-next","has_more":true}`},
		"GET /v2/traces/trace%2F1": {body: `{"session_id":"sesn/1","turn_id":"turn/1","trace_id":"trace/1","status":"future_state","steps":[],"spans":[]}`},
		"GET /v2/spans?critical=true&cursor=span%2Fcursor&include_archived=true&kind=tool&limit=10&max_duration_ms=100&min_duration_ms=5&min_self_duration_ms=2&q=read+file&session_id=sesn%2F1&status=future_state&trace_id=trace%2F1&turn_id=turn%2F1&workspace_id=wksp%2F1": {body: `{"items":[{"trace_id":"trace/1","session_id":"sesn/1","turn_id":"turn/1","span_id":"span/1","name":"read","kind":"tool","status":"future_state","start_time":"2026-07-15T00:00:00Z","duration_ms":10,"event_count":1}],"next_cursor":"","has_more":false}`},
		"GET /v2/traces/trace%2F1/spans/span%2F1": {body: `{"session_id":"sesn/1","turn_id":"turn/1","trace_id":"trace/1","span":{"trace_id":"trace/1","span_id":"span/1","name":"read","kind":"tool","status":"future_state","start_time":"2026-07-15T00:00:00Z","end_time":"2026-07-15T00:00:01Z","duration_ms":1000,"attributes":{"dynamic":"preserved"},"events":[]}}`},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		item, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected request %s", key)
		}
		delete(expected, key)
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		if item.check != nil {
			item.check(t, raw)
		}
		status := item.status
		if status == 0 {
			status = http.StatusOK
		}
		if item.body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		fmt.Fprint(w, item.body)
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err = client.Agents.Default(ctx); err != nil {
		t.Fatal(err)
	}
	tools := json.RawMessage(`{"custom":{"enabled":true}}`)
	if _, err = client.Agents.Import(ctx, AgentImportRequest{Format: PortableAgentFormat, SchemaVersion: PortableAgentSchemaVersion, Agent: PortableAgentConfig{Name: "A", System: "S", Tools: tools}}); err != nil {
		t.Fatal(err)
	}
	exported, err := client.Agents.Export(ctx, "agt/1")
	if err != nil || !strings.Contains(string(exported.Agent.Tools), `"custom"`) {
		t.Fatalf("export=%+v err=%v", exported, err)
	}
	if _, err = client.Agents.Rollback(ctx, "agt/1", 7); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Agents.ToolingHealth(ctx, "agt/1", ToolingHealthRequest{}); err != nil {
		t.Fatal(err)
	}
	pinned := true
	tags := []string{"sdk"}
	if _, err = client.Sessions.UpdateMetadata(ctx, "sesn/1", UpdateSessionMetadataRequest{Pinned: &pinned, Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	if err = client.Sessions.Delete(ctx, "sesn/delete"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.Rerun(ctx, "sesn/1", RerunSessionRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.Compare(ctx, "left/1", "right/1"); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := client.Sessions.RuntimeConfig(ctx, "sesn/1")
	if err != nil || !strings.Contains(string(runtimeConfig.Tools), `"custom"`) {
		t.Fatalf("runtime config=%+v err=%v", runtimeConfig, err)
	}
	if _, err = client.Sessions.RuntimeCapabilities(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.ListDeliberations(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	deliberation, err := client.Orchestration.GetDeliberation(ctx, "sesn/1", "del/1")
	if err != nil || deliberation.Deliberation.Status != "future_state" {
		t.Fatalf("deliberation=%+v err=%v", deliberation, err)
	}
	if _, err = client.Orchestration.CancelDeliberation(ctx, "sesn/1", "del/1", CancelAgentDeliberationRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.RetryDeliberationParticipant(ctx, "sesn/1", "del/1", 2, RetryAgentDeliberationParticipantRequest{RoundNumber: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.ListTaskGroups(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.TaskGroupTree(ctx, "sesn/1"); err != nil {
		t.Fatal(err)
	}
	group, err := client.Orchestration.GetTaskGroup(ctx, "sesn/1", "grp/1")
	if err != nil || group.State.Status != "future_state" {
		t.Fatalf("group=%+v err=%v", group, err)
	}
	if _, err = client.Orchestration.CancelTaskGroup(ctx, "sesn/1", "grp/1", CancelTaskGroupRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.RetryTaskGroup(ctx, "sesn/1", "grp/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.RetryTaskGroupItem(ctx, "sesn/1", "grp/1", 3); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Orchestration.ReapOrphans(ctx, ReapOrphanSubagentsRequest{}); err != nil {
		t.Fatal(err)
	}
	page, err := client.Traces.List(ctx, TraceListQuery{WorkspaceID: "wksp/1", SessionID: "sesn/1", TurnID: "turn/1", SessionStatus: "active", IncludeArchived: true, Limit: 25, Cursor: "next/cursor"})
	if err != nil || len(page.Items) != 1 || page.Items[0].TurnStatus != "future_state" || page.NextCursor != "opaque-next" {
		t.Fatalf("trace page=%+v err=%v", page, err)
	}
	trace, err := client.Traces.Get(ctx, "trace/1")
	if err != nil || trace.Status != "future_state" {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	critical := true
	spanPage, err := client.Traces.ListSpans(ctx, TraceSpanListQuery{WorkspaceID: "wksp/1", TraceID: "trace/1", SessionID: "sesn/1", TurnID: "turn/1", Kind: "tool", Status: "future_state", Search: "read file", Critical: &critical, MinDurationMillis: 5, MaxDurationMillis: 100, MinSelfDurationMillis: 2, IncludeArchived: true, Limit: 10, Cursor: "span/cursor"})
	if err != nil || len(spanPage.Items) != 1 || spanPage.Items[0].Status != "future_state" {
		t.Fatalf("span page=%+v err=%v", spanPage, err)
	}
	span, err := client.Traces.GetSpan(ctx, "trace/1", "span/1")
	if err != nil || span.Span.Status != "future_state" || span.Span.Attributes["dynamic"] != "preserved" {
		t.Fatalf("span=%+v err=%v", span, err)
	}
	if len(expected) != 0 {
		t.Fatalf("operations not called: %#v", expected)
	}
}

func deliberationFixture(status string) string {
	return fmt.Sprintf(`{"deliberation":{"id":"del/1","status":%q,"plan":{"dynamic":true}},"participants":[],"rounds":[]}`, status)
}

func taskGroupFixture(status string) string {
	return fmt.Sprintf(`{"group":{"id":"grp/1"},"status":%q,"summary":{},"aggregate":{},"items":[]}`, status)
}
