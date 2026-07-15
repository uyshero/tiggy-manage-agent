package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const securityAuditScopeName = "tma.security.authorization"

type AuthorizationDecisionEvent struct {
	ID                   string    `json:"id,omitempty"`
	At                   time.Time `json:"at"`
	Outcome              string    `json:"outcome"`
	Reason               string    `json:"reason"`
	AuthType             string    `json:"auth_type"`
	Method               string    `json:"method,omitempty"`
	Path                 string    `json:"path,omitempty"`
	RequiredRole         string    `json:"required_role,omitempty"`
	Subject              string    `json:"subject,omitempty"`
	OrganizationID       string    `json:"organization_id,omitempty"`
	WorkspaceID          string    `json:"workspace_id,omitempty"`
	OwnerID              string    `json:"owner_id,omitempty"`
	Roles                []string  `json:"roles,omitempty"`
	AuthorizationSources []string  `json:"authorization_sources,omitempty"`
	Detail               string    `json:"detail,omitempty"`
}

type AuthorizationDecisionSink interface {
	EnqueueAuthorizationDecision(event AuthorizationDecisionEvent) bool
}

type SecurityAuditMetricsProvider interface {
	SecurityAuditMetrics() SecurityAuditExporterMetrics
}

type SecurityAuditExporterConfig struct {
	Endpoint      string
	Token         string
	ServiceName   string
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	HTTPClient    *http.Client
	Logger        *slog.Logger
}

type SecurityAuditExporterMetrics struct {
	Enabled                                 bool
	Durable                                 bool
	QueueDepth                              int64
	QueueCapacity                           int64
	Sent                                    int64
	Failed                                  int64
	Dropped                                 int64
	PersistenceFailed                       int64
	Pending                                 int64
	Delivering                              int64
	Delivered                               int64
	DeadLetter                              int64
	OldestPendingSeconds                    int64
	IntegrityStatusAvailable                bool
	IntegrityUnconfiguredBlocking           int64
	IntegrityHistoricalUnidentifiedBlocking int64
	IntegrityInactiveKeyBlocking            int64
	IntegrityKeysReadyToRemove              int64
	IntegrityKeysRemovalBlocked             int64
}

type SecurityAuditExporter struct {
	endpoint      string
	token         string
	serviceName   string
	batchSize     int
	flushInterval time.Duration
	client        *http.Client
	logger        *slog.Logger
	queue         chan AuthorizationDecisionEvent
	stop          chan struct{}
	done          chan struct{}

	mu      sync.Mutex
	closed  bool
	sent    int64
	failed  int64
	dropped int64
}

func NewSecurityAuditExporter(config SecurityAuditExporterConfig) (*SecurityAuditExporter, error) {
	endpoint, err := NormalizeOTLPLogsEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		return nil, nil
	}
	if config.QueueSize < 1 {
		return nil, errors.New("security audit queue size must be positive")
	}
	if config.BatchSize < 1 || config.BatchSize > config.QueueSize {
		return nil, errors.New("security audit batch size must be positive and no larger than queue size")
	}
	if config.FlushInterval <= 0 {
		return nil, errors.New("security audit flush interval must be positive")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	serviceName := strings.TrimSpace(config.ServiceName)
	if serviceName == "" {
		serviceName = "tiggy-manage-agent"
	}
	exporter := &SecurityAuditExporter{
		endpoint: endpoint, token: strings.TrimSpace(config.Token), serviceName: serviceName,
		batchSize: config.BatchSize, flushInterval: config.FlushInterval,
		client: client, logger: logger, queue: make(chan AuthorizationDecisionEvent, config.QueueSize),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go exporter.run()
	return exporter, nil
}

func (e *SecurityAuditExporter) EnqueueAuthorizationDecision(event AuthorizationDecisionEvent) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	event = normalizeAuthorizationDecisionEvent(event)
	select {
	case e.queue <- event:
		return true
	default:
		e.dropped++
		return false
	}
}

