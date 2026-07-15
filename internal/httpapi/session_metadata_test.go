package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestSessionMetadataPinTagsSummaryAndAudit(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "organized-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	older := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Older task")
	newer := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Newer task")
	if _, err := store.SaveSessionSummary(older.ID, managedagents.UpsertSessionSummaryInput{
		SummaryText: "Found the root cause and added coverage.", SourceUntilSeq: 8,
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)

	updated := postJSONWithStatus[managedagents.Session](t, server, http.MethodPatch, "/v1/sessions/"+older.ID, `{
		"pinned":true,
		"tags":["代码","代码"," 调研 "]
	}`, http.StatusOK)
	if updated.PinnedAt == nil || len(updated.Tags) != 2 || updated.Tags[0] != "代码" || updated.Tags[1] != "调研" {
		t.Fatalf("unexpected session metadata: %#v", updated)
	}
	if updated.SummaryText != "Found the root cause and added coverage." {
		t.Fatalf("expected summary on metadata response, got %q", updated.SummaryText)
	}

	listed := getJSON[struct {
		Sessions []managedagents.Session `json:"sessions"`
	}](t, server, "/v1/sessions?limit=10")
	if len(listed.Sessions) != 2 || listed.Sessions[0].ID != older.ID || listed.Sessions[1].ID != newer.ID {
		t.Fatalf("expected pinned session first, got %#v", listed.Sessions)
	}
	if listed.Sessions[0].SummaryText == "" {
		t.Fatalf("expected summary in session list: %#v", listed.Sessions[0])
	}

	audit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/operator-audit?action=session.metadata.update")
	if len(audit.Records) != 1 || audit.Records[0].ResourceID != older.ID || audit.Records[0].Outcome != "succeeded" {
		t.Fatalf("unexpected metadata audit: %#v", audit.Records)
	}

	unpinned := postJSONWithStatus[map[string]json.RawMessage](t, server, http.MethodPatch, "/v1/sessions/"+older.ID, `{
		"pinned":false
	}`, http.StatusOK)
	pinnedAt, exists := unpinned["pinned_at"]
	if !exists || string(pinnedAt) != "null" {
		t.Fatalf("expected explicit null pinned_at after unpin, got %s", pinnedAt)
	}
}

func TestSessionMetadataRejectsTooManyTags(t *testing.T) {
	store := newTestStore()
	agent := mustCreateAgentForSubagentTest(t, store, "tag-limit-agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	session := mustCreateSessionForSubagentTest(t, store, agent.ID, environment.ID, "Tagged task")
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)

	postJSONWithStatus[map[string]any](t, server, http.MethodPatch, "/v1/sessions/"+session.ID, `{
		"tags":["1","2","3","4","5","6","7","8","9"]
	}`, http.StatusBadRequest)
}
