package modelruntimeprovider

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	MultimodalRealtimeProtocolVersion = "tma.multimodal.realtime.v1"
	MultimodalRealtimeSubprotocol     = "tma.multimodal.realtime.v1"
	MultimodalMediaHeaderBytes        = 28
	MultimodalMaxTrackIDBytes         = 64
	MultimodalMaxTracks               = 8
	MultimodalMaxFrameBytes           = 4 << 20
	MultimodalMaxInFlightBytes        = 16 << 20
	MultimodalMaxInFlightFrames       = 8
)

const (
	MultimodalMediaAudio = "audio"
	MultimodalMediaImage = "image"
	MultimodalMediaVideo = "video"

	MultimodalDeliveryReliable = "reliable"
	MultimodalDeliveryLatest   = "latest"
)

type MultimodalMediaFlags uint16

const (
	MultimodalMediaFlagKeyFrame      MultimodalMediaFlags = 1 << 0
	MultimodalMediaFlagEndOfTrack    MultimodalMediaFlags = 1 << 1
	MultimodalMediaFlagDiscontinuity MultimodalMediaFlags = 1 << 2
	multimodalMediaAllowedFlags                           = MultimodalMediaFlagKeyFrame | MultimodalMediaFlagEndOfTrack | MultimodalMediaFlagDiscontinuity
)

var (
	ErrMultimodalInvalidFrame        = errors.New("invalid multimodal media frame")
	ErrMultimodalFlowControl         = errors.New("multimodal flow control violation")
	ErrMultimodalSequence            = errors.New("multimodal media sequence violation")
	ErrMultimodalUnsupportedProtocol = errors.New("unsupported multimodal realtime protocol")
)

