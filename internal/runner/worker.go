package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/observability"
)

var (
	ErrRunnerQueueFull = errors.New("runner queue full")
	ErrRunnerStopped   = errors.New("runner stopped")
)

const (
	DefaultTurnPollInterval      = 500 * time.Millisecond
	DefaultTurnLeaseDuration     = 10 * time.Second
	DefaultTurnHeartbeatInterval = time.Second
)

type TurnExecutor interface {
	RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error)
}

type TurnResult struct {
	AgentPayload json.RawMessage
	Usage        *managedagents.RecordLLMUsageInput
}

type WorkerRunnerConfig struct {
	WorkerCount       int
	WakeBuffer        int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	OrphanReapLimit   int
	InstanceID        string
	PostProcess       func(sessionID string, turnID string)
}

type WorkerRunner struct {
	store        managedagents.Store
	queueStore   managedagents.SessionTurnQueueStore
	startQueue   managedagents.SubagentStartQueueStore
	orphanStore  managedagents.OrphanSubagentStore
	turnExecutor TurnExecutor
	logger       *slog.Logger
	config       WorkerRunnerConfig

	queue   chan workerJob
	wake    chan struct{}
	stopped chan struct{}

	mu        sync.Mutex
	turns     map[turnKey]context.CancelFunc
	closeOnce sync.Once
	workerWG  sync.WaitGroup
	postWG    sync.WaitGroup
}

type workerJob struct {
	ctx     context.Context
	request TurnRequest
	leased  bool
}

func NewWorkerRunner(store managedagents.Store, turnExecutor TurnExecutor, queueSize int, logger *slog.Logger) *WorkerRunner {
	return NewWorkerRunnerWithConfig(store, turnExecutor, WorkerRunnerConfig{
		WorkerCount: queueSize,
		WakeBuffer:  queueSize,
	}, logger)
}

func NewWorkerRunnerWithConfig(store managedagents.Store, turnExecutor TurnExecutor, config WorkerRunnerConfig, logger *slog.Logger) *WorkerRunner {
	if logger == nil {
		logger = slog.Default()
	}
	if config.WorkerCount <= 0 {
		config.WorkerCount = 4
	}
	if config.WakeBuffer <= 0 {
		config.WakeBuffer = config.WorkerCount
	}
	if config.PollInterval <= 0 {
		config.PollInterval = DefaultTurnPollInterval
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = DefaultTurnLeaseDuration
	}
	if config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration {
		config.HeartbeatInterval = config.LeaseDuration / 3
		if config.HeartbeatInterval <= 0 {
			config.HeartbeatInterval = time.Millisecond
		}
	}
	if config.OrphanReapLimit <= 0 {
		config.OrphanReapLimit = config.WorkerCount
	}
	if config.InstanceID == "" {
		config.InstanceID = newRunnerInstanceID()
	}
	runner := &WorkerRunner{
		store:        store,
		turnExecutor: turnExecutor,
		logger:       logger,
		config:       config,
		queue:        make(chan workerJob, config.WakeBuffer),
		wake:         make(chan struct{}, config.WakeBuffer),
		stopped:      make(chan struct{}),
		turns:        make(map[turnKey]context.CancelFunc),
	}
	runner.queueStore, _ = store.(managedagents.SessionTurnQueueStore)
	runner.startQueue, _ = store.(managedagents.SubagentStartQueueStore)
	runner.orphanStore, _ = store.(managedagents.OrphanSubagentStore)
	for range config.WorkerCount {
		runner.workerWG.Add(1)
		if runner.queueStore != nil {
			go runner.workPersistent()
		} else {
			go runner.workMemory()
		}
	}
	logger.Info("worker runner started",
		"instance_id", config.InstanceID,
		"workers", config.WorkerCount,
		"persistent_queue", runner.queueStore != nil,
		"orphan_reap_limit", config.OrphanReapLimit,
		"lease_duration", config.LeaseDuration.String(),
		"heartbeat_interval", config.HeartbeatInterval.String(),
	)
	return runner
}

