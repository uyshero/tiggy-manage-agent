package modelruntimeprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	openAIRealtimeOutputTrackID   = "assistant_audio"
	openAIRealtimeAudioSampleRate = 24000
	openAIRealtimeAudioChannels   = 1
	openAIRealtimeMaxMessageBytes = 2 * MultimodalMaxFrameBytes
	openAIRealtimeWriteTimeout    = 5 * time.Second
)

var errOpenAIRealtimeBackpressureTimeout = errors.New("OpenAI realtime provider write timed out")

type openAIRealtimeState struct {
	request          MultimodalRequest
	sessionID        string
	inputTracks      map[string]MultimodalTrack
	inputWindow      *MultimodalCreditWindow
	outputWindow     *MultimodalCreditWindow
	metrics          *MultimodalMetrics
	audioBuffered    bool
	outputSequence   uint64
	outputAudioBytes int64
	providerTimeout  time.Duration
	providerWriter   func(context.Context, *websocket.Conn, any) error
}

func proxyOpenAIRealtimeWithDialer(
	ctx context.Context,
	clientContext context.Context,
	client *websocket.Conn,
	request MultimodalRequest,
	dialer MultimodalProviderDialer,
) (MultimodalMetrics, error) {
	metrics := newMultimodalMetrics(request.Start.InputTracks)
	if strings.TrimSpace(request.Route.APIKey) == "" {
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "multimodal_provider_unconfigured", errors.New("OpenAI realtime credential is not configured"))
	}
	target, err := openAIRealtimeEndpoint(request.Route)
	if err != nil {
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "invalid_multimodal_route", err)
	}
	if dialer == nil {
		dialer = defaultMultimodalProviderDialer
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+strings.TrimSpace(request.Route.APIKey))
	upstream, response, err := dialer(ctx, target, headers, "")
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "multimodal_provider_connect_failed", errors.New("OpenAI realtime connection failed"))
	}
	upstream.SetReadLimit(openAIRealtimeMaxMessageBytes)
	defer upstream.CloseNow()

	handshakeContext, cancelHandshake := context.WithTimeout(ctx, 15*time.Second)
	defer cancelHandshake()
	sessionID, err := waitForOpenAISessionCreated(handshakeContext, upstream)
	if err != nil {
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "multimodal_provider_handshake_failed", err)
	}
	if err := writeOpenAIRealtimeEvent(handshakeContext, upstream, openAISessionUpdate(request.Start)); err != nil {
		return metrics, err
	}
	if err := waitForOpenAISessionUpdated(handshakeContext, upstream); err != nil {
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "multimodal_provider_handshake_failed", err)
	}

	started := openAISessionStarted(request, sessionID)
	if err := started.Validate(request.Start); err != nil {
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "multimodal_provider_capability_violation", err)
	}
	if err := validateMultimodalNegotiation(request.Route.Constraints, started); err != nil {
		return failOpenAIRealtimeHandshake(clientContext, client, metrics, "multimodal_provider_capability_violation", err)
	}
	inputWindow, err := NewMultimodalCreditWindow(started.InputFlowLimits, started.InitialInputCredit)
	if err != nil {
		return metrics, err
	}
	var outputWindow *MultimodalCreditWindow
	if started.OutputFlowLimits != nil && started.InitialOutputCredit != nil {
		outputWindow, err = NewMultimodalCreditWindow(*started.OutputFlowLimits, *started.InitialOutputCredit)
		if err != nil {
			return metrics, err
		}
	}
	metrics.setOutputTracks(started.OutputTracks)
	if err := writeMultimodalJSON(clientContext, client, started); err != nil {
		return metrics, err
	}

	state := &openAIRealtimeState{
		request: request, sessionID: started.SessionID, inputTracks: multimodalTrackMap(request.Start.InputTracks),
		inputWindow: inputWindow, outputWindow: outputWindow, metrics: &metrics,
		providerTimeout: openAIRealtimeProviderWriteTimeout(request.Start),
	}
	err = proxyOpenAIRealtimeLoop(ctx, clientContext, client, upstream, &metrics,
		func(messageType websocket.MessageType, payload []byte) (bool, error) {
			return state.processClientMessage(ctx, clientContext, client, upstream, messageType, payload)
		},
		func(messageType websocket.MessageType, payload []byte) (bool, error) {
			return state.processProviderMessage(clientContext, client, messageType, payload)
		},
	)
	return metrics, err
}

