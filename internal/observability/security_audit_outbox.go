package observability

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type DurableSecurityAuditConfig struct {
	Store             managedagents.SecurityAuditOutboxStore
	Endpoint          string
	Token             string
	IntegrityKey      string
	IntegrityKeyID    string
	IntegrityKeys     map[string]string
	ServiceName       string
	WorkerID          string
	BatchSize         int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	MaxAttempts       int
	RetryInitialDelay time.Duration
	RetryMaxDelay     time.Duration
	Retention         time.Duration
	PruneInterval     time.Duration
	PruneLimit        int
	HTTPClient        *http.Client
	Logger            *slog.Logger
}

type SecurityAuditDeliveryResult struct {
	Claimed      int `json:"claimed"`
	Delivered    int `json:"delivered"`
	Retried      int `json:"retried"`
	DeadLettered int `json:"dead_lettered"`
}

type SecurityAuditDeadLetterReplayer interface {
	ReplayDeadLetters(before time.Time, limit int) (int, error)
}

type SecurityAuditDeadLetterContextReplayer interface {
	ReplayDeadLettersContext(context.Context, time.Time, int) (int, error)
}

type SecurityAuditIntegrityKeyStatusProvider interface {
	SecurityAuditIntegrityKeyStatus() (SecurityAuditIntegrityKeyStatus, error)
}

type SecurityAuditIntegrityKeyContextStatusProvider interface {
	SecurityAuditIntegrityKeyStatusContext(context.Context) (SecurityAuditIntegrityKeyStatus, error)
}

type SecurityAuditIntegrityKeyStatus struct {
	ActiveKeyID                    string                           `json:"active_key_id,omitempty"`
	HistoricalUnidentifiedBlocking int64                            `json:"historical_unidentified_blocking"`
	Keys                           []SecurityAuditIntegrityKeyState `json:"keys"`
}

type SecurityAuditIntegrityKeyState struct {
	KeyID        string `json:"key_id"`
	Configured   bool   `json:"configured"`
	Active       bool   `json:"active"`
	Pending      int64  `json:"pending"`
	Delivering   int64  `json:"delivering"`
	Delivered    int64  `json:"delivered"`
	DeadLetter   int64  `json:"dead_letter"`
	Blocking     int64  `json:"blocking"`
	SafeToRemove bool   `json:"safe_to_remove"`
}

type DurableSecurityAuditPipeline struct {
	store              managedagents.SecurityAuditOutboxStore
	endpoint           string
	token              string
	activeKeyID        string
	integrityKeys      map[string][]byte
	legacyIntegrityKey []byte
	serviceName        string
	workerID           string
	batchSize          int
	pollInterval       time.Duration
	leaseDuration      time.Duration
	maxAttempts        int
	retryInitialDelay  time.Duration
	retryMaxDelay      time.Duration
	retention          time.Duration
	pruneInterval      time.Duration
	pruneLimit         int
	client             *http.Client
	logger             *slog.Logger

	metricsMu         sync.Mutex
	sent              int64
	failed            int64
	persistenceFailed int64
}

func NewDurableSecurityAuditPipeline(config DurableSecurityAuditConfig) (*DurableSecurityAuditPipeline, error) {
	if config.Store == nil {
		return nil, errors.New("durable security audit store is required")
	}
	if _, err := config.Store.GetSecurityAuditOutboxStats(time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("verify durable security audit outbox schema: %w", err)
	}
	endpoint, err := NormalizeOTLPLogsEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		return nil, errors.New("durable security audit OTLP endpoint is required")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New("durable security audit batch size must be between 1 and 1000")
	}
	if config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.LeaseDuration <= config.PollInterval {
		return nil, errors.New("durable security audit lease duration must be greater than the poll interval")
	}
	if config.MaxAttempts < 1 {
		return nil, errors.New("durable security audit max attempts must be positive")
	}
	if config.RetryInitialDelay <= 0 || config.RetryMaxDelay < config.RetryInitialDelay {
		return nil, errors.New("durable security audit retry delays are invalid")
	}
	if config.Retention <= 0 || config.PruneInterval <= 0 || config.PruneLimit < 1 {
		return nil, errors.New("durable security audit retention and prune settings must be positive")
	}
	workerID := strings.TrimSpace(config.WorkerID)
	if workerID == "" {
		workerID, err = newSecurityAuditEventID()
		if err != nil {
			return nil, err
		}
		workerID = "security-audit-" + workerID
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
	activeKeyID, integrityKeys, legacyIntegrityKey, err := buildSecurityAuditIntegrityKeys(config)
	if err != nil {
		return nil, err
	}
	return &DurableSecurityAuditPipeline{
		store: config.Store, endpoint: endpoint, token: strings.TrimSpace(config.Token),
		activeKeyID: activeKeyID, integrityKeys: integrityKeys, legacyIntegrityKey: legacyIntegrityKey,
		serviceName: serviceName, workerID: workerID,
		batchSize: config.BatchSize, pollInterval: config.PollInterval, leaseDuration: config.LeaseDuration,
		maxAttempts: config.MaxAttempts, retryInitialDelay: config.RetryInitialDelay, retryMaxDelay: config.RetryMaxDelay,
		retention: config.Retention, pruneInterval: config.PruneInterval, pruneLimit: config.PruneLimit,
		client: client, logger: logger,
	}, nil
}

