import { defineConfig } from "vite";
import type { PluginOption } from "vite";
import uniModule from "@dcloudio/vite-plugin-uni";

type UniPluginFactory = (options?: Record<string, unknown>) => PluginOption[];
const uni = (typeof uniModule === "function"
  ? uniModule
  : (uniModule as unknown as { default: UniPluginFactory }).default) as UniPluginFactory;

export default defineConfig({
  plugins: [uni()],
  server: {
    port: 5174,
    strictPort: false,
  },
});
