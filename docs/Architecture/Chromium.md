---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Session]]"
  - "[[Architecture/Workspace folder]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The shared browser: one profile in the workspace folder for the human and the agent. It starts on demand, when someone opens it or an agent connects over CDP (`127.0.0.1:9221` inside, through `cdp-connect`, which also restarts it), and comes back after a restart if it was open. Agents elsewhere reach CDP through the [[Architecture/Agent API]]. Files: `images/debian-xfce/rootfs/usr/local/bin/start-chromium`, `cdp-connect`.
