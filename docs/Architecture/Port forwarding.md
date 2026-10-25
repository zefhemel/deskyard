---
tags: component
status: built
partOf: "[[Architecture/Remote access API]]"
uses:
  - "[[Architecture/Access tokens]]"
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Deskyard CLI]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
TCP port mappings between your machine and a desktop, over a single authenticated connection. `deskyard -d alice forward 3000` makes your machine’s `127.0.0.1:3000` reachable as `localhost:3000` inside the desktop; `forward -R 5173` does the reverse, making the desktop's `localhost:5173` available on your machine. Everything listens on loopback only. A mapping is `LOCAL:DESKTOP` for plain `forward` and `DESKTOP:LOCAL` after `-R`. Only listening in the desktop is limited to ports from 1024 up; `forward 80` is refused, but `forward -R 80:8080` works because the helper only dials. Like any local process in the desktop, pages in the desktop's browser can reach the in-mapped ports.

How it's reached: the CLI (outside the desktop, see [[Architecture/Remote CLI]]) opens the WebSocket `/api/agent/v1/forward` with an access token. The supervisor runs the hidden `deskyard forward-helper` in the desktop through exec as `user` and relays binary frames between the WebSocket and the helper's stdio, so the desktop side listens on and dials `127.0.0.1` inside the desktop. The CLI reconnects with backoff (1 s up to 30 s) when the connection or desktop drops, keeping its `-R` local listeners bound meanwhile; plain mappings are listened on again in the desktop once the link is back.

Limits: a token may have 4 links open per desktop (a fifth gets an error message before any exec), each link at most 256 mappings, and at most 64 mappings per token are listed. Viewer events for one desktop are coalesced to one per 250 ms with a trailing send, so churning mappings can't flood the event stream. Known limitation: "Linked to …" and the `out` lines are printed when the link is accepted, before the helper is known to run; a ready signal from the helper is a follow-up.

The protocol lives in `internal/forward`: a small frame format (`type | stream | payload`, at most 32 KiB of payload) multiplexing many TCP connections and listeners on one stream. Each connection has a 256 KiB flow-control window per direction, and each side allows at most 64 listeners and 256 connections; a peer that violates ids, windows or queue bounds is reset or disconnected. The frame table is in `supervisor/API.md`.

Open mappings are shown to people: `forwards` in the Desktop JSON and `viewers` events lists each token's `in` and `out` desktop ports, which the web UI displays next to the viewers.

Files: `supervisor/internal/cli/app/cmd_forward.go` (the `forward` command and `forward-helper`), `supervisor/internal/forward/` (frames and mux), `supervisor/internal/server/forward.go` (the relay) and `supervisor/internal/server/viewers.go` (the `forwards` list).
