package runner

import (
	"sync"
	"sync/atomic"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestWorkerRunnerHandlesInterruptDuringFailedLeaseHeartbeat(t *testing.T) {
	store := &coordinatedLeaseFailureStore{
		persistentMockStore: newPersistentMockStore(managedagents.SessionTurnWork{
			SessionID: "sesn_lease_race", TurnID: "turn_lease_race", UserEventSeq: 4,
		}),
		renewStarted: make(chan struct{}),
		finishRenew:  make(chan struct{}),
	}
	t.Cleanup(store.finish)
	executor := newBlockingExecutor()
	runner := NewWorkerRunnerWithConfig(store, executor, persistentRunnerTestConfig(1), nil)
	defer runner.Close()

	<-executor.started
	<-store.renewStarted
	if err := runner.InterruptTurn(t.Context(), InterruptRequest{
		SessionID: "sesn_lease_race",
		TurnID:    "turn_lease_race",
	}); err != nil {
		t.Fatalf("interrupt turn: %v", err)
	}
	<-executor.canceled
	store.finish()

	waitFor(t, func() bool { return store.releaseCalls.Load() == 1 })
	if got := store.completeCalls(); got != 0 {
		t.Fatalf("canceled turn completed %d times", got)
	}
	if got := store.failCalls(); got != 0 {
		t.Fatalf("canceled turn failed %d times", got)
	}
	if got := store.releaseCalls.Load(); got != 1 {
		t.Fatalf("lease released %d times", got)
	}
}

type coordinatedLeaseFailureStore struct {
	*persistentMockStore
	renewStarted chan struct{}
	finishRenew  chan struct{}
	finishOnce   sync.Once
	releaseCalls atomic.Int32
}

func (s *coordinatedLeaseFailureStore) RenewSessionTurnLease(managedagents.RenewSessionTurnLeaseInput) (bool, error) {
	close(s.renewStarted)
	<-s.finishRenew
	return false, managedagents.ErrLeaseLost
}

func (s *coordinatedLeaseFailureStore) ReleaseSessionTurnLease(managedagents.ReleaseSessionTurnLeaseInput) error {
	s.releaseCalls.Add(1)
	return nil
}

func (s *coordinatedLeaseFailureStore) finish() {
	s.finishOnce.Do(func() { close(s.finishRenew) })
}
