package managedagents

import "context"

// WorkerContextStore applies the authenticated database scope to worker and
// worker-work operations without expanding the legacy Store interface.
type WorkerContextStore interface {
	RegisterWorkerContext(context.Context, RegisterWorkerInput) (Worker, error)
	GetWorkerContext(context.Context, string) (Worker, error)
	ListWorkersContext(context.Context, ListWorkersInput) ([]Worker, error)
	HeartbeatWorkerContext(context.Context, string, WorkerHeartbeatInput) (Worker, error)
	ArchiveWorkerContext(context.Context, string) (Worker, error)
	ReapExpiredWorkersContext(context.Context, ReapExpiredWorkersInput) ([]Worker, error)
	EnqueueWorkerWorkContext(context.Context, EnqueueWorkerWorkInput) (WorkerWork, error)
	GetWorkerWorkContext(context.Context, string) (WorkerWork, error)
	PollWorkerWorkContext(context.Context, string, PollWorkerWorkInput) (*WorkerWork, error)
	AckWorkerWorkContext(context.Context, string, string) (WorkerWork, error)
	HeartbeatWorkerWorkContext(context.Context, string, string, WorkerWorkHeartbeatInput) (WorkerWork, error)
	CancelWorkerWorkContext(context.Context, string, CancelWorkerWorkInput) (WorkerWork, error)
	RequeueWorkerWorkContext(context.Context, string, RequeueWorkerWorkInput) (WorkerWork, error)
	ReapExpiredWorkerWorkContext(context.Context, ReapExpiredWorkerWorkInput) ([]WorkerWork, error)
	CompleteWorkerWorkContext(context.Context, string, string, CompleteWorkerWorkInput) (WorkerWork, error)
}

func RegisterWorkerWithContext(ctx context.Context, store interface {
	RegisterWorker(RegisterWorkerInput) (Worker, error)
}, input RegisterWorkerInput) (Worker, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.RegisterWorkerContext(ctx, input)
	}
	return store.RegisterWorker(input)
}

func GetWorkerWithContext(ctx context.Context, store interface {
	GetWorker(string) (Worker, error)
}, id string) (Worker, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.GetWorkerContext(ctx, id)
	}
	return store.GetWorker(id)
}

func ListWorkersWithContext(ctx context.Context, store interface {
	ListWorkers(ListWorkersInput) ([]Worker, error)
}, input ListWorkersInput) ([]Worker, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.ListWorkersContext(ctx, input)
	}
	return store.ListWorkers(input)
}

func HeartbeatWorkerWithContext(ctx context.Context, store interface {
	HeartbeatWorker(string, WorkerHeartbeatInput) (Worker, error)
}, id string, input WorkerHeartbeatInput) (Worker, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.HeartbeatWorkerContext(ctx, id, input)
	}
	return store.HeartbeatWorker(id, input)
}

func ArchiveWorkerWithContext(ctx context.Context, store interface {
	ArchiveWorker(string) (Worker, error)
}, id string) (Worker, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.ArchiveWorkerContext(ctx, id)
	}
	return store.ArchiveWorker(id)
}

func ReapExpiredWorkersWithContext(ctx context.Context, store interface {
	ReapExpiredWorkers(ReapExpiredWorkersInput) ([]Worker, error)
}, input ReapExpiredWorkersInput) ([]Worker, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.ReapExpiredWorkersContext(ctx, input)
	}
	return store.ReapExpiredWorkers(input)
}

func EnqueueWorkerWorkWithContext(ctx context.Context, store interface {
	EnqueueWorkerWork(EnqueueWorkerWorkInput) (WorkerWork, error)
}, input EnqueueWorkerWorkInput) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.EnqueueWorkerWorkContext(ctx, input)
	}
	return store.EnqueueWorkerWork(input)
}

func GetWorkerWorkWithContext(ctx context.Context, store interface {
	GetWorkerWork(string) (WorkerWork, error)
}, id string) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.GetWorkerWorkContext(ctx, id)
	}
	return store.GetWorkerWork(id)
}

func PollWorkerWorkWithContext(ctx context.Context, store interface {
	PollWorkerWork(string, PollWorkerWorkInput) (*WorkerWork, error)
}, workerID string, input PollWorkerWorkInput) (*WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.PollWorkerWorkContext(ctx, workerID, input)
	}
	return store.PollWorkerWork(workerID, input)
}

func AckWorkerWorkWithContext(ctx context.Context, store interface {
	AckWorkerWork(string, string) (WorkerWork, error)
}, workerID, workID string) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.AckWorkerWorkContext(ctx, workerID, workID)
	}
	return store.AckWorkerWork(workerID, workID)
}

func HeartbeatWorkerWorkWithContext(ctx context.Context, store interface {
	HeartbeatWorkerWork(string, string, WorkerWorkHeartbeatInput) (WorkerWork, error)
}, workerID, workID string, input WorkerWorkHeartbeatInput) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.HeartbeatWorkerWorkContext(ctx, workerID, workID, input)
	}
	return store.HeartbeatWorkerWork(workerID, workID, input)
}

func CancelWorkerWorkWithContext(ctx context.Context, store interface {
	CancelWorkerWork(string, CancelWorkerWorkInput) (WorkerWork, error)
}, workID string, input CancelWorkerWorkInput) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.CancelWorkerWorkContext(ctx, workID, input)
	}
	return store.CancelWorkerWork(workID, input)
}

func RequeueWorkerWorkWithContext(ctx context.Context, store interface {
	RequeueWorkerWork(string, RequeueWorkerWorkInput) (WorkerWork, error)
}, workID string, input RequeueWorkerWorkInput) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.RequeueWorkerWorkContext(ctx, workID, input)
	}
	return store.RequeueWorkerWork(workID, input)
}

func ReapExpiredWorkerWorkWithContext(ctx context.Context, store interface {
	ReapExpiredWorkerWork(ReapExpiredWorkerWorkInput) ([]WorkerWork, error)
}, input ReapExpiredWorkerWorkInput) ([]WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.ReapExpiredWorkerWorkContext(ctx, input)
	}
	return store.ReapExpiredWorkerWork(input)
}

func CompleteWorkerWorkWithContext(ctx context.Context, store interface {
	CompleteWorkerWork(string, string, CompleteWorkerWorkInput) (WorkerWork, error)
}, workerID, workID string, input CompleteWorkerWorkInput) (WorkerWork, error) {
	if scoped, ok := store.(WorkerContextStore); ok {
		return scoped.CompleteWorkerWorkContext(ctx, workerID, workID, input)
	}
	return store.CompleteWorkerWork(workerID, workID, input)
}
