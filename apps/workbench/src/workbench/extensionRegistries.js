import { defineCommand } from "./contracts.js";

export class ExtensionRegistryError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "ExtensionRegistryError";
    this.code = code;
  }
}

function createEmitter(getSnapshot) {
  const listeners = new Set();
  return {
    emit() {
      const snapshot = getSnapshot();
      for (const listener of listeners) listener(snapshot);
    },
    subscribe(listener) {
      if (typeof listener !== "function") throw new ExtensionRegistryError("invalid_listener", "Registry listener must be a function.");
      listeners.add(listener);
      listener(getSnapshot());
      return () => listeners.delete(listener);
    }
  };
}

function pluginIDValue(value) {
  const pluginID = typeof value === "string" ? value.trim() : "";
  if (!pluginID) throw new ExtensionRegistryError("invalid_plugin", "Plugin id is required.");
  return pluginID;
}

export function createNavigationRegistry(options = {}) {
  const allowedGroups = new Set(options.groups || ["workspace"]);
  const entries = new Map();
  const snapshot = () => Object.freeze([...entries.values()].sort((left, right) => (
    (left.order ?? 1000) - (right.order ?? 1000) || left.title.localeCompare(right.title)
  )));
  const emitter = createEmitter(snapshot);

  function register(pluginIDInput, input) {
    const pluginID = pluginIDValue(pluginIDInput);
    if (!input || typeof input !== "object" || Array.isArray(input)) {
      throw new ExtensionRegistryError("invalid_navigation", "Navigation contribution must be an object.");
    }
    if (!allowedGroups.has(input.group)) {
      throw new ExtensionRegistryError("navigation_group_denied", `Navigation group ${input.group} is not available.`);
    }
    const key = `${pluginID}:${input.id}`;
    if (entries.has(key)) throw new ExtensionRegistryError("navigation_exists", `Navigation contribution ${key} already exists.`);
    const entry = Object.freeze({
      pluginID,
      id: input.id,
      group: input.group,
      title: input.title,
      route: input.route,
      order: input.order
    });
    entries.set(key, entry);
    emitter.emit();
    let registered = true;
    return () => {
      if (!registered) return false;
      registered = false;
      entries.delete(key);
      emitter.emit();
      return true;
    };
  }

  function list(filters = {}) {
    const values = snapshot();
    return filters.group ? Object.freeze(values.filter((entry) => entry.group === filters.group)) : values;
  }

  return Object.freeze({ register, list, getSnapshot: snapshot, subscribe: emitter.subscribe });
}

export function createRouteRegistry() {
  const entries = new Map();
  const snapshot = () => Object.freeze([...entries.values()]);
  const emitter = createEmitter(snapshot);

  function register(pluginIDInput, input, component, context = null) {
    const pluginID = pluginIDValue(pluginIDInput);
    if (typeof component !== "function") {
      throw new ExtensionRegistryError("route_component_missing", `Route ${input?.path || "unknown"} requires a component export.`);
    }
    if (entries.has(input.path)) throw new ExtensionRegistryError("route_exists", `Route ${input.path} already exists.`);
    const entry = Object.freeze({
      pluginID,
      id: input.id,
      title: input.title || input.id,
      path: input.path,
      componentName: input.component,
      component,
      context,
      requiredPermissions: input.required_permissions || Object.freeze([])
    });
    entries.set(input.path, entry);
    emitter.emit();
    let registered = true;
    return () => {
      if (!registered) return false;
      registered = false;
      entries.delete(input.path);
      emitter.emit();
      return true;
    };
  }

  return Object.freeze({
    register,
    get: (path) => entries.get(path) || null,
    list: snapshot,
    getSnapshot: snapshot,
    subscribe: emitter.subscribe
  });
}

export function createCommandRegistry(options = {}) {
  const entries = new Map();
  const snapshot = () => Object.freeze([...entries.values()].map(({ handler, ...entry }) => Object.freeze({ ...entry, available: typeof handler === "function" })));
  const emitter = createEmitter(snapshot);
  const authorize = options.authorize || (async (command, context) => {
    if (!command.requiredPermissions.length) return;
    if (typeof context?.permissions?.require !== "function") {
      throw new ExtensionRegistryError("permission_service_missing", `Command ${command.id} requires a permission service.`);
    }
    await context.permissions.require(command.requiredPermissions);
  });

  function declare(pluginIDInput, input) {
    const pluginID = pluginIDValue(pluginIDInput);
    const command = defineCommand({
      id: input.id,
      title: input.title,
      risk: input.risk,
      requiredPermissions: input.required_permissions,
      contexts: input.contexts,
      inputSchema: input.input_schema,
      outputSchema: input.output_schema
    });
    if (!command.id.startsWith(`${pluginID}.`)) {
      throw new ExtensionRegistryError("command_namespace_denied", `Command ${command.id} does not belong to ${pluginID}.`);
    }
    if (entries.has(command.id)) throw new ExtensionRegistryError("command_exists", `Command ${command.id} already exists.`);
    entries.set(command.id, {
      pluginID,
      id: command.id,
      title: command.title,
      risk: command.risk,
      requiredPermissions: command.requiredPermissions,
      contexts: command.contexts,
      inputSchema: command.inputSchema,
      outputSchema: command.outputSchema,
      handler: null
    });
    emitter.emit();
    let declared = true;
    return () => {
      if (!declared) return false;
      declared = false;
      entries.delete(command.id);
      emitter.emit();
      return true;
    };
  }

  function registerHandler(pluginIDInput, commandID, handler) {
    const pluginID = pluginIDValue(pluginIDInput);
    const entry = entries.get(commandID);
    if (!entry || entry.pluginID !== pluginID) {
      throw new ExtensionRegistryError("command_not_declared", `Command ${commandID} is not declared by ${pluginID}.`);
    }
    if (typeof handler !== "function") throw new ExtensionRegistryError("invalid_handler", `Command ${commandID} handler must be a function.`);
    if (entry.handler) throw new ExtensionRegistryError("handler_exists", `Command ${commandID} already has a handler.`);
    entry.handler = handler;
    emitter.emit();
    let registered = true;
    return () => {
      if (!registered) return false;
      registered = false;
      if (entries.get(commandID) === entry) entry.handler = null;
      emitter.emit();
      return true;
    };
  }

  async function execute(commandID, input, context = {}) {
    const entry = entries.get(commandID);
    if (!entry) throw new ExtensionRegistryError("command_not_found", `Command ${commandID} is not registered.`);
    if (typeof entry.handler !== "function") throw new ExtensionRegistryError("command_unavailable", `Command ${commandID} is not active.`);
    const publicCommand = snapshot().find((command) => command.id === commandID);
    const startedAt = Date.now();
    try {
      await authorize(publicCommand, context);
      await options.beforeExecute?.(publicCommand, input, context);
      const result = await entry.handler(input, context);
      await options.afterExecute?.(publicCommand, { ok: true, result, durationMs: Date.now() - startedAt }, context);
      return result;
    } catch (error) {
      await options.afterExecute?.(publicCommand, { ok: false, error, durationMs: Date.now() - startedAt }, context);
      throw error;
    }
  }

  return Object.freeze({
    declare,
    registerHandler,
    execute,
    get: (commandID) => snapshot().find((entry) => entry.id === commandID) || null,
    hasHandler: (commandID) => typeof entries.get(commandID)?.handler === "function",
    list: snapshot,
    getSnapshot: snapshot,
    subscribe: emitter.subscribe
  });
}
