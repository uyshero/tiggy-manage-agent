package managedagents

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	LLMEmbeddingDistanceCosine       = "cosine"
	LLMEmbeddingDistanceL2           = "l2"
	LLMEmbeddingDistanceInnerProduct = "inner_product"
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
	capabilities.DistanceMetric = strings.ToLower(strings.TrimSpace(capabilities.DistanceMetric))

	switch capabilityType {
	case LLMModelCapabilityEmbedding:
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
	default:
		if capabilities != (LLMModelCapabilities{}) {
			return LLMModelCapabilities{}, fmt.Errorf("%w: capabilities are only supported for embedding and reranker models", ErrInvalid)
		}
	}
	return capabilities, nil
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
