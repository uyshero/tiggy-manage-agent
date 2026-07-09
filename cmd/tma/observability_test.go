package main

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestCommandObservabilityStatusPrintsJSON(t *testing.T) {
	client := newTestAPIClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/observability/status" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		return jsonResponse(`{"perfetto":{"enabled":true,"destination":"/tmp/tma-traces"},"otlp":{"enabled":true,"destination":"http://collector.test/v1/traces","token_provided":true}}`), nil
	})

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()

	if err := commandObservability(client, []string{"status"}); err != nil {
		t.Fatalf("commandObservability(status): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, `"perfetto"`) || !strings.Contains(text, `"otlp"`) || !strings.Contains(text, `"token_provided": true`) {
		t.Fatalf("expected observability status json, got %q", text)
	}
}
