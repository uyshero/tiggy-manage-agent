package managedagents

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const (
	LLMEmbeddingDistanceCosine                = "cosine"
	LLMEmbeddingDistanceL2                    = "l2"
	LLMEmbeddingDistanceInnerProduct          = "inner_product"
	LLMMultimodalRealtimeProtocolTMAWebSocket = "tma_multimodal_websocket_v1"
	LLMMultimodalRealtimeProtocolOpenAI       = "openai_realtime_websocket"
	LLMMultimodalRealtimeMaxTracks            = 8
	LLMMultimodalRealtimeMaxFrameBytes        = int64(4 << 20)
)

type llmModelDefaults struct {
	Vision    bool
	Embedding bool
	Reranker  bool
}

// NormalizeLLMModelInput applies the same capability and default-model rules
// used by the PostgreSQL control plane. Stores that do not use PostgreSQL use
// this helper to keep API behavior consistent.
func NormalizeLLMModelInput(input UpsertLLMModelInput, existing *LLMModel) (UpsertLLMModelInput, error) {
	normalized, defaults, err := normalizeLLMModelMutationInput(input, existing)
	if err != nil {
		return UpsertLLMModelInput{}, err
	}
	normalized.IsDefaultVision = boolPointer(defaults.Vision)
	normalized.IsDefaultEmbedding = boolPointer(defaults.Embedding)
	normalized.IsDefaultReranker = boolPointer(defaults.Reranker)
	return normalized, nil
}

func boolPointer(value bool) *bool {
	return &value
}

