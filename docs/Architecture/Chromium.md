---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Session]]"
  - "[[Architecture/Workspace folder]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The shared browser: one profile in the workspace folder for everyone using the desktop. It starts on demand, when someone opens it or a CLI client connects over CDP (`127.0.0.1:9221` inside, through `cdp-connect`, which also restarts it), and comes back after a restart if it was open. Remote clients reach CDP through the [[Architecture/Remote access API]]. Files: `images/common/rootfs/usr/local/bin/start-chromium`, `cdp-connect`.

The session stop handler calls `stop-chromium` before native logout so the independently launched browser can flush its profile while X is available. Recovery of a previously open browser requests its last session and acknowledges crash recovery; without that acknowledgment Chromium can stop saving subsequent sessions. A deliberately closed browser removes its open marker and stays closed across restarts. Both shutdown and the launcher's open-marker cleanup identify the shared profile's main process by its exact profile argument and absence of a renderer/utility `--type` argument; lingering child processes cannot keep the marker open.

The MATE panel launcher references `/usr/local/share/applications/chromium.desktop`, the shared-profile entry. Debian also installs `/usr/share/applications/chromium.desktop`; using that absolute path bypasses Deskyard's wrapper.
