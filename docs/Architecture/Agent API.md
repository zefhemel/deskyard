---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent tokens]]"
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Caddy]]"
  - "[[Architecture/Computer CLI]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
`/api/agent/v1/`, for agents elsewhere, authenticated with an agent token: exec (one command, or streamed), an interactive shell over a WebSocket, file upload and download, and the shared Chromium's CDP. Exec, shell and files run through the engine as `user`, never as root; desktop actions run the in-computer `computer` command; CDP goes through the computer's Caddy (`/_cdp/`).
