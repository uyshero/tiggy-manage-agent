package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DiagnosticStatusSucceeded = "succeeded"
	DiagnosticStatusFailed    = "failed"

	DiagnosticErrorConfiguration     = "configuration"
	DiagnosticErrorAuthentication    = "authentication"
	DiagnosticErrorRateLimit         = "rate_limit"
	DiagnosticErrorTimeout           = "timeout"
	DiagnosticErrorNetwork           = "network"
	DiagnosticErrorInvalidRequest    = "invalid_request"
	DiagnosticErrorInvalidResponse   = "invalid_response"
	DiagnosticErrorDimensionMismatch = "dimension_mismatch"
	DiagnosticErrorUnsupported       = "unsupported"
	DiagnosticErrorUpstream          = "upstream"
)

const (
	EmbeddingProtocolOpenAI = "openai_embeddings"
	EmbeddingProtocolTEI    = "tei_embeddings"
	EmbeddingProtocolOllama = "ollama_embed"
	RerankProtocolJina      = "jina_rerank"
	RerankProtocolCohere    = "cohere_rerank"
	RerankProtocolVLLM      = "vllm_score"
)

type DiagnosticConfig struct {
	ProviderType       string
	BaseURL            string
	APIKey             string
	APIKeyConfigured   bool
	Model              string
	CapabilityType     string
	Protocol           string
	ExpectedDimensions int
}

// DiagnosticResult deliberately contains no endpoint, request, credential, or upstream response data.
type DiagnosticResult struct {
	Status         string    `json:"status"`
	CapabilityType string    `json:"capability_type,omitempty"`
	Protocol       string    `json:"protocol,omitempty"`
	LatencyMS      int64     `json:"latency_ms"`
	Dimensions     int       `json:"dimensions,omitempty"`
	CandidateCount int       `json:"candidate_count,omitempty"`
	Authenticated  bool      `json:"authenticated"`
	ErrorType      string    `json:"error_type,omitempty"`
	Message        string    `json:"message"`
	Retryable      bool      `json:"retryable"`
	CheckedAt      time.Time `json:"checked_at"`
}

type DiagnosticService struct {
	Client  *http.Client
	Timeout time.Duration
	Now     func() time.Time
}

func (s DiagnosticService) TestProvider(ctx context.Context, config DiagnosticConfig) DiagnosticResult {
	started := s.now()
	result := DiagnosticResult{Authenticated: strings.TrimSpace(config.APIKey) != ""}
	if strings.TrimSpace(config.ProviderType) == ProviderFake {
		return s.success(started, result, "Provider connection succeeded.")
	}
	if config.APIKeyConfigured && strings.TrimSpace(config.APIKey) == "" {
		return s.failure(started, result, DiagnosticErrorConfiguration, "Configured API key environment variable is not set.", false)
	}
	endpoint, ok := diagnosticEndpoint(config.BaseURL, "/models")
	if !ok {
		return s.failure(started, result, DiagnosticErrorConfiguration, "Provider Base URL is invalid.", false)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return s.failure(started, result, DiagnosticErrorConfiguration, "Provider Base URL is invalid.", false)
	}
	s.setHeaders(request, config.APIKey, false)
	if _, diagnosticFailure := s.do(request); diagnosticFailure != nil {
		return s.failure(started, result, diagnosticFailure.errorType, diagnosticFailure.message, diagnosticFailure.retryable)
	}
	return s.success(started, result, "Provider connection succeeded.")
}

func (s DiagnosticService) TestModel(ctx context.Context, config DiagnosticConfig) DiagnosticResult {
	started := s.now()
	capabilityType := strings.TrimSpace(config.CapabilityType)
	result := DiagnosticResult{
		CapabilityType: capabilityType,
		Protocol:       strings.TrimSpace(config.Protocol),
		Authenticated:  strings.TrimSpace(config.APIKey) != "",
	}
	if strings.TrimSpace(config.Model) == "" {
		return s.failure(started, result, DiagnosticErrorConfiguration, "Model ID is required.", false)
	}
	if strings.TrimSpace(config.ProviderType) == ProviderFake {
		switch capabilityType {
		case "embedding":
			result.Dimensions = config.ExpectedDimensions
		case "reranker":
			result.CandidateCount = 2
		}
		return s.success(started, result, "Model diagnostic succeeded.")
	}
	if config.APIKeyConfigured && strings.TrimSpace(config.APIKey) == "" {
		return s.failure(started, result, DiagnosticErrorConfiguration, "Configured API key environment variable is not set.", false)
	}

	var dimensions, candidates int
	var failure *diagnosticFailure
	switch capabilityType {
	case "text", "text_image", "":
		failure = s.testChat(ctx, config)
	case "embedding":
		dimensions, failure = s.testEmbedding(ctx, config)
		result.Dimensions = dimensions
	case "reranker":
		candidates, failure = s.testReranker(ctx, config)
		result.CandidateCount = candidates
	default:
		return s.failure(started, result, DiagnosticErrorUnsupported, "Model capability is not supported for diagnostics.", false)
	}
	if failure != nil {
		return s.failure(started, result, failure.errorType, failure.message, failure.retryable)
	}
	return s.success(started, result, "Model diagnostic succeeded.")
}