func (r *WorkerRunner) StartTurn(_ context.Context, request TurnRequest) error {
	if r.turnExecutor == nil {
		return errors.New("runner turn executor is nil")
	}
	select {
	case <-r.stopped:
		return ErrRunnerStopped
	default:
	}

	if r.queueStore != nil {
		r.signalWake()
		return nil
	}

	key := turnKey{sessionID: request.SessionID, turnID: request.TurnID}
	ctx, cancel := context.WithCancel(context.Background())
	if !r.registerTurn(key, cancel) {
		cancel()
		return ErrTurnAlreadyRunning
	}

	job := workerJob{ctx: ctx, request: request}
	select {
	case r.queue <- job:
		r.logger.Info("worker runner turn queued", "session_id", request.SessionID, "turn_id", request.TurnID)
		return nil
	case <-r.stopped:
		r.takeTurn(key)
		cancel()
		return ErrRunnerStopped
	default:
		r.takeTurn(key)
		cancel()
		return ErrRunnerQueueFull
	}
}

func (r *WorkerRunner) InterruptTurn(_ context.Context, request InterruptRequest) error {
	key := turnKey{sessionID: request.SessionID, turnID: request.TurnID}
	cancel := r.takeTurn(key)
	if cancel == nil {
		r.logger.Info("worker runner interrupt delegated to persistent state",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
		)
		r.signalWake()
		return nil
	}
	cancel()
	r.logger.Info("worker runner turn canceled", "session_id", request.SessionID, "turn_id", request.TurnID)
	return nil
}

func (r *WorkerRunner) PostProcessTurn(sessionID string, turnID string) {
	r.schedulePostProcess(TurnRequest{SessionID: sessionID, TurnID: turnID})
}

func (r *WorkerRunner) SubscribeLiveEvents(sessionID string) (<-chan LiveEvent, func(), error) {
	source, ok := r.turnExecutor.(LiveEventSource)
	if !ok {
		return nil, nil, ErrLiveEventsUnavailable
	}
	return source.SubscribeLiveEvents(sessionID)
}

func (r *WorkerRunner) schedulePostProcess(request TurnRequest) {
	select {
	case <-r.stopped:
		return
	default:
	}
	r.postWG.Add(1)
	go func() {
		defer r.postWG.Done()
		if r.config.PostProcess != nil {
			r.config.PostProcess(request.SessionID, request.TurnID)
			return
		}
		r.postProcessTurn(request)
	}()
}

func (r *WorkerRunner) Close() {
	r.closeOnce.Do(func() {
		close(r.stopped)
		r.mu.Lock()
		cancels := make([]context.CancelFunc, 0, len(r.turns))
		for key, cancel := range r.turns {
			cancels = append(cancels, cancel)
			delete(r.turns, key)
		}
		r.mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		r.workerWG.Wait()
		r.postWG.Wait()
	})
}

func (r *WorkerRunner) MCPHostStats() mcp.StdioHostStats {
	provider, ok := r.turnExecutor.(MCPHostStatsProvider)
	if !ok {
		return mcp.StdioHostStats{}
	}
	return provider.MCPHostStats()
}

func (r *WorkerRunner) MCPHTTPHostStats() mcp.StreamableHTTPHostStats {
	provider, ok := r.turnExecutor.(MCPHTTPHostStatsProvider)
	if !ok {
		return mcp.StreamableHTTPHostStats{}
	}
	return provider.MCPHTTPHostStats()
}

func (r *WorkerRunner) MCPRuntimeGuardStats() mcp.RuntimeGuardStats {
	provider, ok := r.turnExecutor.(MCPRuntimeGuardStatsProvider)
	if !ok {
		return mcp.RuntimeGuardStats{}
	}
	return provider.MCPRuntimeGuardStats()
}

