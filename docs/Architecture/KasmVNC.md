---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
X server, VNC server and web client in one (`Xkasmvnc` on display `:1`, `-AlwaysShared` so several viewers share it). Its web server listens on `127.0.0.1:6080` only, behind [[Architecture/Caddy]]. The supervisor serves its web client from the computer's image, not from the computer. Files: `images/debian-xfce/rootfs/usr/local/bin/start-xvnc`.
