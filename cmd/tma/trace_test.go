package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandTraceShowPrintsTimeline(t *testing.T) {
	client := newTestAPIClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v2/sessions/sesn_1/trace" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		return jsonResponse(`{
			"session_id":"sesn_1",
			"turn_id":"turn_1",
			"status":"completed",
			"summary":"user: please read\ntool result: default_read_file success artifacts=1",
			"stats":{"duration_ms":120,"step_count":2,"span_count":2,"tool_calls":1},
			"graph":{"root_span_ids":["span_root"],"edges":[{"parent_span_id":"span_root","child_span_id":"span_tool"}],"critical_span_ids":["span_root","span_tool"],"critical_path_duration_ms":190,"max_depth":1},
			"spans":[
				{"span_id":"span_root","name":"tma.interaction","kind":"interaction","status":"completed","duration_ms":120,"self_duration_ms":50,"critical":true,"event_count":2},
				{"span_id":"span_tool","parent_span_id":"span_root","name":"tma.tool.default.read_file","kind":"tool","status":"ok","depth":1,"start_offset_ms":50,"duration_ms":70,"self_duration_ms":70,"critical":true,"event_count":1}
			],
			"steps":[
				{"seq":1,"type":"user.message","message":"please read"},
				{"seq":2,"type":"runtime.tool_result","identifier":"default","api_name":"read_file","outcome":"success","message":"Received tool result.","artifacts":[{"artifact_id":"art_000001","name":"read_file.json","artifact_type":"asset","download_path":"/v1/sessions/sesn_1/artifacts/art_000001/download"}]}
			]
		}`), nil
	})

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()

	if err := commandTrace(client, []string{"show", "--session", "sesn_1"}); err != nil {
		t.Fatalf("commandTrace(show): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "trace session=sesn_1 turn=turn_1 status=completed") {
		t.Fatalf("expected header, got %q", text)
	}
	if !strings.Contains(text, "stats: steps=2 spans=2 tools=1") ||
		!strings.Contains(text, "graph: roots=1 edges=1 max_depth=1 critical_path=190ms critical_spans=2") ||
		!strings.Contains(text, "critical path:") ||
		!strings.Contains(text, "span_tool tma.tool.default.read_file duration=70ms self=70ms") ||
		!strings.Contains(text, "* span_root tma.interaction kind=interaction status=completed duration=120ms self=50ms events=2") ||
		!strings.Contains(text, "  * span_tool tma.tool.default.read_file kind=tool status=ok duration=70ms self=70ms events=1") ||
		!strings.Contains(text, "timeline:") {
		t.Fatalf("expected span graph details, got %q", text)
	}
	if !strings.Contains(text, "runtime.tool_result default_read_file outcome=success") {
		t.Fatalf("expected tool result line, got %q", text)
	}
	if !strings.Contains(text, "art_000001 read_file.json [asset] download: /v1/sessions/sesn_1/artifacts/art_000001/download") {
		t.Fatalf("expected artifact detail, got %q", text)
	}
	if !strings.Contains(text, "cli: bin/tma session artifact download --session sesn_1 --artifact art_000001") {
		t.Fatalf("expected artifact cli hint, got %q", text)
	}
}

func TestCommandTraceExportPrintsRawJSON(t *testing.T) {
	clearOTLPEnv(t)
	client := newTestAPIClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v2/sessions/sesn_1/trace" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if request.URL.Query().Get("format") != "perfetto" || request.URL.Query().Get("turn_id") != "turn_1" {
			t.Fatalf("unexpected query: %s", request.URL.RawQuery)
		}
		return jsonResponse(`{"traceEvents":[{"name":"tma.interaction"}]}`), nil
	})

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()

	if err := commandTrace(client, []string{"export", "--session", "sesn_1", "--turn", "turn_1", "--format", "perfetto"}); err != nil {
		t.Fatalf("commandTrace(export): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(out), `"traceEvents"`) {
		t.Fatalf("expected raw export json, got %q", string(out))
	}
}

func TestCommandTraceExportWritesOutputFile(t *testing.T) {
	clearOTLPEnv(t)
	client := newTestAPIClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("format") != "otel" {
			t.Fatalf("unexpected query: %s", request.URL.RawQuery)
		}
		return jsonResponse(`{"resourceSpans":[{"scopeSpans":[]}]}`), nil
	})

	outputPath := filepath.Join(t.TempDir(), "trace.json")
	if err := commandTrace(client, []string{"export", "--session", "sesn_1", "--format", "otel", "--output", outputPath}); err != nil {
		t.Fatalf("commandTrace(export --output): %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(content), `"resourceSpans"`) {
		t.Fatalf("expected output file to contain export JSON, got %q", string(content))
	}
}

func TestCommandTraceExportPushesOTLPHTTP(t *testing.T) {
	clearOTLPEnv(t)
	var pushedBody string
	var sawAuth bool
	client := newTestAPIClient(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "tma.test":
			if request.URL.Query().Get("format") != "otel" {
				t.Fatalf("expected command to request otel export, got query: %s", request.URL.RawQuery)
			}
			return jsonResponse(`{"resourceSpans":[{"scopeSpans":[]}]}`), nil
		case "collector.test":
			if request.Method != http.MethodPost {
				t.Fatalf("expected OTLP POST, got %s", request.Method)
			}
			if request.URL.Path != "/v1/traces" {
				t.Fatalf("expected normalized OTLP traces path, got %s", request.URL.Path)
			}
			if request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("expected json content-type, got %q", request.Header.Get("Content-Type"))
			}
			sawAuth = request.Header.Get("Authorization") == "Bearer secret-token"
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read OTLP body: %v", err)
			}
			pushedBody = string(body)
			return jsonResponse(`{}`), nil
		default:
			t.Fatalf("unexpected host: %s", request.URL.Host)
			return nil, nil
		}
	})

	if err := commandTrace(client, []string{"export", "--session", "sesn_1", "--otlp-endpoint", "http://collector.test", "--otlp-token", "secret-token"}); err != nil {
		t.Fatalf("commandTrace(export --otlp-endpoint): %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected OTLP bearer auth header")
	}
	if !strings.Contains(pushedBody, `"resourceSpans"`) {
		t.Fatalf("expected pushed OTLP JSON body, got %q", pushedBody)
	}
}

func TestCommandTraceExportRejectsNonOTLPFormatPush(t *testing.T) {
	clearOTLPEnv(t)
	client := newTestAPIClient(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("request should not be sent: %s", request.URL.String())
		return nil, nil
	})

	err := commandTrace(client, []string{"export", "--session", "sesn_1", "--format", "perfetto", "--otlp-endpoint", "http://collector.test"})
	if err == nil || !strings.Contains(err.Error(), "requires --format otel") {
		t.Fatalf("expected format validation error, got %v", err)
	}
}

func clearOTLPEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TOKEN", "")
}
