package modelruntimeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/llm"
)

const (
	MaxRequestBytes   = 64 << 20
	MaxResponseBytes  = 64 << 20
	streamContentType = "application/x-ndjson"
)

const (
	streamEventDelta    = "delta"
	streamEventResponse = "response"
	streamEventError    = "error"
)

type streamEvent struct {
	Type     string        `json:"type"`
	Delta    *llm.Delta    `json:"delta,omitempty"`
	Response *llm.Response `json:"response,omitempty"`
	Error    *RuntimeError `json:"error,omitempty"`
}

type ErrorResponse struct {
	Error RuntimeError `json:"error"`
}

type RuntimeError struct {
	Class             llm.ErrorClass `json:"class"`
	StatusCode        int            `json:"status_code,omitempty"`
	Retryable         bool           `json:"retryable"`
	RetryAfterSeconds int            `json:"retry_after_seconds,omitempty"`
	Message           string         `json:"message"`
}

type HandlerConfig struct {
	AuthToken  string
	Auth       AuthConfig
	Executor   Executor
	Speech     SpeechProxy
	Multimodal MultimodalProxy
	Metrics    *RuntimeMetrics
}

func NewHandler(config HandlerConfig) (http.Handler, error) {
	authConfig := config.Auth
	if strings.TrimSpace(authConfig.Secret) == "" {
		authConfig = AuthConfig{Mode: AuthModeStatic, Secret: config.AuthToken}
	}
	authenticator, err := newRequestAuthenticator(authConfig)
	if err != nil {
		return nil, err
	}
	executor := config.Executor
	if executor == nil {
		executor = LocalExecutor{}
	}
	speech := config.Speech
	if speech == nil {
		speech = LocalExecutor{}
	}
	multimodal := config.Multimodal
	if multimodal == nil {
		multimodal = LocalExecutor{}
	}
	metrics := config.Metrics
	if metrics == nil {
		metrics = NewRuntimeMetrics(0)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", metrics.ServeHTTP)
	mux.Handle("POST /internal/v1/generate", requireToken(authenticator, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request GenerateRequest
		if !decodeRequest(w, r, &request) {
			return
		}
		response, err := executor.Generate(r.Context(), request)
		writeExecutionResult(w, response, err)
	})))
	mux.Handle("POST /internal/v1/generate-stream", requireToken(authenticator, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request GenerateRequest
		if !decodeRequest(w, r, &request) {
			return
		}
		streaming, ok := executor.(StreamingExecutor)
		if !ok {
			writeJSON(w, http.StatusNotImplemented, ErrorResponse{Error: RuntimeError{
				Class: llm.ErrorClassServer, StatusCode: http.StatusNotImplemented,
				Message: "model runtime streaming is not available",
			}})
			return
		}
		writeGenerateStream(w, r, streaming, request, metrics)
	})))
	mux.Handle("POST /internal/v1/embeddings", requireToken(authenticator, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request EmbeddingRequest
		if !decodeRequest(w, r, &request) {
			return
		}
		response, err := executor.Embed(r.Context(), request)
		writeExecutionResult(w, response, err)
	})))
	mux.Handle("POST /internal/v1/rerank", requireToken(authenticator, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request RerankRequest
		if !decodeRequest(w, r, &request) {
			return
		}
		response, err := executor.Rerank(r.Context(), request)
		writeExecutionResult(w, response, err)
	})))
	mux.Handle("GET /internal/v1/speech/realtime", requireToken(authenticator, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveSpeechRuntime(w, r, speech, metrics)
	})))
	mux.Handle("GET "+multimodalRuntimePath, requireToken(authenticator, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveMultimodalRuntime(w, r, multimodal, metrics)
	})))
	return mux, nil
}

