---
displayName: "Mobile access and application views"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "experimental"
summary: "Responsive controls and iPhone Home Screen mode coexist with an experimental application picker and browser-local focused-window view."
outcome: "Use a desktop from phone and tablet clients."
acceptance:
  - "Supported touch, keyboard, viewport and resolution controls remain usable in the verified environments."
  - "Focused application views preserve shared-screen input mapping and expose a full-desktop escape."
limitations:
  - "Full screen pixels are still transmitted; focus and input stay shared."
  - "Off-screen or covered pixels and outlying menus cannot be reconstructed by local cropping."
  - "Physical-device and comprehensive Android coverage remain incomplete."
components:
  - "[[Architecture/Application bridge]]"
  - "[[Architecture/Web UI]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Mobile 2026-10-23]]"
  - "[[Testing/PWA iOS 27]]"
  - "[[Testing/Mobile application prototype 2026-10-23]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/web/src/desktop/applications.tsx`
- `supervisor/web/src/desktop/window-view.ts`
- `supervisor/web/src/desktop/touch-controls.tsx`
- `supervisor/internal/server/applications.go`
- `images/common/rootfs/usr/local/bin/deskyard-apps`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
