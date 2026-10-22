---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Caddy]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Serves what's under `/<name>/vnc/`: the KasmVNC web client extracted from the computer's image (never from the running computer), and a WebSocket proxy to the computer's port 8080 with the per-computer token. Counts viewers per computer and streams who's viewing to the desktop pages, which switch the resize mode.