func (p *DurableSecurityAuditPipeline) EnqueueAuthorizationDecision(event AuthorizationDecisionEvent) bool {
	if p == nil {
		return false
	}
	if strings.TrimSpace(event.ID) == "" {
		id, err := newSecurityAuditEventID()
		if err != nil {
			p.recordPersistenceFailure(err)
			return false
		}
		event.ID = id
	}
	event = normalizeAuthorizationDecisionEvent(event)
	payload, err := json.Marshal(event)
	if err != nil {
		p.recordPersistenceFailure(fmt.Errorf("encode security audit event: %w", err))
		return false
	}
	key := p.integrityKeys[p.activeKeyID]
	algorithm, digest := securityAuditIntegrity(payload, key)
	keyID := ""
	if algorithm == "hmac-sha256" {
		keyID = p.activeKeyID
	}
	_, err = p.store.RecordSecurityAuditOutbox(managedagents.RecordSecurityAuditOutboxInput{
		ID: event.ID, WorkspaceID: event.WorkspaceID, Payload: payload, IntegrityAlgorithm: algorithm, IntegrityKeyID: keyID,
		IntegrityDigest: digest, CreatedAt: event.At,
	})
	if err != nil {
		p.recordPersistenceFailure(err)
		return false
	}
	return true
}

func (p *DurableSecurityAuditPipeline) Run(ctx context.Context) {
	if p == nil {
		return
	}
	p.logger.Info("durable security audit worker started",
		"worker_id", p.workerID, "poll_interval", p.pollInterval, "batch_size", p.batchSize,
	)
	p.deliverAndLog(ctx)
	p.pruneAndLog()
	deliveryTicker := time.NewTicker(p.pollInterval)
	pruneTicker := time.NewTicker(p.pruneInterval)
	defer deliveryTicker.Stop()
	defer pruneTicker.Stop()
	defer p.logger.Info("durable security audit worker stopped", "worker_id", p.workerID)
	for {
		select {
		case <-ctx.Done():
			return
		case <-deliveryTicker.C:
			p.deliverAndLog(ctx)
		case <-pruneTicker.C:
			p.pruneAndLog()
		}
	}
}

