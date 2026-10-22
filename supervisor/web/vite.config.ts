import { defineConfig, type ProxyOptions } from "vite";
import preact from "@preact/preset-vite";
import tailwindcss from "@tailwindcss/vite";
import { mockApi } from "./src/dev/mock.ts";

// `npm run dev` serves a fake API (src/dev/mock.ts). With
// SUPERVISOR_API=http://localhost:8080 it proxies to a real supervisor
// instead (Origin rewritten, since the supervisor checks it).
const target = process.env.SUPERVISOR_API;
const proxied: ProxyOptions = {
  target,
  ws: true,
  configure: (proxy) => {
    proxy.on("proxyReq", (req) => {
      if (target) req.setHeader("origin", target);
    });
    proxy.on("proxyReqWs", (req) => {
      if (target) req.setHeader("origin", target);
    });
  },
};

// The build goes into the Go module (supervisor/internal/web/dist), which
// embeds it. `public/.gitkeep` is copied along, so the folder survives the
// build's emptyOutDir and Go builds before the UI is built.
export default defineConfig({
  plugins: [preact(), tailwindcss(), mockApi()],
  server: target
    ? {
        proxy: {
          "/api": proxied,
          "^/[a-z][a-z0-9-]*/(vnc/|websockify)": proxied,
        },
      }
    : {},
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
});
