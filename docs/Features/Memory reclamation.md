---
displayName: "Memory reclamation"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Supported engines offer manual reclamation and one automatic attempt after a continuously viewer-free idle period."
outcome: "Reduce idle desktop memory without stopping applications."
acceptance:
  - "Reclamation preserves live applications on the supported cgroup-v2 engine path."
  - "Automatic attempts respect viewer generations, tracked work and lifecycle reservations."
limitations:
  - "Rootful Linux Docker, cgroup v2 and suitable swap support are required."
  - "Unreported application activity is outside detection; this is not checkpoint suspend."
components:
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Engine adapter]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/server/memory.go`
- `supervisor/internal/server/memory_idle.go`
- `supervisor/internal/engine/docker/memory.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
