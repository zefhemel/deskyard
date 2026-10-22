---
tags: component
status: built
partOf: "[[Architecture/Agent API]]"
uses:
  - "[[Architecture/Agent tokens]]"
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Deskyard CLI]]"
spec: "[[superpowers/specs/2026-10-22-audio-and-ports-design|Audio and ports design]]"
---
TCP port mappings between an agent's machine and a desktop, over the one connection the agent already has. `deskyard -d alice forward 3000` makes the agent machine's `127.0.0.1:3000` reachable as `localhost:3000` inside the desktop; `forward -R 5173` does the reverse, making the desktop's `localhost:5173` available on the agent machine. Everything listens on loopback only, and desktop ports below 1024 are refused.

How it's reached: the CLI (outside the desktop, see [[Architecture/Remote CLI]]) opens the WebSocket `/api/agent/v1/forward` with an agent token. The supervisor runs the hidden `deskyard forward-helper` in the desktop through exec as `user` and relays binary frames between the WebSocket and the helper's stdio, so the desktop side listens on and dials `127.0.0.1` inside the desktop. The CLI reconnects with backoff (1 s up to 30 s) when the connection or desktop drops, keeping its local listeners bound meanwhile.

The protocol lives in `internal/forward`: a small frame format (`type | stream | payload`, at most 32 KiB of payload) multiplexing many TCP connections and listeners on one stream. Each connection has a 256 KiB flow-control window per direction, and each side allows at most 64 listeners and 256 connections; a peer that violates ids, windows or queue bounds is reset or disconnected. The frame table is in `supervisor/API.md`.

Open mappings are shown to people: `forwards` in the Desktop JSON and `viewers` events lists each token's `in` and `out` desktop ports, which the web UI displays next to the viewers.

Files: `supervisor/internal/cli/app/cmd_forward.go` (the `forward` command and `forward-helper`), `supervisor/internal/forward/` (frames and mux), `supervisor/internal/server/forward.go` (the relay) and `supervisor/internal/server/viewers.go` (the `forwards` list).
