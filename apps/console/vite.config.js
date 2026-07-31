import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiTarget = process.env.TMA_DEV_API_BASE_URL || "http://127.0.0.1:8080";
const embeddedBuild = process.env.TMA_CONSOLE_EMBEDDED === "1";
const assetBase = process.env.TMA_CONSOLE_BASE_PATH || (embeddedBuild ? "/console/assets/" : "/");
const apiProxy = {
  "/auth": { target: apiTarget, changeOrigin: true },
  "/v2": { target: apiTarget, changeOrigin: true }
};

export default defineConfig({
  base: assetBase,
  plugins: [react()],
  publicDir: false,
  server: {
    proxy: apiProxy
  },
  preview: {
    proxy: apiProxy
  },
  build: {
    outDir: embeddedBuild ? "../../internal/httpapi/console" : "dist",
    emptyOutDir: true,
    minify: false,
    cssCodeSplit: false,
    rollupOptions: {
      input: "index.html",
      output: {
        entryFileNames: "app.js",
        chunkFileNames: "[name].js",
        assetFileNames: (assetInfo) => assetInfo.name?.endsWith(".css") ? "styles.css" : "[name][extname]"
      }
    }
  }
});
