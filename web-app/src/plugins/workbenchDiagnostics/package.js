import manifest from "./plugin.json" with { type: "json" };
import { DiagnosticsPage, plugin } from "./index.js";

export default Object.freeze({
  manifest,
  plugin,
  components: Object.freeze({ DiagnosticsPage }),
  enablement: Object.freeze({ defaultEnabled: true })
});