func normalizeLLMModelCapabilities(capabilityType string, requested *LLMModelCapabilities, existing *LLMModel) (LLMModelCapabilities, error) {
	capabilities := LLMModelCapabilities{}
	if requested != nil {
		capabilities = *requested
	} else if existing != nil && existing.CapabilityType == capabilityType {
		capabilities = existing.Capabilities
	}
	capabilities.Protocol = strings.TrimSpace(capabilities.Protocol)
	capabilities.ResourceID = strings.TrimSpace(capabilities.ResourceID)
	capabilities.DefaultVoice = strings.TrimSpace(capabilities.DefaultVoice)
	capabilities.AudioFormat = strings.TrimSpace(capabilities.AudioFormat)
	capabilities.UpstreamModel = strings.TrimSpace(capabilities.UpstreamModel)
	capabilities.DistanceMetric = strings.ToLower(strings.TrimSpace(capabilities.DistanceMetric))

	switch capabilityType {
	case LLMModelCapabilityEmbedding:
		capabilities.Realtime = nil
		if capabilities.Dimensions <= 0 || capabilities.Dimensions > 65535 {
			return LLMModelCapabilities{}, fmt.Errorf("%w: embedding model dimensions must be between 1 and 65535", ErrInvalid)
		}
		if capabilities.DistanceMetric == "" {
			capabilities.DistanceMetric = LLMEmbeddingDistanceCosine
		}
		switch capabilities.DistanceMetric {
		case LLMEmbeddingDistanceCosine, LLMEmbeddingDistanceL2, LLMEmbeddingDistanceInnerProduct:
		default:
			return LLMModelCapabilities{}, fmt.Errorf("%w: unsupported embedding distance_metric %q", ErrInvalid, capabilities.DistanceMetric)
		}
		if capabilities.MaxBatchSize <= 0 {
			capabilities.MaxBatchSize = 32
		}
		if capabilities.MaxBatchSize > 4096 {
			return LLMModelCapabilities{}, fmt.Errorf("%w: embedding model max_batch_size must not exceed 4096", ErrInvalid)
		}
		if capabilities.Protocol == "" {
			return LLMModelCapabilities{}, fmt.Errorf("%w: embedding model protocol is required", ErrInvalid)
		}
		capabilities.MaxCandidates = 0
	case LLMModelCapabilityReranker:
		capabilities.Realtime = nil
		if capabilities.MaxCandidates <= 0 {
			capabilities.MaxCandidates = 50
		}
		if capabilities.MaxCandidates > 1000 {
			return LLMModelCapabilities{}, fmt.Errorf("%w: reranker model max_candidates must not exceed 1000", ErrInvalid)
		}
		if capabilities.Protocol == "" {
			return LLMModelCapabilities{}, fmt.Errorf("%w: reranker model protocol is required", ErrInvalid)
		}
		capabilities.Dimensions = 0
		capabilities.DistanceMetric = ""
		capabilities.Normalized = false
		capabilities.MaxBatchSize = 0
	case LLMModelCapabilitySpeechToText, LLMModelCapabilityTextToSpeech:
		capabilities.Realtime = nil
		if capabilities.Protocol == "" || capabilities.ResourceID == "" {
			return LLMModelCapabilities{}, fmt.Errorf("%w: speech model protocol and resource_id are required", ErrInvalid)
		}
		if capabilities.AudioFormat == "" {
			capabilities.AudioFormat = "pcm_s16le"
		}
		if capabilities.SampleRateHz == 0 {
			capabilities.SampleRateHz = 16000
		}
		if capabilities.SampleRateHz < 8000 || capabilities.SampleRateHz > 48000 {
			return LLMModelCapabilities{}, fmt.Errorf("%w: speech model sample_rate_hz must be between 8000 and 48000", ErrInvalid)
		}
		if capabilityType == LLMModelCapabilityTextToSpeech && capabilities.DefaultVoice == "" {
			return LLMModelCapabilities{}, fmt.Errorf("%w: text-to-speech model default_voice is required", ErrInvalid)
		}
		capabilities.Dimensions = 0
		capabilities.DistanceMetric = ""
		capabilities.Normalized = false
		capabilities.MaxBatchSize = 0
		capabilities.MaxCandidates = 0
	case LLMModelCapabilityMultimodalRealtime:
		switch capabilities.Protocol {
		case LLMMultimodalRealtimeProtocolTMAWebSocket, LLMMultimodalRealtimeProtocolOpenAI:
		default:
			return LLMModelCapabilities{}, fmt.Errorf("%w: unsupported multimodal realtime protocol %q", ErrInvalid, capabilities.Protocol)
		}
		realtime, err := normalizeLLMRealtimeCapabilities(capabilities.Realtime)
		if err != nil {
			return LLMModelCapabilities{}, err
		}
		if capabilities.Protocol == LLMMultimodalRealtimeProtocolOpenAI {
			if err := validateOpenAIRealtimeCapabilities(realtime); err != nil {
				return LLMModelCapabilities{}, err
			}
		}
		capabilities.Realtime = &realtime
		capabilities.Dimensions = 0
		capabilities.DistanceMetric = ""
		capabilities.Normalized = false
		capabilities.MaxBatchSize = 0
		capabilities.MaxCandidates = 0
		capabilities.ResourceID = ""
		capabilities.DefaultVoice = ""
		capabilities.AudioFormat = ""
		capabilities.SampleRateHz = 0
	default:
		if !reflect.DeepEqual(capabilities, LLMModelCapabilities{}) {
			return LLMModelCapabilities{}, fmt.Errorf("%w: capabilities are not supported for this model type", ErrInvalid)
		}
	}
	return capabilities, nil
}

func validateOpenAIRealtimeCapabilities(realtime LLMRealtimeCapabilities) error {
	for _, format := range realtime.InputFormats {
		if !openAIRealtimeInputFormat(format) {
			return fmt.Errorf("%w: OpenAI realtime input format %s/%s/%s is unsupported", ErrInvalid, format.Kind, format.ContentType, format.Codec)
		}
	}
	for _, modality := range realtime.OutputModalities {
		if modality != "text" && modality != "audio" {
			return fmt.Errorf("%w: OpenAI realtime output modality %q is unsupported", ErrInvalid, modality)
		}
	}
	for _, format := range realtime.OutputFormats {
		if format.Kind != "audio" || format.ContentType != "audio/pcm" || format.Codec != "pcm_s16le" {
			return fmt.Errorf("%w: OpenAI realtime only supports PCM16 audio output", ErrInvalid)
		}
	}
	return nil
}

