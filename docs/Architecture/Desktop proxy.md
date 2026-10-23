---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Caddy]]"
  - "[[Architecture/Selkies]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Serves what's under `/<name>/screen/`: [[Architecture/Selkies]]' web client extracted from the desktop's image per image ID (never from the running desktop), served with our additions (the page and additions `no-store`, the client's other files `no-cache`) (`screen_additions.js`: the hooks the desktop page drives, the `pointerrawupdate` fix, and dropping the client's `STOP_VIDEO` so a background tab never stops the stream), and a WebSocket proxy for the stream (`/<name>/screen/api/websockets`) to the desktop's port 8080 with the per-desktop token. Every other Selkies HTTP API is refused. Session, access and same-origin checks apply, and an open stream counts as viewing. Dials to one desktop are spaced 600 ms apart (Selkies refuses a second connection from the same address within 500 ms), and a stream that shows no picture 2.5 s after its first message gets Selkies' `START_VIDEO`. Knows which accounts are viewing each desktop, and which of them have their mic or camera on (per tab, `POST /api/desktops/{name}/mic` and `…/cam`), and streams that to the desktop pages. Upload into `~/Desktop` and download from `/workspace` are the supervisor's own routes, run as `user` through exec, not Selkies'. Files: `supervisor/internal/server/desktop.go`, `screen_additions.js`, `stream_watch.go`, `viewers.go`, `files.go`, `upload.go`.
