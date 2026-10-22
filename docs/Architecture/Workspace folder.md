---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
`/workspace`, the single user's home and the only thing that persists: files, tools, Flatpak apps, the browser profile, the desktop session. A folder on the host, so it can be backed up; with the supervisor, one per computer under its data folder. It stays `/workspace` when the user is renamed.
