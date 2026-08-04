package modelruntimeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	MultimodalProviderProtocolTMAWebSocket = "tma_multimodal_websocket_v1"
	MultimodalProviderProtocolOpenAI       = "openai_realtime_websocket"
	multimodalRuntimePath                  = "/internal/v1/multimodal/realtime"
)

type MultimodalRoute struct {
	ProviderID    string                     `json:"provider_id"`
	ProviderType  string                     `json:"provider_type"`
	BaseURL       string                     `json:"base_url"`
	APIKey        string                     `json:"api_key,omitempty"`
	Model         string                     `json:"model"`
	UpstreamModel string                     `json:"upstream_model,omitempty"`
	Protocol      string                     `json:"protocol"`
	Constraints   MultimodalRouteConstraints `json:"constraints"`
}

type MultimodalRouteConstraints struct {
	InputFormats     []MultimodalMediaFormat `json:"input_formats"`
	OutputModalities []string                `json:"output_modalities"`
	OutputFormats    []MultimodalMediaFormat `json:"output_formats,omitempty"`
	MaxInputTracks   int                     `json:"max_input_tracks"`
	MaxFrameBytes    int64                   `json:"max_frame_bytes"`
}

type MultimodalMediaFormat struct {
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Codec       string `json:"codec"`
}

type MultimodalRequest struct {
	Type             string                      `json:"type"`
	Route            MultimodalRoute             `json:"route"`
	Start            MultimodalSessionStart      `json:"start"`
	ResolveObjectRef MultimodalObjectRefResolver `json:"-"`
}

type MultimodalObjectRefResolver func(context.Context, MultimodalObjectRefInput) (MultimodalMediaFrame, error)

