import { defineCommand } from "./contracts.js";

export const WORKBENCH_PLUGIN_PROTOCOL = "tma.workbench_plugin.v1";
export const WORKBENCH_PLUGIN_SURFACES = Object.freeze([
  "web_desktop",
  "desktop_shell",
  "web_tablet",
  "web_mobile"
]);

const namespacedIdentifierPattern = /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)+$/;
const localIdentifierPattern = /^[a-z][a-z0-9_-]*$/;
const semverPattern = /^(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
const comparatorPattern = /^(>=|<=|>|<|=)?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$/;

export class PluginManifestError extends TypeError {
  constructor(field, message, code = "invalid_manifest") {
    super(`${field}: ${message}`);
    this.name = "PluginManifestError";
    this.field = field;
    this.code = code;
  }
}

function isPlainObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function objectValue(value, field) {
  if (!isPlainObject(value)) throw new PluginManifestError(field, "must be a plain object");
  return value;
}

function allowedKeys(value, field, keys) {
  const allowed = new Set(keys);
  const unknown = Object.keys(value).find((key) => !allowed.has(key));
  if (unknown) throw new PluginManifestError(`${field}.${unknown}`, "is not supported by the Phase 0A manifest");
}

function stringValue(value, field) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) throw new PluginManifestError(field, "must be a non-empty string");
  return normalized;
}

function optionalString(value, field) {
  if (value === undefined || value === null || value === "") return undefined;
  return stringValue(value, field);
}

function namespacedIdentifier(value, field) {
  const normalized = stringValue(value, field);
  if (!namespacedIdentifierPattern.test(normalized)) {
    throw new PluginManifestError(field, "must be a lowercase namespaced identifier");
  }
  return normalized;
}

function localIdentifier(value, field) {
  const normalized = stringValue(value, field);
  if (!localIdentifierPattern.test(normalized)) {
    throw new PluginManifestError(field, "must be a lowercase identifier");
  }
  return normalized;
}

function uniqueStrings(value, field, normalize) {
  if (!Array.isArray(value)) throw new PluginManifestError(field, "must be an array");
  const result = [];
  const seen = new Set();
  value.forEach((item, index) => {
    const normalized = normalize(item, `${field}[${index}]`);
    if (seen.has(normalized)) return;
    seen.add(normalized);
    result.push(normalized);
  });
  return Object.freeze(result);
}

function parseSemver(value, field) {
  const normalized = stringValue(value, field);
  const match = normalized.match(semverPattern);
  if (!match) throw new PluginManifestError(field, "must be a semantic version");
  return { raw: normalized, parts: match.slice(1, 4).map(Number) };
}

function compareSemver(left, right) {
  for (let index = 0; index < 3; index += 1) {
    if (left[index] !== right[index]) return left[index] < right[index] ? -1 : 1;
  }
  return 0;
}

function parseRange(value, field) {
  const normalized = stringValue(value, field);
  if (normalized === "*") return Object.freeze([]);
  const comparators = normalized.split(/\s+/).map((part, index) => {
    const match = part.match(comparatorPattern);
    if (!match) throw new PluginManifestError(`${field}[${index}]`, "must use exact or comparator semantic versions");
    return Object.freeze({ operator: match[1] || "=", version: parseSemver(match[2], field).parts });
  });
  return Object.freeze(comparators);
}

export function satisfiesSemverRange(version, range) {
  const current = parseSemver(version, "version").parts;
  const comparators = parseRange(range, "range");
  return comparators.every(({ operator, version: expected }) => {
    const comparison = compareSemver(current, expected);
    if (operator === ">=") return comparison >= 0;
    if (operator === "<=") return comparison <= 0;
    if (operator === ">") return comparison > 0;
    if (operator === "<") return comparison < 0;
    return comparison === 0;
  });
}

function requiredPermissions(value, field, manifestPermissions) {
  const permissions = uniqueStrings(value ?? [], field, namespacedIdentifier);
  permissions.forEach((permission, index) => {
    if (!manifestPermissions.has(permission)) {
      throw new PluginManifestError(`${field}[${index}]`, `permission ${permission} is not declared by the plugin`, "undeclared_permission");
    }
  });
  return permissions;
}

function normalizeEntry(value, field = "manifest.entry") {
  const entry = stringValue(value, field);
  if (!entry.startsWith("./") || entry.includes("\\") || entry.split("/").includes("..") || /^[a-z]+:/i.test(entry)) {
    throw new PluginManifestError(field, "must be a relative path inside the plugin package");
  }
  return entry;
}

