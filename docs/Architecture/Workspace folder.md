---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
`/workspace`, the single user's home and the only thing that persists: files, tools, Flatpak apps, the browser profile, the desktop session, caches. A folder on the host under the supervisor's data folder (`<data>/<name>`), so it can be backed up; creating a computer whose folder exists reuses it.
