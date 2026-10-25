---
displayName: "Persistent workspace"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "The host-backed /workspace preserves files, browser profile and user-installed tools across container replacement."
outcome: "Return to personal work after restarts and image upgrades."
acceptance:
  - "Workspace files and supported user-installed applications survive container recreation."
  - "Home-template initialization does not overwrite existing personal files."
limitations:
  - "Persistence does not provide backup, undelete or full memory suspend."
  - "Session restoration depends on each application and desktop environment."
components:
  - "[[Architecture/Workspace folder]]"
  - "[[Architecture/Session]]"
  - "[[Architecture/Desktop tools]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Desktop variants 2026-10-23]]"
  - "[[Testing/RetroArch fresh desktop 2026-10-24]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/desktops/service.go`
- `images/common/rootfs/usr/local/bin/workspace-init`
- `images/common/rootfs/usr/local/bin/start-session`
- `images/tests/test_persistence.py`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
