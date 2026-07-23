package tma

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

type TokenSource func(context.Context) (string, error)

type Option func(*Client) error

type Client struct {
	baseURL     *url.URL
	httpClient  *http.Client
	streamHTTP  *http.Client
	token       string
	tokenSource TokenSource
	transport   http.RoundTripper

	Auth                 *AuthService
	Agents               *AgentsService
	Environments         *EnvironmentsService
	Sessions             *SessionsService
	Evaluations          *EvaluationsService
	Runs                 *RunsService
	Interventions        *InterventionsService
	Artifacts            *ArtifactsService
	ObjectRefs           *ObjectRefsService
	Traces               *TracesService
	LLM                  *LLMService
	Workers              *WorkersService
	WorkerWork           *WorkerWorkService
	MCP                  *MCPService
	Skills               *SkillsService
	Marketplace          *MarketplaceService
	Orchestration        *OrchestrationService
	Observability        *ObservabilityService
	Audit                *AuditService
	EnvironmentVariables *EnvironmentVariablesService
}

func NewClient(baseURL string, options ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("tma: base URL must be an absolute HTTP URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	client := &Client{
		baseURL:    parsed,
		httpClient: &http.Client{},
		streamHTTP: &http.Client{},
	}
	for _, option := range options {
		if option != nil {
			if err := option(client); err != nil {
				return nil, err
			}
		}
	}
	if client.transport != nil {
		httpClient := *client.httpClient
		httpClient.Transport = client.transport
		client.httpClient = &httpClient
		streamHTTP := *client.streamHTTP
		streamHTTP.Transport = client.transport
		client.streamHTTP = &streamHTTP
	}
	client.initializeServices()
	return client, nil
}

func WithBearerToken(token string) Option {
	return func(client *Client) error {
		client.token = strings.TrimSpace(token)
		return nil
	}
}

func WithTokenSource(source TokenSource) Option {
	return func(client *Client) error {
		if source == nil {
			return errors.New("tma: token source is required")
		}
		client.tokenSource = source
		return nil
	}
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) error {
		if httpClient == nil {
			return errors.New("tma: HTTP client is required")
		}
		client.httpClient = httpClient
		return nil
	}
}

func WithStreamHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) error {
		if httpClient == nil {
			return errors.New("tma: stream HTTP client is required")
		}
		client.streamHTTP = httpClient
		return nil
	}
}

// WithTransport installs the same transport for ordinary and streaming requests.
// The configured HTTP clients are cloned so the caller's clients are not mutated.
func WithTransport(transport http.RoundTripper) Option {
	return func(client *Client) error {
		if transport == nil {
			return errors.New("tma: HTTP transport is required")
		}
		client.transport = transport
		return nil
	}
}

func (c *Client) initializeServices() {
	c.Auth = &AuthService{client: c}
	c.Agents = &AgentsService{client: c}
	c.Environments = &EnvironmentsService{client: c}
	c.Sessions = &SessionsService{client: c}
	c.Evaluations = &EvaluationsService{client: c}
	c.Runs = &RunsService{client: c}
	c.Interventions = &InterventionsService{client: c}
	c.Artifacts = &ArtifactsService{client: c}
	c.ObjectRefs = &ObjectRefsService{client: c}
	c.Traces = &TracesService{client: c}
	c.LLM = &LLMService{client: c}
	c.Workers = &WorkersService{client: c}
	c.WorkerWork = &WorkerWorkService{client: c}
	c.MCP = &MCPService{client: c}
	c.Skills = &SkillsService{client: c}
	c.Marketplace = &MarketplaceService{client: c}
	c.Orchestration = &OrchestrationService{client: c}
	c.Observability = &ObservabilityService{client: c}
	c.Audit = &AuditService{client: c}
	c.EnvironmentVariables = &EnvironmentVariablesService{client: c}
}

type Service struct {
	client *Client
	Prefix string
}

func (s *Service) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	if s == nil || s.client == nil {
		return errors.New("tma: service is not initialized")
	}
	return s.client.DoJSON(ctx, method, joinAPIPath(s.Prefix, path), request, response)
}