func (r *WorkerRunner) MCPRegistryRuntimeStates(workspaceID string) []mcp.RegistryRuntimeState {
	provider, ok := r.turnExecutor.(MCPRegistryRuntimeStatesProvider)
	if !ok {
		return nil
	}
	return provider.MCPRegistryRuntimeStates(workspaceID)
}

func (r *WorkerRunner) MCPHTTPEgressPolicy() *mcp.EgressPolicy {
	provider, ok := r.turnExecutor.(MCPHTTPEgressPolicyProvider)
	if !ok {
		return nil
	}
	return provider.MCPHTTPEgressPolicy()
}

func (r *WorkerRunner) workMemory() {
	defer r.workerWG.Done()
	for {
		select {
		case <-r.stopped:
			return
		case job := <-r.queue:
			r.runJob(job)
		}
	}
}

func (r *WorkerRunner) workPersistent() {
	defer r.workerWG.Done()
	for {
		select {
		case <-r.stopped:
			return
		default:
		}

		if r.orphanStore != nil {
			reaped, err := r.orphanStore.ReapOrphanSubagents(managedagents.ReapOrphanSubagentsInput{Limit: r.config.OrphanReapLimit})
			if err != nil {
				r.logger.Warn("subagent orphan reap failed", "instance_id", r.config.InstanceID, "error", err)
			} else if len(reaped) > 0 {
				r.logger.Info("subagent orphans reaped", "count", len(reaped), "instance_id", r.config.InstanceID)
			}
		}

		if r.startQueue != nil {
			promotions, err := r.startQueue.PromoteSubagentStarts(managedagents.PromoteSubagentStartsInput{Limit: 1})
			if err != nil {
				r.logger.Warn("subagent start queue promotion failed", "instance_id", r.config.InstanceID, "error", err)
			} else if len(promotions) > 0 {
				r.logger.Info("subagent start promoted", "request_id", promotions[0].Request.ID, "session_id", promotions[0].Request.SessionID, "turn_id", promotions[0].Request.TurnID)
			}
		}
		work, err := r.queueStore.ClaimSessionTurns(managedagents.ClaimSessionTurnsInput{
			LeaseOwner:    r.config.InstanceID,
			LeaseDuration: r.config.LeaseDuration,
			Limit:         1,
		})
		if err != nil {
			r.logger.Warn("worker runner claim failed", "instance_id", r.config.InstanceID, "error", err)
			r.waitForWork()
			continue
		}
		if len(work) == 0 {
			r.waitForWork()
			continue
		}
		item := work[0]
		key := turnKey{sessionID: item.SessionID, turnID: item.TurnID}
		ctx, cancel := context.WithCancel(context.Background())
		if !r.registerTurn(key, cancel) {
			cancel()
			_ = r.releaseLease(item.SessionID, item.TurnID, item.Scope)
			continue
		}
		r.logger.Info("worker runner turn claimed",
			"instance_id", r.config.InstanceID,
			"session_id", item.SessionID,
			"turn_id", item.TurnID,
			"attempt", item.Attempt,
		)
		r.runJob(workerJob{
			ctx: ctx,
			request: TurnRequest{
				SessionID:          item.SessionID,
				TurnID:             item.TurnID,
				Scope:              item.Scope,
				UserEventSeq:       item.UserEventSeq,
				UserPayload:        item.UserPayload,
				ResumeIntervention: item.ResumeIntervention,
			},
			leased: true,
		})
	}
}

func (r *WorkerRunner) waitForWork() {
	timer := time.NewTimer(r.config.PollInterval)
	defer timer.Stop()
	select {
	case <-r.stopped:
	case <-r.wake:
	case <-timer.C:
	}
}

