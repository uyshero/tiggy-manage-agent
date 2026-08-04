package tma

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const (
	MultimodalRealtimeProtocol  = "tma.multimodal.realtime.v1"
	MultimodalMediaHeaderBytes  = 28
	MultimodalMaxTrackIDBytes   = 64
	MultimodalMaxFrameBytes     = 4 << 20
	MultimodalMaxInFlightBytes  = 16 << 20
	MultimodalMaxInFlightFrames = 8
	MultimodalDeliveryReliable  = "reliable"
	MultimodalDeliveryLatest    = "latest"
	MultimodalMediaAudio        = "audio"
	MultimodalMediaImage        = "image"
	MultimodalMediaVideo        = "video"
)

type MultimodalMediaFlags uint16

const (
	MultimodalMediaFlagKeyFrame      MultimodalMediaFlags = 1 << 0
	MultimodalMediaFlagEndOfTrack    MultimodalMediaFlags = 1 << 1
	MultimodalMediaFlagDiscontinuity MultimodalMediaFlags = 1 << 2
	multimodalMediaAllowedFlags                           = MultimodalMediaFlagKeyFrame | MultimodalMediaFlagEndOfTrack | MultimodalMediaFlagDiscontinuity
)

var (
	ErrMultimodalInvalidFrame = errors.New("tma: invalid multimodal media frame")
	ErrMultimodalFlowControl  = errors.New("tma: multimodal flow control violation")
	ErrMultimodalSequence     = errors.New("tma: multimodal media sequence violation")
)

