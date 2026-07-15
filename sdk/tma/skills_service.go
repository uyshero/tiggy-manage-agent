package tma

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type SkillsService struct{ client *Client }

func (s *SkillsService) Create(ctx context.Context, request CreateSkillRequest) (Skill, error) {
	var skill Skill
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skills", request, &skill)
	return skill, err
}

func (s *SkillsService) List(ctx context.Context, query SkillListQuery) ([]Skill, error) {
	values := url.Values{}
	if query.WorkspaceID != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if query.IncludeArchived {
		values.Set("include_archived", "true")
	}
	var response struct {
		Skills []Skill `json:"skills"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skills", values), nil, &response)
	return response.Skills, err
}

func (s *SkillsService) Get(ctx context.Context, skillID string) (Skill, error) {
	var skill Skill
	err := s.client.DoJSON(ctx, http.MethodGet, skillPath(skillID), nil, &skill)
	return skill, err
}

func (s *SkillsService) Archive(ctx context.Context, skillID string) (Skill, error) {
	var skill Skill
	err := s.client.DoJSON(ctx, http.MethodPost, skillPath(skillID)+"/archive", nil, &skill)
	return skill, err
}

func (s *SkillsService) CreateVersion(ctx context.Context, skillID string, request CreateSkillVersionRequest) (SkillVersion, error) {
	var version SkillVersion
	err := s.client.DoJSON(ctx, http.MethodPost, skillPath(skillID)+"/versions", request, &version)
	return version, err
}

func (s *SkillsService) ListVersions(ctx context.Context, skillID string) ([]SkillVersion, error) {
	var response struct {
		Versions []SkillVersion `json:"versions"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, skillPath(skillID)+"/versions", nil, &response)
	return response.Versions, err
}

func (s *SkillsService) GetVersion(ctx context.Context, skillID string, version int32) (SkillVersion, error) {
	var result SkillVersion
	err := s.client.DoJSON(ctx, http.MethodGet, skillVersionPath(skillID, version), nil, &result)
	return result, err
}

func (s *SkillsService) DownloadPackage(ctx context.Context, skillID string, version int32, output io.Writer) error {
	return s.client.Download(ctx, skillVersionPath(skillID, version)+"/package", output)
}

func (s *SkillsService) ResolvePreview(ctx context.Context, request ResolveSkillsPreviewRequest) (ResolveSkillsResult, error) {
	var result ResolveSkillsResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skills/resolve-preview", request, &result)
	return result, err
}

