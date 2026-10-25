---
displayName: "Named shared folders"
tags: "feature"
status: "discovery"
kind: "capability"
availability: "unavailable"
summary: "Per-desktop host mounts exist; a named sharing registry and live grant reconciliation do not."
outcome: "Administrators grant selected desktops access to a named shared folder."
acceptance:
  - "Grants are explicit per desktop and revocable without deleting shared data."
  - "Permissions and recovery are verified on the supported host storage platforms."
next: "Design the named registry and membership/recovery contract using the mount feasibility findings."
components:
  - "[[Architecture/Workspace folder]]"
---
The [[superpowers/spikes/2026-10-23-shared-folders|Shared folders investigation]] found supervisor-controlled live mounts feasible on tested storage. ACL-only isolation was unsuitable on tested Mac-backed folders. The implemented host-directory settings are separate from this proposed registry.

# Implementation boundary
- `supervisor/internal/desktops/host_mounts.go`
