package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	DefaultStreamableHTTPHostIdleTimeout   = 10 * time.Minute
	DefaultStreamableHTTPHostSweepInterval = time.Minute
	DefaultStreamableHTTPHostMaxSessions   = 64
)

type StreamableHTTPHostOptions struct {
	IdleTimeout   time.Duration
	SweepInterval time.Duration
	MaxSessions   int
	EgressPolicy  *EgressPolicy
	Logger        *slog.Logger
	Now           func() time.Time
}

type StreamableHTTPHostStats struct {
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
	DeleteErrorsTotal          int64            `json:"delete_errors_total"`
	ToolsListChangedTotal      int64            `json:"tools_list_changed_total"`
	ResourcesListChangedTotal  int64            `json:"resources_list_changed_total"`
	PromptsListChangedTotal    int64            `json:"prompts_list_changed_total"`
	ProgressNotificationsTotal int64            `json:"progress_notifications_total"`
	LogMessagesTotal           int64            `json:"log_messages_total"`
	InvalidNotificationsTotal  int64            `json:"invalid_notifications_total"`
	LogMessagesByLevel         map[string]int64 `json:"log_messages_by_level,omitempty"`
	EgressPolicyEnabled        bool             `json:"egress_policy_enabled"`
	EgressAllowHTTP            bool             `json:"egress_allow_http"`
	EgressAllowPrivateNetworks bool             `json:"egress_allow_private_networks"`
	EgressAllowedHostCount     int              `json:"egress_allowed_host_count"`
	EgressAllowedCIDRCount     int              `json:"egress_allowed_cidr_count"`
	EgressBlockedTotal         int64            `json:"egress_blocked_total"`
}

type StreamableHTTPHost struct {
	mu                         sync.Mutex
	entries                    map[string]*streamableHTTPHostEntry
	idleTimeout                time.Duration
	sweepInterval              time.Duration
	maxSessions                int
	logger                     *slog.Logger
	now                        func() time.Time
	egressPolicy               *EgressPolicy
	stop                       chan struct{}
	done                       chan struct{}
	closed                     bool
	startsTotal                int64
	stopsTotal                 int64
	discardsTotal              int64
	reapedTotal                int64
	evictionsTotal             int64
	rejectionsTotal            int64
	deleteErrorsTotal          int64
	toolsListChangedTotal      int64
	resourcesListChangedTotal  int64
	promptsListChangedTotal    int64
	progressNotificationsTotal int64
	logMessagesTotal           int64
	invalidNotificationsTotal  int64
	logMessagesByLevel         map[string]int64
}

type streamableHTTPHostEntry struct {
	scope       string
	client      Client
	gate        chan struct{}
	refs        int
	lastUsed    time.Time
	session     *httpSession
	initialized InitializeResult
	cancel      context.CancelFunc
}

