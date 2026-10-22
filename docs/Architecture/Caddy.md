---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/KasmVNC]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The computer's front door on port 8080, running as user `caddy` so the agent inside can't replace it. Today: password (basic auth) in front of KasmVNC's web client and WebSocket, the Origin check against cross-site WebSocket hijacking, and HTTPS when a domain is set. With the supervisor, which is then the only way to run a computer, all of that goes: Caddy only checks the per-computer token (`WORKSPACE_PROXY_TOKEN`) and serves KasmVNC's WebSocket and the `/_cdp/` route to Chromium. Files: `rootfs/usr/local/bin/start-caddy`, `rootfs/etc/caddy/Caddyfile`.