func openAIRealtimeInputFormat(format LLMRealtimeMediaFormat) bool {
	if format.Kind == "audio" {
		return format.ContentType == "audio/pcm" && format.Codec == "pcm_s16le"
	}
	if format.Kind != "image" {
		return false
	}
	return (format.ContentType == "image/jpeg" && format.Codec == "jpeg") ||
		(format.ContentType == "image/png" && format.Codec == "png")
}

func normalizeLLMRealtimeCapabilities(requested *LLMRealtimeCapabilities) (LLMRealtimeCapabilities, error) {
	if requested == nil {
		return LLMRealtimeCapabilities{}, fmt.Errorf("%w: multimodal realtime capabilities are required", ErrInvalid)
	}
	realtime := *requested
	realtime.OutputModalities = append([]string(nil), requested.OutputModalities...)
	if realtime.MaxInputTracks == 0 {
		realtime.MaxInputTracks = LLMMultimodalRealtimeMaxTracks
	}
	if realtime.MaxInputTracks < 1 || realtime.MaxInputTracks > LLMMultimodalRealtimeMaxTracks {
		return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime max_input_tracks must be between 1 and %d", ErrInvalid, LLMMultimodalRealtimeMaxTracks)
	}
	if realtime.MaxFrameBytes == 0 {
		realtime.MaxFrameBytes = LLMMultimodalRealtimeMaxFrameBytes
	}
	if realtime.MaxFrameBytes < 1 || realtime.MaxFrameBytes > LLMMultimodalRealtimeMaxFrameBytes {
		return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime max_frame_bytes must be between 1 and %d", ErrInvalid, LLMMultimodalRealtimeMaxFrameBytes)
	}
	var err error
	realtime.InputFormats, err = normalizeLLMRealtimeFormats("input_formats", realtime.InputFormats, true)
	if err != nil {
		return LLMRealtimeCapabilities{}, err
	}
	realtime.OutputFormats, err = normalizeLLMRealtimeFormats("output_formats", realtime.OutputFormats, false)
	if err != nil {
		return LLMRealtimeCapabilities{}, err
	}
	modalities := make(map[string]bool, len(realtime.OutputModalities))
	for index, value := range realtime.OutputModalities {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "text", "audio", "image", "video":
		default:
			return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime output_modalities[%d] is unsupported", ErrInvalid, index)
		}
		if modalities[value] {
			return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime output_modalities duplicates %q", ErrInvalid, value)
		}
		modalities[value] = true
		realtime.OutputModalities[index] = value
	}
	if len(modalities) == 0 {
		return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime output_modalities must not be empty", ErrInvalid)
	}
	formatKinds := make(map[string]bool, len(realtime.OutputFormats))
	for _, format := range realtime.OutputFormats {
		if !modalities[format.Kind] {
			return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime output format kind %q is not an output modality", ErrInvalid, format.Kind)
		}
		formatKinds[format.Kind] = true
	}
	for modality := range modalities {
		if modality != "text" && !formatKinds[modality] {
			return LLMRealtimeCapabilities{}, fmt.Errorf("%w: realtime output modality %q requires an output format", ErrInvalid, modality)
		}
	}
	sort.Strings(realtime.OutputModalities)
	return realtime, nil
}

