package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestSkillCLIUsesTypedSDK(t *testing.T) {
	calls := 0
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/v2/skills" {
				t.Fatalf("unexpected Skill create %s %s", r.Method, r.URL.String())
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["identifier"] != "review" || body["workspace_id"] != "wksp/1" {
				t.Fatalf("unexpected Skill create body %#v", body)
			}
			return jsonResponse(`{"id":"skill/1","workspace_id":"wksp/1","identifier":"review","title":"Review","owner_type":"workspace","source_type":"inline","status":"active","created_by":"test","created_at":"2026-07-15T00:00:00Z"}`), nil
		case 2:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v2/skills/skill%2F1/versions" {
				t.Fatalf("unexpected Skill version create %s %s", r.Method, r.URL.EscapedPath())
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			manifest, _ := body["manifest"].(map[string]any)
			if body["content_text"] != "Review carefully" || manifest["system_role"] != "Reviewer" {
				t.Fatalf("unexpected Skill version body %#v", body)
			}
			return jsonResponse(`{"id":"version/1","skill_id":"skill/1","version":1,"content_format":"hybrid","manifest":{"system_role":"Reviewer"},"content_text":"Review carefully","checksum_sha256":"checksum","package_format":"tma.skill-package.v1","created_by":"test","created_at":"2026-07-15T00:00:00Z"}`), nil
		case 3:
			if r.Method != http.MethodPost || r.URL.Path != "/v2/skills/resolve-preview" {
				t.Fatalf("unexpected Skill resolve %s %s", r.Method, r.URL.String())
			}
			return jsonResponse(`{"config":{"enabled":[]},"skills":[],"estimated_tokens":0,"truncated":false}`), nil
		case 4:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v2/skills/skill%2F1/versions/1/package" {
				t.Fatalf("unexpected Skill package download %s %s", r.Method, r.URL.EscapedPath())
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/zip"}}, Body: io.NopCloser(strings.NewReader("package"))}, nil
		default:
			t.Fatalf("unexpected extra Skill request %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})

	captureStdout(t, func() {
		if err := commandSkill(client, []string{"create", "--workspace", "wksp/1", "--identifier", "review", "--title", "Review"}); err != nil {
			t.Fatal(err)
		}
		if err := commandSkill(client, []string{"version", "create", "--skill", "skill/1", "--content", "Review carefully", "--manifest", `{"system_role":"Reviewer"}`}); err != nil {
			t.Fatal(err)
		}
		if err := commandSkill(client, []string{"resolve", "--skills", `{"enabled":[]}`, "--max-tokens", "100"}); err != nil {
			t.Fatal(err)
		}
	})
	output := t.TempDir() + "/skill.zip"
	if err := commandSkill(client, []string{"version", "download", "--skill", "skill/1", "--version", "1", "--output", output}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "package" {
		t.Fatalf("downloaded package=%q err=%v", content, err)
	}
	if calls != 4 {
		t.Fatalf("Skill calls=%d", calls)
	}
}

func TestMarketplaceCLIUsesTypedSDK(t *testing.T) {
	calls := 0
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/v2/skills/marketplace/discover" || r.URL.Query().Get("repository") != "acme/review" || r.URL.Query().Get("session_id") != "session/1" {
				t.Fatalf("unexpected Marketplace discover %s %s", r.Method, r.URL.String())
			}
			return jsonResponse(`{"provider":"github","search_mode":"repository","items":[],"count":0}`), nil
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/v2/skills/marketplace/preview" {
				t.Fatalf("unexpected Marketplace preview %s %s", r.Method, r.URL.String())
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			source, _ := body["source"].(map[string]any)
			if source["repository"] != "acme/review" {
				t.Fatalf("unexpected Marketplace source %#v", source)
			}
			return jsonResponse(marketplaceCLIPreviewJSON), nil
		case 3:
			if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v2/skills/skill%2F1/enable" {
				t.Fatalf("unexpected Marketplace enable %s %s", r.Method, r.URL.EscapedPath())
			}
			return jsonResponse(`{"agent_id":"agent/1","previous_config_version":1,"new_config_version":2,"current_session_version":1,"binding":{"skill":"review","version":1},"changed":true,"requires_session_upgrade":true}`), nil
		case 4:
			if r.Method != http.MethodGet || r.URL.Path != "/v2/skills/marketplace/internal" || strings.Join(r.URL.Query()["tag"], ",") != "go,security" {
				t.Fatalf("unexpected internal Marketplace list %s %s", r.Method, r.URL.String())
			}
			return jsonResponse(`{"provider":"catalog","items":[],"count":0}`), nil
		case 5:
			if r.Method != http.MethodPost || r.URL.Path != "/v2/skill-marketplace-entries" {
				t.Fatalf("unexpected Marketplace entry create %s %s", r.Method, r.URL.String())
			}
			return jsonResponse(marketplaceCLIEntryJSON), nil
		case 6:
			if r.Method != http.MethodPost || r.URL.Path != "/v2/skill-marketplace-policies" {
				t.Fatalf("unexpected Marketplace policy create %s %s", r.Method, r.URL.String())
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			config, _ := body["config"].(map[string]any)
			if body["scope_type"] != "workspace" || config["require_commit_sha"] != true {
				t.Fatalf("unexpected Marketplace policy body %#v", body)
			}
			return jsonResponse(`{"policy":{"id":"policy/1","scope_type":"workspace","workspace_id":"wksp/1","status":"active","current_version":1,"created_by":"test","created_at":"2026-07-15T00:00:00Z"},"version":{"id":"pv/1","policy_id":"policy/1","version":1,"config":{"require_commit_sha":true},"checksum_sha256":"revision","created_by":"test","created_at":"2026-07-15T00:00:00Z"}}`), nil
		default:
			t.Fatalf("unexpected extra Marketplace request %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})

	captureStdout(t, func() {
		if err := commandMarketplace(client, []string{"discover", "--session", "session/1", "--repository", "acme/review"}); err != nil {
			t.Fatal(err)
		}
		if err := commandMarketplace(client, []string{"preview", "--session", "session/1", "--source", `{"provider":"github","repository":"acme/review"}`}); err != nil {
			t.Fatal(err)
		}
		if err := commandMarketplace(client, []string{"enable", "--skill", "skill/1", "--session", "session/1", "--version", "1", "--inputs", `{"style":"strict"}`}); err != nil {
			t.Fatal(err)
		}
		if err := commandMarketplace(client, []string{"internal", "list", "--session", "session/1", "--tags", "go,security"}); err != nil {
			t.Fatal(err)
		}
		if err := commandMarketplace(client, []string{"entry", "create", "--workspace", "wksp/1", "--skill", "skill/1", "--version", "1", "--tags", "go,review"}); err != nil {
			t.Fatal(err)
		}
		if err := commandMarketplace(client, []string{"policy", "create", "--scope", "workspace", "--workspace", "wksp/1", "--config", `{"require_commit_sha":true}`}); err != nil {
			t.Fatal(err)
		}
	})
	if calls != 6 {
		t.Fatalf("Marketplace calls=%d", calls)
	}
}

const marketplaceCLIPreviewJSON = `{"identifier":"review","source":{"provider":"github","repository":"acme/review"},"assets":{"files":[],"total_bytes":0},"policy":{"allowed":true,"checks":[]},"security":{"digest_sha256":"digest","attestation":{"status":"missing","digest_sha256":"digest","message":"missing"},"findings":[],"scanned_files":1,"binary_files":[],"sbom":{"format":"tma.skill.sbom.v1","package_digest_sha256":"digest","components":[]}},"install_state":"new_install","changes":{"content_changed":false,"added_files":[],"removed_files":[],"changed_files":[]}}`

const marketplaceCLIEntryJSON = `{"id":"entry/1","workspace_id":"wksp/1","skill_id":"skill/1","skill_version":1,"skill_identifier":"review","skill_title":"Review","skill_status":"active","version_checksum_sha256":"checksum","package_format":"tma.skill-package.v1","tags":[],"status":"draft","created_by":"test","created_at":"2026-07-15T00:00:00Z","updated_by":"test","updated_at":"2026-07-15T00:00:00Z"}`
