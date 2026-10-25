---
displayName: "Forwarding readiness reporting"
tags: "feature"
status: "discovery"
kind: "defect"
availability: "unavailable"
summary: "The CLI prints Linked before the remote helper is known to be running."
outcome: "A forwarding success message reflects verified helper readiness."
acceptance:
  - "Success is reported only after the helper is ready."
  - "Helper startup failure and reconnect behavior have deterministic regression coverage."
next: "Define a readiness acknowledgement across the forwarding link and add a failing regression before implementation."
components:
  - "[[Architecture/Port forwarding]]"
---
The current limitation is documented in [[Architecture/Port forwarding]]. Port forwarding itself remains available; this record tracks the narrower reporting gap.

# Implementation boundary
- `supervisor/internal/cli/app/cmd_forward.go`
- `supervisor/internal/server/forward.go`
