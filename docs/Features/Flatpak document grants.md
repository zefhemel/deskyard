---
displayName: "Flatpak document grants"
tags: "feature"
status: "discovery"
kind: "improvement"
availability: "unavailable"
summary: "Matching portals exist, but the document portal cannot mount FUSE in the current engine configuration."
outcome: "Support applications that require document-portal file grants within the agreed desktop security model."
acceptance:
  - "Representative required applications can receive and use their selected file grants."
  - "The chosen engine/security changes are justified and do not silently weaken the container contract."
next: "Identify affected required applications and investigate a compatible portal/FUSE approach before approving runtime changes."
components:
  - "[[Architecture/Desktop runtime]]"
---
[[Architecture/Desktop runtime]] states the current limitation. LibreOffice graphical saving with permitted workspace access was verified; that result does not prove document-portal-dependent workflows.

# Implementation boundary
- `images/Dockerfile`
- `supervisor/internal/desktops/service.go`
