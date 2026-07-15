package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2025-06-18"

type Client struct {
	Transport    string
	StdioFraming string
	Command      string
	Args         []string
	Env          map[string]string
	Cwd          string
	URL          string
	Headers      map[string]string
	OAuth        *OAuthClientCredentials
	OAuthCache   *OAuthTokenCache
	Listen       bool
	Roots        []Root
	Sampling     *SamplingConfig
	Elicitation  *ElicitationConfig
	LoggingLevel string
	HTTPClient   *http.Client
	EgressPolicy *EgressPolicy
}

type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion,omitempty"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	} `json:"serverInfo,omitempty"`
	Capabilities ServerCapabilities `json:"capabilities,omitempty"`
}

type ServerCapabilities struct {
	Tools       json.RawMessage            `json:"tools,omitempty"`
	Resources   json.RawMessage            `json:"resources,omitempty"`
	Prompts     json.RawMessage            `json:"prompts,omitempty"`
	Completions json.RawMessage            `json:"completions,omitempty"`
	Logging     json.RawMessage            `json:"logging,omitempty"`
	Raw         map[string]json.RawMessage `json:"-"`
}

func (c *ServerCapabilities) UnmarshalJSON(raw []byte) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	c.Raw = values
	c.Tools = values["tools"]
	c.Resources = values["resources"]
	c.Prompts = values["prompts"]
	c.Completions = values["completions"]
	c.Logging = values["logging"]
	return nil
}

func (c ServerCapabilities) Names() []string {
	names := make([]string, 0, 5)
	if len(c.Tools) > 0 && string(c.Tools) != "null" {
		names = append(names, "tools")
	}
	if len(c.Resources) > 0 && string(c.Resources) != "null" {
		names = append(names, "resources")
	}
	if len(c.Prompts) > 0 && string(c.Prompts) != "null" {
		names = append(names, "prompts")
	}
	if len(c.Completions) > 0 && string(c.Completions) != "null" {
		names = append(names, "completions")
	}
	if len(c.Logging) > 0 && string(c.Logging) != "null" {
		names = append(names, "logging")
	}
	sort.Strings(names)
	return names
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations ToolAnnotations `json:"annotations,omitempty"`
}

type ToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint,omitempty"`
	DestructiveHint bool `json:"destructiveHint,omitempty"`
	IdempotentHint  bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   bool `json:"openWorldHint,omitempty"`
}

type ToolListResult struct {
	Tools      []ToolDefinition `json:"tools"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ResourceListResult struct {
	Resources  []ResourceDefinition `json:"resources"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string          `json:"uriTemplate"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	MimeType    string          `json:"mimeType,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}

type ResourceTemplateListResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
}

type ResourceReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

