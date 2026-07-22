package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"tiggy-manage-agent/internal/agentschedule"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/httpapi"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/serverconfig"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillretention"
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
	if err := validateProductionDatabaseTenantIsolation(config.Environment, store); err != nil {
		logger.Error("validate production database tenant isolation failed", "error", err)
		os.Exit(1)
	}
	var authorizationSink observability.AuthorizationDecisionSink
	var securityAuditExporter *observability.SecurityAuditExporter
	var securityAuditPipeline *observability.DurableSecurityAuditPipeline
	if strings.TrimSpace(config.Observability.SecurityAudit.OTLPEndpoint) == "" {
		logger.Info("security audit OTLP exporter disabled")
	} else if config.Observability.SecurityAudit.Durable {
		outboxStore, ok := store.(managedagents.SecurityAuditOutboxStore)
		if !ok {
			logger.Error("durable security audit requires an outbox-capable store")
			os.Exit(1)
		}
		securityAuditPipeline, err = observability.NewDurableSecurityAuditPipeline(observability.DurableSecurityAuditConfig{
			Store: outboxStore, Endpoint: config.Observability.SecurityAudit.OTLPEndpoint,
			Token: config.Observability.SecurityAudit.OTLPToken, IntegrityKey: config.Observability.SecurityAudit.IntegrityKey,
			IntegrityKeyID: config.Observability.SecurityAudit.IntegrityKeyID,
			IntegrityKeys:  config.Observability.SecurityAudit.IntegrityKeys,
			ServiceName:    "tiggy-manage-agent", BatchSize: config.Observability.SecurityAudit.BatchSize,
			PollInterval:      config.Observability.SecurityAudit.WorkerInterval,
			LeaseDuration:     config.Observability.SecurityAudit.LeaseDuration,
			MaxAttempts:       config.Observability.SecurityAudit.MaxAttempts,
			RetryInitialDelay: config.Observability.SecurityAudit.RetryInitialDelay,
			RetryMaxDelay:     config.Observability.SecurityAudit.RetryMaxDelay,
			Retention:         config.Observability.SecurityAudit.Retention,
			PruneInterval:     config.Observability.SecurityAudit.PruneInterval,
			PruneLimit:        config.Observability.SecurityAudit.PruneLimit,
			HTTPClient:        &http.Client{Timeout: config.Observability.SecurityAudit.HTTPTimeout}, Logger: logger,
		})
		if err != nil {
			logger.Error("build durable security audit pipeline failed", "error", err)
			os.Exit(1)
		}
		authorizationSink = securityAuditPipeline
		logger.Info("durable security audit OTLP pipeline enabled",
			"endpoint", config.Observability.SecurityAudit.OTLPEndpoint,
			"batch_size", config.Observability.SecurityAudit.BatchSize,
			"retention", config.Observability.SecurityAudit.Retention,
		)
	} else {
		securityAuditExporter, err = observability.NewSecurityAuditExporter(observability.SecurityAuditExporterConfig{
			Endpoint: config.Observability.SecurityAudit.OTLPEndpoint,
			Token:    config.Observability.SecurityAudit.OTLPToken, ServiceName: "tiggy-manage-agent",
			QueueSize:     config.Observability.SecurityAudit.QueueSize,
			BatchSize:     config.Observability.SecurityAudit.BatchSize,
			FlushInterval: config.Observability.SecurityAudit.FlushInterval,
			HTTPClient:    &http.Client{Timeout: config.Observability.SecurityAudit.HTTPTimeout}, Logger: logger,
		})
		if err != nil {
			logger.Error("build security audit OTLP exporter failed", "error", err)
			os.Exit(1)
		}
		authorizationSink = securityAuditExporter
		logger.Info("in-memory security audit OTLP exporter enabled",
			"endpoint", config.Observability.SecurityAudit.OTLPEndpoint,
			"queue_size", config.Observability.SecurityAudit.QueueSize,
			"batch_size", config.Observability.SecurityAudit.BatchSize,
		)
	}
	if err := ensureDefaultLLMProvider(store, config, logger); err != nil {
		logger.Error("ensure default llm provider failed", "error", err)
		os.Exit(1)
	}
	if err := ensureBuiltinGeneralAgent(store, config, logger); err != nil {
		logger.Error("ensure builtin general agent failed", "error", err)
		os.Exit(1)
	}
	objectStore := buildObjectStore(config, logger)
	if postgresStore, ok := store.(*managedagents.PostgresStore); ok {
		if err := postgresStore.ConfigureSkillPackageStorage(objectStore, config.ObjectStore.Bucket); err != nil {
			logger.Error("configure skills package storage failed", "error", err)
			os.Exit(1)
		}
		logger.Info("using standard SKILL.md package storage",
			"object_store_provider", config.ObjectStore.Provider,
			"object_store_bucket", config.ObjectStore.Bucket,
		)
	}
	binaryScanner, err := skillmarketplace.NewBinaryScanner(skillmarketplace.BinaryScannerConfig{
		Provider: config.Skills.BinaryScanner.Provider, Endpoint: config.Skills.BinaryScanner.Endpoint,
		Token: config.Skills.BinaryScanner.Token, Timeout: config.Skills.BinaryScanner.Timeout,
		MaxAttempts: config.Skills.BinaryScanner.MaxAttempts, PollInterval: config.Skills.BinaryScanner.PollInterval,
	})
	if err != nil {
		logger.Error("build skills binary scanner failed", "provider", config.Skills.BinaryScanner.Provider, "error", err)
		os.Exit(1)
	}
	if binaryScanner == nil {
		logger.Info("using built-in skills binary scanner")
	} else {
		logger.Info("using external skills binary scanner", "provider", binaryScanner.Provider(), "endpoint", config.Skills.BinaryScanner.Endpoint)
	}
	executionResolver, executionCleanup := buildExecutionResolver(config, store, objectStore, logger)
	defer executionCleanup()
	turnRunner, runnerCleanup, err := buildRunner(config, store, objectStore, executionResolver, logger)
	if err != nil {
		logger.Error("build runner failed", "error", err)
		os.Exit(1)
	}
	defer runnerCleanup()

	serverContext, cancelServerContext := context.WithCancel(context.Background())
	defer cancelServerContext()
	server := &http.Server{
		Addr: config.HTTPAddr,
		Handler: httpapi.NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
			store,
			turnRunner,
			logger,
			config.LLM.Provider,
			config.LLM.Model,
			objectStore,
			executionResolver,
			config.Worker.AuthToken,
			config.Worker.ControlAuthToken,
			httpapi.AuthConfig{
				Mode: config.Auth.Mode, JWTSecret: config.Auth.JWTSecret, JWTIssuer: config.Auth.JWTIssuer,
				JWTAudience: config.Auth.JWTAudience, GatewayToken: config.Auth.GatewayToken,
				OIDCIssuer: config.Auth.OIDCIssuer, OIDCAudience: config.Auth.OIDCAudience,
				OIDCJWKSURL: config.Auth.OIDCJWKSURL, OIDCSigningAlgs: config.Auth.OIDCSigningAlgs,
				OIDCHTTPTimeout:      time.Duration(config.Auth.OIDCHTTPTimeoutSecs) * time.Second,
				OIDCRefreshInterval:  time.Duration(config.Auth.OIDCRefreshIntervalSecs) * time.Second,
				OIDCMaxStale:         time.Duration(config.Auth.OIDCMaxStaleSecs) * time.Second,
				OIDCClaimMapping:     config.Auth.OIDCClaimMapping,
				OIDCWebLoginEnabled:  config.Auth.OIDCWebLoginEnabled,
				OIDCWebClientID:      config.Auth.OIDCWebClientID,
				OIDCWebClientSecret:  config.Auth.OIDCWebClientSecret,
				OIDCWebRedirectURL:   config.Auth.OIDCWebRedirectURL,
				OIDCWebPostLogoutURL: config.Auth.OIDCWebPostLogoutURL,
				OIDCWebSessionSecret: config.Auth.OIDCWebSessionSecret,
				OIDCCLIClientID:      config.Auth.OIDCCLIClientID,
				CookieTrustedOrigins: config.Auth.CookieTrustedOrigins,
				GatewayTrustedCIDRs:  config.Auth.GatewayTrustedCIDRs,
				WorkerWorkspaceID:    config.Worker.AuthWorkspaceID,
				AuthorizationSink:    authorizationSink,
			},
			httpapi.SubagentPolicy{
				MaxDepth:              config.Subagent.MaxDepth,
				MaxChildrenPerTurn:    config.Subagent.MaxChildrenPerTurn,
				MaxChildrenPerSession: config.Subagent.MaxChildrenPerSession,
				WorkspaceActiveLimit:  config.Subagent.WorkspaceActiveLimit,
				UserActiveLimit:       config.Subagent.UserActiveLimit,
				WorkspaceQueuedLimit:  config.Subagent.WorkspaceQueuedLimit,
				UserQueuedLimit:       config.Subagent.UserQueuedLimit,
				QueueTimeoutSeconds:   config.Subagent.QueueTimeoutSeconds,
			},
			binaryScanner,
		),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return serverContext
		},
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
	stopAgentScheduler := func() {}
	if scheduleStore, ok := store.(managedagents.AgentScheduleStore); ok {
		stopAgentScheduler = agentschedule.Start(serverContext, agentschedule.Service{
			Store: scheduleStore, State: store, Runner: turnRunner, Logger: logger, Limit: 20,
		}, 15*time.Second)
		logger.Info("agent scheduler enabled", "interval", 15*time.Second, "batch_size", 20)
	}
	stopObservabilityRetry := startObservabilityExporterRetry(config.Observability.ExporterRetry, store, logger)
	stopSecurityAuditWorker := startSecurityAuditWorker(securityAuditPipeline)
	stopSkillAssetGC := func() {}
	if retentionStore, ok := store.(skillretention.Store); ok {
		stopSkillAssetGC = startSkillAssetGC(config.Skills.AssetRetention, retentionStore, objectStore, logger)
	} else {
		logger.Warn("skill asset GC worker disabled: store does not support retention")
	}
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
	// Shutdown waits for active SSE requests, so cancel their shared base context first.
	cancelServerContext()
	stopAgentScheduler()
	stopTraceIndexRetention()
	stopSkillAssetGC()
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
	stopSecurityAuditWorker()
	if securityAuditExporter != nil {
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := securityAuditExporter.Close(flushCtx); err != nil {
			logger.Warn("security audit OTLP exporter flush failed", "error", err)
		}
		flushCancel()
	}

	logger.Info("server stopped")
}

