import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// FORGE frontend Vite config. A few Tauri-specific settings (fixed dev
// server port, ignoring apps/desktop in the watcher) are required so the
// desktop shell's `devUrl` in tauri.conf.json stays valid; see
// https://v2.tauri.app/start/frontend/ for the upstream rationale.
//
// The "@/" -> "src/" alias is declared in tsconfig.json for the type
// checker, but Vite's own module resolution doesn't read tsconfig `paths`
// — it needs the same mapping declared again here, or every "@/..."
// import resolves fine to `tsc` and then 404s at dev-server/build time.
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    watch: {
      ignored: ["**/apps/desktop/**"],
    },
  },
  envPrefix: ["VITE_", "TAURI_"],
  build: {
    target: "es2021",
    outDir: "dist",
    sourcemap: true,
  },
});