func NewStreamableHTTPHost(options StreamableHTTPHostOptions) *StreamableHTTPHost {
	idleTimeout := options.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = DefaultStreamableHTTPHostIdleTimeout
	}
	sweepInterval := options.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = DefaultStreamableHTTPHostSweepInterval
	}
	maxSessions := options.MaxSessions
	if maxSessions <= 0 {
		maxSessions = DefaultStreamableHTTPHostMaxSessions
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	host := &StreamableHTTPHost{
		entries:       map[string]*streamableHTTPHostEntry{},
		idleTimeout:   idleTimeout,
		sweepInterval: sweepInterval,
		maxSessions:   maxSessions,
		logger:        logger,
		now:           now,
		egressPolicy:  options.EgressPolicy,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go host.sweepLoop()
	return host
}

func (h *StreamableHTTPHost) Client(scope string, client Client) HostedClient {
	if client.EgressPolicy == nil {
		client.EgressPolicy = h.egressPolicy
	}
	return HostedClient{httpHost: h, scope: strings.TrimSpace(scope), client: client}
}

func (h *StreamableHTTPHost) EgressPolicy() *EgressPolicy {
	if h == nil {
		return nil
	}
	return h.egressPolicy
}

func (h *StreamableHTTPHost) Stats() StreamableHTTPHostStats {
	if h == nil {
		return StreamableHTTPHostStats{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	stats := StreamableHTTPHostStats{
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
		DeleteErrorsTotal:          h.deleteErrorsTotal,
		ToolsListChangedTotal:      h.toolsListChangedTotal,
		ResourcesListChangedTotal:  h.resourcesListChangedTotal,
		PromptsListChangedTotal:    h.promptsListChangedTotal,
		ProgressNotificationsTotal: h.progressNotificationsTotal,
		LogMessagesTotal:           h.logMessagesTotal,
		InvalidNotificationsTotal:  h.invalidNotificationsTotal,
		LogMessagesByLevel:         cloneInt64Map(h.logMessagesByLevel),
	}
	egress := h.egressPolicy.Summary()
	stats.EgressPolicyEnabled = egress.Enabled
	stats.EgressAllowHTTP = egress.AllowHTTP
	stats.EgressAllowPrivateNetworks = egress.AllowPrivateNetworks
	stats.EgressAllowedHostCount = egress.AllowedHostCount
	stats.EgressAllowedCIDRCount = egress.AllowedCIDRCount
	stats.EgressBlockedTotal = egress.BlockedTotal
	for _, entry := range h.entries {
		if entry.refs > 0 {
			stats.InUseSessions++
		}
	}
	return stats
}

func (h *StreamableHTTPHost) ReapIdle(now time.Time) int {
	if h == nil {
		return 0
	}
	cutoff := now.Add(-h.idleTimeout)
	h.mu.Lock()
	var stale []*streamableHTTPHostEntry
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

func (h *StreamableHTTPHost) Close() {
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
	entries := make([]*streamableHTTPHostEntry, 0, len(h.entries))
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

func (h *StreamableHTTPHost) sweepLoop() {
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

func (h *StreamableHTTPHost) acquire(ctx context.Context, scope string, client Client) (*streamableHTTPHostEntry, error) {
	key, err := streamableHTTPHostKey(scope, client)
	if err != nil {
		return nil, err
	}
	now := h.now()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("mcp streamable_http host is closed")
	}
	entry := h.entries[key]
	var evicted *streamableHTTPHostEntry
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
				return nil, fmt.Errorf("mcp streamable_http host capacity reached (%d sessions)", h.maxSessions)
			}
			delete(h.entries, evictKey)
			h.evictionsTotal++
		}
		entry = &streamableHTTPHostEntry{
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

func (h *StreamableHTTPHost) release(entry *streamableHTTPHostEntry) {
	<-entry.gate
	h.releaseRef(entry, true)
}

func (h *StreamableHTTPHost) releaseRef(entry *streamableHTTPHostEntry, used bool) {
	h.mu.Lock()
	if entry.refs > 0 {
		entry.refs--
	}
	if used {
		entry.lastUsed = h.now()
	}
	h.mu.Unlock()
}

func (h *StreamableHTTPHost) withSession(ctx context.Context, scope string, client Client, fn func(*httpSession) error) (InitializeResult, error) {
	if h == nil {
		return InitializeResult{}, errors.New("mcp streamable_http host is required")
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
		sess, startErr := newHTTPSession(ctx, entry.client)
		if startErr != nil {
			return InitializeResult{}, startErr
		}
		listenerCtx, cancel := context.WithCancel(context.Background())
		sess.onNotification = func(method string, params json.RawMessage) {
			h.recordServerNotification(entry.scope, method, params)
		}
		initialized, initializeErr := sess.initializeWithListener(ctx, listenerCtx)
		if initializeErr != nil {
			cancel()
			h.terminateSession(sess)
			return InitializeResult{}, initializeErr
		}
		entry.session = sess
		entry.initialized = initialized
		entry.cancel = cancel
		h.mu.Lock()
		h.startsTotal++
		h.mu.Unlock()
		h.logger.Debug("mcp streamable_http host session started", "scope", entry.scope)
	} else if refreshErr := refreshHTTPSessionAuthorization(ctx, entry.client, entry.session); refreshErr != nil {
		return entry.initialized, refreshErr
	}
	runErr := fn(entry.session)
	initialized := entry.initialized
	if shouldDiscardHostedHTTPSession(runErr) {
		h.discardEntrySession(entry, "session_invalid")
	}
	return initialized, runErr
}

func refreshHTTPSessionAuthorization(ctx context.Context, client Client, sess *httpSession) error {
	if client.OAuth == nil {
		return nil
	}
	token, err := client.OAuthCache.Token(ctx, sess.client, client.OAuth)
	if err != nil {
		return err
	}
	sess.setHeader("Authorization", "Bearer "+token.AccessToken)
	return nil
}

func shouldDiscardHostedHTTPSession(err error) bool {
	var statusErr httpStatusError
	return errors.As(err, &statusErr) && (statusErr.Status == http.StatusNotFound || statusErr.Status == http.StatusGone)
}

func (h *StreamableHTTPHost) discardEntrySession(entry *streamableHTTPHostEntry, reason string) {
	if entry.session == nil {
		return
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	h.terminateSession(entry.session)
	entry.session = nil
	entry.initialized = InitializeResult{}
	entry.cancel = nil
	h.logger.Debug("mcp streamable_http host session discarded", "scope", entry.scope, "reason", reason)
	h.mu.Lock()
	h.stopsTotal++
	h.discardsTotal++
	h.mu.Unlock()
}

func (h *StreamableHTTPHost) closeEntry(entry *streamableHTTPHostEntry, reason string) {
	entry.gate <- struct{}{}
	if entry.session != nil {
		if entry.cancel != nil {
			entry.cancel()
		}
		h.terminateSession(entry.session)
		entry.session = nil
		entry.initialized = InitializeResult{}
		entry.cancel = nil
		h.logger.Debug("mcp streamable_http host session stopped", "scope", entry.scope, "reason", reason)
		h.mu.Lock()
		h.stopsTotal++
		h.mu.Unlock()
	}
	<-entry.gate
}

func (h *StreamableHTTPHost) terminateSession(sess *httpSession) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sess.terminate(ctx); err != nil {
		h.logger.Debug("mcp streamable_http session delete failed", "error", err)
		h.mu.Lock()
		h.deleteErrorsTotal++
		h.mu.Unlock()
	}
}

func (h *StreamableHTTPHost) recordServerNotification(scope string, method string, params json.RawMessage) {
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
		h.logger.Debug("mcp streamable_http host notification received", "scope", scope, "method", method, "level", loggingLevel)
		return
	}
	h.logger.Debug("mcp streamable_http host notification received", "scope", scope, "method", method)
}

func streamableHTTPHostKey(scope string, client Client) (string, error) {
	payload, err := json.Marshal(struct {
		Scope        string
		URL          string
		Headers      map[string]string
		OAuth        *OAuthClientCredentials
		Listen       bool
		Roots        []Root
		Sampling     *SamplingConfig
		Elicitation  *ElicitationConfig
		LoggingLevel string
		HTTPClient   string
		EgressPolicy string
	}{
		Scope:        strings.TrimSpace(scope),
		URL:          strings.TrimSpace(client.URL),
		Headers:      client.Headers,
		OAuth:        client.OAuth,
		Listen:       client.Listen,
		Roots:        client.Roots,
		Sampling:     client.Sampling,
		Elicitation:  client.Elicitation,
		LoggingLevel: NormalizeLoggingLevel(client.LoggingLevel),
		HTTPClient:   httpClientIdentity(client.HTTPClient),
		EgressPolicy: client.EgressPolicy.Fingerprint(),
	})
	if err != nil {
		return "", fmt.Errorf("encode mcp streamable_http host key: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func httpClientIdentity(client *http.Client) string {
	if client == nil || client == http.DefaultClient {
		return "default"
	}
	return fmt.Sprintf("%p", client)
}