func (s DiagnosticService) testChat(ctx context.Context, config DiagnosticConfig) *diagnosticFailure {
	body := map[string]any{
		"model":      config.Model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "diagnostic_ping",
				"description": "Validate model tool-calling compatibility.",
				"parameters": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": false,
				},
			},
		}},
	}
	responseBody, failure := s.postJSON(ctx, config, "/chat/completions", body)
	if failure != nil {
		return failure
	}
	var response struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if json.Unmarshal(responseBody, &response) != nil || len(response.Choices) == 0 {
		return invalidDiagnosticResponse()
	}
	return nil
}

func (s DiagnosticService) testEmbedding(ctx context.Context, config DiagnosticConfig) (int, *diagnosticFailure) {
	protocol := strings.TrimSpace(config.Protocol)
	var suffix string
	var body any
	switch protocol {
	case EmbeddingProtocolOpenAI:
		suffix = "/embeddings"
		body = map[string]any{"model": config.Model, "input": []string{"diagnostic"}}
	case EmbeddingProtocolTEI:
		suffix = "/embed"
		body = map[string]any{"inputs": []string{"diagnostic"}}
	case EmbeddingProtocolOllama:
		suffix = "/api/embed"
		body = map[string]any{"model": config.Model, "input": "diagnostic"}
	default:
		return 0, &diagnosticFailure{errorType: DiagnosticErrorUnsupported, message: "Embedding protocol is not supported for diagnostics."}
	}
	responseBody, failure := s.postJSON(ctx, config, suffix, body)
	if failure != nil {
		return 0, failure
	}
	dimensions := embeddingDimensions(protocol, responseBody)
	if dimensions <= 0 {
		return 0, invalidDiagnosticResponse()
	}
	if config.ExpectedDimensions > 0 && dimensions != config.ExpectedDimensions {
		return dimensions, &diagnosticFailure{errorType: DiagnosticErrorDimensionMismatch, message: "Embedding dimensions do not match the model configuration."}
	}
	return dimensions, nil
}

func (s DiagnosticService) testReranker(ctx context.Context, config DiagnosticConfig) (int, *diagnosticFailure) {
	protocol := strings.TrimSpace(config.Protocol)
	var suffix string
	var body any
	switch protocol {
	case RerankProtocolJina, RerankProtocolCohere:
		suffix = "/rerank"
		body = map[string]any{
			"model": config.Model, "query": "diagnostic query",
			"documents": []string{"first candidate", "second candidate"}, "top_n": 2,
		}
	case RerankProtocolVLLM:
		suffix = "/score"
		body = map[string]any{
			"model": config.Model, "text_1": "diagnostic query",
			"text_2": []string{"first candidate", "second candidate"},
		}
	default:
		return 0, &diagnosticFailure{errorType: DiagnosticErrorUnsupported, message: "Reranker protocol is not supported for diagnostics."}
	}
	responseBody, failure := s.postJSON(ctx, config, suffix, body)
	if failure != nil {
		return 0, failure
	}
	candidates := rerankerResultCount(responseBody)
	if candidates <= 0 || candidates > 2 {
		return 0, invalidDiagnosticResponse()
	}
	return candidates, nil
}

func (s DiagnosticService) postJSON(ctx context.Context, config DiagnosticConfig, suffix string, body any) ([]byte, *diagnosticFailure) {
	endpoint, ok := diagnosticEndpoint(config.BaseURL, suffix)
	if !ok {
		return nil, &diagnosticFailure{errorType: DiagnosticErrorConfiguration, message: "Provider Base URL is invalid."}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, &diagnosticFailure{errorType: DiagnosticErrorConfiguration, message: "Diagnostic request could not be prepared."}
	}
	requestContext, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, &diagnosticFailure{errorType: DiagnosticErrorConfiguration, message: "Provider Base URL is invalid."}
	}
	s.setHeaders(request, config.APIKey, true)
	return s.do(request)
}

