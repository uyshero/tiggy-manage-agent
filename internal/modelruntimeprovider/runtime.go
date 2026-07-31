package modelruntimeprovider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/llm"
)

// Route is the control plane-approved provider configuration for one call.
// It intentionally contains no workspace, principal, quota, or database state.
type Route struct {
	ProviderID   string `json:"provider_id"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	Model        string `json:"model"`
	Protocol     string `json:"protocol,omitempty"`
}

type GenerateRequest struct {
	Route            Route         `json:"route"`
	Messages         []llm.Message `json:"messages"`
	Tools            []llm.Tool    `json:"tools,omitempty"`
	ThinkingMode     string        `json:"thinking_mode,omitempty"`
	MaxOutputTokens  int           `json:"max_output_tokens,omitempty"`
	MaxAttempts      int           `json:"max_attempts,omitempty"`
	RetryBaseDelayMS int           `json:"retry_base_delay_ms,omitempty"`
}

type EmbeddingRequest struct {
	Route  Route    `json:"route"`
	Inputs []string `json:"inputs"`
}

type RerankRequest struct {
	Route     Route    `json:"route"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type Executor interface {
	Generate(context.Context, GenerateRequest) (llm.Response, error)
	Embed(context.Context, EmbeddingRequest) (llm.EmbeddingResponse, error)
	Rerank(context.Context, RerankRequest) ([]llm.RerankResult, error)
}

type StreamingExecutor interface {
	GenerateStream(context.Context, GenerateRequest, func(llm.Delta) error) (llm.Response, error)
}

type LocalExecutor struct{}

func (LocalExecutor) Generate(ctx context.Context, request GenerateRequest) (llm.Response, error) {
	manager, modelRequest, err := managerAndRequest(request)
	if err != nil {
		return llm.Response{}, err
	}
	return manager.Generate(ctx, modelRequest)
}

func (LocalExecutor) GenerateStream(ctx context.Context, request GenerateRequest, sink func(llm.Delta) error) (llm.Response, error) {
	manager, modelRequest, err := managerAndRequest(request)
	if err != nil {
		return llm.Response{}, err
	}
	return manager.GenerateStream(ctx, modelRequest, sink)
}

func (LocalExecutor) Embed(ctx context.Context, request EmbeddingRequest) (llm.EmbeddingResponse, error) {
	if err := validateRoute(request.Route); err != nil {
		return llm.EmbeddingResponse{}, err
	}
	return (llm.InferenceService{}).Embed(ctx, inferenceConfig(request.Route), request.Inputs)
}

func (LocalExecutor) Rerank(ctx context.Context, request RerankRequest) ([]llm.RerankResult, error) {
	if err := validateRoute(request.Route); err != nil {
		return nil, err
	}
	return (llm.InferenceService{}).Rerank(ctx, inferenceConfig(request.Route), request.Query, request.Documents, request.TopN)
}

func validateRoute(route Route) error {
	if strings.TrimSpace(route.ProviderID) == "" {
		return fmt.Errorf("model runtime route provider_id is required")
	}
	if strings.TrimSpace(route.Model) == "" {
		return fmt.Errorf("model runtime route model is required")
	}
	return nil
}

func managerAndRequest(request GenerateRequest) (*llm.Manager, llm.Request, error) {
	if err := validateRoute(request.Route); err != nil {
		return nil, llm.Request{}, err
	}
	if request.MaxAttempts < 0 || request.MaxAttempts > 10 {
		return nil, llm.Request{}, fmt.Errorf("model runtime max_attempts must be between 1 and 10 when specified")
	}
	if request.RetryBaseDelayMS < 0 || request.RetryBaseDelayMS > 60000 {
		return nil, llm.Request{}, fmt.Errorf("model runtime retry_base_delay_ms must be between 1 and 60000 when specified")
	}
	manager, err := llm.NewManagerWithConfig(llm.ManagerConfig{
		Provider: request.Route.ProviderID, ProviderType: request.Route.ProviderType,
		Model: request.Route.Model, BaseURL: request.Route.BaseURL, APIKey: request.Route.APIKey,
		MaxAttempts: request.MaxAttempts, RetryBaseDelay: time.Duration(request.RetryBaseDelayMS) * time.Millisecond,
	})
	if err != nil {
		return nil, llm.Request{}, err
	}
	return manager, llm.Request{
		Provider: request.Route.ProviderID, Model: request.Route.Model,
		ThinkingMode: request.ThinkingMode, MaxOutputTokens: request.MaxOutputTokens,
		Messages: request.Messages, Tools: request.Tools,
	}, nil
}

func inferenceConfig(route Route) llm.InferenceConfig {
	return llm.InferenceConfig{
		ProviderType: route.ProviderType,
		BaseURL:      route.BaseURL,
		APIKey:       route.APIKey,
		Model:        route.Model,
		Protocol:     route.Protocol,
	}
}
