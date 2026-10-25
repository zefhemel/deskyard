---
displayName: "Monitoring and CLI activity"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "The UI displays resource history, processes, viewers and bounded tracked CLI activity with screen action marks."
outcome: "Understand what each desktop and its connected clients are doing."
acceptance:
  - "Authorized viewers receive resource snapshots and live event updates."
  - "Tracked actions mask supported secrets and use bounded retention."
limitations:
  - "Resource and action history is in memory and not a durable audit log."
  - "Arbitrary background work can be outside tracked CLI visibility."
components:
  - "[[Architecture/Monitoring]]"
  - "[[Architecture/CLI activity]]"
  - "[[Architecture/Activity stream]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/WebSocket events 2026-10-24]]"
  - "[[Testing/CI transport regressions 2026-10-24]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/server/collector.go`
- `supervisor/internal/server/events.go`
- `supervisor/internal/activity/tracker.go`
- `supervisor/web/src/lib/event-feed.ts`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
