package httpapi

import (
	"net/http"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skillmarketplace"
)

func TestMarketplacePolicyHTTPLifecycleAndPrecedence(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)

	organization := postJSONWithStatus[struct {
		Policy  skillmarketplace.PolicyRecord  `json:"policy"`
		Version skillmarketplace.PolicyVersion `json:"version"`
	}](t, server, http.MethodPost, "/v1/skill-marketplace-policies", `{
		"scope_type":"organization",
		"organization_id":"org_default",
		"config":{"allowed_owners":["acme"],"require_license":true}
	}`, http.StatusCreated)
	if organization.Policy.CurrentVersion != 1 || organization.Version.Checksum == "" {
		t.Fatalf("unexpected organization policy: %#v", organization)
	}

	workspace := postJSONWithStatus[struct {
		Policy  skillmarketplace.PolicyRecord  `json:"policy"`
		Version skillmarketplace.PolicyVersion `json:"version"`
	}](t, server, http.MethodPost, "/v1/skill-marketplace-policies", `{
		"scope_type":"workspace",
		"workspace_id":"wksp_default",
		"config":{"allowed_repositories":["trusted/review"]}
	}`, http.StatusCreated)
	effective, err := store.ResolveMarketplacePolicy(t.Context(), managedagents.DefaultWorkspaceID)
	if err != nil || effective.Policy.ID != workspace.Policy.ID || effective.Source != skillmarketplace.PolicyScopeWorkspace {
		t.Fatalf("expected workspace policy precedence: effective=%#v err=%v", effective, err)
	}

	version2 := postJSONWithStatus[skillmarketplace.PolicyVersion](t, server, http.MethodPost,
		"/v1/skill-marketplace-policies/"+workspace.Policy.ID+"/versions", `{
			"config":{"allowed_owners":["trusted"],"require_commit_sha":true}
		}`, http.StatusCreated)
	if version2.Version != 2 || version2.Checksum == workspace.Version.Checksum {
		t.Fatalf("unexpected published policy version: %#v", version2)
	}
	gotVersion := getJSON[skillmarketplace.PolicyVersion](t, server,
		"/v1/skill-marketplace-policies/"+workspace.Policy.ID+"/versions/2")
	if gotVersion.Checksum != version2.Checksum {
		t.Fatalf("unexpected fetched version: %#v", gotVersion)
	}

	listed := getJSON[struct {
		Policies []skillmarketplace.PolicyRecord `json:"policies"`
	}](t, server, "/v1/skill-marketplace-policies")
	if len(listed.Policies) != 2 {
		t.Fatalf("unexpected policy list: %#v", listed.Policies)
	}

	archived := postJSONWithStatus[skillmarketplace.PolicyRecord](t, server, http.MethodPost,
		"/v1/skill-marketplace-policies/"+workspace.Policy.ID+"/archive", `{}`, http.StatusOK)
	if archived.Status != skillmarketplace.PolicyStatusArchived {
		t.Fatalf("unexpected archived policy: %#v", archived)
	}
	effective, err = store.ResolveMarketplacePolicy(t.Context(), managedagents.DefaultWorkspaceID)
	if err != nil || effective.Policy.ID != organization.Policy.ID || effective.Source != skillmarketplace.PolicyScopeOrganization {
		t.Fatalf("expected organization fallback: effective=%#v err=%v", effective, err)
	}

	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 20})
	if err != nil || len(audits) != 4 {
		t.Fatalf("expected create/create/publish/archive audits: audits=%#v err=%v", audits, err)
	}
}
