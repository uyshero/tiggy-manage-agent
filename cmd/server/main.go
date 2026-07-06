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
	turnRunner, runnerCleanup := buildRunner(config.Turn, store, logger)
	defer runnerCleanup()

	server := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           httpapi.NewServerWithStoreAndRunner(store, turnRunner, logger),
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

func buildRunner(config serverconfig.TurnConfig, store managedagents.Store, logger *slog.Logger) (runner.Runner, func()) {
	turnExecutor := runner.AgentRuntimeTurnExecutor{
		Runtime: agentruntime.DemoRuntime{},
		Store:   store,
		Timeout: config.Timeout,
	}
	worker := runner.NewWorkerRunner(store, turnExecutor, config.QueueSize, logger)
	logger.Info("using worker runner",
		"turn_executor", "agent_runtime",
		"agent_runtime", "demo",
		"queue_size", config.QueueSize,
	)
	return worker, worker.Close
}

func shutdownSignal() <-chan os.Signal {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals
}