func (r *WorkerRunner) signalWake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *WorkerRunner) runJob(job workerJob) {
	key := turnKey{sessionID: job.request.SessionID, turnID: job.request.TurnID}
	defer r.deleteTurn(key)

	stopHeartbeat := func() {}
	if job.leased {
		stopHeartbeat = r.startLeaseHeartbeat(job.ctx, job.request, key)
	}
	result, err := r.turnExecutor.RunTurn(job.ctx, job.request)
	stopHeartbeat()
	if err != nil {
		if errors.Is(err, ErrTurnWaitingApproval) || errors.Is(err, ErrTurnWaitingHuman) {
			r.logger.Info("worker runner turn waiting for human intervention", "session_id", job.request.SessionID, "turn_id", job.request.TurnID)
			return
		}
		if job.ctx.Err() != nil {
			if job.leased {
				_ = r.releaseLease(job.request.SessionID, job.request.TurnID, job.request.Scope)
			}
			r.logger.Info("worker runner turn canceled before completion", "session_id", job.request.SessionID, "turn_id", job.request.TurnID)
			return
		}
		r.recordFailedUsage(job.request, result.Usage, err)
		r.failTurn(job.request, err)
		return
	}
	if job.ctx.Err() != nil {
		if job.leased {
			_ = r.releaseLease(job.request.SessionID, job.request.TurnID, job.request.Scope)
		}
		return
	}

	databaseCtx, err := databaseContextForTurn(job.ctx, job.request)
	if err != nil {
		r.logger.Error("worker runner completion scope failed", "session_id", job.request.SessionID, "turn_id", job.request.TurnID, "error", err)
		return
	}
	events, err := managedagents.CompleteSessionTurnWithContext(databaseCtx, r.store, job.request.SessionID, job.request.TurnID, result.AgentPayload)
	if err != nil {
		r.logger.Error("worker runner completion failed", "session_id", job.request.SessionID, "turn_id", job.request.TurnID, "error", err)
		if job.leased {
			_ = r.releaseLease(job.request.SessionID, job.request.TurnID, job.request.Scope)
		}
		return
	}
	if result.Usage != nil {
		usage := *result.Usage
		usage.SessionID = job.request.SessionID
		usage.TurnID = job.request.TurnID
		if _, err := managedagents.RecordLLMUsageWithContext(databaseCtx, r.store, usage); err != nil {
			r.logger.Error("worker runner llm usage record failed", "session_id", job.request.SessionID, "turn_id", job.request.TurnID, "error", err)
		}
	}
	r.logger.Info("worker runner turn completed", "session_id", job.request.SessionID, "turn_id", job.request.TurnID, "events", len(events))
	r.schedulePostProcess(job.request)
}

