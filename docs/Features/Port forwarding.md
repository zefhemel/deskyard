---
displayName: "Port forwarding"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Authenticated WebSocket links carry bidirectional loopback TCP mappings with reconnect and bounded multiplexing."
outcome: "Reach services across the client and desktop boundary."
acceptance:
  - "Local and reverse mappings use the documented loopback and authentication policy."
  - "Mapping, link and stream limits remain enforced."
limitations:
  - "The CLI reports Linked before it knows the helper is ready."
  - "Mappings are TCP; this is not arbitrary network routing."
components:
  - "[[Architecture/Port forwarding]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/forward/mux.go`
- `supervisor/internal/cli/app/cmd_forward.go`
- `supervisor/internal/server/forward.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
