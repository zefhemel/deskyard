---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent activity]]"
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Accounts]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The lifecycle of desktops as jobs: create (reusing a folder that's already there), start, stop, restart, remove, change settings, upgrade. Desktops and their settings are container labels; the engine is the only list of them.

Start/stop/restart and the manual and automatic memory-reclaim actions reserve the same desktop operation slot used by recreate/remove jobs, so they cannot overlap. Reclamation reserves the existing running state without stopping apps, closing viewers or creating a lifecycle job. `supervisor/internal/server/memory.go` enforces administrator access, checks the running state and calls the optional [[Architecture/Engine adapter]] capability. Startup missing-camera recovery retains its own reservation; power-request camera recovery reuses the request's reservation.

The [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans paired desktop/VPN-runtime lifecycle and durable per-desktop network intent for transition recovery. VPN-enabled desktops will start only after firewall readiness and will need coordinated recreation when their shared network namespace is replaced. This is planned behavior, not the current lifecycle implementation.

Automatic reclamation (`supervisor/internal/server/memory_idle.go`) checks every 30 seconds, serially, after 15 minutes continuously without viewers. Viewer join generations catch even joins/leaves between checks. Container identity/start time, supervisor restart and policy re-enabling reset the timer; each actual attempt consumes the absence's allowance, including failures. Busy operations, tracked agent work and missing/stale idle status skip without consuming it. After acquiring the operation slot, it rechecks desktop identity/running state, viewers, policy and agent activity. A viewer returning during an in-flight request does not interrupt applications or prevent reconnection.

The default-on per-desktop preference lives in `memory-policy.json`, keyed by stable desktop ID (`supervisor/internal/server/memory_policy.go`), so changing it needs no container recreation and upgrades retain it. The [[Architecture/API]] and [[Architecture/Web UI]] expose the switch. Automatic reclamation uses the [[Architecture/Agent activity]] tracker and an explicit idle [[Architecture/Activity stream]] heartbeat; legacy/missing/stale streams fail closed. Work that bypasses Deskyard activity reporting remains outside this detection.

Per-desktop host-folder settings are validated and preflighted before the current container stops. They are stored in the `deskyard.setting.mounts` label and carried through the existing image/limit/camera recreation paths. Applying a mount change recreates the container, restarting a running desktop and preserving stopped state. Invalid saved mount labels prevent recreation rather than silently dropping configuration. Failed replacement restores the old mounts and attempts to restart the restored container when the old desktop was running. Detachment never deletes source data; file deletion remains scoped to the desktop's private workspace. Implementation: `supervisor/internal/desktops/host_mounts.go` and `supervisor/internal/desktops/service.go`.