type diagnosticFailure struct {
	errorType string
	message   string
	retryable bool
}

func (s DiagnosticService) do(request *http.Request) ([]byte, *diagnosticFailure) {
	response, err := s.client().Do(request)
	if err != nil {
		if errors.Is(request.Context().Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, &diagnosticFailure{errorType: DiagnosticErrorTimeout, message: "Provider request timed out.", retryable: true}
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, &diagnosticFailure{errorType: DiagnosticErrorTimeout, message: "Provider request timed out.", retryable: true}
		}
		return nil, &diagnosticFailure{errorType: DiagnosticErrorNetwork, message: "Provider network connection failed.", retryable: true}
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if readErr != nil || len(responseBody) > 1<<20 {
		return nil, &diagnosticFailure{errorType: DiagnosticErrorInvalidResponse, message: "Provider returned an invalid diagnostic response."}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, diagnosticHTTPFailure(response.StatusCode)
	}
	return responseBody, nil
}

func diagnosticHTTPFailure(statusCode int) *diagnosticFailure {
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return &diagnosticFailure{errorType: DiagnosticErrorAuthentication, message: "Provider authentication failed."}
	case statusCode == http.StatusTooManyRequests:
		return &diagnosticFailure{errorType: DiagnosticErrorRateLimit, message: "Provider rate limit was reached.", retryable: true}
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusGatewayTimeout:
		return &diagnosticFailure{errorType: DiagnosticErrorTimeout, message: "Provider request timed out.", retryable: true}
	case statusCode >= 500:
		return &diagnosticFailure{errorType: DiagnosticErrorUpstream, message: "Provider service is unavailable.", retryable: true}
	default:
		return &diagnosticFailure{errorType: DiagnosticErrorInvalidRequest, message: "Provider rejected the diagnostic request."}
	}
}

func diagnosticEndpoint(baseURL string, suffix string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + suffix
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func embeddingDimensions(protocol string, body []byte) int {
	switch protocol {
	case EmbeddingProtocolOpenAI:
		var response struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &response) == nil && len(response.Data) > 0 {
			return len(response.Data[0].Embedding)
		}
	case EmbeddingProtocolTEI:
		var response [][]float64
		if json.Unmarshal(body, &response) == nil && len(response) > 0 {
			return len(response[0])
		}
	case EmbeddingProtocolOllama:
		var response struct {
			Embeddings [][]float64 `json:"embeddings"`
		}
		if json.Unmarshal(body, &response) == nil && len(response.Embeddings) > 0 {
			return len(response.Embeddings[0])
		}
	}
	return 0
}

func rerankerResultCount(body []byte) int {
	var response struct {
		Results []json.RawMessage `json:"results"`
		Data    []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil {
		return 0
	}
	if len(response.Results) > 0 {
		return len(response.Results)
	}
	return len(response.Data)
}

func invalidDiagnosticResponse() *diagnosticFailure {
	return &diagnosticFailure{errorType: DiagnosticErrorInvalidResponse, message: "Provider returned an invalid diagnostic response."}
}

func (s DiagnosticService) setHeaders(request *http.Request, apiKey string, jsonBody bool) {
	if jsonBody {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(apiKey) != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func (s DiagnosticService) success(started time.Time, result DiagnosticResult, message string) DiagnosticResult {
	result.Status = DiagnosticStatusSucceeded
	result.Message = message
	result.CheckedAt = s.now()
	result.LatencyMS = max(result.CheckedAt.Sub(started).Milliseconds(), 0)
	return result
}

func (s DiagnosticService) failure(started time.Time, result DiagnosticResult, errorType string, message string, retryable bool) DiagnosticResult {
	result.Status = DiagnosticStatusFailed
	result.ErrorType = errorType
	result.Message = message
	result.Retryable = retryable
	result.CheckedAt = s.now()
	result.LatencyMS = max(result.CheckedAt.Sub(started).Milliseconds(), 0)
	return result
}

func (s DiagnosticService) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s DiagnosticService) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 10 * time.Second
}

func (s DiagnosticService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
