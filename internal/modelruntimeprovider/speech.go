package modelruntimeprovider

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	SpeechProtocolDoubaoASR = "doubao_realtime_asr"
	SpeechProtocolDoubaoTTS = "doubao_bidirectional_tts"
	SpeechMaxFrameBytes     = 1024 * 1024
)

type SpeechRoute struct {
	ProviderID    string `json:"provider_id"`
	ProviderType  string `json:"provider_type"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	Model         string `json:"model"`
	Protocol      string `json:"protocol"`
	ResourceID    string `json:"resource_id"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	DefaultVoice  string `json:"default_voice,omitempty"`
	SampleRateHz  int    `json:"sample_rate_hz"`
}

type SpeechStart struct {
	SessionID    string `json:"session_id"`
	Voice        string `json:"voice,omitempty"`
	Style        string `json:"style,omitempty"`
	AudioFormat  string `json:"audio_format,omitempty"`
	SampleRateHz int    `json:"sample_rate_hz,omitempty"`
}

type SpeechRequest struct {
	Type  string      `json:"type"`
	Route SpeechRoute `json:"route"`
	Start SpeechStart `json:"start"`
}

type SpeechEvent struct {
	Type              string `json:"type"`
	SessionID         string `json:"session_id,omitempty"`
	Mode              string `json:"mode,omitempty"`
	Text              string `json:"text,omitempty"`
	AudioFormat       string `json:"audio_format,omitempty"`
	SampleRateHz      int    `json:"sample_rate_hz,omitempty"`
	Code              string `json:"code,omitempty"`
	Message           string `json:"message,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	LimitScope        string `json:"limit_scope,omitempty"`
}

type SpeechMetrics struct {
	InputItems       int64
	OutputItems      int64
	InputBytes       int64
	OutputBytes      int64
	InputCharacters  int64
	OutputCharacters int64
	Canceled         bool
	Completed        bool
	ErrorCode        string
}

type SpeechProviderDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

type SpeechProxy interface {
	ProxySpeech(context.Context, context.Context, *websocket.Conn, SpeechRequest) (SpeechMetrics, error)
}

func (LocalExecutor) ProxySpeech(ctx, clientContext context.Context, client *websocket.Conn, request SpeechRequest) (SpeechMetrics, error) {
	return ProxySpeechWithDialer(ctx, clientContext, client, request, defaultSpeechProviderDialer)
}

func ProxySpeechWithDialer(ctx, clientContext context.Context, client *websocket.Conn, request SpeechRequest, dialer SpeechProviderDialer) (SpeechMetrics, error) {
	metrics := SpeechMetrics{}
	if err := validateSpeechRequest(request); err != nil {
		metrics.ErrorCode = "invalid_speech_route"
		_ = writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: err.Error()})
		return metrics, err
	}
	if dialer == nil {
		dialer = defaultSpeechProviderDialer
	}
	upstream, response, err := dialer(ctx, request.Route.BaseURL, doubaoSpeechHeaders(request.Route.APIKey, request.Route.ResourceID))
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		metrics.ErrorCode = "speech_provider_connect_failed"
		_ = writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "speech provider connection failed", Retryable: true})
		return metrics, err
	}
	upstream.SetReadLimit(SpeechMaxFrameBytes)
	defer upstream.CloseNow()

	switch request.Route.Protocol {
	case SpeechProtocolDoubaoASR:
		err = proxyDoubaoASR(ctx, clientContext, client, upstream, request, &metrics)
	case SpeechProtocolDoubaoTTS:
		err = proxyDoubaoTTS(ctx, clientContext, client, upstream, request, &metrics)
	default:
		err = errors.New("unsupported speech provider protocol")
	}
	if err != nil && ctx.Err() == nil && metrics.ErrorCode == "" && !isExpectedSpeechClose(err) {
		metrics.ErrorCode = "speech_session_failed"
		_ = writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "speech session failed", Retryable: true})
	}
	return metrics, err
}

func validateSpeechRequest(request SpeechRequest) error {
	if request.Type != "session.open" || strings.TrimSpace(request.Start.SessionID) == "" {
		return errors.New("model runtime speech request requires session.open and session_id")
	}
	if strings.TrimSpace(request.Route.ProviderID) == "" || strings.TrimSpace(request.Route.Model) == "" {
		return errors.New("model runtime speech route requires provider_id and model")
	}
	if strings.TrimSpace(request.Route.APIKey) == "" || strings.TrimSpace(request.Route.ResourceID) == "" {
		return errors.New("model runtime speech route credential and resource_id are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(request.Route.BaseURL))
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" {
		return errors.New("model runtime speech route requires an absolute wss URL")
	}
	if request.Route.Protocol != SpeechProtocolDoubaoASR && request.Route.Protocol != SpeechProtocolDoubaoTTS {
		return errors.New("model runtime speech route protocol is unsupported")
	}
	return nil
}

func defaultSpeechProviderDialer(ctx context.Context, target string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, target, &websocket.DialOptions{HTTPHeader: headers})
}

func serveSpeechRuntime(w http.ResponseWriter, r *http.Request, proxy SpeechProxy, metrics *RuntimeMetrics) {
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	metrics.streamStarted(streamProtocolWebSocket)
	defer metrics.streamFinished(streamProtocolWebSocket)
	connection.SetReadLimit(SpeechMaxFrameBytes)
	defer connection.CloseNow()
	ctx := withRuntimeMetrics(r.Context(), metrics)
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		return
	}
	var request SpeechRequest
	if messageType != websocket.MessageText || json.Unmarshal(payload, &request) != nil || request.Type != "session.open" {
		_ = writeSpeechEvent(ctx, connection, SpeechEvent{Type: "error", Code: "invalid_session_start", Message: "first internal event must be session.open"})
		return
	}
	_, _ = proxy.ProxySpeech(ctx, ctx, connection, request)
	_ = connection.Close(websocket.StatusNormalClosure, "speech session complete")
}

func (e *HTTPExecutor) ProxySpeech(ctx, clientContext context.Context, client *websocket.Conn, request SpeechRequest) (SpeechMetrics, error) {
	metrics := SpeechMetrics{}
	target, err := websocketEndpoint(e.endpoint, "/internal/v1/speech/realtime")
	if err != nil {
		metrics.ErrorCode = "speech_provider_connect_failed"
		_ = writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "speech provider connection failed", Retryable: true})
		return metrics, err
	}
	headers := make(http.Header)
	authorization, err := e.auth.authorization(http.MethodGet, "/internal/v1/speech/realtime")
	if err != nil {
		metrics.ErrorCode = "speech_provider_connect_failed"
		_ = writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "speech provider connection failed", Retryable: true})
		return metrics, err
	}
	headers.Set("Authorization", authorization)
	websocketClient := *e.client
	websocketClient.Timeout = 0
	upstream, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: &websocketClient, HTTPHeader: headers})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		metrics.ErrorCode = "speech_provider_connect_failed"
		_ = writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "speech provider connection failed", Retryable: true})
		return metrics, err
	}
	upstream.SetReadLimit(SpeechMaxFrameBytes)
	defer upstream.CloseNow()
	open, err := json.Marshal(request)
	if err != nil {
		return metrics, err
	}
	if err := upstream.Write(ctx, websocket.MessageText, open); err != nil {
		return metrics, err
	}
	return proxyGenericSpeech(ctx, clientContext, client, upstream, &metrics)
}

func websocketEndpoint(endpoint, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/") + path)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", errors.New("model runtime endpoint does not support WebSocket")
	}
	return parsed.String(), nil
}

func proxyGenericSpeech(ctx, clientContext context.Context, client, upstream *websocket.Conn, metrics *SpeechMetrics) (SpeechMetrics, error) {
	err := proxyWebSocketLoop(ctx, clientContext, client, upstream, func(messageType websocket.MessageType, payload []byte) (bool, error) {
		observeSpeechInput(messageType, payload, metrics)
		return false, upstream.Write(ctx, messageType, payload)
	}, func(messageType websocket.MessageType, payload []byte) (bool, error) {
		done := observeSpeechOutput(messageType, payload, metrics)
		return done, client.Write(clientContext, messageType, payload)
	})
	return *metrics, err
}

func observeSpeechInput(messageType websocket.MessageType, payload []byte, metrics *SpeechMetrics) {
	if messageType == websocket.MessageBinary {
		metrics.InputItems++
		metrics.InputBytes += int64(len(payload))
		return
	}
	var event struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	switch event.Type {
	case "text.append":
		if text := strings.TrimSpace(event.Text); text != "" {
			metrics.InputItems++
			metrics.InputCharacters += int64(utf8.RuneCountInString(text))
		}
	case "session.cancel":
		metrics.Canceled = true
	}
}

func observeSpeechOutput(messageType websocket.MessageType, payload []byte, metrics *SpeechMetrics) bool {
	if messageType == websocket.MessageBinary {
		metrics.OutputItems++
		metrics.OutputBytes += int64(len(payload))
		return false
	}
	var event SpeechEvent
	if json.Unmarshal(payload, &event) != nil {
		return false
	}
	switch event.Type {
	case "transcript.final":
		metrics.OutputItems++
		metrics.OutputCharacters += int64(utf8.RuneCountInString(strings.TrimSpace(event.Text)))
		metrics.Completed = true
		return true
	case "audio.done":
		metrics.Completed = true
		return true
	case "session.canceled":
		metrics.Canceled = true
		return true
	case "error":
		metrics.ErrorCode = strings.TrimSpace(event.Code)
		return true
	default:
		return false
	}
}

type speechClientEvent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type websocketInbound struct {
	messageType websocket.MessageType
	payload     []byte
	err         error
}

func proxyDoubaoASR(ctx, clientContext context.Context, client, upstream *websocket.Conn, request SpeechRequest, metrics *SpeechMetrics) error {
	sampleRate := request.Route.SampleRateHz
	if request.Start.SampleRateHz != 0 {
		sampleRate = request.Start.SampleRateHz
	}
	payload, _ := json.Marshal(map[string]any{
		"user":    map[string]any{"uid": request.Start.SessionID},
		"audio":   map[string]any{"format": "pcm", "rate": sampleRate, "bits": 16, "channel": 1},
		"request": map[string]any{"model_name": "bigmodel", "enable_itn": true, "enable_punc": true, "show_utterances": true, "result_type": "single"},
	})
	frame, _ := buildDoubaoASRStart(payload)
	if err := upstream.Write(ctx, websocket.MessageBinary, frame); err != nil {
		return err
	}
	if err := writeSpeechEvent(clientContext, client, SpeechEvent{Type: "session.started", SessionID: request.Start.SessionID, Mode: "transcription", AudioFormat: "pcm_s16le", SampleRateHz: sampleRate}); err != nil {
		return err
	}
	return proxyWebSocketLoop(ctx, clientContext, client, upstream, func(messageType websocket.MessageType, payload []byte) (bool, error) {
		if messageType == websocket.MessageBinary {
			metrics.InputItems++
			metrics.InputBytes += int64(len(payload))
			frame, _ := buildDoubaoASRAudio(payload, false)
			return false, upstream.Write(ctx, websocket.MessageBinary, frame)
		}
		var event speechClientEvent
		if json.Unmarshal(payload, &event) != nil {
			return false, nil
		}
		if event.Type == "audio.commit" {
			frame, _ := buildDoubaoASRAudio(nil, true)
			return false, upstream.Write(ctx, websocket.MessageBinary, frame)
		}
		if event.Type == "session.cancel" {
			metrics.Canceled = true
			return true, writeSpeechEvent(clientContext, client, SpeechEvent{Type: "session.canceled", SessionID: request.Start.SessionID})
		}
		return false, nil
	}, func(messageType websocket.MessageType, payload []byte) (bool, error) {
		if messageType != websocket.MessageBinary {
			return false, nil
		}
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return true, err
		}
		if frame.MessageType == doubaoMessageError {
			metrics.ErrorCode = "speech_provider_error"
			return true, writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "transcription provider failed", Retryable: true})
		}
		var result struct {
			Result struct {
				Text string `json:"text"`
			} `json:"result"`
		}
		if json.Unmarshal(frame.Payload, &result) != nil || strings.TrimSpace(result.Result.Text) == "" {
			return false, nil
		}
		final := frame.Flags&0x03 == doubaoFlagLastNoSequence || frame.Flags&0x03 == doubaoFlagLastWithSequence
		typeName := "transcript.partial"
		if final {
			typeName = "transcript.final"
			metrics.OutputItems++
			metrics.OutputCharacters += int64(utf8.RuneCountInString(strings.TrimSpace(result.Result.Text)))
			metrics.Completed = true
		}
		return final, writeSpeechEvent(clientContext, client, SpeechEvent{Type: typeName, SessionID: request.Start.SessionID, Text: strings.TrimSpace(result.Result.Text)})
	})
}

func proxyDoubaoTTS(ctx, clientContext context.Context, client, upstream *websocket.Conn, request SpeechRequest, metrics *SpeechMetrics) error {
	connect, _ := buildDoubaoTTSConnectEvent(doubaoEventStartConnection, []byte(`{"namespace":"BidirectionalTTS"}`))
	if err := upstream.Write(ctx, websocket.MessageBinary, connect); err != nil {
		return err
	}
	if err := waitForDoubaoEvent(ctx, upstream, doubaoEventConnectionStarted); err != nil {
		return err
	}
	providerSessionID := newSpeechID("tts")
	voice := strings.TrimSpace(request.Start.Voice)
	if voice == "" {
		voice = request.Route.DefaultVoice
	}
	params := map[string]any{"speaker": voice, "audio_params": map[string]any{"format": "pcm", "sample_rate": request.Route.SampleRateHz}, "section_id": request.Start.SessionID}
	if strings.TrimSpace(request.Route.UpstreamModel) != "" {
		params["model"] = request.Route.UpstreamModel
	}
	if strings.TrimSpace(request.Start.Style) != "" {
		params["context_texts"] = []string{strings.TrimSpace(request.Start.Style)}
	}
	body, _ := json.Marshal(map[string]any{"user": map[string]any{"uid": request.Start.SessionID}, "event": doubaoEventStartSession, "req_params": params})
	startFrame, _ := buildDoubaoTTSSessionEvent(providerSessionID, doubaoEventStartSession, body)
	if err := upstream.Write(ctx, websocket.MessageBinary, startFrame); err != nil {
		return err
	}
	if err := waitForDoubaoEvent(ctx, upstream, doubaoEventSessionStarted); err != nil {
		return err
	}
	if err := writeSpeechEvent(clientContext, client, SpeechEvent{Type: "session.started", SessionID: request.Start.SessionID, Mode: "synthesis", AudioFormat: "pcm_s16le", SampleRateHz: request.Route.SampleRateHz}); err != nil {
		return err
	}
	return proxyWebSocketLoop(ctx, clientContext, client, upstream, func(messageType websocket.MessageType, payload []byte) (bool, error) {
		if messageType != websocket.MessageText {
			return false, nil
		}
		var event speechClientEvent
		if json.Unmarshal(payload, &event) != nil {
			return false, nil
		}
		var providerEvent int32
		var providerPayload any
		switch event.Type {
		case "text.append":
			if strings.TrimSpace(event.Text) == "" {
				return false, nil
			}
			metrics.InputItems++
			metrics.InputCharacters += int64(utf8.RuneCountInString(strings.TrimSpace(event.Text)))
			providerEvent = doubaoEventTaskRequest
			providerPayload = map[string]any{"user": map[string]any{"uid": request.Start.SessionID}, "event": doubaoEventTaskRequest, "req_params": map[string]any{"text": strings.TrimSpace(event.Text)}}
		case "text.commit":
			providerEvent, providerPayload = doubaoEventFinishSession, map[string]any{}
		case "session.cancel":
			metrics.Canceled = true
			providerEvent, providerPayload = doubaoEventCancelSession, map[string]any{}
		default:
			return false, nil
		}
		body, _ := json.Marshal(providerPayload)
		frame, _ := buildDoubaoTTSSessionEvent(providerSessionID, providerEvent, body)
		return false, upstream.Write(ctx, websocket.MessageBinary, frame)
	}, func(messageType websocket.MessageType, payload []byte) (bool, error) {
		if messageType != websocket.MessageBinary {
			return false, nil
		}
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return true, err
		}
		if frame.MessageType == doubaoMessageError || frame.Event == doubaoEventSessionFailed {
			metrics.ErrorCode = "speech_provider_error"
			return true, writeSpeechEvent(clientContext, client, SpeechEvent{Type: "error", Code: metrics.ErrorCode, Message: "synthesis provider failed", Retryable: true})
		}
		if (frame.MessageType == doubaoMessageAudioServer || frame.Event == doubaoEventTTSResponse) && len(frame.Payload) > 0 {
			metrics.OutputItems++
			metrics.OutputBytes += int64(len(frame.Payload))
			return false, client.Write(clientContext, websocket.MessageBinary, frame.Payload)
		}
		if frame.Event == doubaoEventSessionFinished {
			metrics.Completed = true
			return true, writeSpeechEvent(clientContext, client, SpeechEvent{Type: "audio.done", SessionID: request.Start.SessionID})
		}
		if frame.Event == doubaoEventSessionCanceled {
			metrics.Canceled = true
			return true, writeSpeechEvent(clientContext, client, SpeechEvent{Type: "session.canceled", SessionID: request.Start.SessionID})
		}
		return false, nil
	})
}

func proxyWebSocketLoop(ctx, clientContext context.Context, client, upstream *websocket.Conn, onClient, onUpstream func(websocket.MessageType, []byte) (bool, error)) error {
	readContext, cancelReads := context.WithCancel(ctx)
	defer cancelReads()
	clientFrames, upstreamFrames := make(chan websocketInbound, 1), make(chan websocketInbound, 1)
	go readWebSocketFrames(clientContext, client, clientFrames)
	go readWebSocketFrames(readContext, upstream, upstreamFrames)
	for {
		select {
		case <-readContext.Done():
			return readContext.Err()
		case frame := <-clientFrames:
			if frame.err != nil {
				return frame.err
			}
			startedAt := time.Now()
			done, err := onClient(frame.messageType, frame.payload)
			if metrics := runtimeMetricsFromContext(ctx); metrics != nil {
				metrics.observeStreamEvent(streamProtocolWebSocket, streamDirectionClientToRuntime, time.Since(startedAt))
			}
			if err != nil || done {
				return err
			}
		case frame := <-upstreamFrames:
			if frame.err != nil {
				return frame.err
			}
			startedAt := time.Now()
			done, err := onUpstream(frame.messageType, frame.payload)
			if metrics := runtimeMetricsFromContext(ctx); metrics != nil {
				metrics.observeStreamEvent(streamProtocolWebSocket, streamDirectionRuntimeToClient, time.Since(startedAt))
			}
			if err != nil || done {
				return err
			}
		}
	}
}

func readWebSocketFrames(ctx context.Context, connection *websocket.Conn, frames chan<- websocketInbound) {
	for {
		messageType, payload, err := connection.Read(ctx)
		select {
		case frames <- websocketInbound{messageType: messageType, payload: payload, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func waitForDoubaoEvent(ctx context.Context, connection *websocket.Conn, expected int32) error {
	handshake, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		messageType, payload, err := connection.Read(handshake)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return err
		}
		if frame.MessageType == doubaoMessageError || frame.Event == doubaoEventConnectionFailed || frame.Event == doubaoEventSessionFailed {
			return errors.New("speech provider handshake failed")
		}
		if frame.Event == expected {
			return nil
		}
	}
}

func writeSpeechEvent(ctx context.Context, connection *websocket.Conn, event SpeechEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func doubaoSpeechHeaders(apiKey, resourceID string) http.Header {
	headers := make(http.Header)
	headers.Set("X-Api-Key", apiKey)
	headers.Set("X-Api-Resource-Id", resourceID)
	headers.Set("X-Api-Connect-Id", newSpeechID("speech-connect"))
	return headers
}

func newSpeechID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-fallback"
	}
	return prefix + "-" + hex.EncodeToString(value)
}

func isExpectedSpeechClose(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway
}

const (
	doubaoMessageFullClient    byte = 0x1
	doubaoMessageAudioClient   byte = 0x2
	doubaoMessageFullServer    byte = 0x9
	doubaoMessageAudioServer   byte = 0xB
	doubaoMessageError         byte = 0xF
	doubaoFlagNone             byte = 0x0
	doubaoFlagPositiveSequence byte = 0x1
	doubaoFlagLastNoSequence   byte = 0x2
	doubaoFlagLastWithSequence byte = 0x3
	doubaoFlagWithEvent        byte = 0x4
	doubaoSerializationNone    byte = 0x0
	doubaoSerializationJSON    byte = 0x1
	doubaoCompressionGzip      byte = 0x1
	doubaoMaxDecodedPayload         = 10 * 1024 * 1024
)

const (
	doubaoEventStartConnection   int32 = 1
	doubaoEventConnectionStarted int32 = 50
	doubaoEventConnectionFailed  int32 = 51
	doubaoEventStartSession      int32 = 100
	doubaoEventCancelSession     int32 = 101
	doubaoEventFinishSession     int32 = 102
	doubaoEventTaskRequest       int32 = 200
	doubaoEventSessionStarted    int32 = 150
	doubaoEventSessionCanceled   int32 = 151
	doubaoEventSessionFinished   int32 = 152
	doubaoEventSessionFailed     int32 = 153
	doubaoEventTTSResponse       int32 = 352
)

type doubaoFrame struct {
	MessageType, Flags, Serialization, Compression byte
	Sequence                                       int32
	HasSequence                                    bool
	Event                                          int32
	HasEvent                                       bool
	EventID                                        string
	ErrorCode                                      uint32
	Payload                                        []byte
}

func buildDoubaoASRStart(payload []byte) ([]byte, error) {
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullClient, Serialization: doubaoSerializationJSON, Payload: payload})
}

func buildDoubaoASRAudio(payload []byte, last bool) ([]byte, error) {
	flags := doubaoFlagNone
	if last {
		flags = doubaoFlagLastNoSequence
	}
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageAudioClient, Flags: flags, Payload: payload})
}

func buildDoubaoTTSConnectEvent(event int32, payload []byte) ([]byte, error) {
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullClient, Flags: doubaoFlagWithEvent, Serialization: doubaoSerializationJSON, Event: event, HasEvent: true, Payload: payload})
}

func buildDoubaoTTSSessionEvent(sessionID string, event int32, payload []byte) ([]byte, error) {
	if sessionID == "" {
		return nil, errors.New("doubao TTS session id is required")
	}
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullClient, Flags: doubaoFlagWithEvent, Serialization: doubaoSerializationJSON, Event: event, HasEvent: true, EventID: sessionID, Payload: payload})
}

func buildDoubaoFrame(frame doubaoFrame) ([]byte, error) {
	flags := frame.Flags
	if frame.HasEvent {
		flags |= doubaoFlagWithEvent
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 24+len(frame.EventID)+len(frame.Payload)))
	buffer.Write([]byte{0x11, frame.MessageType<<4 | flags, frame.Serialization<<4 | frame.Compression, 0})
	if frame.HasSequence {
		_ = binary.Write(buffer, binary.BigEndian, frame.Sequence)
	}
	if frame.HasEvent {
		_ = binary.Write(buffer, binary.BigEndian, frame.Event)
		if doubaoEventCarriesID(frame.Event) {
			_ = binary.Write(buffer, binary.BigEndian, uint32(len(frame.EventID)))
			buffer.WriteString(frame.EventID)
		}
	}
	if frame.MessageType == doubaoMessageError {
		_ = binary.Write(buffer, binary.BigEndian, frame.ErrorCode)
	}
	payload := frame.Payload
	if frame.Compression == doubaoCompressionGzip && len(payload) > 0 {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(payload); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		payload = compressed.Bytes()
	}
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(payload)))
	buffer.Write(payload)
	return buffer.Bytes(), nil
}

func parseDoubaoFrame(data []byte) (doubaoFrame, error) {
	if len(data) < 8 {
		return doubaoFrame{}, errors.New("doubao frame too short")
	}
	headerBytes := int(data[0]&0x0F) * 4
	if headerBytes < 4 || len(data) < headerBytes {
		return doubaoFrame{}, errors.New("invalid doubao header")
	}
	frame := doubaoFrame{MessageType: data[1] >> 4, Flags: data[1] & 0x0F, Serialization: data[2] >> 4, Compression: data[2] & 0x0F}
	offset := headerBytes
	sequenceFlags := frame.Flags & 0x03
	if sequenceFlags == doubaoFlagPositiveSequence || sequenceFlags == doubaoFlagLastWithSequence {
		if len(data) < offset+4 {
			return doubaoFrame{}, errors.New("doubao frame missing sequence")
		}
		frame.HasSequence, frame.Sequence = true, int32(binary.BigEndian.Uint32(data[offset:offset+4]))
		offset += 4
	}
	if frame.Flags&doubaoFlagWithEvent != 0 {
		if len(data) < offset+4 {
			return doubaoFrame{}, errors.New("doubao frame missing event")
		}
		frame.HasEvent, frame.Event = true, int32(binary.BigEndian.Uint32(data[offset:offset+4]))
		offset += 4
		if doubaoEventCarriesID(frame.Event) {
			if len(data) < offset+4 {
				return doubaoFrame{}, errors.New("doubao frame missing event id")
			}
			length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
			if length < 0 || len(data) < offset+length {
				return doubaoFrame{}, errors.New("invalid doubao event id")
			}
			frame.EventID = string(data[offset : offset+length])
			offset += length
		}
	}
	if frame.MessageType == doubaoMessageError {
		if len(data) < offset+4 {
			return doubaoFrame{}, errors.New("doubao frame missing error code")
		}
		frame.ErrorCode = binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
	}
	if len(data) < offset+4 {
		return doubaoFrame{}, errors.New("doubao frame missing payload")
	}
	length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if length < 0 || len(data) < offset+length {
		return doubaoFrame{}, errors.New("invalid doubao payload length")
	}
	payload := data[offset : offset+length]
	if frame.Compression == doubaoCompressionGzip && len(payload) > 0 {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return doubaoFrame{}, err
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, doubaoMaxDecodedPayload+1))
		_ = reader.Close()
		if err != nil || len(decoded) > doubaoMaxDecodedPayload {
			return doubaoFrame{}, errors.New("decode doubao compressed payload")
		}
		payload = decoded
	}
	frame.Payload = append([]byte(nil), payload...)
	return frame, nil
}

func doubaoEventCarriesID(event int32) bool {
	return event == doubaoEventConnectionStarted || event == doubaoEventConnectionFailed || event >= doubaoEventStartSession
}