function normalizeNavigation(value, pluginID) {
  if (value === undefined) return Object.freeze([]);
  if (!Array.isArray(value)) throw new PluginManifestError("manifest.contributes.navigation", "must be an array");
  const ids = new Set();
  return Object.freeze(value.map((item, index) => {
    const field = `manifest.contributes.navigation[${index}]`;
    const input = objectValue(item, field);
    allowedKeys(input, field, ["id", "group", "title", "route", "order"]);
    const id = localIdentifier(input.id, `${field}.id`);
    if (ids.has(id)) throw new PluginManifestError(`${field}.id`, `duplicate navigation id ${id}`);
    ids.add(id);
    const route = stringValue(input.route, `${field}.route`);
    if (!route.startsWith(`/plugins/${pluginID}/`)) {
      throw new PluginManifestError(`${field}.route`, `must be under /plugins/${pluginID}/`);
    }
    const navigation = {
      id,
      group: localIdentifier(input.group, `${field}.group`),
      title: stringValue(input.title, `${field}.title`),
      route
    };
    if (input.order !== undefined) {
      if (!Number.isInteger(input.order)) throw new PluginManifestError(`${field}.order`, "must be an integer");
      navigation.order = input.order;
    }
    return Object.freeze(navigation);
  }));
}

function normalizeRoutes(value, pluginID, manifestPermissions) {
  if (value === undefined) return Object.freeze([]);
  if (!Array.isArray(value)) throw new PluginManifestError("manifest.contributes.routes", "must be an array");
  const ids = new Set();
  const paths = new Set();
  return Object.freeze(value.map((item, index) => {
    const field = `manifest.contributes.routes[${index}]`;
    const input = objectValue(item, field);
    allowedKeys(input, field, ["id", "path", "component", "required_permissions"]);
    const id = localIdentifier(input.id, `${field}.id`);
    const path = stringValue(input.path, `${field}.path`);
    if (ids.has(id)) throw new PluginManifestError(`${field}.id`, `duplicate route id ${id}`);
    if (paths.has(path)) throw new PluginManifestError(`${field}.path`, `duplicate route path ${path}`);
    if (!path.startsWith(`/plugins/${pluginID}/`)) {
      throw new PluginManifestError(`${field}.path`, `must be under /plugins/${pluginID}/`);
    }
    ids.add(id);
    paths.add(path);
    return Object.freeze({
      id,
      path,
      component: stringValue(input.component, `${field}.component`),
      required_permissions: requiredPermissions(input.required_permissions, `${field}.required_permissions`, manifestPermissions)
    });
  }));
}

function normalizeCommands(value, pluginID, manifestPermissions) {
  if (value === undefined) return Object.freeze([]);
  if (!Array.isArray(value)) throw new PluginManifestError("manifest.contributes.commands", "must be an array");
  const ids = new Set();
  return Object.freeze(value.map((item, index) => {
    const field = `manifest.contributes.commands[${index}]`;
    const input = objectValue(item, field);
    allowedKeys(input, field, ["id", "title", "risk", "required_permissions", "contexts", "input_schema", "output_schema"]);
    const required = requiredPermissions(input.required_permissions, `${field}.required_permissions`, manifestPermissions);
    const command = defineCommand({
      id: input.id,
      title: input.title,
      risk: input.risk,
      requiredPermissions: required,
      contexts: input.contexts,
      inputSchema: input.input_schema,
      outputSchema: input.output_schema
    });
    if (!command.id.startsWith(`${pluginID}.`)) {
      throw new PluginManifestError(`${field}.id`, `must use plugin namespace ${pluginID}`);
    }
    if (ids.has(command.id)) throw new PluginManifestError(`${field}.id`, `duplicate command id ${command.id}`);
    ids.add(command.id);
    const normalized = {
      id: command.id,
      title: command.title,
      risk: command.risk,
      required_permissions: command.requiredPermissions,
      contexts: command.contexts
    };
    if (command.inputSchema) normalized.input_schema = command.inputSchema;
    if (command.outputSchema) normalized.output_schema = command.outputSchema;
    return Object.freeze(normalized);
  }));
}

function normalizeConfiguration(value) {
  if (value === undefined) return undefined;
  const input = objectValue(value, "manifest.configuration");
  allowedKeys(input, "manifest.configuration", ["schema", "scope"]);
  const scope = stringValue(input.scope, "manifest.configuration.scope");
  if (!["organization", "workspace", "user"].includes(scope)) {
    throw new PluginManifestError("manifest.configuration.scope", "must be organization, workspace, or user");
  }
  return Object.freeze({ schema: normalizeEntry(input.schema, "manifest.configuration.schema"), scope });
}

