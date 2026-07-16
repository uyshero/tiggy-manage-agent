const identifierPattern = /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)+$/;
const semverPattern = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;

export const RESOURCE_REF_TYPES = Object.freeze([
  "file",
  "artifact",
  "task",
  "session",
  "url",
  "business_object"
]);

export const COMMAND_RISKS = Object.freeze([
  "read",
  "write",
  "exec",
  "external_effect"
]);

export const PLUGIN_CONTEXT_SERVICE_METHODS = Object.freeze({
  permissions: Object.freeze(["has", "require"]),
  commands: Object.freeze(["register", "execute"]),
  dialog: Object.freeze(["confirm", "form", "choice", "open"]),
  notifications: Object.freeze(["show"]),
  resources: Object.freeze(["listRelated", "preview", "open"]),
  tasks: Object.freeze(["list"]),
  artifacts: Object.freeze(["list"]),
  http: Object.freeze(["request"])
});

export class WorkbenchContractError extends TypeError {
  constructor(field, message) {
    super(`${field}: ${message}`);
    this.name = "WorkbenchContractError";
    this.field = field;
  }
}

function isPlainObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function requireObject(value, field) {
  if (!isPlainObject(value)) {
    throw new WorkbenchContractError(field, "must be a plain object");
  }
  return value;
}

function requireString(value, field) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) {
    throw new WorkbenchContractError(field, "must be a non-empty string");
  }
  return normalized;
}

function optionalString(value, field) {
  if (value === undefined || value === null || value === "") return undefined;
  return requireString(value, field);
}

function requireIdentifier(value, field) {
  const normalized = requireString(value, field);
  if (!identifierPattern.test(normalized)) {
    throw new WorkbenchContractError(field, "must be a lowercase namespaced identifier");
  }
  return normalized;
}

function stringList(value, field, options = {}) {
  if (value === undefined) return Object.freeze([]);
  if (!Array.isArray(value)) {
    throw new WorkbenchContractError(field, "must be an array of strings");
  }
  const unique = new Set();
  value.forEach((item, index) => {
    const normalized = requireString(item, `${field}[${index}]`);
    if (options.namespaced && !identifierPattern.test(normalized)) {
      throw new WorkbenchContractError(`${field}[${index}]`, "must be a lowercase namespaced identifier");
    }
    unique.add(normalized);
  });
  return Object.freeze([...unique]);
}

function cloneJSONValue(value, field) {
  if (value === null || typeof value === "string" || typeof value === "boolean") return value;
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (Array.isArray(value)) {
    return Object.freeze(value.map((item, index) => cloneJSONValue(item, `${field}[${index}]`)));
  }
  if (isPlainObject(value)) {
    const result = {};
    for (const [key, item] of Object.entries(value)) {
      result[key] = cloneJSONValue(item, `${field}.${key}`);
    }
    return Object.freeze(result);
  }
  throw new WorkbenchContractError(field, "must contain only JSON-compatible values");
}

function optionalSchema(value, field) {
  if (value === undefined) return undefined;
  requireObject(value, field);
  return cloneJSONValue(value, field);
}

/**
 * @typedef {Object} ResourceRef
 * @property {string} id
 * @property {"file"|"artifact"|"task"|"session"|"url"|"business_object"} type
 * @property {string} title
 * @property {string} source
 * @property {string} [mimeType]
 * @property {boolean} [previewable]
 * @property {Record<string, unknown>} metadata
 */

/**
 * Validate and freeze a resource reference crossing the Workbench/plugin boundary.
 *
 * @param {unknown} input
 * @returns {Readonly<ResourceRef>}
 */
export function defineResourceRef(input) {
  const value = requireObject(input, "resource");
  const type = requireString(value.type, "resource.type");
  if (!RESOURCE_REF_TYPES.includes(type)) {
    throw new WorkbenchContractError("resource.type", `must be one of ${RESOURCE_REF_TYPES.join(", ")}`);
  }
  if (value.previewable !== undefined && typeof value.previewable !== "boolean") {
    throw new WorkbenchContractError("resource.previewable", "must be a boolean");
  }

  const resource = {
    id: requireString(value.id, "resource.id"),
    type,
    title: requireString(value.title, "resource.title"),
    source: requireString(value.source, "resource.source"),
    metadata: cloneJSONValue(value.metadata ?? {}, "resource.metadata")
  };
  const mimeType = optionalString(value.mimeType, "resource.mimeType");
  if (mimeType !== undefined) resource.mimeType = mimeType;
  if (value.previewable !== undefined) resource.previewable = value.previewable;
  return Object.freeze(resource);
}