func NewHealthHandler(metrics *RuntimeMetrics) http.Handler {
	if metrics == nil {
		metrics = NewRuntimeMetrics(0)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", metrics.ServeHTTP)
	return mux
}

func requireToken(authenticator *requestAuthenticator, metrics *RuntimeMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authenticator.verifyRequest(r) {
			metrics.observeAuthentication(false)
			w.Header().Set("WWW-Authenticate", `Bearer realm="tma-model-runtime"`)
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: RuntimeError{Class: llm.ErrorClassAuth, StatusCode: http.StatusUnauthorized, Message: "model runtime authentication failed"}})
			return
		}
		metrics.observeAuthentication(true)
		next.ServeHTTP(w, r)
	})
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: RuntimeError{Class: llm.ErrorClassInvalidRequest, StatusCode: http.StatusBadRequest, Message: "invalid model runtime request"}})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: RuntimeError{Class: llm.ErrorClassInvalidRequest, StatusCode: http.StatusBadRequest, Message: "model runtime request must contain one JSON object"}})
		return false
	}
	return true
}

func writeExecutionResult[T any](w http.ResponseWriter, response T, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}
	runtimeError := runtimeErrorFrom(err)
	status := runtimeError.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	if runtimeError.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(runtimeError.RetryAfterSeconds))
	}
	writeJSON(w, status, ErrorResponse{Error: runtimeError})
}

func writeGenerateStream(w http.ResponseWriter, r *http.Request, executor StreamingExecutor, request GenerateRequest, metrics *RuntimeMetrics) {
	metrics.streamStarted(streamProtocolNDJSON)
	defer metrics.streamFinished(streamProtocolNDJSON)
	encoder := json.NewEncoder(w)
	started := false
	writeEvent := func(event streamEvent) error {
		startedAt := time.Now()
		if !started {
			w.Header().Set("Content-Type", streamContentType)
			w.WriteHeader(http.StatusOK)
			started = true
		}
		if err := encoder.Encode(event); err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		metrics.observeStreamEvent(streamProtocolNDJSON, streamDirectionRuntimeToClient, time.Since(startedAt))
		return nil
	}
	response, err := executor.GenerateStream(r.Context(), request, func(delta llm.Delta) error {
		return writeEvent(streamEvent{Type: streamEventDelta, Delta: &delta})
	})
	if err != nil {
		if !started {
			writeExecutionResult(w, response, err)
			return
		}
		_ = writeEvent(streamEvent{Type: streamEventError, Error: runtimeErrorPointer(err)})
		return
	}
	_ = writeEvent(streamEvent{Type: streamEventResponse, Response: &response})
}

func runtimeErrorPointer(err error) *RuntimeError {
	converted := runtimeErrorFrom(err)
	return &converted
}

func runtimeErrorFrom(err error) RuntimeError {
	var providerError *llm.ProviderError
	if errors.As(err, &providerError) {
		retryAfter := 0
		if providerError.RetryAfter > 0 {
			retryAfter = int(providerError.RetryAfter.Round(time.Second) / time.Second)
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
		message := strings.TrimSpace(providerError.Message)
		if message == "" {
			message = providerError.Error()
		}
		return RuntimeError{
			Class: providerError.Class, StatusCode: providerError.StatusCode, Retryable: providerError.Retryable,
			RetryAfterSeconds: retryAfter, Message: message,
		}
	}
	return RuntimeError{Class: llm.ErrorClassUnknown, StatusCode: http.StatusBadGateway, Retryable: true, Message: "model provider request failed"}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type HTTPExecutor struct {
	endpoint string
	auth     *requestAuthenticator
	client   *http.Client
}

type HTTPModelClient struct {
	executor       *HTTPExecutor
	maxAttempts    int
	retryBaseDelay time.Duration
}

func NewHTTPExecutor(endpoint string, authToken string, client *http.Client) (*HTTPExecutor, error) {
	return NewHTTPExecutorWithAuth(endpoint, AuthConfig{Mode: AuthModeStatic, Secret: authToken}, client)
}

func NewHTTPExecutorWithAuth(endpoint string, authConfig AuthConfig, client *http.Client) (*HTTPExecutor, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("model runtime endpoint must be an absolute HTTP(S) URL without query or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("model runtime endpoint must use http or https")
	}
	authenticator, err := newRequestAuthenticator(authConfig)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 70 * time.Second}
	}
	return &HTTPExecutor{endpoint: endpoint, auth: authenticator, client: client}, nil
}

