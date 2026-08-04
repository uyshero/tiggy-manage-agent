package managedagents

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeLLMModelInputEmbeddingAndReranker(t *testing.T) {
	defaultEmbedding := true
	embedding, err := NormalizeLLMModelInput(UpsertLLMModelInput{
		ProviderID: "local", Model: "bge-m3", ContextWindowTokens: 8192,
		CapabilityType: LLMModelCapabilityEmbedding,
		Capabilities: &LLMModelCapabilities{
			Dimensions: 1024, DistanceMetric: "COSINE", Normalized: true, Protocol: "openai_embeddings",
		},
		IsDefaultEmbedding: &defaultEmbedding,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if embedding.Capabilities.Dimensions != 1024 || embedding.Capabilities.DistanceMetric != LLMEmbeddingDistanceCosine || embedding.Capabilities.MaxBatchSize != 32 {
		t.Fatalf("unexpected normalized embedding capabilities: %+v", embedding.Capabilities)
	}
	if !*embedding.IsDefaultEmbedding || *embedding.IsDefaultVision || *embedding.IsDefaultReranker {
		t.Fatalf("unexpected embedding defaults: vision=%t embedding=%t reranker=%t", *embedding.IsDefaultVision, *embedding.IsDefaultEmbedding, *embedding.IsDefaultReranker)
	}

	defaultReranker := true
	reranker, err := NormalizeLLMModelInput(UpsertLLMModelInput{
		ProviderID: "local", Model: "bge-reranker-v2-m3", ContextWindowTokens: 8192,
		CapabilityType:    LLMModelCapabilityReranker,
		Capabilities:      &LLMModelCapabilities{Protocol: "jina_rerank"},
		IsDefaultReranker: &defaultReranker,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reranker.Capabilities.MaxCandidates != 50 || reranker.Capabilities.Protocol != "jina_rerank" || !*reranker.IsDefaultReranker {
		t.Fatalf("unexpected normalized reranker: %+v defaults=%+v", reranker.Capabilities, reranker)
	}
}

func TestNormalizeLLMModelInputSpeech(t *testing.T) {
	model, err := NormalizeLLMModelInput(UpsertLLMModelInput{
		ProviderID: "doubao-tts", Model: "seed-tts", CapabilityType: LLMModelCapabilityTextToSpeech,
		Capabilities: &LLMModelCapabilities{Protocol: "doubao_bidirectional_tts", ResourceID: "seed-tts-2.0", DefaultVoice: "warm"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.Capabilities.AudioFormat != "pcm_s16le" || model.Capabilities.SampleRateHz != 16000 || model.Capabilities.DefaultVoice != "warm" {
		t.Fatalf("unexpected speech defaults: %+v", model.Capabilities)
	}
}

func TestNormalizeLLMModelInputMultimodalRealtime(t *testing.T) {
	model, err := NormalizeLLMModelInput(UpsertLLMModelInput{
		ProviderID: "realtime", Model: "native", CapabilityType: LLMModelCapabilityMultimodalRealtime,
		Capabilities: &LLMModelCapabilities{
			Protocol: LLMMultimodalRealtimeProtocolTMAWebSocket,
			Realtime: &LLMRealtimeCapabilities{
				InputFormats: []LLMRealtimeMediaFormat{
					{Kind: "VIDEO", ContentType: "video/H264; profile=main", Codec: "H264"},
					{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"},
				},
				OutputModalities: []string{"audio", "text"},
				OutputFormats:    []LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	realtime := model.Capabilities.Realtime
	if realtime == nil || realtime.MaxInputTracks != LLMMultimodalRealtimeMaxTracks || realtime.MaxFrameBytes != LLMMultimodalRealtimeMaxFrameBytes {
		t.Fatalf("unexpected realtime defaults: %+v", realtime)
	}
	if realtime.InputFormats[0].Kind != "audio" || realtime.InputFormats[1].ContentType != "video/h264" || realtime.OutputModalities[0] != "audio" || realtime.OutputModalities[1] != "text" {
		t.Fatalf("unexpected normalized realtime capabilities: %+v", realtime)
	}
}

func TestNormalizeLLMModelInputOpenAIRealtime(t *testing.T) {
	model, err := NormalizeLLMModelInput(UpsertLLMModelInput{
		ProviderID: "openai", Model: "realtime", CapabilityType: LLMModelCapabilityMultimodalRealtime,
		Capabilities: &LLMModelCapabilities{
			Protocol: LLMMultimodalRealtimeProtocolOpenAI, UpstreamModel: "gpt-realtime",
			Realtime: &LLMRealtimeCapabilities{
				InputFormats: []LLMRealtimeMediaFormat{
					{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"},
					{Kind: "image", ContentType: "image/jpeg", Codec: "jpeg"},
					{Kind: "image", ContentType: "image/png", Codec: "png"},
				},
				OutputModalities: []string{"text", "audio"},
				OutputFormats:    []LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.Capabilities.Protocol != LLMMultimodalRealtimeProtocolOpenAI || model.Capabilities.UpstreamModel != "gpt-realtime" || model.Capabilities.Realtime == nil {
		t.Fatalf("unexpected OpenAI realtime capabilities: %+v", model.Capabilities)
	}
}

func TestNormalizeLLMModelInputRejectsUnsupportedOpenAIRealtimeCapabilities(t *testing.T) {
	tests := []LLMRealtimeCapabilities{
		{
			InputFormats:     []LLMRealtimeMediaFormat{{Kind: "video", ContentType: "video/h264", Codec: "h264"}},
			OutputModalities: []string{"text"},
		},
		{
			InputFormats:     []LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/opus", Codec: "opus"}},
			OutputModalities: []string{"text"},
		},
		{
			InputFormats:     []LLMRealtimeMediaFormat{{Kind: "image", ContentType: "image/webp", Codec: "webp"}},
			OutputModalities: []string{"text"},
		},
		{
			InputFormats:     []LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}},
			OutputModalities: []string{"image"},
			OutputFormats:    []LLMRealtimeMediaFormat{{Kind: "image", ContentType: "image/png", Codec: "png"}},
		},
	}
	for _, realtime := range tests {
		capabilities := LLMModelCapabilities{Protocol: LLMMultimodalRealtimeProtocolOpenAI, Realtime: &realtime}
		if _, err := NormalizeLLMModelInput(UpsertLLMModelInput{
			CapabilityType: LLMModelCapabilityMultimodalRealtime, Capabilities: &capabilities,
		}, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid OpenAI realtime capabilities for %+v, got %v", realtime, err)
		}
	}
}

func TestNormalizeLLMModelInputRejectsInvalidMultimodalRealtime(t *testing.T) {
	validRealtime := &LLMRealtimeCapabilities{
		InputFormats:     []LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}},
		OutputModalities: []string{"text"},
	}
	tests := []LLMModelCapabilities{
		{Protocol: "vendor_private", Realtime: validRealtime},
		{Protocol: LLMMultimodalRealtimeProtocolTMAWebSocket},
		{Protocol: LLMMultimodalRealtimeProtocolTMAWebSocket, Realtime: &LLMRealtimeCapabilities{InputFormats: []LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "video/h264", Codec: "h264"}}, OutputModalities: []string{"text"}}},
		{Protocol: LLMMultimodalRealtimeProtocolTMAWebSocket, Realtime: &LLMRealtimeCapabilities{InputFormats: validRealtime.InputFormats, OutputModalities: []string{"audio"}}},
		{Protocol: LLMMultimodalRealtimeProtocolTMAWebSocket, Realtime: &LLMRealtimeCapabilities{InputFormats: validRealtime.InputFormats, OutputModalities: []string{"text"}, MaxFrameBytes: LLMMultimodalRealtimeMaxFrameBytes + 1}},
	}
	for _, capabilities := range tests {
		if _, err := NormalizeLLMModelInput(UpsertLLMModelInput{CapabilityType: LLMModelCapabilityMultimodalRealtime, Capabilities: &capabilities}, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid realtime capabilities for %+v, got %v", capabilities, err)
		}
	}
}

func TestNormalizeLLMModelInputRejectsInvalidCapabilityConfiguration(t *testing.T) {
	trueValue := true
	tests := []UpsertLLMModelInput{
		{CapabilityType: LLMModelCapabilityEmbedding, Capabilities: &LLMModelCapabilities{Protocol: "openai_embeddings"}},
		{CapabilityType: LLMModelCapabilityEmbedding, Capabilities: &LLMModelCapabilities{Dimensions: 1024, DistanceMetric: "manhattan", Protocol: "openai_embeddings"}},
		{CapabilityType: LLMModelCapabilityReranker, Capabilities: &LLMModelCapabilities{}},
		{CapabilityType: LLMModelCapabilityText, Capabilities: &LLMModelCapabilities{Protocol: "openai_embeddings"}},
		{CapabilityType: LLMModelCapabilityText, IsDefaultEmbedding: &trueValue},
	}
	for _, input := range tests {
		if _, err := NormalizeLLMModelInput(input, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid input for %+v, got %v", input, err)
		}
	}
}

func TestNormalizeLLMModelInputClearsIncompatibleDefaultsOnTypeChange(t *testing.T) {
	existing := &LLMModel{
		CapabilityType:     LLMModelCapabilityEmbedding,
		Capabilities:       LLMModelCapabilities{Dimensions: 1024, DistanceMetric: LLMEmbeddingDistanceCosine, Protocol: "openai_embeddings"},
		IsDefaultEmbedding: true,
	}
	normalized, err := NormalizeLLMModelInput(UpsertLLMModelInput{CapabilityType: LLMModelCapabilityText}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if *normalized.IsDefaultEmbedding || !reflect.DeepEqual(*normalized.Capabilities, LLMModelCapabilities{}) {
		t.Fatalf("expected type change to clear embedding-only state: %+v", normalized)
	}
}
