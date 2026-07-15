package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	DefaultStdioHostIdleTimeout   = 10 * time.Minute
	DefaultStdioHostSweepInterval = time.Minute
	DefaultStdioHostMaxSessions   = 64
)

type StdioHostOptions struct {
	IdleTimeout   time.Duration
	SweepInterval time.Duration
	MaxSessions   int
	Logger        *slog.Logger
	Now           func() time.Time
}

type StdioHostStats struct {
	Sessions                   int              `json:"sessions"`
	InUseSessions              int              `json:"in_use_sessions"`
	MaxSessions                int              `json:"max_sessions"`
	IdleTimeoutSeconds         int64            `json:"idle_timeout_seconds"`
	SweepIntervalSeconds       int64            `json:"sweep_interval_seconds"`
	StartsTotal                int64            `json:"starts_total"`
	StopsTotal                 int64            `json:"stops_total"`
	DiscardsTotal              int64            `json:"discards_total"`
	ReapedTotal                int64            `json:"reaped_total"`
	EvictionsTotal             int64            `json:"evictions_total"`
	RejectionsTotal            int64            `json:"rejections_total"`
	ToolsListChangedTotal      int64            `json:"tools_list_changed_total"`
	ResourcesListChangedTotal  int64            `json:"resources_list_changed_total"`
	PromptsListChangedTotal    int64            `json:"prompts_list_changed_total"`
	ProgressNotificationsTotal int64            `json:"progress_notifications_total"`
	LogMessagesTotal           int64            `json:"log_messages_total"`
	InvalidNotificationsTotal  int64            `json:"invalid_notifications_total"`
	LogMessagesByLevel         map[string]int64 `json:"log_messages_by_level,omitempty"`
}

type StdioHost struct {
	mu                         sync.Mutex
	entries                    map[string]*stdioHostEntry
	idleTimeout                time.Duration
	sweepInterval              time.Duration
	maxSessions                int
	logger                     *slog.Logger
	now                        func() time.Time
	stop                       chan struct{}
	done                       chan struct{}
	closed                     bool
	startsTotal                int64
	stopsTotal                 int64
	discardsTotal              int64
	reapedTotal                int64
	evictionsTotal             int64
	rejectionsTotal            int64
	toolsListChangedTotal      int64
	resourcesListChangedTotal  int64
	promptsListChangedTotal    int64
	progressNotificationsTotal int64
	logMessagesTotal           int64
	invalidNotificationsTotal  int64
	logMessagesByLevel         map[string]int64
}

type stdioHostEntry struct {
	scope       string
	client      Client
	gate        chan struct{}
	refs        int
	lastUsed    time.Time
	session     *session
	initialized InitializeResult
}

type HostedClient struct {
	host     *StdioHost
	httpHost *StreamableHTTPHost
	scope    string
	client   Client
}

func NewStdioHost(options StdioHostOptions) *StdioHost {
	idleTimeout := options.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = DefaultStdioHostIdleTimeout
	}
	sweepInterval := options.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = DefaultStdioHostSweepInterval
	}
	maxSessions := options.MaxSessions
	if maxSessions <= 0 {
		maxSessions = DefaultStdioHostMaxSessions
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	host := &StdioHost{
		entries:       map[string]*stdioHostEntry{},
		idleTimeout:   idleTimeout,
		sweepInterval: sweepInterval,
		maxSessions:   maxSessions,
		logger:        logger,
		now:           now,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go host.sweepLoop()
	return host
}

func (h *StdioHost) Client(scope string, client Client) HostedClient {
	return HostedClient{host: h, scope: strings.TrimSpace(scope), client: client}
}

func (h *StdioHost) Stats() StdioHostStats {
	if h == nil {
		return StdioHostStats{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	stats := StdioHostStats{
		Sessions:                   len(h.entries),
		MaxSessions:                h.maxSessions,
		IdleTimeoutSeconds:         int64(h.idleTimeout / time.Second),
		SweepIntervalSeconds:       int64(h.sweepInterval / time.Second),
		StartsTotal:                h.startsTotal,
		StopsTotal:                 h.stopsTotal,
		DiscardsTotal:              h.discardsTotal,
		ReapedTotal:                h.reapedTotal,
		EvictionsTotal:             h.evictionsTotal,
		RejectionsTotal:            h.rejectionsTotal,
		ToolsListChangedTotal:      h.toolsListChangedTotal,
		ResourcesListChangedTotal:  h.resourcesListChangedTotal,
		PromptsListChangedTotal:    h.promptsListChangedTotal,
		ProgressNotificationsTotal: h.progressNotificationsTotal,
		LogMessagesTotal:           h.logMessagesTotal,
		InvalidNotificationsTotal:  h.invalidNotificationsTotal,
		LogMessagesByLevel:         cloneInt64Map(h.logMessagesByLevel),
	}
	for _, entry := range h.entries {
		if entry.refs > 0 {
			stats.InUseSessions++
		}
	}
	return stats
}

func (h *StdioHost) ReapIdle(now time.Time) int {
	if h == nil {
		return 0
	}
	cutoff := now.Add(-h.idleTimeout)
	h.mu.Lock()
	var stale []*stdioHostEntry
	for key, entry := range h.entries {
		if entry.refs != 0 || entry.lastUsed.After(cutoff) {
			continue
		}
		delete(h.entries, key)
		stale = append(stale, entry)
	}
	h.reapedTotal += int64(len(stale))
	h.mu.Unlock()
	for _, entry := range stale {
		h.closeEntry(entry, "idle_timeout")
	}
	return len(stale)
}

func (h *StdioHost) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		<-h.done
		return
	}
	h.closed = true
	close(h.stop)
	entries := make([]*stdioHostEntry, 0, len(h.entries))
	for key, entry := range h.entries {
		delete(h.entries, key)
		entries = append(entries, entry)
	}
	h.mu.Unlock()
	for _, entry := range entries {
		h.closeEntry(entry, "host_shutdown")
	}
	<-h.done
}

func (h *StdioHost) sweepLoop() {
	defer close(h.done)
	ticker := time.NewTicker(h.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			h.ReapIdle(now)
		case <-h.stop:
			return
		}
	}
}

