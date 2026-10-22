---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/Desktop]]"
  - "[[Architecture/Workspace folder]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The shared browser: one profile in the workspace folder for the human and the agent. Agents drive it over CDP on 9221 inside the computer (9222 for the host), through `cdp-connect`, which restarts Chromium if someone closed it. Files: `rootfs/usr/local/bin/start-chromium`, `rootfs/usr/local/bin/cdp-connect`.
