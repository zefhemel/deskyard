---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent tokens]]"
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Monitoring]]"
  - "[[Architecture/Image catalog]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The JSON API behind the UI, plus login and sessions. Every handler checks the signed-in account through one function, `can(account, action, computer)`, and state-changing calls check `Origin`.
