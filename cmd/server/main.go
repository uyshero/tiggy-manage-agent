package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/httpapi"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/serverconfig"
	"tiggy-manage-agent/internal/tools"
)

type serverCLIOptions struct {
	PIDFile     string
	Restart     bool
	RestartWait time.Duration
}

func main() {
	options, err := parseServerCLIOptions(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "tma-server: %v\n", err)
		os.Exit(2)
	}

	// 统一使用 JSON 结构化日志，方便后续按 session_id / turn_id 检索。
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := maybeRestartExistingServer(options, logger); err != nil {
		logger.Error("restart existing server failed", "error", err)
		os.Exit(1)
	}

	config, err := serverconfig.Load(".env")
	if err != nil {
		logger.Error("load server config failed", "error", err)
		os.Exit(1)
	}

	store, cleanup := mustOpenStore(config.DatabaseURL, logger)
	defer cleanup()
	if err := ensureDefaultLLMProvider(store, config, logger); err != nil {
		logger.Error("ensure default llm provider failed", "error", err)
		os.Exit(1)
	}
	if err := ensureBuiltinGeneralAgent(store, config, logger); err != nil {
		logger.Error("ensure builtin general agent failed", "error", err)
		os.Exit(1)
	}
	objectStore := buildObjectStore(config, logger)
	executionResolver, executionCleanup := buildExecutionResolver(config, store, objectStore, logger)
	defer executionCleanup()
	turnRunner, runnerCleanup, err := buildRunner(config, store, objectStore, executionResolver, logger)
	if err != nil {
		logger.Error("build runner failed", "error", err)
		os.Exit(1)
	}
	defer runnerCleanup()

	server := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           httpapi.NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(store, turnRunner, logger, config.LLM.Provider, config.LLM.Model, objectStore, executionResolver, config.Worker.AuthToken, config.Worker.ControlAuthToken),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", config.HTTPAddr)
	if err != nil {
		logger.Error("listen failed", "addr", config.HTTPAddr, "error", err)
		os.Exit(1)
	}
	pidFileCleanup, err := writePIDFile(options.PIDFile, os.Getpid())
	if err != nil {
		logger.Error("write pid file failed", "pid_file", options.PIDFile, "error", err)
		_ = listener.Close()
		os.Exit(1)
	}
	defer pidFileCleanup()
	stopWorkerReaper := startWorkerReaper(config.Worker.Reaper, store, logger)
	stopWorkerWorkReaper := startWorkerWorkReaper(config.Worker.WorkReaper, store, logger)
	stopObservabilityRetry := startObservabilityExporterRetry(config.Observability.ExporterRetry, store, logger)
	stopTraceIndexRetention := func() {}
	if traceStore, ok := store.(traceIndexRetentionStore); ok {
		stopTraceIndexRetention = startTraceIndexRetention(config.Observability.TraceIndexRetention, traceStore, logger)
	} else {
		logger.Warn("trace index retention disabled: store does not support trace index pruning")
	}

	go func() {
		logger.Info("tma server listening", "addr", config.HTTPAddr)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdownSignal()
	stopTraceIndexRetention()
	stopObservabilityRetry()
	stopWorkerReaper()
	stopWorkerWorkReaper()

	// 收到退出信号后给连接一点时间完成，避免直接中断 SSE / HTTP 请求。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

func parseServerCLIOptions(args []string) (serverCLIOptions, error) {
	options := serverCLIOptions{}
	flags := flag.NewFlagSet("tma-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&options.PIDFile, "pid-file", "", "write current pid to this file")
	flags.BoolVar(&options.Restart, "restart", false, "stop existing process from --pid-file before starting")
	flags.DurationVar(&options.RestartWait, "restart-wait", 15*time.Second, "maximum wait time for --restart to stop the existing process")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s [--pid-file PATH] [--restart] [--restart-wait DURATION]\n", flags.Name())
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return serverCLIOptions{}, err
	}
	if flags.NArg() > 0 {
		return serverCLIOptions{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if options.Restart && strings.TrimSpace(options.PIDFile) == "" {
		return serverCLIOptions{}, fmt.Errorf("--restart requires --pid-file")
	}
	if options.RestartWait <= 0 {
		return serverCLIOptions{}, fmt.Errorf("--restart-wait must be greater than 0")
	}
	options.PIDFile = strings.TrimSpace(options.PIDFile)
	return options, nil
}

func maybeRestartExistingServer(options serverCLIOptions, logger *slog.Logger) error {
	if !options.Restart {
		return nil
	}
	pid, err := readPIDFile(options.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("restart requested but pid file was not found, continuing", "pid_file", options.PIDFile)
			return nil
		}
		return err
	}
	if pid == os.Getpid() {
		return fmt.Errorf("pid file %s points to current process", options.PIDFile)
	}
	exists, err := processExists(pid)
	if err != nil {
		return err
	}
	if !exists {
		logger.Info("restart requested but pid file points to a stopped process, continuing", "pid_file", options.PIDFile, "pid", pid)
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find existing process %d: %w", pid, err)
	}
	logger.Info("stopping existing server before restart",
		"pid_file", options.PIDFile,
		"pid", pid,
		"restart_wait", options.RestartWait.String(),
	)
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal existing process %d: %w", pid, err)
	}
	deadline := time.Now().Add(options.RestartWait)
	for {
		exists, err := processExists(pid)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("existing process %d did not exit within %s", pid, options.RestartWait)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func writePIDFile(path string, pid int) (func(), error) {
	if strings.TrimSpace(path) == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create pid file directory: %w", err)
	}
	tempPath := fmt.Sprintf("%s.%d.tmp", path, pid)
	if err := os.WriteFile(tempPath, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write temp pid file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("rename pid file: %w", err)
	}
	return func() {
		removePIDFileIfOwned(path, pid)
	}, nil
}

func removePIDFileIfOwned(path string, pid int) {
	recordedPID, err := readPIDFile(path)
	if err != nil || recordedPID != pid {
		return
	}
	_ = os.Remove(path)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid file %s: %w", path, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("parse pid file %s: invalid pid %d", path, pid)
	}
	return pid, nil
}

func processExists(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return false, nil
		}
		if errors.Is(err, syscall.EPERM) {
			return true, nil
		}
		return false, fmt.Errorf("check process %d: %w", pid, err)
	}
	return true, nil
}

