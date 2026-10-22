---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Caddy]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Serves what's under `/<name>/vnc/`: the KasmVNC web client extracted from the computer's image (never from the running computer), and a WebSocket proxy to the computer's port 8080 with the per-computer token. Knows which accounts are connected to each computer and streams that to the desktop pages.
