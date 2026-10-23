---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
`/workspace`, the single user's persistent home managed by Deskyard: files, tools, Flatpak apps, the browser profile, the desktop session, caches. A folder on the host under the supervisor's data folder (`<data>/<name>`), so it can be backed up; creating a desktop whose folder exists reuses it.

Named shared folders between desktops are not implemented. The [[superpowers/spikes/2026-10-23-shared-folders|Dynamic shared folders on Mac and Linux investigation]] found that ACL-based isolation is unsuitable for the tested Mac-backed storage. Supervisor-managed per-desktop mounts are the recommended direction, retaining the existing UID and host-folder model; live operations need engine capability detection and durable reconciliation.

Per-desktop host folders are implemented separately from named sharing. Admins configure `{source, target, readOnly}` mounts in Settings; sources are existing directories on the engine host and targets stay below `/mnt/`, outside the home so initialization cannot seed or chown them. Removing a mount leaves its source untouched; desktop deletion still affects only its private workspace when requested. Host folders elsewhere persist independently and are excluded from image-from-desktop templates and the supervisor's workspace file browser. `supervisor/internal/desktops/host_mounts.go` validates and preflights them; the `deskyard.setting.mounts` container label preserves configuration across recreation. See [[Architecture/Lifecycle]] and [[Architecture/Engine adapter]].
