---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Accounts]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The lifecycle of desktops as jobs: create (reusing a folder that's already there), start, stop, restart, remove, change settings, upgrade. Desktops and their settings are container labels; the engine is the only list of them.
