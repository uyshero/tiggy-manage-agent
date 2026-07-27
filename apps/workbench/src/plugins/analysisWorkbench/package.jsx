import manifest from "./plugin.json" with { type: "json" };
import { RSurvivalWorkbenchPage, plugin } from "./index.jsx";

export default Object.freeze({
  manifest,
  plugin,
  components: Object.freeze({ RSurvivalWorkbenchPage }),
  enablement: Object.freeze({ defaultEnabled: true })
});
