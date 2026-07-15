import { definePluginContext } from "./contracts.js";
import {
  createCommandRegistry,
  createNavigationRegistry,
  createRouteRegistry
} from "./extensionRegistries.js";
import { checkPluginCompatibility } from "./pluginManifest.js";
import { evaluatePluginEnablement } from "./pluginPolicy.js";

export class PluginRuntimeError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "PluginRuntimeError";
    this.code = code;
  }
}

function publicRecord(record) {
  return Object.freeze({
    id: record.manifest.id,
    name: record.manifest.name,
    version: record.manifest.version,
    status: record.status,
    compatible: record.compatibility.compatible,
    compatibilityReasons: record.compatibility.reasons,
    enabled: record.enablement.enabled,
    enablementReasons: record.enablement.reasons,
    error: record.error || "",
    manifest: record.manifest
  });
}

function permissionList(value) {
  if (Array.isArray(value)) return value;
  return value === undefined || value === null ? [] : [value];
}

export function createStaticPluginRegistry(options) {
  if (!options || typeof options !== "object") throw new PluginRuntimeError("invalid_options", "Plugin Runtime options are required.");
  const environment = Object.freeze({
    workbenchAPIVersion: options.workbenchAPIVersion,
    designSystemVersion: options.designSystemVersion,
    surface: options.surface
  });
  const navigation = options.navigationRegistry || createNavigationRegistry({ groups: options.navigationGroups });
  const routes = options.routeRegistry || createRouteRegistry();
  const commands = options.commandRegistry || createCommandRegistry(options.commandOptions);
  const records = new Map();
  const listeners = new Set();

  function list() {
    return Object.freeze([...records.values()].map(publicRecord));
  }

  function emit() {
    const snapshot = list();
    for (const listener of listeners) listener(snapshot);
  }

  function get(pluginID) {
    const record = records.get(pluginID);
    return record ? publicRecord(record) : null;
  }

  function registerPackage(pluginPackage) {
    if (!pluginPackage || typeof pluginPackage !== "object" || Array.isArray(pluginPackage)) {
      throw new PluginRuntimeError("invalid_package", "Static plugin package must be an object.");
    }
    const compatibility = checkPluginCompatibility(pluginPackage.manifest, environment);
    const manifest = compatibility.manifest;
    const enablement = options.evaluateEnablement
      ? options.evaluateEnablement(pluginPackage, options.scope)
      : evaluatePluginEnablement(pluginPackage.enablement, options.scope);
    if (records.has(manifest.id)) throw new PluginRuntimeError("plugin_exists", `Plugin ${manifest.id} is already registered.`);
    const plugin = pluginPackage.plugin;
    if (!plugin || typeof plugin !== "object" || typeof plugin.activate !== "function") {
      throw new PluginRuntimeError("invalid_plugin", `Plugin ${manifest.id} must export activate().`);
    }
    if (plugin.id !== manifest.id) {
      throw new PluginRuntimeError("plugin_id_mismatch", `Plugin export id ${plugin.id || "missing"} does not match ${manifest.id}.`);
    }
    const record = {
      manifest,
      compatibility,
      enablement,
      plugin,
      components: pluginPackage.components || {},
      status: !compatibility.compatible ? "incompatible" : enablement.enabled ? "registered" : "disabled",
      error: "",
      disposers: []
    };
    records.set(manifest.id, record);
    emit();
    return publicRecord(record);
  }

  function createPermissions(record) {
    const declared = new Set(record.manifest.permissions);
    const host = options.services?.permissions;
    return Object.freeze({
      async has(value) {
        const permissions = permissionList(value);
        if (permissions.some((permission) => !declared.has(permission))) return false;
        if (typeof host?.has !== "function") return permissions.length === 0;
        return Boolean(await host.has(permissions));
      },
      async require(value) {
        const permissions = permissionList(value);
        const undeclared = permissions.find((permission) => !declared.has(permission));
        if (undeclared) {
          throw new PluginRuntimeError("permission_not_declared", `Plugin ${record.manifest.id} did not declare ${undeclared}.`);
        }
        if (typeof host?.require !== "function") {
          if (permissions.length) throw new PluginRuntimeError("permission_service_missing", "Host permission service is unavailable.");
          return true;
        }
        return host.require(permissions);
      }
    });
  }

  function createContext(record, disposers) {
    const permissions = createPermissions(record);
    const declaredCommands = new Set(record.manifest.contributes.commands.map((command) => command.id));
    const commandService = Object.freeze({
      register(commandID, handler) {
        if (!declaredCommands.has(commandID)) {
          throw new PluginRuntimeError("command_not_declared", `Plugin ${record.manifest.id} did not declare ${commandID}.`);
        }
        const dispose = commands.registerHandler(record.manifest.id, commandID, handler);
        disposers.push(dispose);
        return dispose;
      },
      execute(commandID, input, context = {}) {
        return commands.execute(commandID, input, { ...context, permissions });
      }
    });
    return definePluginContext({
      plugin: { id: record.manifest.id, version: record.manifest.version },
      scope: options.scope,
      permissions,
      commands: commandService,
      dialog: options.services?.dialog,
      notifications: options.services?.notifications,
      resources: options.services?.resources,
      tasks: options.services?.tasks,
      artifacts: options.services?.artifacts,
      http: options.services?.http
    });
  }

  function rollback(disposers) {
    for (const dispose of [...disposers].reverse()) {
      try {
        dispose();
      } catch {
        // Registry cleanup continues even when one plugin disposer is faulty.
      }
    }
  }

  async function activate(pluginID) {
    const record = records.get(pluginID);
    if (!record) throw new PluginRuntimeError("plugin_not_found", `Plugin ${pluginID} is not registered.`);
    if (!record.compatibility.compatible) throw new PluginRuntimeError("plugin_incompatible", `Plugin ${pluginID} is incompatible.`);
    if (!record.enablement.enabled) throw new PluginRuntimeError("plugin_disabled", `Plugin ${pluginID} is disabled for the current scope.`);
    if (record.status === "active") return publicRecord(record);
    if (["activating", "deactivating"].includes(record.status)) {
      throw new PluginRuntimeError("plugin_busy", `Plugin ${pluginID} is currently ${record.status}.`);
    }

    const disposers = [];
    record.status = "activating";
    record.error = "";
    emit();
    try {
      for (const contribution of record.manifest.contributes.commands) {
        disposers.push(commands.declare(pluginID, contribution));
      }
      const context = createContext(record, disposers);
      for (const contribution of record.manifest.contributes.routes) {
        const component = record.components[contribution.component];
        const navigationItem = record.manifest.contributes.navigation.find((item) => item.route === contribution.path);
        disposers.push(routes.register(pluginID, { ...contribution, title: navigationItem?.title || contribution.id }, component, context));
      }
      for (const contribution of record.manifest.contributes.navigation) {
        disposers.push(navigation.register(pluginID, contribution));
      }
      await record.plugin.activate(context);
      const missingHandler = record.manifest.contributes.commands.find((command) => !commands.hasHandler(command.id));
      if (missingHandler) throw new PluginRuntimeError("command_handler_missing", `Command ${missingHandler.id} has no active handler.`);
      record.disposers = disposers;
      record.status = "active";
      emit();
      return publicRecord(record);
    } catch (error) {
      rollback(disposers);
      record.disposers = [];
      record.status = "failed";
      record.error = error.message || String(error);
      emit();
      if (error instanceof PluginRuntimeError) throw error;
      throw new PluginRuntimeError("activation_failed", `Plugin ${pluginID} failed to activate.`, error);
    }
  }

  async function deactivate(pluginID) {
    const record = records.get(pluginID);
    if (!record) throw new PluginRuntimeError("plugin_not_found", `Plugin ${pluginID} is not registered.`);
    if (record.status !== "active") return publicRecord(record);
    record.status = "deactivating";
    emit();
    let error = null;
    try {
      await record.plugin.deactivate?.();
    } catch (caught) {
      error = caught;
    } finally {
      rollback(record.disposers);
      record.disposers = [];
    }
    if (error) {
      record.status = "failed";
      record.error = error.message || String(error);
      emit();
      throw new PluginRuntimeError("deactivation_failed", `Plugin ${pluginID} failed while deactivating.`, error);
    }
    record.status = "registered";
    record.error = "";
    emit();
    return publicRecord(record);
  }

  async function unregisterPackage(pluginID) {
    const record = records.get(pluginID);
    if (!record) return false;
    let error = null;
    try {
      if (record.status === "active") await deactivate(pluginID);
    } catch (caught) {
      error = caught;
    } finally {
      records.delete(pluginID);
      emit();
    }
    if (error) throw error;
    return true;
  }

  async function load(pluginPackages, loadOptions = {}) {
    const registered = pluginPackages.map(registerPackage);
    if (loadOptions.activate === false) return Object.freeze(registered);
    const results = [];
    for (const plugin of registered) {
      if (!plugin.compatible || !plugin.enabled) {
        results.push(plugin);
        continue;
      }
      try {
        results.push(await activate(plugin.id));
      } catch {
        results.push(get(plugin.id));
      }
    }
    return Object.freeze(results);
  }

  function subscribe(listener) {
    if (typeof listener !== "function") throw new PluginRuntimeError("invalid_listener", "Plugin listener must be a function.");
    listeners.add(listener);
    listener(list());
    return () => listeners.delete(listener);
  }

  return Object.freeze({
    registerPackage,
    unregisterPackage,
    activate,
    deactivate,
    load,
    get,
    list,
    subscribe,
    navigation,
    routes,
    commands
  });
}
