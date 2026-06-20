import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { defineConfig } from "vite"

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// Backend dev server. The Vite dev server proxies backend-owned routes here
// so the whole app is reachable from a single origin (the Vite server) with
// HMR for the frontend.
const backend = "http://localhost:8080"

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react({
      jsxRuntime: "automatic",
    }),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      // The upload endpoint (POST /) shares the "/" path with the SPA root
      // (GET /). Proxy only non-GET requests to the backend and let Vite
      // serve GET "/" so the SPA and HMR keep working.
      "^/$": {
        target: backend,
        changeOrigin: true,
        bypass: (req) => (req.method === "GET" ? req.url : undefined),
      },
      // Backend-owned routes: downloads and operational endpoints.
      "/d": { target: backend, changeOrigin: true },
      "/healthz": { target: backend, changeOrigin: true },
      "/readyz": { target: backend, changeOrigin: true },
      "/metrics": { target: backend, changeOrigin: true },
    },
  },
})
