package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workruntime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("worker exited", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	if len(args) > 0 && args[0] == "doctor" {
		cfg, err := parseWorkerConfig(args[1:])
		if err != nil {
			return err
		}
		client := newWorkerAPIClient(cfg.BaseURL, cfg.Token)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return runWorkerDoctor(ctx, client, cfg, workruntime.DefaultExecutor(cfg.Name), os.Stdout)
	}

	cfg, err := parseWorkerConfig(args)
	if err != nil {
		return err
	}
	client := newWorkerAPIClient(cfg.BaseURL, cfg.Token)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	executor := workruntime.DefaultExecutor(cfg.Name)
	executor.ArtifactUploader = client
	return runWorker(ctx, client, cfg, executor, logger)
}

func parseWorkerConfig(args []string) (workerConfig, error) {
	cfg := workerConfig{}
	global := flag.NewFlagSet("tma-worker", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	global.StringVar(&cfg.BaseURL, "base-url", getenvDefault("TMA_BASE_URL", "http://localhost:8080"), "TMA API base URL")
	global.StringVar(&cfg.Token, "token", os.Getenv("TMA_WORKER_TOKEN"), "worker bearer token")
	global.StringVar(&cfg.Name, "name", defaultWorkerName(), "worker name")
	global.StringVar(&cfg.WorkspaceID, "workspace", getenvDefault("TMA_WORKER_WORKSPACE_ID", managedagents.DefaultWorkspaceID), "workspace id")
	global.StringVar(&cfg.WorkerType, "type", getenvDefault("TMA_WORKER_TYPE", managedagents.WorkerTypeLocal), "worker type")
	global.StringVar(&cfg.RegisteredBy, "registered-by", getenvDefault("TMA_WORKER_REGISTERED_BY", defaultWorkerName()), "registrar id")
	global.IntVar(&cfg.LeaseSeconds, "lease-seconds", getenvDefaultInt("TMA_WORKER_LEASE_SECONDS", 60), "lease seconds")
	global.DurationVar(&cfg.PollInterval, "poll-interval", getenvDefaultDuration("TMA_WORKER_POLL_INTERVAL", 3*time.Second), "poll interval")
	global.DurationVar(&cfg.HeartbeatInterval, "heartbeat-interval", getenvDefaultDuration("TMA_WORKER_HEARTBEAT_INTERVAL", 30*time.Second), "heartbeat interval")
	if err := global.Parse(args); err != nil {
		return workerConfig{}, err
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Name == "" {
		return workerConfig{}, fmt.Errorf("worker name is required")
	}
	return cfg, nil
}

type workerConfig struct {
	BaseURL           string
	Token             string
	Name              string
	WorkspaceID       string
	WorkerType        string
	RegisteredBy      string
	LeaseSeconds      int
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

type workerAPI interface {
	RegisterWorker(context.Context, managedagents.RegisterWorkerInput) (managedagents.Worker, error)
	HeartbeatWorker(context.Context, string, managedagents.WorkerHeartbeatInput) (managedagents.Worker, error)
	PollWorkerWork(context.Context, string, managedagents.PollWorkerWorkInput) (*managedagents.WorkerWork, error)
	AckWorkerWork(context.Context, string, string) (managedagents.WorkerWork, error)
	CompleteWorkerWork(context.Context, string, string, managedagents.CompleteWorkerWorkInput) (managedagents.WorkerWork, error)
}

type workerDoctorAPI interface {
	Health(context.Context) error
	RegisterWorker(context.Context, managedagents.RegisterWorkerInput) (managedagents.Worker, error)
	HeartbeatWorker(context.Context, string, managedagents.WorkerHeartbeatInput) (managedagents.Worker, error)
	PollWorkerWork(context.Context, string, managedagents.PollWorkerWorkInput) (*managedagents.WorkerWork, error)
	DiagnoseWorkers(context.Context, workerDiagnoseRequest) (workerDiagnoseResponse, error)
	ArchiveWorker(context.Context, string) (managedagents.Worker, error)
}

type workerDiagnoseRequest struct {
	WorkspaceID  string          `json:"workspace_id,omitempty"`
	Namespace    string          `json:"namespace"`
	API          string          `json:"api"`
	Runtime      string          `json:"runtime,omitempty"`
	Capabilities []string        `json:"capabilities,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
}

type workerDiagnoseResponse struct {
	Invocation  tools.WorkInvocation    `json:"invocation"`
	Matches     int                     `json:"matches"`
	Diagnostics []workerDiagnosisResult `json:"diagnostics"`
}

type workerDiagnosisResult struct {
	WorkerID     string   `json:"worker_id"`
	Name         string   `json:"name"`
	WorkerType   string   `json:"worker_type"`
	Status       string   `json:"status"`
	Match        bool     `json:"match"`
	Reasons      []string `json:"reasons,omitempty"`
	Runtimes     []string `json:"runtimes,omitempty"`
	APIs         []string `json:"apis,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func runWorker(ctx context.Context, client workerAPI, cfg workerConfig, executor workruntime.Executor, logger *slog.Logger) error {
	worker, err := client.RegisterWorker(ctx, managedagents.RegisterWorkerInput{
		WorkspaceID:  cfg.WorkspaceID,
		Name:         cfg.Name,
		WorkerType:   cfg.WorkerType,
		Capabilities: executor.WorkerCapabilitiesJSON(),
		Metadata:     workerMetadata(),
		RegisteredBy: cfg.RegisteredBy,
		LeaseSeconds: cfg.LeaseSeconds,
	})
	if err != nil {
		return err
	}
	logger.Info("worker registered",
		"worker_id", worker.ID,
		"workspace_id", worker.WorkspaceID,
		"worker_type", worker.WorkerType,
	)

	heartbeatTicker := time.NewTicker(cfg.HeartbeatInterval)
	defer heartbeatTicker.Stop()
	pollTicker := time.NewTicker(cfg.PollInterval)
	defer pollTicker.Stop()

	if err := heartbeatOnce(ctx, client, worker.ID, cfg.LeaseSeconds, executor, logger); err != nil {
		return err
	}
	if err := pollOnce(ctx, client, cfg, worker.ID, executor, logger); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped", "worker_id", worker.ID)
			return nil
		case <-heartbeatTicker.C:
			if err := heartbeatOnce(ctx, client, worker.ID, cfg.LeaseSeconds, executor, logger); err != nil {
				return err
			}
		case <-pollTicker.C:
			if err := pollOnce(ctx, client, cfg, worker.ID, executor, logger); err != nil {
				return err
			}
		}
	}
}

func runWorkerDoctor(ctx context.Context, client workerDoctorAPI, cfg workerConfig, executor workruntime.Executor, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	fmt.Fprintf(output, "server: checking %s\n", cfg.BaseURL)
	if err := client.Health(ctx); err != nil {
		fmt.Fprintf(output, "server: failed (%v)\n", err)
		return err
	}
	fmt.Fprintln(output, "server: ok")

	capabilities := executor.WorkerCapabilities()
	printDoctorCapabilities(output, capabilities)

	worker, err := client.RegisterWorker(ctx, managedagents.RegisterWorkerInput{
		WorkspaceID:  cfg.WorkspaceID,
		Name:         cfg.Name + "-doctor",
		WorkerType:   cfg.WorkerType,
		Capabilities: executor.WorkerCapabilitiesJSON(),
		Metadata:     workerMetadata(),
		RegisteredBy: cfg.RegisteredBy,
		LeaseSeconds: cfg.LeaseSeconds,
	})
	if err != nil {
		fmt.Fprintf(output, "register: failed (%v)\n", err)
		return err
	}
	fmt.Fprintf(output, "register: ok %s\n", worker.ID)

	archive := true
	defer func() {
		if archive {
			_, _ = client.ArchiveWorker(context.Background(), worker.ID)
		}
	}()

	if _, err := client.HeartbeatWorker(ctx, worker.ID, managedagents.WorkerHeartbeatInput{
		Status:       managedagents.WorkerStatusOnline,
		Capabilities: executor.WorkerCapabilitiesJSON(),
		Metadata:     workerMetadata(),
		LeaseSeconds: cfg.LeaseSeconds,
	}); err != nil {
		fmt.Fprintf(output, "heartbeat: failed (%v)\n", err)
		return err
	}
	fmt.Fprintln(output, "heartbeat: ok")

	if _, err := client.PollWorkerWork(ctx, worker.ID, managedagents.PollWorkerWorkInput{LeaseSeconds: cfg.LeaseSeconds}); err != nil {
		fmt.Fprintf(output, "poll: failed (%v)\n", err)
		return err
	}
	fmt.Fprintln(output, "poll: ok")

	request := doctorDiagnoseRequest(cfg.WorkspaceID, capabilities)
	diagnosis, err := client.DiagnoseWorkers(ctx, request)
	if err != nil {
		fmt.Fprintf(output, "diagnose: failed (%v)\n", err)
		return err
	}
	if !diagnosisContainsWorker(diagnosis, worker.ID) {
		err := fmt.Errorf("registered worker %s was not returned by diagnose", worker.ID)
		fmt.Fprintf(output, "diagnose: failed (%v)\n", err)
		return err
	}
	fmt.Fprintf(output, "diagnose: ok matches=%d\n", diagnosis.Matches)

	if _, err := client.ArchiveWorker(ctx, worker.ID); err != nil {
		fmt.Fprintf(output, "archive: failed (%v)\n", err)
		return err
	}
	archive = false
	fmt.Fprintln(output, "archive: ok")
	fmt.Fprintln(output, "doctor: ok")
	return nil
}

func printDoctorCapabilities(output io.Writer, capabilities tools.WorkerCapabilities) {
	fmt.Fprintln(output, "capabilities:")
	if len(capabilities.Runtimes) > 0 {
		fmt.Fprintf(output, "  runtimes: %s\n", strings.Join(capabilities.Runtimes, ", "))
	}
	if len(capabilities.APIs) > 0 {
		fmt.Fprintf(output, "  apis: %s\n", strings.Join(capabilities.APIs, ", "))
	}
	if len(capabilities.Capabilities) > 0 {
		fmt.Fprintf(output, "  capabilities: %s\n", strings.Join(capabilities.Capabilities, ", "))
	}
}

func doctorDiagnoseRequest(workspaceID string, capabilities tools.WorkerCapabilities) workerDiagnoseRequest {
	namespace := tools.NamespaceDefault
	apiName := "run_command"
	if len(capabilities.APIs) > 0 {
		namespace, apiName = splitQualifiedAPI(capabilities.APIs[0])
	}
	runtime := tools.ToolRuntimeLocalSystem
	if len(capabilities.Runtimes) > 0 {
		runtime = capabilities.Runtimes[0]
	}
	return workerDiagnoseRequest{
		WorkspaceID:  workspaceID,
		Namespace:    namespace,
		API:          apiName,
		Runtime:      runtime,
		Capabilities: capabilities.Capabilities,
		Input:        json.RawMessage(`{}`),
	}
}

func splitQualifiedAPI(value string) (string, string) {
	namespace, apiName, ok := strings.Cut(value, ".")
	if !ok || strings.TrimSpace(namespace) == "" || strings.TrimSpace(apiName) == "" {
		return tools.NamespaceDefault, value
	}
	return namespace, apiName
}

func diagnosisContainsWorker(response workerDiagnoseResponse, workerID string) bool {
	for _, diagnosis := range response.Diagnostics {
		if diagnosis.WorkerID == workerID {
			return true
		}
	}
	return false
}

func heartbeatOnce(ctx context.Context, client workerAPI, workerID string, leaseSeconds int, executor workruntime.Executor, logger *slog.Logger) error {
	_, err := client.HeartbeatWorker(ctx, workerID, managedagents.WorkerHeartbeatInput{
		Status:       managedagents.WorkerStatusOnline,
		Capabilities: executor.WorkerCapabilitiesJSON(),
		Metadata:     workerMetadata(),
		LeaseSeconds: leaseSeconds,
	})
	if err != nil {
		return err
	}
	logger.Info("worker heartbeat", "worker_id", workerID)
	return nil
}

func pollOnce(ctx context.Context, client workerAPI, cfg workerConfig, workerID string, executor workruntime.Executor, logger *slog.Logger) error {
	work, err := client.PollWorkerWork(ctx, workerID, managedagents.PollWorkerWorkInput{LeaseSeconds: cfg.LeaseSeconds})
	if err != nil {
		return err
	}
	if work == nil {
		return nil
	}

	logger.Info("worker work received",
		"worker_id", workerID,
		"work_id", work.ID,
		"work_type", work.WorkType,
		"session_id", work.SessionID,
		"turn_id", work.TurnID,
	)
	if _, err := client.AckWorkerWork(ctx, workerID, work.ID); err != nil {
		return err
	}
	logger.Info("worker work acknowledged", "worker_id", workerID, "work_id", work.ID)

	result := executeWork(ctx, executor, *work)
	completed, err := client.CompleteWorkerWork(ctx, workerID, work.ID, result)
	if err != nil {
		return err
	}
	logger.Info("worker work completed",
		"worker_id", workerID,
		"work_id", completed.ID,
		"status", completed.Status,
	)
	return nil
}

func executeWork(ctx context.Context, executor workruntime.Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
	return executor.Execute(ctx, work)
}

type workerHTTPClient struct {
	baseURL string
	http    *http.Client
	token   string
}

func newWorkerAPIClient(baseURL string, token string) *workerHTTPClient {
	return &workerHTTPClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
		token:   strings.TrimSpace(token),
	}
}

