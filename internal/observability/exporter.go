package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type traceEventStore interface {
	ListEvents(sessionID string, afterSeq int64) ([]managedagents.Event, error)
}

type ExporterConfig struct {
	PerfettoEnabled bool
	PerfettoDir     string
	OTLPEndpoint    string
	OTLPToken       string
	HTTPClient      *http.Client
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
	Perfetto ExporterStatus `json:"perfetto"`
	OTLP     ExporterStatus `json:"otlp"`
}

type ExporterStatus struct {
	Enabled       bool   `json:"enabled"`
	Configured    bool   `json:"configured"`
	Destination   string `json:"destination,omitempty"`
	TokenProvided bool   `json:"token_provided,omitempty"`
}

func EnvExporterConfig() ExporterConfig {
	return ExporterConfig{
		PerfettoEnabled: envEnabled("TMA_PERFETTO"),
		PerfettoDir:     strings.TrimSpace(os.Getenv("TMA_PERFETTO_DIR")),
		OTLPEndpoint:    DefaultOTLPEndpoint(),
		OTLPToken:       strings.TrimSpace(firstNonEmpty(os.Getenv("TMA_OTEL_EXPORTER_OTLP_TOKEN"), os.Getenv("OTEL_EXPORTER_OTLP_TOKEN"))),
	}
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
	return Status{
		Perfetto: ExporterStatus{
			Enabled:     config.PerfettoEnabled,
			Configured:  config.PerfettoEnabled,
			Destination: perfettoDir,
		},
		OTLP: ExporterStatus{
			Enabled:       otlpEndpoint != "",
			Configured:    otlpEndpoint != "",
			Destination:   otlpEndpoint,
			TokenProvided: strings.TrimSpace(config.OTLPToken) != "",
		},
	}
}

func ExportTurnTraceFromEnv(store traceEventStore, sessionID string, turnID string) (ExporterResult, error) {
	return ExportTurnTrace(store, sessionID, turnID, EnvExporterConfig())
}

func ExportTurnTrace(store traceEventStore, sessionID string, turnID string, config ExporterConfig) (ExporterResult, error) {
	if !config.PerfettoEnabled && strings.TrimSpace(config.OTLPEndpoint) == "" {
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
	if config.PerfettoEnabled {
		path, err := WritePerfettoFile(trace, config.PerfettoDir)
		if err != nil {
			return result, err
		}
		result.Perfetto = &PerfettoFileResult{Path: path}
	}
	if strings.TrimSpace(config.OTLPEndpoint) != "" {
		push, err := PushOTLPHTTP(config.HTTPClient, config.OTLPEndpoint, config.OTLPToken, ExportOTel(trace))
		if err != nil {
			return result, err
		}
		result.OTLPPush = &push
	}
	return result, nil
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