type MultimodalEvent struct {
	Type                 string `json:"type"`
	SessionID            string `json:"session_id,omitempty"`
	TrackID              string `json:"track_id,omitempty"`
	Text                 string `json:"text,omitempty"`
	Sequence             uint64 `json:"sequence,omitempty"`
	TimestampMicros      int64  `json:"timestamp_us,omitempty"`
	ObjectRefID          string `json:"object_ref_id,omitempty"`
	ContentType          string `json:"content_type,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	ChecksumSHA256       string `json:"checksum_sha256,omitempty"`
	Bytes                int64  `json:"bytes,omitempty"`
	Frames               int64  `json:"frames,omitempty"`
	AcknowledgedSequence uint64 `json:"acknowledged_sequence,omitempty"`
	RecommendedFPS       int    `json:"recommended_fps,omitempty"`
	Reason               string `json:"reason,omitempty"`
	Code                 string `json:"code,omitempty"`
	Message              string `json:"message,omitempty"`
	Retryable            bool   `json:"retryable,omitempty"`
	RetryAfterSeconds    int    `json:"retry_after_seconds,omitempty"`
	LimitScope           string `json:"limit_scope,omitempty"`
}

type MultimodalMetrics struct {
	InputItems         int64
	OutputItems        int64
	InputBytes         int64
	OutputBytes        int64
	InputCharacters    int64
	OutputCharacters   int64
	InputAudioMillis   int64
	OutputAudioMillis  int64
	InputVideoFrames   int64
	OutputVideoFrames  int64
	InputVideoDropped  int64
	OutputVideoDropped int64
	InputVideoMillis   int64
	OutputVideoMillis  int64
	Canceled           bool
	Completed          bool
	ErrorCode          string
	inputMedia         *multimodalMediaMetrics
	outputMedia        *multimodalMediaMetrics
}

type MultimodalProxy interface {
	ProxyMultimodal(context.Context, context.Context, *websocket.Conn, MultimodalRequest) (MultimodalMetrics, error)
}

type MultimodalProviderDialer func(context.Context, string, http.Header, string) (*websocket.Conn, *http.Response, error)

func (LocalExecutor) ProxyMultimodal(ctx, clientContext context.Context, client *websocket.Conn, request MultimodalRequest) (MultimodalMetrics, error) {
	return ProxyMultimodalWithDialer(ctx, clientContext, client, request, defaultMultimodalProviderDialer)
}

func ProxyMultimodalWithDialer(ctx, clientContext context.Context, client *websocket.Conn, request MultimodalRequest, dialer MultimodalProviderDialer) (MultimodalMetrics, error) {
	metrics := newMultimodalMetrics(request.Start.InputTracks)
	if err := validateMultimodalRequest(request); err != nil {
		metrics.ErrorCode = "invalid_multimodal_route"
		_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "error", Code: metrics.ErrorCode, Message: err.Error()})
		return metrics, err
	}
	if request.Route.Protocol == MultimodalProviderProtocolOpenAI {
		return proxyOpenAIRealtimeWithDialer(ctx, clientContext, client, request, dialer)
	}
	if dialer == nil {
		dialer = defaultMultimodalProviderDialer
	}
	headers := make(http.Header)
	if strings.TrimSpace(request.Route.APIKey) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(request.Route.APIKey))
	}
	upstream, response, err := dialer(ctx, request.Route.BaseURL, headers, MultimodalRealtimeSubprotocol)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		metrics.ErrorCode = "multimodal_provider_connect_failed"
		_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{
			Type: "error", Code: metrics.ErrorCode, Message: "multimodal provider connection failed", Retryable: true,
		})
		return metrics, err
	}
	upstream.SetReadLimit(int64(MultimodalMediaHeaderBytes + MultimodalMaxTrackIDBytes + MultimodalMaxFrameBytes))
	defer upstream.CloseNow()
	if upstream.Subprotocol() != MultimodalRealtimeSubprotocol {
		metrics.ErrorCode = "multimodal_protocol_negotiation_failed"
		err = errors.New("multimodal provider did not negotiate the required WebSocket subprotocol")
		_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "error", Code: metrics.ErrorCode, Message: err.Error()})
		return metrics, err
	}
	startPayload, err := json.Marshal(request.Start)
	if err != nil {
		return metrics, err
	}
	if err := upstream.Write(ctx, websocket.MessageText, startPayload); err != nil {
		return metrics, err
	}
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, 15*time.Second)
	defer cancelHandshake()
	messageType, payload, err := upstream.Read(handshakeContext)
	if err != nil {
		metrics.ErrorCode = "multimodal_provider_handshake_failed"
		_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "error", Code: metrics.ErrorCode, Message: "multimodal provider handshake failed", Retryable: true})
		return metrics, err
	}
	var started MultimodalSessionStarted
	if messageType != websocket.MessageText || decodeMultimodalControl(payload, &started) != nil || started.Validate(request.Start) != nil {
		metrics.ErrorCode = "multimodal_provider_handshake_failed"
		err = errors.New("multimodal provider returned an invalid session.started event")
		_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "error", Code: metrics.ErrorCode, Message: err.Error()})
		return metrics, err
	}
	if err := validateMultimodalNegotiation(request.Route.Constraints, started); err != nil {
		metrics.ErrorCode = "multimodal_provider_capability_violation"
		_ = writeMultimodalEvent(clientContext, client, MultimodalEvent{Type: "error", Code: metrics.ErrorCode, Message: err.Error()})
		return metrics, err
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
	inputTracks := multimodalTrackMap(request.Start.InputTracks)
	outputTracks := multimodalTrackMap(started.OutputTracks)
	metrics.setOutputTracks(started.OutputTracks)
	if err := client.Write(clientContext, websocket.MessageText, payload); err != nil {
		return metrics, err
	}
	err = proxyWebSocketLoop(ctx, clientContext, client, upstream,
		func(messageType websocket.MessageType, payload []byte) (bool, error) {
			return processMultimodalClientMessage(ctx, clientContext, client, upstream, messageType, payload, inputTracks, inputWindow, outputWindow, request.ResolveObjectRef, &metrics)
		},
		func(messageType websocket.MessageType, payload []byte) (bool, error) {
			return processMultimodalProviderMessage(ctx, clientContext, client, upstream, messageType, payload, outputTracks, inputWindow, outputWindow, &metrics)
		},
	)
	return metrics, err
}

func validateMultimodalRequest(request MultimodalRequest) error {
	if request.Type != "session.open" {
		return errors.New("model runtime multimodal request requires session.open")
	}
	if err := request.Start.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(request.Route.ProviderID) == "" || strings.TrimSpace(request.Route.Model) == "" {
		return errors.New("model runtime multimodal route requires provider_id and model")
	}
	switch request.Route.Protocol {
	case MultimodalProviderProtocolTMAWebSocket, MultimodalProviderProtocolOpenAI:
	default:
		return errors.New("model runtime multimodal route protocol is unsupported")
	}
	if err := validateMultimodalRouteConstraints(request.Route.Constraints, request.Start); err != nil {
		return err
	}
	parsed, err := url.Parse(strings.TrimSpace(request.Route.BaseURL))
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("model runtime multimodal route requires an absolute wss URL without credentials")
	}
	if request.Route.Protocol == MultimodalProviderProtocolOpenAI {
		return validateOpenAIMultimodalRequest(request)
	}
	return nil
}

// ValidateMultimodalRequest validates the approved internal route and session
// before the Runtime or an in-process executor connects to a Provider.
func ValidateMultimodalRequest(request MultimodalRequest) error {
	return validateMultimodalRequest(request)
}

func validateMultimodalRouteConstraints(constraints MultimodalRouteConstraints, start MultimodalSessionStart) error {
	if constraints.MaxInputTracks < 1 || constraints.MaxInputTracks > MultimodalMaxTracks || len(start.InputTracks) > constraints.MaxInputTracks {
		return errors.New("multimodal route input track limit is invalid or exceeded")
	}
	if constraints.MaxFrameBytes < 1 || constraints.MaxFrameBytes > MultimodalMaxFrameBytes {
		return errors.New("multimodal route max_frame_bytes is invalid")
	}
	if len(constraints.InputFormats) == 0 || len(constraints.OutputModalities) == 0 {
		return errors.New("multimodal route requires input formats and output modalities")
	}
	for _, track := range start.InputTracks {
		if !multimodalFormatMatchesTrack(constraints.InputFormats, track) {
			return fmt.Errorf("multimodal route does not support input track %q format", track.ID)
		}
	}
	allowedOutputs := make(map[string]bool, len(constraints.OutputModalities))
	for _, modality := range constraints.OutputModalities {
		allowedOutputs[strings.ToLower(strings.TrimSpace(modality))] = true
	}
	for _, modality := range start.OutputModalities {
		if !allowedOutputs[modality] {
			return fmt.Errorf("multimodal route does not support output modality %q", modality)
		}
	}
	if start.OutputFlowLimits != nil && start.OutputFlowLimits.MaxFrameBytes > constraints.MaxFrameBytes {
		return errors.New("multimodal output frame offer exceeds the route limit")
	}
	return nil
}

func validateMultimodalNegotiation(constraints MultimodalRouteConstraints, started MultimodalSessionStarted) error {
	if started.InputFlowLimits.MaxFrameBytes > constraints.MaxFrameBytes {
		return errors.New("provider input frame limit exceeds the approved route")
	}
	if started.OutputFlowLimits != nil && started.OutputFlowLimits.MaxFrameBytes > constraints.MaxFrameBytes {
		return errors.New("provider output frame limit exceeds the approved route")
	}
	for _, track := range started.OutputTracks {
		if !multimodalFormatMatchesTrack(constraints.OutputFormats, track) {
			return fmt.Errorf("provider output track %q format is outside the approved route", track.ID)
		}
	}
	return nil
}

func multimodalFormatMatchesTrack(formats []MultimodalMediaFormat, track MultimodalTrack) bool {
	contentType := normalizedContentType(track.ContentType)
	codec := strings.ToLower(strings.TrimSpace(track.Codec))
	for _, format := range formats {
		if strings.ToLower(strings.TrimSpace(format.Kind)) == track.Kind &&
			normalizedContentType(format.ContentType) == contentType &&
			strings.ToLower(strings.TrimSpace(format.Codec)) == codec {
			return true
		}
	}
	return false
}

func processMultimodalClientMessage(
	ctx context.Context,
	clientContext context.Context,
	client *websocket.Conn,
	upstream *websocket.Conn,
	messageType websocket.MessageType,
	payload []byte,
	tracks map[string]MultimodalTrack,
	inputWindow *MultimodalCreditWindow,
	outputWindow *MultimodalCreditWindow,
	resolveObjectRef MultimodalObjectRefResolver,
	metrics *MultimodalMetrics,
) (bool, error) {
	if messageType == websocket.MessageBinary {
		frame, err := DecodeMultimodalMediaFrame(payload)
		if err != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_media_frame", err)
		}
		if err := validateMultimodalFrameTrack(frame, tracks); err != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_media_track", err)
		}
		if err := inputWindow.ReserveFrame(frame); err != nil {
			return failMultimodalSession(clientContext, client, metrics, multimodalFlowErrorCode(err), err)
		}
		metrics.observeInputFrame(frame)
		return false, upstream.Write(ctx, messageType, payload)
	}
	var event MultimodalEvent
	if messageType != websocket.MessageText || decodeMultimodalControl(payload, &event) != nil {
		return failMultimodalSession(clientContext, client, metrics, "invalid_control_event", errors.New("invalid multimodal client control event"))
	}
	switch event.Type {
	case "input.text.append":
		text := strings.TrimSpace(event.Text)
		if text == "" {
			return failMultimodalSession(clientContext, client, metrics, "invalid_control_event", errors.New("input.text.append requires text"))
		}
		metrics.InputItems++
		metrics.InputCharacters += int64(utf8.RuneCountInString(text))
	case "input.commit", "ping":
	case "session.cancel":
		metrics.Canceled = true
	case "flow.credit":
		if outputWindow == nil {
			return failMultimodalSession(clientContext, client, metrics, "flow_control_violation", errors.New("session has no binary output flow window"))
		}
		var credit MultimodalFlowCredit
		if decodeMultimodalControl(payload, &credit) != nil || credit.Type != "flow.credit" {
			return failMultimodalSession(clientContext, client, metrics, "invalid_control_event", errors.New("invalid output flow.credit event"))
		}
		if err := outputWindow.Grant(credit); err != nil {
			return failMultimodalSession(clientContext, client, metrics, "flow_control_violation", err)
		}
	case "input.object_ref":
		if resolveObjectRef == nil {
			return failMultimodalSession(clientContext, client, metrics, "unresolved_object_ref", errors.New("object ref input must be resolved and verified by tma-server"))
		}
		var input MultimodalObjectRefInput
		if decodeMultimodalControl(payload, &input) != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_control_event", errors.New("invalid input.object_ref event"))
		}
		frame, err := resolveObjectRef(ctx, input)
		if err != nil {
			return failMultimodalSession(clientContext, client, metrics, "object_ref_rejected", errors.New("multimodal object ref is unavailable"))
		}
		if err := validateMultimodalFrameTrack(frame, tracks); err != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_media_track", err)
		}
		if err := inputWindow.ReserveFrame(frame); err != nil {
			return failMultimodalSession(clientContext, client, metrics, multimodalFlowErrorCode(err), err)
		}
		encoded, err := EncodeMultimodalMediaFrame(frame)
		if err != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_media_frame", err)
		}
		metrics.observeInputFrame(frame)
		return false, upstream.Write(ctx, websocket.MessageBinary, encoded)
	default:
		return failMultimodalSession(clientContext, client, metrics, "unsupported_control_event", fmt.Errorf("unsupported client event %q", event.Type))
	}
	return false, upstream.Write(ctx, messageType, payload)
}

func processMultimodalProviderMessage(
	ctx context.Context,
	clientContext context.Context,
	client *websocket.Conn,
	upstream *websocket.Conn,
	messageType websocket.MessageType,
	payload []byte,
	tracks map[string]MultimodalTrack,
	inputWindow *MultimodalCreditWindow,
	outputWindow *MultimodalCreditWindow,
	metrics *MultimodalMetrics,
) (bool, error) {
	if messageType == websocket.MessageBinary {
		if outputWindow == nil {
			return failMultimodalSession(clientContext, client, metrics, "flow_control_violation", errors.New("provider sent binary output without a negotiated output window"))
		}
		frame, err := DecodeMultimodalMediaFrame(payload)
		if err != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_media_frame", err)
		}
		if err := validateMultimodalFrameTrack(frame, tracks); err != nil {
			return failMultimodalSession(clientContext, client, metrics, "invalid_media_track", err)
		}
		if err := outputWindow.ReserveFrame(frame); err != nil {
			return failMultimodalSession(clientContext, client, metrics, multimodalFlowErrorCode(err), err)
		}
		metrics.observeOutputFrame(frame)
		return false, client.Write(clientContext, messageType, payload)
	}
	var event MultimodalEvent
	if messageType != websocket.MessageText || decodeMultimodalControl(payload, &event) != nil {
		return failMultimodalSession(clientContext, client, metrics, "invalid_provider_event", errors.New("invalid multimodal provider control event"))
	}
	done := false
	switch event.Type {
	case "output.text.delta":
		metrics.OutputCharacters += int64(utf8.RuneCountInString(event.Text))
	case "output.text.final":
		metrics.OutputItems++
	case "flow.credit":
		var credit MultimodalFlowCredit
		if decodeMultimodalControl(payload, &credit) != nil || credit.Type != "flow.credit" {
			return failMultimodalSession(clientContext, client, metrics, "invalid_provider_event", errors.New("invalid input flow.credit event"))
		}
		if err := inputWindow.Grant(credit); err != nil {
			return failMultimodalSession(clientContext, client, metrics, "flow_control_violation", err)
		}
	case "flow.slow", "pong":
	case "session.completed":
		metrics.Completed = true
		done = true
	case "session.canceled":
		metrics.Canceled = true
		done = true
	case "error":
		metrics.ErrorCode = strings.TrimSpace(event.Code)
		if metrics.ErrorCode == "" {
			metrics.ErrorCode = "multimodal_provider_error"
		}
		done = true
	default:
		return failMultimodalSession(clientContext, client, metrics, "unsupported_provider_event", fmt.Errorf("unsupported provider event %q", event.Type))
	}
	return done, client.Write(clientContext, messageType, payload)
}

func validateMultimodalFrameTrack(frame MultimodalMediaFrame, tracks map[string]MultimodalTrack) error {
	track, exists := tracks[frame.TrackID]
	if !exists {
		return fmt.Errorf("frame references undeclared track %q", frame.TrackID)
	}
	if track.Kind != frame.Kind {
		return fmt.Errorf("frame kind %q does not match track kind %q", frame.Kind, track.Kind)
	}
	return nil
}

func multimodalTrackMap(tracks []MultimodalTrack) map[string]MultimodalTrack {
	result := make(map[string]MultimodalTrack, len(tracks))
	for _, track := range tracks {
		result[track.ID] = track
	}
	return result
}

func failMultimodalSession(ctx context.Context, client *websocket.Conn, metrics *MultimodalMetrics, code string, err error) (bool, error) {
	metrics.ErrorCode = code
	_ = writeMultimodalEvent(ctx, client, MultimodalEvent{Type: "error", Code: code, Message: err.Error()})
	return true, err
}

func multimodalFlowErrorCode(err error) string {
	if errors.Is(err, ErrMultimodalSequence) {
		return "media_sequence_violation"
	}
	return "flow_control_violation"
}

func decodeMultimodalControl(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("control event must contain exactly one JSON object")
	}
	return nil
}

func writeMultimodalEvent(ctx context.Context, connection *websocket.Conn, event MultimodalEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func defaultMultimodalProviderDialer(ctx context.Context, target string, headers http.Header, subprotocol string) (*websocket.Conn, *http.Response, error) {
	options := &websocket.DialOptions{HTTPHeader: headers}
	if strings.TrimSpace(subprotocol) != "" {
		options.Subprotocols = []string{subprotocol}
	}
	return websocket.Dial(ctx, target, options)
}

func serveMultimodalRuntime(w http.ResponseWriter, r *http.Request, proxy MultimodalProxy, metrics *RuntimeMetrics) {
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
	if err != nil {
		return
	}
	metrics.streamStarted(streamProtocolWebSocket)
	defer metrics.streamFinished(streamProtocolWebSocket)
	connection.SetReadLimit(int64(MultimodalMediaHeaderBytes + MultimodalMaxTrackIDBytes + MultimodalMaxFrameBytes))
	defer connection.CloseNow()
	if connection.Subprotocol() != MultimodalRealtimeSubprotocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "multimodal subprotocol is required")
		return
	}
	ctx := withRuntimeMetrics(r.Context(), metrics)
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		return
	}
	var request MultimodalRequest
	if messageType != websocket.MessageText || decodeMultimodalControl(payload, &request) != nil || request.Type != "session.open" {
		_ = writeMultimodalEvent(ctx, connection, MultimodalEvent{Type: "error", Code: "invalid_session_start", Message: "first internal event must be session.open"})
		return
	}
	_, _ = proxy.ProxyMultimodal(ctx, ctx, connection, request)
	_ = connection.Close(websocket.StatusNormalClosure, "multimodal session complete")
}

func (e *HTTPExecutor) ProxyMultimodal(ctx, clientContext context.Context, client *websocket.Conn, request MultimodalRequest) (MultimodalMetrics, error) {
	metrics := newMultimodalMetrics(request.Start.InputTracks)
	inputTracks := multimodalTrackMap(request.Start.InputTracks)
	target, err := websocketEndpoint(e.endpoint, multimodalRuntimePath)
	if err != nil {
		return failMultimodalTransport(clientContext, client, metrics, err)
	}
	authorization, err := e.auth.authorization(http.MethodGet, multimodalRuntimePath)
	if err != nil {
		return failMultimodalTransport(clientContext, client, metrics, err)
	}
	headers := make(http.Header)
	headers.Set("Authorization", authorization)
	websocketClient := *e.client
	websocketClient.Timeout = 0
	upstream, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: &websocketClient, HTTPHeader: headers, Subprotocols: []string{MultimodalRealtimeSubprotocol},
	})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return failMultimodalTransport(clientContext, client, metrics, err)
	}
	upstream.SetReadLimit(int64(MultimodalMediaHeaderBytes + MultimodalMaxTrackIDBytes + MultimodalMaxFrameBytes))
	defer upstream.CloseNow()
	if upstream.Subprotocol() != MultimodalRealtimeSubprotocol {
		return failMultimodalTransport(clientContext, client, metrics, errors.New("model runtime did not negotiate the multimodal subprotocol"))
	}
	open, err := json.Marshal(request)
	if err != nil {
		return metrics, err
	}
	if err := upstream.Write(ctx, websocket.MessageText, open); err != nil {
		return metrics, err
	}
	err = proxyWebSocketLoop(ctx, clientContext, client, upstream,
		func(messageType websocket.MessageType, payload []byte) (bool, error) {
			if messageType == websocket.MessageText && request.ResolveObjectRef != nil {
				var event MultimodalEvent
				if decodeMultimodalControl(payload, &event) == nil && event.Type == "input.object_ref" {
					var input MultimodalObjectRefInput
					if decodeMultimodalControl(payload, &input) != nil {
						return failMultimodalSession(clientContext, client, &metrics, "invalid_control_event", errors.New("invalid input.object_ref event"))
					}
					frame, resolveErr := request.ResolveObjectRef(ctx, input)
					if resolveErr != nil {
						return failMultimodalSession(clientContext, client, &metrics, "object_ref_rejected", errors.New("multimodal object ref is unavailable"))
					}
					if trackErr := validateMultimodalFrameTrack(frame, inputTracks); trackErr != nil {
						return failMultimodalSession(clientContext, client, &metrics, "invalid_media_track", trackErr)
					}
					encoded, encodeErr := EncodeMultimodalMediaFrame(frame)
					if encodeErr != nil {
						return failMultimodalSession(clientContext, client, &metrics, "invalid_media_frame", encodeErr)
					}
					metrics.observeInputFrame(frame)
					return false, upstream.Write(ctx, websocket.MessageBinary, encoded)
				}
			}
			observeMultimodalTransportInput(messageType, payload, &metrics)
			return false, upstream.Write(ctx, messageType, payload)
		},
		func(messageType websocket.MessageType, payload []byte) (bool, error) {
			done := observeMultimodalTransportOutput(messageType, payload, &metrics)
			return done, client.Write(clientContext, messageType, payload)
		},
	)
	return metrics, err
}

func failMultimodalTransport(ctx context.Context, client *websocket.Conn, metrics MultimodalMetrics, cause error) (MultimodalMetrics, error) {
	metrics.ErrorCode = "multimodal_runtime_connect_failed"
	_ = writeMultimodalEvent(ctx, client, MultimodalEvent{
		Type: "error", Code: metrics.ErrorCode, Message: "multimodal runtime connection failed", Retryable: true,
	})
	return metrics, cause
}

func observeMultimodalTransportInput(messageType websocket.MessageType, payload []byte, metrics *MultimodalMetrics) {
	if messageType == websocket.MessageBinary {
		if frame, err := DecodeMultimodalMediaFrame(payload); err == nil {
			metrics.observeInputFrame(frame)
		}
		return
	}
	var event MultimodalEvent
	if decodeMultimodalControl(payload, &event) != nil {
		return
	}
	if event.Type == "input.text.append" {
		metrics.InputItems++
		metrics.InputCharacters += int64(utf8.RuneCountInString(strings.TrimSpace(event.Text)))
	}
	if event.Type == "session.cancel" {
		metrics.Canceled = true
	}
}

func observeMultimodalTransportOutput(messageType websocket.MessageType, payload []byte, metrics *MultimodalMetrics) bool {
	if messageType == websocket.MessageBinary {
		if frame, err := DecodeMultimodalMediaFrame(payload); err == nil {
			metrics.observeOutputFrame(frame)
		}
		return false
	}
	var event MultimodalEvent
	if decodeMultimodalControl(payload, &event) != nil {
		return false
	}
	if event.Type == "session.started" {
		var started MultimodalSessionStarted
		if decodeMultimodalControl(payload, &started) == nil {
			metrics.setOutputTracks(started.OutputTracks)
		}
	}
	switch event.Type {
	case "output.text.delta":
		metrics.OutputCharacters += int64(utf8.RuneCountInString(event.Text))
	case "output.text.final":
		metrics.OutputItems++
	case "session.completed":
		metrics.Completed = true
		return true
	case "session.canceled":
		metrics.Canceled = true
		return true
	case "error":
		metrics.ErrorCode = strings.TrimSpace(event.Code)
		if metrics.ErrorCode == "" {
			metrics.ErrorCode = "multimodal_runtime_error"
		}
		return true
	}
	return false
}