func (c *workerHTTPClient) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/health", nil, nil)
}

func (c *workerHTTPClient) RegisterWorker(ctx context.Context, input managedagents.RegisterWorkerInput) (managedagents.Worker, error) {
	var response managedagents.Worker
	if err := c.do(ctx, http.MethodPost, "/v1/workers", input, &response); err != nil {
		return managedagents.Worker{}, err
	}
	return response, nil
}

func (c *workerHTTPClient) HeartbeatWorker(ctx context.Context, workerID string, input managedagents.WorkerHeartbeatInput) (managedagents.Worker, error) {
	var response managedagents.Worker
	if err := c.do(ctx, http.MethodPost, "/v1/workers/"+url.PathEscape(workerID)+"/heartbeat", input, &response); err != nil {
		return managedagents.Worker{}, err
	}
	return response, nil
}

func (c *workerHTTPClient) PollWorkerWork(ctx context.Context, workerID string, input managedagents.PollWorkerWorkInput) (*managedagents.WorkerWork, error) {
	path := "/v1/workers/" + url.PathEscape(workerID) + "/work/poll"
	if input.LeaseSeconds > 0 {
		path += "?lease_seconds=" + fmt.Sprintf("%d", input.LeaseSeconds)
	}
	var response struct {
		Work *managedagents.WorkerWork `json:"work"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return response.Work, nil
}

func (c *workerHTTPClient) DiagnoseWorkers(ctx context.Context, input workerDiagnoseRequest) (workerDiagnoseResponse, error) {
	var response workerDiagnoseResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workers/diagnose", input, &response); err != nil {
		return workerDiagnoseResponse{}, err
	}
	return response, nil
}

func (c *workerHTTPClient) ArchiveWorker(ctx context.Context, workerID string) (managedagents.Worker, error) {
	var response managedagents.Worker
	if err := c.do(ctx, http.MethodPost, "/v1/workers/"+url.PathEscape(workerID)+"/archive", map[string]any{}, &response); err != nil {
		return managedagents.Worker{}, err
	}
	return response, nil
}

func (c *workerHTTPClient) AckWorkerWork(ctx context.Context, workerID string, workID string) (managedagents.WorkerWork, error) {
	var response managedagents.WorkerWork
	if err := c.do(ctx, http.MethodPost, "/v1/workers/"+url.PathEscape(workerID)+"/work/"+url.PathEscape(workID)+"/ack", map[string]any{}, &response); err != nil {
		return managedagents.WorkerWork{}, err
	}
	return response, nil
}

func (c *workerHTTPClient) CompleteWorkerWork(ctx context.Context, workerID string, workID string, input managedagents.CompleteWorkerWorkInput) (managedagents.WorkerWork, error) {
	var response managedagents.WorkerWork
	path := "/v1/workers/" + url.PathEscape(workerID) + "/work/" + url.PathEscape(workID) + "/result"
	if err := c.do(ctx, http.MethodPost, path, input, &response); err != nil {
		return managedagents.WorkerWork{}, err
	}
	return response, nil
}

func (c *workerHTTPClient) UploadArtifact(ctx context.Context, input workruntime.ArtifactUpload) (tools.ArtifactRef, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return tools.ArtifactRef{}, fmt.Errorf("worker artifact upload requires session id")
	}
	filename := strings.TrimSpace(input.Name)
	if filename == "" {
		filename = filepath.Base(strings.TrimSpace(input.Path))
	}
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = "artifact.bin"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writeMultipartField := func(name string, value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		return writer.WriteField(name, value)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "environment_id", value: input.EnvironmentID},
		{name: "turn_id", value: input.TurnID},
		{name: "tool_call_id", value: input.ToolCallID},
		{name: "name", value: filename},
		{name: "description", value: input.Description},
		{name: "artifact_type", value: defaultString(input.ArtifactType, managedagents.ArtifactTypeFile)},
		{name: "content_type", value: input.ContentType},
		{name: "visibility", value: managedagents.ObjectVisibilitySession},
		{name: "created_by", value: "tma-worker"},
		{name: "metadata", value: fmt.Sprintf(`{"protocol_version":"tma.worker_artifact.v1","path":%q}`, input.Path)},
	} {
		if err := writeMultipartField(field.name, field.value); err != nil {
			return tools.ArtifactRef{}, err
		}
	}
	file, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return tools.ArtifactRef{}, err
	}
	if _, err := file.Write(input.Content); err != nil {
		return tools.ArtifactRef{}, err
	}
	if err := writer.Close(); err != nil {
		return tools.ArtifactRef{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/sessions/"+url.PathEscape(sessionID)+"/artifacts/upload", &body)
	if err != nil {
		return tools.ArtifactRef{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return tools.ArtifactRef{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(response.Body)
		return tools.ArtifactRef{}, fmt.Errorf("POST /v1/sessions/%s/artifacts/upload returned %s: %s", sessionID, response.Status, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		ObjectRef managedagents.ObjectRef       `json:"object_ref"`
		Artifact  managedagents.SessionArtifact `json:"artifact"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return tools.ArtifactRef{}, err
	}
	return tools.ArtifactRef{
		ArtifactID:   decoded.Artifact.ID,
		ObjectRefID:  decoded.ObjectRef.ID,
		Name:         decoded.Artifact.Name,
		ArtifactType: decoded.Artifact.ArtifactType,
		DownloadPath: "/v1/sessions/" + sessionID + "/artifacts/" + decoded.Artifact.ID + "/download",
	}, nil
}

func (c *workerHTTPClient) do(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(res.Body)
		return fmt.Errorf("%s %s returned %s: %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	if responseBody == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(responseBody)
}

func workerMetadata() json.RawMessage {
	encoded, err := json.Marshal(map[string]string{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func defaultWorkerName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "tma-worker"
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func getenvDefaultInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func getenvDefaultDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}
