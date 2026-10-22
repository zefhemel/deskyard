---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Accounts and access in `accounts.json`: username, argon2id hash, admin flag, session epoch, and the IDs of the computers the account may use. Written atomically; the first start creates an admin.
