function pluginPackageFromModule(moduleValue, source) {
  const pluginPackage = moduleValue?.default;
  if (!pluginPackage || typeof pluginPackage !== "object" || Array.isArray(pluginPackage)) {
    throw new TypeError(`Static plugin module ${source} must export a default package object.`);
  }
  const pluginID = typeof pluginPackage.manifest?.id === "string" ? pluginPackage.manifest.id.trim() : "";
  if (!pluginID) throw new TypeError(`Static plugin module ${source} is missing manifest.id.`);
  if (pluginPackage.plugin?.id !== pluginID || typeof pluginPackage.plugin?.activate !== "function") {
    throw new TypeError(`Static plugin module ${source} must export an activate() implementation for ${pluginID}.`);
  }
  const components = pluginPackage.components;
  if (!components || typeof components !== "object" || Array.isArray(components)) {
    throw new TypeError(`Static plugin module ${source} must export a components object.`);
  }
  for (const route of pluginPackage.manifest?.contributes?.routes || []) {
    if (typeof components[route.component] !== "function") {
      throw new TypeError(`Static plugin ${pluginID} is missing route component ${route.component}.`);
    }
  }
  return Object.freeze({
    ...pluginPackage,
    components: Object.freeze({ ...components })
  });
}

export function buildStaticPluginCatalog(modules) {
  if (!modules || typeof modules !== "object" || Array.isArray(modules)) {
    throw new TypeError("Static plugin modules must be an object.");
  }
  const seen = new Set();
  const catalog = Object.entries(modules).map(([source, moduleValue]) => {
    const pluginPackage = pluginPackageFromModule(moduleValue, source);
    const pluginID = pluginPackage.manifest.id;
    if (seen.has(pluginID)) throw new TypeError(`Static plugin id ${pluginID} is duplicated.`);
    seen.add(pluginID);
    return pluginPackage;
  });
  catalog.sort((left, right) => left.manifest.id.localeCompare(right.manifest.id));
  return Object.freeze(catalog);
}

export function loadPluginCatalog(runtime, catalog, options = {}) {
  if (!runtime || typeof runtime.load !== "function") {
    throw new TypeError("A Workbench plugin runtime is required.");
  }
  if (!Array.isArray(catalog)) {
    throw new TypeError("A static plugin catalog is required.");
  }
  return runtime.load(catalog, options);
}
