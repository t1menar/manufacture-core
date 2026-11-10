/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Real core for `npm run dev`; `npm run dev:mock` runs fully in-browser against the simulator.
const target = process.env.MF_CORE_URL ?? "http://127.0.0.1:8080";

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  base: "/",
  define: {
    __MF_MOCK__: JSON.stringify(mode === "mock"),
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    fs: { allow: [".."] },
    proxy: mode === "mock" ? undefined : { "/api": { target, changeOrigin: false } },
  },
  preview: {
    host: "127.0.0.1",
    port: 4173,
    proxy: mode === "mock" ? undefined : { "/api": { target, changeOrigin: false } },
  },
  build: {
    outDir: "dist",
    emptyOutDir: false,
    sourcemap: false,
    chunkSizeWarningLimit: 1800,
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          if (/node_modules\/(three|@react-three|postprocessing|maath|three-stdlib|@react-spring)/.test(id)) return "three";
          if (/node_modules\/(@codemirror|@uiw|@lezer|codemirror)/.test(id)) return "editor";
          if (/node_modules\/(recharts|d3-|@mantine\/charts)/.test(id)) return "charts";
          return undefined;
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    css: false,
  },
}));
