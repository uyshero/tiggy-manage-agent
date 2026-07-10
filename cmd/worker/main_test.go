package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workruntime"
)

func TestRunWorkerPollsAndCompletesOneWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	api := &fakeWorkerAPI{
		onComplete: cancel,
		work: &managedagents.WorkerWork{
			ID:       "work_000001",
			WorkType: managedagents.WorkerWorkTypeToolExecution,
			Status:   managedagents.WorkerWorkStatusPending,
			Payload:  json.RawMessage(`{"protocol_version":"tma.work.v1","namespace":"default","api":"run_command","capabilities":["exec"],"risk":"exec","runtime":"local_system","input":{"command":"sh","args":["-c","printf worker-tool"]}}`),
		},
	}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	executor := workruntime.DefaultExecutor("test-worker")
	err := runWorker(ctx, api, workerConfig{
		Name:              "test-worker",
		WorkspaceID:       managedagents.DefaultWorkspaceID,
		WorkerType:        managedagents.WorkerTypeLocal,
		RegisteredBy:      "test",
		LeaseSeconds:      30,
		PollInterval:      time.Hour,
		HeartbeatInterval: time.Hour,
	}, executor, logger)
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("expected worker context to be canceled")
	}
	if api.registerCalls != 1 || api.heartbeatCalls != 1 || api.pollCalls != 1 || api.ackCalls != 1 || api.completeCalls != 1 {
		t.Fatalf("unexpected calls: register=%d heartbeat=%d poll=%d ack=%d complete=%d", api.registerCalls, api.heartbeatCalls, api.pollCalls, api.ackCalls, api.completeCalls)
	}
	if api.completedWorkID != "work_000001" {
		t.Fatalf("expected completed work id, got %q", api.completedWorkID)
	}
	var result map[string]any
	if err := json.Unmarshal(api.completedResult, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["worker_name"] != "test-worker" || result["status"] != "executed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	toolResult, ok := result["tool_result"].(map[string]any)
	if !ok || toolResult["content"] != "worker-tool" {
		t.Fatalf("unexpected tool result: %#v", result["tool_result"])
	}
	assertWorkerCapabilities(t, api.registerCapabilities, executor.WorkerCapabilities())
	assertWorkerCapabilities(t, api.heartbeatCapabilities, executor.WorkerCapabilities())
}

func TestParseWorkerConfigConcurrency(t *testing.T) {
	cfg, err := parseWorkerConfig([]string{"--name", "test-worker"})
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if cfg.Concurrency != 1 {
		t.Fatalf("expected default concurrency 1, got %d", cfg.Concurrency)
	}

	cfg, err = parseWorkerConfig([]string{"--name", "test-worker", "--concurrency", "3"})
	if err != nil {
		t.Fatalf("parse configured concurrency: %v", err)
	}
	if cfg.Concurrency != 3 {
		t.Fatalf("expected configured concurrency 3, got %d", cfg.Concurrency)
	}
}

func TestParseWorkerConfigWorkHeartbeatInterval(t *testing.T) {
	cfg, err := parseWorkerConfig([]string{"--name", "test-worker"})
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if cfg.WorkHeartbeatInterval != 15*time.Second {
		t.Fatalf("expected default work heartbeat interval 15s, got %s", cfg.WorkHeartbeatInterval)
	}

	cfg, err = parseWorkerConfig([]string{"--name", "test-worker", "--work-heartbeat-interval", "250ms"})
	if err != nil {
		t.Fatalf("parse configured work heartbeat interval: %v", err)
	}
	if cfg.WorkHeartbeatInterval != 250*time.Millisecond {
		t.Fatalf("expected configured work heartbeat interval 250ms, got %s", cfg.WorkHeartbeatInterval)
	}
}

