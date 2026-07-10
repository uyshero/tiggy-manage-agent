package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type traceEventStore interface {
	ListEvents(sessionID string, afterSeq int64) ([]managedagents.Event, error)
}

type exporterRunRecorder interface {
	RecordObservabilityExporterRun(input managedagents.RecordObservabilityExporterRunInput) (managedagents.ObservabilityExporterRun, error)
}

type ExporterRunStore interface {
	traceEventStore
	exporterRunRecorder
	ListObservabilityExporterRuns(input managedagents.ListObservabilityExporterRunsInput) ([]managedagents.ObservabilityExporterRun, error)
}

type ExporterConfig struct {
	PerfettoEnabled      bool
	PerfettoDir          string
	OTLPEndpoint         string
	OTLPToken            string
	HTTPClient           *http.Client
	SampleRate           float64
	SampleRateConfigured bool
	RetryEnabled         bool
	RetryMaxAttempts     int
	RetryInitialDelay    time.Duration
	RetryMaxDelay        time.Duration
}

type ExporterResult struct {
	TraceID     string              `json:"trace_id,omitempty"`
	Perfetto    *PerfettoFileResult `json:"perfetto,omitempty"`
	OTLPPush    *OTLPHTTPPushResult `json:"otlp_push,omitempty"`
	Skipped     bool                `json:"skipped,omitempty"`
	SkipMessage string              `json:"skip_message,omitempty"`
}

type PerfettoFileResult struct {
	Path string `json:"path"`
}

type OTLPHTTPPushResult struct {
	Endpoint string `json:"endpoint"`
	Status   string `json:"status"`
}

type Status struct {
	Perfetto   ExporterStatus                           `json:"perfetto"`
	OTLP       ExporterStatus                           `json:"otlp"`
	Sampling   SamplingStatus                           `json:"sampling"`
	Retry      RetryStatus                              `json:"retry"`
	RecentRuns []managedagents.ObservabilityExporterRun `json:"recent_runs,omitempty"`
}

type SamplingStatus struct {
	Enabled    bool    `json:"enabled"`
	SampleRate float64 `json:"sample_rate"`
	Configured bool    `json:"configured"`
}

type RetryStatus struct {
	Enabled              bool  `json:"enabled"`
	MaxAttempts          int   `json:"max_attempts"`
	InitialDelayMillis   int64 `json:"initial_delay_ms"`
	MaxDelayMillis       int64 `json:"max_delay_ms"`
	PendingRecentRetries int   `json:"pending_recent_retries"`
}

type ExporterStatus struct {
	Enabled       bool            `json:"enabled"`
	Configured    bool            `json:"configured"`
	Destination   string          `json:"destination,omitempty"`
	TokenProvided bool            `json:"token_provided,omitempty"`
	LastSuccess   *ExporterHealth `json:"last_success,omitempty"`
	LastFailure   *ExporterHealth `json:"last_failure,omitempty"`
	LastAttempt   *ExporterHealth `json:"last_attempt,omitempty"`
}

type ExporterHealth struct {
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
	Message   string    `json:"message,omitempty"`
}

var exporterHealth = struct {
	sync.Mutex
	byName map[string]exporterHealthRecord
}{
	byName: map[string]exporterHealthRecord{},
}

type exporterHealthRecord struct {
	LastSuccess *ExporterHealth
	LastFailure *ExporterHealth
	LastAttempt *ExporterHealth
}

type RetryResult struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

func EnvExporterConfig() ExporterConfig {
	sampleRate, sampleRateConfigured := envSampleRate()
	return ExporterConfig{
		PerfettoEnabled:      envEnabled("TMA_PERFETTO"),
		PerfettoDir:          strings.TrimSpace(os.Getenv("TMA_PERFETTO_DIR")),
		OTLPEndpoint:         DefaultOTLPEndpoint(),
		OTLPToken:            strings.TrimSpace(firstNonEmpty(os.Getenv("TMA_OTEL_EXPORTER_OTLP_TOKEN"), os.Getenv("OTEL_EXPORTER_OTLP_TOKEN"))),
		SampleRate:           sampleRate,
		SampleRateConfigured: sampleRateConfigured,
		RetryEnabled:         !envExplicitlyDisabled("TMA_OBSERVABILITY_EXPORTER_RETRY"),
		RetryMaxAttempts:     envInt("TMA_OBSERVABILITY_EXPORTER_RETRY_MAX_ATTEMPTS", 3),
		RetryInitialDelay:    envDurationMillis("TMA_OBSERVABILITY_EXPORTER_RETRY_INITIAL_DELAY_MS", 30*time.Second),
		RetryMaxDelay:        envDurationMillis("TMA_OBSERVABILITY_EXPORTER_RETRY_MAX_DELAY_MS", 10*time.Minute),
	}
}

