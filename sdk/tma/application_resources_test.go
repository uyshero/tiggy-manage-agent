package tma

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestApplicationResourceListQueries(t *testing.T) {
	expected := []string{
		"/v2/agents?app_id=svc%2Fapp&external_ref=agent%2Fmain",
		"/v2/environments?app_id=svc%2Fapp&external_ref=environment%2Fdefault",
		"/v2/sessions?app_id=svc%2Fapp&external_ref=session%2Fcase",
		"/v2/skills?app_id=svc%2Fapp&external_ref=skill%2Finterview&workspace_id=wksp%2F1",
		"/v2/mcp-servers?app_id=svc%2Fapp&external_ref=mcp%2Frepository&workspace_id=wksp%2F1",
	}
	requestIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIndex >= len(expected) || r.URL.RequestURI() != expected[requestIndex] {
			t.Fatalf("application resource request %d = %s, want %s", requestIndex, r.URL.RequestURI(), expected[requestIndex])
		}
		requestIndex++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/agents":
			fmt.Fprint(w, `{"agents":[]}`)
		case "/v2/environments":
			fmt.Fprint(w, `{"environments":[]}`)
		case "/v2/sessions":
			fmt.Fprint(w, `{"sessions":[]}`)
		case "/v2/skills":
			fmt.Fprint(w, `{"skills":[]}`)
		case "/v2/mcp-servers":
			fmt.Fprint(w, `{"servers":[]}`)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err = client.Agents.ListByApplication(ctx, ApplicationResourceQuery{AppID: "svc/app", ExternalRef: "agent/main"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Environments.ListByApplication(ctx, ApplicationResourceQuery{AppID: "svc/app", ExternalRef: "environment/default"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Sessions.List(ctx, url.Values{"app_id": {"svc/app"}, "external_ref": {"session/case"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.List(ctx, SkillListQuery{WorkspaceID: "wksp/1", AppID: "svc/app", ExternalRef: "skill/interview"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.MCP.List(ctx, MCPServerQuery{WorkspaceID: "wksp/1", AppID: "svc/app", ExternalRef: "mcp/repository"}); err != nil {
		t.Fatal(err)
	}
	if requestIndex != len(expected) {
		t.Fatalf("received %d application resource requests, want %d", requestIndex, len(expected))
	}
}
