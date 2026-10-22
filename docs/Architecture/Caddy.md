---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/KasmVNC]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The computer's only door, port 8080, running as user `caddy` so the agent inside can't replace it. Every request needs the per-computer token the supervisor sends (`X-Deskyard-Token`, from `WORKSPACE_PROXY_TOKEN`, which only Caddy can read inside the computer); it then serves KasmVNC's WebSocket, and the `/_cdp/` route to Chromium for the [[Architecture/Agent API]]. Files: `images/debian-xfce/rootfs/usr/local/bin/start-caddy`, `images/debian-xfce/rootfs/etc/caddy/Caddyfile`.