func (p *DurableSecurityAuditPipeline) RunOnce(ctx context.Context, now time.Time) (SecurityAuditDeliveryResult, error) {
	if p == nil {
		return SecurityAuditDeliveryResult{}, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	claimed, err := p.store.ClaimSecurityAuditOutbox(managedagents.ClaimSecurityAuditOutboxInput{
		LeaseOwner: p.workerID, Now: now, LeaseDuration: p.leaseDuration,
		MaxAttempts: p.maxAttempts, Limit: p.batchSize,
	})
	if err != nil {
		return SecurityAuditDeliveryResult{}, err
	}
	result := SecurityAuditDeliveryResult{Claimed: len(claimed)}
	if len(claimed) == 0 {
		return result, nil
	}
	events := make([]AuthorizationDecisionEvent, 0, len(claimed))
	valid := make([]managedagents.SecurityAuditOutboxEvent, 0, len(claimed))
	for _, record := range claimed {
		if !p.verifyIntegrity(record) {
			if _, failErr := p.store.FailSecurityAuditOutbox(managedagents.FailSecurityAuditOutboxInput{
				IDs: []string{record.ID}, LeaseOwner: p.workerID, ErrorMessage: "security audit integrity verification failed",
				DeadLetter: true, At: now,
			}); failErr != nil {
				return result, failErr
			}
			result.DeadLettered++
			continue
		}
		var event AuthorizationDecisionEvent
		if err := json.Unmarshal(record.Payload, &event); err != nil || strings.TrimSpace(event.ID) != record.ID {
			message := "security audit payload is invalid"
			if err == nil {
				message = "security audit payload id does not match outbox id"
			}
			if _, failErr := p.store.FailSecurityAuditOutbox(managedagents.FailSecurityAuditOutboxInput{
				IDs: []string{record.ID}, LeaseOwner: p.workerID, ErrorMessage: message, DeadLetter: true, At: now,
			}); failErr != nil {
				return result, failErr
			}
			result.DeadLettered++
			continue
		}
		events = append(events, event)
		valid = append(valid, record)
	}
	if len(events) == 0 {
		return result, nil
	}
	err = pushAuthorizationDecisionEvents(ctx, p.client, p.endpoint, p.token, p.serviceName, events)
	if err == nil {
		ids := securityAuditOutboxIDs(valid)
		updated, completeErr := p.store.CompleteSecurityAuditOutbox(managedagents.CompleteSecurityAuditOutboxInput{
			IDs: ids, LeaseOwner: p.workerID, At: time.Now().UTC(),
		})
		if completeErr != nil {
			return result, completeErr
		}
		if updated != len(ids) {
			return result, fmt.Errorf("completed %d of %d delivered security audit events", updated, len(ids))
		}
		result.Delivered = len(ids)
		p.metricsMu.Lock()
		p.sent += int64(len(ids))
		p.metricsMu.Unlock()
		return result, nil
	}
	p.metricsMu.Lock()
	p.failed += int64(len(valid))
	p.metricsMu.Unlock()
	retryIDs := make([]string, 0, len(valid))
	deadIDs := make([]string, 0, len(valid))
	maxAttempt := 1
	for _, record := range valid {
		if record.AttemptCount >= p.maxAttempts {
			deadIDs = append(deadIDs, record.ID)
		} else {
			retryIDs = append(retryIDs, record.ID)
			if record.AttemptCount > maxAttempt {
				maxAttempt = record.AttemptCount
			}
		}
	}
	if len(retryIDs) > 0 {
		nextAttemptAt := now.Add(securityAuditRetryDelay(p.retryInitialDelay, p.retryMaxDelay, maxAttempt))
		if _, failErr := p.store.FailSecurityAuditOutbox(managedagents.FailSecurityAuditOutboxInput{
			IDs: retryIDs, LeaseOwner: p.workerID, ErrorMessage: err.Error(), NextAttemptAt: nextAttemptAt, At: now,
		}); failErr != nil {
			return result, failErr
		}
		result.Retried = len(retryIDs)
	}
	if len(deadIDs) > 0 {
		if _, failErr := p.store.FailSecurityAuditOutbox(managedagents.FailSecurityAuditOutboxInput{
			IDs: deadIDs, LeaseOwner: p.workerID, ErrorMessage: err.Error(), DeadLetter: true, At: now,
		}); failErr != nil {
			return result, failErr
		}
		result.DeadLettered += len(deadIDs)
	}
	return result, err
}

func (p *DurableSecurityAuditPipeline) ReplayDeadLetters(before time.Time, limit int) (int, error) {
	if p == nil {
		return 0, nil
	}
	return p.store.ReplaySecurityAuditDeadLetters(managedagents.ReplaySecurityAuditDeadLettersInput{Before: before, Limit: limit})
}

func (p *DurableSecurityAuditPipeline) ReplayDeadLettersContext(ctx context.Context, before time.Time, limit int) (int, error) {
	if p == nil {
		return 0, nil
	}
	return managedagents.ReplaySecurityAuditDeadLettersWithContext(ctx, p.store, managedagents.ReplaySecurityAuditDeadLettersInput{Before: before, Limit: limit})
}

func (p *DurableSecurityAuditPipeline) PruneDelivered(now time.Time) (int, error) {
	if p == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return p.store.PruneDeliveredSecurityAuditOutbox(now.UTC().Add(-p.retention), p.pruneLimit)
}

func (p *DurableSecurityAuditPipeline) SecurityAuditMetrics() SecurityAuditExporterMetrics {
	if p == nil {
		return SecurityAuditExporterMetrics{}
	}
	stats, _ := p.store.GetSecurityAuditOutboxStats(time.Now().UTC())
	p.metricsMu.Lock()
	metrics := SecurityAuditExporterMetrics{
		Enabled: true, Durable: true, QueueDepth: stats.Pending + stats.Delivering,
		Sent: p.sent, Failed: p.failed, PersistenceFailed: p.persistenceFailed,
		Pending: stats.Pending, Delivering: stats.Delivering, Delivered: stats.Delivered,
		DeadLetter: stats.DeadLetter, OldestPendingSeconds: stats.OldestPendingSeconds,
	}
	p.metricsMu.Unlock()
	if keyStatus, err := p.SecurityAuditIntegrityKeyStatus(); err == nil {
		metrics.IntegrityStatusAvailable = true
		metrics.IntegrityHistoricalUnidentifiedBlocking = keyStatus.HistoricalUnidentifiedBlocking
		for _, key := range keyStatus.Keys {
			switch {
			case key.KeyID != "" && !key.Configured:
				metrics.IntegrityUnconfiguredBlocking += key.Blocking
			case key.Configured && !key.Active:
				metrics.IntegrityInactiveKeyBlocking += key.Blocking
			}
			if key.SafeToRemove {
				metrics.IntegrityKeysReadyToRemove++
			} else if key.Configured && !key.Active {
				metrics.IntegrityKeysRemovalBlocked++
			}
		}
	}
	return metrics
}

func (p *DurableSecurityAuditPipeline) OutboxStats(now time.Time) (managedagents.SecurityAuditOutboxStats, error) {
	if p == nil {
		return managedagents.SecurityAuditOutboxStats{}, nil
	}
	return p.store.GetSecurityAuditOutboxStats(now)
}

func (p *DurableSecurityAuditPipeline) OutboxStatsContext(ctx context.Context, now time.Time) (managedagents.SecurityAuditOutboxStats, error) {
	if p == nil {
		return managedagents.SecurityAuditOutboxStats{}, nil
	}
	return managedagents.GetSecurityAuditOutboxStatsWithContext(ctx, p.store, now)
}

func (p *DurableSecurityAuditPipeline) SecurityAuditIntegrityKeyStatus() (SecurityAuditIntegrityKeyStatus, error) {
	if p == nil {
		return SecurityAuditIntegrityKeyStatus{}, nil
	}
	stored, err := p.store.ListSecurityAuditIntegrityKeyStats()
	if err != nil {
		return SecurityAuditIntegrityKeyStatus{}, err
	}
	return p.securityAuditIntegrityKeyStatus(stored), nil
}

func (p *DurableSecurityAuditPipeline) SecurityAuditIntegrityKeyStatusContext(ctx context.Context) (SecurityAuditIntegrityKeyStatus, error) {
	if p == nil {
		return SecurityAuditIntegrityKeyStatus{}, nil
	}
	stored, err := managedagents.ListSecurityAuditIntegrityKeyStatsWithContext(ctx, p.store)
	if err != nil {
		return SecurityAuditIntegrityKeyStatus{}, err
	}
	return p.securityAuditIntegrityKeyStatus(stored), nil
}

func (p *DurableSecurityAuditPipeline) securityAuditIntegrityKeyStatus(stored []managedagents.SecurityAuditIntegrityKeyStats) SecurityAuditIntegrityKeyStatus {
	byID := make(map[string]managedagents.SecurityAuditIntegrityKeyStats, len(stored)+len(p.integrityKeys))
	var historicalUnidentifiedBlocking int64
	for _, item := range stored {
		byID[item.KeyID] = item
		if item.KeyID == "" {
			historicalUnidentifiedBlocking = item.Pending + item.Delivering + item.DeadLetter
		}
	}
	for keyID := range p.integrityKeys {
		if _, ok := byID[keyID]; !ok {
			byID[keyID] = managedagents.SecurityAuditIntegrityKeyStats{KeyID: keyID}
		}
	}
	keyIDs := make([]string, 0, len(byID))
	for keyID := range byID {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	states := make([]SecurityAuditIntegrityKeyState, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		item := byID[keyID]
		_, configured := p.integrityKeys[keyID]
		active := keyID != "" && keyID == p.activeKeyID
		blocking := item.Pending + item.Delivering + item.DeadLetter
		states = append(states, SecurityAuditIntegrityKeyState{
			KeyID: keyID, Configured: configured, Active: active,
			Pending: item.Pending, Delivering: item.Delivering, Delivered: item.Delivered,
			DeadLetter: item.DeadLetter, Blocking: blocking,
			SafeToRemove: keyID != "" && configured && !active && blocking == 0 && historicalUnidentifiedBlocking == 0,
		})
	}
	return SecurityAuditIntegrityKeyStatus{
		ActiveKeyID: p.activeKeyID, HistoricalUnidentifiedBlocking: historicalUnidentifiedBlocking, Keys: states,
	}
}

func (p *DurableSecurityAuditPipeline) recordPersistenceFailure(err error) {
	p.metricsMu.Lock()
	p.persistenceFailed++
	p.metricsMu.Unlock()
	p.logger.Error("persist security audit event failed", "error", err)
}

func (p *DurableSecurityAuditPipeline) deliverAndLog(ctx context.Context) {
	result, err := p.RunOnce(ctx, time.Now().UTC())
	if err != nil && ctx.Err() == nil {
		p.logger.Warn("durable security audit delivery failed", "result", result, "error", err)
	}
}

func (p *DurableSecurityAuditPipeline) pruneAndLog() {
	pruned, err := p.PruneDelivered(time.Now().UTC())
	if err != nil {
		p.logger.Warn("durable security audit retention failed", "error", err)
		return
	}
	if pruned > 0 {
		p.logger.Info("durable security audit events pruned", "count", pruned, "retention", p.retention)
	}
}

func securityAuditIntegrity(payload []byte, key []byte) (string, string) {
	if len(key) > 0 {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(payload)
		return "hmac-sha256", hex.EncodeToString(mac.Sum(nil))
	}
	digest := sha256.Sum256(payload)
	return "sha256", hex.EncodeToString(digest[:])
}

func verifySecurityAuditIntegrity(payload []byte, algorithm string, expected string, key []byte) bool {
	algorithm = strings.TrimSpace(strings.ToLower(algorithm))
	expectedBytes, err := hex.DecodeString(strings.TrimSpace(expected))
	if err != nil {
		return false
	}
	var actual []byte
	switch algorithm {
	case "hmac-sha256":
		if len(key) == 0 {
			return false
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(payload)
		actual = mac.Sum(nil)
	case "sha256":
		digest := sha256.Sum256(payload)
		actual = digest[:]
	default:
		return false
	}
	return hmac.Equal(actual, expectedBytes)
}

func buildSecurityAuditIntegrityKeys(config DurableSecurityAuditConfig) (string, map[string][]byte, []byte, error) {
	activeKeyID := strings.TrimSpace(config.IntegrityKeyID)
	keys := make(map[string][]byte, len(config.IntegrityKeys)+1)
	for rawID, rawKey := range config.IntegrityKeys {
		keyID := strings.TrimSpace(rawID)
		if keyID != rawID || !validSecurityAuditIntegrityKeyID(keyID) {
			return "", nil, nil, fmt.Errorf("invalid security audit integrity key id %q", rawID)
		}
		if rawKey == "" {
			return "", nil, nil, fmt.Errorf("security audit integrity key %q is empty", keyID)
		}
		keys[keyID] = []byte(rawKey)
	}
	legacyKey := []byte(config.IntegrityKey)
	if len(keys) > 0 {
		if activeKeyID == "" {
			return "", nil, nil, errors.New("security audit active integrity key id is required with a keyring")
		}
		if _, ok := keys[activeKeyID]; !ok {
			return "", nil, nil, fmt.Errorf("security audit active integrity key id %q is not in the keyring", activeKeyID)
		}
		return activeKeyID, keys, legacyKey, nil
	}
	if len(legacyKey) == 0 {
		if activeKeyID != "" {
			return "", nil, nil, errors.New("security audit integrity key id requires an integrity key")
		}
		return "", keys, nil, nil
	}
	if activeKeyID == "" {
		activeKeyID = "legacy"
	}
	if !validSecurityAuditIntegrityKeyID(activeKeyID) {
		return "", nil, nil, fmt.Errorf("invalid security audit integrity key id %q", activeKeyID)
	}
	keys[activeKeyID] = legacyKey
	return activeKeyID, keys, legacyKey, nil
}

func validSecurityAuditIntegrityKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > 128 {
		return false
	}
	for _, char := range keyID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func (p *DurableSecurityAuditPipeline) verifyIntegrity(record managedagents.SecurityAuditOutboxEvent) bool {
	algorithm := strings.TrimSpace(strings.ToLower(record.IntegrityAlgorithm))
	keyID := strings.TrimSpace(record.IntegrityKeyID)
	if algorithm != "hmac-sha256" {
		return keyID == "" && verifySecurityAuditIntegrity(record.Payload, algorithm, record.IntegrityDigest, nil)
	}
	if keyID != "" {
		key, ok := p.integrityKeys[keyID]
		return ok && verifySecurityAuditIntegrity(record.Payload, algorithm, record.IntegrityDigest, key)
	}
	if len(p.legacyIntegrityKey) > 0 && verifySecurityAuditIntegrity(record.Payload, algorithm, record.IntegrityDigest, p.legacyIntegrityKey) {
		return true
	}
	for _, key := range p.integrityKeys {
		if verifySecurityAuditIntegrity(record.Payload, algorithm, record.IntegrityDigest, key) {
			return true
		}
	}
	return false
}

func securityAuditRetryDelay(initial time.Duration, maximum time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := initial
	for current := 1; current < attempt && delay < maximum; current++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func securityAuditOutboxIDs(events []managedagents.SecurityAuditOutboxEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}
