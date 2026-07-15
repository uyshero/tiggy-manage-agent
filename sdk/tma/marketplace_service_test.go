package tma

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestTypedMarketplaceService(t *testing.T) {
	entry := `{"id":"entry/1","workspace_id":"wksp/1","skill_id":"skill/1","skill_version":1,"skill_identifier":"review","skill_title":"Review","skill_status":"active","version_checksum_sha256":"checksum","package_format":"tma.skill-package.v1","tags":[],"status":"draft","created_by":"user_1","created_at":"2026-07-15T00:00:00Z","updated_by":"user_1","updated_at":"2026-07-15T00:00:00Z"}`
	policy := `{"id":"policy/1","scope_type":"workspace","workspace_id":"wksp/1","status":"active","current_version":1,"created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	policyVersion := `{"id":"policy-version/1","policy_id":"policy/1","version":1,"config":{"allowed_owners":["acme"]},"checksum_sha256":"revision","created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	preview := `{"identifier":"review","source":{"provider":"github","repository":"acme/review"},"assets":{"files":[],"total_bytes":0},"policy":{"allowed":true,"checks":[]},"security":{"digest_sha256":"digest","attestation":{"status":"missing","digest_sha256":"digest","message":"missing"},"findings":[],"scanned_files":1,"binary_files":[],"sbom":{"format":"tma.skill.sbom.v1","package_digest_sha256":"digest","components":[]}},"install_state":"new_install","changes":{"content_changed":false,"added_files":[],"removed_files":[],"changed_files":[]}}`
	skill := `{"id":"skill/1","workspace_id":"wksp/1","identifier":"review","title":"Review","owner_type":"workspace","source_type":"github","status":"active","created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	version := `{"id":"version/1","skill_id":"skill/1","version":1,"content_format":"markdown","manifest":{},"content_text":"Review","checksum_sha256":"checksum","package_format":"tma.skill-package.v1","created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	install := `{"skill":` + skill + `,"version":` + version + `}`
	enable := `{"agent_id":"agent/1","previous_config_version":1,"new_config_version":2,"current_session_version":1,"binding":{"skill":"review","version":1},"changed":true,"requires_session_upgrade":true}`
	disable := `{"agent_id":"agent/1","previous_config_version":2,"new_config_version":3,"current_session_version":1,"binding":{"skill":"review","version":1},"removed":true,"requires_session_upgrade":true}`

	expected := map[string]string{
		"GET /v2/skills/marketplace/discover?limit=10&query=review&repository=acme%2Freview&session_id=session%2F1": `{"provider":"github","search_mode":"repository","items":[],"count":0}`,
		"POST /v2/skills/marketplace/preview": preview,
		"POST /v2/skills/marketplace/install": install,
		"GET /v2/skills/marketplace/internal?category=quality&limit=20&query=review&session_id=session%2F1&tag=go&tag=security": `{"provider":"catalog","items":[],"count":0}`,
		"POST /v2/skills/marketplace/internal/preview":                                                preview,
		"POST /v2/skills/marketplace/internal/install":                                                install,
		"POST /v2/skills/skill%2F1/enable":                                                            enable,
		"POST /v2/skills/skill%2F1/disable":                                                           disable,
		"POST /v2/skill-marketplace-entries":                                                          entry,
		"GET /v2/skill-marketplace-entries?include_withdrawn=true&status=draft&workspace_id=wksp%2F1": `{"entries":[` + entry + `]}`,
		"GET /v2/skill-marketplace-entries/entry%2F1?workspace_id=wksp%2F1":                           entry,
		"PATCH /v2/skill-marketplace-entries/entry%2F1":                                               entry,
		"POST /v2/skill-marketplace-entries/entry%2F1/submit":                                         entry,
		"POST /v2/skill-marketplace-entries/entry%2F1/publish":                                        entry,
		"POST /v2/skill-marketplace-entries/entry%2F1/withdraw":                                       entry,
		"POST /v2/skill-marketplace-policies":                                                         `{"policy":` + policy + `,"version":` + policyVersion + `}`,
		"GET /v2/skill-marketplace-policies?include_archived=true&workspace_id=wksp%2F1":              `{"policies":[` + policy + `]}`,
		"GET /v2/skill-marketplace-policies/policy%2F1":                                               `{"policy":` + policy + `,"version":` + policyVersion + `}`,
		"POST /v2/skill-marketplace-policies/policy%2F1/versions":                                     policyVersion,
		"GET /v2/skill-marketplace-policies/policy%2F1/versions/1":                                    policyVersion,
		"POST /v2/skill-marketplace-policies/policy%2F1/archive":                                      strings.Replace(policy, `"status":"active"`, `"status":"archived"`, 1),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		body, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected Marketplace request %s", key)
		}
		delete(expected, key)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && (r.URL.Path == "/v2/skills/marketplace/install" || r.URL.Path == "/v2/skills/marketplace/internal/install" || r.URL.Path == "/v2/skill-marketplace-entries" || r.URL.Path == "/v2/skill-marketplace-policies" || strings.HasSuffix(r.URL.Path, "/versions")) {
			w.WriteHeader(http.StatusCreated)
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	source := MarketplaceSource{Provider: "github", Repository: "acme/review"}
	if _, err = client.Marketplace.Discover(ctx, MarketplaceDiscoverQuery{SessionID: "session/1", Query: "review", Repository: "acme/review", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.Preview(ctx, MarketplacePreviewRequest{SessionID: "session/1", Source: source}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.Install(ctx, MarketplaceInstallRequest{SessionID: "session/1", Source: source}); err != nil {
		t.Fatal(err)
	}
	internalQuery := MarketplaceInternalQuery{SessionID: "session/1", Query: "review", Category: "quality", Tags: []string{"go", "security"}, Limit: 20}
	if _, err = client.Marketplace.BrowseInternal(ctx, internalQuery); err != nil {
		t.Fatal(err)
	}
	catalogRequest := MarketplacePreviewRequest{SessionID: "session/1", Source: MarketplaceSource{Provider: "catalog", CatalogEntryID: "entry/1"}}
	if _, err = client.Marketplace.PreviewInternal(ctx, catalogRequest); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.InstallInternal(ctx, MarketplaceInstallRequest{SessionID: "session/1", Source: catalogRequest.Source}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.EnableInstalled(ctx, "skill/1", MarketplaceEnableRequest{SessionID: "session/1", Version: 1, Inputs: json.RawMessage(`{"style":"strict"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.DisableInstalled(ctx, "skill/1", MarketplaceDisableRequest{SessionID: "session/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.CreateEntry(ctx, CreateMarketplaceEntryRequest{WorkspaceID: "wksp/1", SkillID: "skill/1", SkillVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.ListEntries(ctx, MarketplaceEntryQuery{WorkspaceID: "wksp/1", Status: "draft", IncludeWithdrawn: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.GetEntry(ctx, "entry/1", "wksp/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.UpdateEntry(ctx, "entry/1", UpdateMarketplaceEntryRequest{Summary: "review"}); err != nil {
		t.Fatal(err)
	}
	transition := MarketplaceTransitionRequest{WorkspaceID: "wksp/1", Note: "approved"}
	if _, err = client.Marketplace.SubmitEntry(ctx, "entry/1", transition); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.PublishEntry(ctx, "entry/1", transition); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.WithdrawEntry(ctx, "entry/1", transition); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.CreatePolicy(ctx, CreateMarketplacePolicyRequest{ScopeType: "workspace", WorkspaceID: "wksp/1", Config: MarketplacePolicyConfig{AllowedOwners: []string{"acme"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.ListPolicies(ctx, MarketplacePolicyQuery{WorkspaceID: "wksp/1", IncludeArchived: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.GetPolicy(ctx, "policy/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.PublishPolicyVersion(ctx, "policy/1", PublishMarketplacePolicyRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.GetPolicyVersion(ctx, "policy/1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Marketplace.ArchivePolicy(ctx, "policy/1"); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("Marketplace operations not called: %#v", expected)
	}
}

func TestClientMarketplaceFieldIsTyped(t *testing.T) {
	field, ok := reflect.TypeOf(Client{}).FieldByName("Marketplace")
	if !ok || field.Type.Kind() != reflect.Pointer || field.Type.Elem().Name() != "MarketplaceService" {
		t.Fatalf("Client.Marketplace must be *MarketplaceService, got %v", field.Type)
	}
}
