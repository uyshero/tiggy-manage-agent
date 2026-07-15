package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skillretention"
)

func TestSkillAssetRetentionHTTPLifecyclePreviewAndRun(t *testing.T) {
	store := newRetentionHTTPTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
	created := postJSONWithStatus[struct {
		Policy  skillretention.PolicyRecord  `json:"policy"`
		Version skillretention.PolicyVersion `json:"version"`
	}](t, server, http.MethodPost, "/v1/skill-asset-retention/policies", `{
		"scope_type":"workspace","workspace_id":"wksp_default",
		"config":{"enabled":true,"retention_days":30,"delete_limit":25}
	}`, http.StatusCreated)
	if created.Policy.CurrentVersion != 1 || !created.Version.Config.Enabled {
		t.Fatalf("unexpected created retention policy: %#v", created)
	}
	published := postJSONWithStatus[skillretention.PolicyVersion](t, server, http.MethodPost,
		"/v1/skill-asset-retention/policies/"+created.Policy.ID+"/versions", `{
			"config":{"enabled":true,"retention_days":45,"delete_limit":10}
		}`, http.StatusCreated)
	if published.Version != 2 || published.Config.RetentionDays != 45 {
		t.Fatalf("unexpected published retention policy: %#v", published)
	}
	version1 := getJSON[skillretention.PolicyVersion](t, server,
		"/v1/skill-asset-retention/policies/"+created.Policy.ID+"/versions/1")
	if version1.Version != 1 || version1.Config.RetentionDays != 30 || version1.Checksum == published.Checksum {
		t.Fatalf("immutable retention policy version changed: %#v", version1)
	}
	effective := getJSON[skillretention.EffectivePolicy](t, server, "/v1/skill-asset-retention/effective?workspace_id=wksp_default")
	if effective.Policy.ID != created.Policy.ID || effective.Version.Version != 2 {
		t.Fatalf("unexpected effective retention policy: %#v", effective)
	}
	preview := postJSONWithStatus[skillretention.Preview](t, server, http.MethodPost, "/v1/skill-asset-gc/preview",
		`{"workspace_id":"wksp_default"}`, http.StatusOK)
	if preview.CandidateCount != 0 || preview.Effective.Version.Version != 2 {
		t.Fatalf("unexpected GC preview: %#v", preview)
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPost, "/v1/skill-asset-gc/run",
		`{"workspace_id":"wksp_default"}`, http.StatusBadRequest)
	result := postJSONWithStatus[skillretention.RunResult](t, server, http.MethodPost, "/v1/skill-asset-gc/run",
		`{"workspace_id":"wksp_default","confirm":"DELETE"}`, http.StatusOK)
	if result.Run.Status != skillretention.RunStatusSucceeded || result.Run.CandidateCount != 0 {
		t.Fatalf("unexpected GC run: %#v", result)
	}
	runs := getJSON[struct {
		Runs []skillretention.Run `json:"runs"`
	}](t, server, "/v1/skill-asset-gc/runs?workspace_id=wksp_default")
	if len(runs.Runs) != 1 || runs.Runs[0].ID != result.Run.ID {
		t.Fatalf("unexpected GC run list: %#v", runs.Runs)
	}
	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 20})
	if err != nil || len(audits) != 4 {
		t.Fatalf("expected create/publish/preview/run audits: audits=%#v err=%v", audits, err)
	}
}

type retentionHTTPTestStore struct {
	*testStore
	policies []skillretention.PolicyRecord
	versions map[string][]skillretention.PolicyVersion
	runs     []skillretention.Run
}

func newRetentionHTTPTestStore() *retentionHTTPTestStore {
	return &retentionHTTPTestStore{testStore: newTestStore(), versions: map[string][]skillretention.PolicyVersion{}}
}

