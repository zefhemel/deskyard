---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent tokens]]"
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Monitoring]]"
  - "[[Architecture/Image catalog]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The JSON API behind the UI, plus login and sessions. Every handler checks the signed-in account through one function, `can(account, action, desktop)`, and state-changing calls check `Origin`.

The experimental [[Architecture/Application bridge]] adds authenticated application inventory and launch/window-activation routes in `internal/server/applications.go`. It executes a bounded helper as `user` in running desktops; application IDs and decimal X11 window IDs are validated before execution.

`POST /api/desktops/{name}/reclaim-memory` (`supervisor/internal/server/memory.go`) is a synchronous administrator-only action for running desktops. It excludes concurrent lifecycle/reclaim operations and returns resident memory and logical swap before/after a 128 MiB request. Unsupported hosts return 501, stopped/busy desktops 409 and helper failures 502; agent tokens cannot use this session route. See `supervisor/API.md` for the JSON contract.

The server probes memory reclamation before accepting requests, logs enabled requirements or the disabling reason, and caches the result for this process. `GET /api/host` exposes it as `memoryReclaim: {enabled, reason?}`. A failed startup probe gates the action with 501; runtime per-desktop failures do not disable the host-wide capability. Startup cancellation is propagated from the supervisor's signal context.

`PUT /api/desktops/{name}/memory-policy` (`supervisor/internal/server/memory_policy.go`) saves the administrator's `autoReclaim` preference by stable desktop ID without operating on its container. Desktop JSON/SSE carries the preference; [[Architecture/Lifecycle]] uses it to gate automatic reclamation independently of manual requests.
