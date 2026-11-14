import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// ReproService web UI. Local dev tool only: static SPA, no SSR, talks directly
// to the controller API (CORS is already permissive for localhost/127.0.0.1 origins).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
  },
});