func (s *SkillsService) ListUsages(ctx context.Context, sessionID string, turnID string) ([]SkillUsage, error) {
	values := url.Values{}
	if turnID != "" {
		values.Set("turn_id", turnID)
	}
	var response struct {
		Usages []SkillUsage `json:"skill_usages"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery(sessionPath(sessionID)+"/skill-usages", values), nil, &response)
	return response.Usages, err
}

func (s *SkillsService) BackfillPackages(ctx context.Context, request SkillPackageBackfillRequest) (SkillPackageBackfillResult, error) {
	var result SkillPackageBackfillResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skill-packages/backfill", request, &result)
	return result, err
}

func (s *SkillsService) EffectiveRetentionPolicy(ctx context.Context, workspaceID string) (EffectiveSkillRetentionPolicy, error) {
	values := url.Values{}
	if workspaceID != "" {
		values.Set("workspace_id", workspaceID)
	}
	var result EffectiveSkillRetentionPolicy
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skill-asset-retention/effective", values), nil, &result)
	return result, err
}

func (s *SkillsService) CreateRetentionPolicy(ctx context.Context, request CreateSkillRetentionPolicyRequest) (SkillRetentionPolicyResult, error) {
	var result SkillRetentionPolicyResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skill-asset-retention/policies", request, &result)
	return result, err
}

func (s *SkillsService) ListRetentionPolicies(ctx context.Context, query SkillRetentionPolicyQuery) ([]SkillRetentionPolicy, error) {
	values := url.Values{}
	if query.OrganizationID != "" {
		values.Set("organization_id", query.OrganizationID)
	}
	if query.WorkspaceID != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if query.IncludeArchived {
		values.Set("include_archived", "true")
	}
	var response struct {
		Policies []SkillRetentionPolicy `json:"policies"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skill-asset-retention/policies", values), nil, &response)
	return response.Policies, err
}

func (s *SkillsService) GetRetentionPolicy(ctx context.Context, policyID string) (SkillRetentionPolicyResult, error) {
	var result SkillRetentionPolicyResult
	err := s.client.DoJSON(ctx, http.MethodGet, retentionPolicyPath(policyID), nil, &result)
	return result, err
}

func (s *SkillsService) PublishRetentionPolicyVersion(ctx context.Context, policyID string, request PublishSkillRetentionPolicyRequest) (SkillRetentionPolicyVersion, error) {
	var version SkillRetentionPolicyVersion
	err := s.client.DoJSON(ctx, http.MethodPost, retentionPolicyPath(policyID)+"/versions", request, &version)
	return version, err
}

func (s *SkillsService) GetRetentionPolicyVersion(ctx context.Context, policyID string, version int32) (SkillRetentionPolicyVersion, error) {
	var result SkillRetentionPolicyVersion
	err := s.client.DoJSON(ctx, http.MethodGet, retentionPolicyPath(policyID)+"/versions/"+strconv.FormatInt(int64(version), 10), nil, &result)
	return result, err
}

func (s *SkillsService) ArchiveRetentionPolicy(ctx context.Context, policyID string) (SkillRetentionPolicy, error) {
	var result SkillRetentionPolicy
	err := s.client.DoJSON(ctx, http.MethodPost, retentionPolicyPath(policyID)+"/archive", nil, &result)
	return result, err
}

func (s *SkillsService) PreviewAssetGC(ctx context.Context, request SkillAssetGCRequest) (SkillAssetGCPreview, error) {
	var preview SkillAssetGCPreview
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skill-asset-gc/preview", request, &preview)
	return preview, err
}

func (s *SkillsService) RunAssetGC(ctx context.Context, request SkillAssetGCRequest) (SkillAssetGCRunResult, error) {
	var result SkillAssetGCRunResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skill-asset-gc/run", request, &result)
	return result, err
}

func (s *SkillsService) ListAssetGCRuns(ctx context.Context, query SkillAssetGCListQuery) ([]SkillAssetGCRun, error) {
	values := assetGCListValues(query)
	var response struct {
		Runs []SkillAssetGCRun `json:"runs"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skill-asset-gc/runs", values), nil, &response)
	return response.Runs, err
}

func (s *SkillsService) GetAssetGCRun(ctx context.Context, runID string) (SkillAssetGCRunResult, error) {
	var result SkillAssetGCRunResult
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/skill-asset-gc/runs/"+url.PathEscape(runID), nil, &result)
	return result, err
}

func (s *SkillsService) ListAssetGCTombstones(ctx context.Context, query SkillAssetGCListQuery) ([]SkillAssetGCTombstone, error) {
	var response struct {
		Tombstones []SkillAssetGCTombstone `json:"tombstones"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skill-asset-gc/tombstones", assetGCListValues(query)), nil, &response)
	return response.Tombstones, err
}

func skillPath(skillID string) string { return "/v2/skills/" + url.PathEscape(skillID) }
func skillVersionPath(skillID string, version int32) string {
	return skillPath(skillID) + "/versions/" + strconv.FormatInt(int64(version), 10)
}
func retentionPolicyPath(policyID string) string {
	return "/v2/skill-asset-retention/policies/" + url.PathEscape(policyID)
}
func withQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}
func assetGCListValues(query SkillAssetGCListQuery) url.Values {
	values := url.Values{}
	if query.WorkspaceID != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(query.Limit), 10))
	}
	return values
}
