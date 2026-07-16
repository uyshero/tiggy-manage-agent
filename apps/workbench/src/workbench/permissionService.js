export class PermissionDeniedError extends Error {
  constructor(permissions) {
    const denied = Object.freeze([...permissions]);
    super(`Missing required permissions: ${denied.join(", ")}`);
    this.name = "PermissionDeniedError";
    this.code = "permission_denied";
    this.permissions = denied;
  }
}

function permissionList(value) {
  if (value === undefined || value === null) return [];
  const values = Array.isArray(value) ? value : [value];
  return [...new Set(values.map((item) => typeof item === "string" ? item.trim() : "").filter(Boolean))];
}

export function createPermissionService(options = {}) {
  const grants = new Set(permissionList(options.grants));

  async function has(value) {
    const required = permissionList(value);
    return required.every((permission) => grants.has(permission));
  }

  async function requirePermissions(value) {
    const required = permissionList(value);
    const denied = required.filter((permission) => !grants.has(permission));
    if (denied.length) throw new PermissionDeniedError(denied);
    return true;
  }

  return Object.freeze({ has, require: requirePermissions });
}
