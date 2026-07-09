package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/httpapi"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/serverconfig"
	"tiggy-manage-agent/internal/tools"
)

func main() {
	// 统一使用 JSON 结构化日志，方便后续按 session_id / turn_id 检索。
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

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
	objectStore := buildObjectStore(config, logger)
	executionResolver := buildExecutionResolver(config, store, objectStore, logger)
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
	stopWorkerWorkReaper := startWorkerWorkReaper(config.Worker.WorkReaper, store, logger)

	go func() {
		logger.Info("tma server listening", "addr", config.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdownSignal()
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

func startWorkerWorkReaper(config serverconfig.WorkerWorkReaperConfig, store managedagents.Store, logger *slog.Logger) func() {
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

func reapExpiredWorkerWork(store managedagents.Store, logger *slog.Logger, limit int) {
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

func buildExecutionResolver(config serverconfig.Config, store managedagents.Store, objectStore objectstore.Client, logger *slog.Logger) execution.ProviderResolver {
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
		"cloud_sandbox_allow_network", config.ToolRuntime.AllowNetwork,
		"allow_server_local_system", config.ToolRuntime.AllowLocalSystem,
	)
	return execution.SessionProviderResolver{
		Store:                      store,
		ObjectStore:                objectStore,
		DefaultRuntime:             runtime,
		CloudSandboxRoot:           config.ToolRuntime.Root,
		CloudSandboxImage:          config.ToolRuntime.Image,
		CloudSandboxDataRoot:       config.ToolRuntime.DataRoot,
		CloudSandboxDataTTL:        config.ToolRuntime.DataTTL,
		CloudSandboxDisableNetwork: !config.ToolRuntime.AllowNetwork,
		AllowLocalSystem:           config.ToolRuntime.AllowLocalSystem,
	}
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
			Client: llmManager,
		},
		Store:            store,
		ObjectStore:      objectStore,
		ArtifactBucket:   config.ObjectStore.Bucket,
		Timeout:          config.Turn.Timeout,
		ProviderResolver: executionResolver,
	}
	worker := runner.NewWorkerRunner(store, turnExecutor, config.Turn.QueueSize, logger)
	logger.Info("using worker runner",
		"turn_executor", "agent_runtime",
		"agent_runtime", "demo",
		"llm_provider", llmProvider,
		"llm_model", llmModel,
		"default_context_window_tokens", config.Context.DefaultWindowTokens,
		"queue_size", config.Turn.QueueSize,
	)
	return worker, worker.Close, nil
}

func shutdownSignal() <-chan os.Signal {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals
}
