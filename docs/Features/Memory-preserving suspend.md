---
displayName: "Memory-preserving suspend"
tags: "feature"
status: "parked"
kind: "capability"
availability: "unavailable"
summary: "Minimal checkpoint/restore passed, but the full desktop with sandboxed Chromium did not checkpoint on the tested stack."
outcome: "Suspend a full desktop to disk and resume its application memory safely."
acceptance:
  - "The complete desktop and sandboxed browser checkpoint and resume with usable display/input and filesystem watches."
  - "Recovery, storage and supported-host limits are designed and verified."
next: "Revisit only if a suitable runtime/CRIU stack resolves the documented full-desktop blockers."
components:
  - "[[Architecture/Lifecycle]]"
---
[[superpowers/spikes/2026-10-23-desktop-suspend|The suspend investigation]] documents tested runtime, nested PID namespace and watch blockers. Memory reclamation is implemented separately and is not full desktop suspend.

# Implementation boundary
- `docs/superpowers/spikes/2026-10-23-desktop-suspend.md`
