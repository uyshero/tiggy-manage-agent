import type {
  CreateMarketplaceEntryRequest,
  CreateMarketplacePolicyRequest,
  MarketplaceDisableRequest,
  MarketplaceDisableResult,
  MarketplaceDiscoverQuery,
  MarketplaceDiscoverResult,
  MarketplaceEnableRequest,
  MarketplaceEnableResult,
  MarketplaceEntry,
  MarketplaceEntryQuery,
  MarketplaceInstallRequest,
  MarketplaceInstallResult,
  MarketplaceInternalQuery,
  MarketplaceInternalResult,
  MarketplacePolicy,
  MarketplacePolicyQuery,
  MarketplacePolicyResult,
  MarketplacePolicyVersion,
  MarketplacePreviewRequest,
  MarketplacePreviewResult,
  MarketplaceTransitionRequest,
  PublishMarketplacePolicyRequest,
  UpdateMarketplaceEntryRequest,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import { skillPath } from "./skills.js";

export class MarketplaceService extends ServiceBase {
  discover(query: MarketplaceDiscoverQuery, signal?: AbortSignal): Promise<MarketplaceDiscoverResult> {
    const path = withQuery("/v2/skills/marketplace/discover", { session_id: query.sessionId, query: query.query, repository: query.repository, limit: query.limit });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  preview(request: MarketplacePreviewRequest, signal?: AbortSignal): Promise<MarketplacePreviewResult> {
    return this.transport.requestJSON("POST", "/v2/skills/marketplace/preview", request, signal ? { signal } : {});
  }

  install(request: MarketplaceInstallRequest, signal?: AbortSignal): Promise<MarketplaceInstallResult> {
    return this.transport.requestJSON("POST", "/v2/skills/marketplace/install", request, signal ? { signal } : {});
  }

  browseInternal(query: MarketplaceInternalQuery, signal?: AbortSignal): Promise<MarketplaceInternalResult> {
    const values = new URLSearchParams();
    values.set("session_id", query.sessionId);
    if (query.query) values.set("query", query.query);
    if (query.category) values.set("category", query.category);
    for (const tag of query.tags ?? []) values.append("tag", tag);
    if (query.limit) values.set("limit", String(query.limit));
    return this.transport.requestJSON("GET", `/v2/skills/marketplace/internal?${values.toString()}`, undefined, signal ? { signal } : {});
  }

  previewInternal(request: MarketplacePreviewRequest, signal?: AbortSignal): Promise<MarketplacePreviewResult> {
    return this.transport.requestJSON("POST", "/v2/skills/marketplace/internal/preview", request, signal ? { signal } : {});
  }

  installInternal(request: MarketplaceInstallRequest, signal?: AbortSignal): Promise<MarketplaceInstallResult> {
    return this.transport.requestJSON("POST", "/v2/skills/marketplace/internal/install", request, signal ? { signal } : {});
  }

  enableInstalled(skillId: string, request: MarketplaceEnableRequest, signal?: AbortSignal): Promise<MarketplaceEnableResult> {
    return this.transport.requestJSON("POST", `${skillPath(skillId)}/enable`, request, signal ? { signal } : {});
  }

  disableInstalled(skillId: string, request: MarketplaceDisableRequest, signal?: AbortSignal): Promise<MarketplaceDisableResult> {
    return this.transport.requestJSON("POST", `${skillPath(skillId)}/disable`, request, signal ? { signal } : {});
  }

  createEntry(request: CreateMarketplaceEntryRequest, signal?: AbortSignal): Promise<MarketplaceEntry> {
    return this.transport.requestJSON("POST", "/v2/skill-marketplace-entries", request, signal ? { signal } : {});
  }

  listEntries(query: MarketplaceEntryQuery = {}, signal?: AbortSignal): Promise<MarketplaceEntry[]> {
    const path = withQuery("/v2/skill-marketplace-entries", { workspace_id: query.workspaceId, status: query.status, include_withdrawn: query.includeWithdrawn || undefined });
    return this.transport.requestJSON<{ entries: MarketplaceEntry[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.entries);
  }

  getEntry(entryId: string, workspaceId?: string, signal?: AbortSignal): Promise<MarketplaceEntry> {
    return this.transport.requestJSON("GET", withQuery(entryPath(entryId), { workspace_id: workspaceId }), undefined, signal ? { signal } : {});
  }

  updateEntry(entryId: string, request: UpdateMarketplaceEntryRequest, signal?: AbortSignal): Promise<MarketplaceEntry> {
    return this.transport.requestJSON("PATCH", entryPath(entryId), request, signal ? { signal } : {});
  }

  submitEntry(entryId: string, request: MarketplaceTransitionRequest = {}, signal?: AbortSignal): Promise<MarketplaceEntry> { return this.transitionEntry(entryId, "submit", request, signal); }
  publishEntry(entryId: string, request: MarketplaceTransitionRequest = {}, signal?: AbortSignal): Promise<MarketplaceEntry> { return this.transitionEntry(entryId, "publish", request, signal); }
  withdrawEntry(entryId: string, request: MarketplaceTransitionRequest = {}, signal?: AbortSignal): Promise<MarketplaceEntry> { return this.transitionEntry(entryId, "withdraw", request, signal); }

  createPolicy(request: CreateMarketplacePolicyRequest, signal?: AbortSignal): Promise<MarketplacePolicyResult> {
    return this.transport.requestJSON("POST", "/v2/skill-marketplace-policies", request, signal ? { signal } : {});
  }

  listPolicies(query: MarketplacePolicyQuery = {}, signal?: AbortSignal): Promise<MarketplacePolicy[]> {
    const path = withQuery("/v2/skill-marketplace-policies", { organization_id: query.organizationId, workspace_id: query.workspaceId, include_archived: query.includeArchived || undefined });
    return this.transport.requestJSON<{ policies: MarketplacePolicy[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.policies);
  }

  getPolicy(policyId: string, signal?: AbortSignal): Promise<MarketplacePolicyResult> {
    return this.transport.requestJSON("GET", policyPath(policyId), undefined, signal ? { signal } : {});
  }

  publishPolicyVersion(policyId: string, request: PublishMarketplacePolicyRequest, signal?: AbortSignal): Promise<MarketplacePolicyVersion> {
    return this.transport.requestJSON("POST", `${policyPath(policyId)}/versions`, request, signal ? { signal } : {});
  }

  getPolicyVersion(policyId: string, version: number, signal?: AbortSignal): Promise<MarketplacePolicyVersion> {
    return this.transport.requestJSON("GET", resourcePath(`${policyPath(policyId)}/versions`, version), undefined, signal ? { signal } : {});
  }

  archivePolicy(policyId: string, signal?: AbortSignal): Promise<MarketplacePolicy> {
    return this.transport.requestJSON("POST", `${policyPath(policyId)}/archive`, undefined, signal ? { signal } : {});
  }

  private transitionEntry(entryId: string, action: string, request: MarketplaceTransitionRequest, signal?: AbortSignal): Promise<MarketplaceEntry> {
    return this.transport.requestJSON("POST", `${entryPath(entryId)}/${action}`, request, signal ? { signal } : {});
  }
}

function entryPath(entryId: string): string { return resourcePath("/v2/skill-marketplace-entries", entryId); }
function policyPath(policyId: string): string { return resourcePath("/v2/skill-marketplace-policies", policyId); }
