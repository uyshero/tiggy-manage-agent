import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiTarget = process.env.TMA_DEV_API_BASE_URL || "http://127.0.0.1:8080";

export default defineConfig({
  base: "/knowledge/assets/",
  plugins: [react()],
  publicDir: false,
  server: {
    proxy: {
      "/auth": { target: apiTarget, changeOrigin: true },
      "/v2": { target: apiTarget, changeOrigin: true }
    }
  },
  build: {
    outDir: "../../internal/httpapi/knowledge",
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
