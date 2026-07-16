import manifest from "./plugin.json" with { type: "json" };
import { ResearchProjectsPage, plugin } from "./index.jsx";

export default Object.freeze({
  manifest,
  plugin,
  components: Object.freeze({ ResearchProjectsPage }),
  enablement: Object.freeze({ defaultEnabled: true })
});
