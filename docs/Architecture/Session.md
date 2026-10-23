---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Selkies]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The XFCE session on display `:1`, with its panel, terminal and file manager. Nothing opens by itself at start; the session saves and restores itself (windows, terminals with their folders) across restarts. Files: `images/debian-xfce/rootfs/usr/local/bin/start-session`, `images/debian-xfce/rootfs/etc/xdg/`.