func joinAPIPath(prefix string, path string) string {
	if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v2/") {
		return path
	}
	if strings.TrimSpace(path) == "" {
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

func (c *Client) DoJSON(ctx context.Context, method string, path string, requestBody any, responseBody any) error {
	return c.DoJSONWithHeaders(ctx, method, path, nil, requestBody, responseBody)
}

// DoJSONWithHeaders performs one JSON request with caller-supplied headers.
// It is intended for protocol requirements such as conditional writes and does
// not retry the request.
func (c *Client) DoJSONWithHeaders(ctx context.Context, method string, path string, headers http.Header, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("tma: encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("tma: request failed: %w", err)
	}
	defer response.Body.Close()
	return decodeResponse(response, responseBody)
}

func (c *Client) Download(ctx context.Context, path string, output io.Writer) error {
	if output == nil {
		return errors.New("tma: download output is required")
	}
	request, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("tma: download failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeResponse(response, nil)
	}
	if _, err := io.Copy(output, response.Body); err != nil {
		return fmt.Errorf("tma: copy download: %w", err)
	}
	return nil
}

type UploadFile struct {
	FieldName   string
	FileName    string
	ContentType string
	Body        io.Reader
}

// Upload sends a streaming multipart request. It does not retry the request.
func (c *Client) Upload(ctx context.Context, path string, fields map[string]string, file UploadFile, target any) error {
	if file.Body == nil {
		return errors.New("tma: upload body is required")
	}
	if strings.TrimSpace(file.FileName) == "" {
		return errors.New("tma: upload file name is required")
	}
	fieldName := strings.TrimSpace(file.FieldName)
	if fieldName == "" {
		fieldName = "file"
	}

	pipeReader, pipeWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(pipeWriter)
	request, err := c.newRequest(ctx, http.MethodPost, path, pipeReader)
	if err != nil {
		_ = pipeReader.Close()
		_ = pipeWriter.Close()
		return err
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())

	writeDone := make(chan error, 1)
	go func() {
		writeErr := writeMultipartUpload(multipartWriter, fields, fieldName, file)
		if closeErr := multipartWriter.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = pipeWriter.CloseWithError(writeErr)
		writeDone <- writeErr
	}()

	response, err := c.httpClient.Do(request)
	if err != nil {
		_ = pipeReader.CloseWithError(err)
		<-writeDone
		return fmt.Errorf("tma: upload failed: %w", err)
	}
	defer response.Body.Close()
	if writeErr := <-writeDone; writeErr != nil {
		return fmt.Errorf("tma: write upload: %w", writeErr)
	}
	return decodeResponse(response, target)
}

func writeMultipartUpload(writer *multipart.Writer, fields map[string]string, fieldName string, file UploadFile) error {
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return err
		}
	}
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": fieldName, "filename": file.FileName})
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", disposition)
	contentType := strings.TrimSpace(file.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file.Body)
	return err
}

func (c *Client) OpenStream(ctx context.Context, path string) (*http.Response, error) {
	request, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.streamHTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("tma: stream request failed: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, decodeResponse(response, nil)
	}
	return response, nil
}

// Events opens a reconnecting structured SSE stream for an event endpoint.
func (c *Client) Events(ctx context.Context, path string, afterSeq int64) (*EventStream, error) {
	if afterSeq < 0 {
		return nil, errors.New("tma: after_seq must not be negative")
	}
	return newEventStream(ctx, c, path, afterSeq), nil
}

func (c *Client) newRequest(ctx context.Context, method string, path string, body io.Reader) (*http.Request, error) {
	if c == nil || c.baseURL == nil {
		return nil, errors.New("tma: client is not initialized")
	}
	parsedPath, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("tma: parse request path: %w", err)
	}
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(parsedPath.Path, "/")
	requestURL.RawPath = strings.TrimRight(c.baseURL.EscapedPath(), "/") + "/" + strings.TrimLeft(parsedPath.EscapedPath(), "/")
	if requestURL.RawPath == requestURL.Path {
		requestURL.RawPath = ""
	}
	requestURL.RawQuery = parsedPath.RawQuery
	requestURL.Fragment = ""
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("tma: create request: %w", err)
	}
	token := c.token
	if c.tokenSource != nil {
		resolved, err := c.tokenSource(ctx)
		if err != nil {
			return nil, fmt.Errorf("tma: resolve bearer token: %w", err)
		}
		token = strings.TrimSpace(resolved)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request, nil
}
