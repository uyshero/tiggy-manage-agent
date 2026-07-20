import manifest from "./plugin.json" with { type: "json" };
import { EnterpriseBrowserPage, plugin } from "./index.jsx";

export default Object.freeze({
  manifest,
  plugin,
  components: Object.freeze({ EnterpriseBrowserPage }),
  enablement: Object.freeze({ defaultEnabled: true })
});