func validateOpenAIMultimodalRequest(request MultimodalRequest) error {
	parsed, _ := url.Parse(strings.TrimSpace(request.Route.BaseURL))
	for key := range parsed.Query() {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if strings.Contains(normalized, "key") || strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") ||
			strings.Contains(normalized, "credential") || normalized == "authorization" || normalized == "auth" {
			return errors.New("OpenAI realtime credentials must not be present in the provider URL")
		}
	}
	if strings.TrimSpace(request.Route.UpstreamModel) == "" && strings.TrimSpace(request.Route.Model) == "" {
		return errors.New("OpenAI realtime route requires an upstream model")
	}
	audioTracks := 0
	for _, track := range request.Start.InputTracks {
		switch track.Kind {
		case MultimodalMediaAudio:
			audioTracks++
			if normalizedContentType(track.ContentType) != "audio/pcm" || !strings.EqualFold(track.Codec, "pcm_s16le") ||
				track.SampleRateHz != openAIRealtimeAudioSampleRate || track.Channels != openAIRealtimeAudioChannels {
				return fmt.Errorf("OpenAI realtime audio track %q must be mono PCM16 at 24000 Hz", track.ID)
			}
		case MultimodalMediaImage:
			contentType, codec := normalizedContentType(track.ContentType), strings.ToLower(strings.TrimSpace(track.Codec))
			if (contentType != "image/jpeg" || codec != "jpeg") && (contentType != "image/png" || codec != "png") {
				return fmt.Errorf("OpenAI realtime image track %q must use JPEG or PNG", track.ID)
			}
		default:
			return fmt.Errorf("OpenAI realtime input track %q kind %q is unsupported", track.ID, track.Kind)
		}
	}
	if audioTracks > 1 {
		return errors.New("OpenAI realtime supports at most one input audio track")
	}
	for _, modality := range request.Start.OutputModalities {
		if modality != "text" && modality != MultimodalMediaAudio {
			return fmt.Errorf("OpenAI realtime output modality %q is unsupported", modality)
		}
	}
	for _, modality := range request.Route.Constraints.OutputModalities {
		if modality != "text" && modality != MultimodalMediaAudio {
			return fmt.Errorf("OpenAI realtime route output modality %q is unsupported", modality)
		}
	}
	for _, format := range request.Route.Constraints.InputFormats {
		if !openAIRuntimeInputFormat(format) {
			return errors.New("OpenAI realtime route contains an unsupported input format")
		}
	}
	for _, format := range request.Route.Constraints.OutputFormats {
		if format.Kind != MultimodalMediaAudio || normalizedContentType(format.ContentType) != "audio/pcm" || !strings.EqualFold(format.Codec, "pcm_s16le") {
			return errors.New("OpenAI realtime route contains an unsupported output format")
		}
	}
	return nil
}

func openAIRuntimeInputFormat(format MultimodalMediaFormat) bool {
	kind, contentType, codec := strings.ToLower(strings.TrimSpace(format.Kind)), normalizedContentType(format.ContentType), strings.ToLower(strings.TrimSpace(format.Codec))
	return (kind == MultimodalMediaAudio && contentType == "audio/pcm" && codec == "pcm_s16le") ||
		(kind == MultimodalMediaImage && contentType == "image/jpeg" && codec == "jpeg") ||
		(kind == MultimodalMediaImage && contentType == "image/png" && codec == "png")
}

