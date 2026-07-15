import type {
  CreateSkillRequest,
  CreateSkillRetentionPolicyRequest,
  CreateSkillVersionRequest,
  EffectiveSkillRetentionPolicy,
  PublishSkillRetentionPolicyRequest,
  ResolveSkillsPreviewRequest,
  ResolveSkillsResult,
  Skill,
  SkillAssetGCListQuery,
  SkillAssetGCPreview,
  SkillAssetGCRequest,
  SkillAssetGCRun,
  SkillAssetGCRunResult,
  SkillAssetGCTombstone,
  SkillListQuery,
  SkillPackageBackfillRequest,
  SkillPackageBackfillResult,
  SkillRetentionPolicy,
  SkillRetentionPolicyQuery,
  SkillRetentionPolicyResult,
  SkillRetentionPolicyVersion,
  SkillUsage,
  SkillVersion,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import { sessionPath } from "./sessions.js";

export class SkillsService extends ServiceBase {
  create(request: CreateSkillRequest, signal?: AbortSignal): Promise<Skill> {
    return this.transport.requestJSON("POST", "/v2/skills", request, signal ? { signal } : {});
  }

  list(query: SkillListQuery = {}, signal?: AbortSignal): Promise<Skill[]> {
    const path = withQuery("/v2/skills", { workspace_id: query.workspaceId, include_archived: query.includeArchived || undefined });
    return this.transport.requestJSON<{ skills: Skill[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.skills);
  }

  get(skillId: string, signal?: AbortSignal): Promise<Skill> {
    return this.transport.requestJSON("GET", skillPath(skillId), undefined, signal ? { signal } : {});
  }

  archive(skillId: string, signal?: AbortSignal): Promise<Skill> {
    return this.transport.requestJSON("POST", `${skillPath(skillId)}/archive`, undefined, signal ? { signal } : {});
  }

  createVersion(skillId: string, request: CreateSkillVersionRequest, signal?: AbortSignal): Promise<SkillVersion> {
    return this.transport.requestJSON("POST", `${skillPath(skillId)}/versions`, request, signal ? { signal } : {});
  }

  listVersions(skillId: string, signal?: AbortSignal): Promise<SkillVersion[]> {
    return this.transport.requestJSON<{ versions: SkillVersion[] }>("GET", `${skillPath(skillId)}/versions`, undefined, signal ? { signal } : {}).then((value) => value.versions);
  }

  getVersion(skillId: string, version: number, signal?: AbortSignal): Promise<SkillVersion> {
    return this.transport.requestJSON("GET", skillVersionPath(skillId, version), undefined, signal ? { signal } : {});
  }

  downloadPackage(skillId: string, version: number, signal?: AbortSignal): Promise<Response> {
    return this.transport.request("GET", `${skillVersionPath(skillId, version)}/package`, signal ? { signal } : {});
  }

  resolvePreview(request: ResolveSkillsPreviewRequest, signal?: AbortSignal): Promise<ResolveSkillsResult> {
    return this.transport.requestJSON("POST", "/v2/skills/resolve-preview", request, signal ? { signal } : {});
  }

  listUsages(sessionId: string, turnId?: string, signal?: AbortSignal): Promise<SkillUsage[]> {
    const path = withQuery(`${sessionPath(sessionId)}/skill-usages`, { turn_id: turnId });
    return this.transport.requestJSON<{ skill_usages: SkillUsage[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.skill_usages);
  }

  backfillPackages(request: SkillPackageBackfillRequest = {}, signal?: AbortSignal): Promise<SkillPackageBackfillResult> {
    return this.transport.requestJSON("POST", "/v2/skill-packages/backfill", request, signal ? { signal } : {});
  }

  effectiveRetentionPolicy(workspaceId?: string, signal?: AbortSignal): Promise<EffectiveSkillRetentionPolicy> {
    const path = withQuery("/v2/skill-asset-retention/effective", { workspace_id: workspaceId });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  createRetentionPolicy(request: CreateSkillRetentionPolicyRequest, signal?: AbortSignal): Promise<SkillRetentionPolicyResult> {
    return this.transport.requestJSON("POST", "/v2/skill-asset-retention/policies", request, signal ? { signal } : {});
  }

  listRetentionPolicies(query: SkillRetentionPolicyQuery = {}, signal?: AbortSignal): Promise<SkillRetentionPolicy[]> {
    const path = withQuery("/v2/skill-asset-retention/policies", {
      organization_id: query.organizationId,
      workspace_id: query.workspaceId,
      include_archived: query.includeArchived || undefined,
    });
    return this.transport.requestJSON<{ policies: SkillRetentionPolicy[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.policies);
  }

  getRetentionPolicy(policyId: string, signal?: AbortSignal): Promise<SkillRetentionPolicyResult> {
    return this.transport.requestJSON("GET", retentionPolicyPath(policyId), undefined, signal ? { signal } : {});
  }

  publishRetentionPolicyVersion(policyId: string, request: PublishSkillRetentionPolicyRequest, signal?: AbortSignal): Promise<SkillRetentionPolicyVersion> {
    return this.transport.requestJSON("POST", `${retentionPolicyPath(policyId)}/versions`, request, signal ? { signal } : {});
  }

  getRetentionPolicyVersion(policyId: string, version: number, signal?: AbortSignal): Promise<SkillRetentionPolicyVersion> {
    return this.transport.requestJSON("GET", resourcePath(`${retentionPolicyPath(policyId)}/versions`, version), undefined, signal ? { signal } : {});
  }

  archiveRetentionPolicy(policyId: string, signal?: AbortSignal): Promise<SkillRetentionPolicy> {
    return this.transport.requestJSON("POST", `${retentionPolicyPath(policyId)}/archive`, undefined, signal ? { signal } : {});
  }

  previewAssetGC(request: SkillAssetGCRequest, signal?: AbortSignal): Promise<SkillAssetGCPreview> {
    return this.transport.requestJSON("POST", "/v2/skill-asset-gc/preview", request, signal ? { signal } : {});
  }

  runAssetGC(request: SkillAssetGCRequest, signal?: AbortSignal): Promise<SkillAssetGCRunResult> {
    return this.transport.requestJSON("POST", "/v2/skill-asset-gc/run", request, signal ? { signal } : {});
  }

  listAssetGCRuns(query: SkillAssetGCListQuery = {}, signal?: AbortSignal): Promise<SkillAssetGCRun[]> {
    return this.transport.requestJSON<{ runs: SkillAssetGCRun[] }>("GET", assetGCListPath("/v2/skill-asset-gc/runs", query), undefined, signal ? { signal } : {}).then((value) => value.runs);
  }

  getAssetGCRun(runId: string, signal?: AbortSignal): Promise<SkillAssetGCRunResult> {
    return this.transport.requestJSON("GET", resourcePath("/v2/skill-asset-gc/runs", runId), undefined, signal ? { signal } : {});
  }

  listAssetGCTombstones(query: SkillAssetGCListQuery = {}, signal?: AbortSignal): Promise<SkillAssetGCTombstone[]> {
    return this.transport.requestJSON<{ tombstones: SkillAssetGCTombstone[] }>("GET", assetGCListPath("/v2/skill-asset-gc/tombstones", query), undefined, signal ? { signal } : {}).then((value) => value.tombstones);
  }
}

export function skillPath(skillId: string): string { return resourcePath("/v2/skills", skillId); }
function skillVersionPath(skillId: string, version: number): string { return resourcePath(`${skillPath(skillId)}/versions`, version); }
function retentionPolicyPath(policyId: string): string { return resourcePath("/v2/skill-asset-retention/policies", policyId); }
function assetGCListPath(path: string, query: SkillAssetGCListQuery): string { return withQuery(path, { workspace_id: query.workspaceId, limit: query.limit }); }