export function definePluginManifest(input) {
  const value = objectValue(input, "manifest");
  allowedKeys(value, "manifest", [
    "protocol_version", "id", "name", "description", "version", "entry", "surfaces", "engines",
    "permissions", "contributes", "configuration"
  ]);
  const protocolVersion = stringValue(value.protocol_version, "manifest.protocol_version");
  if (protocolVersion !== WORKBENCH_PLUGIN_PROTOCOL) {
    throw new PluginManifestError("manifest.protocol_version", `must be ${WORKBENCH_PLUGIN_PROTOCOL}`, "unsupported_protocol");
  }
  const id = namespacedIdentifier(value.id, "manifest.id");
  const version = parseSemver(value.version, "manifest.version").raw;
  const surfaces = uniqueStrings(value.surfaces, "manifest.surfaces", (item, field) => {
    const surface = stringValue(item, field);
    if (!WORKBENCH_PLUGIN_SURFACES.includes(surface)) throw new PluginManifestError(field, "is not a supported surface");
    return surface;
  });
  if (!surfaces.length) throw new PluginManifestError("manifest.surfaces", "must include at least one surface");
  const permissions = uniqueStrings(value.permissions ?? [], "manifest.permissions", namespacedIdentifier);
  const permissionSet = new Set(permissions);
  const enginesInput = objectValue(value.engines, "manifest.engines");
  allowedKeys(enginesInput, "manifest.engines", ["workbench_api", "design_system"]);
  const engines = Object.freeze({
    workbench_api: stringValue(enginesInput.workbench_api, "manifest.engines.workbench_api"),
    design_system: stringValue(enginesInput.design_system, "manifest.engines.design_system")
  });
  parseRange(engines.workbench_api, "manifest.engines.workbench_api");
  parseRange(engines.design_system, "manifest.engines.design_system");

  const contributesInput = value.contributes === undefined ? {} : objectValue(value.contributes, "manifest.contributes");
  allowedKeys(contributesInput, "manifest.contributes", ["navigation", "routes", "commands"]);
  const routes = normalizeRoutes(contributesInput.routes, id, permissionSet);
  const navigation = normalizeNavigation(contributesInput.navigation, id);
  const routePaths = new Set(routes.map((route) => route.path));
  navigation.forEach((item, index) => {
    if (!routePaths.has(item.route)) {
      throw new PluginManifestError(`manifest.contributes.navigation[${index}].route`, "must reference a declared route");
    }
  });
  const contributes = Object.freeze({
    navigation,
    routes,
    commands: normalizeCommands(contributesInput.commands, id, permissionSet)
  });
  const manifest = {
    protocol_version: protocolVersion,
    id,
    name: stringValue(value.name, "manifest.name"),
    version,
    entry: normalizeEntry(value.entry),
    surfaces,
    engines,
    permissions,
    contributes
  };
  const description = optionalString(value.description, "manifest.description");
  if (description !== undefined) manifest.description = description;
  const configuration = normalizeConfiguration(value.configuration);
  if (configuration !== undefined) manifest.configuration = configuration;
  return Object.freeze(manifest);
}

export function checkPluginCompatibility(input, environment) {
  const manifest = definePluginManifest(input);
  const value = objectValue(environment, "environment");
  const workbenchAPIVersion = parseSemver(value.workbenchAPIVersion, "environment.workbenchAPIVersion").raw;
  const designSystemVersion = parseSemver(value.designSystemVersion, "environment.designSystemVersion").raw;
  const surface = stringValue(value.surface, "environment.surface");
  const reasons = [];
  if (!manifest.surfaces.includes(surface)) reasons.push(Object.freeze({ code: "surface_unsupported", message: `Surface ${surface} is not declared.` }));
  if (!satisfiesSemverRange(workbenchAPIVersion, manifest.engines.workbench_api)) {
    reasons.push(Object.freeze({ code: "workbench_api_incompatible", message: `Workbench API ${workbenchAPIVersion} does not satisfy ${manifest.engines.workbench_api}.` }));
  }
  if (!satisfiesSemverRange(designSystemVersion, manifest.engines.design_system)) {
    reasons.push(Object.freeze({ code: "design_system_incompatible", message: `Design System ${designSystemVersion} does not satisfy ${manifest.engines.design_system}.` }));
  }
  return Object.freeze({ compatible: reasons.length === 0, manifest, reasons: Object.freeze(reasons) });
}
