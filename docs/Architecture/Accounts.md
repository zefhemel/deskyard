---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Accounts and access in `accounts.json`: username, argon2id hash, admin flag, session epoch, and the IDs of the desktops the account may use. Written atomically; the first start creates an admin.

`supervisor/internal/auth/session.go` verifies signed cookie claims through one parser shared by HTTP sessions and browser terminals. Each new login has a random signed ID, retained during sliding refresh, plus account epoch and absolute expiry. Legacy cookies migrate while retaining their signed identity. Logout requires same-origin Origin and cancels terminals for that login; epoch/password changes, deletion or access revocation cancel affected terminals through `supervisor/internal/server/terminal_connections.go`. A socket retains its original expiry even if the cookie is later refreshed.