func normalizeLLMRealtimeFormats(field string, requested []LLMRealtimeMediaFormat, required bool) ([]LLMRealtimeMediaFormat, error) {
	if required && len(requested) == 0 {
		return nil, fmt.Errorf("%w: realtime %s must not be empty", ErrInvalid, field)
	}
	if len(requested) > 32 {
		return nil, fmt.Errorf("%w: realtime %s must not contain more than 32 formats", ErrInvalid, field)
	}
	formats := append([]LLMRealtimeMediaFormat(nil), requested...)
	seen := make(map[string]bool, len(formats))
	for index := range formats {
		format := &formats[index]
		format.Kind = strings.ToLower(strings.TrimSpace(format.Kind))
		format.ContentType = strings.ToLower(strings.TrimSpace(strings.Split(format.ContentType, ";")[0]))
		format.Codec = strings.ToLower(strings.TrimSpace(format.Codec))
		if !validLLMRealtimeFormat(*format) {
			return nil, fmt.Errorf("%w: realtime %s[%d] has an invalid kind, content_type, or codec", ErrInvalid, field, index)
		}
		key := format.Kind + "\x00" + format.ContentType + "\x00" + format.Codec
		if seen[key] {
			return nil, fmt.Errorf("%w: realtime %s contains a duplicate format", ErrInvalid, field)
		}
		seen[key] = true
	}
	sort.Slice(formats, func(i, j int) bool {
		left, right := formats[i], formats[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.ContentType != right.ContentType {
			return left.ContentType < right.ContentType
		}
		return left.Codec < right.Codec
	})
	return formats, nil
}

func validLLMRealtimeFormat(format LLMRealtimeMediaFormat) bool {
	contentTypeValid := false
	switch format.Kind {
	case "audio":
		contentTypeValid = strings.HasPrefix(format.ContentType, "audio/")
	case "image":
		contentTypeValid = strings.HasPrefix(format.ContentType, "image/")
	case "video":
		contentTypeValid = strings.HasPrefix(format.ContentType, "video/") || strings.HasPrefix(format.ContentType, "image/")
	default:
		return false
	}
	return contentTypeValid && validLLMRealtimeToken(format.Codec)
}

func validLLMRealtimeToken(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '+' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeLLMModelDefaults(input UpsertLLMModelInput, capabilityType string, existing *LLMModel) (llmModelDefaults, error) {
	defaults := llmModelDefaults{}
	if existing != nil {
		defaults = llmModelDefaults{
			Vision: existing.IsDefaultVision, Embedding: existing.IsDefaultEmbedding, Reranker: existing.IsDefaultReranker,
		}
	}
	if input.IsDefaultVision != nil {
		defaults.Vision = *input.IsDefaultVision
	}
	if input.IsDefaultEmbedding != nil {
		defaults.Embedding = *input.IsDefaultEmbedding
	}
	if input.IsDefaultReranker != nil {
		defaults.Reranker = *input.IsDefaultReranker
	}

	if capabilityType != LLMModelCapabilityTextImage {
		defaults.Vision = false
	}
	if capabilityType != LLMModelCapabilityEmbedding {
		defaults.Embedding = false
	}
	if capabilityType != LLMModelCapabilityReranker {
		defaults.Reranker = false
	}
	if input.IsDefaultVision != nil && *input.IsDefaultVision && !LLMModelSupportsVision(capabilityType) {
		return llmModelDefaults{}, fmt.Errorf("%w: default vision model must use capability_type %s", ErrInvalid, LLMModelCapabilityTextImage)
	}
	if input.IsDefaultEmbedding != nil && *input.IsDefaultEmbedding && capabilityType != LLMModelCapabilityEmbedding {
		return llmModelDefaults{}, fmt.Errorf("%w: default embedding model must use capability_type %s", ErrInvalid, LLMModelCapabilityEmbedding)
	}
	if input.IsDefaultReranker != nil && *input.IsDefaultReranker && capabilityType != LLMModelCapabilityReranker {
		return llmModelDefaults{}, fmt.Errorf("%w: default reranker model must use capability_type %s", ErrInvalid, LLMModelCapabilityReranker)
	}
	return defaults, nil
}

func llmModelCapabilitiesJSON(capabilities LLMModelCapabilities) ([]byte, error) {
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		return nil, fmt.Errorf("encode llm model capabilities: %w", err)
	}
	return encoded, nil
}

func scanLLMModelCapabilities(raw []byte, target *LLMModel) error {
	if len(raw) == 0 {
		target.Capabilities = LLMModelCapabilities{}
		return nil
	}
	if err := json.Unmarshal(raw, &target.Capabilities); err != nil {
		return fmt.Errorf("decode llm model capabilities: %w", err)
	}
	return nil
}