type MultimodalSessionStart struct {
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

type MultimodalFlowCredit struct {
	Type                 string `json:"type,omitempty"`
	Bytes                int64  `json:"bytes"`
	Frames               int64  `json:"frames"`
	TrackID              string `json:"track_id,omitempty"`
	AcknowledgedSequence uint64 `json:"acknowledged_sequence,omitempty"`
}

type MultimodalObjectRefInput struct {
	Type            string `json:"type"`
	TrackID         string `json:"track_id"`
	Sequence        uint64 `json:"sequence"`
	TimestampMicros int64  `json:"timestamp_us"`
	ObjectRefID     string `json:"object_ref_id"`
	ContentType     string `json:"content_type"`
	SizeBytes       int64  `json:"size_bytes"`
	ChecksumSHA256  string `json:"checksum_sha256,omitempty"`
}

type MultimodalMediaFrame struct {
	Kind            string
	Flags           MultimodalMediaFlags
	Sequence        uint64
	TimestampMicros int64
	TrackID         string
	Payload         []byte
}

func DefaultMultimodalFlowLimits() MultimodalFlowLimits {
	return MultimodalFlowLimits{
		MaxFrameBytes: MultimodalMaxFrameBytes, MaxInFlightBytes: MultimodalMaxInFlightBytes,
		MaxInFlightFrames: MultimodalMaxInFlightFrames,
	}
}

func (s MultimodalSessionStart) Validate() error {
	if s.Type != "session.start" {
		return fmt.Errorf("%w: first event must be session.start", ErrMultimodalUnsupportedProtocol)
	}
	if s.ProtocolVersion != MultimodalRealtimeProtocolVersion {
		return fmt.Errorf("%w: protocol_version must be %q", ErrMultimodalUnsupportedProtocol, MultimodalRealtimeProtocolVersion)
	}
	if strings.TrimSpace(s.ProviderID) == "" || strings.TrimSpace(s.Model) == "" {
		return errors.New("multimodal session provider_id and model are required")
	}
	if len(s.InputTracks) == 0 || len(s.InputTracks) > MultimodalMaxTracks {
		return fmt.Errorf("multimodal session input_tracks must contain between 1 and %d items", MultimodalMaxTracks)
	}
	trackIDs := make(map[string]struct{}, len(s.InputTracks))
	for index, track := range s.InputTracks {
		if err := track.Validate(); err != nil {
			return fmt.Errorf("input_tracks[%d]: %w", index, err)
		}
		if _, exists := trackIDs[track.ID]; exists {
			return fmt.Errorf("input_tracks[%d]: duplicate track id %q", index, track.ID)
		}
		trackIDs[track.ID] = struct{}{}
	}
	if len(s.OutputModalities) == 0 {
		return errors.New("multimodal session output_modalities must not be empty")
	}
	modalities := make(map[string]struct{}, len(s.OutputModalities))
	binaryOutput := false
	for index, modality := range s.OutputModalities {
		modality = strings.TrimSpace(modality)
		switch modality {
		case "text", MultimodalMediaAudio, MultimodalMediaImage, MultimodalMediaVideo:
		default:
			return fmt.Errorf("output_modalities[%d] is unsupported", index)
		}
		if _, exists := modalities[modality]; exists {
			return fmt.Errorf("output_modalities[%d] duplicates %q", index, modality)
		}
		modalities[modality] = struct{}{}
		if modality != "text" {
			binaryOutput = true
		}
	}
	if binaryOutput {
		if s.OutputFlowLimits == nil || s.InitialOutputCredit == nil {
			return errors.New("binary output modalities require output_flow_limits and initial_output_credit")
		}
		if err := validateMultimodalFlowLimits(*s.OutputFlowLimits); err != nil {
			return fmt.Errorf("output_flow_limits: %w", err)
		}
		if s.InitialOutputCredit.Bytes <= 0 || s.InitialOutputCredit.Frames <= 0 ||
			s.InitialOutputCredit.Bytes > s.OutputFlowLimits.MaxInFlightBytes || s.InitialOutputCredit.Frames > s.OutputFlowLimits.MaxInFlightFrames {
			return fmt.Errorf("%w: initial_output_credit must fit the output flow limits", ErrMultimodalFlowControl)
		}
	}
	if s.BackpressureTimeoutMS != 0 && (s.BackpressureTimeoutMS < 100 || s.BackpressureTimeoutMS > 60000) {
		return errors.New("backpressure_timeout_ms must be between 100 and 60000 when specified")
	}
	return nil
}

func (s MultimodalSessionStarted) Validate(start MultimodalSessionStart) error {
	if s.Type != "session.started" || s.ProtocolVersion != MultimodalRealtimeProtocolVersion || strings.TrimSpace(s.SessionID) == "" {
		return fmt.Errorf("%w: invalid session.started event", ErrMultimodalUnsupportedProtocol)
	}
	if expected := strings.TrimSpace(start.SessionID); expected != "" && s.SessionID != expected {
		return errors.New("session.started session_id does not match session.start")
	}
	if err := validateMultimodalFlowLimits(s.InputFlowLimits); err != nil {
		return fmt.Errorf("input_flow_limits: %w", err)
	}
	if s.InitialInputCredit.Bytes <= 0 || s.InitialInputCredit.Frames <= 0 ||
		s.InitialInputCredit.Bytes > s.InputFlowLimits.MaxInFlightBytes || s.InitialInputCredit.Frames > s.InputFlowLimits.MaxInFlightFrames {
		return fmt.Errorf("%w: initial_input_credit must fit the input flow limits", ErrMultimodalFlowControl)
	}
	requestedOutputs := make(map[string]struct{}, len(start.OutputModalities))
	binaryOutput := false
	for _, modality := range start.OutputModalities {
		requestedOutputs[modality] = struct{}{}
		if modality != "text" {
			binaryOutput = true
		}
	}
	if !binaryOutput {
		if len(s.OutputTracks) != 0 {
			return errors.New("text-only sessions must not declare output_tracks")
		}
		return nil
	}
	if start.OutputFlowLimits == nil || start.InitialOutputCredit == nil || s.OutputFlowLimits == nil || s.InitialOutputCredit == nil {
		return errors.New("binary output negotiation requires output limits and credit")
	}
	if err := validateMultimodalFlowLimits(*s.OutputFlowLimits); err != nil {
		return fmt.Errorf("output_flow_limits: %w", err)
	}
	if s.OutputFlowLimits.MaxFrameBytes > start.OutputFlowLimits.MaxFrameBytes ||
		s.OutputFlowLimits.MaxInFlightBytes > start.OutputFlowLimits.MaxInFlightBytes ||
		s.OutputFlowLimits.MaxInFlightFrames > start.OutputFlowLimits.MaxInFlightFrames {
		return fmt.Errorf("%w: negotiated output limits exceed the client offer", ErrMultimodalFlowControl)
	}
	if s.InitialOutputCredit.Bytes <= 0 || s.InitialOutputCredit.Frames <= 0 ||
		s.InitialOutputCredit.Bytes > s.OutputFlowLimits.MaxInFlightBytes || s.InitialOutputCredit.Frames > s.OutputFlowLimits.MaxInFlightFrames ||
		s.InitialOutputCredit.Bytes > start.InitialOutputCredit.Bytes || s.InitialOutputCredit.Frames > start.InitialOutputCredit.Frames {
		return fmt.Errorf("%w: negotiated output credit exceeds the client offer", ErrMultimodalFlowControl)
	}
	if len(s.OutputTracks) == 0 || len(s.OutputTracks) > MultimodalMaxTracks {
		return fmt.Errorf("session.started output_tracks must contain between 1 and %d items", MultimodalMaxTracks)
	}
	trackIDs := make(map[string]struct{}, len(s.OutputTracks))
	for index, track := range s.OutputTracks {
		if err := track.Validate(); err != nil {
			return fmt.Errorf("output_tracks[%d]: %w", index, err)
		}
		if _, allowed := requestedOutputs[track.Kind]; !allowed {
			return fmt.Errorf("output_tracks[%d] kind %q was not requested", index, track.Kind)
		}
		if _, exists := trackIDs[track.ID]; exists {
			return fmt.Errorf("output_tracks[%d]: duplicate track id %q", index, track.ID)
		}
		trackIDs[track.ID] = struct{}{}
	}
	return nil
}

func (t MultimodalTrack) Validate() error {
	if !validMultimodalTrackID(t.ID) {
		return fmt.Errorf("track id must contain 1 to %d ASCII letters, digits, dot, underscore, or hyphen", MultimodalMaxTrackIDBytes)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(t.ContentType, ";")[0]))
	if strings.TrimSpace(t.Codec) == "" || contentType == "" {
		return errors.New("track content_type and codec are required")
	}
	switch t.Kind {
	case MultimodalMediaAudio:
		if t.Delivery != MultimodalDeliveryReliable {
			return errors.New("audio tracks require reliable delivery")
		}
		if !strings.HasPrefix(contentType, "audio/") || t.SampleRateHz < 8000 || t.SampleRateHz > 192000 || t.Channels < 1 || t.Channels > 8 {
			return errors.New("audio tracks require audio content_type, sample_rate_hz 8000..192000, and channels 1..8")
		}
		if t.Width != 0 || t.Height != 0 || t.MaxFPS != 0 {
			return errors.New("audio tracks must not declare video dimensions")
		}
	case MultimodalMediaImage:
		if t.Delivery != MultimodalDeliveryReliable || !strings.HasPrefix(contentType, "image/") {
			return errors.New("image tracks require reliable delivery and image content_type")
		}
		if !validMultimodalDimensions(t.Width, t.Height, false) || t.MaxFPS != 0 || t.SampleRateHz != 0 || t.Channels != 0 {
			return errors.New("image track dimensions must be both omitted or between 1 and 8192")
		}
	case MultimodalMediaVideo:
		if t.Delivery != MultimodalDeliveryReliable && t.Delivery != MultimodalDeliveryLatest {
			return errors.New("video tracks require reliable or latest delivery")
		}
		if !strings.HasPrefix(contentType, "video/") && !strings.HasPrefix(contentType, "image/") {
			return errors.New("video tracks require video or image frame content_type")
		}
		if !validMultimodalDimensions(t.Width, t.Height, true) || t.MaxFPS < 1 || t.MaxFPS > 120 || t.SampleRateHz != 0 || t.Channels != 0 {
			return errors.New("video tracks require width/height 1..8192 and max_fps 1..120")
		}
	default:
		return errors.New("track kind must be audio, image, or video")
	}
	return nil
}

