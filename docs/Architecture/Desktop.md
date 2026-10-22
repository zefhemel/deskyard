---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/KasmVNC]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The XFCE session on display `:1`, with its panel, terminal and file manager. It saves and restores itself (windows, terminals with their folders) across restarts. Files: `rootfs/usr/local/bin/start-session`, `rootfs/etc/xdg/`.
