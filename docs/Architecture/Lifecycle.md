---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Accounts]]"
  - "[[Architecture/VPN connections]]"
  - "[[Architecture/VPN runtime]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The lifecycle of desktops as jobs: create (reusing a folder that is already there), start, stop, restart, remove, change settings and upgrade. Legacy desktop settings are discovered from container labels. Managed network selections additionally persist full private recovery snapshots and desired power in `.deskyard/vpn/desktops.json`; list/get includes interrupted transitions even when the container is absent.

`supervisor/internal/desktops/networking.go` defines the provider-neutral runtime boundary, `networking_store.go` owns atomic snapshots, `networking_lifecycle.go` orders pair transitions, `networking_reconcile.go` repairs and observes them, and `networking_connections.go` coordinates saved revisions/references. Production uses the real VPN manager. Networking is configured, including the entire reserved allocation pool, before startup reconciliation in `cmd/deskyard-supervisor/main.go`.

Every lifecycle mutation uses one per-desktop operation lock, independent of the existing server busy/job marker. Before replacing a namespace the old desktop loses independent restart, stops, and desired intent is committed. Consumer removal precedes runtime removal; firewall/control readiness precedes creation in the returned namespace. Failed enabled transitions never restore a direct-network container. Stable ID, Caddy token, creation time, image, limits, camera and workspace survive recovery; a vanished host camera is removed through the same safe transition.

Central bounded observations and owned engine events publish changed public snapshots without per-viewer probes. A tunnel reconnect changes health, not desktop power. Stopping keeps the gateway idle; removing tears down the owned pair and intent while retaining the reusable connection and respecting the workspace deletion choice. Shared revision edits validate first, confirm the revision/reference preview, then report each desktop result. Assignment and deletion use the same coordination boundary. Explicit disable preserves omitted LAN rules for reuse.
