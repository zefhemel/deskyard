---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent tokens]]"
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Caddy]]"
  - "[[Architecture/Deskyard CLI]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
`/api/agent/v1/`, for agents elsewhere, authenticated with an agent token. Two endpoints: an exec WebSocket (one command with streamed stdin, stdout and stderr and an exit code, or an interactive terminal with `tty`), and the shared Chromium's CDP. Exec runs through the engine as `user`, never as root; the [[Architecture/Remote CLI]] builds shell, file copies and screen actions on it. CDP goes through the desktop's Caddy (`/_cdp/`).
