---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Deskyard CLI]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Live CPU and memory with 15 minutes of history, processes and screen thumbnails (via `deskyard screenshot`), streamed to the UI.


Stats workers in `supervisor/internal/server/stats.go` own an explicit runtime attachment (`collector.go`), identified by desktop ID, container ID and start time. Cancellation completes before a replacement worker starts; completion wakes reconciliation. Worker cleanup and sample publication are checked against that attachment, and demand timestamps are synchronized. Stats history remains in memory for the existing 15-minute window.

`supervisor/internal/server/events.go` closes a subscriber on event-queue overflow; EventSource reconnects and obtains an authoritative snapshot. Failed snapshot reads also close rather than leaving a healthy-looking stream with incomplete state. Queued desktop/removal notifications resolve current engine state before sending, so a payload queued before a snapshot cannot overwrite it with an older lifecycle state. Screen action telemetry retains its separate bounded, lossy contract.