export function isResourceRef(value) {
  try {
    defineResourceRef(value);
    return true;
  } catch (error) {
    if (error instanceof WorkbenchContractError) return false;
    throw error;
  }
}

/**
 * @typedef {Object} CommandDefinition
 * @property {string} id
 * @property {string} title
 * @property {"read"|"write"|"exec"|"external_effect"} risk
 * @property {readonly string[]} requiredPermissions
 * @property {readonly string[]} contexts
 * @property {Record<string, unknown>} [inputSchema]
 * @property {Record<string, unknown>} [outputSchema]
 */

/**
 * Validate the serializable declaration for a Workbench command.
 * Command handlers are registered separately by the plugin runtime.
 *
 * @param {unknown} input
 * @returns {Readonly<CommandDefinition>}
 */
export function defineCommand(input) {
  const value = requireObject(input, "command");
  const risk = value.risk === undefined ? "read" : requireString(value.risk, "command.risk");
  if (!COMMAND_RISKS.includes(risk)) {
    throw new WorkbenchContractError("command.risk", `must be one of ${COMMAND_RISKS.join(", ")}`);
  }

  const command = {
    id: requireIdentifier(value.id, "command.id"),
    title: requireString(value.title, "command.title"),
    risk,
    requiredPermissions: stringList(value.requiredPermissions, "command.requiredPermissions", { namespaced: true }),
    contexts: stringList(value.contexts, "command.contexts", { namespaced: true })
  };
  const inputSchema = optionalSchema(value.inputSchema, "command.inputSchema");
  const outputSchema = optionalSchema(value.outputSchema, "command.outputSchema");
  if (inputSchema !== undefined) command.inputSchema = inputSchema;
  if (outputSchema !== undefined) command.outputSchema = outputSchema;
  return Object.freeze(command);
}

export function isCommandDefinition(value) {
  try {
    defineCommand(value);
    return true;
  } catch (error) {
    if (error instanceof WorkbenchContractError) return false;
    throw error;
  }
}

function requireService(context, serviceName, methods) {
  const service = context[serviceName];
  if (!service || (typeof service !== "object" && typeof service !== "function") || Array.isArray(service)) {
    throw new WorkbenchContractError(`context.${serviceName}`, "must be a service object");
  }
  for (const method of methods) {
    if (typeof service[method] !== "function") {
      throw new WorkbenchContractError(`context.${serviceName}.${method}`, "must be a function");
    }
  }
  return service;
}

/**
 * Validate and freeze the stable identity portion of a plugin context while
 * preserving stateful service instances supplied by the Workbench runtime.
 *
 * @param {unknown} input
 * @returns {Readonly<Record<string, unknown>>}
 */
export function definePluginContext(input) {
  const value = requireObject(input, "context");
  const pluginInput = requireObject(value.plugin, "context.plugin");
  const scopeInput = requireObject(value.scope, "context.scope");
  const version = requireString(pluginInput.version, "context.plugin.version");
  if (!semverPattern.test(version)) {
    throw new WorkbenchContractError("context.plugin.version", "must be a semantic version");
  }

  const plugin = Object.freeze({
    id: requireIdentifier(pluginInput.id, "context.plugin.id"),
    version
  });
  const scope = {
    workspaceId: requireString(scopeInput.workspaceId, "context.scope.workspaceId"),
    userId: requireString(scopeInput.userId, "context.scope.userId"),
    roles: stringList(scopeInput.roles, "context.scope.roles")
  };
  const organizationId = optionalString(scopeInput.organizationId, "context.scope.organizationId");
  if (organizationId !== undefined) scope.organizationId = organizationId;

  const context = {
    plugin,
    scope: Object.freeze(scope)
  };
  for (const [serviceName, methods] of Object.entries(PLUGIN_CONTEXT_SERVICE_METHODS)) {
    context[serviceName] = requireService(value, serviceName, methods);
  }
  return Object.freeze(context);
}
