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