func NewHTTPModelClient(endpoint string, authToken string, client *http.Client, maxAttempts int, retryBaseDelay time.Duration) (*HTTPModelClient, error) {
	executor, err := NewHTTPExecutor(endpoint, authToken, client)
	if err != nil {
		return nil, err
	}
	return &HTTPModelClient{executor: executor, maxAttempts: maxAttempts, retryBaseDelay: retryBaseDelay}, nil
}

func NewHTTPModelClientWithAuth(endpoint string, authConfig AuthConfig, client *http.Client, maxAttempts int, retryBaseDelay time.Duration) (*HTTPModelClient, error) {
	executor, err := NewHTTPExecutorWithAuth(endpoint, authConfig, client)
	if err != nil {
		return nil, err
	}
	return &HTTPModelClient{executor: executor, maxAttempts: maxAttempts, retryBaseDelay: retryBaseDelay}, nil
}

var _ llm.Client = (*HTTPModelClient)(nil)
var _ llm.StreamingClient = (*HTTPModelClient)(nil)

func (c *HTTPModelClient) Generate(ctx context.Context, request llm.Request) (llm.Response, error) {
	return c.executor.Generate(ctx, c.generateRequest(request))
}

func (c *HTTPModelClient) GenerateStream(ctx context.Context, request llm.Request, sink func(llm.Delta) error) (llm.Response, error) {
	return c.executor.GenerateStream(ctx, c.generateRequest(request), sink)
}

func (c *HTTPModelClient) generateRequest(request llm.Request) GenerateRequest {
	return GenerateRequest{
		Route: Route{
			ProviderID: request.Provider, ProviderType: request.ProviderType,
			BaseURL: request.BaseURL, APIKey: request.APIKey, Model: request.Model,
		},
		Messages: request.Messages, Tools: request.Tools, ThinkingMode: request.ThinkingMode,
		MaxOutputTokens: request.MaxOutputTokens, MaxAttempts: c.maxAttempts,
		RetryBaseDelayMS: int(c.retryBaseDelay / time.Millisecond),
	}
}

func (e *HTTPExecutor) Generate(ctx context.Context, request GenerateRequest) (llm.Response, error) {
	var response llm.Response
	err := e.post(ctx, "/internal/v1/generate", request, &response)
	return response, err
}

func (e *HTTPExecutor) GenerateStream(ctx context.Context, request GenerateRequest, sink func(llm.Delta) error) (llm.Response, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return llm.Response{}, fmt.Errorf("encode model runtime stream request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+"/internal/v1/generate-stream", bytes.NewReader(encoded))
	if err != nil {
		return llm.Response{}, fmt.Errorf("build model runtime stream request: %w", err)
	}
	if err := e.authorize(httpRequest); err != nil {
		return llm.Response{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", streamContentType)
	httpResponse, err := e.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return llm.Response{}, ctx.Err()
		}
		return llm.Response{}, &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: true, Message: "model runtime is unavailable", Cause: err}
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, MaxResponseBytes+1))
		if readErr != nil {
			return llm.Response{}, &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: true, Message: "could not read model runtime response", Cause: readErr}
		}
		if len(body) > MaxResponseBytes {
			return llm.Response{}, &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime response exceeds the Platform limit"}
		}
		if httpResponse.StatusCode == http.StatusUnauthorized && strings.Contains(httpResponse.Header.Get("WWW-Authenticate"), `realm="tma-model-runtime"`) {
			return llm.Response{}, &llm.ProviderError{Class: llm.ErrorClassServer, StatusCode: http.StatusBadGateway, Retryable: false, Message: "model runtime authentication failed"}
		}
		return llm.Response{}, decodeRuntimeError(httpResponse.StatusCode, httpResponse.Header.Get("Retry-After"), body)
	}

	limited := &streamLimitReader{reader: httpResponse.Body, remaining: MaxResponseBytes}
	decoder := json.NewDecoder(limited)
	for {
		var event streamEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return llm.Response{}, &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime stream ended without a response"}
			}
			if ctx.Err() != nil {
				return llm.Response{}, ctx.Err()
			}
			return llm.Response{}, &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime returned an invalid stream", Cause: err}
		}
		switch event.Type {
		case streamEventDelta:
			if event.Delta == nil {
				return llm.Response{}, invalidStreamEventError()
			}
			if sink != nil {
				if err := sink(*event.Delta); err != nil {
					_ = httpResponse.Body.Close()
					return llm.Response{}, err
				}
			}
		case streamEventResponse:
			if event.Response == nil {
				return llm.Response{}, invalidStreamEventError()
			}
			return *event.Response, nil
		case streamEventError:
			if event.Error == nil || strings.TrimSpace(event.Error.Message) == "" {
				return llm.Response{}, invalidStreamEventError()
			}
			return llm.Response{}, providerErrorFromRuntime(*event.Error, event.Error.StatusCode, "")
		default:
			return llm.Response{}, invalidStreamEventError()
		}
	}
}