func (h *StdioHost) closeEntry(entry *stdioHostEntry, reason string) {
	entry.gate <- struct{}{}
	if entry.session != nil {
		_ = stopStdioSession(entry.session, false)
		entry.session = nil
		entry.initialized = InitializeResult{}
		h.logger.Debug("mcp stdio host session stopped", "scope", entry.scope, "reason", reason)
		h.mu.Lock()
		h.stopsTotal++
		h.mu.Unlock()
	}
	<-entry.gate
}

func (h *StdioHost) acquire(ctx context.Context, scope string, client Client) (*stdioHostEntry, error) {
	key, err := stdioHostKey(scope, client)
	if err != nil {
		return nil, err
	}
	now := h.now()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("mcp stdio host is closed")
	}
	entry := h.entries[key]
	var evicted *stdioHostEntry
	if entry == nil {
		if len(h.entries) >= h.maxSessions {
			var evictKey string
			for candidateKey, candidate := range h.entries {
				if candidate.refs != 0 {
					continue
				}
				if evicted == nil || candidate.lastUsed.Before(evicted.lastUsed) {
					evictKey = candidateKey
					evicted = candidate
				}
			}
			if evicted == nil {
				h.rejectionsTotal++
				h.mu.Unlock()
				return nil, fmt.Errorf("mcp stdio host capacity reached (%d sessions)", h.maxSessions)
			}
			delete(h.entries, evictKey)
			h.evictionsTotal++
		}
		entry = &stdioHostEntry{
			scope:    scope,
			client:   client,
			gate:     make(chan struct{}, 1),
			lastUsed: now,
		}
		h.entries[key] = entry
	}
	entry.refs++
	h.mu.Unlock()
	if evicted != nil {
		h.closeEntry(evicted, "capacity")
	}
	select {
	case entry.gate <- struct{}{}:
		return entry, nil
	case <-ctx.Done():
		h.releaseRef(entry, false)
		return nil, ctx.Err()
	}
}

func (h *StdioHost) release(entry *stdioHostEntry) {
	<-entry.gate
	h.releaseRef(entry, true)
}

func (h *StdioHost) releaseRef(entry *stdioHostEntry, used bool) {
	h.mu.Lock()
	if entry.refs > 0 {
		entry.refs--
	}
	if used {
		entry.lastUsed = h.now()
	}
	h.mu.Unlock()
}

func (h *StdioHost) withSession(ctx context.Context, scope string, client Client, fn func(*session) error) (InitializeResult, error) {
	if h == nil {
		return InitializeResult{}, errors.New("mcp stdio host is required")
	}
	if err := ctx.Err(); err != nil {
		return InitializeResult{}, err
	}
	entry, err := h.acquire(ctx, scope, client)
	if err != nil {
		return InitializeResult{}, err
	}
	defer h.release(entry)
	if entry.session == nil {
		sess, _, startErr := startStdioSession(entry.client)
		if startErr != nil {
			return InitializeResult{}, startErr
		}
		entry.session = sess
		sess.onNotification = func(method string, params json.RawMessage) {
			h.recordServerNotification(entry.scope, method, params)
		}
		h.mu.Lock()
		h.startsTotal++
		h.mu.Unlock()
		initialized, initializeErr := sess.initialize(ctx)
		if initializeErr != nil {
			h.discardEntrySession(entry, "initialize_failed")
			return InitializeResult{}, initializeErr
		}
		entry.initialized = initialized
		h.logger.Debug("mcp stdio host session started", "scope", entry.scope)
	}
	runErr := fn(entry.session)
	initialized := entry.initialized
	if shouldDiscardHostedSession(ctx, runErr) {
		h.discardEntrySession(entry, "request_failed")
	}
	return initialized, runErr
}

