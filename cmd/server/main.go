package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/httpapi"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/serverconfig"
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
	turnRunner, runnerCleanup, err := buildRunner(config, store, logger)
	if err != nil {
		logger.Error("build runner failed", "error", err)
		os.Exit(1)
	}
	defer runnerCleanup()

	server := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           httpapi.NewServerWithStoreRunnerAndLLMDefaults(store, turnRunner, logger, config.LLM.Provider, config.LLM.Model),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("tma server listening", "addr", config.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdownSignal()

	// 收到退出信号后给连接一点时间完成，避免直接中断 SSE / HTTP 请求。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
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

func buildRunner(config serverconfig.Config, store managedagents.Store, logger *slog.Logger) (runner.Runner, func(), error) {
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
		Store:   store,
		Timeout: config.Turn.Timeout,
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
