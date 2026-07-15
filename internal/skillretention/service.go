package skillretention

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

type Service struct {
	store         Store
	objectStore   objectstore.Client
	defaultPolicy Policy
	now           func() time.Time
}

func NewService(store Store, objectStore objectstore.Client, defaultPolicy Policy) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalid)
	}
	if objectStore == nil {
		return nil, fmt.Errorf("%w: object store is required", ErrInvalid)
	}
	normalized, err := NormalizePolicy(defaultPolicy)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid server policy: %v", ErrInvalid, err)
	}
	return &Service{store: store, objectStore: objectStore, defaultPolicy: normalized, now: time.Now}, nil
}

func (s *Service) EffectivePolicy(ctx context.Context, workspaceID string) (EffectivePolicy, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return EffectivePolicy{}, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	var err error
	ctx, err = s.workspaceContext(ctx, workspaceID)
	if err != nil {
		return EffectivePolicy{}, err
	}
	effective, found, err := s.store.ResolveSkillAssetRetentionPolicy(ctx, workspaceID)
	if err != nil {
		return EffectivePolicy{}, err
	}
	if found {
		return effective, nil
	}
	revision, err := PolicyRevision(s.defaultPolicy)
	if err != nil {
		return EffectivePolicy{}, err
	}
	return EffectivePolicy{Source: SourceServer, Config: s.defaultPolicy, Revision: revision}, nil
}

func (s *Service) Preview(ctx context.Context, workspaceID string, requestedLimit int) (Preview, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	var err error
	ctx, err = s.workspaceContext(ctx, workspaceID)
	if err != nil {
		return Preview{}, err
	}
	effective, err := s.EffectivePolicy(ctx, workspaceID)
	if err != nil {
		return Preview{}, err
	}
	now := s.now().UTC()
	cutoff := now.Add(-time.Duration(effective.Config.RetentionDays) * 24 * time.Hour)
	limit := effective.Config.DeleteLimit
	if requestedLimit > 0 && requestedLimit < limit {
		limit = requestedLimit
	}
	candidates, err := s.store.ListSkillAssetGCCandidates(ctx, ListCandidatesInput{
		WorkspaceID: strings.TrimSpace(workspaceID), Cutoff: cutoff, Limit: limit,
	})
	if err != nil {
		return Preview{}, err
	}
	var bytes int64
	for _, candidate := range candidates {
		bytes += candidate.SizeBytes
	}
	recordCandidates(len(candidates))
	return Preview{
		WorkspaceID: strings.TrimSpace(workspaceID), Effective: effective, Cutoff: cutoff,
		CandidateCount: len(candidates), CandidateBytes: bytes, Candidates: candidates,
	}, nil
}

func (s *Service) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	if request.WorkspaceID == "" {
		return RunResult{}, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	var err error
	ctx, err = s.workspaceContext(ctx, request.WorkspaceID)
	if err != nil {
		return RunResult{}, err
	}
	release, err := s.store.AcquireSkillAssetGCLock(ctx, request.WorkspaceID)
	if err != nil {
		return RunResult{}, err
	}
	defer release()

	preview, err := s.Preview(ctx, request.WorkspaceID, request.Limit)
	if err != nil {
		return RunResult{}, err
	}
	if !preview.Effective.Config.Enabled {
		return RunResult{}, ErrDisabled
	}
	startedAt := s.now().UTC()
	run, items, err := s.store.StartSkillAssetGCRun(ctx, StartRunInput{
		WorkspaceID: request.WorkspaceID, Effective: preview.Effective,
		RequestedBy: request.RequestedBy, StartedAt: startedAt, Candidates: preview.Candidates,
	})
	if err != nil {
		return RunResult{}, err
	}
	for _, item := range items {
		candidate, eligible, reason, claimErr := s.store.ClaimSkillAssetGCItem(ctx, item.ID, preview.Cutoff)
		if claimErr != nil {
			_ = s.store.FailSkillAssetGCItem(ctx, item.ID, claimErr.Error())
			recordObject(ItemStatusFailed, item.Candidate.SizeBytes)
			continue
		}
		if !eligible {
			_ = s.store.SkipSkillAssetGCItem(ctx, item.ID, reason)
			recordObject(ItemStatusSkipped, item.Candidate.SizeBytes)
			continue
		}
		deleteErr := s.objectStore.DeleteObject(ctx, objectstore.DeleteObjectInput{
			Bucket: candidate.Bucket, Key: candidate.ObjectKey, Version: candidate.ObjectVersion,
		})
		objectWasMissing := errors.Is(deleteErr, objectstore.ErrNotFound)
		if deleteErr != nil && !objectWasMissing {
			_ = s.store.FailSkillAssetGCItem(ctx, item.ID, deleteErr.Error())
			recordObject(ItemStatusFailed, candidate.SizeBytes)
			continue
		}
		if _, finalizeErr := s.store.FinalizeSkillAssetGCItem(ctx, item.ID, request.RequestedBy, objectWasMissing); finalizeErr != nil {
			_ = s.store.FailSkillAssetGCItem(ctx, item.ID, finalizeErr.Error())
			recordObject(ItemStatusFailed, candidate.SizeBytes)
			continue
		}
		recordObject(ItemStatusDeleted, candidate.SizeBytes)
	}
	run, err = s.store.FinishSkillAssetGCRun(ctx, run.ID)
	if err != nil {
		return RunResult{}, err
	}
	run, items, err = s.store.GetSkillAssetGCRun(ctx, run.ID)
	if err != nil {
		return RunResult{}, err
	}
	recordRun(run.Status)
	return RunResult{Run: run, Items: items}, nil
}

func (s *Service) workspaceContext(ctx context.Context, workspaceID string) (context.Context, error) {
	provider, ok := s.store.(WorkspaceContextProvider)
	if !ok {
		return ctx, nil
	}
	return provider.SkillAssetGCWorkspaceContext(ctx, workspaceID)
}
