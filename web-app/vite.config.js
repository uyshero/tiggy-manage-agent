import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiTarget = process.env.TMA_DEV_API_BASE_URL || "http://127.0.0.1:8080";

export default defineConfig({
  base: "/app/assets/",
  plugins: [react()],
  publicDir: "public",
  server: {
    proxy: {
      "/auth": {
        target: apiTarget,
        changeOrigin: true
      },
      "/v1": {
        target: apiTarget,
        changeOrigin: true
      },
      "/v2": {
        target: apiTarget,
        changeOrigin: true
      },
      "/metrics": {
        target: apiTarget,
        changeOrigin: true
      }
    }
  },
  build: {
    outDir: "../internal/httpapi/app",
    emptyOutDir: true,
    minify: false,
    cssCodeSplit: false,
    rollupOptions: {
      input: "index.html",
      output: {
        entryFileNames: "app.js",
        chunkFileNames: "[name].js",
        assetFileNames: (assetInfo) => {
          if (assetInfo.name && assetInfo.name.endsWith(".css")) {
            return "styles.css";
          }
          return "[name][extname]";
        }
      }
    }
  }
});