func openAIRealtimeEndpoint(route MultimodalRoute) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(route.BaseURL))
	if err != nil {
		return "", err
	}
	model := strings.TrimSpace(route.UpstreamModel)
	if model == "" {
		model = strings.TrimSpace(route.Model)
	}
	query := parsed.Query()
	query.Set("model", model)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func openAISessionUpdate(start MultimodalSessionStart) map[string]any {
	outputModality := "text"
	if multimodalHasModality(start.OutputModalities, MultimodalMediaAudio) {
		outputModality = MultimodalMediaAudio
	}
	session := map[string]any{
		"type":              "realtime",
		"output_modalities": []string{outputModality},
	}
	audio := make(map[string]any)
	if multimodalHasTrackKind(start.InputTracks, MultimodalMediaAudio) {
		audio["input"] = map[string]any{
			"format":         map[string]any{"type": "audio/pcm", "rate": openAIRealtimeAudioSampleRate},
			"turn_detection": nil,
		}
	}
	if outputModality == MultimodalMediaAudio {
		audio["output"] = map[string]any{"format": map[string]any{"type": "audio/pcm"}}
	}
	if len(audio) > 0 {
		session["audio"] = audio
	}
	return map[string]any{"type": "session.update", "session": session}
}

func openAISessionStarted(request MultimodalRequest, sessionID string) MultimodalSessionStarted {
	if requestedSessionID := strings.TrimSpace(request.Start.SessionID); requestedSessionID != "" {
		sessionID = requestedSessionID
	}
	maxFrameBytes := request.Route.Constraints.MaxFrameBytes
	maxInFlightFrames := int64(4)
	maxInFlightBytes := maxFrameBytes * maxInFlightFrames
	if maxInFlightBytes > MultimodalMaxInFlightBytes {
		maxInFlightBytes = MultimodalMaxInFlightBytes
	}
	limits := MultimodalFlowLimits{
		MaxFrameBytes: maxFrameBytes, MaxInFlightBytes: maxInFlightBytes, MaxInFlightFrames: maxInFlightFrames,
	}
	started := MultimodalSessionStarted{
		Type: "session.started", ProtocolVersion: MultimodalRealtimeProtocolVersion, SessionID: sessionID,
		InputFlowLimits: limits, InitialInputCredit: MultimodalFlowCredit{Bytes: limits.MaxInFlightBytes, Frames: limits.MaxInFlightFrames},
		HeartbeatMS: 15000,
	}
	if multimodalHasModality(request.Start.OutputModalities, MultimodalMediaAudio) {
		started.OutputTracks = []MultimodalTrack{{
			ID: openAIRealtimeOutputTrackID, Kind: MultimodalMediaAudio, ContentType: "audio/pcm", Codec: "pcm_s16le",
			Delivery: MultimodalDeliveryReliable, SampleRateHz: openAIRealtimeAudioSampleRate, Channels: openAIRealtimeAudioChannels,
		}}
		started.OutputFlowLimits = request.Start.OutputFlowLimits
		started.InitialOutputCredit = request.Start.InitialOutputCredit
	}
	return started
}

