package main

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/serverconfig"
)

type testWorkerWorkReaperStore struct {
	mu      sync.Mutex
	calls   int
	results []managedagents.WorkerWork
	err     error
}

func (s *testWorkerWorkReaperStore) ReapExpiredWorkerWork(input managedagents.ReapExpiredWorkerWorkInput) ([]managedagents.WorkerWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	results := make([]managedagents.WorkerWork, len(s.results))
	copy(results, s.results)
	return results, nil
}

func (s *testWorkerWorkReaperStore) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type testWorkerReaperStore struct {
	mu      sync.Mutex
	calls   int
	results []managedagents.Worker
	err     error
}

func (s *testWorkerReaperStore) ReapExpiredWorkers(input managedagents.ReapExpiredWorkersInput) ([]managedagents.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	results := make([]managedagents.Worker, len(s.results))
	copy(results, s.results)
	return results, nil
}

func (s *testWorkerReaperStore) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type testObservabilityExporterRetryStore struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *testObservabilityExporterRetryStore) ListEvents(string, int64) ([]managedagents.Event, error) {
	return nil, nil
}

func (s *testObservabilityExporterRetryStore) RecordObservabilityExporterRun(input managedagents.RecordObservabilityExporterRunInput) (managedagents.ObservabilityExporterRun, error) {
	return managedagents.ObservabilityExporterRun{}, nil
}

func (s *testObservabilityExporterRetryStore) ListObservabilityExporterRuns(input managedagents.ListObservabilityExporterRunsInput) ([]managedagents.ObservabilityExporterRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

func (s *testObservabilityExporterRetryStore) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestStartWorkerReaperDisabled(t *testing.T) {
	store := &testWorkerReaperStore{}
	stop := startWorkerReaper(serverconfig.WorkerReaperConfig{
		Enabled:  false,
		Interval: time.Millisecond,
		Limit:    1,
	}, store, testLogger())
	defer stop()

	time.Sleep(20 * time.Millisecond)
	if store.CallCount() != 0 {
		t.Fatalf("expected disabled reaper not to call store, got %d", store.CallCount())
	}
}

func TestStartWorkerReaperRunsImmediatelyAndOnTicker(t *testing.T) {
	store := &testWorkerReaperStore{
		results: []managedagents.Worker{{ID: "wrk_1"}},
	}
	stop := startWorkerReaper(serverconfig.WorkerReaperConfig{
		Enabled:  true,
		Interval: 10 * time.Millisecond,
		Limit:    2,
	}, store, testLogger())
	defer stop()

	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.CallCount() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected reaper to call store at least twice, got %d", store.CallCount())
}

func TestReapExpiredWorkersSwallowsStoreErrors(t *testing.T) {
	store := &testWorkerReaperStore{err: errors.New("boom")}
	reapExpiredWorkers(store, testLogger(), 5)
	if store.CallCount() != 1 {
		t.Fatalf("expected one store call, got %d", store.CallCount())
	}
}

func TestStartWorkerWorkReaperDisabled(t *testing.T) {
	store := &testWorkerWorkReaperStore{}
	stop := startWorkerWorkReaper(serverconfig.WorkerWorkReaperConfig{
		Enabled:  false,
		Interval: time.Millisecond,
		Limit:    1,
	}, store, testLogger())
	defer stop()

	time.Sleep(20 * time.Millisecond)
	if store.CallCount() != 0 {
		t.Fatalf("expected disabled reaper not to call store, got %d", store.CallCount())
	}
}

func TestStartWorkerWorkReaperRunsImmediatelyAndOnTicker(t *testing.T) {
	store := &testWorkerWorkReaperStore{
		results: []managedagents.WorkerWork{{ID: "work_1"}},
	}
	stop := startWorkerWorkReaper(serverconfig.WorkerWorkReaperConfig{
		Enabled:  true,
		Interval: 10 * time.Millisecond,
		Limit:    2,
	}, store, testLogger())
	defer stop()

	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.CallCount() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected reaper to call store at least twice, got %d", store.CallCount())
}

func TestReapExpiredWorkerWorkSwallowsStoreErrors(t *testing.T) {
	store := &testWorkerWorkReaperStore{err: errors.New("boom")}
	reapExpiredWorkerWork(store, testLogger(), 5)
	if store.CallCount() != 1 {
		t.Fatalf("expected one store call, got %d", store.CallCount())
	}
}

func TestStartObservabilityExporterRetryDisabled(t *testing.T) {
	store := &testObservabilityExporterRetryStore{}
	stop := startObservabilityExporterRetry(serverconfig.ObservabilityExporterRetryConfig{
		Enabled:  false,
		Interval: time.Millisecond,
		Limit:    1,
	}, store, testLogger())
	defer stop()

	time.Sleep(20 * time.Millisecond)
	if store.CallCount() != 0 {
		t.Fatalf("expected disabled observability retry worker not to call store, got %d", store.CallCount())
	}
}

func TestStartObservabilityExporterRetryRunsImmediatelyAndOnTicker(t *testing.T) {
	store := &testObservabilityExporterRetryStore{}
	stop := startObservabilityExporterRetry(serverconfig.ObservabilityExporterRetryConfig{
		Enabled:  true,
		Interval: 10 * time.Millisecond,
		Limit:    2,
	}, store, testLogger())
	defer stop()

	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.CallCount() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected observability retry worker to call store at least twice, got %d", store.CallCount())
}

func TestRetryObservabilityExportersSwallowsStoreErrors(t *testing.T) {
	store := &testObservabilityExporterRetryStore{err: errors.New("boom")}
	retryObservabilityExporters(store, testLogger(), 5)
	if store.CallCount() != 1 {
		t.Fatalf("expected one store call, got %d", store.CallCount())
	}
}
