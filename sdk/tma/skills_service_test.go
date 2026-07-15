package tma

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestTypedSkillsService(t *testing.T) {
	skill := `{"id":"skl/1","workspace_id":"wksp/1","identifier":"review","title":"Review","owner_type":"workspace","source_type":"inline","status":"active","created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	version := `{"id":"sklv_1","skill_id":"skl/1","version":1,"content_format":"hybrid","manifest":{"system_role":"Review carefully"},"content_text":"Check behavior","checksum_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","package_format":"tma.skill-package.v1","created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	policy := `{"id":"sarp/1","scope_type":"workspace","workspace_id":"wksp/1","status":"active","current_version":1,"created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	policyVersion := `{"id":"sarpv_1","policy_id":"sarp/1","version":1,"config":{"enabled":true,"retention_days":30,"delete_limit":25},"checksum_sha256":"revision-1","created_by":"user_1","created_at":"2026-07-15T00:00:00Z"}`
	gcRun := `{"id":"sagcr/1","workspace_id":"wksp/1","policy_source":"workspace","policy_id":"sarp/1","policy_version":1,"policy_revision":"revision-1","retention_days":30,"delete_limit":25,"status":"succeeded","candidate_count":0,"deleted_count":0,"skipped_count":0,"failed_count":0,"bytes_deleted":0,"requested_by":"user_1","started_at":"2026-07-15T00:00:00Z","finished_at":"2026-07-15T00:00:01Z"}`
	expected := map[string]string{
		"POST /v2/skills": skill,
		"GET /v2/skills?include_archived=true&workspace_id=wksp%2F1":                         `{"skills":[` + skill + `]}`,
		"GET /v2/skills/skl%2F1":                                                             skill,
		"POST /v2/skills/skl%2F1/archive":                                                    strings.Replace(skill, `"status":"active"`, `"status":"archived"`, 1),
		"POST /v2/skills/skl%2F1/versions":                                                   version,
		"GET /v2/skills/skl%2F1/versions":                                                    `{"versions":[` + version + `]}`,
		"GET /v2/skills/skl%2F1/versions/1":                                                  version,
		"GET /v2/skills/skl%2F1/versions/1/package":                                          "skill-package",
		"POST /v2/skills/resolve-preview":                                                    `{"config":{"enabled":[{"skill":"review","version":1}]},"rendered":{"format":"tma.skills.context.v1","content":"Review carefully"},"skills":[],"estimated_tokens":12,"truncated":false}`,
		"GET /v2/sessions/sesn%2F1/skill-usages?turn_id=turn%2F1":                            `{"skill_usages":[]}`,
		"POST /v2/skill-packages/backfill":                                                   `{"workspace_id":"wksp/1","scanned":1,"migrated":1}`,
		"GET /v2/skill-asset-retention/effective?workspace_id=wksp%2F1":                      `{"source":"workspace","policy":` + policy + `,"version":` + policyVersion + `,"config":{"enabled":true,"retention_days":30,"delete_limit":25},"revision":"revision-1"}`,
		"POST /v2/skill-asset-retention/policies":                                            `{"policy":` + policy + `,"version":` + policyVersion + `}`,
		"GET /v2/skill-asset-retention/policies?include_archived=true&workspace_id=wksp%2F1": `{"policies":[` + policy + `]}`,
		"GET /v2/skill-asset-retention/policies/sarp%2F1":                                    `{"policy":` + policy + `,"version":` + policyVersion + `}`,
		"POST /v2/skill-asset-retention/policies/sarp%2F1/versions":                          policyVersion,
		"GET /v2/skill-asset-retention/policies/sarp%2F1/versions/1":                         policyVersion,
		"POST /v2/skill-asset-retention/policies/sarp%2F1/archive":                           strings.Replace(policy, `"status":"active"`, `"status":"archived"`, 1),
		"POST /v2/skill-asset-gc/preview":                                                    `{"workspace_id":"wksp/1","effective_policy":{"source":"workspace","config":{"enabled":true,"retention_days":30,"delete_limit":25},"revision":"revision-1"},"cutoff":"2026-06-15T00:00:00Z","candidate_count":0,"candidate_bytes":0,"candidates":[]}`,
		"POST /v2/skill-asset-gc/run":                                                        `{"run":` + gcRun + `,"items":[]}`,
		"GET /v2/skill-asset-gc/runs?limit=20&workspace_id=wksp%2F1":                         `{"runs":[` + gcRun + `]}`,
		"GET /v2/skill-asset-gc/runs/sagcr%2F1":                                              `{"run":` + gcRun + `,"items":[]}`,
		"GET /v2/skill-asset-gc/tombstones?limit=20&workspace_id=wksp%2F1":                   `{"tombstones":[]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		body, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected Skills request %s", key)
		}
		delete(expected, key)
		if strings.HasSuffix(r.URL.Path, "/package") {
			w.Header().Set("Content-Type", "application/zip")
			fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if key == "POST /v2/skills" || strings.HasSuffix(key, "/versions") && r.Method == http.MethodPost || key == "POST /v2/skill-asset-retention/policies" {
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
	if _, err = client.Skills.Create(ctx, CreateSkillRequest{Identifier: "review", Title: "Review"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.List(ctx, SkillListQuery{WorkspaceID: "wksp/1", IncludeArchived: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.Get(ctx, "skl/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.Archive(ctx, "skl/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.CreateVersion(ctx, "skl/1", CreateSkillVersionRequest{Manifest: SkillManifest{}, ContentText: "Check behavior"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.ListVersions(ctx, "skl/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.GetVersion(ctx, "skl/1", 1); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = client.Skills.DownloadPackage(ctx, "skl/1", 1, &archive); err != nil || archive.String() != "skill-package" {
		t.Fatalf("package=%q err=%v", archive.String(), err)
	}
	if _, err = client.Skills.ResolvePreview(ctx, ResolveSkillsPreviewRequest{Skills: SkillConfig{Enabled: []EnabledSkill{{Skill: "review", Version: 1}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.ListUsages(ctx, "sesn/1", "turn/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.BackfillPackages(ctx, SkillPackageBackfillRequest{WorkspaceID: "wksp/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.EffectiveRetentionPolicy(ctx, "wksp/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.CreateRetentionPolicy(ctx, CreateSkillRetentionPolicyRequest{ScopeType: "workspace", WorkspaceID: "wksp/1", Config: SkillRetentionPolicyConfig{Enabled: true, RetentionDays: 30, DeleteLimit: 25}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.ListRetentionPolicies(ctx, SkillRetentionPolicyQuery{WorkspaceID: "wksp/1", IncludeArchived: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.GetRetentionPolicy(ctx, "sarp/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.PublishRetentionPolicyVersion(ctx, "sarp/1", PublishSkillRetentionPolicyRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.GetRetentionPolicyVersion(ctx, "sarp/1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.ArchiveRetentionPolicy(ctx, "sarp/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.PreviewAssetGC(ctx, SkillAssetGCRequest{WorkspaceID: "wksp/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.RunAssetGC(ctx, SkillAssetGCRequest{WorkspaceID: "wksp/1", Confirm: "DELETE"}); err != nil {
		t.Fatal(err)
	}
	gcQuery := SkillAssetGCListQuery{WorkspaceID: "wksp/1", Limit: 20}
	if _, err = client.Skills.ListAssetGCRuns(ctx, gcQuery); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.GetAssetGCRun(ctx, "sagcr/1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Skills.ListAssetGCTombstones(ctx, gcQuery); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 0 {
		t.Fatalf("Skills operations not called: %#v", expected)
	}
}

func TestClientSkillsFieldIsTyped(t *testing.T) {
	field, ok := reflect.TypeOf(Client{}).FieldByName("Skills")
	if !ok || field.Type.Kind() != reflect.Pointer || field.Type.Elem().Name() != "SkillsService" {
		t.Fatalf("Client.Skills must be *SkillsService, got %v", field.Type)
	}
}

func TestSkillVersionAcceptsLegacyAssetArray(t *testing.T) {
	var version SkillVersion
	if err := json.Unmarshal([]byte(`{"assets":[{"path":"README.md","content":"hello","size":5}]}`), &version); err != nil {
		t.Fatalf("decode legacy Skill assets: %v", err)
	}
	if version.Assets == nil || len(version.Assets.Files) != 1 || version.Assets.Files[0].Path != "README.md" {
		t.Fatalf("unexpected legacy Skill assets: %+v", version.Assets)
	}
	if err := json.Unmarshal([]byte(`{"assets":{}}`), &version); err != nil || version.Assets == nil || version.Assets.Files == nil {
		t.Fatalf("decode empty Skill asset bundle: %+v err=%v", version.Assets, err)
	}
}