func (o MultimodalObjectRefInput) Validate(start MultimodalSessionStart) error {
	if o.Type != "input.object_ref" || o.Sequence == 0 || o.TimestampMicros < 0 || strings.TrimSpace(o.ObjectRefID) == "" {
		return errors.New("object ref input requires type, track_id, positive sequence, non-negative timestamp_us, and object_ref_id")
	}
	if o.SizeBytes <= 0 || o.SizeBytes > MultimodalMaxFrameBytes {
		return fmt.Errorf("object ref input size_bytes must be between 1 and %d", MultimodalMaxFrameBytes)
	}
	if o.ChecksumSHA256 != "" && !validMultimodalSHA256(o.ChecksumSHA256) {
		return errors.New("object ref input checksum_sha256 must contain 64 hexadecimal characters")
	}
	for _, track := range start.InputTracks {
		if track.ID != o.TrackID {
			continue
		}
		if track.Kind == MultimodalMediaAudio {
			return errors.New("object ref input is not supported for audio tracks")
		}
		if normalizedContentType(o.ContentType) != normalizedContentType(track.ContentType) {
			return errors.New("object ref input content_type does not match its track")
		}
		return nil
	}
	return errors.New("object ref input references an undeclared track")
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
		return MultimodalMediaFrame{}, fmt.Errorf("%w: invalid header", ErrMultimodalInvalidFrame)
	}
	kind, err := multimodalMediaKindName(encoded[5])
	if err != nil {
		return MultimodalMediaFrame{}, err
	}
	trackLength := int(binary.BigEndian.Uint16(encoded[24:26]))
	if trackLength == 0 || trackLength > MultimodalMaxTrackIDBytes || len(encoded) <= MultimodalMediaHeaderBytes+trackLength {
		return MultimodalMediaFrame{}, fmt.Errorf("%w: invalid track or empty payload", ErrMultimodalInvalidFrame)
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
	if _, err := multimodalMediaKindCode(frame.Kind); err != nil {
		return err
	}
	if !validMultimodalTrackID(frame.TrackID) || frame.Sequence == 0 || frame.TimestampMicros < 0 {
		return fmt.Errorf("%w: track, sequence, or timestamp is invalid", ErrMultimodalInvalidFrame)
	}
	if len(frame.Payload) == 0 || len(frame.Payload) > MultimodalMaxFrameBytes {
		return fmt.Errorf("%w: payload must contain between 1 and %d bytes", ErrMultimodalInvalidFrame, MultimodalMaxFrameBytes)
	}
	if frame.Flags&^multimodalMediaAllowedFlags != 0 {
		return fmt.Errorf("%w: unsupported flags", ErrMultimodalInvalidFrame)
	}
	if frame.Flags&MultimodalMediaFlagKeyFrame != 0 && frame.Kind == MultimodalMediaAudio {
		return fmt.Errorf("%w: audio frames cannot be key frames", ErrMultimodalInvalidFrame)
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
		return 0, fmt.Errorf("%w: unsupported media kind", ErrMultimodalInvalidFrame)
	}
}

func multimodalMediaKindName(kind byte) (string, error) {
	switch kind {
	case 1:
		return MultimodalMediaAudio, nil
	case 2:
		return MultimodalMediaImage, nil
	case 3:
		return MultimodalMediaVideo, nil
	default:
		return "", fmt.Errorf("%w: unsupported media kind", ErrMultimodalInvalidFrame)
	}
}

func validMultimodalTrackID(value string) bool {
	if len(value) == 0 || len(value) > MultimodalMaxTrackIDBytes {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validMultimodalDimensions(width, height int, required bool) bool {
	if width == 0 && height == 0 {
		return !required
	}
	return width >= 1 && width <= 8192 && height >= 1 && height <= 8192
}

func normalizedContentType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}

func validMultimodalSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
			continue
		}
		return false
	}
	return true
}
