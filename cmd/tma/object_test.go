package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandObjectCreate(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/object-refs" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["bucket"] != "tma-artifacts" || body["object_key"] != "wksp/sesn/output.txt" {
			t.Fatalf("unexpected object request: %#v", body)
		}
		if body["size_bytes"].(float64) != 42 {
			t.Fatalf("unexpected size_bytes: %#v", body["size_bytes"])
		}
		metadata, ok := body["metadata"].(map[string]any)
		if !ok || metadata["source"] != "tool" {
			t.Fatalf("unexpected metadata: %#v", body["metadata"])
		}
		return jsonResponse(`{"id":"obj_000001"}`), nil
	})
	err := commandObject(client, []string{
		"create",
		"--bucket", "tma-artifacts",
		"--key", "wksp/sesn/output.txt",
		"--size", "42",
		"--metadata", `{"source":"tool"}`,
	})
	if err != nil {
		t.Fatalf("object create: %v", err)
	}
}

func TestCommandObjectGet(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/object-refs/obj_000001" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"id":"obj_000001"}`), nil
	})
	if err := commandObject(client, []string{"get", "--id", "obj_000001"}); err != nil {
		t.Fatalf("object get: %v", err)
	}
}

func TestCommandObjectDownload(t *testing.T) {
	content := "workspace content"
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/object-refs/obj_000001/download" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("session_id"); got != "sesn_000001" {
			t.Fatalf("unexpected session_id query %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader(content)),
		}, nil
	})

	outputPath := filepath.Join(t.TempDir(), "object.txt")
	if err := commandObject(client, []string{"download", "--id", "obj_000001", "--session", "sesn_000001", "--output", outputPath}); err != nil {
		t.Fatalf("object download: %v", err)
	}

	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read object output file: %v", err)
	}
	if string(written) != content {
		t.Fatalf("unexpected object output content: %q", string(written))
	}
}

func TestCommandObjectDelete(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/object-refs/obj_000001" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := commandObject(client, []string{"delete", "--id", "obj_000001"}); err != nil {
		t.Fatalf("object delete: %v", err)
	}
}

func TestCommandSessionArtifactCreate(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/sessions/sesn_000001/artifacts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["object_ref_id"] != "obj_000001" || body["turn_id"] != "turn_000001" || body["artifact_type"] != "file" {
			t.Fatalf("unexpected artifact request: %#v", body)
		}
		metadata, ok := body["metadata"].(map[string]any)
		if !ok || metadata["preview"] != "hello" {
			t.Fatalf("unexpected metadata: %#v", body["metadata"])
		}
		return jsonResponse(`{"id":"art_000001"}`), nil
	})
	err := commandSessionArtifact(client, []string{
		"create",
		"--session", "sesn_000001",
		"--object", "obj_000001",
		"--turn", "turn_000001",
		"--type", "file",
		"--metadata", `{"preview":"hello"}`,
	})
	if err != nil {
		t.Fatalf("session artifact create: %v", err)
	}
}

func TestCommandSessionArtifactList(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/sessions/sesn_000001/artifacts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"artifacts":[{"id":"art_000001","object_ref_id":"obj_000001","turn_id":"turn_000001","tool_call_id":"call_read","name":"read_file.json","description":"tool output","artifact_type":"asset"}]}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandSessionArtifact(client, []string{"list", "--session", "sesn_000001"}); err != nil {
			t.Fatalf("session artifact list: %v", err)
		}
	})
	for _, expected := range []string{
		"session artifacts: sesn_000001",
		"art_000001 read_file.json [asset]",
		"object: obj_000001",
		"turn: turn_000001 call: call_read",
		"download: /v1/sessions/sesn_000001/artifacts/art_000001/download",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, stdout)
		}
	}
}

func TestCommandSessionArtifactListJSON(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/sessions/sesn_000001/artifacts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"artifacts":[]}`), nil
	})

	stdout := captureStdout(t, func() {
		if err := commandSessionArtifact(client, []string{"list", "--session", "sesn_000001", "--json"}); err != nil {
			t.Fatalf("session artifact list json: %v", err)
		}
	})
	if !strings.Contains(stdout, `"artifacts": []`) {
		t.Fatalf("expected JSON output, got %q", stdout)
	}
}

func TestCommandSessionArtifactDownload(t *testing.T) {
	content := "hello download"
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/sessions/sesn_000001/artifacts/art_000001/download" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader(content)),
		}, nil
	})

	outputPath := filepath.Join(t.TempDir(), "artifact.txt")
	if err := commandSessionArtifact(client, []string{"download", "--session", "sesn_000001", "--artifact", "art_000001", "--output", outputPath}); err != nil {
		t.Fatalf("session artifact download: %v", err)
	}

	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(written) != content {
		t.Fatalf("unexpected output file content: %q", string(written))
	}
}

func TestCommandSessionArtifactDelete(t *testing.T) {
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/sessions/sesn_000001/artifacts/art_000001" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := commandSessionArtifact(client, []string{"delete", "--session", "sesn_000001", "--artifact", "art_000001"}); err != nil {
		t.Fatalf("session artifact delete: %v", err)
	}
}

func TestObjectCommandsUsage(t *testing.T) {
	stderr := captureStderr(t, func() {
		printUsage()
	})
	for _, expected := range []string{
		"object create --bucket BUCKET --key KEY",
		"object get --id OBJECT_REF_ID",
		"object download --id OBJECT_REF_ID",
		"object delete --id OBJECT_REF_ID",
		"session artifact create --session SESSION_ID --object OBJECT_REF_ID",
		"session artifact list --session SESSION_ID",
		"session artifact download --session SESSION_ID --artifact ARTIFACT_ID",
		"session artifact delete --session SESSION_ID --artifact ARTIFACT_ID",
	} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("expected usage to contain %q, got %q", expected, stderr)
		}
	}
}

func TestParseOptionalJSONObjectFlagRejectsArray(t *testing.T) {
	if _, err := parseOptionalJSONObjectFlag(`["not","object"]`, "metadata"); err == nil {
		t.Fatal("expected array metadata to be rejected")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writer

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = old
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return string(out)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newTestAPIClient(fn roundTripFunc) *apiClient {
	return &apiClient{
		baseURL: "http://tma.test",
		http: &http.Client{
			Transport: fn,
		},
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