func (h *StdioHost) recordServerNotification(scope string, method string, params json.RawMessage) {
	loggingLevel := ""
	h.mu.Lock()
	switch method {
	case "notifications/tools/list_changed":
		h.toolsListChangedTotal++
	case "notifications/resources/list_changed":
		h.resourcesListChangedTotal++
	case "notifications/prompts/list_changed":
		h.promptsListChangedTotal++
	case "notifications/progress":
		h.progressNotificationsTotal++
		if !validProgressNotification(params) {
			h.invalidNotificationsTotal++
		}
	case "notifications/message":
		h.logMessagesTotal++
		level, valid := loggingNotificationLevel(params)
		loggingLevel = level
		if !valid {
			h.invalidNotificationsTotal++
		}
		if h.logMessagesByLevel == nil {
			h.logMessagesByLevel = map[string]int64{}
		}
		h.logMessagesByLevel[level]++
	default:
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	if loggingLevel != "" {
		h.logger.Debug("mcp stdio host notification received", "scope", scope, "method", method, "level", loggingLevel)
		return
	}
	h.logger.Debug("mcp stdio host notification received", "scope", scope, "method", method)
}

func (h *StdioHost) discardEntrySession(entry *stdioHostEntry, reason string) {
	if entry.session == nil {
		return
	}
	_ = stopStdioSession(entry.session, true)
	entry.session = nil
	entry.initialized = InitializeResult{}
	h.logger.Debug("mcp stdio host session discarded", "scope", entry.scope, "reason", reason)
	h.mu.Lock()
	h.stopsTotal++
	h.discardsTotal++
	h.mu.Unlock()
}

func shouldDiscardHostedSession(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return true
	}
	var rpcErr rpcCallError
	return !errors.As(err, &rpcErr)
}

func stdioHostKey(scope string, client Client) (string, error) {
	payload, err := json.Marshal(struct {
		Scope        string
		StdioFraming string
		Command      string
		Args         []string
		Env          map[string]string
		Cwd          string
		Roots        []Root
		Sampling     *SamplingConfig
		Elicitation  *ElicitationConfig
		LoggingLevel string
	}{
		Scope:        strings.TrimSpace(scope),
		StdioFraming: effectiveClientStdioFraming(client.StdioFraming),
		Command:      strings.TrimSpace(client.Command),
		Args:         client.Args,
		Env:          client.Env,
		Cwd:          client.Cwd,
		Roots:        client.Roots,
		Sampling:     client.Sampling,
		Elicitation:  client.Elicitation,
		LoggingLevel: NormalizeLoggingLevel(client.LoggingLevel),
	})
	if err != nil {
		return "", fmt.Errorf("encode mcp stdio host key: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (c HostedClient) ListTools(ctx context.Context) (InitializeResult, []ToolDefinition, error) {
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		var listed []ToolDefinition
		initialized, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			tools, listErr := listToolsWithPagination(func(params map[string]any, result *ToolListResult) error {
				return sess.call(ctx, "tools/list", params, result)
			})
			listed = tools
			return listErr
		})
		return initialized, listed, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.ListTools(ctx)
	}
	var listed []ToolDefinition
	initialized, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		tools, listErr := listToolsWithPagination(func(params map[string]any, result *ToolListResult) error {
			return sess.call(ctx, "tools/list", params, result)
		})
		listed = tools
		return listErr
	})
	return initialized, listed, err
}

func (c HostedClient) ListResources(ctx context.Context) (InitializeResult, []ResourceDefinition, error) {
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		var listed []ResourceDefinition
		initialized, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			resources, listErr := listResourcesWithPagination(func(params map[string]any, result *ResourceListResult) error {
				return sess.call(ctx, "resources/list", params, result)
			})
			if isRPCMethodNotFound(listErr) {
				listed = nil
				return nil
			}
			listed = resources
			return listErr
		})
		return initialized, listed, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.ListResources(ctx)
	}
	var listed []ResourceDefinition
	initialized, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		resources, listErr := listResourcesWithPagination(func(params map[string]any, result *ResourceListResult) error {
			return sess.call(ctx, "resources/list", params, result)
		})
		if isRPCMethodNotFound(listErr) {
			listed = nil
			return nil
		}
		listed = resources
		return listErr
	})
	return initialized, listed, err
}