func waitForOpenAISessionCreated(ctx context.Context, upstream *websocket.Conn) (string, error) {
	messageType, payload, err := upstream.Read(ctx)
	if err != nil {
		return "", errors.New("OpenAI realtime did not create a session")
	}
	var event struct {
		Type    string `json:"type"`
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if messageType != websocket.MessageText || json.Unmarshal(payload, &event) != nil || event.Type != "session.created" || strings.TrimSpace(event.Session.ID) == "" {
		return "", errors.New("OpenAI realtime returned an invalid session.created event")
	}
	return event.Session.ID, nil
}

func waitForOpenAISessionUpdated(ctx context.Context, upstream *websocket.Conn) error {
	for {
		messageType, payload, err := upstream.Read(ctx)
		if err != nil {
			return errors.New("OpenAI realtime did not confirm session configuration")
		}
		var event struct {
			Type string `json:"type"`
		}
		if messageType != websocket.MessageText || json.Unmarshal(payload, &event) != nil {
			return errors.New("OpenAI realtime returned an invalid handshake event")
		}
		switch event.Type {
		case "session.updated":
			return nil
		case "rate_limits.updated":
			continue
		case "error":
			return errors.New("OpenAI realtime rejected the session configuration")
		default:
			return fmt.Errorf("OpenAI realtime returned unexpected handshake event %q", event.Type)
		}
	}
}

func (state *openAIRealtimeState) processClientMessage(
	ctx context.Context,
	clientContext context.Context,
	client *websocket.Conn,
	upstream *websocket.Conn,
	messageType websocket.MessageType,
	payload []byte,
) (bool, error) {
	if messageType == websocket.MessageBinary {
		frame, err := DecodeMultimodalMediaFrame(payload)
		if err != nil {
			return failMultimodalSession(clientContext, client, state.metrics, "invalid_media_frame", err)
		}
		return state.sendInputFrame(ctx, clientContext, client, upstream, frame)
	}
	var event MultimodalEvent
	if messageType != websocket.MessageText || decodeMultimodalControl(payload, &event) != nil {
		return failMultimodalSession(clientContext, client, state.metrics, "invalid_control_event", errors.New("invalid multimodal client control event"))
	}
	switch event.Type {
	case "input.text.append":
		text := strings.TrimSpace(event.Text)
		if text == "" {
			return failMultimodalSession(clientContext, client, state.metrics, "invalid_control_event", errors.New("input.text.append requires text"))
		}
		providerEvent := openAIConversationInput("input_text", "text", text)
		if err := state.writeProviderEvent(ctx, upstream, providerEvent); err != nil {
			return state.failProviderWrite(clientContext, client, ctx, err)
		}
		state.metrics.InputItems++
		state.metrics.InputCharacters += int64(utf8.RuneCountInString(text))
	case "input.object_ref":
		if state.request.ResolveObjectRef == nil {
			return failMultimodalSession(clientContext, client, state.metrics, "unresolved_object_ref", errors.New("object ref input must be resolved and verified by tma-server"))
		}
		var input MultimodalObjectRefInput
		if decodeMultimodalControl(payload, &input) != nil {
			return failMultimodalSession(clientContext, client, state.metrics, "invalid_control_event", errors.New("invalid input.object_ref event"))
		}
		frame, err := state.request.ResolveObjectRef(ctx, input)
		if err != nil {
			return failMultimodalSession(clientContext, client, state.metrics, "object_ref_rejected", errors.New("multimodal object ref is unavailable"))
		}
		return state.sendInputFrame(ctx, clientContext, client, upstream, frame)
	case "input.commit":
		if state.audioBuffered {
			if err := state.writeProviderEvent(ctx, upstream, map[string]any{"type": "input_audio_buffer.commit"}); err != nil {
				return state.failProviderWrite(clientContext, client, ctx, err)
			}
			state.audioBuffered = false
		}
		if err := state.writeProviderEvent(ctx, upstream, map[string]any{"type": "response.create"}); err != nil {
			return state.failProviderWrite(clientContext, client, ctx, err)
		}
		return false, nil
	case "flow.credit":
		if state.outputWindow == nil {
			return failMultimodalSession(clientContext, client, state.metrics, "flow_control_violation", errors.New("session has no binary output flow window"))
		}
		var credit MultimodalFlowCredit
		if decodeMultimodalControl(payload, &credit) != nil || credit.Type != "flow.credit" {
			return failMultimodalSession(clientContext, client, state.metrics, "invalid_control_event", errors.New("invalid output flow.credit event"))
		}
		if err := state.outputWindow.Grant(credit); err != nil {
			return failMultimodalSession(clientContext, client, state.metrics, "flow_control_violation", err)
		}
	case "ping":
		return false, writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "pong"})
	case "session.cancel":
		cancelContext, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		_ = state.providerWrite(cancelContext, upstream, map[string]any{"type": "response.cancel"})
		cancel()
		state.metrics.Canceled = true
		return true, writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "session.canceled", SessionID: state.sessionID})
	default:
		return failMultimodalSession(clientContext, client, state.metrics, "unsupported_control_event", fmt.Errorf("unsupported client event %q", event.Type))
	}
	return false, nil
}

