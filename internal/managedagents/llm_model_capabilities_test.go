package managedagents

import (
	"errors"
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
	if *normalized.IsDefaultEmbedding || *normalized.Capabilities != (LLMModelCapabilities{}) {
		t.Fatalf("expected type change to clear embedding-only state: %+v", normalized)
	}
}
