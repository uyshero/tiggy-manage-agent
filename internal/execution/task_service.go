package execution

import (
	"context"

	"tiggy-manage-agent/internal/managedagents"
)

type taskToolService struct {
	store managedagents.SessionTaskPlanStore
}

func resolveTaskToolService(store any) *taskToolService {
	taskStore, ok := store.(managedagents.SessionTaskPlanStore)
	if !ok || taskStore == nil {
		return nil
	}
	return &taskToolService{store: taskStore}
}

func (s taskToolService) CreatePlan(ctx context.Context, sessionID string, input managedagents.CreateSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return s.store.CreateSessionTaskPlanContext(ctx, sessionID, input)
}

func (s taskToolService) GetPlan(ctx context.Context, sessionID string) (managedagents.SessionTaskPlan, error) {
	return s.store.GetCurrentSessionTaskPlanContext(ctx, sessionID)
}

func (s taskToolService) UpdateItems(ctx context.Context, sessionID string, input managedagents.UpdateSessionTaskItemsInput) (managedagents.SessionTaskPlanResult, error) {
	return s.store.UpdateSessionTaskItemsContext(ctx, sessionID, input)
}

func (s taskToolService) CompletePlan(ctx context.Context, sessionID string, input managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return s.store.CompleteSessionTaskPlanContext(ctx, sessionID, input)
}

func (s taskToolService) CancelPlan(ctx context.Context, sessionID string, input managedagents.FinishSessionTaskPlanInput) (managedagents.SessionTaskPlanResult, error) {
	return s.store.CancelSessionTaskPlanContext(ctx, sessionID, input)
}