func (state *openAIRealtimeState) sendInputFrame(
	ctx context.Context,
	clientContext context.Context,
	client *websocket.Conn,
	upstream *websocket.Conn,
	frame MultimodalMediaFrame,
) (bool, error) {
	if err := validateMultimodalFrameTrack(frame, state.inputTracks); err != nil {
		return failMultimodalSession(clientContext, client, state.metrics, "invalid_media_track", err)
	}
	if err := state.inputWindow.ReserveFrame(frame); err != nil {
		return failMultimodalSession(clientContext, client, state.metrics, multimodalFlowErrorCode(err), err)
	}
	var providerEvent map[string]any
	switch frame.Kind {
	case MultimodalMediaAudio:
		providerEvent = map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(frame.Payload)}
		state.audioBuffered = true
	case MultimodalMediaImage:
		track := state.inputTracks[frame.TrackID]
		dataURL := "data:" + normalizedContentType(track.ContentType) + ";base64," + base64.StdEncoding.EncodeToString(frame.Payload)
		providerEvent = openAIConversationInput("input_image", "image_url", dataURL)
	default:
		return failMultimodalSession(clientContext, client, state.metrics, "invalid_media_track", errors.New("OpenAI realtime only accepts audio and image frames"))
	}
	if err := state.writeProviderEvent(ctx, upstream, providerEvent); err != nil {
		return state.failProviderWrite(clientContext, client, ctx, err)
	}
	state.metrics.observeInputFrame(frame)
	credit := MultimodalFlowCredit{
		Type: "flow.credit", Bytes: int64(len(frame.Payload)), Frames: 1,
		TrackID: frame.TrackID, AcknowledgedSequence: frame.Sequence,
	}
	if err := state.inputWindow.Grant(credit); err != nil {
		return failMultimodalSession(clientContext, client, state.metrics, "flow_control_violation", err)
	}
	return false, writeMultimodalJSON(clientContext, client, credit)
}

func (state *openAIRealtimeState) processProviderMessage(
	clientContext context.Context,
	client *websocket.Conn,
	messageType websocket.MessageType,
	payload []byte,
) (bool, error) {
	if messageType != websocket.MessageText {
		return failMultimodalSession(clientContext, client, state.metrics, "invalid_provider_event", errors.New("OpenAI realtime returned a non-JSON event"))
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &envelope) != nil || strings.TrimSpace(envelope.Type) == "" {
		return failMultimodalSession(clientContext, client, state.metrics, "invalid_provider_event", errors.New("OpenAI realtime returned invalid JSON"))
	}
	switch envelope.Type {
	case "response.output_text.delta", "response.text.delta", "response.output_audio_transcript.delta", "response.audio_transcript.delta":
		text := openAIRealtimeText(payload, "delta")
		if text == "" {
			return false, nil
		}
		state.metrics.OutputCharacters += int64(utf8.RuneCountInString(text))
		return false, writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "output.text.delta", Text: text})
	case "response.output_text.done", "response.text.done":
		return state.writeTextFinal(clientContext, client, openAIRealtimeText(payload, "text"))
	case "response.output_audio_transcript.done", "response.audio_transcript.done":
		return state.writeTextFinal(clientContext, client, openAIRealtimeText(payload, "transcript"))
	case "response.output_audio.delta", "response.audio.delta":
		return state.writeAudioDelta(clientContext, client, payload)
	case "response.done":
		return state.finishResponse(clientContext, client, payload)
	case "error":
		state.metrics.ErrorCode = "openai_realtime_provider_error"
		return true, writeMultimodalEvent(clientContext, client, MultimodalEvent{
			Type: "error", Code: state.metrics.ErrorCode, Message: "OpenAI realtime provider rejected the request",
		})
	case "rate_limits.updated":
		return false, state.writeRateLimitSignal(clientContext, client, payload)
	case "session.created", "session.updated", "conversation.created", "conversation.item.created",
		"input_audio_buffer.committed", "input_audio_buffer.cleared", "input_audio_buffer.speech_started", "input_audio_buffer.speech_stopped",
		"response.created", "response.output_item.added", "response.output_item.done", "response.content_part.added", "response.content_part.done",
		"response.output_audio.done", "response.audio.done", "conversation.item.input_audio_transcription.completed":
		return false, nil
	default:
		// Provider lifecycle events are intentionally not exposed through the
		// stable TMA protocol. Unknown additions remain forward compatible.
		return false, nil
	}
}

func (state *openAIRealtimeState) writeTextFinal(ctx context.Context, client *websocket.Conn, text string) (bool, error) {
	state.metrics.OutputItems++
	return false, writeMultimodalEvent(ctx, client, MultimodalEvent{Type: "output.text.final", Text: text})
}

