package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxInferenceResponseBytes = 32 << 20

type InferenceConfig struct {
	ProviderType string
	BaseURL      string
	APIKey       string
	Model        string
	Protocol     string
}

type EmbeddingResponse struct {
	Embeddings [][]float64
	Usage      Usage
}

type RerankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

type InferenceService struct {
	Client  *http.Client
	Timeout time.Duration
}

func (s InferenceService) Embed(ctx context.Context, config InferenceConfig, inputs []string) (EmbeddingResponse, error) {
	protocol := strings.TrimSpace(config.Protocol)
	var suffix string
	var body any
	switch protocol {
	case EmbeddingProtocolOpenAI:
		suffix = "/embeddings"
		body = map[string]any{"model": config.Model, "input": inputs}
	case EmbeddingProtocolTEI:
		suffix = "/embed"
		body = map[string]any{"inputs": inputs}
	case EmbeddingProtocolOllama:
		suffix = "/api/embed"
		body = map[string]any{"model": config.Model, "input": inputs}
	default:
		return EmbeddingResponse{}, fmt.Errorf("unsupported embedding protocol %q", protocol)
	}

	responseBody, err := s.postJSON(ctx, config, suffix, body)
	if err != nil {
		return EmbeddingResponse{}, err
	}
	response, err := decodeEmbeddingResponse(protocol, responseBody)
	if err != nil {
		return EmbeddingResponse{}, err
	}
	if len(response.Embeddings) != len(inputs) {
		return EmbeddingResponse{}, invalidInferenceResponse("embedding provider returned a different number of vectors than inputs")
	}
	return response, nil
}

func (s InferenceService) Rerank(ctx context.Context, config InferenceConfig, query string, documents []string, topN int) ([]RerankResult, error) {
	protocol := strings.TrimSpace(config.Protocol)
	var suffix string
	var body any
	switch protocol {
	case RerankProtocolJina, RerankProtocolCohere:
		suffix = "/rerank"
		body = map[string]any{"model": config.Model, "query": query, "documents": documents, "top_n": topN}
	case RerankProtocolVLLM:
		suffix = "/score"
		body = map[string]any{"model": config.Model, "text_1": query, "text_2": documents}
	default:
		return nil, fmt.Errorf("unsupported reranker protocol %q", protocol)
	}

	responseBody, err := s.postJSON(ctx, config, suffix, body)
	if err != nil {
		return nil, err
	}
	results, err := decodeRerankResponse(responseBody, len(documents))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(results, func(left, right int) bool { return results[left].Score > results[right].Score })
	if len(results) > topN {
		results = results[:topN]
	}
	return results, nil
}

func (s InferenceService) postJSON(ctx context.Context, config InferenceConfig, suffix string, body any) ([]byte, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" && isOpenAIProviderType(strings.TrimSpace(config.ProviderType)) {
		baseURL = DefaultOpenAIBaseURL
	}
	endpoint, ok := diagnosticEndpoint(baseURL, suffix)
	if !ok {
		return nil, &ProviderError{Class: ErrorClassInvalidRequest, Message: "provider Base URL is invalid"}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode inference request: %w", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Class: ErrorClassInvalidRequest, Message: "provider Base URL is invalid", Cause: err}
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(config.APIKey); apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := s.client().Do(request)
	if err != nil {
		return nil, classifyProviderTransportError(requestContext, err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxInferenceResponseBytes+1))
	if readErr != nil {
		return nil, &ProviderError{Class: ErrorClassServer, Retryable: true, Message: "could not read provider response", Cause: readErr}
	}
	if len(responseBody) > maxInferenceResponseBytes {
		return nil, invalidInferenceResponse("provider response exceeds the Platform limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, classifyProviderHTTPError(response.StatusCode, responseBody)
	}
	return responseBody, nil
}

func decodeEmbeddingResponse(protocol string, body []byte) (EmbeddingResponse, error) {
	var response EmbeddingResponse
	switch protocol {
	case EmbeddingProtocolOpenAI:
		var decoded struct {
			Data []struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
			Usage struct {
				PromptTokens int64 `json:"prompt_tokens"`
				TotalTokens  int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Data) == 0 {
			return EmbeddingResponse{}, invalidInferenceResponse("embedding provider returned invalid JSON")
		}
		sort.Slice(decoded.Data, func(left, right int) bool { return decoded.Data[left].Index < decoded.Data[right].Index })
		response.Embeddings = make([][]float64, 0, len(decoded.Data))
		for _, item := range decoded.Data {
			response.Embeddings = append(response.Embeddings, item.Embedding)
		}
		response.Usage = Usage{InputTokens: decoded.Usage.PromptTokens, TotalTokens: decoded.Usage.TotalTokens}
	case EmbeddingProtocolTEI:
		if err := json.Unmarshal(body, &response.Embeddings); err != nil {
			return EmbeddingResponse{}, invalidInferenceResponse("embedding provider returned invalid JSON")
		}
	case EmbeddingProtocolOllama:
		var decoded struct {
			Embeddings      [][]float64 `json:"embeddings"`
			PromptEvalCount int64       `json:"prompt_eval_count"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			return EmbeddingResponse{}, invalidInferenceResponse("embedding provider returned invalid JSON")
		}
		response.Embeddings = decoded.Embeddings
		response.Usage = Usage{InputTokens: decoded.PromptEvalCount, TotalTokens: decoded.PromptEvalCount}
	}
	if len(response.Embeddings) == 0 {
		return EmbeddingResponse{}, invalidInferenceResponse("embedding provider returned no vectors")
	}
	dimensions := len(response.Embeddings[0])
	if dimensions == 0 {
		return EmbeddingResponse{}, invalidInferenceResponse("embedding provider returned an empty vector")
	}
	for _, vector := range response.Embeddings {
		if len(vector) != dimensions {
			return EmbeddingResponse{}, invalidInferenceResponse("embedding vectors have inconsistent dimensions")
		}
	}
	return response, nil
}

func decodeRerankResponse(body []byte, documentCount int) ([]RerankResult, error) {
	var decoded struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Score          float64 `json:"score"`
		} `json:"results"`
		Data []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Score          float64 `json:"score"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, invalidInferenceResponse("reranker provider returned invalid JSON")
	}
	items := decoded.Results
	if len(items) == 0 {
		items = decoded.Data
	}
	if len(items) == 0 {
		return nil, invalidInferenceResponse("reranker provider returned no results")
	}
	seen := make(map[int]struct{}, len(items))
	results := make([]RerankResult, 0, len(items))
	for _, item := range items {
		if item.Index < 0 || item.Index >= documentCount {
			return nil, invalidInferenceResponse("reranker provider returned an out-of-range document index")
		}
		if _, exists := seen[item.Index]; exists {
			return nil, invalidInferenceResponse("reranker provider returned a duplicate document index")
		}
		seen[item.Index] = struct{}{}
		score := item.RelevanceScore
		if score == 0 {
			score = item.Score
		}
		results = append(results, RerankResult{Index: item.Index, Score: score})
	}
	return results, nil
}

func invalidInferenceResponse(message string) error {
	return &ProviderError{Class: ErrorClassUnknown, Retryable: false, Message: message, Cause: errors.New(message)}
}

func (s InferenceService) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s InferenceService) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 60 * time.Second
}
