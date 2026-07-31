package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

type testSpeechProxyFunc func(context.Context, context.Context, *websocket.Conn, modelruntime.SpeechRequest) (modelruntime.SpeechMetrics, error)

func (f testSpeechProxyFunc) ProxySpeech(ctx, clientContext context.Context, client *websocket.Conn, request modelruntime.SpeechRequest) (modelruntime.SpeechMetrics, error) {
	return f(ctx, clientContext, client, request)
}

func TestSpeechRealtimeTranslatesGenericASRProtocol(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		ctx := r.Context()
		_, startPayload, err := connection.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		start, err := parseDoubaoFrame(startPayload)
		if err != nil || start.MessageType != doubaoMessageFullClient || !strings.Contains(string(start.Payload), `"rate":16000`) {
			t.Errorf("unexpected ASR start frame: %+v err=%v", start, err)
			return
		}
		_, audioPayload, _ := connection.Read(ctx)
		audio, _ := parseDoubaoFrame(audioPayload)
		if string(audio.Payload) != "pcm-data" || audio.MessageType != doubaoMessageAudioClient {
			t.Errorf("unexpected ASR audio frame: %+v", audio)
			return
		}
		_, commitPayload, _ := connection.Read(ctx)
		commit, _ := parseDoubaoFrame(commitPayload)
		if commit.Flags != doubaoFlagLastNoSequence {
			t.Errorf("unexpected ASR commit frame: %+v", commit)
			return
		}
		result, _ := json.Marshal(map[string]any{"code": 0, "result": map[string]any{"text": "这是最终转写"}})
		final, _ := buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullServer, Flags: doubaoFlagLastWithSequence, HasSequence: true, Sequence: 1, Serialization: doubaoSerializationJSON, Payload: result})
		_ = connection.Write(ctx, websocket.MessageBinary, final)
	}))
	defer upstream.Close()

	store := newTestStore()
	store.providers["speech-asr"] = managedagents.LLMProvider{ID: "speech-asr", ProviderType: "doubao", BaseURL: "wss://speech.example/asr", APIKeyEnv: "TEST_SPEECH_KEY", Enabled: true}
	store.models[llmModelKey("speech-asr", "seed-asr")] = managedagents.LLMModel{
		ProviderID: "speech-asr", Model: "seed-asr", CapabilityType: managedagents.LLMModelCapabilitySpeechToText,
		Capabilities: managedagents.LLMModelCapabilities{Protocol: speechProtocolDoubaoASR, ResourceID: "asr-resource", AudioFormat: "pcm_s16le", SampleRateHz: 16000},
	}
	t.Setenv("TEST_SPEECH_KEY", "secret")
	server := &Server{store: store, logger: slog.Default()}
	server.speechDialer = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		if headers.Get("X-Api-Key") != "secret" || headers.Get("X-Api-Resource-Id") != "asr-resource" {
			t.Fatalf("unexpected speech provider headers: %v", headers)
		}
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/speech/realtime", server.serveSpeechRealtime)
	platform := httptest.NewServer(mux)
	defer platform.Close()

	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http")+"/v2/speech/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeTestSpeechJSON(t, connection, map[string]any{"type": "session.start", "provider_id": "speech-asr", "model": "seed-asr", "session_id": "bio-1"})
	started := readTestSpeechEvent(t, connection)
	if started.Type != "session.started" || started.Mode != "transcription" || started.SampleRateHz != 16000 {
		t.Fatalf("unexpected started event: %+v", started)
	}
	if err := connection.Write(t.Context(), websocket.MessageBinary, []byte("pcm-data")); err != nil {
		t.Fatal(err)
	}
	writeTestSpeechJSON(t, connection, map[string]any{"type": "audio.commit"})
	final := readTestSpeechEvent(t, connection)
	if final.Type != "transcript.final" || final.Text != "这是最终转写" {
		t.Fatalf("unexpected final transcript: %+v", final)
	}
	invocation := waitForSpeechInvocation(t, store)
	if invocation.Capability != managedagents.ModelInvocationCapabilitySpeechToText || invocation.Status != managedagents.ModelInvocationStatusCompleted ||
		invocation.InputBytes != int64(len("pcm-data")) || invocation.InputItems != 1 || invocation.OutputCharacters != 6 {
		t.Fatalf("unexpected ASR invocation: %+v", invocation)
	}
}