func (e *SecurityAuditExporter) SecurityAuditMetrics() SecurityAuditExporterMetrics {
	if e == nil {
		return SecurityAuditExporterMetrics{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return SecurityAuditExporterMetrics{
		Enabled: true, QueueDepth: int64(len(e.queue)), QueueCapacity: int64(cap(e.queue)),
		Sent: e.sent, Failed: e.failed, Dropped: e.dropped,
	}
}

func (e *SecurityAuditExporter) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.stop)
	}
	e.mu.Unlock()
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *SecurityAuditExporter) run() {
	defer close(e.done)
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()
	batch := make([]AuthorizationDecisionEvent, 0, e.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		pending := append([]AuthorizationDecisionEvent(nil), batch...)
		batch = batch[:0]
		if err := e.push(context.Background(), pending); err != nil {
			e.mu.Lock()
			e.failed += int64(len(pending))
			e.mu.Unlock()
			e.logger.Warn("security audit OTLP export failed", "event_count", len(pending), "endpoint", e.endpoint, "error", err)
			return
		}
		e.mu.Lock()
		e.sent += int64(len(pending))
		e.mu.Unlock()
	}
	for {
		select {
		case event := <-e.queue:
			batch = append(batch, event)
			if len(batch) >= e.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-e.stop:
			for {
				select {
				case event := <-e.queue:
					batch = append(batch, event)
					if len(batch) >= e.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (e *SecurityAuditExporter) push(ctx context.Context, events []AuthorizationDecisionEvent) error {
	return pushAuthorizationDecisionEvents(ctx, e.client, e.endpoint, e.token, e.serviceName, events)
}

func pushAuthorizationDecisionEvents(ctx context.Context, client *http.Client, endpoint string, token string, serviceName string, events []AuthorizationDecisionEvent) error {
	payload := otlpAuthorizationLogsPayload(serviceName, events)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode security audit OTLP logs: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create security audit OTLP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("push security audit OTLP logs: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("push security audit OTLP logs returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func NormalizeOTLPLogsEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("security audit OTLP endpoint must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("security audit OTLP endpoint must use http or https")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	switch {
	case parsed.Path == "":
		parsed.Path = "/v1/logs"
	case strings.HasSuffix(parsed.Path, "/v1/traces"):
		parsed.Path = strings.TrimSuffix(parsed.Path, "/v1/traces") + "/v1/logs"
	case strings.HasSuffix(parsed.Path, "/v1/logs"):
	default:
		parsed.Path += "/v1/logs"
	}
	return parsed.String(), nil
}

func normalizeAuthorizationDecisionEvent(event AuthorizationDecisionEvent) AuthorizationDecisionEvent {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	} else {
		event.At = event.At.UTC()
	}
	event.Outcome = strings.TrimSpace(event.Outcome)
	event.Reason = strings.TrimSpace(event.Reason)
	event.AuthType = strings.TrimSpace(event.AuthType)
	event.Roles = sortedUniqueStrings(event.Roles)
	event.AuthorizationSources = sortedUniqueStrings(event.AuthorizationSources)
	return event
}

func newSecurityAuditEventID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate security audit event id: %w", err)
	}
	return fmt.Sprintf("saud_%x", random[:]), nil
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func otlpAuthorizationLogsPayload(serviceName string, events []AuthorizationDecisionEvent) map[string]any {
	records := make([]map[string]any, 0, len(events))
	for _, event := range events {
		severityNumber := 9
		severityText := "INFO"
		if event.Outcome != "allowed" {
			severityNumber = 13
			severityText = "WARN"
		}
		attributes := map[string]any{
			"event.name": "authorization_decision", "auth.outcome": event.Outcome,
			"auth.reason": event.Reason, "auth.type": event.AuthType,
		}
		addOTLPAttribute(attributes, "event.id", event.ID)
		addOTLPAttribute(attributes, "http.request.method", event.Method)
		addOTLPAttribute(attributes, "url.path", event.Path)
		addOTLPAttribute(attributes, "auth.required_role", event.RequiredRole)
		addOTLPAttribute(attributes, "enduser.id", event.Subject)
		addOTLPAttribute(attributes, "tma.organization.id", event.OrganizationID)
		addOTLPAttribute(attributes, "tma.workspace.id", event.WorkspaceID)
		addOTLPAttribute(attributes, "tma.owner.id", event.OwnerID)
		addOTLPAttribute(attributes, "auth.roles", event.Roles)
		addOTLPAttribute(attributes, "auth.authorization_sources", event.AuthorizationSources)
		addOTLPAttribute(attributes, "error.message", event.Detail)
		records = append(records, map[string]any{
			"timeUnixNano":   strconv.FormatInt(event.At.UnixNano(), 10),
			"severityNumber": severityNumber,
			"severityText":   severityText,
			"body":           map[string]any{"stringValue": "authorization decision"},
			"attributes":     otlpAttributes(attributes),
		})
	}
	return map[string]any{"resourceLogs": []any{map[string]any{
		"resource": map[string]any{"attributes": otlpAttributes(map[string]any{"service.name": serviceName})},
		"scopeLogs": []any{map[string]any{
			"scope":      map[string]any{"name": securityAuditScopeName},
			"logRecords": records,
		}},
	}}}
}

func addOTLPAttribute(attributes map[string]any, key string, value any) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			attributes[key] = typed
		}
	case []string:
		if len(typed) > 0 {
			attributes[key] = typed
		}
	}
}

func otlpAttributes(values map[string]any) []map[string]any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		result = append(result, map[string]any{"key": key, "value": otlpAnyValue(values[key])})
	}
	return result
}

func otlpAnyValue(value any) map[string]any {
	switch typed := value.(type) {
	case []string:
		values := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			values = append(values, map[string]any{"stringValue": item})
		}
		return map[string]any{"arrayValue": map[string]any{"values": values}}
	default:
		return map[string]any{"stringValue": fmt.Sprint(value)}
	}
}
