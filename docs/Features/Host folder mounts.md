---
displayName: "Host folder mounts"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Administrators attach existing engine-host directories below /mnt with read-only or writable access and preflight checks."
outcome: "Expose explicitly selected host directories to a desktop."
acceptance:
  - "Host sources are checked before a running desktop is replaced."
  - "Mount settings persist across recreation and removal leaves source data intact."
limitations:
  - "Host UID permissions still apply and changes recreate the desktop."
  - "This does not provide named shared-folder grants or live membership reconciliation."
components:
  - "[[Architecture/Workspace folder]]"
  - "[[Architecture/Lifecycle]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/desktops/host_mounts.go`
- `supervisor/internal/desktops/host_preflight.go`
- `supervisor/internal/desktops/service.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
