import { buildStaticPluginCatalog, loadPluginCatalog } from "./catalogLoader.js";

// Vite resolves these trusted packages at build time. This remains a static
// catalog: no remote JavaScript or runtime filesystem loading is introduced.
const packageModules = {
  ...import.meta.glob("./*/package.js", { eager: true }),
  ...import.meta.glob("./*/package.jsx", { eager: true })
};

export const staticPluginCatalog = buildStaticPluginCatalog(packageModules);

export function loadStaticPluginCatalog(runtime, options = {}) {
  return loadPluginCatalog(runtime, staticPluginCatalog, options);
}
