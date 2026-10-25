---
displayName: "Desktop lifecycle"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Create, configure, start, stop, restart, upgrade and remove desktops with persistent replacement recovery."
outcome: "Maintain desktops centrally while preserving their private workspace."
acceptance:
  - "Authorized lifecycle actions use the current access model and report operation outcomes."
  - "Replacement preserves identity and workspace and exposes recovery failures."
limitations:
  - "Replacement ends application memory and shared terminal sessions."
  - "Missing images or host sources can block recovery."
components:
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Engine adapter]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Desktop startup readiness 2026-10-24]]"
  - "[[Testing/CI transport regressions 2026-10-24]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/desktops/service.go`
- `supervisor/internal/desktops/recreate.go`
- `supervisor/internal/desktops/recreate_recovery_test.go`
- `supervisor/web/src/pages/details/settings-tab.tsx`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
