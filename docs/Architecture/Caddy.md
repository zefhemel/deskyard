---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/KasmVNC]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The computer's front door on port 8080, running as user `caddy`: password (basic auth) in front of KasmVNC's web client and WebSocket, the Origin check against cross-site WebSocket hijacking, and HTTPS when a domain is set. With the supervisor it switches to `WORKSPACE_AUTH=supervisor` and checks a per-computer token instead. Files: `rootfs/usr/local/bin/start-caddy`, `rootfs/etc/caddy/Caddyfile`.
