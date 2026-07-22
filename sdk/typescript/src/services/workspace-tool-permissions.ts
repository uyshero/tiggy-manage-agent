import type {
  EvaluateWorkspaceToolPermissionRequest,
  EvaluateWorkspaceToolPermissionResult,
  UpdateWorkspaceToolPermissionPolicyRequest,
  WorkspaceToolPermissionPolicy,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export class WorkspaceToolPermissionsService extends ServiceBase {
  get(workspaceId: string, signal?: AbortSignal): Promise<WorkspaceToolPermissionPolicy> {
    return this.transport.requestJSON("GET", policyPath(workspaceId), undefined, signal ? { signal } : {});
  }

  update(workspaceId: string, expectedRevision: number, request: UpdateWorkspaceToolPermissionPolicyRequest, signal?: AbortSignal): Promise<WorkspaceToolPermissionPolicy> {
    return this.transport.requestJSON("PUT", policyPath(workspaceId), request, {
      headers: { "If-Match": `"${expectedRevision}"` },
      ...(signal === undefined ? {} : { signal }),
    });
  }

  evaluate(workspaceId: string, request: EvaluateWorkspaceToolPermissionRequest, signal?: AbortSignal): Promise<EvaluateWorkspaceToolPermissionResult> {
    return this.transport.requestJSON("POST", policyPath(workspaceId) + "/evaluate", request, signal ? { signal } : {});
  }
}

function policyPath(workspaceId: string): string {
  return resourcePath("/v2/workspaces", workspaceId) + "/tool-permissions";
}
