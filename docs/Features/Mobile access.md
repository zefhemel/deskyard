---
displayName: "Mobile access"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "experimental"
summary: "Responsive toolbar, touch, keyboard, local magnification and remote resolution controls plus iPhone Home Screen mode make the full shared desktop usable from phones and tablets."
outcome: "Use a desktop from phone and tablet clients."
acceptance:
  - "Supported touch, keyboard, viewport and resolution controls remain usable in the verified environments."
limitations:
  - "Full screen pixels are still transmitted; focus and input stay shared."
  - "Physical-device and comprehensive Android coverage remain incomplete."
components:
  - "[[Architecture/Web UI]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Mobile 2026-10-23]]"
  - "[[Testing/PWA iOS 27]]"
  - "[[Testing/Mobile application prototype 2026-10-23]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

The experimental application picker and focused-window crop were removed on 26 October 2026 at the owner's request, together with the image's `deskyard-apps` helper and the `/api/desktops/{name}/applications` routes. Mobile use now always shows the full desktop, with local pinch magnification for detail.

# Implementation
- `supervisor/web/src/desktop/touch-controls.tsx`
- `supervisor/web/src/desktop/zoom-viewport.tsx`
- `supervisor/web/src/desktop/view-zoom.ts`
- `supervisor/web/src/desktop/viewport.ts`
- `supervisor/tests/test_ui_touch.py`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