func (s *retentionHTTPTestStore) CreateSkillAssetRetentionPolicy(_ context.Context, input skillretention.CreatePolicyInput) (skillretention.PolicyRecord, skillretention.PolicyVersion, error) {
	normalized, err := skillretention.NormalizePolicy(input.Config)
	if err != nil {
		return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	for _, policy := range s.policies {
		if policy.Status == skillretention.PolicyStatusActive && policy.ScopeType == input.ScopeType &&
			(policy.WorkspaceID == input.WorkspaceID || policy.OrganizationID == input.OrganizationID) {
			return skillretention.PolicyRecord{}, skillretention.PolicyVersion{}, managedagents.ErrConflict
		}
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("sarp_test_%d", len(s.policies)+1)
	revision, _ := skillretention.PolicyRevision(normalized)
	record := skillretention.PolicyRecord{
		ID: id, ScopeType: input.ScopeType, OrganizationID: input.OrganizationID, WorkspaceID: input.WorkspaceID,
		Status: skillretention.PolicyStatusActive, CurrentVersion: 1, CreatedBy: input.CreatedBy, CreatedAt: now,
	}
	version := skillretention.PolicyVersion{ID: id + "_v1", PolicyID: id, Version: 1, Config: normalized, Checksum: revision, CreatedBy: input.CreatedBy, CreatedAt: now}
	s.policies = append(s.policies, record)
	s.versions[id] = []skillretention.PolicyVersion{version}
	return record, version, nil
}

func (s *retentionHTTPTestStore) GetSkillAssetRetentionPolicy(_ context.Context, id string) (skillretention.PolicyRecord, error) {
	for _, policy := range s.policies {
		if policy.ID == id {
			return policy, nil
		}
	}
	return skillretention.PolicyRecord{}, managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) ListSkillAssetRetentionPolicies(_ context.Context, input skillretention.ListPoliciesInput) ([]skillretention.PolicyRecord, error) {
	items := []skillretention.PolicyRecord{}
	for _, policy := range s.policies {
		if !input.IncludeArchived && policy.Status != skillretention.PolicyStatusActive {
			continue
		}
		if input.WorkspaceID != "" && policy.WorkspaceID != input.WorkspaceID {
			continue
		}
		if input.OrganizationID != "" && policy.OrganizationID != input.OrganizationID {
			continue
		}
		items = append(items, policy)
	}
	return items, nil
}

func (s *retentionHTTPTestStore) PublishSkillAssetRetentionPolicyVersion(_ context.Context, id string, config skillretention.Policy, createdBy string) (skillretention.PolicyVersion, error) {
	policy, err := s.GetSkillAssetRetentionPolicy(context.Background(), id)
	if err != nil {
		return skillretention.PolicyVersion{}, err
	}
	normalized, err := skillretention.NormalizePolicy(config)
	if err != nil {
		return skillretention.PolicyVersion{}, managedagents.ErrInvalid
	}
	versionNumber := policy.CurrentVersion + 1
	revision, _ := skillretention.PolicyRevision(normalized)
	version := skillretention.PolicyVersion{
		ID: fmt.Sprintf("%s_v%d", id, versionNumber), PolicyID: id, Version: versionNumber,
		Config: normalized, Checksum: revision, CreatedBy: createdBy, CreatedAt: time.Now().UTC(),
	}
	s.versions[id] = append(s.versions[id], version)
	for index := range s.policies {
		if s.policies[index].ID == id {
			s.policies[index].CurrentVersion = versionNumber
		}
	}
	return version, nil
}

func (s *retentionHTTPTestStore) GetSkillAssetRetentionPolicyVersion(_ context.Context, id string, version int) (skillretention.PolicyVersion, error) {
	for _, item := range s.versions[id] {
		if item.Version == version {
			return item, nil
		}
	}
	return skillretention.PolicyVersion{}, managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) ArchiveSkillAssetRetentionPolicy(_ context.Context, id string) (skillretention.PolicyRecord, error) {
	for index := range s.policies {
		if s.policies[index].ID == id {
			now := time.Now().UTC()
			s.policies[index].Status = skillretention.PolicyStatusArchived
			s.policies[index].ArchivedAt = &now
			return s.policies[index], nil
		}
	}
	return skillretention.PolicyRecord{}, managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) ResolveSkillAssetRetentionPolicy(_ context.Context, workspaceID string) (skillretention.EffectivePolicy, bool, error) {
	for index := len(s.policies) - 1; index >= 0; index-- {
		policy := s.policies[index]
		if policy.Status != skillretention.PolicyStatusActive || policy.WorkspaceID != workspaceID {
			continue
		}
		version := s.versions[policy.ID][policy.CurrentVersion-1]
		return skillretention.EffectivePolicy{Source: policy.ScopeType, Policy: policy, Version: version, Config: version.Config, Revision: version.Checksum}, true, nil
	}
	return skillretention.EffectivePolicy{}, false, nil
}

func (s *retentionHTTPTestStore) ListSkillAssetGCCandidates(context.Context, skillretention.ListCandidatesInput) ([]skillretention.Candidate, error) {
	return []skillretention.Candidate{}, nil
}

func (s *retentionHTTPTestStore) AcquireSkillAssetGCLock(context.Context, string) (func() error, error) {
	return func() error { return nil }, nil
}

func (s *retentionHTTPTestStore) StartSkillAssetGCRun(_ context.Context, input skillretention.StartRunInput) (skillretention.Run, []skillretention.Item, error) {
	finished := input.StartedAt
	run := skillretention.Run{
		ID: fmt.Sprintf("sagcr_test_%d", len(s.runs)+1), WorkspaceID: input.WorkspaceID,
		PolicySource: input.Effective.Source, PolicyID: input.Effective.Policy.ID,
		PolicyVersion: input.Effective.Version.Version, PolicyRevision: input.Effective.Revision,
		RetentionDays: input.Effective.Config.RetentionDays, DeleteLimit: input.Effective.Config.DeleteLimit,
		Status: skillretention.RunStatusSucceeded, CandidateCount: len(input.Candidates),
		RequestedBy: input.RequestedBy, StartedAt: input.StartedAt, FinishedAt: &finished,
	}
	s.runs = append(s.runs, run)
	return run, []skillretention.Item{}, nil
}

func (s *retentionHTTPTestStore) ClaimSkillAssetGCItem(context.Context, string, time.Time) (skillretention.Candidate, bool, string, error) {
	return skillretention.Candidate{}, false, "not_found", managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) SkipSkillAssetGCItem(context.Context, string, string) error {
	return nil
}
func (s *retentionHTTPTestStore) FailSkillAssetGCItem(context.Context, string, string) error {
	return nil
}

func (s *retentionHTTPTestStore) FinalizeSkillAssetGCItem(context.Context, string, string, bool) (skillretention.Tombstone, error) {
	return skillretention.Tombstone{}, managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) FinishSkillAssetGCRun(_ context.Context, id string) (skillretention.Run, error) {
	for _, run := range s.runs {
		if run.ID == id {
			return run, nil
		}
	}
	return skillretention.Run{}, managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) ListSkillAssetGCRuns(_ context.Context, input skillretention.ListRunsInput) ([]skillretention.Run, error) {
	items := []skillretention.Run{}
	for _, run := range s.runs {
		if run.WorkspaceID == input.WorkspaceID {
			items = append(items, run)
		}
	}
	return items, nil
}

func (s *retentionHTTPTestStore) GetSkillAssetGCRun(_ context.Context, id string) (skillretention.Run, []skillretention.Item, error) {
	for _, run := range s.runs {
		if run.ID == id {
			return run, []skillretention.Item{}, nil
		}
	}
	return skillretention.Run{}, nil, managedagents.ErrNotFound
}

func (s *retentionHTTPTestStore) ListSkillAssetGCTombstones(context.Context, skillretention.ListTombstonesInput) ([]skillretention.Tombstone, error) {
	return []skillretention.Tombstone{}, nil
}

func (s *retentionHTTPTestStore) ListSkillAssetGCWorkspaceIDs(context.Context) ([]string, error) {
	return []string{managedagents.DefaultWorkspaceID}, nil
}

var _ skillretention.Store = (*retentionHTTPTestStore)(nil)
var _ managedagents.Store = (*retentionHTTPTestStore)(nil)
