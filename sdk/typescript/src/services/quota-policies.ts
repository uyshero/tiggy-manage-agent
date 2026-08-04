import type { ModelRuntimeQuotaPolicy, ModelRuntimeQuotaStatus, PutModelRuntimeQuotaPolicyRequest } from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class QuotaPoliciesService extends ServiceBase {
  list(includeArchived = false, signal?: AbortSignal): Promise<ModelRuntimeQuotaPolicy[]> {
    const path = withQuery("/v2/quota-policies", { include_archived: includeArchived || undefined });
    return this.transport.requestJSON<{ policies: ModelRuntimeQuotaPolicy[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.policies);
  }

  putWorkspace(request: PutModelRuntimeQuotaPolicyRequest, expectedRevision?: number, signal?: AbortSignal): Promise<ModelRuntimeQuotaPolicy> {
    return this.put("/v2/quota-policies/workspace", request, expectedRevision, signal);
  }

  putApplication(appId: string, request: PutModelRuntimeQuotaPolicyRequest, expectedRevision?: number, signal?: AbortSignal): Promise<ModelRuntimeQuotaPolicy> {
    return this.put(resourcePath("/v2/quota-policies/applications", appId), request, expectedRevision, signal);
  }

  archiveApplication(appId: string, expectedRevision: number, signal?: AbortSignal): Promise<ModelRuntimeQuotaPolicy> {
    return this.transport.requestJSON("DELETE", resourcePath("/v2/quota-policies/applications", appId), undefined, {
      headers: { "If-Match": `"${expectedRevision}"` },
      ...(signal === undefined ? {} : { signal }),
    });
  }

  effective(appId?: string, signal?: AbortSignal): Promise<ModelRuntimeQuotaStatus> {
    return this.transport.requestJSON("GET", withQuery("/v2/quota-policies/effective", { app_id: appId }), undefined, signal ? { signal } : {});
  }

  private put(path: string, request: PutModelRuntimeQuotaPolicyRequest, expectedRevision?: number, signal?: AbortSignal): Promise<ModelRuntimeQuotaPolicy> {
    return this.transport.requestJSON("PUT", path, request, {
      ...(expectedRevision === undefined ? {} : { headers: { "If-Match": `"${expectedRevision}"` } }),
      ...(signal === undefined ? {} : { signal }),
    });
  }
}
