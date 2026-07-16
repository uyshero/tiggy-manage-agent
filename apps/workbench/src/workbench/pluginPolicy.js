function normalizedList(value) {
  if (value === undefined || value === null) return Object.freeze([]);
  if (!Array.isArray(value)) throw new TypeError("Plugin enablement values must be arrays.");
  return Object.freeze([...new Set(value.map((item) => typeof item === "string" ? item.trim() : "").filter(Boolean))]);
}

export function definePluginEnablement(input = {}) {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new TypeError("Plugin enablement must be an object.");
  const unknown = Object.keys(input).find((key) => ![
    "defaultEnabled", "organizations", "workspaces", "roles", "excludedOrganizations", "excludedWorkspaces", "excludedRoles"
  ].includes(key));
  if (unknown) throw new TypeError(`Plugin enablement field ${unknown} is not supported.`);
  if (input.defaultEnabled !== undefined && typeof input.defaultEnabled !== "boolean") {
    throw new TypeError("Plugin enablement defaultEnabled must be a boolean.");
  }
  return Object.freeze({
    defaultEnabled: input.defaultEnabled !== false,
    organizations: normalizedList(input.organizations),
    workspaces: normalizedList(input.workspaces),
    roles: normalizedList(input.roles),
    excludedOrganizations: normalizedList(input.excludedOrganizations),
    excludedWorkspaces: normalizedList(input.excludedWorkspaces),
    excludedRoles: normalizedList(input.excludedRoles)
  });
}

export function evaluatePluginEnablement(input, scope = {}) {
  const policy = definePluginEnablement(input);
  const organizationID = String(scope.organizationId || "").trim();
  const workspaceID = String(scope.workspaceId || "").trim();
  const roles = new Set(normalizedList(scope.roles));
  const reasons = [];
  const hasAllowlist = Boolean(policy.organizations.length || policy.workspaces.length || policy.roles.length);

  if (!policy.defaultEnabled && !hasAllowlist) reasons.push("disabled_by_default");
  if (policy.organizations.length && !policy.organizations.includes(organizationID)) reasons.push("organization_not_enabled");
  if (policy.workspaces.length && !policy.workspaces.includes(workspaceID)) reasons.push("workspace_not_enabled");
  if (policy.roles.length && !policy.roles.some((role) => roles.has(role))) reasons.push("role_not_enabled");
  if (organizationID && policy.excludedOrganizations.includes(organizationID)) reasons.push("organization_excluded");
  if (workspaceID && policy.excludedWorkspaces.includes(workspaceID)) reasons.push("workspace_excluded");
  if (policy.excludedRoles.some((role) => roles.has(role))) reasons.push("role_excluded");

  return Object.freeze({ enabled: reasons.length === 0, policy, reasons: Object.freeze(reasons) });
}
