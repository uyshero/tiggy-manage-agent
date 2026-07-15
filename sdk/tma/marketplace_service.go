package tma

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type MarketplaceService struct{ client *Client }

func (s *MarketplaceService) Discover(ctx context.Context, query MarketplaceDiscoverQuery) (MarketplaceDiscoverResult, error) {
	values := url.Values{}
	values.Set("session_id", query.SessionID)
	if query.Query != "" {
		values.Set("query", query.Query)
	}
	if query.Repository != "" {
		values.Set("repository", query.Repository)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(query.Limit), 10))
	}
	var result MarketplaceDiscoverResult
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skills/marketplace/discover", values), nil, &result)
	return result, err
}

func (s *MarketplaceService) Preview(ctx context.Context, request MarketplacePreviewRequest) (MarketplacePreviewResult, error) {
	var result MarketplacePreviewResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skills/marketplace/preview", request, &result)
	return result, err
}

func (s *MarketplaceService) Install(ctx context.Context, request MarketplaceInstallRequest) (MarketplaceInstallResult, error) {
	var result MarketplaceInstallResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skills/marketplace/install", request, &result)
	return result, err
}

func (s *MarketplaceService) BrowseInternal(ctx context.Context, query MarketplaceInternalQuery) (MarketplaceInternalResult, error) {
	values := url.Values{}
	values.Set("session_id", query.SessionID)
	if query.Query != "" {
		values.Set("query", query.Query)
	}
	if query.Category != "" {
		values.Set("category", query.Category)
	}
	for _, tag := range query.Tags {
		values.Add("tag", tag)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(query.Limit), 10))
	}
	var result MarketplaceInternalResult
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skills/marketplace/internal", values), nil, &result)
	return result, err
}

func (s *MarketplaceService) PreviewInternal(ctx context.Context, request MarketplacePreviewRequest) (MarketplacePreviewResult, error) {
	var result MarketplacePreviewResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skills/marketplace/internal/preview", request, &result)
	return result, err
}

func (s *MarketplaceService) InstallInternal(ctx context.Context, request MarketplaceInstallRequest) (MarketplaceInstallResult, error) {
	var result MarketplaceInstallResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skills/marketplace/internal/install", request, &result)
	return result, err
}

func (s *MarketplaceService) EnableInstalled(ctx context.Context, skillID string, request MarketplaceEnableRequest) (MarketplaceEnableResult, error) {
	var result MarketplaceEnableResult
	err := s.client.DoJSON(ctx, http.MethodPost, skillPath(skillID)+"/enable", request, &result)
	return result, err
}

func (s *MarketplaceService) DisableInstalled(ctx context.Context, skillID string, request MarketplaceDisableRequest) (MarketplaceDisableResult, error) {
	var result MarketplaceDisableResult
	err := s.client.DoJSON(ctx, http.MethodPost, skillPath(skillID)+"/disable", request, &result)
	return result, err
}

func (s *MarketplaceService) CreateEntry(ctx context.Context, request CreateMarketplaceEntryRequest) (MarketplaceEntry, error) {
	var entry MarketplaceEntry
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skill-marketplace-entries", request, &entry)
	return entry, err
}

func (s *MarketplaceService) ListEntries(ctx context.Context, query MarketplaceEntryQuery) ([]MarketplaceEntry, error) {
	values := url.Values{}
	if query.WorkspaceID != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if query.Status != "" {
		values.Set("status", query.Status)
	}
	if query.IncludeWithdrawn {
		values.Set("include_withdrawn", "true")
	}
	var response struct {
		Entries []MarketplaceEntry `json:"entries"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skill-marketplace-entries", values), nil, &response)
	return response.Entries, err
}

func (s *MarketplaceService) GetEntry(ctx context.Context, entryID string, workspaceID string) (MarketplaceEntry, error) {
	values := url.Values{}
	if workspaceID != "" {
		values.Set("workspace_id", workspaceID)
	}
	var entry MarketplaceEntry
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery(marketplaceEntryPath(entryID), values), nil, &entry)
	return entry, err
}

func (s *MarketplaceService) UpdateEntry(ctx context.Context, entryID string, request UpdateMarketplaceEntryRequest) (MarketplaceEntry, error) {
	var entry MarketplaceEntry
	err := s.client.DoJSON(ctx, http.MethodPatch, marketplaceEntryPath(entryID), request, &entry)
	return entry, err
}

func (s *MarketplaceService) SubmitEntry(ctx context.Context, entryID string, request MarketplaceTransitionRequest) (MarketplaceEntry, error) {
	return s.transitionEntry(ctx, entryID, "submit", request)
}

func (s *MarketplaceService) PublishEntry(ctx context.Context, entryID string, request MarketplaceTransitionRequest) (MarketplaceEntry, error) {
	return s.transitionEntry(ctx, entryID, "publish", request)
}

func (s *MarketplaceService) WithdrawEntry(ctx context.Context, entryID string, request MarketplaceTransitionRequest) (MarketplaceEntry, error) {
	return s.transitionEntry(ctx, entryID, "withdraw", request)
}

func (s *MarketplaceService) transitionEntry(ctx context.Context, entryID string, action string, request MarketplaceTransitionRequest) (MarketplaceEntry, error) {
	var entry MarketplaceEntry
	err := s.client.DoJSON(ctx, http.MethodPost, marketplaceEntryPath(entryID)+"/"+action, request, &entry)
	return entry, err
}

func (s *MarketplaceService) CreatePolicy(ctx context.Context, request CreateMarketplacePolicyRequest) (MarketplacePolicyResult, error) {
	var result MarketplacePolicyResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/skill-marketplace-policies", request, &result)
	return result, err
}

func (s *MarketplaceService) ListPolicies(ctx context.Context, query MarketplacePolicyQuery) ([]MarketplacePolicy, error) {
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
		Policies []MarketplacePolicy `json:"policies"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, withQuery("/v2/skill-marketplace-policies", values), nil, &response)
	return response.Policies, err
}

func (s *MarketplaceService) GetPolicy(ctx context.Context, policyID string) (MarketplacePolicyResult, error) {
	var result MarketplacePolicyResult
	err := s.client.DoJSON(ctx, http.MethodGet, marketplacePolicyPath(policyID), nil, &result)
	return result, err
}

func (s *MarketplaceService) PublishPolicyVersion(ctx context.Context, policyID string, request PublishMarketplacePolicyRequest) (MarketplacePolicyVersion, error) {
	var version MarketplacePolicyVersion
	err := s.client.DoJSON(ctx, http.MethodPost, marketplacePolicyPath(policyID)+"/versions", request, &version)
	return version, err
}

func (s *MarketplaceService) GetPolicyVersion(ctx context.Context, policyID string, version int32) (MarketplacePolicyVersion, error) {
	var result MarketplacePolicyVersion
	path := marketplacePolicyPath(policyID) + "/versions/" + strconv.FormatInt(int64(version), 10)
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (s *MarketplaceService) ArchivePolicy(ctx context.Context, policyID string) (MarketplacePolicy, error) {
	var policy MarketplacePolicy
	err := s.client.DoJSON(ctx, http.MethodPost, marketplacePolicyPath(policyID)+"/archive", nil, &policy)
	return policy, err
}

func marketplaceEntryPath(entryID string) string {
	return "/v2/skill-marketplace-entries/" + url.PathEscape(entryID)
}

func marketplacePolicyPath(policyID string) string {
	return "/v2/skill-marketplace-policies/" + url.PathEscape(policyID)
}