func (c HostedClient) ListPrompts(ctx context.Context) (InitializeResult, []PromptDefinition, error) {
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		var listed []PromptDefinition
		initialized, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			prompts, listErr := listPromptsWithPagination(func(params map[string]any, result *PromptListResult) error {
				return sess.call(ctx, "prompts/list", params, result)
			})
			if isRPCMethodNotFound(listErr) {
				listed = nil
				return nil
			}
			listed = prompts
			return listErr
		})
		return initialized, listed, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.ListPrompts(ctx)
	}
	var listed []PromptDefinition
	initialized, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		prompts, listErr := listPromptsWithPagination(func(params map[string]any, result *PromptListResult) error {
			return sess.call(ctx, "prompts/list", params, result)
		})
		if isRPCMethodNotFound(listErr) {
			listed = nil
			return nil
		}
		listed = prompts
		return listErr
	})
	return initialized, listed, err
}

func (c HostedClient) ListResourceTemplates(ctx context.Context) (InitializeResult, []ResourceTemplate, error) {
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		var listed []ResourceTemplate
		initialized, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			templates, listErr := listResourceTemplatesWithPagination(func(params map[string]any, result *ResourceTemplateListResult) error {
				return sess.call(ctx, "resources/templates/list", params, result)
			})
			if isRPCMethodNotFound(listErr) {
				listed = nil
				return nil
			}
			listed = templates
			return listErr
		})
		return initialized, listed, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.ListResourceTemplates(ctx)
	}
	var listed []ResourceTemplate
	initialized, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		templates, listErr := listResourceTemplatesWithPagination(func(params map[string]any, result *ResourceTemplateListResult) error {
			return sess.call(ctx, "resources/templates/list", params, result)
		})
		if isRPCMethodNotFound(listErr) {
			listed = nil
			return nil
		}
		listed = templates
		return listErr
	})
	return initialized, listed, err
}

func (c HostedClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolCallResult, error) {
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		var result ToolCallResult
		_, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			return sess.call(ctx, "tools/call", map[string]any{
				"name":      name,
				"arguments": rawJSONObject(arguments),
			}, &result)
		})
		return result, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.CallTool(ctx, name, arguments)
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var result ToolCallResult
	_, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		return sess.call(ctx, "tools/call", map[string]any{
			"name":      name,
			"arguments": rawJSONObject(arguments),
		}, &result)
	})
	return result, err
}

func (c HostedClient) GetPrompt(ctx context.Context, name string, arguments json.RawMessage) (PromptGetResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return PromptGetResult{}, fmt.Errorf("mcp prompt name is required")
	}
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		var result PromptGetResult
		_, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			return sess.call(ctx, "prompts/get", map[string]any{
				"name":      name,
				"arguments": rawJSONObject(arguments),
			}, &result)
		})
		return result, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.GetPrompt(ctx, name, arguments)
	}
	var result PromptGetResult
	_, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		return sess.call(ctx, "prompts/get", map[string]any{
			"name":      name,
			"arguments": rawJSONObject(arguments),
		}, &result)
	})
	return result, err
}

func (c HostedClient) ReadResource(ctx context.Context, uri string) (ResourceReadResult, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ResourceReadResult{}, fmt.Errorf("mcp resource uri is required")
	}
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		var result ResourceReadResult
		_, err := c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			return sess.call(ctx, "resources/read", map[string]any{"uri": uri}, &result)
		})
		return result, err
	}
	if c.host == nil || c.client.transport() != TransportStdio {
		return c.client.ReadResource(ctx, uri)
	}
	var result ResourceReadResult
	_, err := c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
		return sess.call(ctx, "resources/read", map[string]any{"uri": uri}, &result)
	})
	return result, err
}

func (c HostedClient) Complete(ctx context.Context, reference CompletionReference, argument CompletionArgument, completionContext CompletionContext) (CompletionResult, error) {
	params, err := completionRequestParams(reference, argument, completionContext)
	if err != nil {
		return CompletionResult{}, err
	}
	var result CompletionResult
	if c.httpHost != nil && c.client.transport() == TransportStreamableHTTP {
		_, err = c.httpHost.withSession(ctx, c.scope, c.client, func(sess *httpSession) error {
			return sess.call(ctx, "completion/complete", params, &result)
		})
	} else if c.host != nil && c.client.transport() == TransportStdio {
		_, err = c.host.withSession(ctx, c.scope, c.client, func(sess *session) error {
			return sess.call(ctx, "completion/complete", params, &result)
		})
	} else {
		return c.client.Complete(ctx, reference, argument, completionContext)
	}
	if err != nil {
		return CompletionResult{}, err
	}
	return validateCompletionResult(result)
}