func TestSpeechRealtimeTranslatesGenericTTSProtocol(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		ctx := r.Context()
		_, connectPayload, err := connection.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		connect, err := parseDoubaoFrame(connectPayload)
		if err != nil || connect.Event != doubaoEventStartConnection {
			t.Errorf("unexpected TTS connection frame: %+v err=%v", connect, err)
			return
		}
		writeTestDoubaoFrame(t, ctx, connection, doubaoFrame{
			MessageType: doubaoMessageFullServer, Flags: doubaoFlagWithEvent,
			Serialization: doubaoSerializationJSON, HasEvent: true, Event: doubaoEventConnectionStarted,
			EventID: "connection-1", Payload: []byte(`{}`),
		})

		_, startPayload, err := connection.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		start, err := parseDoubaoFrame(startPayload)
		if err != nil || start.Event != doubaoEventStartSession || start.EventID == "" ||
			!strings.Contains(string(start.Payload), `"speaker":"voice-1"`) ||
			!strings.Contains(string(start.Payload), `"model":"tts-upstream"`) ||
			!strings.Contains(string(start.Payload), `"温和"`) {
			t.Errorf("unexpected TTS start frame: %+v err=%v", start, err)
			return
		}
		writeTestDoubaoFrame(t, ctx, connection, doubaoFrame{
			MessageType: doubaoMessageFullServer, Flags: doubaoFlagWithEvent,
			Serialization: doubaoSerializationJSON, HasEvent: true, Event: doubaoEventSessionStarted,
			EventID: start.EventID, Payload: []byte(`{}`),
		})

		_, appendPayload, err := connection.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		appendFrame, err := parseDoubaoFrame(appendPayload)
		if err != nil || appendFrame.Event != doubaoEventTaskRequest ||
			!strings.Contains(string(appendFrame.Payload), `"text":"请继续讲"`) {
			t.Errorf("unexpected TTS task frame: %+v err=%v", appendFrame, err)
			return
		}
		_, commitPayload, err := connection.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		commit, err := parseDoubaoFrame(commitPayload)
		if err != nil || commit.Event != doubaoEventFinishSession || commit.EventID != start.EventID {
			t.Errorf("unexpected TTS finish frame: %+v err=%v", commit, err)
			return
		}
		writeTestDoubaoFrame(t, ctx, connection, doubaoFrame{
			MessageType: doubaoMessageAudioServer, Flags: doubaoFlagWithEvent,
			Serialization: doubaoSerializationNone, HasEvent: true, Event: doubaoEventTTSResponse,
			EventID: start.EventID, Payload: []byte{1, 2, 3, 4},
		})
		writeTestDoubaoFrame(t, ctx, connection, doubaoFrame{
			MessageType: doubaoMessageFullServer, Flags: doubaoFlagWithEvent,
			Serialization: doubaoSerializationJSON, HasEvent: true, Event: doubaoEventSessionFinished,
			EventID: start.EventID, Payload: []byte(`{}`),
		})
	}))
	defer upstream.Close()

	store := newTestStore()
	store.providers["speech-tts"] = managedagents.LLMProvider{ID: "speech-tts", ProviderType: "doubao", BaseURL: "wss://speech.example/tts", APIKeyEnv: "TEST_SPEECH_KEY", Enabled: true}
	store.models[llmModelKey("speech-tts", "seed-tts")] = managedagents.LLMModel{
		ProviderID: "speech-tts", Model: "seed-tts", CapabilityType: managedagents.LLMModelCapabilityTextToSpeech,
		Capabilities: managedagents.LLMModelCapabilities{
			Protocol: speechProtocolDoubaoTTS, ResourceID: "tts-resource", DefaultVoice: "default-voice",
			AudioFormat: "pcm_s16le", SampleRateHz: 24000, UpstreamModel: "tts-upstream",
		},
	}
	t.Setenv("TEST_SPEECH_KEY", "secret")
	server := &Server{store: store, logger: slog.Default()}
	server.speechDialer = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		if headers.Get("X-Api-Key") != "secret" || headers.Get("X-Api-Resource-Id") != "tts-resource" {
			t.Fatalf("unexpected speech provider headers: %v", headers)
		}
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/speech/realtime", server.serveSpeechRealtime)
	platform := httptest.NewServer(mux)
	defer platform.Close()

	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http")+"/v2/speech/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeTestSpeechJSON(t, connection, map[string]any{
		"type": "session.start", "provider_id": "speech-tts", "model": "seed-tts",
		"session_id": "bio-1", "voice": "voice-1", "style": "温和",
	})
	started := readTestSpeechEvent(t, connection)
	if started.Type != "session.started" || started.Mode != "synthesis" || started.SampleRateHz != 24000 {
		t.Fatalf("unexpected started event: %+v", started)
	}
	writeTestSpeechJSON(t, connection, map[string]any{"type": "text.append", "text": "请继续讲"})
	writeTestSpeechJSON(t, connection, map[string]any{"type": "text.commit"})
	messageType, audio, err := connection.Read(t.Context())
	if err != nil || messageType != websocket.MessageBinary || string(audio) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("unexpected synthesized audio: type=%v audio=%v err=%v", messageType, audio, err)
	}
	done := readTestSpeechEvent(t, connection)
	if done.Type != "audio.done" || done.SessionID != "bio-1" {
		t.Fatalf("unexpected audio completion: %+v", done)
	}
	invocation := waitForSpeechInvocation(t, store)
	if invocation.Capability != managedagents.ModelInvocationCapabilityTextToSpeech || invocation.Status != managedagents.ModelInvocationStatusCompleted ||
		invocation.InputCharacters != 4 || invocation.OutputBytes != 4 || invocation.OutputItems != 1 {
		t.Fatalf("unexpected TTS invocation: %+v", invocation)
	}
}