func (state *openAIRealtimeState) writeAudioDelta(ctx context.Context, client *websocket.Conn, payload []byte) (bool, error) {
	if state.outputWindow == nil {
		return failMultimodalSession(ctx, client, state.metrics, "flow_control_violation", errors.New("OpenAI realtime sent audio without an output credit window"))
	}
	encoded := openAIRealtimeText(payload, "delta")
	audio, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(audio) == 0 {
		return failMultimodalSession(ctx, client, state.metrics, "invalid_provider_event", errors.New("OpenAI realtime returned invalid audio"))
	}
	state.outputSequence++
	frame := MultimodalMediaFrame{
		Kind: MultimodalMediaAudio, TrackID: openAIRealtimeOutputTrackID, Sequence: state.outputSequence,
		TimestampMicros: state.outputAudioBytes * 1_000_000 / (openAIRealtimeAudioSampleRate * openAIRealtimeAudioChannels * 2), Payload: audio,
	}
	if err := state.outputWindow.ReserveFrame(frame); err != nil {
		return failMultimodalSession(ctx, client, state.metrics, multimodalFlowErrorCode(err), err)
	}
	encodedFrame, err := EncodeMultimodalMediaFrame(frame)
	if err != nil {
		return failMultimodalSession(ctx, client, state.metrics, "invalid_media_frame", err)
	}
	state.outputAudioBytes += int64(len(audio))
	state.metrics.observeOutputFrame(frame)
	return false, client.Write(ctx, websocket.MessageBinary, encodedFrame)
}

func (state *openAIRealtimeState) finishResponse(ctx context.Context, client *websocket.Conn, payload []byte) (bool, error) {
	var event struct {
		Response struct {
			Status string `json:"status"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return failMultimodalSession(ctx, client, state.metrics, "invalid_provider_event", errors.New("OpenAI realtime returned an invalid response.done event"))
	}
	switch strings.ToLower(strings.TrimSpace(event.Response.Status)) {
	case "", "completed":
		state.metrics.Completed = true
		return true, writeMultimodalEvent(ctx, client, MultimodalEvent{Type: "session.completed", SessionID: state.sessionID})
	case "cancelled", "canceled":
		state.metrics.Canceled = true
		return true, writeMultimodalEvent(ctx, client, MultimodalEvent{Type: "session.canceled", SessionID: state.sessionID})
	default:
		state.metrics.ErrorCode = "openai_realtime_response_failed"
		return true, writeMultimodalEvent(ctx, client, MultimodalEvent{
			Type: "error", Code: state.metrics.ErrorCode, Message: "OpenAI realtime response did not complete",
		})
	}
}

func (state *openAIRealtimeState) writeRateLimitSignal(ctx context.Context, client *websocket.Conn, payload []byte) error {
	var event struct {
		RateLimits []struct {
			Remaining    float64 `json:"remaining"`
			ResetSeconds float64 `json:"reset_seconds"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return nil
	}
	retryAfter := 0
	limited := false
	for _, limit := range event.RateLimits {
		if limit.Remaining > 0 {
			continue
		}
		limited = true
		if seconds := int(math.Ceil(limit.ResetSeconds)); seconds > retryAfter {
			retryAfter = seconds
		}
	}
	if !limited {
		return nil
	}
	return writeMultimodalEvent(ctx, client, MultimodalEvent{
		Type: "flow.slow", Reason: "provider_rate_limit", Retryable: true, RetryAfterSeconds: retryAfter,
	})
}

func openAIConversationInput(contentType, field, value string) map[string]any {
	content := map[string]any{"type": contentType, field: value}
	return map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{"type": "message", "role": "user", "content": []any{content}},
	}
}

func openAIRealtimeText(payload []byte, field string) string {
	var event map[string]json.RawMessage
	if json.Unmarshal(payload, &event) != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(event[field], &value)
	return value
}

func writeOpenAIRealtimeEvent(ctx context.Context, upstream *websocket.Conn, event any) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return upstream.Write(ctx, websocket.MessageText, payload)
}

