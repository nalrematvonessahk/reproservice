import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// ReproService web UI. Local dev tool only: static SPA, no SSR, talks directly
// to the controller API (CORS is already permissive for localhost/127.0.0.1 origins).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
  },
  // Unit tests cover the pure client logic (api.ts, events, export, compare). They run in
  // plain Node with a mocked fetch; src/api.live.test.ts only runs against a real stand.
  test: {
    environment: "node",
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
