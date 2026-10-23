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
Serves what's under `/<name>/screen/`: [[Architecture/Selkies]]' web client extracted from the desktop's image per image ID (never from the running desktop), served with our additions (the page and additions `no-store`, the client's other files `no-cache`) (`screen_additions.js`: the hooks the desktop page drives, the `pointerrawupdate` fix, dropping the client's `STOP_VIDEO` so a background tab never stops the stream, and letting the client run over plain http: an insecure context, where it streams JPEG and plays sound without an AudioWorklet), and a WebSocket proxy for the stream (`/<name>/screen/api/websockets`) to the desktop's port 8080 with the per-desktop token. Every other Selkies HTTP API is refused. Session, access and same-origin checks apply, and an open stream counts as viewing. Dials to one desktop are spaced 600 ms apart (Selkies refuses a second connection from the same address within 500 ms), and a stream that shows no picture 2.5 s after its first message gets Selkies' `START_VIDEO`. Knows which accounts are viewing each desktop, and which of them have their mic or camera on (per tab, `POST /api/desktops/{name}/mic` and `…/cam`), and streams that to the desktop pages. Upload into `~/Desktop` and download from `/workspace` are the supervisor's own routes, run as `user` through exec, not Selkies'. Files: `supervisor/internal/server/desktop.go`, `screen_additions.js`, `stream_watch.go`, `viewers.go`, `files.go`, `upload.go`.

For VPN-enabled desktops, the [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans to resolve the Caddy target through [[Architecture/VPN runtime]] while continuing to authenticate with the desktop's token. Runtime target resolution and full proxy validation are not implemented yet.

The additions also expose explicit software-keyboard show/hide and local touch-view events. They bypass the automatic-focus guard only on an explicit keyboard request and report actual assist-input focus. A wrapper around the pinned Selkies input object's `_sendZoomNotches` diverts classified touch pinches to browser-local magnification; mouse Ctrl+wheel remains an application command. While magnified, classified two-finger scroll deltas pan the local view instead. The upstream touch lifecycle still handles finger release and button state. Gesture points are normalized into the top-level viewport before classification so magnification and panning do not change their coordinate system mid-gesture. The requested touch mode is retained until the input object initializes, including on reconnect/reload. These private input methods are version-specific and must be checked when updating Selkies; real multi-touch browser integration tests cover them.