func TestParseWorkerConfigShutdownTimeout(t *testing.T) {
	cfg, err := parseWorkerConfig([]string{"--name", "test-worker"})
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("expected default shutdown timeout 30s, got %s", cfg.ShutdownTimeout)
	}

	cfg, err = parseWorkerConfig([]string{"--name", "test-worker", "--shutdown-timeout", "750ms"})
	if err != nil {
		t.Fatalf("parse configured shutdown timeout: %v", err)
	}
	if cfg.ShutdownTimeout != 750*time.Millisecond {
		t.Fatalf("expected configured shutdown timeout 750ms, got %s", cfg.ShutdownTimeout)
	}
}

func TestRunWorkerProcessesConcurrentWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	api := &fakeWorkerAPI{
		works: []*managedagents.WorkerWork{{
			ID:       "work_000001",
			WorkType: managedagents.WorkerWorkTypeSandboxCommand,
			Status:   managedagents.WorkerWorkStatusPending,
			Payload:  json.RawMessage(`{"command":"sh","args":["-c","printf one"]}`),
		}, {
			ID:       "work_000002",
			WorkType: managedagents.WorkerWorkTypeSandboxCommand,
			Status:   managedagents.WorkerWorkStatusPending,
			Payload:  json.RawMessage(`{"command":"sh","args":["-c","printf two"]}`),
		}},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var mu sync.Mutex
	startedCount := 0
	executor := workruntime.Executor{
		WorkerName: "concurrent-worker",
		Handlers: map[string]workruntime.WorkHandler{
			managedagents.WorkerWorkTypeSandboxCommand: workruntime.WorkHandlerFunc(func(ctx context.Context, _ workruntime.Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
				mu.Lock()
				startedCount++
				if startedCount == 2 {
					startedOnce.Do(func() { close(started) })
				}
				mu.Unlock()
				select {
				case <-release:
				case <-ctx.Done():
					return managedagents.CompleteWorkerWorkInput{Success: false, ErrorMessage: ctx.Err().Error()}
				}
				result, _ := json.Marshal(map[string]any{"work_id": work.ID})
				return managedagents.CompleteWorkerWorkInput{Success: true, Result: result}
			}),
		},
		DeclaredCapabilities: &tools.WorkerCapabilities{
			Namespaces:   []string{"default"},
			APIs:         []string{"default.run_command"},
			Runtimes:     []string{"local_system"},
			Capabilities: []string{"exec"},
		},
	}
	api.onComplete = func() {
		if api.completeCount() >= 2 {
			cancel()
		}
	}

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWorker(ctx, api, workerConfig{
			Name:              "concurrent-worker",
			WorkspaceID:       managedagents.DefaultWorkspaceID,
			WorkerType:        managedagents.WorkerTypeLocal,
			RegisteredBy:      "test",
			LeaseSeconds:      30,
			PollInterval:      time.Hour,
			HeartbeatInterval: time.Hour,
			Concurrency:       2,
		}, executor, logger)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected two work items to start concurrently")
	}
	if api.ackCount() != 2 {
		t.Fatalf("expected two acknowledged work items before release, got %d", api.ackCount())
	}
	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after completing concurrent work")
	}
	if api.completeCount() != 2 {
		t.Fatalf("expected two completed work items, got %d", api.completeCount())
	}
}

func TestRunWorkerDrainsRunningWorkOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	api := &fakeWorkerAPI{
		work: &managedagents.WorkerWork{
			ID:       "work_drain",
			WorkType: managedagents.WorkerWorkTypeSandboxCommand,
			Status:   managedagents.WorkerWorkStatusPending,
		},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	executor := workruntime.Executor{
		WorkerName: "drain-worker",
		Handlers: map[string]workruntime.WorkHandler{
			managedagents.WorkerWorkTypeSandboxCommand: workruntime.WorkHandlerFunc(func(ctx context.Context, _ workruntime.Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
				startedOnce.Do(func() { close(started) })
				select {
				case <-release:
				case <-ctx.Done():
					return managedagents.CompleteWorkerWorkInput{Success: false, ErrorMessage: ctx.Err().Error()}
				}
				result, _ := json.Marshal(map[string]any{"work_id": work.ID})
				return managedagents.CompleteWorkerWorkInput{Success: true, Result: result}
			}),
		},
	}

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	errCh := make(chan error, 1)
	go func() {
		errCh <- runWorker(ctx, api, workerConfig{
			Name:              "drain-worker",
			WorkspaceID:       managedagents.DefaultWorkspaceID,
			WorkerType:        managedagents.WorkerTypeLocal,
			RegisteredBy:      "test",
			LeaseSeconds:      30,
			PollInterval:      time.Hour,
			HeartbeatInterval: time.Hour,
			ShutdownTimeout:   2 * time.Second,
		}, executor, logger)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected work handler to start")
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for !api.hasHeartbeatStatus(managedagents.WorkerStatusDraining) {
		select {
		case <-deadline:
			t.Fatal("expected worker to heartbeat draining during shutdown")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if api.completeCount() != 0 {
		t.Fatalf("expected running work not to complete before release, got %d", api.completeCount())
	}
	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after drained work completed")
	}
	if api.completeCount() != 1 {
		t.Fatalf("expected drained work to complete once, got %d", api.completeCount())
	}
	if api.completedWorkID != "work_drain" {
		t.Fatalf("expected completed drained work id, got %q", api.completedWorkID)
	}
}

func TestProcessWorkHeartbeatsWhileRunning(t *testing.T) {
	api := &fakeWorkerAPI{}
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	executor := workruntime.Executor{
		WorkerName: "heartbeat-worker",
		Handlers: map[string]workruntime.WorkHandler{
			managedagents.WorkerWorkTypeSandboxCommand: workruntime.WorkHandlerFunc(func(ctx context.Context, _ workruntime.Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
				startedOnce.Do(func() { close(started) })
				select {
				case <-release:
				case <-ctx.Done():
					return managedagents.CompleteWorkerWorkInput{Success: false, ErrorMessage: ctx.Err().Error()}
				}
				result, _ := json.Marshal(map[string]any{"work_id": work.ID})
				return managedagents.CompleteWorkerWorkInput{Success: true, Result: result}
			}),
		},
	}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	errCh := make(chan error, 1)
	go func() {
		errCh <- processWork(t.Context(), api, workerConfig{
			LeaseSeconds:          45,
			WorkHeartbeatInterval: 10 * time.Millisecond,
		}, "wrk_000001", executor, logger, managedagents.WorkerWork{
			ID:       "work_heartbeat",
			WorkType: managedagents.WorkerWorkTypeSandboxCommand,
			Status:   managedagents.WorkerWorkStatusLeased,
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected work handler to start")
	}
	deadline := time.After(2 * time.Second)
	for api.workHeartbeatCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected running work heartbeat")
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("process work: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process work did not finish")
	}
	if api.workHeartbeatWorkID != "work_heartbeat" {
		t.Fatalf("expected work heartbeat id, got %q", api.workHeartbeatWorkID)
	}
	if api.workHeartbeatLeaseSeconds != 45 {
		t.Fatalf("expected work heartbeat lease seconds 45, got %d", api.workHeartbeatLeaseSeconds)
	}
	if api.completeCount() != 1 {
		t.Fatalf("expected completed work, got %d", api.completeCount())
	}
}

func TestProcessWorkStopsWithoutCompletingWhenHeartbeatReturnsCanceled(t *testing.T) {
	api := &fakeWorkerAPI{workHeartbeatStatus: managedagents.WorkerWorkStatusCanceled}
	started := make(chan struct{})
	var startedOnce sync.Once
	executor := workruntime.Executor{
		WorkerName: "cancel-worker",
		Handlers: map[string]workruntime.WorkHandler{
			managedagents.WorkerWorkTypeSandboxCommand: workruntime.WorkHandlerFunc(func(ctx context.Context, _ workruntime.Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
				startedOnce.Do(func() { close(started) })
				<-ctx.Done()
				result, _ := json.Marshal(map[string]any{"work_id": work.ID, "canceled": true})
				return managedagents.CompleteWorkerWorkInput{Success: false, Result: result, ErrorMessage: ctx.Err().Error()}
			}),
		},
	}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	errCh := make(chan error, 1)
	go func() {
		errCh <- processWork(t.Context(), api, workerConfig{
			LeaseSeconds:          45,
			WorkHeartbeatInterval: 10 * time.Millisecond,
		}, "wrk_000001", executor, logger, managedagents.WorkerWork{
			ID:       "work_cancel",
			WorkType: managedagents.WorkerWorkTypeSandboxCommand,
			Status:   managedagents.WorkerWorkStatusLeased,
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected work handler to start")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("process work: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process work did not finish after cancel heartbeat")
	}
	if api.workHeartbeatCount() == 0 {
		t.Fatal("expected work heartbeat")
	}
	if api.completeCount() != 0 {
		t.Fatalf("expected canceled work not to complete, got %d", api.completeCount())
	}
}

func TestRunWorkerRegistersExecutorDeclaredCapabilities(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	declared := tools.WorkerCapabilities{
		Namespaces:   []string{"artifact"},
		APIs:         []string{"artifact.write"},
		Runtimes:     []string{"local_system"},
		Capabilities: []string{"artifact.write"},
	}
	api := &fakeWorkerAPI{onHeartbeat: cancel}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	err := runWorker(ctx, api, workerConfig{
		Name:              "custom-worker",
		WorkspaceID:       managedagents.DefaultWorkspaceID,
		WorkerType:        managedagents.WorkerTypeLocal,
		RegisteredBy:      "test",
		LeaseSeconds:      30,
		PollInterval:      time.Hour,
		HeartbeatInterval: time.Hour,
	}, workruntime.Executor{
		WorkerName:           "custom-worker",
		DeclaredCapabilities: &declared,
	}, logger)
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	assertWorkerCapabilities(t, api.registerCapabilities, declared)
	assertWorkerCapabilities(t, api.heartbeatCapabilities, declared)
}

func TestRunWorkerDoctorChecksLifecycleAndArchives(t *testing.T) {
	api := &fakeWorkerAPI{}
	output := &bytes.Buffer{}
	executor := workruntime.DefaultExecutor("doctor-worker")

	err := runWorkerDoctor(t.Context(), api, workerConfig{
		BaseURL:      "http://tma.test",
		Name:         "doctor-worker",
		WorkspaceID:  managedagents.DefaultWorkspaceID,
		WorkerType:   managedagents.WorkerTypeLocal,
		RegisteredBy: "test",
		LeaseSeconds: 30,
	}, executor, output)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if api.healthCalls != 1 || api.registerCalls != 1 || api.heartbeatCalls != 1 || api.pollCalls != 1 || api.diagnoseCalls != 1 || api.archiveCalls != 1 {
		t.Fatalf("unexpected doctor calls: health=%d register=%d heartbeat=%d poll=%d diagnose=%d archive=%d",
			api.healthCalls,
			api.registerCalls,
			api.heartbeatCalls,
			api.pollCalls,
			api.diagnoseCalls,
			api.archiveCalls,
		)
	}
	if api.registerName != "doctor-worker-doctor" {
		t.Fatalf("unexpected doctor worker name %q", api.registerName)
	}
	if api.diagnoseRequest.WorkspaceID != managedagents.DefaultWorkspaceID || api.diagnoseRequest.API == "" || api.diagnoseRequest.Runtime == "" {
		t.Fatalf("unexpected diagnose request: %+v", api.diagnoseRequest)
	}
	assertWorkerCapabilities(t, api.registerCapabilities, executor.WorkerCapabilities())
	assertWorkerCapabilities(t, api.heartbeatCapabilities, executor.WorkerCapabilities())
	for _, expected := range []string{
		"server: ok",
		"capabilities:",
		"register: ok wrk_000001",
		"heartbeat: ok",
		"poll: ok",
		"diagnose: ok",
		"archive: ok",
		"doctor: ok",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected doctor output to contain %q, got %q", expected, output.String())
		}
	}
}

func TestRunWorkerDoctorArchivesOnFailureAfterRegister(t *testing.T) {
	api := &fakeWorkerAPI{heartbeatError: context.Canceled}
	err := runWorkerDoctor(t.Context(), api, workerConfig{
		BaseURL:      "http://tma.test",
		Name:         "doctor-worker",
		WorkspaceID:  managedagents.DefaultWorkspaceID,
		WorkerType:   managedagents.WorkerTypeLocal,
		RegisteredBy: "test",
		LeaseSeconds: 30,
	}, workruntime.DefaultExecutor("doctor-worker"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected doctor failure")
	}
	if api.archiveCalls != 1 {
		t.Fatalf("expected doctor to archive registered worker on failure, got %d", api.archiveCalls)
	}
}

func TestExecuteWorkRejectsInvalidToolExecutionPayload(t *testing.T) {
	result := executeWork(t.Context(), workruntime.DefaultExecutor("test-worker"), managedagents.WorkerWork{
		ID:       "work_000004",
		WorkType: managedagents.WorkerWorkTypeToolExecution,
		Payload:  json.RawMessage(`{"hello":"worker"}`),
	})
	if result.Success {
		t.Fatalf("expected invalid tool execution to fail, got result %s", string(result.Result))
	}
	if !strings.Contains(result.ErrorMessage, "unsupported tool namespace") {
		t.Fatalf("unexpected error message %q", result.ErrorMessage)
	}
}

func TestExecuteWorkRunsSandboxCommandWithLocalSystemProvider(t *testing.T) {
	payload, err := json.Marshal(capability.RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "printf worker-command"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result := executeWork(t.Context(), workruntime.DefaultExecutor("test-worker"), managedagents.WorkerWork{
		ID:       "work_000002",
		WorkType: managedagents.WorkerWorkTypeSandboxCommand,
		Payload:  payload,
	})
	if !result.Success {
		t.Fatalf("expected successful sandbox command, got error %q result %s", result.ErrorMessage, string(result.Result))
	}
	var body struct {
		Status        string `json:"status"`
		CommandResult struct {
			ExitCode int    `json:"exit_code"`
			Stdout   string `json:"stdout"`
		} `json:"command_result"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if body.Status != "executed" || body.CommandResult.ExitCode != 0 || body.CommandResult.Stdout != "worker-command" {
		t.Fatalf("unexpected sandbox command result: %+v", body)
	}
}

func TestExecuteWorkFailsSandboxCommandOnNonZeroExit(t *testing.T) {
	payload, err := json.Marshal(capability.RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "exit 9"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result := executeWork(t.Context(), workruntime.DefaultExecutor("test-worker"), managedagents.WorkerWork{
		ID:       "work_000003",
		WorkType: managedagents.WorkerWorkTypeSandboxCommand,
		Payload:  payload,
	})
	if result.Success {
		t.Fatalf("expected failed sandbox command, got result %s", string(result.Result))
	}
	if result.ErrorMessage != "command exited with code 9" {
		t.Fatalf("unexpected error message %q", result.ErrorMessage)
	}
}

func TestWorkerHTTPClientSendsBearerToken(t *testing.T) {
	var seenAuth string
	client := &workerHTTPClient{
		baseURL: "http://example.invalid",
		token:   "worker-secret",
		http: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				seenAuth = r.Header.Get("Authorization")
				if r.Method != http.MethodPost || r.URL.Path != "/v1/workers" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{
					StatusCode: http.StatusCreated,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewBufferString(`{"id":"wrk_000001","workspace_id":"wksp_default","name":"test-worker","worker_type":"local","status":"online"}`)),
				}, nil
			}),
		},
	}
	worker, err := client.RegisterWorker(t.Context(), managedagents.RegisterWorkerInput{Name: "test-worker"})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	if worker.ID != "wrk_000001" {
		t.Fatalf("unexpected worker: %+v", worker)
	}
	if seenAuth != "Bearer worker-secret" {
		t.Fatalf("expected worker bearer token, got %q", seenAuth)
	}
}

func TestWorkerHTTPClientHeartbeatsWorkerWork(t *testing.T) {
	var seenBody managedagents.WorkerWorkHeartbeatInput
	client := &workerHTTPClient{
		baseURL: "http://example.invalid",
		http: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/workers/wrk_000001/work/work_000001/heartbeat" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewBufferString(`{"id":"work_000001","status":"running"}`)),
				}, nil
			}),
		},
	}
	work, err := client.HeartbeatWorkerWork(t.Context(), "wrk_000001", "work_000001", managedagents.WorkerWorkHeartbeatInput{LeaseSeconds: 45})
	if err != nil {
		t.Fatalf("heartbeat work: %v", err)
	}
	if work.ID != "work_000001" || work.Status != managedagents.WorkerWorkStatusRunning {
		t.Fatalf("unexpected heartbeat response: %+v", work)
	}
	if seenBody.LeaseSeconds != 45 {
		t.Fatalf("expected lease seconds 45, got %d", seenBody.LeaseSeconds)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

type fakeWorkerAPI struct {
	mu             sync.Mutex
	onComplete     func()
	onHeartbeat    func()
	heartbeatError error

	work                  *managedagents.WorkerWork
	works                 []*managedagents.WorkerWork
	completedWorkID       string
	completedResult       json.RawMessage
	registerCapabilities  json.RawMessage
	heartbeatCapabilities json.RawMessage
	heartbeatStatuses     []string
	registerName          string
	diagnoseRequest       workerDiagnoseRequest

	healthCalls               int
	registerCalls             int
	heartbeatCalls            int
	pollCalls                 int
	ackCalls                  int
	workHeartbeatCalls        int
	workHeartbeatWorkID       string
	workHeartbeatLeaseSeconds int
	workHeartbeatStatus       string
	completeCalls             int
	diagnoseCalls             int
	archiveCalls              int
}

func (a *fakeWorkerAPI) Health(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.healthCalls++
	return nil
}

func (a *fakeWorkerAPI) RegisterWorker(_ context.Context, input managedagents.RegisterWorkerInput) (managedagents.Worker, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.registerCalls++
	a.registerCapabilities = append(json.RawMessage(nil), input.Capabilities...)
	a.registerName = input.Name
	return managedagents.Worker{
		ID:          "wrk_000001",
		WorkspaceID: managedagents.DefaultWorkspaceID,
		WorkerType:  managedagents.WorkerTypeLocal,
		Status:      managedagents.WorkerStatusOnline,
	}, nil
}

func (a *fakeWorkerAPI) HeartbeatWorker(_ context.Context, _ string, input managedagents.WorkerHeartbeatInput) (managedagents.Worker, error) {
	a.mu.Lock()
	a.heartbeatCalls++
	a.heartbeatCapabilities = append(json.RawMessage(nil), input.Capabilities...)
	a.heartbeatStatuses = append(a.heartbeatStatuses, defaultString(input.Status, managedagents.WorkerStatusOnline))
	err := a.heartbeatError
	onHeartbeat := a.onHeartbeat
	a.mu.Unlock()
	if err != nil {
		return managedagents.Worker{}, err
	}
	if onHeartbeat != nil {
		onHeartbeat()
	}
	return managedagents.Worker{ID: "wrk_000001", Status: managedagents.WorkerStatusOnline}, nil
}

func (a *fakeWorkerAPI) PollWorkerWork(context.Context, string, managedagents.PollWorkerWorkInput) (*managedagents.WorkerWork, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pollCalls++
	if len(a.works) > 0 {
		work := a.works[0]
		a.works = a.works[1:]
		return work, nil
	}
	work := a.work
	a.work = nil
	return work, nil
}

func (a *fakeWorkerAPI) AckWorkerWork(_ context.Context, _ string, workID string) (managedagents.WorkerWork, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ackCalls++
	return managedagents.WorkerWork{ID: workID, Status: managedagents.WorkerWorkStatusRunning}, nil
}

func (a *fakeWorkerAPI) HeartbeatWorkerWork(_ context.Context, _ string, workID string, input managedagents.WorkerWorkHeartbeatInput) (managedagents.WorkerWork, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workHeartbeatCalls++
	a.workHeartbeatWorkID = workID
	a.workHeartbeatLeaseSeconds = input.LeaseSeconds
	return managedagents.WorkerWork{ID: workID, Status: defaultString(a.workHeartbeatStatus, managedagents.WorkerWorkStatusRunning)}, nil
}

func (a *fakeWorkerAPI) CompleteWorkerWork(_ context.Context, _ string, workID string, input managedagents.CompleteWorkerWorkInput) (managedagents.WorkerWork, error) {
	a.mu.Lock()
	a.completeCalls++
	a.completedWorkID = workID
	a.completedResult = append(json.RawMessage(nil), input.Result...)
	onComplete := a.onComplete
	a.mu.Unlock()
	if onComplete != nil {
		onComplete()
	}
	return managedagents.WorkerWork{ID: workID, Status: managedagents.WorkerWorkStatusCompleted}, nil
}

func (a *fakeWorkerAPI) DiagnoseWorkers(_ context.Context, input workerDiagnoseRequest) (workerDiagnoseResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.diagnoseCalls++
	a.diagnoseRequest = input
	return workerDiagnoseResponse{
		Matches: 1,
		Diagnostics: []workerDiagnosisResult{{
			WorkerID: "wrk_000001",
			Name:     a.registerName,
			Status:   managedagents.WorkerStatusOnline,
			Match:    true,
		}},
	}, nil
}

func (a *fakeWorkerAPI) ArchiveWorker(context.Context, string) (managedagents.Worker, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archiveCalls++
	return managedagents.Worker{ID: "wrk_000001", Status: managedagents.WorkerStatusArchived}, nil
}

func (a *fakeWorkerAPI) ackCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ackCalls
}

func (a *fakeWorkerAPI) completeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.completeCalls
}

func (a *fakeWorkerAPI) workHeartbeatCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workHeartbeatCalls
}

func (a *fakeWorkerAPI) hasHeartbeatStatus(status string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, seen := range a.heartbeatStatuses {
		if seen == status {
			return true
		}
	}
	return false
}

func assertWorkerCapabilities(t *testing.T, raw json.RawMessage, expected tools.WorkerCapabilities) {
	t.Helper()

	var actual tools.WorkerCapabilities
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatalf("decode worker capabilities: %v", err)
	}
	assertStringSet(t, actual.Namespaces, expected.Namespaces, "namespaces")
	assertStringSet(t, actual.APIs, expected.APIs, "apis")
	assertStringSet(t, actual.Runtimes, expected.Runtimes, "runtimes")
	assertStringSet(t, actual.Capabilities, expected.Capabilities, "capabilities")
}

func assertStringSet(t *testing.T, actual []string, expected []string, label string) {
	t.Helper()

	actualSet := make(map[string]bool, len(actual))
	for _, value := range actual {
		actualSet[value] = true
	}
	for _, value := range expected {
		if !actualSet[value] {
			t.Fatalf("expected %s to include %q, got %#v", label, value, actual)
		}
	}
}