func openAIRealtimeProviderWriteTimeout(start MultimodalSessionStart) time.Duration {
	if start.BackpressureTimeoutMS > 0 {
		return time.Duration(start.BackpressureTimeoutMS) * time.Millisecond
	}
	return openAIRealtimeWriteTimeout
}

func (state *openAIRealtimeState) providerWrite(ctx context.Context, upstream *websocket.Conn, event any) error {
	if state.providerWriter != nil {
		return state.providerWriter(ctx, upstream, event)
	}
	return writeOpenAIRealtimeEvent(ctx, upstream, event)
}

func (state *openAIRealtimeState) writeProviderEvent(ctx context.Context, upstream *websocket.Conn, event any) error {
	timeout := state.providerTimeout
	if timeout <= 0 {
		timeout = openAIRealtimeWriteTimeout
	}
	writeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := state.providerWrite(writeContext, upstream, event)
	if ctx.Err() == nil && errors.Is(writeContext.Err(), context.DeadlineExceeded) {
		return errOpenAIRealtimeBackpressureTimeout
	}
	return err
}

func (state *openAIRealtimeState) failProviderWrite(clientContext context.Context, client *websocket.Conn, parentContext context.Context, cause error) (bool, error) {
	if parentContext.Err() != nil {
		return true, parentContext.Err()
	}
	code, message := "multimodal_provider_disconnected", "OpenAI realtime provider disconnected"
	if errors.Is(cause, errOpenAIRealtimeBackpressureTimeout) {
		code, message = "backpressure_timeout", "OpenAI realtime provider did not consume input before the backpressure timeout"
	}
	state.metrics.ErrorCode = code
	_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "error", Code: code, Message: message, Retryable: true})
	return true, cause
}

func proxyOpenAIRealtimeLoop(
	ctx context.Context,
	clientContext context.Context,
	client *websocket.Conn,
	upstream *websocket.Conn,
	metrics *MultimodalMetrics,
	onClient func(websocket.MessageType, []byte) (bool, error),
	onUpstream func(websocket.MessageType, []byte) (bool, error),
) error {
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
			if runtimeMetrics := runtimeMetricsFromContext(ctx); runtimeMetrics != nil {
				runtimeMetrics.observeStreamEvent(streamProtocolWebSocket, streamDirectionClientToRuntime, time.Since(startedAt))
			}
			if err != nil || done {
				return err
			}
		case frame := <-upstreamFrames:
			if frame.err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				metrics.ErrorCode = "multimodal_provider_disconnected"
				_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{
					Type: "error", Code: metrics.ErrorCode, Message: "OpenAI realtime provider disconnected", Retryable: true,
				})
				return errors.New("OpenAI realtime provider disconnected")
			}
			startedAt := time.Now()
			done, err := onUpstream(frame.messageType, frame.payload)
			if runtimeMetrics := runtimeMetricsFromContext(ctx); runtimeMetrics != nil {
				runtimeMetrics.observeStreamEvent(streamProtocolWebSocket, streamDirectionRuntimeToClient, time.Since(startedAt))
			}
			if err != nil || done {
				return err
			}
		}
	}
}

func writeMultimodalJSON(ctx context.Context, connection *websocket.Conn, event any) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func failOpenAIRealtimeHandshake(ctx context.Context, client *websocket.Conn, metrics MultimodalMetrics, code string, cause error) (MultimodalMetrics, error) {
	metrics.ErrorCode = code
	message := cause.Error()
	if code == "multimodal_provider_connect_failed" || code == "multimodal_provider_handshake_failed" {
		message = "OpenAI realtime provider handshake failed"
	}
	_ = writeMultimodalEvent(ctx, client, MultimodalEvent{Type: "error", Code: code, Message: message, Retryable: code == "multimodal_provider_connect_failed"})
	return metrics, cause
}

func multimodalHasTrackKind(tracks []MultimodalTrack, kind string) bool {
	for _, track := range tracks {
		if track.Kind == kind {
			return true
		}
	}
	return false
}

func multimodalHasModality(modalities []string, modality string) bool {
	for _, value := range modalities {
		if value == modality {
			return true
		}
	}
	return false
}