func TestSpeechRealtimeUsesRemoteRuntimeAndKeepsInvocationAudit(t *testing.T) {
	runtimeHandler, err := modelruntime.NewHandler(modelruntime.HandlerConfig{
		AuthToken: "runtime-secret",
		Speech: testSpeechProxyFunc(func(ctx, clientContext context.Context, client *websocket.Conn, request modelruntime.SpeechRequest) (modelruntime.SpeechMetrics, error) {
			if request.Route.APIKey != "provider-secret" || request.Route.Protocol != speechProtocolDoubaoASR || request.Start.SessionID != "bio-remote" {
				t.Fatalf("unexpected remote speech request: %+v", request)
			}
			payload, _ := json.Marshal(speechServerEvent{Type: "session.started", SessionID: request.Start.SessionID, Mode: "transcription", AudioFormat: "pcm_s16le", SampleRateHz: 16000})
			if err := client.Write(clientContext, websocket.MessageText, payload); err != nil {
				return modelruntime.SpeechMetrics{}, err
			}
			messageType, audio, err := client.Read(ctx)
			if err != nil || messageType != websocket.MessageBinary || string(audio) != "remote-pcm" {
				return modelruntime.SpeechMetrics{}, errors.New("remote runtime did not receive audio")
			}
			if _, _, err := client.Read(ctx); err != nil {
				return modelruntime.SpeechMetrics{}, err
			}
			payload, _ = json.Marshal(speechServerEvent{Type: "transcript.final", SessionID: request.Start.SessionID, Text: "远程转写"})
			return modelruntime.SpeechMetrics{}, client.Write(clientContext, websocket.MessageText, payload)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	remoteRuntime, err := modelruntime.NewHTTPExecutor(runtimeServer.URL, "runtime-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore()
	store.providers["speech-remote"] = managedagents.LLMProvider{
		ID: "speech-remote", ProviderType: "doubao", BaseURL: "wss://speech.example/asr",
		APIKeyEnv: "TEST_REMOTE_SPEECH_KEY", Enabled: true,
	}
	store.models[llmModelKey("speech-remote", "asr")] = managedagents.LLMModel{
		ProviderID: "speech-remote", Model: "asr", CapabilityType: managedagents.LLMModelCapabilitySpeechToText,
		Capabilities: managedagents.LLMModelCapabilities{Protocol: speechProtocolDoubaoASR, ResourceID: "resource", AudioFormat: "pcm_s16le", SampleRateHz: 16000},
	}
	t.Setenv("TEST_REMOTE_SPEECH_KEY", "provider-secret")
	server := &Server{store: store, logger: slog.Default(), speechRuntime: remoteRuntime}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/speech/realtime", server.serveSpeechRealtime)
	platform := httptest.NewServer(mux)
	defer platform.Close()
	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http")+"/v2/speech/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeTestSpeechJSON(t, connection, map[string]any{"type": "session.start", "provider_id": "speech-remote", "model": "asr", "session_id": "bio-remote"})
	if started := readTestSpeechEvent(t, connection); started.Type != "session.started" || started.Mode != "transcription" {
		t.Fatalf("unexpected remote start: %+v", started)
	}
	if err := connection.Write(t.Context(), websocket.MessageBinary, []byte("remote-pcm")); err != nil {
		t.Fatal(err)
	}
	writeTestSpeechJSON(t, connection, map[string]any{"type": "audio.commit"})
	if final := readTestSpeechEvent(t, connection); final.Type != "transcript.final" || final.Text != "远程转写" {
		t.Fatalf("unexpected remote transcript: %+v", final)
	}
	invocation := waitForSpeechInvocation(t, store)
	if invocation.Status != managedagents.ModelInvocationStatusCompleted || invocation.InputBytes != int64(len("remote-pcm")) || invocation.OutputCharacters != 4 {
		t.Fatalf("remote speech invocation was not preserved: %+v", invocation)
	}
}

func TestSpeechRealtimeAuditsRemoteProviderErrorAsFailed(t *testing.T) {
	runtimeHandler, err := modelruntime.NewHandler(modelruntime.HandlerConfig{
		AuthToken: "runtime-secret",
		Speech: testSpeechProxyFunc(func(_ context.Context, clientContext context.Context, client *websocket.Conn, request modelruntime.SpeechRequest) (modelruntime.SpeechMetrics, error) {
			payload, _ := json.Marshal(speechServerEvent{Type: "error", SessionID: request.Start.SessionID, Code: "speech_provider_error", Message: "transcription provider failed", Retryable: true})
			return modelruntime.SpeechMetrics{}, client.Write(clientContext, websocket.MessageText, payload)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer := httptest.NewServer(runtimeHandler)
	defer runtimeServer.Close()
	remoteRuntime, err := modelruntime.NewHTTPExecutor(runtimeServer.URL, "runtime-secret", runtimeServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore()
	store.providers["speech-error"] = managedagents.LLMProvider{
		ID: "speech-error", ProviderType: "doubao", BaseURL: "wss://speech.example/asr",
		APIKeyEnv: "TEST_REMOTE_SPEECH_ERROR_KEY", Enabled: true,
	}
	store.models[llmModelKey("speech-error", "asr")] = managedagents.LLMModel{
		ProviderID: "speech-error", Model: "asr", CapabilityType: managedagents.LLMModelCapabilitySpeechToText,
		Capabilities: managedagents.LLMModelCapabilities{Protocol: speechProtocolDoubaoASR, ResourceID: "resource", SampleRateHz: 16000},
	}
	t.Setenv("TEST_REMOTE_SPEECH_ERROR_KEY", "provider-secret")
	server := &Server{store: store, logger: slog.Default(), speechRuntime: remoteRuntime}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/speech/realtime", server.serveSpeechRealtime)
	platform := httptest.NewServer(mux)
	defer platform.Close()
	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http")+"/v2/speech/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeTestSpeechJSON(t, connection, map[string]any{"type": "session.start", "provider_id": "speech-error", "model": "asr"})
	if event := readTestSpeechEvent(t, connection); event.Type != "error" || event.Code != "speech_provider_error" {
		t.Fatalf("unexpected remote provider error: %+v", event)
	}
	invocation := waitForSpeechInvocation(t, store)
	if invocation.Status != managedagents.ModelInvocationStatusFailed || invocation.ErrorCode != "speech_provider_error" {
		t.Fatalf("remote provider error was not audited as failed: %+v", invocation)
	}
}

func TestResolveSpeechRouteRejectsNonSpeechAndWrongAdapter(t *testing.T) {
	store := newTestStore()
	store.providers["speech"] = managedagents.LLMProvider{ID: "speech", BaseURL: "wss://speech.example", Enabled: true}
	store.models[llmModelKey("speech", "wrong")] = managedagents.LLMModel{
		ProviderID: "speech", Model: "wrong", CapabilityType: managedagents.LLMModelCapabilitySpeechToText,
		Capabilities: managedagents.LLMModelCapabilities{Protocol: "vendor_raw", ResourceID: "resource"},
	}
	server := &Server{store: store}
	if _, _, err := server.resolveSpeechRoute(speechClientEvent{ProviderID: "fake", Model: "fake-demo"}); err == nil {
		t.Fatal("expected text model to be rejected")
	}
	if _, _, err := server.resolveSpeechRoute(speechClientEvent{ProviderID: "speech", Model: "wrong"}); err == nil {
		t.Fatal("expected unsupported speech adapter to be rejected")
	}
}

func TestSpeechRealtimeRejectsQuotaBeforeProviderDial(t *testing.T) {
	store := newTestStore()
	store.providers["speech"] = managedagents.LLMProvider{ID: "speech", ProviderType: "doubao", BaseURL: "wss://speech.example/asr", Enabled: true}
	store.models[llmModelKey("speech", "asr")] = managedagents.LLMModel{
		ProviderID: "speech", Model: "asr", CapabilityType: managedagents.LLMModelCapabilitySpeechToText,
		Capabilities: managedagents.LLMModelCapabilities{Protocol: speechProtocolDoubaoASR, ResourceID: "resource", SampleRateHz: 16000},
	}
	admission := newModelRuntimeAdmission(ModelRuntimePolicy{SpeechIdentitySessionsPerMinute: 1, SpeechMaxSessionDuration: time.Minute})
	request := modelRuntimeAdmissionRequest{
		Family: modelRuntimeFamilySpeech, WorkspaceID: managedagents.DefaultWorkspaceID, PrincipalID: "local-development",
		Capability: managedagents.ModelInvocationCapabilitySpeechToText, ProviderID: "speech", Model: "asr",
	}
	if err := admission.reserveLocalQuota(request, time.Now()); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, logger: slog.Default(), modelRuntimeAdmission: admission}
	server.speechDialer = func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		t.Fatal("quota rejection must happen before provider dial")
		return nil, nil, nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/speech/realtime", server.serveSpeechRealtime)
	platform := httptest.NewServer(mux)
	defer platform.Close()

	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http")+"/v2/speech/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeTestSpeechJSON(t, connection, map[string]any{"type": "session.start", "provider_id": "speech", "model": "asr"})
	event := readTestSpeechEvent(t, connection)
	if event.Code != "speech_quota_exceeded" || event.LimitScope != "identity" || event.RetryAfterSeconds < 1 || !event.Retryable {
		t.Fatalf("unexpected speech quota event: %+v", event)
	}
	invocation := waitForSpeechInvocation(t, store)
	if invocation.Status != managedagents.ModelInvocationStatusFailed || invocation.ErrorCode != "speech_quota_exceeded" {
		t.Fatalf("speech quota rejection was not audited: %+v", invocation)
	}
}

func TestSpeechRealtimeTerminatesSessionAtConfiguredDuration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(r.Context())
		<-r.Context().Done()
	}))
	defer upstream.Close()

	store := newTestStore()
	store.providers["speech"] = managedagents.LLMProvider{ID: "speech", ProviderType: "doubao", BaseURL: "wss://speech.example/asr", APIKeyEnv: "TEST_SPEECH_TIMEOUT_KEY", Enabled: true}
	store.models[llmModelKey("speech", "asr")] = managedagents.LLMModel{
		ProviderID: "speech", Model: "asr", CapabilityType: managedagents.LLMModelCapabilitySpeechToText,
		Capabilities: managedagents.LLMModelCapabilities{Protocol: speechProtocolDoubaoASR, ResourceID: "resource", SampleRateHz: 16000},
	}
	t.Setenv("TEST_SPEECH_TIMEOUT_KEY", "secret")
	server := &Server{
		store: store, logger: slog.Default(),
		modelRuntimeAdmission: newModelRuntimeAdmission(ModelRuntimePolicy{SpeechMaxSessionDuration: 100 * time.Millisecond}),
	}
	server.speechDialer = func(ctx context.Context, _ string, _ http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/speech/realtime", server.serveSpeechRealtime)
	platform := httptest.NewServer(mux)
	defer platform.Close()

	connection, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(platform.URL, "http")+"/v2/speech/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeTestSpeechJSON(t, connection, map[string]any{"type": "session.start", "provider_id": "speech", "model": "asr"})
	if started := readTestSpeechEvent(t, connection); started.Type != "session.started" {
		t.Fatalf("unexpected session start: %+v", started)
	}
	if event := readTestSpeechEvent(t, connection); event.Code != "speech_session_duration_exceeded" || !event.Retryable {
		t.Fatalf("unexpected session timeout event: %+v", event)
	}
	invocation := waitForSpeechInvocation(t, store)
	if invocation.Status != managedagents.ModelInvocationStatusFailed || invocation.ErrorCode != "speech_session_duration_exceeded" {
		t.Fatalf("speech timeout was not audited: %+v", invocation)
	}
}

func writeTestSpeechJSON(t *testing.T, connection *websocket.Conn, value any) {
	t.Helper()
	payload, _ := json.Marshal(value)
	if err := connection.Write(t.Context(), websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readTestSpeechEvent(t *testing.T, connection *websocket.Conn) speechServerEvent {
	t.Helper()
	messageType, payload, err := connection.Read(t.Context())
	if err != nil || messageType != websocket.MessageText {
		t.Fatalf("read speech event: type=%v err=%v", messageType, err)
	}
	var event speechServerEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func writeTestDoubaoFrame(t *testing.T, ctx context.Context, connection *websocket.Conn, frame doubaoFrame) {
	t.Helper()
	payload, err := buildDoubaoFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Error(err)
	}
}

func waitForSpeechInvocation(t *testing.T, store *testStore) managedagents.ModelInvocation {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		if len(store.modelInvocations) > 0 {
			invocation := store.modelInvocations[len(store.modelInvocations)-1]
			store.mu.Unlock()
			return invocation
		}
		store.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for speech invocation record")
	return managedagents.ModelInvocation{}
}
