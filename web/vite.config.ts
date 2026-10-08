/// <reference types="vitest/config" />
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const proxyTarget = process.env.PUSHRUN_PROXY_TARGET ?? "http://localhost:8000";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    proxy: {
      "/api": proxyTarget,
      "/git": proxyTarget,
      "/install.sh": proxyTarget,
      "/client.sh": proxyTarget,
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
  },
});
