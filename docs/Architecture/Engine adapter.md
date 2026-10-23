---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Container engine]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The supervisor's own interface to the container engine, with one implementation now (Docker, via its Go SDK) and room for Podman. Nothing else in the supervisor talks to the engine.

Memory reclamation is an optional `engine.MemoryReclaimer` capability (`supervisor/internal/engine/memory.go`), implemented by `supervisor/internal/engine/docker/memory.go`. It requires rootful Linux Docker, cgroup v2 and swap available to the desktop, including when the supervisor connects from macOS to OrbStack. It requests 128 MiB through the target's `memory.reclaim` with `swappiness=100`, leaves all applications running, and returns resident/swap accounting before and after; resident includes file cache and excludes the host's compressed-swap footprint. Kernel `EAGAIN` is reported as partial success, not a failed request. The helper uses BusyBox `dd` to write the request because its `printf` builtin returns failure without printing the kernel error, which would prevent distinguishing partial success.

Two temporary containers of `alpine:3.22` (pulled if missing, then addressed by image ID) run only fixed supervisor shell programs: one reads a read-only bind of `/proc/<target-pid>/cgroup` in the host cgroup namespace; the other mounts only the validated desktop cgroup writable. The path must contain the exact full container ID and have no traversal. Both helpers have no network, all capabilities dropped, a read-only root filesystem, no host PID namespace and bounded CPU, memory, PIDs and output. The operation has a 35-second deadline and each helper gets independent cleanup time on success, failure or request cancellation. No root privileges are added to the desktop and no host swap settings or memory limits change. Helpers can remain as exited containers if the supervisor crashes before cleanup.

The [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] plans explicit network-namespace sharing, VPN runtime capabilities/devices, health inspection and restart-policy control for [[Architecture/VPN runtime]]. These extensions are not implemented yet.

The optional memory capability also exposes `ProbeMemoryReclaim` (`supervisor/internal/engine/docker/memory_support.go`). At boot it checks rootful Linux, cgroup v2, memory and swap controllers, prepares the helper image, and exercises the complete read-only discovery/writable target mount/reclaim/result/cleanup path on a tiny disposable container. This detects active swap, writable memory/swap interfaces, host cgroup namespace/mount permissions and kernel support for the swappiness option without relying on version numbers or touching existing desktops. Partial reclamation is sufficient for support detection. The probe has a 35-second operation deadline with independent cleanup; its failures are nonfatal to supervisor startup. Actual requests still recheck the platform and target, including desktop-specific swap limits.
