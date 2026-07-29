package objectcleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

type Config struct {
	WorkerID           string
	BatchSize          int
	LeaseDuration      time.Duration
	MaxAttempts        int
	RetryInitialDelay  time.Duration
	RetryMaxDelay      time.Duration
	OrphanSweepEnabled bool
	OrphanGracePeriod  time.Duration
	OrphanSweepLimit   int
}

type RunResult struct {
	Staged     int
	Claimed    int
	Completed  int
	Retried    int
	DeadLetter int
}

type Service struct {
	store       Store
	objectStore objectstore.Client
	config      Config
	now         func() time.Time
}

func NewService(store Store, objectStore objectstore.Client, config Config) (*Service, error) {
	config.WorkerID = strings.TrimSpace(config.WorkerID)
	if store == nil || objectStore == nil || config.WorkerID == "" {
		return nil, fmt.Errorf("%w: store, object store, and worker ID are required", ErrInvalid)
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, fmt.Errorf("%w: batch size must be between 1 and 1000", ErrInvalid)
	}
	if config.LeaseDuration <= 0 || config.MaxAttempts < 1 || config.RetryInitialDelay <= 0 || config.RetryMaxDelay < config.RetryInitialDelay {
		return nil, fmt.Errorf("%w: invalid retry configuration", ErrInvalid)
	}
	if config.OrphanSweepEnabled && (config.OrphanGracePeriod <= 0 || config.OrphanSweepLimit < 1 || config.OrphanSweepLimit > 1000) {
		return nil, fmt.Errorf("%w: invalid orphan sweep configuration", ErrInvalid)
	}
	return &Service{store: store, objectStore: objectStore, config: config, now: time.Now}, nil
}

func (s *Service) RunWorkspace(ctx context.Context, workspaceID string) (RunResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return RunResult{}, fmt.Errorf("%w: workspace ID is required", ErrInvalid)
	}
	var err error
	ctx, err = s.workspaceContext(ctx, workspaceID)
	if err != nil {
		return RunResult{}, err
	}
	now := s.now().UTC()
	result := RunResult{}
	if s.config.OrphanSweepEnabled {
		staged, err := s.store.StageOrphanObjectCleanup(ctx, StageInput{
			WorkspaceID: workspaceID,
			Cutoff:      now.Add(-s.config.OrphanGracePeriod),
			Limit:       s.config.OrphanSweepLimit,
			Now:         now,
		})
		if err != nil {
			return result, err
		}
		result.Staged = len(staged)
	}
	jobs, err := s.store.ClaimObjectCleanup(ctx, ClaimInput{
		WorkspaceID: workspaceID, WorkerID: s.config.WorkerID, Limit: s.config.BatchSize,
		Now: now, LeaseExpiresAt: now.Add(s.config.LeaseDuration),
	})
	if err != nil {
		return result, err
	}
	result.Claimed = len(jobs)
	for _, job := range jobs {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		deleteErr := s.deleteJobObject(ctx, job)
		missing := errors.Is(deleteErr, objectstore.ErrNotFound)
		if deleteErr == nil || missing {
			if err := s.store.CompleteObjectCleanup(ctx, CompleteInput{
				WorkspaceID: workspaceID, JobID: job.ID, WorkerID: s.config.WorkerID,
				ObjectWasMissing: missing, CompletedAt: s.now().UTC(),
			}); err != nil {
				return result, err
			}
			result.Completed++
			continue
		}
		deadLetter := job.AttemptCount >= s.config.MaxAttempts
		delay := cleanupRetryDelay(job.AttemptCount, s.config.RetryInitialDelay, s.config.RetryMaxDelay)
		if err := s.store.FailObjectCleanup(ctx, FailInput{
			WorkspaceID: workspaceID, JobID: job.ID, WorkerID: s.config.WorkerID,
			ErrorMessage: deleteErr.Error(), NextAttemptAt: s.now().UTC().Add(delay),
			DeadLetter: deadLetter, FailedAt: s.now().UTC(),
		}); err != nil {
			return result, err
		}
		if deadLetter {
			result.DeadLetter++
		} else {
			result.Retried++
		}
	}
	return result, nil
}

func (s *Service) deleteJobObject(ctx context.Context, job Job) error {
	actualProvider := objectstore.ProviderForClient(s.objectStore)
	if actualProvider != "" && strings.TrimSpace(job.StorageProvider) != "" && actualProvider != job.StorageProvider {
		return fmt.Errorf("object cleanup provider mismatch: worker=%s job=%s", actualProvider, job.StorageProvider)
	}
	return s.objectStore.DeleteObject(ctx, objectstore.DeleteObjectInput{
		Bucket: job.Bucket, Key: job.ObjectKey, Version: job.ObjectVersion,
	})
}

func (s *Service) workspaceContext(ctx context.Context, workspaceID string) (context.Context, error) {
	if provider, ok := s.store.(WorkspaceContextProvider); ok {
		return provider.ObjectCleanupWorkspaceContext(ctx, workspaceID)
	}
	return ctx, nil
}

func cleanupRetryDelay(attempt int, initial, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := initial
	for i := 1; i < attempt && delay < maximum; i++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}
