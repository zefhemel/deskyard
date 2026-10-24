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
Serves what's under `/<name>/screen/`: [[Architecture/Selkies]]' web client extracted from the desktop's immutable image ID (never from the running desktop). The page and adapter are `no-store`; other client files are `no-cache`. `desktop.go` selects the typed v1 adapter (`supervisor/web/src/selkies/adapter.ts`, built by Vite as `deskyard-screen.js` and embedded with the UI) for images advertising the versioned client hook contract. The module is inserted before Selkies' module scripts. Images without the marker retain `screen_additions.js`, the legacy pre-contract Selkies 2.1.0 bridge, so existing desktops need no immediate rebuild. Unknown advertised versions show an actionable upgrade error. The v1 adapter uses explicit client calls for transport, focus, capture, playback and classified touch gestures, without private-method or browser-prototype interception.

The stream WebSocket (`/<name>/screen/api/websockets`) proxies to the desktop's port 8080 with its token; every other Selkies HTTP API is refused. Session, access and same-origin checks apply, and an open stream counts as viewing. Dials to one desktop are spaced 600 ms apart (Selkies refuses a second connection from the same address within 500 ms), and a stream showing no picture 2.5 s after its first message gets `START_VIDEO`. Viewer/mic/camera state is tracked per tab. The adapter filters shared `STOP_VIDEO` and automatic audio subscription commands while explicit per-viewer sound controls bypass that filter. Capture consent is revoked on connection changes, including pending browser permission requests; a late granted stream is stopped before reaching the client. Upload/download remain supervisor routes through user exec. Files: `desktop.go`, `stream_watch.go`, `viewers.go`, `files.go`, `upload.go`, `supervisor/web/src/selkies/contract.ts`, `adapter.ts`, `worklet.ts`.


For VPN-enabled desktops, the [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans to resolve the Caddy target through [[Architecture/VPN runtime]] while continuing to authenticate with the desktop's token. Runtime target resolution and full proxy validation are not implemented yet.

The adapters expose explicit software-keyboard show/hide and local touch-view events, report actual assist-input focus and defer automatic focus until the frame is focused. On v1 clients, reviewed call sites keep the complete upstream touch lifecycle while routing classified pinches to local magnification and classified scrolling to local pan when magnified. Mouse Ctrl+wheel remains an application command. Gesture points are normalized to the stable top-level viewport; the client compensates trackpad origins when the transformed frame moves and reports predicted cursor coordinates for edge following. The requested touch mode survives input initialization/reconnect. Existing image bridges retain the old version-specific method wrappers until those images are upgraded. Real multi-touch browser tests cover both public touch behaviors; contract tests reject changed pinned source and capture consent crossing a reconnect.
