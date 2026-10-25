---
displayName: "VPN fleet observation fairness"
tags: "feature"
status: "discovery"
kind: "improvement"
availability: "unavailable"
summary: "Bounded shared poll/repair slots do not guarantee fair observation or fresh cached health for larger fleets."
outcome: "Observe and repair a larger VPN desktop fleet with explicit freshness and fairness guarantees."
acceptance:
  - "Observation and repair work cannot indefinitely starve an eligible desktop."
  - "Current connected/IP observations expire or are labelled stale under slot saturation."
next: "Define a target fleet and freshness guarantee, then separate/deduplicate repair and observation work as needed."
components:
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/VPN runtime]]"
---
[[Architecture/Lifecycle]] records five-second passes, eight shared slots, long repairs and a verified two-desktop scope. This is an explicit scalability follow-up, not a claim that a larger deployment is already verified.

# Implementation boundary
- `supervisor/internal/desktops/networking_reconcile.go`
- `supervisor/internal/desktops/networking.go`
