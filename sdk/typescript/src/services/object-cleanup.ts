import type {
  ObjectCleanupJob,
  ObjectCleanupListQuery,
  ObjectCleanupStats,
  ObjectReconciliationArtifactExport,
  ObjectReconciliationArtifactRequest,
  ObjectReconciliationPreviewRequest,
  ObjectReconciliationReport,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class ObjectCleanupService extends ServiceBase {
  previewReconciliation(input: ObjectReconciliationPreviewRequest, signal?: AbortSignal): Promise<ObjectReconciliationReport> {
    return this.transport.requestJSON("POST", "/v2/object-cleanup/reconciliation/preview", input, signal ? { signal } : {});
  }

  exportReconciliationArtifact(input: ObjectReconciliationArtifactRequest, signal?: AbortSignal): Promise<ObjectReconciliationArtifactExport> {
    return this.transport.requestJSON("POST", "/v2/object-cleanup/reconciliation/artifacts", input, signal ? { signal } : {});
  }

  list(query: ObjectCleanupListQuery = {}, signal?: AbortSignal): Promise<ObjectCleanupJob[]> {
    const path = withQuery("/v2/object-cleanup/jobs", {
      workspace_id: query.workspaceId,
      status: query.status,
      reason: query.reason,
      created_from: dateQueryValue(query.createdFrom),
      created_to: dateQueryValue(query.createdTo),
      limit: query.limit,
    });
    return this.transport.requestJSON<{ jobs: ObjectCleanupJob[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.jobs);
  }

  stats(workspaceId?: string, signal?: AbortSignal): Promise<ObjectCleanupStats> {
    const path = withQuery("/v2/object-cleanup/stats", { workspace_id: workspaceId });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  retry(jobId: string, workspaceId?: string, signal?: AbortSignal): Promise<ObjectCleanupJob> {
    const path = withQuery(`${objectCleanupJobPath(jobId)}/retry`, { workspace_id: workspaceId });
    return this.transport.requestJSON("POST", path, undefined, signal ? { signal } : {});
  }

  approve(jobId: string, confirm: string, workspaceId?: string, signal?: AbortSignal): Promise<ObjectCleanupJob> {
    const path = withQuery(`${objectCleanupJobPath(jobId)}/approve`, { workspace_id: workspaceId });
    return this.transport.requestJSON("POST", path, { confirm }, signal ? { signal } : {});
  }
}

function objectCleanupJobPath(jobId: string): string {
  return resourcePath("/v2/object-cleanup/jobs", jobId);
}

function dateQueryValue(value?: Date | string): string | undefined {
  if (!value) return undefined;
  return value instanceof Date ? value.toISOString() : value;
}