type ResourceContent struct {
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type PromptDefinition struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptListResult struct {
	Prompts    []PromptDefinition `json:"prompts"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

type PromptGetResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

const (
	CompletionReferencePrompt   = "ref/prompt"
	CompletionReferenceResource = "ref/resource"
)

type CompletionReference struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

type CompletionArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CompletionContext struct {
	Arguments map[string]string `json:"arguments,omitempty"`
}

type CompletionResult struct {
	Completion CompletionValues `json:"completion"`
}

type CompletionValues struct {
	Values  []string `json:"values"`
	Total   int      `json:"total,omitempty"`
	HasMore bool     `json:"hasMore,omitempty"`
}

type PromptMessage struct {
	Role    string      `json:"role"`
	Content ContentItem `json:"content"`
}

type ToolCallResult struct {
	Content           []ContentItem   `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
	Meta              map[string]any  `json:"_meta,omitempty"`
}

type ContentItem struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MimeType string          `json:"mimeType,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

type session struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         *bufio.Reader
	roots          []Root
	sampling       *SamplingConfig
	elicitation    *ElicitationConfig
	loggingLevel   string
	stdioFraming   string
	onNotification func(string, json.RawMessage)
	nextID         int64
	writeMu        sync.Mutex
}

type httpSession struct {
	mu              sync.RWMutex
	client          *http.Client
	endpoint        string
	headers         map[string]string
	sessionID       string
	protocolVersion string
	listen          bool
	roots           []Root
	sampling        *SamplingConfig
	elicitation     *ElicitationConfig
	loggingLevel    string
	onNotification  func(string, json.RawMessage)
	lastEventID     string
	nextID          int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcServerResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcCallError struct {
	Method  string
	Code    int
	Message string
}

type httpStatusError struct {
	Status  int
	Message string
}

func (e httpStatusError) Error() string {
	return e.Message
}

func (e rpcCallError) Error() string {
	return fmt.Sprintf("mcp %s failed (%d): %s", e.Method, e.Code, e.Message)
}

func (c Client) ListTools(ctx context.Context) (InitializeResult, []ToolDefinition, error) {
	if c.transport() == TransportStreamableHTTP {
		return c.listToolsHTTP(ctx)
	}
	var (
		initialized InitializeResult
		tools       []ToolDefinition
	)
	err := c.withSession(ctx, func(sess *session) error {
		initResult, err := sess.initialize(ctx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listToolsWithPagination(func(params map[string]any, result *ToolListResult) error {
			return sess.call(ctx, "tools/list", params, result)
		})
		if err != nil {
			return err
		}
		tools = listed
		return nil
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, tools, nil
}

func (c Client) ListResources(ctx context.Context) (InitializeResult, []ResourceDefinition, error) {
	if c.transport() == TransportStreamableHTTP {
		return c.listResourcesHTTP(ctx)
	}
	var (
		initialized InitializeResult
		resources   []ResourceDefinition
	)
	err := c.withSession(ctx, func(sess *session) error {
		initResult, err := sess.initialize(ctx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listResourcesWithPagination(func(params map[string]any, result *ResourceListResult) error {
			return sess.call(ctx, "resources/list", params, result)
		})
		if err != nil {
			if isRPCMethodNotFound(err) {
				resources = nil
				return nil
			}
			return err
		}
		resources = listed
		return nil
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, resources, nil
}

func (c Client) ListPrompts(ctx context.Context) (InitializeResult, []PromptDefinition, error) {
	if c.transport() == TransportStreamableHTTP {
		return c.listPromptsHTTP(ctx)
	}
	var (
		initialized InitializeResult
		prompts     []PromptDefinition
	)
	err := c.withSession(ctx, func(sess *session) error {
		initResult, err := sess.initialize(ctx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listPromptsWithPagination(func(params map[string]any, result *PromptListResult) error {
			return sess.call(ctx, "prompts/list", params, result)
		})
		if err != nil {
			if isRPCMethodNotFound(err) {
				prompts = nil
				return nil
			}
			return err
		}
		prompts = listed
		return nil
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, prompts, nil
}

func (c Client) ListResourceTemplates(ctx context.Context) (InitializeResult, []ResourceTemplate, error) {
	if c.transport() == TransportStreamableHTTP {
		return c.listResourceTemplatesHTTP(ctx)
	}
	var (
		initialized InitializeResult
		templates   []ResourceTemplate
	)
	err := c.withSession(ctx, func(sess *session) error {
		initResult, err := sess.initialize(ctx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listResourceTemplatesWithPagination(func(params map[string]any, result *ResourceTemplateListResult) error {
			return sess.call(ctx, "resources/templates/list", params, result)
		})
		if isRPCMethodNotFound(err) {
			templates = nil
			return nil
		}
		templates = listed
		return err
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, templates, nil
}

func (c Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolCallResult, error) {
	if c.transport() == TransportStreamableHTTP {
		return c.callToolHTTP(ctx, name, arguments)
	}
	var result ToolCallResult
	err := c.withSession(ctx, func(sess *session) error {
		if _, err := sess.initialize(ctx); err != nil {
			return err
		}
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		params := map[string]any{
			"name":      name,
			"arguments": rawJSONObject(arguments),
		}
		return sess.call(ctx, "tools/call", params, &result)
	})
	return result, err
}

func (c Client) GetPrompt(ctx context.Context, name string, arguments json.RawMessage) (PromptGetResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return PromptGetResult{}, fmt.Errorf("mcp prompt name is required")
	}
	if c.transport() == TransportStreamableHTTP {
		return c.getPromptHTTP(ctx, name, arguments)
	}
	var result PromptGetResult
	err := c.withSession(ctx, func(sess *session) error {
		if _, err := sess.initialize(ctx); err != nil {
			return err
		}
		params := map[string]any{
			"name":      name,
			"arguments": rawJSONObject(arguments),
		}
		return sess.call(ctx, "prompts/get", params, &result)
	})
	return result, err
}

func (c Client) ReadResource(ctx context.Context, uri string) (ResourceReadResult, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ResourceReadResult{}, fmt.Errorf("mcp resource uri is required")
	}
	if c.transport() == TransportStreamableHTTP {
		return c.readResourceHTTP(ctx, uri)
	}
	var result ResourceReadResult
	err := c.withSession(ctx, func(sess *session) error {
		if _, err := sess.initialize(ctx); err != nil {
			return err
		}
		return sess.call(ctx, "resources/read", map[string]any{"uri": uri}, &result)
	})
	return result, err
}

func (c Client) Complete(ctx context.Context, reference CompletionReference, argument CompletionArgument, completionContext CompletionContext) (CompletionResult, error) {
	params, err := completionRequestParams(reference, argument, completionContext)
	if err != nil {
		return CompletionResult{}, err
	}
	if c.transport() == TransportStreamableHTTP {
		return c.completeHTTP(ctx, params)
	}
	var result CompletionResult
	err = c.withSession(ctx, func(sess *session) error {
		if _, err := sess.initialize(ctx); err != nil {
			return err
		}
		return sess.call(ctx, "completion/complete", params, &result)
	})
	if err != nil {
		return CompletionResult{}, err
	}
	return validateCompletionResult(result)
}

func (c Client) transport() string {
	transport := strings.TrimSpace(strings.ToLower(c.Transport))
	if transport == "" {
		return TransportStdio
	}
	return transport
}

func (c Client) listToolsHTTP(ctx context.Context) (InitializeResult, []ToolDefinition, error) {
	var (
		initialized InitializeResult
		tools       []ToolDefinition
	)
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		initResult, err := sess.initialize(runCtx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listToolsWithPagination(func(params map[string]any, result *ToolListResult) error {
			return sess.call(runCtx, "tools/list", params, result)
		})
		if err != nil {
			return err
		}
		tools = listed
		return nil
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, tools, nil
}

func (c Client) listResourcesHTTP(ctx context.Context) (InitializeResult, []ResourceDefinition, error) {
	var (
		initialized InitializeResult
		resources   []ResourceDefinition
	)
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		initResult, err := sess.initialize(runCtx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listResourcesWithPagination(func(params map[string]any, result *ResourceListResult) error {
			return sess.call(runCtx, "resources/list", params, result)
		})
		if err != nil {
			if isRPCMethodNotFound(err) {
				resources = nil
				return nil
			}
			return err
		}
		resources = listed
		return nil
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, resources, nil
}

func (c Client) listPromptsHTTP(ctx context.Context) (InitializeResult, []PromptDefinition, error) {
	var (
		initialized InitializeResult
		prompts     []PromptDefinition
	)
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		initResult, err := sess.initialize(runCtx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listPromptsWithPagination(func(params map[string]any, result *PromptListResult) error {
			return sess.call(runCtx, "prompts/list", params, result)
		})
		if err != nil {
			if isRPCMethodNotFound(err) {
				prompts = nil
				return nil
			}
			return err
		}
		prompts = listed
		return nil
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, prompts, nil
}

func (c Client) listResourceTemplatesHTTP(ctx context.Context) (InitializeResult, []ResourceTemplate, error) {
	var (
		initialized InitializeResult
		templates   []ResourceTemplate
	)
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		initResult, err := sess.initialize(runCtx)
		if err != nil {
			return err
		}
		initialized = initResult
		listed, err := listResourceTemplatesWithPagination(func(params map[string]any, result *ResourceTemplateListResult) error {
			return sess.call(runCtx, "resources/templates/list", params, result)
		})
		if isRPCMethodNotFound(err) {
			templates = nil
			return nil
		}
		templates = listed
		return err
	})
	if err != nil {
		return InitializeResult{}, nil, err
	}
	return initialized, templates, nil
}

func (c Client) callToolHTTP(ctx context.Context, name string, arguments json.RawMessage) (ToolCallResult, error) {
	var result ToolCallResult
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		if _, err := sess.initialize(runCtx); err != nil {
			return err
		}
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		params := map[string]any{
			"name":      name,
			"arguments": rawJSONObject(arguments),
		}
		return sess.call(runCtx, "tools/call", params, &result)
	})
	return result, err
}

func (c Client) getPromptHTTP(ctx context.Context, name string, arguments json.RawMessage) (PromptGetResult, error) {
	var result PromptGetResult
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		if _, err := sess.initialize(runCtx); err != nil {
			return err
		}
		params := map[string]any{
			"name":      name,
			"arguments": rawJSONObject(arguments),
		}
		return sess.call(runCtx, "prompts/get", params, &result)
	})
	return result, err
}

func (c Client) readResourceHTTP(ctx context.Context, uri string) (ResourceReadResult, error) {
	var result ResourceReadResult
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		if _, err := sess.initialize(runCtx); err != nil {
			return err
		}
		return sess.call(runCtx, "resources/read", map[string]any{"uri": uri}, &result)
	})
	return result, err
}

func (c Client) completeHTTP(ctx context.Context, params map[string]any) (CompletionResult, error) {
	var result CompletionResult
	err := c.withHTTPSession(ctx, func(runCtx context.Context, sess *httpSession) error {
		if _, err := sess.initialize(runCtx); err != nil {
			return err
		}
		return sess.call(runCtx, "completion/complete", params, &result)
	})
	if err != nil {
		return CompletionResult{}, err
	}
	return validateCompletionResult(result)
}

func (c Client) withHTTPSession(ctx context.Context, fn func(context.Context, *httpSession) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess, err := newHTTPSession(runCtx, c)
	if err != nil {
		return err
	}
	return fn(runCtx, sess)
}

func newHTTPSession(ctx context.Context, c Client) (*httpSession, error) {
	endpoint := strings.TrimSpace(c.URL)
	if endpoint == "" {
		return nil, fmt.Errorf("mcp streamable_http url is required")
	}
	httpClient := c.HTTPClient
	httpClient = c.EgressPolicy.HTTPClient(httpClient)
	headers := cloneStringMap(c.Headers)
	if c.OAuth != nil {
		if hasAuthorizationStringHeader(headers) {
			return nil, fmt.Errorf("mcp streamable_http cannot set both oauth and Authorization header")
		}
		token, err := c.OAuthCache.Token(ctx, httpClient, c.OAuth)
		if err != nil {
			return nil, err
		}
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Authorization"] = "Bearer " + token.AccessToken
	}
	return &httpSession{
		client:       httpClient,
		endpoint:     endpoint,
		headers:      headers,
		listen:       c.Listen,
		roots:        append([]Root(nil), c.Roots...),
		sampling:     cloneSamplingConfig(c.Sampling),
		elicitation:  cloneElicitationConfig(c.Elicitation),
		loggingLevel: NormalizeLoggingLevel(c.LoggingLevel),
		nextID:       1,
	}, nil
}

func (c Client) withSession(ctx context.Context, fn func(*session) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sess, stderrBuffer, err := startStdioSession(c)
	if err != nil {
		return err
	}

	runErr := fn(sess)
	waitErr := stopStdioSession(sess, ctx.Err() != nil)
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if stderr := strings.TrimSpace(stderrBuffer.String()); stderr != "" {
				return fmt.Errorf("%w%s", ctxErr, formatStderr(stderr))
			}
			return ctxErr
		}
		if stderr := strings.TrimSpace(stderrBuffer.String()); stderr != "" {
			return fmt.Errorf("%w: %s", runErr, stderr)
		}
		return runErr
	}
	if waitErr != nil && ctx.Err() == nil {
		return fmt.Errorf("mcp command %s exited: %w%s", strings.TrimSpace(c.Command), waitErr, formatStderr(stderrBuffer.String()))
	}
	return nil
}

func startStdioSession(c Client) (*session, *bytes.Buffer, error) {
	command := strings.TrimSpace(c.Command)
	if command == "" {
		return nil, nil, fmt.Errorf("mcp command is required")
	}
	cmd := exec.Command(command, c.Args...)
	cmd.Dir = c.Cwd
	cmd.Env = mergeEnvPairs(os.Environ(), c.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("open mcp stdout: %w", err)
	}
	stderrBuffer := &bytes.Buffer{}
	cmd.Stderr = stderrBuffer
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("start mcp command %s: %w", command, err)
	}
	return &session{
		cmd:          cmd,
		stdin:        stdin,
		stdout:       bufio.NewReader(stdout),
		roots:        append([]Root(nil), c.Roots...),
		sampling:     cloneSamplingConfig(c.Sampling),
		elicitation:  cloneElicitationConfig(c.Elicitation),
		loggingLevel: NormalizeLoggingLevel(c.LoggingLevel),
		stdioFraming: effectiveClientStdioFraming(c.StdioFraming),
		nextID:       1,
	}, stderrBuffer, nil
}

func stopStdioSession(sess *session, force bool) error {
	if sess == nil || sess.cmd == nil {
		return nil
	}
	if sess.stdin != nil {
		_ = sess.stdin.Close()
	}
	if force && sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() {
		done <- sess.cmd.Wait()
	}()
	select {
	case err := <-done:
		if force {
			return nil
		}
		return err
	case <-time.After(500 * time.Millisecond):
		if sess.cmd.Process != nil {
			_ = sess.cmd.Process.Kill()
		}
		<-done
		return nil
	}
}

func (s *session) initialize(ctx context.Context) (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    clientCapabilities(s.roots),
		"clientInfo": map[string]any{
			"name":    "tiggy-manage-agent",
			"version": "1.0.0",
		},
	}
	var result InitializeResult
	if err := s.call(ctx, "initialize", params, &result); err != nil {
		return InitializeResult{}, err
	}
	if err := s.notify("notifications/initialized", map[string]any{}); err != nil {
		return InitializeResult{}, err
	}
	if err := s.configureLogging(ctx, result.Capabilities); err != nil {
		return InitializeResult{}, err
	}
	return result, nil
}

func (s *httpSession) initialize(ctx context.Context) (InitializeResult, error) {
	return s.initializeWithListener(ctx, ctx)
}

func (s *httpSession) initializeWithListener(ctx context.Context, listenerCtx context.Context) (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    clientCapabilities(s.roots),
		"clientInfo": map[string]any{
			"name":    "tiggy-manage-agent",
			"version": "1.0.0",
		},
	}
	var result InitializeResult
	if err := s.call(ctx, "initialize", params, &result); err != nil {
		return InitializeResult{}, err
	}
	s.mu.Lock()
	s.protocolVersion = protocolVersion
	s.mu.Unlock()
	if err := s.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return InitializeResult{}, err
	}
	if err := s.configureLogging(ctx, result.Capabilities); err != nil {
		return InitializeResult{}, err
	}
	if s.listen {
		go s.listenStream(listenerCtx)
	}
	return result, nil
}

func (s *session) configureLogging(ctx context.Context, capabilities ServerCapabilities) error {
	if s.loggingLevel == "" {
		return nil
	}
	if !hasServerCapability(capabilities.Logging) {
		return fmt.Errorf("mcp logging level %q configured but server does not declare logging capability", s.loggingLevel)
	}
	return s.call(ctx, "logging/setLevel", map[string]any{"level": s.loggingLevel}, nil)
}

func (s *httpSession) configureLogging(ctx context.Context, capabilities ServerCapabilities) error {
	if s.loggingLevel == "" {
		return nil
	}
	if !hasServerCapability(capabilities.Logging) {
		return fmt.Errorf("mcp logging level %q configured but server does not declare logging capability", s.loggingLevel)
	}
	return s.call(ctx, "logging/setLevel", map[string]any{"level": s.loggingLevel}, nil)
}

func hasServerCapability(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

func (s *session) call(ctx context.Context, method string, params any, result any) error {
	requestID := s.nextID
	s.nextID++
	if err := s.writeMessage(rpcRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  params,
	}); err != nil {
		return err
	}
	done := make(chan struct{})
	if ctx != nil {
		defer close(done)
		go s.cancelStdioRequestOnContextDone(ctx, done, requestID)
	}
	for {
		raw, err := s.readMessage()
		if err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
			}
			return err
		}
		var response rpcResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("decode mcp response: %w", err)
		}
		if response.Method != "" {
			if hasRPCID(response.ID) {
				if err := s.replyServerRequest(response); err != nil {
					return err
				}
			} else if s.onNotification != nil {
				s.onNotification(response.Method, response.Params)
			}
			continue
		}
		if !rpcIDMatchesInt(response.ID, requestID) {
			continue
		}
		if response.Error != nil {
			return rpcCallError{Method: method, Code: response.Error.Code, Message: response.Error.Message}
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode mcp %s result: %w", method, err)
		}
		return nil
	}
}

func (s *httpSession) call(ctx context.Context, method string, params any, result any) error {
	requestID := s.nextID
	s.nextID++
	responses, err := s.post(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	for _, response := range responses {
		if response.Method != "" {
			if hasRPCID(response.ID) {
				if err := s.replyServerRequest(ctx, response); err != nil {
					return err
				}
			} else if s.onNotification != nil {
				s.onNotification(response.Method, response.Params)
			}
			continue
		}
		if !rpcIDMatchesInt(response.ID, requestID) {
			continue
		}
		if response.Error != nil {
			return rpcCallError{Method: method, Code: response.Error.Code, Message: response.Error.Message}
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode mcp %s result: %w", method, err)
		}
		return nil
	}
	return fmt.Errorf("mcp %s response missing request id %d", method, requestID)
}

func (s *session) notify(method string, params any) error {
	return s.writeMessage(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func (s *session) cancelStdioRequestOnContextDone(ctx context.Context, done <-chan struct{}, requestID int64) {
	select {
	case <-ctx.Done():
	case <-done:
		return
	}
	var reason string
	if err := ctx.Err(); err != nil {
		reason = strings.TrimSpace(err.Error())
	}
	if reason == "" {
		reason = "request canceled"
	}
	_ = s.notify("notifications/cancelled", map[string]any{
		"requestId": requestID,
		"reason":    reason,
	})
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	}
}

func (s *httpSession) notify(ctx context.Context, method string, params any) error {
	_, err := s.post(ctx, rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	return err
}

func clientCapabilities(roots []Root) map[string]any {
	capabilities := map[string]any{}
	if len(roots) > 0 {
		capabilities["roots"] = map[string]any{"listChanged": false}
	}
	return capabilities
}

func hasRPCID(id json.RawMessage) bool {
	value := strings.TrimSpace(string(id))
	return value != "" && value != "null"
}

func rpcIDMatchesInt(id json.RawMessage, expected int64) bool {
	if !hasRPCID(id) {
		return false
	}
	var value int64
	return json.Unmarshal(id, &value) == nil && value == expected
}

func serverRequestResponse(requestID json.RawMessage, method string, roots []Root, sampling *SamplingConfig, elicitation *ElicitationConfig) rpcServerResponse {
	response := rpcServerResponse{JSONRPC: "2.0", ID: append(json.RawMessage(nil), requestID...)}
	switch method {
	case "ping":
		response.Result = map[string]any{}
	case "roots/list":
		items := make([]map[string]string, 0, len(roots))
		for _, root := range roots {
			entry := map[string]string{"uri": root.URI}
			if strings.TrimSpace(root.Name) != "" {
				entry["name"] = strings.TrimSpace(root.Name)
			}
			items = append(items, entry)
		}
		response.Result = map[string]any{"roots": items}
	case "sampling/createMessage":
		message := "MCP sampling/createMessage is disabled for this MCP server"
		if sampling != nil && sampling.Enabled {
			message = "MCP sampling/createMessage is not implemented; remote model sampling requires an audited sampler backend"
		}
		response.Error = &rpcError{Code: -32000, Message: message}
	case "elicitation/create":
		message := "MCP elicitation/create is disabled for this MCP server"
		if elicitation != nil && elicitation.Enabled {
			message = "MCP elicitation/create is not implemented; remote user elicitation requires an audited interaction backend"
		}
		response.Error = &rpcError{Code: -32000, Message: message}
	default:
		response.Error = &rpcError{Code: -32601, Message: "client does not support server request method: " + method}
	}
	return response
}

func (s *session) replyServerRequest(message rpcResponse) error {
	return s.writeMessage(serverRequestResponse(message.ID, message.Method, s.roots, s.sampling, s.elicitation))
}

func (s *httpSession) replyServerRequest(ctx context.Context, message rpcResponse) error {
	_, err := s.post(ctx, serverRequestResponse(message.ID, message.Method, s.roots, s.sampling, s.elicitation))
	return err
}

func listToolsWithPagination(call func(map[string]any, *ToolListResult) error) ([]ToolDefinition, error) {
	var tools []ToolDefinition
	err := listPaginated(func(params map[string]any) (string, error) {
		var result ToolListResult
		if err := call(params, &result); err != nil {
			return "", err
		}
		tools = append(tools, result.Tools...)
		return result.NextCursor, nil
	})
	return tools, err
}

func listResourcesWithPagination(call func(map[string]any, *ResourceListResult) error) ([]ResourceDefinition, error) {
	var resources []ResourceDefinition
	err := listPaginated(func(params map[string]any) (string, error) {
		var result ResourceListResult
		if err := call(params, &result); err != nil {
			return "", err
		}
		resources = append(resources, result.Resources...)
		return result.NextCursor, nil
	})
	return resources, err
}

func listResourceTemplatesWithPagination(call func(map[string]any, *ResourceTemplateListResult) error) ([]ResourceTemplate, error) {
	var templates []ResourceTemplate
	err := listPaginated(func(params map[string]any) (string, error) {
		var result ResourceTemplateListResult
		if err := call(params, &result); err != nil {
			return "", err
		}
		templates = append(templates, result.ResourceTemplates...)
		return result.NextCursor, nil
	})
	return templates, err
}

func completionRequestParams(reference CompletionReference, argument CompletionArgument, completionContext CompletionContext) (map[string]any, error) {
	reference.Type = strings.TrimSpace(reference.Type)
	reference.Name = strings.TrimSpace(reference.Name)
	reference.URI = strings.TrimSpace(reference.URI)
	switch reference.Type {
	case CompletionReferencePrompt:
		if reference.Name == "" {
			return nil, fmt.Errorf("mcp completion prompt name is required")
		}
		reference.URI = ""
	case CompletionReferenceResource:
		if reference.URI == "" {
			return nil, fmt.Errorf("mcp completion resource uri is required")
		}
		reference.Name = ""
	default:
		return nil, fmt.Errorf("mcp completion reference type %q is not supported", reference.Type)
	}
	argument.Name = strings.TrimSpace(argument.Name)
	if argument.Name == "" {
		return nil, fmt.Errorf("mcp completion argument name is required")
	}
	params := map[string]any{
		"ref":      reference,
		"argument": argument,
	}
	if len(completionContext.Arguments) > 0 {
		params["context"] = completionContext
	}
	return params, nil
}

func validateCompletionResult(result CompletionResult) (CompletionResult, error) {
	if len(result.Completion.Values) > 100 {
		return CompletionResult{}, fmt.Errorf("mcp completion returned %d values; maximum is 100", len(result.Completion.Values))
	}
	return result, nil
}

func listPromptsWithPagination(call func(map[string]any, *PromptListResult) error) ([]PromptDefinition, error) {
	var prompts []PromptDefinition
	err := listPaginated(func(params map[string]any) (string, error) {
		var result PromptListResult
		if err := call(params, &result); err != nil {
			return "", err
		}
		prompts = append(prompts, result.Prompts...)
		return result.NextCursor, nil
	})
	return prompts, err
}

func listPaginated(call func(map[string]any) (string, error)) error {
	var cursor string
	seen := map[string]bool{}
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		nextCursor, err := call(params)
		if err != nil {
			return err
		}
		nextCursor = strings.TrimSpace(nextCursor)
		if nextCursor == "" {
			return nil
		}
		if seen[nextCursor] {
			return fmt.Errorf("mcp list pagination repeated cursor %q", nextCursor)
		}
		seen[nextCursor] = true
		cursor = nextCursor
	}
}

func isRPCMethodNotFound(err error) bool {
	var rpcErr rpcCallError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32601
}

func IsMethodNotFound(err error) bool {
	return isRPCMethodNotFound(err)
}

func (s *httpSession) post(ctx context.Context, message any) ([]rpcResponse, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode mcp request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create mcp http request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	sessionID, currentProtocol, headers, _ := s.requestState()
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	if currentProtocol != "" {
		request.Header.Set("Mcp-Protocol-Version", currentProtocol)
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		request.Header.Set(key, value)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("post mcp http request: %w", sanitizeEgressError(err))
	}
	defer response.Body.Close()
	if sessionID := strings.TrimSpace(response.Header.Get("Mcp-Session-Id")); sessionID != "" {
		s.mu.Lock()
		s.sessionID = sessionID
		s.mu.Unlock()
	}
	if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if discovery, discoveryErr := discoverOAuthFromUnauthorized(ctx, s.client, s.endpoint, response); discoveryErr == nil {
			if formatted := formatOAuthDiscovery(discovery); formatted != "" {
				message := fmt.Sprintf("mcp http status 401: authorization required (%s)%s", formatted, formatHTTPBody(body))
				return nil, httpStatusError{Status: response.StatusCode, Message: message}
			}
		}
		message := fmt.Sprintf("mcp http status 401: authorization required%s", formatHTTPBody(body))
		return nil, httpStatusError{Status: response.StatusCode, Message: message}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		message := fmt.Sprintf("mcp http status %d%s", response.StatusCode, formatHTTPBody(body))
		return nil, httpStatusError{Status: response.StatusCode, Message: message}
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	switch strings.ToLower(contentType) {
	case "application/json", "":
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, fmt.Errorf("read mcp http response: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, nil
		}
		var rpc rpcResponse
		if err := json.Unmarshal(raw, &rpc); err != nil {
			return nil, fmt.Errorf("decode mcp http json response: %w", err)
		}
		return []rpcResponse{rpc}, nil
	case "text/event-stream":
		responses, err := readSSEJSONRPC(response.Body)
		if err != nil {
			return nil, err
		}
		return responses, nil
	default:
		return nil, fmt.Errorf("unsupported mcp http content type %q", response.Header.Get("Content-Type"))
	}
}

func (s *session) writeMessage(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode mcp request: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stdioFraming == StdioFramingJSONLines {
		if _, err := s.stdin.Write(append(payload, '\n')); err != nil {
			return fmt.Errorf("write mcp json line: %w", err)
		}
		return nil
	}
	if _, err := io.WriteString(s.stdin, "Content-Length: "+strconv.Itoa(len(payload))+"\r\n\r\n"); err != nil {
		return fmt.Errorf("write mcp header: %w", err)
	}
	if _, err := s.stdin.Write(payload); err != nil {
		return fmt.Errorf("write mcp payload: %w", err)
	}
	return nil
}

func (s *session) readMessage() ([]byte, error) {
	if s.stdioFraming == StdioFramingJSONLines {
		for {
			line, err := s.stdout.ReadBytes('\n')
			if err != nil {
				return nil, fmt.Errorf("read mcp json line: %w", err)
			}
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				return line, nil
			}
		}
	}
	contentLength := -1
	for {
		line, err := s.stdout.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read mcp header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			value := strings.TrimSpace(line[len("Content-Length:"):])
			length, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid mcp content length %q", value)
			}
			contentLength = length
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing mcp content length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(s.stdout, payload); err != nil {
		return nil, fmt.Errorf("read mcp payload: %w", err)
	}
	return payload, nil
}

func effectiveClientStdioFraming(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), StdioFramingJSONLines) {
		return StdioFramingJSONLines
	}
	return StdioFramingContentLength
}

func rawJSONObject(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func envPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}

func mergeEnvPairs(base []string, overrides map[string]string) []string {
	merged := append([]string(nil), base...)
	for key, value := range overrides {
		prefix := key + "="
		filtered := merged[:0]
		for _, entry := range merged {
			if !strings.HasPrefix(entry, prefix) {
				filtered = append(filtered, entry)
			}
		}
		merged = append(filtered, key+"="+value)
	}
	return merged
}

type sseEvent struct {
	ID   string
	Data string
}

func readSSEJSONRPC(reader io.Reader) ([]rpcResponse, error) {
	var responses []rpcResponse
	err := readSSEEvents(reader, func(event sseEvent) error {
		raw := strings.TrimSpace(event.Data)
		if raw == "" || raw == "[DONE]" {
			return nil
		}
		var response rpcResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			return fmt.Errorf("decode mcp sse response: %w", err)
		}
		responses = append(responses, response)
		return nil
	})
	return responses, err
}

func readSSEEvents(reader io.Reader, handle func(sseEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		eventID   string
		dataLines []string
	)
	flush := func() error {
		if len(dataLines) == 0 {
			eventID = ""
			return nil
		}
		event := sseEvent{
			ID:   eventID,
			Data: strings.Join(dataLines, "\n"),
		}
		eventID = ""
		dataLines = nil
		return handle(event)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		field = strings.TrimSpace(field)
		switch field {
		case "id":
			eventID = strings.TrimSpace(value)
		case "data":
			value = strings.TrimPrefix(value, " ")
			dataLines = append(dataLines, value)
		default:
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read mcp sse response: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	return nil
}

func (s *httpSession) listenStream(ctx context.Context) {
	for {
		retry, err := s.listenStreamOnce(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Default().Warn("mcp streamable_http listener failed", "error", err)
		}
		if !retry || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *httpSession) listenStreamOnce(ctx context.Context) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Accept", "text/event-stream")
	sessionID, currentProtocol, headers, lastEventID := s.requestState()
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	if currentProtocol != "" {
		request.Header.Set("Mcp-Protocol-Version", currentProtocol)
	}
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		request.Header.Set(key, value)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return false, sanitizeEgressError(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusMethodNotAllowed {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return false, fmt.Errorf("mcp streamable_http listener status %d%s", response.StatusCode, formatHTTPBody(body))
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if strings.ToLower(contentType) != "text/event-stream" {
		return false, fmt.Errorf("unsupported mcp streamable_http listener content type %q", response.Header.Get("Content-Type"))
	}
	if err := s.readListenerEvents(ctx, response.Body); err != nil && ctx.Err() == nil {
		return false, err
	}
	return ctx.Err() == nil, nil
}

func (s *httpSession) readListenerEvents(ctx context.Context, reader io.Reader) error {
	return readSSEEvents(reader, func(event sseEvent) error {
		if event.ID != "" {
			s.mu.Lock()
			s.lastEventID = event.ID
			s.mu.Unlock()
		}
		raw := strings.TrimSpace(event.Data)
		if raw == "" || raw == "[DONE]" {
			return nil
		}
		var message rpcResponse
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			return fmt.Errorf("decode mcp listener event: %w", err)
		}
		if message.Method == "" {
			return nil
		}
		if !hasRPCID(message.ID) {
			if s.onNotification != nil {
				s.onNotification(message.Method, message.Params)
			}
			return nil
		}
		return s.replyServerRequest(ctx, message)
	})
}

func (s *httpSession) requestState() (string, string, map[string]string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID, s.protocolVersion, cloneStringMap(s.headers), s.lastEventID
}

func (s *httpSession) setHeader(key string, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headers == nil {
		s.headers = map[string]string{}
	}
	s.headers[key] = value
}

func (s *httpSession) terminate(ctx context.Context) error {
	sessionID, currentProtocol, headers, _ := s.requestState()
	if sessionID == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.endpoint, nil)
	if err != nil {
		return fmt.Errorf("create mcp http termination request: %w", err)
	}
	request.Header.Set("Mcp-Session-Id", sessionID)
	if currentProtocol != "" {
		request.Header.Set("Mcp-Protocol-Version", currentProtocol)
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key != "" {
			request.Header.Set(key, value)
		}
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("delete mcp http session: %w", sanitizeEgressError(err))
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("delete mcp http session status %d%s", response.StatusCode, formatHTTPBody(body))
	}
	return nil
}

func formatStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

func formatHTTPBody(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	return ": " + string(body)
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func hasAuthorizationStringHeader(headers map[string]string) bool {
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), "Authorization") {
			return true
		}
	}
	return false
}

func cloneSamplingConfig(value *SamplingConfig) *SamplingConfig {
	if value == nil {
		return nil
	}
	return &SamplingConfig{Enabled: value.Enabled}
}

func cloneElicitationConfig(value *ElicitationConfig) *ElicitationConfig {
	if value == nil {
		return nil
	}
	return &ElicitationConfig{Enabled: value.Enabled}
}