type MultimodalTrack struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	ContentType  string `json:"content_type"`
	Codec        string `json:"codec"`
	Delivery     string `json:"delivery"`
	SampleRateHz int    `json:"sample_rate_hz,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	MaxFPS       int    `json:"max_fps,omitempty"`
}

type MultimodalFlowLimits struct {
	MaxFrameBytes     int64 `json:"max_frame_bytes"`
	MaxInFlightBytes  int64 `json:"max_in_flight_bytes"`
	MaxInFlightFrames int64 `json:"max_in_flight_frames"`
}

func DefaultMultimodalFlowLimits() MultimodalFlowLimits {
	return MultimodalFlowLimits{
		MaxFrameBytes: MultimodalMaxFrameBytes, MaxInFlightBytes: MultimodalMaxInFlightBytes,
		MaxInFlightFrames: MultimodalMaxInFlightFrames,
	}
}

type MultimodalFlowCredit struct {
	Type                 string `json:"type,omitempty"`
	Bytes                int64  `json:"bytes"`
	Frames               int64  `json:"frames"`
	TrackID              string `json:"track_id,omitempty"`
	AcknowledgedSequence uint64 `json:"acknowledged_sequence,omitempty"`
}

type MultimodalSessionStart struct {
	ProviderID            string
	Model                 string
	SessionID             string
	InputTracks           []MultimodalTrack
	OutputModalities      []string
	OutputFlowLimits      *MultimodalFlowLimits
	InitialOutputCredit   *MultimodalFlowCredit
	BackpressureTimeoutMS int
}

type multimodalSessionStartWire struct {
	Type                  string                `json:"type"`
	ProtocolVersion       string                `json:"protocol_version"`
	ProviderID            string                `json:"provider_id"`
	Model                 string                `json:"model"`
	SessionID             string                `json:"session_id,omitempty"`
	InputTracks           []MultimodalTrack     `json:"input_tracks"`
	OutputModalities      []string              `json:"output_modalities"`
	OutputFlowLimits      *MultimodalFlowLimits `json:"output_flow_limits,omitempty"`
	InitialOutputCredit   *MultimodalFlowCredit `json:"initial_output_credit,omitempty"`
	BackpressureTimeoutMS int                   `json:"backpressure_timeout_ms,omitempty"`
}

type MultimodalSessionStarted struct {
	Type                string                `json:"type"`
	ProtocolVersion     string                `json:"protocol_version"`
	SessionID           string                `json:"session_id"`
	OutputTracks        []MultimodalTrack     `json:"output_tracks,omitempty"`
	InputFlowLimits     MultimodalFlowLimits  `json:"input_flow_limits"`
	InitialInputCredit  MultimodalFlowCredit  `json:"initial_input_credit"`
	OutputFlowLimits    *MultimodalFlowLimits `json:"output_flow_limits,omitempty"`
	InitialOutputCredit *MultimodalFlowCredit `json:"initial_output_credit,omitempty"`
	HeartbeatMS         int                   `json:"heartbeat_ms"`
}

type MultimodalMediaFrame struct {
	Kind            string
	Flags           MultimodalMediaFlags
	Sequence        uint64
	TimestampMicros int64
	TrackID         string
	Payload         []byte
}

type MultimodalObjectRefInput struct {
	TrackID         string `json:"track_id"`
	Sequence        uint64 `json:"sequence"`
	TimestampMicros int64  `json:"timestamp_us"`
	ObjectRefID     string `json:"object_ref_id"`
	ContentType     string `json:"content_type"`
	SizeBytes       int64  `json:"size_bytes"`
	ChecksumSHA256  string `json:"checksum_sha256,omitempty"`
}

type MultimodalRealtimeEvent struct {
	Type                 string                    `json:"type"`
	SessionID            string                    `json:"session_id,omitempty"`
	TrackID              string                    `json:"track_id,omitempty"`
	Text                 string                    `json:"text,omitempty"`
	Sequence             uint64                    `json:"sequence,omitempty"`
	TimestampMicros      int64                     `json:"timestamp_us,omitempty"`
	Bytes                int64                     `json:"bytes,omitempty"`
	Frames               int64                     `json:"frames,omitempty"`
	AcknowledgedSequence uint64                    `json:"acknowledged_sequence,omitempty"`
	RecommendedFPS       int                       `json:"recommended_fps,omitempty"`
	Reason               string                    `json:"reason,omitempty"`
	Code                 string                    `json:"code,omitempty"`
	Message              string                    `json:"message,omitempty"`
	Retryable            bool                      `json:"retryable,omitempty"`
	RetryAfterSeconds    int                       `json:"retry_after_seconds,omitempty"`
	LimitScope           string                    `json:"limit_scope,omitempty"`
	Started              *MultimodalSessionStarted `json:"-"`
	Credit               *MultimodalFlowCredit     `json:"-"`
	Media                *MultimodalMediaFrame     `json:"-"`
}

type MultimodalRealtimeError struct {
	Code              string
	Message           string
	Retryable         bool
	RetryAfterSeconds int
	LimitScope        string
}

func (e *MultimodalRealtimeError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "tma: multimodal realtime request failed"
	}
	return "tma: " + e.Message
}

type MultimodalRealtimeClient struct {
	connection *websocket.Conn
	startMu    sync.Mutex
	readMu     sync.Mutex
	writeMu    sync.Mutex
	stateMu    sync.Mutex

	started       bool
	inputTracks   map[string]MultimodalTrack
	inputLimits   MultimodalFlowLimits
	inputBytes    int64
	inputFrames   int64
	inputSequence map[string]uint64
	droppedLatest map[string]bool
	creditChanged chan struct{}
}

func (s *ModelRuntimeService) DialMultimodalRealtime(ctx context.Context) (*MultimodalRealtimeClient, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("tma: model runtime service is not initialized")
	}
	request, err := s.client.newRequest(ctx, http.MethodGet, "/v2/model-runtime/multimodal/realtime", nil)
	if err != nil {
		return nil, err
	}
	target := *request.URL
	if target.Scheme == "https" {
		target.Scheme = "wss"
	} else {
		target.Scheme = "ws"
	}
	connection, response, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{
		HTTPHeader: request.Header, Subprotocols: []string{MultimodalRealtimeProtocol},
	})
	if err != nil {
		if response != nil {
			defer response.Body.Close()
		}
		return nil, err
	}
	if connection.Subprotocol() != MultimodalRealtimeProtocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "multimodal subprotocol was not negotiated")
		return nil, errors.New("tma: server did not negotiate the multimodal realtime subprotocol")
	}
	connection.SetReadLimit(MultimodalMediaHeaderBytes + MultimodalMaxTrackIDBytes + MultimodalMaxFrameBytes)
	return &MultimodalRealtimeClient{
		connection: connection, inputTracks: map[string]MultimodalTrack{}, inputSequence: map[string]uint64{},
		droppedLatest: map[string]bool{}, creditChanged: make(chan struct{}),
	}, nil
}

func (c *MultimodalRealtimeClient) Start(ctx context.Context, request MultimodalSessionStart) (MultimodalSessionStarted, error) {
	if c == nil || c.connection == nil {
		return MultimodalSessionStarted{}, errors.New("tma: multimodal realtime client is not connected")
	}
	c.startMu.Lock()
	defer c.startMu.Unlock()
	c.stateMu.Lock()
	alreadyStarted := c.started
	c.stateMu.Unlock()
	if alreadyStarted {
		return MultimodalSessionStarted{}, errors.New("tma: multimodal session has already started")
	}
	wire := multimodalSessionStartWire{
		Type: "session.start", ProtocolVersion: MultimodalRealtimeProtocol,
		ProviderID: request.ProviderID, Model: request.Model, SessionID: request.SessionID,
		InputTracks: request.InputTracks, OutputModalities: request.OutputModalities,
		OutputFlowLimits: request.OutputFlowLimits, InitialOutputCredit: request.InitialOutputCredit,
		BackpressureTimeoutMS: request.BackpressureTimeoutMS,
	}
	if err := validateMultimodalSessionStart(wire); err != nil {
		return MultimodalSessionStarted{}, err
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if err := c.writeJSON(ctx, wire); err != nil {
		return MultimodalSessionStarted{}, err
	}
	event, err := c.read(ctx)
	if err != nil {
		return MultimodalSessionStarted{}, err
	}
	if event.Type == "error" {
		return MultimodalSessionStarted{}, multimodalEventError(event)
	}
	if event.Started == nil {
		return MultimodalSessionStarted{}, errors.New("tma: first multimodal server event must be session.started")
	}
	started := *event.Started
	if err := validateMultimodalSessionStarted(started, wire); err != nil {
		return MultimodalSessionStarted{}, err
	}
	c.stateMu.Lock()
	c.started = true
	c.inputLimits = started.InputFlowLimits
	c.inputBytes = started.InitialInputCredit.Bytes
	c.inputFrames = started.InitialInputCredit.Frames
	for _, track := range request.InputTracks {
		c.inputTracks[track.ID] = track
	}
	c.signalCreditChangedLocked()
	c.stateMu.Unlock()
	return started, nil
}

func (c *MultimodalRealtimeClient) SendMedia(ctx context.Context, frame MultimodalMediaFrame) (bool, error) {
	if err := validateMultimodalMediaFrame(frame); err != nil {
		return false, err
	}
	c.stateMu.Lock()
	track, trackExists := c.inputTracks[frame.TrackID]
	c.stateMu.Unlock()
	if trackExists && track.Kind != frame.Kind {
		return false, errors.New("tma: media frame kind does not match its input track")
	}
	reserved, err := c.reserveInput(ctx, frame.TrackID, frame.Sequence, int64(len(frame.Payload)))
	if err != nil || !reserved {
		return reserved, err
	}
	c.stateMu.Lock()
	if c.droppedLatest[frame.TrackID] {
		frame.Flags |= MultimodalMediaFlagDiscontinuity
		delete(c.droppedLatest, frame.TrackID)
	}
	c.stateMu.Unlock()
	payload, err := EncodeMultimodalMediaFrame(frame)
	if err != nil {
		return false, err
	}
	return true, c.write(ctx, websocket.MessageBinary, payload)
}

func (c *MultimodalRealtimeClient) SendObjectRef(ctx context.Context, input MultimodalObjectRefInput) (bool, error) {
	if strings.TrimSpace(input.ObjectRefID) == "" || input.SizeBytes <= 0 || input.TimestampMicros < 0 {
		return false, errors.New("tma: multimodal object ref requires object_ref_id, positive size_bytes, and non-negative timestamp_us")
	}
	reserved, err := c.reserveInput(ctx, input.TrackID, input.Sequence, input.SizeBytes)
	if err != nil || !reserved {
		return reserved, err
	}
	c.stateMu.Lock()
	delete(c.droppedLatest, input.TrackID)
	c.stateMu.Unlock()
	return true, c.writeJSON(ctx, struct {
		Type string `json:"type"`
		MultimodalObjectRefInput
	}{Type: "input.object_ref", MultimodalObjectRefInput: input})
}

func (c *MultimodalRealtimeClient) AppendText(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("tma: multimodal input text is required")
	}
	return c.writeJSON(ctx, map[string]any{"type": "input.text.append", "text": text})
}

func (c *MultimodalRealtimeClient) CommitInput(ctx context.Context) error {
	return c.writeJSON(ctx, map[string]any{"type": "input.commit"})
}

func (c *MultimodalRealtimeClient) GrantOutputCredit(ctx context.Context, frame MultimodalMediaFrame) error {
	if len(frame.Payload) == 0 || frame.Sequence == 0 || strings.TrimSpace(frame.TrackID) == "" {
		return errors.New("tma: output credit requires a consumed media frame")
	}
	return c.writeJSON(ctx, MultimodalFlowCredit{
		Type: "flow.credit", Bytes: int64(len(frame.Payload)), Frames: 1,
		TrackID: frame.TrackID, AcknowledgedSequence: frame.Sequence,
	})
}

func (c *MultimodalRealtimeClient) Ping(ctx context.Context) error {
	return c.writeJSON(ctx, map[string]any{"type": "ping"})
}

func (c *MultimodalRealtimeClient) Cancel(ctx context.Context) error {
	return c.writeJSON(ctx, map[string]any{"type": "session.cancel"})
}

func (c *MultimodalRealtimeClient) Read(ctx context.Context) (MultimodalRealtimeEvent, error) {
	if c == nil || c.connection == nil {
		return MultimodalRealtimeEvent{}, errors.New("tma: multimodal realtime client is not connected")
	}
	c.stateMu.Lock()
	started := c.started
	c.stateMu.Unlock()
	if !started {
		return MultimodalRealtimeEvent{}, errors.New("tma: multimodal session has not started")
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return c.read(ctx)
}

func (c *MultimodalRealtimeClient) read(ctx context.Context) (MultimodalRealtimeEvent, error) {
	messageType, payload, err := c.connection.Read(ctx)
	if err != nil {
		return MultimodalRealtimeEvent{}, err
	}
	if messageType == websocket.MessageBinary {
		frame, err := DecodeMultimodalMediaFrame(payload)
		if err != nil {
			return MultimodalRealtimeEvent{}, err
		}
		return MultimodalRealtimeEvent{Type: "media", TrackID: frame.TrackID, Sequence: frame.Sequence, TimestampMicros: frame.TimestampMicros, Media: &frame}, nil
	}
	var event MultimodalRealtimeEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return MultimodalRealtimeEvent{}, err
	}
	switch event.Type {
	case "session.started":
		var started MultimodalSessionStarted
		if err := json.Unmarshal(payload, &started); err != nil {
			return MultimodalRealtimeEvent{}, err
		}
		event.Started = &started
	case "flow.credit":
		var credit MultimodalFlowCredit
		if err := json.Unmarshal(payload, &credit); err != nil {
			return MultimodalRealtimeEvent{}, err
		}
		if err := c.applyInputCredit(credit); err != nil {
			return MultimodalRealtimeEvent{}, err
		}
		event.Credit = &credit
	}
	return event, nil
}

func (c *MultimodalRealtimeClient) Close(status websocket.StatusCode, reason string) error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.Close(status, reason)
}

func (c *MultimodalRealtimeClient) CloseNow() error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.CloseNow()
}

func (c *MultimodalRealtimeClient) reserveInput(ctx context.Context, trackID string, sequence uint64, size int64) (bool, error) {
	for {
		c.stateMu.Lock()
		if !c.started {
			c.stateMu.Unlock()
			return false, errors.New("tma: multimodal session has not started")
		}
		track, ok := c.inputTracks[trackID]
		if !ok || sequence == 0 || size <= 0 || size > c.inputLimits.MaxFrameBytes {
			c.stateMu.Unlock()
			return false, ErrMultimodalInvalidFrame
		}
		if sequence <= c.inputSequence[trackID] {
			c.stateMu.Unlock()
			return false, ErrMultimodalSequence
		}
		if size <= c.inputBytes && c.inputFrames > 0 {
			c.inputBytes -= size
			c.inputFrames--
			c.inputSequence[trackID] = sequence
			c.stateMu.Unlock()
			return true, nil
		}
		if track.Delivery == MultimodalDeliveryLatest {
			c.droppedLatest[trackID] = true
			c.stateMu.Unlock()
			return false, nil
		}
		changed := c.creditChanged
		c.stateMu.Unlock()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-changed:
		}
	}
}

func (c *MultimodalRealtimeClient) applyInputCredit(credit MultimodalFlowCredit) error {
	if credit.Bytes <= 0 || credit.Frames <= 0 {
		return ErrMultimodalFlowControl
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.started || c.inputBytes > c.inputLimits.MaxInFlightBytes-credit.Bytes || c.inputFrames > c.inputLimits.MaxInFlightFrames-credit.Frames {
		return ErrMultimodalFlowControl
	}
	c.inputBytes += credit.Bytes
	c.inputFrames += credit.Frames
	c.signalCreditChangedLocked()
	return nil
}

func (c *MultimodalRealtimeClient) signalCreditChangedLocked() {
	close(c.creditChanged)
	c.creditChanged = make(chan struct{})
}

func (c *MultimodalRealtimeClient) writeJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.write(ctx, websocket.MessageText, payload)
}

func (c *MultimodalRealtimeClient) write(ctx context.Context, messageType websocket.MessageType, payload []byte) error {
	if c == nil || c.connection == nil {
		return errors.New("tma: multimodal realtime client is not connected")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.connection.Write(ctx, messageType, payload)
}

func EncodeMultimodalMediaFrame(frame MultimodalMediaFrame) ([]byte, error) {
	if err := validateMultimodalMediaFrame(frame); err != nil {
		return nil, err
	}
	kind, _ := multimodalMediaKindCode(frame.Kind)
	encoded := make([]byte, MultimodalMediaHeaderBytes+len(frame.TrackID)+len(frame.Payload))
	copy(encoded[0:4], "TMAM")
	encoded[4] = 1
	encoded[5] = kind
	binary.BigEndian.PutUint16(encoded[6:8], uint16(frame.Flags))
	binary.BigEndian.PutUint64(encoded[8:16], frame.Sequence)
	binary.BigEndian.PutUint64(encoded[16:24], uint64(frame.TimestampMicros))
	binary.BigEndian.PutUint16(encoded[24:26], uint16(len(frame.TrackID)))
	copy(encoded[MultimodalMediaHeaderBytes:], frame.TrackID)
	copy(encoded[MultimodalMediaHeaderBytes+len(frame.TrackID):], frame.Payload)
	return encoded, nil
}

func DecodeMultimodalMediaFrame(encoded []byte) (MultimodalMediaFrame, error) {
	if len(encoded) < MultimodalMediaHeaderBytes || string(encoded[0:4]) != "TMAM" || encoded[4] != 1 || encoded[26] != 0 || encoded[27] != 0 {
		return MultimodalMediaFrame{}, ErrMultimodalInvalidFrame
	}
	kind, err := multimodalMediaKindName(encoded[5])
	if err != nil {
		return MultimodalMediaFrame{}, err
	}
	trackLength := int(binary.BigEndian.Uint16(encoded[24:26]))
	if trackLength < 1 || trackLength > MultimodalMaxTrackIDBytes || len(encoded) <= MultimodalMediaHeaderBytes+trackLength {
		return MultimodalMediaFrame{}, ErrMultimodalInvalidFrame
	}
	frame := MultimodalMediaFrame{
		Kind: kind, Flags: MultimodalMediaFlags(binary.BigEndian.Uint16(encoded[6:8])),
		Sequence: binary.BigEndian.Uint64(encoded[8:16]), TimestampMicros: int64(binary.BigEndian.Uint64(encoded[16:24])),
		TrackID: string(encoded[MultimodalMediaHeaderBytes : MultimodalMediaHeaderBytes+trackLength]),
		Payload: append([]byte(nil), encoded[MultimodalMediaHeaderBytes+trackLength:]...),
	}
	if err := validateMultimodalMediaFrame(frame); err != nil {
		return MultimodalMediaFrame{}, err
	}
	return frame, nil
}

func validateMultimodalMediaFrame(frame MultimodalMediaFrame) error {
	if _, err := multimodalMediaKindCode(frame.Kind); err != nil || frame.Sequence == 0 || frame.TimestampMicros < 0 ||
		len(frame.TrackID) < 1 || len(frame.TrackID) > MultimodalMaxTrackIDBytes || len(frame.Payload) < 1 || len(frame.Payload) > MultimodalMaxFrameBytes ||
		frame.Flags&^multimodalMediaAllowedFlags != 0 {
		return ErrMultimodalInvalidFrame
	}
	for _, char := range frame.TrackID {
		if char < 0x21 || char > 0x7e {
			return ErrMultimodalInvalidFrame
		}
	}
	return nil
}

func multimodalMediaKindCode(kind string) (byte, error) {
	switch kind {
	case MultimodalMediaAudio:
		return 1, nil
	case MultimodalMediaImage:
		return 2, nil
	case MultimodalMediaVideo:
		return 3, nil
	default:
		return 0, ErrMultimodalInvalidFrame
	}
}

func multimodalMediaKindName(code byte) (string, error) {
	switch code {
	case 1:
		return MultimodalMediaAudio, nil
	case 2:
		return MultimodalMediaImage, nil
	case 3:
		return MultimodalMediaVideo, nil
	default:
		return "", ErrMultimodalInvalidFrame
	}
}

func validateMultimodalSessionStart(start multimodalSessionStartWire) error {
	if strings.TrimSpace(start.ProviderID) == "" || strings.TrimSpace(start.Model) == "" || len(start.InputTracks) == 0 || len(start.InputTracks) > 8 || len(start.OutputModalities) == 0 {
		return errors.New("tma: multimodal session requires provider, model, input tracks, and output modalities")
	}
	seen := map[string]bool{}
	for _, track := range start.InputTracks {
		if seen[track.ID] || strings.TrimSpace(track.ID) == "" || (track.Delivery != MultimodalDeliveryReliable && track.Delivery != MultimodalDeliveryLatest) ||
			(track.Delivery == MultimodalDeliveryLatest && track.Kind != MultimodalMediaVideo) {
			return errors.New("tma: multimodal session contains an invalid or duplicate input track")
		}
		seen[track.ID] = true
	}
	binaryOutput := false
	for _, modality := range start.OutputModalities {
		if modality != "text" {
			binaryOutput = true
		}
	}
	if binaryOutput {
		if start.OutputFlowLimits == nil || start.InitialOutputCredit == nil || !validMultimodalFlowLimits(*start.OutputFlowLimits) ||
			start.InitialOutputCredit.Bytes < 1 || start.InitialOutputCredit.Frames < 1 ||
			start.InitialOutputCredit.Bytes > start.OutputFlowLimits.MaxInFlightBytes || start.InitialOutputCredit.Frames > start.OutputFlowLimits.MaxInFlightFrames {
			return errors.New("tma: binary multimodal output requires valid flow limits and initial credit")
		}
	}
	return nil
}

func validateMultimodalSessionStarted(started MultimodalSessionStarted, start multimodalSessionStartWire) error {
	if started.Type != "session.started" || started.ProtocolVersion != MultimodalRealtimeProtocol || strings.TrimSpace(started.SessionID) == "" {
		return errors.New("tma: invalid multimodal session.started event")
	}
	if start.SessionID != "" && started.SessionID != start.SessionID {
		return errors.New("tma: multimodal session ID does not match")
	}
	limits := started.InputFlowLimits
	credit := started.InitialInputCredit
	if !validMultimodalFlowLimits(limits) || credit.Bytes < 1 || credit.Frames < 1 ||
		credit.Bytes > limits.MaxInFlightBytes || credit.Frames > limits.MaxInFlightFrames {
		return ErrMultimodalFlowControl
	}
	return nil
}

func validMultimodalFlowLimits(limits MultimodalFlowLimits) bool {
	return limits.MaxFrameBytes >= 1 && limits.MaxFrameBytes <= MultimodalMaxFrameBytes &&
		limits.MaxInFlightBytes >= 1 && limits.MaxInFlightBytes <= MultimodalMaxInFlightBytes &&
		limits.MaxInFlightFrames >= 1 && limits.MaxInFlightFrames <= MultimodalMaxInFlightFrames
}

func multimodalEventError(event MultimodalRealtimeEvent) error {
	return &MultimodalRealtimeError{
		Code: event.Code, Message: event.Message, Retryable: event.Retryable,
		RetryAfterSeconds: event.RetryAfterSeconds, LimitScope: event.LimitScope,
	}
}
