package modelruntimeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type speechProxyFunc func(context.Context, context.Context, *websocket.Conn, SpeechRequest) (SpeechMetrics, error)

func (f speechProxyFunc) ProxySpeech(ctx, clientContext context.Context, client *websocket.Conn, request SpeechRequest) (SpeechMetrics, error) {
	return f(ctx, clientContext, client, request)
}

func TestHTTPSpeechProxyRoundTripsASRAndRecordsMetrics(t *testing.T) {
	runtimeHandler, err := NewHandler(HandlerConfig{
		AuthToken: "runtime-secret",
		Speech: speechProxyFunc(func(ctx, clientContext context.Context, client *websocket.Conn, request SpeechRequest) (SpeechMetrics, error) {
			if request.Type != "session.open" || request.Route.ProviderID != "speech" || request.Route.APIKey != "provider-secret" || request.Route.ResourceID != "resource" || request.Start.SessionID != "session-1" {
				t.Fatalf("unexpected internal speech request: %+v", request)
			}
			if err := writeSpeechEvent(clientContext, client, SpeechEvent{Type: "session.started", SessionID: request.Start.SessionID, Mode: "transcription", AudioFormat: "pcm_s16le", SampleRateHz: 16000}); err != nil {
				return SpeechMetrics{}, err
			}
			messageType, audio, err := client.Read(ctx)
			if err != nil || messageType != websocket.MessageBinary || string(audio) != "pcm-data" {
				return SpeechMetrics{}, errors.New("runtime did not receive audio")
			}
			messageType, payload, err := client.Read(ctx)
			if err != nil || messageType != websocket.MessageText || !strings.Contains(string(payload), "audio.commit") {
				return SpeechMetrics{}, errors.New("runtime did not receive audio commit")
			}
			return SpeechMetrics{}, writeSpeechEvent(clientContext, client, SpeechEvent{Type: "transcript.final", SessionID: request.Start.SessionID, Text: "最终转写"})
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutor(runtimeServer.URL, "runtime-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan struct {
		metrics SpeechMetrics
		err     error
	}, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			result <- struct {
				metrics SpeechMetrics
				err     error
			}{err: acceptErr}
			return
		}
		defer client.CloseNow()
		metrics, proxyErr := executor.ProxySpeech(r.Context(), r.Context(), client, testSpeechRequest())
		result <- struct {
			metrics SpeechMetrics
			err     error
		}{metrics: metrics, err: proxyErr}
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	if event := readSpeechTestEvent(t, client); event.Type != "session.started" || event.Mode != "transcription" {
		t.Fatalf("unexpected started event: %+v", event)
	}
	if err := client.Write(t.Context(), websocket.MessageBinary, []byte("pcm-data")); err != nil {
		t.Fatal(err)
	}
	writeSpeechTestEvent(t, client, map[string]string{"type": "audio.commit"})
	if event := readSpeechTestEvent(t, client); event.Type != "transcript.final" || event.Text != "最终转写" {
		t.Fatalf("unexpected final event: %+v", event)
	}
	select {
	case completed := <-result:
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		if completed.metrics.InputItems != 1 || completed.metrics.InputBytes != int64(len("pcm-data")) || completed.metrics.OutputItems != 1 || completed.metrics.OutputCharacters != 4 || !completed.metrics.Completed {
			t.Fatalf("unexpected speech metrics: %+v", completed.metrics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("speech proxy did not complete")
	}
}

func TestHTTPSpeechProxyTreatsRuntimeAuthenticationAsProviderFailure(t *testing.T) {
	runtimeHandler, err := NewHandler(HandlerConfig{AuthToken: "expected-secret"})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutor(runtimeServer.URL, "wrong-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan SpeechMetrics, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			return
		}
		defer client.CloseNow()
		metrics, _ := executor.ProxySpeech(r.Context(), r.Context(), client, testSpeechRequest())
		result <- metrics
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	event := readSpeechTestEvent(t, client)
	if event.Type != "error" || event.Code != "speech_provider_connect_failed" || !event.Retryable {
		t.Fatalf("unexpected runtime authentication event: %+v", event)
	}
	select {
	case metrics := <-result:
		if metrics.ErrorCode != "speech_provider_connect_failed" {
			t.Fatalf("unexpected authentication metrics: %+v", metrics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authentication failure did not complete")
	}
}

func TestHTTPSpeechProxyCancellationReachesRuntime(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	runtimeHandler, err := NewHandler(HandlerConfig{
		AuthToken: "runtime-secret",
		Speech: speechProxyFunc(func(ctx, _ context.Context, client *websocket.Conn, _ SpeechRequest) (SpeechMetrics, error) {
			close(started)
			_, _, readErr := client.Read(ctx)
			close(canceled)
			return SpeechMetrics{}, readErr
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	executor, err := NewHTTPExecutor(runtimeServer.URL, "runtime-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			result <- acceptErr
			return
		}
		defer client.CloseNow()
		_, proxyErr := executor.ProxySpeech(ctx, r.Context(), client, testSpeechRequest())
		result <- proxyErr
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime speech request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("speech cancellation did not reach runtime")
	}
	select {
	case proxyErr := <-result:
		if !errors.Is(proxyErr, context.Canceled) {
			t.Fatalf("ProxySpeech() error = %v, want context canceled", proxyErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled speech proxy did not return")
	}
}

func testSpeechRequest() SpeechRequest {
	return SpeechRequest{
		Type: "session.open",
		Route: SpeechRoute{
			ProviderID: "speech", ProviderType: "doubao", BaseURL: "wss://speech.example/asr",
			APIKey: "provider-secret", Model: "asr", Protocol: SpeechProtocolDoubaoASR,
			ResourceID: "resource", SampleRateHz: 16000,
		},
		Start: SpeechStart{SessionID: "session-1", AudioFormat: "pcm_s16le", SampleRateHz: 16000},
	}
}

func writeSpeechTestEvent(t *testing.T, connection *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(t.Context(), websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readSpeechTestEvent(t *testing.T, connection *websocket.Conn) SpeechEvent {
	t.Helper()
	messageType, payload, err := connection.Read(t.Context())
	if err != nil || messageType != websocket.MessageText {
		t.Fatalf("read speech event: type=%v err=%v", messageType, err)
	}
	var event SpeechEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return event
}
