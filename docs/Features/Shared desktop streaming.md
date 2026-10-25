---
displayName: "Shared desktop streaming"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Authenticated Selkies video, shared pointer/keyboard, sizing, quality, sound, clipboard and supported devices run in the browser."
outcome: "Use the same live Linux desktop from multiple browser clients."
acceptance:
  - "Authorized viewers receive the live stream and can interact through the supported controls."
  - "Reconnection and display initialization retain documented bounded recovery behavior."
limitations:
  - "Viewers share input and focus; no control arbitration or help-consent workflow exists."
  - "Mic, camera and automatic clipboard need HTTPS; camera also requires host support."
components:
  - "[[Architecture/Selkies]]"
  - "[[Architecture/Desktop proxy]]"
  - "[[Architecture/Web UI]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/WebSocket events 2026-10-24]]"
  - "[[Testing/Desktop startup readiness 2026-10-24]]"
  - "[[Testing/MATE stream startup 2026-10-24]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/server/desktop.go`
- `supervisor/web/src/desktop/selkies.ts`
- `supervisor/web/src/selkies/adapter.ts`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
