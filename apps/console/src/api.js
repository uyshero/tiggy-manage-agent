import { TMAClient } from "@tma/core-sdk";
import { createAuthenticatedFetch } from "./auth.js";

export function createConsoleAPI(options = {}) {
  const configuredBaseURL = import.meta.env?.VITE_TMA_API_BASE_URL;
  const baseURL = options.baseURL || configuredBaseURL || globalThis.location?.origin || "http://localhost";
  const fetch = options.fetch || createAuthenticatedFetch({ baseURL });
  const service = new TMAClient(baseURL, { fetch }).tenantAdministration;
  return {
    getContext: () => service.context(),
    listMembers: () => service.listCurrentWorkspaceMembers(),
    saveMember: (subject, member) => service.upsertCurrentWorkspaceMember(subject, member),
    removeMember: (subject) => service.deleteCurrentWorkspaceMember(subject),
    listWorkspaces: () => service.listTenantWorkspaces(),
    createWorkspace: (name) => service.createTenantWorkspace({ name }),
    listWorkspaceMembersAsPlatform: (workspaceID) => service.listTenantWorkspaceMembers(workspaceID),
    saveWorkspaceMemberAsPlatform: (workspaceID, subject, member) => service.upsertTenantWorkspaceMember(workspaceID, subject, member),
    removeWorkspaceMemberAsPlatform: (workspaceID, subject) => service.deleteTenantWorkspaceMember(workspaceID, subject),
    listPlatformAdmins: () => service.listPlatformAdmins(),
    savePlatformAdmin: (subject, admin) => service.upsertPlatformAdmin(subject, admin),
    removePlatformAdmin: (subject) => service.deletePlatformAdmin(subject),
  };
}

const api = createConsoleAPI();
export const getContext = api.getContext;
export const listMembers = api.listMembers;
export const saveMember = api.saveMember;
export const removeMember = api.removeMember;
export const listWorkspaces = api.listWorkspaces;
export const createWorkspace = api.createWorkspace;
export const listWorkspaceMembersAsPlatform = api.listWorkspaceMembersAsPlatform;
export const saveWorkspaceMemberAsPlatform = api.saveWorkspaceMemberAsPlatform;
export const removeWorkspaceMemberAsPlatform = api.removeWorkspaceMemberAsPlatform;
export const listPlatformAdmins = api.listPlatformAdmins;
export const savePlatformAdmin = api.savePlatformAdmin;
export const removePlatformAdmin = api.removePlatformAdmin;
