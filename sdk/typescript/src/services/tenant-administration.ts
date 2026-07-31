import type {
  AdministrationContext,
  CreateTenantWorkspaceRequest,
  PlatformRoleAssignment,
  TenantWorkspace,
  UpsertPlatformAdminRequest,
  UpsertWorkspaceMembershipRequest,
  WorkspaceMembership,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export class TenantAdministrationService extends ServiceBase {
  context(signal?: AbortSignal): Promise<AdministrationContext> {
    return this.transport.requestJSON("GET", "/v2/administration/context", undefined, signal ? { signal } : {});
  }

  listCurrentWorkspaceMembers(signal?: AbortSignal): Promise<WorkspaceMembership[]> {
    return this.transport.requestJSON<{ members: WorkspaceMembership[] }>("GET", "/v2/workspace/members", undefined, signal ? { signal } : {}).then((value) => value.members);
  }

  upsertCurrentWorkspaceMember(subject: string, request: UpsertWorkspaceMembershipRequest, signal?: AbortSignal): Promise<WorkspaceMembership> {
    return this.transport.requestJSON("PUT", resourcePath("/v2/workspace/members", subject), request, signal ? { signal } : {});
  }

  deleteCurrentWorkspaceMember(subject: string, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", resourcePath("/v2/workspace/members", subject), undefined, signal ? { signal } : {});
  }

  listTenantWorkspaces(signal?: AbortSignal): Promise<TenantWorkspace[]> {
    return this.transport.requestJSON<{ workspaces: TenantWorkspace[] }>("GET", "/v2/platform/workspaces", undefined, signal ? { signal } : {}).then((value) => value.workspaces);
  }

  createTenantWorkspace(request: CreateTenantWorkspaceRequest, signal?: AbortSignal): Promise<TenantWorkspace> {
    return this.transport.requestJSON("POST", "/v2/platform/workspaces", request, signal ? { signal } : {});
  }

  listTenantWorkspaceMembers(workspaceId: string, signal?: AbortSignal): Promise<WorkspaceMembership[]> {
    return this.transport.requestJSON<{ members: WorkspaceMembership[] }>("GET", tenantWorkspaceMembersPath(workspaceId), undefined, signal ? { signal } : {}).then((value) => value.members);
  }

  upsertTenantWorkspaceMember(workspaceId: string, subject: string, request: UpsertWorkspaceMembershipRequest, signal?: AbortSignal): Promise<WorkspaceMembership> {
    return this.transport.requestJSON("PUT", resourcePath(tenantWorkspaceMembersPath(workspaceId), subject), request, signal ? { signal } : {});
  }

  deleteTenantWorkspaceMember(workspaceId: string, subject: string, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", resourcePath(tenantWorkspaceMembersPath(workspaceId), subject), undefined, signal ? { signal } : {});
  }

  listPlatformAdmins(signal?: AbortSignal): Promise<PlatformRoleAssignment[]> {
    return this.transport.requestJSON<{ admins: PlatformRoleAssignment[] }>("GET", "/v2/platform/admins", undefined, signal ? { signal } : {}).then((value) => value.admins);
  }

  upsertPlatformAdmin(subject: string, request: UpsertPlatformAdminRequest, signal?: AbortSignal): Promise<PlatformRoleAssignment> {
    return this.transport.requestJSON("PUT", resourcePath("/v2/platform/admins", subject), request, signal ? { signal } : {});
  }

  deletePlatformAdmin(subject: string, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", resourcePath("/v2/platform/admins", subject), undefined, signal ? { signal } : {});
  }
}

function tenantWorkspaceMembersPath(workspaceId: string): string {
  return resourcePath("/v2/platform/workspaces", workspaceId) + "/members";
}
