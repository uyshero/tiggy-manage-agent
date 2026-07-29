package workbenchprojects

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitLabProvisionerCreatesProjectAndCommitsTemplate(t *testing.T) {
	t.Helper()
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "secret" {
			t.Fatalf("missing GitLab token header")
		}
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v4/projects":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["path"] != "survival-demo" || payload["visibility"] != "private" {
				t.Fatalf("unexpected create payload: %#v", payload)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"web_url":"https://gitlab.example/acme/survival-demo","default_branch":""}`))
		case "GET /api/v4/projects/42/repository/tree":
			_, _ = w.Write([]byte(`[]`))
		case "POST /api/v4/projects/42/repository/commits":
			var payload struct {
				Branch  string              `json:"branch"`
				Actions []map[string]string `json:"actions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Branch != "main" || len(payload.Actions) != 2 || payload.Actions[0]["action"] != "create" {
				t.Fatalf("unexpected commit payload: %#v", payload)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"commit"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provisioner, err := NewGitLabProvisioner(GitLabConfig{APIURL: server.URL + "/api/v4", Token: "secret", NamespaceID: "7"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := provisioner.Provision(context.Background(), ProvisionInput{
		Name: "Survival demo", RepositoryPath: "survival-demo",
		Files: []TemplateFile{{Path: "README.md", Content: "demo"}, {Path: "R/model.R", Content: "fit <- function() {}"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RepositoryID != "42" || result.DefaultBranch != "main" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(requests) != 3 {
		t.Fatalf("expected create, inspect, and commit requests, got %#v", requests)
	}
}

func TestGitLabProvisionerReusesInitializedProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("initialized repository must not be mutated: %s", r.Method)
		}
		_, _ = w.Write([]byte(`[{"name":"README.md"}]`))
	}))
	defer server.Close()
	provisioner, err := NewGitLabProvisioner(GitLabConfig{APIURL: server.URL, Token: "secret"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := provisioner.Provision(context.Background(), ProvisionInput{ExistingRepositoryID: "42", DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RepositoryID != "42" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