func (r *WorkerRunner) startLeaseHeartbeat(ctx context.Context, request TurnRequest, key turnKey) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(r.config.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				active, err := r.queueStore.RenewSessionTurnLease(managedagents.RenewSessionTurnLeaseInput{
					SessionID:     request.SessionID,
					TurnID:        request.TurnID,
					Scope:         request.Scope,
					LeaseOwner:    r.config.InstanceID,
					LeaseDuration: r.config.LeaseDuration,
				})
				if err != nil {
					r.logger.Warn("worker runner lease heartbeat failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err)
				}
				if err != nil || !active {
					if cancel := r.takeTurn(key); cancel != nil {
						cancel()
					}
					return
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func (r *WorkerRunner) releaseLease(sessionID string, turnID string, scope managedagents.AccessScope) error {
	if r.queueStore == nil {
		return nil
	}
	return r.queueStore.ReleaseSessionTurnLease(managedagents.ReleaseSessionTurnLeaseInput{
		SessionID:  sessionID,
		TurnID:     turnID,
		Scope:      scope,
		LeaseOwner: r.config.InstanceID,
	})
}

func (r *WorkerRunner) postProcessTurn(request TurnRequest) {
	databaseCtx, err := databaseContextForTurn(context.Background(), request)
	if err != nil {
		r.logger.Warn("worker runner post-process scope failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err)
		return
	}
	store := scopedPostProcessStore{Store: r.store, ctx: databaseCtx}
	if err := observability.RefreshSessionSummary(store, request.SessionID, request.TurnID); err != nil {
		r.logger.Warn("worker runner summary refresh failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err)
	}
	if result, err := observability.ExportTurnTraceFromEnv(store, request.SessionID, request.TurnID); err != nil {
		r.logger.Warn("worker runner observability export failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err)
	} else if !result.Skipped {
		r.logger.Info("worker runner observability export completed",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"trace_id", result.TraceID,
			"perfetto_path", resultPerfettoPath(result),
			"otlp_endpoint", resultOTLPEndpoint(result),
		)
	}
}

type scopedPostProcessStore struct {
	managedagents.Store
	ctx context.Context
}

func (s scopedPostProcessStore) ListEvents(sessionID string, afterSeq int64) ([]managedagents.Event, error) {
	return managedagents.ListEventsWithContext(s.ctx, s.Store, sessionID, afterSeq)
}

func (s scopedPostProcessStore) GetSessionSummary(sessionID string) (managedagents.SessionSummary, error) {
	return managedagents.GetSessionSummaryWithContext(s.ctx, s.Store, sessionID)
}

func (s scopedPostProcessStore) SaveSessionSummary(sessionID string, input managedagents.UpsertSessionSummaryInput) (managedagents.SessionSummary, error) {
	return managedagents.SaveSessionSummaryWithContext(s.ctx, s.Store, sessionID, input)
}

func (s scopedPostProcessStore) RecordObservabilityExporterRun(input managedagents.RecordObservabilityExporterRunInput) (managedagents.ObservabilityExporterRun, error) {
	return managedagents.RecordObservabilityExporterRunWithContext(s.ctx, s.Store, input)
}

func resultPerfettoPath(result observability.ExporterResult) string {
	if result.Perfetto == nil {
		return ""
	}
	return result.Perfetto.Path
}

func resultOTLPEndpoint(result observability.ExporterResult) string {
	if result.OTLPPush == nil {
		return ""
	}
	return result.OTLPPush.Endpoint
}

func (r *WorkerRunner) recordFailedUsage(request TurnRequest, usage *managedagents.RecordLLMUsageInput, turnErr error) {
	if usage == nil {
		return
	}
	failedUsage := *usage
	failedUsage.SessionID = request.SessionID
	failedUsage.TurnID = request.TurnID
	failedUsage.Status = "failed"
	if turnErr != nil && failedUsage.ErrorMessage == "" {
		failedUsage.ErrorMessage = turnErr.Error()
	}
	databaseCtx, scopeErr := databaseContextForTurn(context.Background(), request)
	if scopeErr != nil {
		r.logger.Error("worker runner failed llm usage scope failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", scopeErr)
		return
	}
	if _, err := managedagents.RecordLLMUsageWithContext(databaseCtx, r.store, failedUsage); err != nil {
		r.logger.Error("worker runner failed llm usage record failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err)
	}
}

func (r *WorkerRunner) failTurn(request TurnRequest, err error) {
	databaseCtx, scopeErr := databaseContextForTurn(context.Background(), request)
	if scopeErr != nil {
		r.logger.Error("worker runner fail transition scope failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", scopeErr)
		return
	}
	events, failErr := managedagents.FailSessionTurnWithContext(databaseCtx, r.store, request.SessionID, request.TurnID, err.Error())
	if failErr != nil {
		r.logger.Error("worker runner fail transition failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", failErr)
		return
	}
	r.logger.Error("worker runner turn failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err, "events", len(events))
}

func (r *WorkerRunner) registerTurn(key turnKey, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.turns[key]; exists {
		return false
	}
	r.turns[key] = cancel
	return true
}

func (r *WorkerRunner) takeTurn(key turnKey) context.CancelFunc {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel := r.turns[key]
	delete(r.turns, key)
	return cancel
}

func (r *WorkerRunner) deleteTurn(key turnKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.turns, key)
}

func newRunnerInstanceID() string {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("runner-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "localhost"
	}
	return fmt.Sprintf("%s-%d-%s", hostname, os.Getpid(), hex.EncodeToString(suffix[:]))
}
