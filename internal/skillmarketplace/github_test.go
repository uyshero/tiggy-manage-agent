package skillmarketplace

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGitHubClientDiscoverRepositoryAndFetch(t *testing.T) {
	content := "---\nname: review-helper\ndescription: Review changes carefully.\nlicense: MIT\ninputs_schema:\n  type: object\n  additionalProperties: false\n  properties:\n    style:\n      type: string\n      enum: [strict, balanced]\n  required: [style]\n---\n\nCheck behavior first."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/review-skill/contents/SKILL.md" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected authorization: %q", got)
		}
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q,"sha":"blob123","html_url":"https://github.com/acme/review-skill/blob/main/SKILL.md","path":"SKILL.md"}`, base64.StdEncoding.EncodeToString([]byte(content)))
	}))
	defer server.Close()

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL, Token: "secret"}
	result, err := client.Discover(context.Background(), DiscoverInput{Repository: "acme/review-skill"})
	if err != nil {
		t.Fatalf("discover repository: %v", err)
	}
	if result.Count != 1 || !result.Items[0].Verified || result.Items[0].SuggestedIdentifier != "review-helper" {
		t.Fatalf("unexpected discovery result: %#v", result)
	}

	pkg, err := client.Fetch(context.Background(), Source{Repository: "acme/review-skill"})
	if err != nil {
		t.Fatalf("fetch skill: %v", err)
	}
	if pkg.Name != "review-helper" || pkg.Description != "Review changes carefully." || pkg.License != "MIT" || pkg.Revision != "blob123" || pkg.Content != content {
		t.Fatalf("unexpected package: %#v", pkg)
	}
	if !strings.Contains(string(pkg.Manifest), `"inputs_schema"`) || !strings.Contains(string(pkg.Manifest), `"additionalProperties":false`) {
		t.Fatalf("expected front matter inputs_schema manifest, got %s", pkg.Manifest)
	}
}

func TestGitHubClientFallsBackToRepositorySearchWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/repositories" || !strings.Contains(r.URL.Query().Get("q"), "review") {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		fmt.Fprint(w, `{"items":[{"full_name":"acme/review-skill","description":"Review helper","html_url":"https://github.com/acme/review-skill","default_branch":"main","stargazers_count":42}]}`)
	}))
	defer server.Close()

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	result, err := client.Discover(context.Background(), DiscoverInput{Query: "review", Limit: 5})
	if err != nil {
		t.Fatalf("discover skills: %v", err)
	}
	if result.SearchMode != "repository" || result.Count != 1 || result.Items[0].Verified || result.Items[0].Repository != "acme/review-skill" {
		t.Fatalf("unexpected discovery result: %#v", result)
	}
}

func TestGitHubClientRejectsUnsafeSources(t *testing.T) {
	client := &GitHubClient{}
	for _, source := range []Source{
		{Repository: "https://github.com/acme/repo"},
		{Repository: "acme/repo", Path: "../SKILL.md"},
		{Repository: "acme/repo", Path: "README.md"},
		{Provider: "url", Repository: "acme/repo"},
	} {
		if _, err := client.Fetch(context.Background(), source); err == nil {
			t.Fatalf("expected source to be rejected: %#v", source)
		}
	}
}

func TestGitHubClientFetchesReferencedPackageFiles(t *testing.T) {
	files := map[string]string{
		"/repos/anthropics/skills/contents/skills/pdf/SKILL.md":         "---\nname: pdf\n---\n\n[Details](REFERENCE.md)\nRun `python scripts/check.py output.json`.\n[Missing](MISSING.md)\n[Image](diagram.png)\n[Outside](../outside.md)",
		"/repos/anthropics/skills/contents/skills/pdf/reference.md":     "Read FORMS.md for forms.",
		"/repos/anthropics/skills/contents/skills/pdf/forms.md":         "Form instructions.",
		"/repos/anthropics/skills/contents/skills/pdf/scripts/check.py": "print('check')\n",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/REFERENCE.md") || strings.HasSuffix(r.URL.Path, "/FORMS.md") {
			t.Fatalf("mixed-case references must be resolved through the directory listing: %s", r.URL.Path)
		}
		if r.URL.Path == "/repos/anthropics/skills/contents/skills/pdf" {
			fmt.Fprint(w, `[{"name":"reference.md","path":"skills/pdf/reference.md","type":"file"},{"name":"forms.md","path":"skills/pdf/forms.md","type":"file"}]`)
			return
		}
		content, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q,"sha":%q,"html_url":%q}`,
			base64.StdEncoding.EncodeToString([]byte(content)), "sha-"+pathBase(r.URL.Path), "https://github.com/anthropics/skills/blob/main/"+strings.TrimPrefix(r.URL.Path, "/repos/anthropics/skills/contents/"))
	}))
	defer server.Close()

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	pkg, err := client.Fetch(context.Background(), Source{
		Repository: "anthropics/skills", Ref: "main", Path: "skills/pdf/SKILL.md",
	})
	if err != nil {
		t.Fatalf("fetch package: %v", err)
	}
	if len(pkg.Files) != 3 || pkg.Files[0].Path != "forms.md" || pkg.Files[1].Path != "reference.md" || pkg.Files[2].Path != "scripts/check.py" {
		t.Fatalf("unexpected package files: %#v", pkg.Files)
	}
	if !pkg.Files[2].Executable || pkg.TotalAssetBytes == 0 {
		t.Fatalf("expected script metadata and package size: %#v", pkg)
	}
	if len(pkg.Warnings) != 3 {
		t.Fatalf("expected missing, binary, and out-of-package warnings, got %#v", pkg.Warnings)
	}
}