type workerWorkReaperStore interface {
	ReapExpiredWorkerWork(input managedagents.ReapExpiredWorkerWorkInput) ([]managedagents.WorkerWork, error)
}

type workerReaperStore interface {
	ReapExpiredWorkers(input managedagents.ReapExpiredWorkersInput) ([]managedagents.Worker, error)
}

type traceIndexRetentionStore interface {
	PruneTraceIndexes(input managedagents.PruneTraceIndexInput) (int, error)
}

func startWorkerReaper(config serverconfig.WorkerReaperConfig, store workerReaperStore, logger *slog.Logger) func() {
	if !config.Enabled {
		logger.Info("worker reaper disabled")
		return func() {}
	}
	if config.Interval <= 0 || config.Limit <= 0 {
		logger.Warn("worker reaper disabled by invalid config",
			"interval", config.Interval,
			"limit", config.Limit,
		)
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		logger.Info("worker reaper started",
			"interval", config.Interval.String(),
			"limit", config.Limit,
		)
		reapExpiredWorkers(store, logger, config.Limit)
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("worker reaper stopped")
				return
			case <-ticker.C:
				reapExpiredWorkers(store, logger, config.Limit)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func startWorkerWorkReaper(config serverconfig.WorkerWorkReaperConfig, store workerWorkReaperStore, logger *slog.Logger) func() {
	if !config.Enabled {
		logger.Info("worker work reaper disabled")
		return func() {}
	}
	if config.Interval <= 0 || config.Limit <= 0 {
		logger.Warn("worker work reaper disabled by invalid config",
			"interval", config.Interval,
			"limit", config.Limit,
		)
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		logger.Info("worker work reaper started",
			"interval", config.Interval.String(),
			"limit", config.Limit,
		)
		reapExpiredWorkerWork(store, logger, config.Limit)
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("worker work reaper stopped")
				return
			case <-ticker.C:
				reapExpiredWorkerWork(store, logger, config.Limit)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func reapExpiredWorkers(store workerReaperStore, logger *slog.Logger, limit int) {
	expired, err := store.ReapExpiredWorkers(managedagents.ReapExpiredWorkersInput{Limit: limit})
	if err != nil {
		logger.Warn("worker reaper failed", "error", err)
		return
	}
	if len(expired) == 0 {
		return
	}
	logger.Info("worker reaper expired workers",
		"count", len(expired),
	)
}

func reapExpiredWorkerWork(store workerWorkReaperStore, logger *slog.Logger, limit int) {
	expired, err := store.ReapExpiredWorkerWork(managedagents.ReapExpiredWorkerWorkInput{Limit: limit})
	if err != nil {
		logger.Warn("worker work reaper failed", "error", err)
		return
	}
	if len(expired) == 0 {
		return
	}
	logger.Info("worker work reaper expired work",
		"count", len(expired),
	)
}

func startTraceIndexRetention(config serverconfig.TraceIndexRetentionConfig, store traceIndexRetentionStore, logger *slog.Logger) func() {
	if !config.Enabled {
		logger.Info("trace index retention worker disabled")
		return func() {}
	}
	if config.Interval <= 0 || config.Retention <= 0 || config.Limit <= 0 {
		logger.Warn("trace index retention worker disabled by invalid config",
			"interval", config.Interval,
			"retention", config.Retention,
			"limit", config.Limit,
		)
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		logger.Info("trace index retention worker started",
			"interval", config.Interval.String(),
			"retention", config.Retention.String(),
			"limit", config.Limit,
		)
		pruneTraceIndexes(store, logger, config.Retention, config.Limit)
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("trace index retention worker stopped")
				return
			case <-ticker.C:
				pruneTraceIndexes(store, logger, config.Retention, config.Limit)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func pruneTraceIndexes(store traceIndexRetentionStore, logger *slog.Logger, retention time.Duration, limit int) {
	before := time.Now().UTC().Add(-retention)
	pruned, err := store.PruneTraceIndexes(managedagents.PruneTraceIndexInput{Before: before, Limit: limit})
	if err != nil {
		logger.Warn("trace index retention worker failed", "error", err)
		return
	}
	if pruned == 0 {
		return
	}
	logger.Info("trace index retention worker pruned trace indexes",
		"count", pruned,
		"before", before,
	)
}

func startObservabilityExporterRetry(config serverconfig.ObservabilityExporterRetryConfig, store observability.ExporterRunStore, logger *slog.Logger) func() {
	if !config.Enabled {
		logger.Info("observability exporter retry worker disabled")
		return func() {}
	}
	if config.Interval <= 0 || config.Limit <= 0 {
		logger.Warn("observability exporter retry worker disabled by invalid config",
			"interval", config.Interval,
			"limit", config.Limit,
		)
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		logger.Info("observability exporter retry worker started",
			"interval", config.Interval.String(),
			"limit", config.Limit,
		)
		retryObservabilityExporters(store, logger, config.Limit)
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Info("observability exporter retry worker stopped")
				return
			case <-ticker.C:
				retryObservabilityExporters(store, logger, config.Limit)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func retryObservabilityExporters(store observability.ExporterRunStore, logger *slog.Logger, limit int) {
	result, err := observability.RetryFailedExporterRuns(store, observability.EnvExporterConfig(), time.Now().UTC(), limit)
	if err != nil {
		logger.Warn("observability exporter retry worker failed", "error", err)
		return
	}
	if result.Attempted == 0 && result.Skipped == 0 {
		return
	}
	logger.Info("observability exporter retry worker completed",
		"attempted", result.Attempted,
		"succeeded", result.Succeeded,
		"failed", result.Failed,
		"skipped", result.Skipped,
	)
}

func mustOpenStore(databaseURL string, logger *slog.Logger) (managedagents.Store, func()) {
	// TMA 现在只保留 PostgresStore，避免生产代码同时维护两套状态机。
	store, err := managedagents.NewPostgresStore(databaseURL)
	if err != nil {
		logger.Error("open postgres store failed", "error", err)
		os.Exit(1)
	}

	logger.Info("using postgres store")
	return store, func() {
		if err := store.Close(); err != nil {
			logger.Error("close postgres store failed", "error", err)
		}
	}
}

func ensureDefaultLLMProvider(store managedagents.Store, config serverconfig.Config, logger *slog.Logger) error {
	providerType := llm.ResolveProviderType(config.LLM.Provider, config.LLM.ProviderType)
	baseURL := config.LLM.BaseURL
	apiKeyEnv := config.LLM.APIKeyEnv
	if providerType == llm.ProviderFake {
		baseURL = ""
		apiKeyEnv = ""
	}
	provider, err := store.EnsureLLMProvider(managedagents.EnsureLLMProviderInput{
		ID:           config.LLM.Provider,
		ProviderType: providerType,
		BaseURL:      baseURL,
		APIKeyEnv:    apiKeyEnv,
		Enabled:      true,
	})
	if err != nil {
		return err
	}
	logger.Info("ensured default llm provider",
		"llm_provider", provider.ID,
		"llm_provider_type", provider.ProviderType,
		"llm_base_url", provider.BaseURL,
		"llm_api_key_env", provider.APIKeyEnv,
	)
	model, err := store.UpsertLLMModel(managedagents.UpsertLLMModelInput{
		ProviderID:          provider.ID,
		Model:               config.LLM.Model,
		ContextWindowTokens: config.Context.DefaultWindowTokens,
	})
	if err != nil {
		return err
	}
	logger.Info("ensured default llm model",
		"llm_provider", model.ProviderID,
		"llm_model", model.Model,
		"context_window_tokens", model.ContextWindowTokens,
		"context_budget_ratio_percent", managedagents.ContextBudgetRatioPercent,
	)
	return nil
}

func ensureBuiltinGeneralAgent(store managedagents.Store, config serverconfig.Config, logger *slog.Logger) error {
	agent, err := store.EnsureAgent(managedagents.BuiltinGeneralAgentInput(config.LLM.Provider, config.LLM.Model))
	if err != nil {
		return err
	}
	logger.Info("ensured builtin general agent",
		"agent_id", agent.ID,
		"agent_name", agent.Name,
		"llm_provider", agent.ConfigVersion.LLMProvider,
		"llm_model", agent.ConfigVersion.LLMModel,
	)
	return nil
}

func buildObjectStore(config serverconfig.Config, logger *slog.Logger) objectstore.Client {
	objectStoreConfig := objectstore.Config{
		Provider:     config.ObjectStore.Provider,
		Endpoint:     config.ObjectStore.Endpoint,
		Region:       config.ObjectStore.Region,
		Bucket:       config.ObjectStore.Bucket,
		RootDir:      config.ObjectStore.RootDir,
		AccessKey:    config.ObjectStore.AccessKey,
		SecretKey:    config.ObjectStore.SecretKey,
		UsePathStyle: config.ObjectStore.UsePathStyle,
	}
	client, err := objectstore.NewClient(objectStoreConfig)
	if err != nil {
		logger.Error("build object store client failed", "error", err)
		os.Exit(1)
	}
	logger.Info("using object store client",
		"object_store_provider", objectStoreConfig.Provider,
		"object_store_endpoint", objectStoreConfig.Endpoint,
		"object_store_region", objectStoreConfig.Region,
		"object_store_bucket", objectStoreConfig.Bucket,
		"object_store_root_dir", objectStoreConfig.RootDir,
		"object_store_use_path_style", objectStoreConfig.UsePathStyle,
		"object_store_client", fmt.Sprintf("%T", client),
	)
	return client
}

func buildExecutionResolver(config serverconfig.Config, store managedagents.Store, objectStore objectstore.Client, logger *slog.Logger) (execution.ProviderResolver, func()) {
	runtime, ok := tools.NormalizeToolRuntime(config.ToolRuntime.Runtime)
	if !ok {
		logger.Warn("invalid default tool runtime, falling back to cloud_sandbox",
			"tool_runtime", config.ToolRuntime.Runtime,
		)
		runtime = tools.ToolRuntimeCloudSandbox
	}
	logger.Info("using default tool runtime",
		"tool_runtime", runtime,
		"cloud_sandbox_root", config.ToolRuntime.Root,
		"cloud_sandbox_image", config.ToolRuntime.Image,
		"cloud_sandbox_data_root", config.ToolRuntime.DataRoot,
		"cloud_sandbox_data_ttl_seconds", config.ToolRuntime.DataTTLSeconds,
		"cloud_sandbox_container_idle_ttl_seconds", config.ToolRuntime.ContainerIdleTTLSeconds,
		"cloud_sandbox_container_max_lifetime_seconds", config.ToolRuntime.ContainerMaxLifetimeSeconds,
		"cloud_sandbox_container_cleanup_interval_seconds", config.ToolRuntime.ContainerCleanupIntervalSeconds,
		"cloud_sandbox_allow_network", config.ToolRuntime.AllowNetwork,
		"allow_server_local_system", config.ToolRuntime.AllowLocalSystem,
	)
	containerManager := capability.NewOnlyboxesContainerManager(capability.OnlyboxesContainerManagerConfig{
		IdleTTL:         config.ToolRuntime.ContainerIdleTTL,
		MaxLifetime:     config.ToolRuntime.ContainerMaxLifetime,
		CleanupInterval: config.ToolRuntime.ContainerCleanupInterval,
		Logger:          logger,
	})
	return execution.SessionProviderResolver{
		Store:                      store,
		ObjectStore:                objectStore,
		DefaultRuntime:             runtime,
		CloudSandboxRoot:           config.ToolRuntime.Root,
		CloudSandboxImage:          config.ToolRuntime.Image,
		CloudSandboxDataRoot:       config.ToolRuntime.DataRoot,
		CloudSandboxDataTTL:        config.ToolRuntime.DataTTL,
		CloudSandboxDisableNetwork: !config.ToolRuntime.AllowNetwork,
		CloudSandboxContainers:     containerManager,
		AllowLocalSystem:           config.ToolRuntime.AllowLocalSystem,
	}, containerManager.Close
}

func buildRunner(config serverconfig.Config, store managedagents.Store, objectStore objectstore.Client, executionResolver execution.ProviderResolver, logger *slog.Logger) (runner.Runner, func(), error) {
	llmManager, err := llm.NewManagerWithConfig(llm.ManagerConfig{
		Provider:     config.LLM.Provider,
		ProviderType: config.LLM.ProviderType,
		Model:        config.LLM.Model,
		BaseURL:      config.LLM.BaseURL,
		APIKey:       config.LLM.APIKey,
	})
	if err != nil {
		return nil, nil, err
	}
	_, llmProvider, llmModel := llmManager.Current()

	turnExecutor := runner.AgentRuntimeTurnExecutor{
		Runtime: agentruntime.DemoRuntime{
			Client:        llmManager,
			MaxToolRounds: config.Turn.MaxToolRounds,
		},
		Store:            store,
		ObjectStore:      objectStore,
		ArtifactBucket:   config.ObjectStore.Bucket,
		Timeout:          config.Turn.Timeout,
		ProviderResolver: executionResolver,
	}
	worker := runner.NewWorkerRunnerWithConfig(store, turnExecutor, runner.WorkerRunnerConfig{
		WorkerCount:       config.Turn.WorkerCount,
		WakeBuffer:        config.Turn.QueueSize,
		PollInterval:      config.Turn.PollInterval,
		LeaseDuration:     config.Turn.LeaseDuration,
		HeartbeatInterval: config.Turn.HeartbeatInterval,
	}, logger)
	logger.Info("using worker runner",
		"turn_executor", "agent_runtime",
		"agent_runtime", "demo",
		"llm_provider", llmProvider,
		"llm_model", llmModel,
		"default_context_window_tokens", config.Context.DefaultWindowTokens,
		"worker_count", config.Turn.WorkerCount,
		"wake_buffer", config.Turn.QueueSize,
		"poll_interval", config.Turn.PollInterval.String(),
		"lease_duration", config.Turn.LeaseDuration.String(),
		"heartbeat_interval", config.Turn.HeartbeatInterval.String(),
	)
	return worker, worker.Close, nil
}

func shutdownSignal() <-chan os.Signal {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals
}