func envSampleRate() (float64, bool) {
	raw := strings.TrimSpace(os.Getenv("TMA_OBSERVABILITY_SAMPLE_RATE"))
	if raw == "" {
		return 1, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 1, true
	}
	return normalizeSampleRate(value), true
}

func normalizeSampleRate(value float64) float64 {
	if math.IsNaN(value) {
		return 1
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func shouldSampleTurn(config ExporterConfig, sessionID string, turnID string) bool {
	rate := normalizeSampleRate(config.SampleRate)
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(strings.TrimSpace(sessionID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(turnID)))
	bucket := float64(hash.Sum64()) / float64(^uint64(0))
	return bucket < rate
}

func normalizeRetryMaxAttempts(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func normalizeRetryDelay(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envDurationMillis(key string, fallback time.Duration) time.Duration {
	value := envInt(key, int(fallback/time.Millisecond))
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func StatusFromEnv() Status {
	config := EnvExporterConfig()
	perfettoDir := strings.TrimSpace(config.PerfettoDir)
	if perfettoDir == "" && config.PerfettoEnabled {
		if home, err := os.UserHomeDir(); err == nil {
			perfettoDir = filepath.Join(home, ".tma", "traces")
		}
	}
	otlpEndpoint := NormalizeOTLPTraceEndpoint(config.OTLPEndpoint)
	perfettoHealth := exporterHealthSnapshot("perfetto")
	otlpHealth := exporterHealthSnapshot("otlp")
	return Status{
		Perfetto: ExporterStatus{
			Enabled:     config.PerfettoEnabled,
			Configured:  config.PerfettoEnabled,
			Destination: perfettoDir,
			LastSuccess: perfettoHealth.LastSuccess,
			LastFailure: perfettoHealth.LastFailure,
			LastAttempt: perfettoHealth.LastAttempt,
		},
		OTLP: ExporterStatus{
			Enabled:       otlpEndpoint != "",
			Configured:    otlpEndpoint != "",
			Destination:   otlpEndpoint,
			TokenProvided: strings.TrimSpace(config.OTLPToken) != "",
			LastSuccess:   otlpHealth.LastSuccess,
			LastFailure:   otlpHealth.LastFailure,
			LastAttempt:   otlpHealth.LastAttempt,
		},
		Sampling: SamplingStatus{
			Enabled:    normalizeSampleRate(config.SampleRate) < 1,
			SampleRate: normalizeSampleRate(config.SampleRate),
			Configured: config.SampleRateConfigured,
		},
		Retry: RetryStatus{
			Enabled:            config.RetryEnabled,
			MaxAttempts:        normalizeRetryMaxAttempts(config.RetryMaxAttempts),
			InitialDelayMillis: normalizeRetryDelay(config.RetryInitialDelay, 30*time.Second).Milliseconds(),
			MaxDelayMillis:     normalizeRetryDelay(config.RetryMaxDelay, 10*time.Minute).Milliseconds(),
		},
	}
}

func StatusFromEnvWithRuns(runs []managedagents.ObservabilityExporterRun) Status {
	status := StatusFromEnv()
	status.RecentRuns = append([]managedagents.ObservabilityExporterRun(nil), runs...)
	for _, run := range runs {
		if run.Status == managedagents.ObservabilityExporterRunFailed && run.NextRetryAt != nil && run.AttemptCount < status.Retry.MaxAttempts {
			status.Retry.PendingRecentRetries++
		}
		health := exporterHealthFromRun(run)
		switch run.Exporter {
		case managedagents.ObservabilityExporterPerfetto:
			applyExporterRunHealth(&status.Perfetto, run.Status, health)
		case managedagents.ObservabilityExporterOTLP:
			applyExporterRunHealth(&status.OTLP, run.Status, health)
		}
	}
	return status
}

func ExportTurnTraceFromEnv(store traceEventStore, sessionID string, turnID string) (ExporterResult, error) {
	config := EnvExporterConfig()
	if exportersDisabled(config) {
		return ExportTurnTrace(store, sessionID, turnID, config)
	}
	if !shouldSampleTurn(config, sessionID, turnID) {
		message := fmt.Sprintf("skipped by observability sampling policy (sample_rate=%.6g)", normalizeSampleRate(config.SampleRate))
		if recorder, ok := store.(exporterRunRecorder); ok {
			recordExporterSkip(recorder, config, sessionID, turnID, message)
		}
		return ExporterResult{Skipped: true, SkipMessage: message}, nil
	}
	return ExportTurnTrace(store, sessionID, turnID, config)
}

func ExportTurnTrace(store traceEventStore, sessionID string, turnID string, config ExporterConfig) (ExporterResult, error) {
	if exportersDisabled(config) {
		return ExporterResult{Skipped: true, SkipMessage: "no observability exporters enabled"}, nil
	}
	if store == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(turnID) == "" {
		return ExporterResult{}, fmt.Errorf("session_id and turn_id are required")
	}
	events, err := store.ListEvents(sessionID, 0)
	if err != nil {
		return ExporterResult{}, err
	}
	trace := ProjectTurnTrace(sessionID, turnID, events)
	if trace.TurnID == "" || len(trace.Steps) == 0 {
		return ExporterResult{}, fmt.Errorf("trace not found for session %s turn %s", sessionID, turnID)
	}

	result := ExporterResult{TraceID: trace.TraceID}
	recorder, _ := store.(exporterRunRecorder)
	if config.PerfettoEnabled {
		startedAt := time.Now().UTC()
		path, err := WritePerfettoFile(trace, config.PerfettoDir)
		if err != nil {
			recordExporterFailure("perfetto", trace, err)
			recordExporterRun(recorder, config, managedagents.ObservabilityExporterPerfetto, trace, managedagents.ObservabilityExporterRunFailed, config.PerfettoDir, err.Error(), 1, startedAt)
			return result, err
		}
		result.Perfetto = &PerfettoFileResult{Path: path}
		recordExporterSuccess("perfetto", trace, path)
		recordExporterRun(recorder, config, managedagents.ObservabilityExporterPerfetto, trace, managedagents.ObservabilityExporterRunSucceeded, path, path, 1, startedAt)
	}
	if strings.TrimSpace(config.OTLPEndpoint) != "" {
		startedAt := time.Now().UTC()
		push, err := PushOTLPHTTP(config.HTTPClient, config.OTLPEndpoint, config.OTLPToken, ExportOTel(trace))
		if err != nil {
			recordExporterFailure("otlp", trace, err)
			recordExporterRun(recorder, config, managedagents.ObservabilityExporterOTLP, trace, managedagents.ObservabilityExporterRunFailed, NormalizeOTLPTraceEndpoint(config.OTLPEndpoint), err.Error(), 1, startedAt)
			return result, err
		}
		result.OTLPPush = &push
		recordExporterSuccess("otlp", trace, push.Endpoint+" "+push.Status)
		recordExporterRun(recorder, config, managedagents.ObservabilityExporterOTLP, trace, managedagents.ObservabilityExporterRunSucceeded, push.Endpoint, push.Status, 1, startedAt)
	}
	return result, nil
}

func RetryFailedExporterRunsFromEnv(store ExporterRunStore) (RetryResult, error) {
	return RetryFailedExporterRuns(store, EnvExporterConfig(), time.Now().UTC(), 20)
}

func RetryFailedExporterRuns(store ExporterRunStore, config ExporterConfig, now time.Time, limit int) (RetryResult, error) {
	if store == nil {
		return RetryResult{}, fmt.Errorf("store is required")
	}
	if !config.RetryEnabled {
		return RetryResult{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	maxAttempts := normalizeRetryMaxAttempts(config.RetryMaxAttempts)
	runs, err := store.ListObservabilityExporterRuns(managedagents.ListObservabilityExporterRunsInput{
		Status:          managedagents.ObservabilityExporterRunFailed,
		RetryDueBefore:  now,
		MaxAttemptCount: maxAttempts,
		Limit:           limit,
	})
	if err != nil {
		return RetryResult{}, err
	}
	var result RetryResult
	for _, run := range runs {
		if !latestExporterRunMatches(store, run) {
			result.Skipped++
			continue
		}
		skipped, err := retryExporterRun(store, config, run)
		if skipped {
			result.Skipped++
			continue
		}
		result.Attempted++
		if err != nil {
			result.Failed++
			continue
		}
		result.Succeeded++
	}
	return result, nil
}

func latestExporterRunMatches(store ExporterRunStore, run managedagents.ObservabilityExporterRun) bool {
	latest, err := store.ListObservabilityExporterRuns(managedagents.ListObservabilityExporterRunsInput{
		Exporter:  run.Exporter,
		SessionID: run.SessionID,
		TurnID:    run.TurnID,
		Limit:     1,
	})
	if err != nil || len(latest) == 0 {
		return false
	}
	return latest[0].ID == run.ID
}

func retryExporterRun(store ExporterRunStore, config ExporterConfig, run managedagents.ObservabilityExporterRun) (bool, error) {
	attemptCount := run.AttemptCount + 1
	if attemptCount <= 1 {
		attemptCount = 2
	}
	startedAt := time.Now().UTC()
	fallbackTrace := TurnTrace{
		SessionID: run.SessionID,
		TurnID:    run.TurnID,
		TraceID:   run.TraceID,
	}
	events, err := store.ListEvents(run.SessionID, 0)
	if err != nil {
		recordExporterRun(store, config, run.Exporter, fallbackTrace, managedagents.ObservabilityExporterRunFailed, run.Destination, err.Error(), attemptCount, startedAt)
		return false, err
	}
	trace := ProjectTurnTrace(run.SessionID, run.TurnID, events)
	if trace.TurnID == "" || len(trace.Steps) == 0 {
		err := fmt.Errorf("trace not found for session %s turn %s", run.SessionID, run.TurnID)
		recordExporterRun(store, config, run.Exporter, fallbackTrace, managedagents.ObservabilityExporterRunFailed, run.Destination, err.Error(), attemptCount, startedAt)
		return false, err
	}
	switch run.Exporter {
	case managedagents.ObservabilityExporterPerfetto:
		if !config.PerfettoEnabled {
			return true, nil
		}
		path, err := WritePerfettoFile(trace, firstNonEmpty(config.PerfettoDir, run.Destination))
		if err != nil {
			recordExporterFailure("perfetto", trace, err)
			recordExporterRun(store, config, run.Exporter, trace, managedagents.ObservabilityExporterRunFailed, firstNonEmpty(config.PerfettoDir, run.Destination), err.Error(), attemptCount, startedAt)
			return false, err
		}
		recordExporterSuccess("perfetto", trace, path)
		recordExporterRun(store, config, run.Exporter, trace, managedagents.ObservabilityExporterRunSucceeded, path, path, attemptCount, startedAt)
		return false, nil
	case managedagents.ObservabilityExporterOTLP:
		if strings.TrimSpace(config.OTLPEndpoint) == "" {
			return true, nil
		}
		push, err := PushOTLPHTTP(config.HTTPClient, config.OTLPEndpoint, config.OTLPToken, ExportOTel(trace))
		if err != nil {
			recordExporterFailure("otlp", trace, err)
			recordExporterRun(store, config, run.Exporter, trace, managedagents.ObservabilityExporterRunFailed, NormalizeOTLPTraceEndpoint(config.OTLPEndpoint), err.Error(), attemptCount, startedAt)
			return false, err
		}
		recordExporterSuccess("otlp", trace, push.Endpoint+" "+push.Status)
		recordExporterRun(store, config, run.Exporter, trace, managedagents.ObservabilityExporterRunSucceeded, push.Endpoint, push.Status, attemptCount, startedAt)
		return false, nil
	default:
		return true, nil
	}
}

func exportersDisabled(config ExporterConfig) bool {
	return !config.PerfettoEnabled && strings.TrimSpace(config.OTLPEndpoint) == ""
}

func recordExporterSkip(recorder exporterRunRecorder, config ExporterConfig, sessionID string, turnID string, message string) {
	if recorder == nil {
		return
	}
	startedAt := time.Now().UTC()
	if config.PerfettoEnabled {
		_, _ = recorder.RecordObservabilityExporterRun(managedagents.RecordObservabilityExporterRunInput{
			Exporter:    managedagents.ObservabilityExporterPerfetto,
			Status:      managedagents.ObservabilityExporterRunSkipped,
			SessionID:   sessionID,
			TurnID:      turnID,
			Destination: strings.TrimSpace(config.PerfettoDir),
			Message:     message,
			StartedAt:   startedAt,
			FinishedAt:  startedAt,
		})
	}
	if strings.TrimSpace(config.OTLPEndpoint) != "" {
		_, _ = recorder.RecordObservabilityExporterRun(managedagents.RecordObservabilityExporterRunInput{
			Exporter:    managedagents.ObservabilityExporterOTLP,
			Status:      managedagents.ObservabilityExporterRunSkipped,
			SessionID:   sessionID,
			TurnID:      turnID,
			Destination: NormalizeOTLPTraceEndpoint(config.OTLPEndpoint),
			Message:     message,
			StartedAt:   startedAt,
			FinishedAt:  startedAt,
		})
	}
}

func recordExporterRun(recorder exporterRunRecorder, config ExporterConfig, exporter string, trace TurnTrace, status string, destination string, message string, attemptCount int, startedAt time.Time) {
	if recorder == nil {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	if attemptCount <= 0 {
		attemptCount = 1
	}
	_, _ = recorder.RecordObservabilityExporterRun(managedagents.RecordObservabilityExporterRunInput{
		Exporter:     exporter,
		Status:       status,
		SessionID:    trace.SessionID,
		TurnID:       trace.TurnID,
		TraceID:      trace.TraceID,
		Destination:  strings.TrimSpace(destination),
		Message:      strings.TrimSpace(message),
		AttemptCount: attemptCount,
		NextRetryAt:  nextExporterRetryAt(config, status, attemptCount, startedAt),
		StartedAt:    startedAt,
		FinishedAt:   time.Now().UTC(),
	})
}

func nextExporterRetryAt(config ExporterConfig, status string, attemptCount int, at time.Time) *time.Time {
	if !config.RetryEnabled || status != managedagents.ObservabilityExporterRunFailed {
		return nil
	}
	maxAttempts := normalizeRetryMaxAttempts(config.RetryMaxAttempts)
	if attemptCount >= maxAttempts {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	delay := retryDelay(config, attemptCount)
	next := at.Add(delay)
	return &next
}

func retryDelay(config ExporterConfig, attemptCount int) time.Duration {
	if attemptCount <= 0 {
		attemptCount = 1
	}
	delay := normalizeRetryDelay(config.RetryInitialDelay, 30*time.Second)
	maxDelay := normalizeRetryDelay(config.RetryMaxDelay, 10*time.Minute)
	for i := 1; i < attemptCount; i++ {
		delay *= 2
		if delay >= maxDelay {
			return maxDelay
		}
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func recordExporterSuccess(name string, trace TurnTrace, message string) {
	recordExporterHealth(name, trace, message, true)
}

func recordExporterFailure(name string, trace TurnTrace, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	recordExporterHealth(name, trace, message, false)
}

func recordExporterHealth(name string, trace TurnTrace, message string, success bool) {
	health := &ExporterHealth{
		At:        time.Now().UTC(),
		SessionID: trace.SessionID,
		TurnID:    trace.TurnID,
		TraceID:   trace.TraceID,
		Message:   message,
	}
	exporterHealth.Lock()
	defer exporterHealth.Unlock()
	record := exporterHealth.byName[name]
	record.LastAttempt = cloneExporterHealth(health)
	if success {
		record.LastSuccess = cloneExporterHealth(health)
	} else {
		record.LastFailure = cloneExporterHealth(health)
	}
	exporterHealth.byName[name] = record
}

func exporterHealthSnapshot(name string) exporterHealthRecord {
	exporterHealth.Lock()
	defer exporterHealth.Unlock()
	record := exporterHealth.byName[name]
	return exporterHealthRecord{
		LastSuccess: cloneExporterHealth(record.LastSuccess),
		LastFailure: cloneExporterHealth(record.LastFailure),
		LastAttempt: cloneExporterHealth(record.LastAttempt),
	}
}

func cloneExporterHealth(input *ExporterHealth) *ExporterHealth {
	if input == nil {
		return nil
	}
	copied := *input
	return &copied
}

func exporterHealthFromRun(run managedagents.ObservabilityExporterRun) *ExporterHealth {
	at := run.FinishedAt
	if at.IsZero() {
		at = run.StartedAt
	}
	return &ExporterHealth{
		At:        at,
		SessionID: run.SessionID,
		TurnID:    run.TurnID,
		TraceID:   run.TraceID,
		Message:   firstNonEmpty(run.Message, run.Destination),
	}
}

func applyExporterRunHealth(status *ExporterStatus, runStatus string, health *ExporterHealth) {
	if health == nil {
		return
	}
	if newerExporterHealth(health, status.LastAttempt) {
		status.LastAttempt = cloneExporterHealth(health)
	}
	switch runStatus {
	case managedagents.ObservabilityExporterRunSucceeded:
		if newerExporterHealth(health, status.LastSuccess) {
			status.LastSuccess = cloneExporterHealth(health)
		}
	case managedagents.ObservabilityExporterRunFailed:
		if newerExporterHealth(health, status.LastFailure) {
			status.LastFailure = cloneExporterHealth(health)
		}
	}
}

func newerExporterHealth(left *ExporterHealth, right *ExporterHealth) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	return left.At.After(right.At)
}

func WritePerfettoFile(trace TurnTrace, baseDir string) (string, error) {
	if strings.TrimSpace(trace.SessionID) == "" || strings.TrimSpace(trace.TurnID) == "" {
		return "", fmt.Errorf("session_id and turn_id are required")
	}
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		baseDir = filepath.Join(home, ".tma", "traces")
	}
	dir := filepath.Join(baseDir, safePathSegment(trace.SessionID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create perfetto trace dir: %w", err)
	}
	path := filepath.Join(dir, safePathSegment(trace.TurnID)+".perfetto.json")
	encoded, err := json.MarshalIndent(ExportPerfetto(trace), "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode perfetto trace: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write perfetto trace: %w", err)
	}
	return path, nil
}

func DefaultOTLPEndpoint() string {
	if value := strings.TrimSpace(os.Getenv("TMA_OTEL_EXPORTER_OTLP_ENDPOINT")); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}

func PushOTLPHTTP(client *http.Client, endpoint string, token string, value any) (OTLPHTTPPushResult, error) {
	endpoint = NormalizeOTLPTraceEndpoint(endpoint)
	if endpoint == "" {
		return OTLPHTTPPushResult{}, fmt.Errorf("otlp endpoint is required")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return OTLPHTTPPushResult{}, fmt.Errorf("encode otlp payload: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return OTLPHTTPPushResult{}, fmt.Errorf("create otlp request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token = strings.TrimSpace(token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return OTLPHTTPPushResult{}, fmt.Errorf("push otlp trace: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return OTLPHTTPPushResult{}, fmt.Errorf("push otlp trace returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return OTLPHTTPPushResult{
		Endpoint: endpoint,
		Status:   response.Status,
	}, nil
}

func NormalizeOTLPTraceEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return ""
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return endpoint
	}
	if parsed.Path == "" {
		parsed.Path = "/v1/traces"
		return parsed.String()
	}
	if strings.HasSuffix(parsed.Path, "/v1/traces") {
		return parsed.String()
	}
	return parsed.String()
}

func envEnabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envExplicitlyDisabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	return replacer.Replace(value)
}
