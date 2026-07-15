package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestAgentExportImportRoundTrip(t *testing.T) {
	server := newTestServer()
	source := postJSON[managedagents.Agent](t, server, "/v1/agents", `{
		"name":"Portable Agent",
		"llm_provider":"fake",
		"llm_model":"fake-v1",
		"system":"portable system",
		"tools":{"enabled_tools":["filesystem"]},
		"mcp":{"servers":[]},
		"skills":{"enabled":[]}
	}`)

	exportRequest := httptest.NewRequest(http.MethodGet, "/v1/agents/"+source.ID+"/export", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export expected status 200, got %d: %s", exportResponse.Code, exportResponse.Body.String())
	}
	if disposition := exportResponse.Header().Get("Content-Disposition"); disposition != `attachment; filename="agent-`+source.ID+`.json"` {
		t.Fatalf("unexpected content disposition %q", disposition)
	}
	var document agentExportDocument
	if err := json.NewDecoder(exportResponse.Body).Decode(&document); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if document.Format != agentExportFormat || document.SchemaVersion != 1 || document.SourceConfigVersion != 1 {
		t.Fatalf("unexpected export envelope: %+v", document)
	}
	if document.Agent.Name != source.Name || document.Agent.System != source.ConfigVersion.System {
		t.Fatalf("unexpected exported config: %+v", document.Agent)
	}

	importBody, err := json.Marshal(agentImportRequest{
		Format:        document.Format,
		SchemaVersion: document.SchemaVersion,
		Name:          "Portable Agent Copy",
		Agent:         document.Agent,
	})
	if err != nil {
		t.Fatalf("marshal import: %v", err)
	}
	created := postJSON[managedagents.Agent](t, server, "/v1/agents/import", string(importBody))
	if created.ID == source.ID || created.Name != "Portable Agent Copy" || created.CurrentConfigVersion != 1 {
		t.Fatalf("unexpected imported agent: %+v", created)
	}
	if created.ConfigVersion.LLMProvider != document.Agent.LLMProvider || created.ConfigVersion.LLMModel != document.Agent.LLMModel || created.ConfigVersion.System != document.Agent.System {
		t.Fatalf("import did not preserve config: %+v", created.ConfigVersion)
	}
	if string(created.ConfigVersion.Tools) != string(document.Agent.Tools) || string(created.ConfigVersion.MCP) != string(document.Agent.MCP) || string(created.ConfigVersion.Skills) != string(document.Agent.Skills) {
		t.Fatalf("import did not preserve tooling config: %+v", created.ConfigVersion)
	}

	exportAudit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/operator-audit?action=agent.export")
	if len(exportAudit.Records) != 1 || exportAudit.Records[0].ResourceID != source.ID || exportAudit.Records[0].Outcome != "succeeded" {
		t.Fatalf("unexpected export audit: %+v", exportAudit.Records)
	}
	importAudit := getJSON[struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}](t, server, "/v1/operator-audit?action=agent.import")
	if len(importAudit.Records) != 1 || importAudit.Records[0].ResourceID != created.ID || importAudit.Records[0].Outcome != "succeeded" {
		t.Fatalf("unexpected import audit: %+v", importAudit.Records)
	}
}

func TestAgentImportRejectsUnsupportedSchema(t *testing.T) {
	server := newTestServer()
	postJSONWithStatus[map[string]any](t, server, http.MethodPost, "/v1/agents/import", `{
		"format":"tma.agent",
		"schema_version":2,
		"agent":{"name":"Future Agent","llm_provider":"fake","llm_model":"fake-v1"}
	}`, http.StatusBadRequest)
}