func startSecurityAuditWorker(pipeline *observability.DurableSecurityAuditPipeline) func() {
	if pipeline == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pipeline.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func startSkillAssetGC(config serverconfig.SkillsAssetRetentionConfig, store skillretention.Store, objectStore objectstore.Client, logger *slog.Logger) func() {
	if !config.WorkerEnabled {
		logger.Info("skill asset GC worker disabled")
		return func() {}
	}
	service, err := skillretention.NewService(store, objectStore, skillretention.Policy{
		Enabled: config.Enabled, RetentionDays: config.RetentionDays, DeleteLimit: config.DeleteLimit,
	})
	if err != nil {
		logger.Error("skill asset GC worker configuration invalid", "error", err)
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	runAll := func() {
		workspaceIDs, err := store.ListSkillAssetGCWorkspaceIDs(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("skill asset GC workspace listing failed", "error", err)
			}
			return
		}
		for _, workspaceID := range workspaceIDs {
			if ctx.Err() != nil {
				return
			}
			effective, err := service.EffectivePolicy(ctx, workspaceID)
			if err != nil {
				logger.Warn("skill asset GC policy resolution failed", "workspace_id", workspaceID, "error", err)
				continue
			}
			if !effective.Config.Enabled {
				continue
			}
			result, err := service.Run(ctx, skillretention.RunRequest{WorkspaceID: workspaceID, RequestedBy: "system:skill-asset-gc"})
			if err != nil {
				if !errors.Is(err, skillretention.ErrConflict) && ctx.Err() == nil {
					logger.Warn("skill asset GC run failed", "workspace_id", workspaceID, "error", err)
				}
				continue
			}
			logger.Info("skill asset GC run completed", "workspace_id", workspaceID, "run_id", result.Run.ID,
				"status", result.Run.Status, "deleted_count", result.Run.DeletedCount, "failed_count", result.Run.FailedCount,
				"bytes_deleted", result.Run.BytesDeleted)
		}
	}
	go func() {
		runAll()
		ticker := time.NewTicker(config.WorkerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runAll()
			}
		}
	}()
	logger.Info("skill asset GC worker enabled", "interval", config.WorkerInterval)
	return cancel
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

func validateProductionDatabaseTenantIsolation(environment string, store any) error {
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment != "production" && environment != "prod" {
		return nil
	}
	validator, ok := store.(managedagents.DatabaseTenantIsolationValidator)
	if !ok {
		return errors.New("production database store does not support tenant isolation validation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return validator.ValidateDatabaseTenantIsolation(ctx)
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
	model, err := ensureDefaultLLMModel(store, managedagents.UpsertLLMModelInput{
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

type defaultLLMModelStore interface {
	ListLLMModels(providerID string) ([]managedagents.LLMModel, error)
	UpsertLLMModel(input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error)
}

func ensureDefaultLLMModel(store defaultLLMModelStore, input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error) {
	models, err := store.ListLLMModels(input.ProviderID)
	if err != nil {
		return managedagents.LLMModel{}, err
	}
	for _, model := range models {
		if model.Model == input.Model {
			return model, nil
		}
	}
	return store.UpsertLLMModel(input)
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
		"read_file_default_max_bytes", config.ToolRuntime.ReadFileLimits.DefaultMaxBytes,
		"read_file_hard_max_bytes", config.ToolRuntime.ReadFileLimits.HardMaxBytes,
		"read_file_small_file_bytes", config.ToolRuntime.ReadFileLimits.SmallFileBytes,
		"read_file_max_lines", config.ToolRuntime.ReadFileLimits.MaxLines,
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
		ReadFileLimits:             config.ToolRuntime.ReadFileLimits,
	}, containerManager.Close
}

func buildMCPHTTPBaseClient(caBundlePath string) (*http.Client, error) {
	caBundlePath = strings.TrimSpace(caBundlePath)
	if caBundlePath == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(caBundlePath)
	if err != nil {
		return nil, fmt.Errorf("read MCP HTTP CA bundle: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("MCP HTTP CA bundle contains no valid PEM certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Transport: transport}, nil
}

func buildRunner(config serverconfig.Config, store managedagents.Store, objectStore objectstore.Client, executionResolver execution.ProviderResolver, logger *slog.Logger) (runner.Runner, func(), error) {
	taskPlanReader, ok := store.(managedagents.SessionTaskPlanReader)
	if !ok {
		return nil, nil, errors.New("agent runtime requires a session task plan reader for completion validation")
	}
	llmManager, err := llm.NewManagerWithConfig(llm.ManagerConfig{
		Provider:       config.LLM.Provider,
		ProviderType:   config.LLM.ProviderType,
		Model:          config.LLM.Model,
		BaseURL:        config.LLM.BaseURL,
		APIKey:         config.LLM.APIKey,
		MaxAttempts:    config.LLM.MaxAttempts,
		RetryBaseDelay: config.LLM.RetryBaseDelay,
	})
	if err != nil {
		return nil, nil, err
	}
	_, llmProvider, llmModel := llmManager.Current()
	mcpHTTPBaseClient, err := buildMCPHTTPBaseClient(config.MCP.StreamableHTTPHost.CABundlePath)
	if err != nil {
		return nil, nil, err
	}
	mcpHTTPEgressPolicy, err := mcp.NewEgressPolicy(mcp.EgressPolicyConfig{
		AllowHTTP:            config.MCP.StreamableHTTPHost.EgressAllowHTTP,
		AllowPrivateNetworks: config.MCP.StreamableHTTPHost.EgressAllowPrivateNetworks,
		AllowedHosts:         config.MCP.StreamableHTTPHost.EgressAllowedHosts,
		AllowedCIDRs:         config.MCP.StreamableHTTPHost.EgressAllowedCIDRs,
		BaseHTTPClient:       mcpHTTPBaseClient,
		OnBlock: func(event mcp.EgressBlockEvent) {
			logger.Warn("mcp_http_egress_blocked", "reason", event.Reason)
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure MCP HTTP egress policy: %w", err)
	}
	mcpRuntimeGuard := mcp.NewRuntimeGuard(mcp.RuntimeGuardOptions{})
	mcpHost := mcp.NewStdioHost(mcp.StdioHostOptions{
		IdleTimeout:   config.MCP.StdioHost.IdleTimeout,
		SweepInterval: config.MCP.StdioHost.SweepInterval,
		MaxSessions:   config.MCP.StdioHost.MaxSessions,
		Logger:        logger,
	})
	mcpHTTPHost := mcp.NewStreamableHTTPHost(mcp.StreamableHTTPHostOptions{
		IdleTimeout:   config.MCP.StreamableHTTPHost.IdleTimeout,
		SweepInterval: config.MCP.StreamableHTTPHost.SweepInterval,
		MaxSessions:   config.MCP.StreamableHTTPHost.MaxSessions,
		EgressPolicy:  mcpHTTPEgressPolicy,
		Logger:        logger,
	})

	liveEvents := runner.NewLiveEventBroker(256)
	completionGate := agentruntime.CompletionGateChain{Gates: []agentruntime.CompletionGate{
		agentruntime.TaskPlanCompletionGate{Reader: taskPlanReader},
		agentruntime.ArtifactCompletionGate{Reader: store},
	}}
	turnExecutor := runner.AgentRuntimeTurnExecutor{
		CoreClient:         llmManager,
		CoreCompletionGate: completionGate,
		CoreMaxRounds:      config.Turn.MaxToolRounds,
		Store:              store,
		ObjectStore:        objectStore,
		ArtifactBucket:     config.ObjectStore.Bucket,
		Timeout:            config.Turn.Timeout,
		ProviderResolver:   executionResolver,
		MCPHost:            mcpHost,
		MCPHTTPHost:        mcpHTTPHost,
		MCPRuntimeGuard:    mcpRuntimeGuard,
		LiveEvents:         liveEvents,
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
		"agent_runtime", "agent_core",
		"llm_provider", llmProvider,
		"llm_model", llmModel,
		"default_context_window_tokens", config.Context.DefaultWindowTokens,
		"worker_count", config.Turn.WorkerCount,
		"wake_buffer", config.Turn.QueueSize,
		"poll_interval", config.Turn.PollInterval.String(),
		"lease_duration", config.Turn.LeaseDuration.String(),
		"heartbeat_interval", config.Turn.HeartbeatInterval.String(),
	)
	logger.Info("using server MCP stdio host",
		"scope", "session_agent_config",
		"idle_timeout", config.MCP.StdioHost.IdleTimeout.String(),
		"sweep_interval", config.MCP.StdioHost.SweepInterval.String(),
		"max_sessions", config.MCP.StdioHost.MaxSessions,
	)
	logger.Info("using server MCP streamable_http host",
		"scope", "session_agent_config",
		"idle_timeout", config.MCP.StreamableHTTPHost.IdleTimeout.String(),
		"sweep_interval", config.MCP.StreamableHTTPHost.SweepInterval.String(),
		"max_sessions", config.MCP.StreamableHTTPHost.MaxSessions,
		"egress_allow_http", config.MCP.StreamableHTTPHost.EgressAllowHTTP,
		"egress_allow_private_networks", config.MCP.StreamableHTTPHost.EgressAllowPrivateNetworks,
		"egress_allowed_host_count", len(config.MCP.StreamableHTTPHost.EgressAllowedHosts),
		"egress_allowed_cidr_count", len(config.MCP.StreamableHTTPHost.EgressAllowedCIDRs),
	)
	logger.Info("using server MCP runtime guard",
		"default_timeout", mcp.DefaultRuntimeTimeout.String(),
		"default_max_concurrency", mcp.DefaultRuntimeMaxConcurrency,
		"default_failure_threshold", mcp.DefaultRuntimeFailureThreshold,
		"default_cooldown", mcp.DefaultRuntimeCooldown.String(),
	)
	return worker, func() {
		worker.Close()
		mcpHost.Close()
		mcpHTTPHost.Close()
	}, nil
}

func shutdownSignal() <-chan os.Signal {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals
}
