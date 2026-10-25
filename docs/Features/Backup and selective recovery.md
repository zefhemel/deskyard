---
displayName: "Backup and selective recovery"
tags: "feature"
status: "discovery"
kind: "capability"
availability: "unavailable"
summary: "Workspace persistence and failed-replacement rollback exist; product backup and selective restore workflows do not."
outcome: "Recover deleted or corrupted personal work without erasing newer work."
acceptance:
  - "Backup scope, retention and loss tolerance are agreed for a real deployment."
  - "A restore demonstrates the agreed recovery granularity and preserves unrelated current work."
next: "Define the actual loss/recovery needs and backup ownership before choosing a mechanism."
components:
  - "[[Architecture/Workspace folder]]"
  - "[[Architecture/Lifecycle]]"
---
[[Use cases#F4: Recover from mistakes and device loss|Family recovery]] and company continuity needs remain discovery. External backups of host folders are possible; persistence across recreation is not a backup system.

# Implementation boundary
- `supervisor/internal/desktops/service.go`
- `supervisor/internal/desktops/recreate.go`
