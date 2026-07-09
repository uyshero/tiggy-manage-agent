package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/observability"
)

var (
	ErrRunnerQueueFull = errors.New("runner queue full")
	ErrRunnerStopped   = errors.New("runner stopped")
)

// TurnExecutor 是真正执行 turn 的最小接口。
// WorkerRunner 负责排队、取消和状态回写；TurnExecutor 只负责产出 agent.message payload。
type TurnExecutor interface {
	RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error)
}

type TurnResult struct {
	AgentPayload json.RawMessage
	Usage        *managedagents.RecordLLMUsageInput
}

// WorkerRunner 是后续接 Sandbox / Agent Runtime 的队列化 Runner 骨架。
// 它不绑定 HTTP 生命周期，StartTurn 只负责把任务放入内部队列。
type WorkerRunner struct {
	store        managedagents.Store
	turnExecutor TurnExecutor
	logger       *slog.Logger

	queue   chan workerJob
	stopped chan struct{}

	mu    sync.Mutex
	turns map[turnKey]context.CancelFunc
}

type workerJob struct {
	ctx     context.Context
	request TurnRequest
}

func NewWorkerRunner(store managedagents.Store, turnExecutor TurnExecutor, queueSize int, logger *slog.Logger) *WorkerRunner {
	if logger == nil {
		logger = slog.Default()
	}
	if queueSize <= 0 {
		queueSize = 16
	}
	runner := &WorkerRunner{
		store:        store,
		turnExecutor: turnExecutor,
		logger:       logger,
		queue:        make(chan workerJob, queueSize),
		stopped:      make(chan struct{}),
		turns:        make(map[turnKey]context.CancelFunc),
	}
	go runner.work()
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

	key := turnKey{sessionID: request.SessionID, turnID: request.TurnID}
	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	if _, exists := r.turns[key]; exists {
		r.mu.Unlock()
		cancel()
		return ErrTurnAlreadyRunning
	}
	r.turns[key] = cancel
	r.mu.Unlock()

	job := workerJob{ctx: ctx, request: request}
	select {
	case r.queue <- job:
		r.logger.Info("worker runner turn queued",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
		)
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
		r.logger.Info("worker runner interrupt ignored",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"reason", "turn_not_active",
		)
		return nil
	}
	cancel()
	r.logger.Info("worker runner turn canceled",
		"session_id", request.SessionID,
		"turn_id", request.TurnID,
	)
	return nil
}

func (r *WorkerRunner) Close() {
	select {
	case <-r.stopped:
		return
	default:
		close(r.stopped)
	}

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
}

func (r *WorkerRunner) work() {
	for {
		select {
		case <-r.stopped:
			return
		case job := <-r.queue:
			r.runJob(job)
		}
	}
}

func (r *WorkerRunner) runJob(job workerJob) {
	key := turnKey{sessionID: job.request.SessionID, turnID: job.request.TurnID}
	defer r.deleteTurn(key)

	result, err := r.turnExecutor.RunTurn(job.ctx, job.request)
	if err != nil {
		if errors.Is(err, ErrTurnWaitingApproval) {
			r.logger.Info("worker runner turn waiting for approval",
				"session_id", job.request.SessionID,
				"turn_id", job.request.TurnID,
			)
			return
		}
		if job.ctx.Err() != nil {
			r.logger.Info("worker runner turn canceled before completion",
				"session_id", job.request.SessionID,
				"turn_id", job.request.TurnID,
			)
			return
		}
		r.recordFailedUsage(job.request, result.Usage, err)
		r.failTurn(job.request, err)
		return
	}
	if job.ctx.Err() != nil {
		r.logger.Info("worker runner turn canceled before completion",
			"session_id", job.request.SessionID,
			"turn_id", job.request.TurnID,
		)
		return
	}

	events, err := r.store.CompleteSessionTurn(job.request.SessionID, job.request.TurnID, result.AgentPayload)
	if err != nil {
		r.logger.Error("worker runner completion failed",
			"session_id", job.request.SessionID,
			"turn_id", job.request.TurnID,
			"error", err,
		)
		return
	}
	if result.Usage != nil {
		if _, err := r.store.RecordLLMUsage(*result.Usage); err != nil {
			r.logger.Error("worker runner llm usage record failed",
				"session_id", job.request.SessionID,
				"turn_id", job.request.TurnID,
				"error", err,
			)
		}
	}
	r.logger.Info("worker runner turn completed",
		"session_id", job.request.SessionID,
		"turn_id", job.request.TurnID,
		"events", len(events),
	)
	if err := observability.RefreshSessionSummary(r.store, job.request.SessionID, job.request.TurnID); err != nil {
		r.logger.Warn("worker runner summary refresh failed",
			"session_id", job.request.SessionID,
			"turn_id", job.request.TurnID,
			"error", err,
		)
	}
	if result, err := observability.ExportTurnTraceFromEnv(r.store, job.request.SessionID, job.request.TurnID); err != nil {
		r.logger.Warn("worker runner observability export failed",
			"session_id", job.request.SessionID,
			"turn_id", job.request.TurnID,
			"error", err,
		)
	} else if !result.Skipped {
		r.logger.Info("worker runner observability export completed",
			"session_id", job.request.SessionID,
			"turn_id", job.request.TurnID,
			"trace_id", result.TraceID,
			"perfetto_path", resultPerfettoPath(result),
			"otlp_endpoint", resultOTLPEndpoint(result),
		)
	}
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
	failedUsage.Status = "failed"
	if turnErr != nil && failedUsage.ErrorMessage == "" {
		failedUsage.ErrorMessage = turnErr.Error()
	}
	if _, err := r.store.RecordLLMUsage(failedUsage); err != nil {
		r.logger.Error("worker runner failed llm usage record failed",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"error", err,
		)
	}
}

func (r *WorkerRunner) failTurn(request TurnRequest, err error) {
	events, failErr := r.store.FailSessionTurn(request.SessionID, request.TurnID, err.Error())
	if failErr != nil {
		r.logger.Error("worker runner fail transition failed",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"error", failErr,
		)
		return
	}
	r.logger.Error("worker runner turn failed",
		"session_id", request.SessionID,
		"turn_id", request.TurnID,
		"error", err,
		"events", len(events),
	)
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
