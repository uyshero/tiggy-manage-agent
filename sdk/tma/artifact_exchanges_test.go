package tma

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactExchangesServiceCreateTransferAndGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/artifact-exchanges/imports":
			if body := readAll(t, r.Body); !strings.Contains(body, `"session_id":"sesn_1"`) {
				t.Fatalf("unexpected create body %s", body)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"exchange":{"id":"aex_1","workspace_id":"wksp_1","owner_id":"app","direction":"import","status":"pending","filename":"report.txt","artifact_type":"file","visibility":"session","max_size_bytes":11,"expires_at":"2026-08-01T00:00:00Z","created_by":"app","created_at":"2026-07-31T00:00:00Z","updated_at":"2026-07-31T00:00:00Z"},"content_url":"/v2/artifact-exchanges/aex_1/content?workspace_id=wksp_1&token=secret"}`)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/artifact-exchanges/aex_1/content":
			if r.URL.Query().Get("token") != "secret" || r.Header.Get("Content-Type") != "text/plain" || r.ContentLength != 11 || readAll(t, r.Body) != "report body" {
				t.Fatalf("unexpected exchange upload: query=%s content_type=%q length=%d", r.URL.RawQuery, r.Header.Get("Content-Type"), r.ContentLength)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"exchange":{"id":"aex_1","status":"completed"},"object_ref":{"id":"obj_1"},"artifact":{"id":"art_1"}}`)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v2/artifact-exchanges/aex%2F1":
			fmt.Fprint(w, `{"id":"aex/1","workspace_id":"wksp_1","owner_id":"app","direction":"import","status":"completed","filename":"report.txt","artifact_type":"file","visibility":"session","max_size_bytes":11,"expires_at":"2026-08-01T00:00:00Z","created_by":"app","created_at":"2026-07-31T00:00:00Z","updated_at":"2026-07-31T00:00:00Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/artifact-exchanges/aex_2/content":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "download body")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := client.ArtifactExchanges.CreateImport(t.Context(), CreateArtifactImportExchangeRequest{
		SessionID: "sesn_1", Filename: "report.txt", ContentType: "text/plain",
	})
	if err != nil || grant.Exchange.ID != "aex_1" {
		t.Fatalf("create import exchange: %+v err=%v", grant, err)
	}
	result, err := client.ArtifactExchanges.Upload(t.Context(), grant, "text/plain", 11, strings.NewReader("report body"))
	if err != nil || result.Artifact.ID != "art_1" || result.ObjectRef.ID != "obj_1" {
		t.Fatalf("upload exchange: %+v err=%v", result, err)
	}
	exchange, err := client.ArtifactExchanges.Get(t.Context(), "aex/1")
	if err != nil || exchange.ID != "aex/1" {
		t.Fatalf("get exchange: %+v err=%v", exchange, err)
	}
	var output bytes.Buffer
	if err := client.ArtifactExchanges.Download(t.Context(), ArtifactExchangeGrant{ContentURL: "/v2/artifact-exchanges/aex_2/content?workspace_id=wksp_1&token=download"}, &output); err != nil || output.String() != "download body" {
		t.Fatalf("download exchange: %q err=%v", output.String(), err)
	}
}

func readArtifactExchangeBody(t *testing.T, reader io.Reader) string {
	t.Helper()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
