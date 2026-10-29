import { fileURLToPath } from "node:url";
import { defineConfig, type ProxyOptions } from "vite";
import preact from "@preact/preset-vite";
import tailwindcss from "@tailwindcss/vite";
import { mockApi } from "./src/dev/mock.ts";

// `npm run dev` serves a fake API (src/dev/mock.ts). With
// DESKYARD_API=http://localhost:7343 it proxies to a real supervisor
// instead (Origin rewritten, since the supervisor checks it).
const target = process.env.DESKYARD_API;
const proxied: ProxyOptions = {
  target,
  changeOrigin: true,
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
          "^/[a-z][a-z0-9-]*/screen/": proxied, // the desktop's client and its stream
        },
      }
    : {},
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: fileURLToPath(new URL("./index.html", import.meta.url)),
        "deskyard-screen": fileURLToPath(
          new URL("./src/selkies/adapter.ts", import.meta.url),
        ),
      },
      output: {
        entryFileNames: (chunk) =>
          chunk.name === "deskyard-screen"
            ? "deskyard-screen.js"
            : "assets/[name]-[hash].js",
      },
    },
  },
});