func TestGitHubClientFetchesControlledBinaryAssets(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 24)...)
	pdf := []byte("%PDF-1.4\n%%EOF")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var content []byte
		switch r.URL.Path {
		case "/repos/acme/visual-skill/contents/SKILL.md":
			content = []byte("# Visual Skill\n\n![Template](assets/template.png)\n\nOpen `theme-showcase.pdf`.")
		case "/repos/acme/visual-skill/contents/assets/template.png":
			content = png
		case "/repos/acme/visual-skill/contents/theme-showcase.pdf":
			content = pdf
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q,"sha":"sha-%s","html_url":"https://github.com/acme/visual-skill/blob/main/%s"}`,
			base64.StdEncoding.EncodeToString(content), pathBase(r.URL.Path), strings.TrimPrefix(r.URL.Path, "/repos/acme/visual-skill/contents/"))
	}))
	defer server.Close()

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	pkg, err := client.Fetch(context.Background(), Source{Repository: "acme/visual-skill", Ref: "main", Path: "SKILL.md"})
	if err != nil {
		t.Fatalf("fetch binary package: %v", err)
	}
	if len(pkg.Files) != 2 {
		t.Fatalf("expected markdown-link and inline-code binary assets, got %#v", pkg.Files)
	}
	file := pkg.Files[0]
	decoded, err := base64.StdEncoding.DecodeString(file.ContentBase64)
	if err != nil {
		t.Fatalf("decode binary asset: %v", err)
	}
	if file.Path != "assets/template.png" || !file.Binary || file.Content != "" || file.ContentType != "image/png" || file.ChecksumSHA256 == "" || string(decoded) != string(png) {
		t.Fatalf("unexpected binary asset: %#v", file)
	}
	pdfFile := pkg.Files[1]
	decodedPDF, err := base64.StdEncoding.DecodeString(pdfFile.ContentBase64)
	if err != nil {
		t.Fatalf("decode PDF asset: %v", err)
	}
	if pdfFile.Path != "theme-showcase.pdf" || !pdfFile.Binary || pdfFile.ContentType != "application/pdf" || string(decodedPDF) != string(pdf) {
		t.Fatalf("unexpected inline-code PDF asset: %#v", pdfFile)
	}
}

func TestGitHubClientRetriesTransientServerErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"message":"temporary failure"}`, http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer server.Close()

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	result, err := client.Discover(context.Background(), DiscoverInput{Query: "pdf", Limit: 5})
	if err != nil {
		t.Fatalf("discover after retry: %v", err)
	}
	if attempts.Load() != 3 || result.Count != 0 {
		t.Fatalf("expected success on third attempt, attempts=%d result=%#v", attempts.Load(), result)
	}
}

func TestGitHubClientDoesNotRetryNotFound(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client := &GitHubClient{HTTPClient: server.Client(), BaseURL: server.URL}
	var response map[string]any
	err := client.getJSON(context.Background(), "/missing", nil, &response)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected one attempt for 404, got %d", attempts.Load())
	}
}

func pathBase(value string) string {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	return parts[len(parts)-1]
}
