---
displayName: "Shared Chromium and automation"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "One persistent Chromium profile supports normal browser use, CDP and desktop screenshot/click/type commands."
outcome: "Use browser and screen tools within the same desktop as people."
acceptance:
  - "The shared profile persists and authenticated local/remote browser tools reach the intended desktop."
  - "Screen commands use the current display and activity-reporting contracts."
limitations:
  - "People and automation share the same live session and application credentials."
  - "Installed agent compatibility does not establish a provider-authenticated model conversation."
components:
  - "[[Architecture/Chromium]]"
  - "[[Architecture/Deskyard CLI]]"
  - "[[Architecture/Desktop tools]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `images/common/rootfs/usr/local/bin/start-chromium`
- `supervisor/internal/cli/app/cmd_desktop.go`
- `supervisor/internal/cli/cdp/cdp.go`
- `supervisor/internal/server/agent.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
