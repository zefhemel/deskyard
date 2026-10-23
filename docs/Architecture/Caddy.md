---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Selkies]]"
  - "[[Architecture/Activity stream]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The desktop's only door, port 8080, running as user `caddy` so the agent inside can't replace it. Every request needs the per-desktop token the supervisor sends (`X-Deskyard-Token`, from `WORKSPACE_PROXY_TOKEN`, which only Caddy can read inside the desktop); it then serves [[Architecture/Selkies]]' stream WebSocket and health probe (Selkies' other HTTP APIs answer 404), the `/_cdp/` route to Chromium for the [[Architecture/Agent API]], and the `/_activity/` route, unbuffered, to the [[Architecture/Activity stream]] on `127.0.0.1:6092`. Files: `images/debian-xfce/rootfs/usr/local/bin/start-caddy`, `images/debian-xfce/rootfs/etc/caddy/Caddyfile`.