type streamLimitReader struct {
	reader    io.Reader
	remaining int64
}

func (r *streamLimitReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("model runtime stream exceeds the Platform limit")
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func invalidStreamEventError() error {
	return &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime returned an invalid stream event"}
}

func (e *HTTPExecutor) Embed(ctx context.Context, request EmbeddingRequest) (llm.EmbeddingResponse, error) {
	var response llm.EmbeddingResponse
	err := e.post(ctx, "/internal/v1/embeddings", request, &response)
	return response, err
}

func (e *HTTPExecutor) Rerank(ctx context.Context, request RerankRequest) ([]llm.RerankResult, error) {
	var response []llm.RerankResult
	err := e.post(ctx, "/internal/v1/rerank", request, &response)
	return response, err
}

func (e *HTTPExecutor) post(ctx context.Context, path string, payload any, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode model runtime request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build model runtime request: %w", err)
	}
	if err := e.authorize(request); err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := e.client.Do(request)
	if err != nil {
		return &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: true, Message: "model runtime is unavailable", Cause: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: true, Message: "could not read model runtime response", Cause: err}
	}
	if len(body) > MaxResponseBytes {
		return &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime response exceeds the Platform limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusUnauthorized && strings.Contains(response.Header.Get("WWW-Authenticate"), `realm="tma-model-runtime"`) {
			return &llm.ProviderError{
				Class: llm.ErrorClassServer, StatusCode: http.StatusBadGateway,
				Retryable: false, Message: "model runtime authentication failed",
			}
		}
		return decodeRuntimeError(response.StatusCode, response.Header.Get("Retry-After"), body)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime returned invalid JSON", Cause: err}
	}
	return nil
}

func (e *HTTPExecutor) authorize(request *http.Request) error {
	authorization, err := e.auth.authorization(request.Method, request.URL.Path)
	if err != nil {
		return &llm.ProviderError{Class: llm.ErrorClassServer, Retryable: false, Message: "model runtime authentication token could not be issued", Cause: err}
	}
	request.Header.Set("Authorization", authorization)
	return nil
}

func decodeRuntimeError(status int, retryAfterHeader string, body []byte) error {
	decoded := ErrorResponse{}
	if err := json.Unmarshal(body, &decoded); err != nil || strings.TrimSpace(decoded.Error.Message) == "" {
		return &llm.ProviderError{Class: llm.ErrorClassServer, StatusCode: status, Retryable: status >= 500, Message: "model runtime request failed"}
	}
	return providerErrorFromRuntime(decoded.Error, status, retryAfterHeader)
}

func providerErrorFromRuntime(runtimeError RuntimeError, status int, retryAfterHeader string) error {
	retryAfter := time.Duration(runtimeError.RetryAfterSeconds) * time.Second
	if retryAfter == 0 {
		if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfterHeader)); err == nil && seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
	}
	return &llm.ProviderError{
		Class: runtimeError.Class, StatusCode: status, Retryable: runtimeError.Retryable,
		RetryAfter: retryAfter, Message: runtimeError.Message,
	}
}
