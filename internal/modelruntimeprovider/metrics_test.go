package modelruntimeprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRuntimeMetricsExposeAuthenticationAndBackpressure(t *testing.T) {
	metrics := NewRuntimeMetrics(time.Millisecond)
	metrics.observeAuthentication(true)
	metrics.observeAuthentication(false)
	metrics.streamStarted(streamProtocolNDJSON)
	metrics.observeStreamEvent(streamProtocolNDJSON, streamDirectionRuntimeToClient, 2*time.Millisecond)
	metrics.streamFinished(streamProtocolNDJSON)
	metrics.streamStarted(streamProtocolWebSocket)
	metrics.observeStreamEvent(streamProtocolWebSocket, streamDirectionClientToRuntime, 500*time.Microsecond)
	metrics.observeStreamEvent(streamProtocolWebSocket, streamDirectionRuntimeToClient, 3*time.Millisecond)
	metrics.streamFinished(streamProtocolWebSocket)

	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		`tma_model_runtime_authentication_total{outcome="accepted"} 1`,
		`tma_model_runtime_authentication_total{outcome="rejected"} 1`,
		`tma_model_runtime_streams_active{protocol="ndjson"} 0`,
		`tma_model_runtime_stream_events_total{direction="client_to_runtime",protocol="websocket"} 1`,
		`tma_model_runtime_stream_backpressure_events_total{direction="runtime_to_client",protocol="ndjson"} 1`,
		`tma_model_runtime_stream_backpressure_events_total{direction="client_to_runtime",protocol="websocket"} 0`,
		`tma_model_runtime_stream_backpressure_events_total{direction="runtime_to_client",protocol="websocket"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
}

func TestHealthHandlerPublishesRuntimeMetrics(t *testing.T) {
	metrics := NewRuntimeMetrics(0)
	handler := NewHealthHandler(metrics)
	for path, expected := range map[string]string{
		"/healthz": `"status":"ok"`,
		"/readyz":  `"status":"ready"`,
		"/metrics": "tma_model_runtime_streams_active",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("%s returned status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}
