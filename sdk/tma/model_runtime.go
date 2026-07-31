package tma

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ModelMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelGenerateRequest struct {
	ProviderID      string         `json:"provider_id,omitempty"`
	Model           string         `json:"model,omitempty"`
	Messages        []ModelMessage `json:"messages"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
}

type ModelUsage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
	TotalTokens       int64 `json:"total_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoning_tokens,omitempty"`
}

type ModelGenerateResponse struct {
	Text         string     `json:"text"`
	ProviderID   string     `json:"provider_id"`
	Model        string     `json:"model"`
	Usage        ModelUsage `json:"usage"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

type ModelEmbeddingRequest struct {
	ProviderID string   `json:"provider_id,omitempty"`
	Model      string   `json:"model,omitempty"`
	Inputs     []string `json:"inputs"`
}

type ModelEmbedding struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type ModelEmbeddingResponse struct {
	Embeddings []ModelEmbedding `json:"embeddings"`
	ProviderID string           `json:"provider_id"`
	Model      string           `json:"model"`
	Dimensions int              `json:"dimensions"`
	Usage      ModelUsage       `json:"usage"`
}

type ModelRerankRequest struct {
	ProviderID string   `json:"provider_id,omitempty"`
	Model      string   `json:"model,omitempty"`
	Query      string   `json:"query"`
	Documents  []string `json:"documents"`
	TopN       int      `json:"top_n,omitempty"`
}

type ModelRerankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

type ModelRerankResponse struct {
	Results    []ModelRerankResult `json:"results"`
	ProviderID string              `json:"provider_id"`
	Model      string              `json:"model"`
}

type ModelInvocation struct {
	ID                string    `json:"id"`
	WorkspaceID       string    `json:"workspace_id"`
	PrincipalID       string    `json:"principal_id"`
	ServiceIdentityID string    `json:"service_identity_id,omitempty"`
	AuthType          string    `json:"auth_type,omitempty"`
	RequestID         string    `json:"request_id"`
	Capability        string    `json:"capability"`
	ProviderID        string    `json:"provider_id"`
	ProviderType      string    `json:"provider_type,omitempty"`
	Model             string    `json:"model"`
	Status            string    `json:"status"`
	ErrorCode         string    `json:"error_code,omitempty"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	TotalTokens       int64     `json:"total_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens"`
	ReasoningTokens   int64     `json:"reasoning_tokens"`
	InputItems        int64     `json:"input_items"`
	OutputItems       int64     `json:"output_items"`
	InputBytes        int64     `json:"input_bytes"`
	OutputBytes       int64     `json:"output_bytes"`
	InputCharacters   int64     `json:"input_characters"`
	OutputCharacters  int64     `json:"output_characters"`
	InputAudioMillis  int64     `json:"input_audio_ms"`
	OutputAudioMillis int64     `json:"output_audio_ms"`
	LatencyMillis     int64     `json:"latency_ms"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
}

type ModelInvocationSummary struct {
	RecordCount       int64 `json:"record_count"`
	CompletedCount    int64 `json:"completed_count"`
	FailedCount       int64 `json:"failed_count"`
	CanceledCount     int64 `json:"canceled_count"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
	InputItems        int64 `json:"input_items"`
	OutputItems       int64 `json:"output_items"`
	InputBytes        int64 `json:"input_bytes"`
	OutputBytes       int64 `json:"output_bytes"`
	InputCharacters   int64 `json:"input_characters"`
	OutputCharacters  int64 `json:"output_characters"`
	InputAudioMillis  int64 `json:"input_audio_ms"`
	OutputAudioMillis int64 `json:"output_audio_ms"`
	LatencyMillis     int64 `json:"latency_ms"`
}

type ModelInvocationReport struct {
	Summary ModelInvocationSummary `json:"summary"`
	Records []ModelInvocation      `json:"records"`
}

type ModelInvocationQuery struct {
	PrincipalID       string
	ServiceIdentityID string
	Capability        string
	ProviderID        string
	Model             string
	Status            string
	From              *time.Time
	To                *time.Time
	Limit             int
}

type ModelRuntimeService struct{ client *Client }

func (s *ModelRuntimeService) Generate(ctx context.Context, request ModelGenerateRequest) (ModelGenerateResponse, error) {
	var response ModelGenerateResponse
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/model-runtime/generate", request, &response)
	return response, err
}

func (s *ModelRuntimeService) Embed(ctx context.Context, request ModelEmbeddingRequest) (ModelEmbeddingResponse, error) {
	var response ModelEmbeddingResponse
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/model-runtime/embeddings", request, &response)
	return response, err
}

func (s *ModelRuntimeService) Rerank(ctx context.Context, request ModelRerankRequest) (ModelRerankResponse, error) {
	var response ModelRerankResponse
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/model-runtime/rerank", request, &response)
	return response, err
}

func (s *ModelRuntimeService) Invocations(ctx context.Context, query ModelInvocationQuery) (ModelInvocationReport, error) {
	values := url.Values{}
	values.Set("principal_id", query.PrincipalID)
	values.Set("service_identity_id", query.ServiceIdentityID)
	values.Set("capability", query.Capability)
	values.Set("provider_id", query.ProviderID)
	values.Set("model", query.Model)
	values.Set("status", query.Status)
	if query.From != nil {
		values.Set("from", query.From.Format(time.RFC3339))
	}
	if query.To != nil {
		values.Set("to", query.To.Format(time.RFC3339))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	for key := range values {
		if values.Get(key) == "" {
			values.Del(key)
		}
	}
	path := "/v2/model-runtime/invocations"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	var response ModelInvocationReport
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response, err
}
