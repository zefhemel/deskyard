---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/Desktop]]"
  - "[[Architecture/Workspace folder]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The shared browser: one profile in the workspace folder for the human and the agent. Agents inside drive it over CDP on `127.0.0.1:9221` through `cdp-connect`, which restarts Chromium if someone closed it. Today it's also on 9222 for the host; with the supervisor that listener goes and agents elsewhere reach CDP through the [[Architecture/Agent API]]. Files: `rootfs/usr/local/bin/start-chromium`, `rootfs/usr/local/bin/cdp-connect`.
