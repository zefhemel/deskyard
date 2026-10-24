---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Accounts and access in `accounts.json`: username, argon2id hash, admin flag, session epoch, and the IDs of the desktops the account may use. Mutations in `supervisor/internal/accounts/accounts.go` prepare independent account and access slices, write atomically, then publish live state. A failed write preserves live accounts and emits no change notification; callers receive the error. Login timestamps are best effort, with failures logged. The first start creates an admin only after its state is saved.

`supervisor/internal/auth/session.go` verifies signed cookie claims through one parser shared by HTTP sessions and browser terminals. Each new login has a random signed ID, retained during sliding refresh, plus account epoch and absolute expiry. Legacy cookies migrate while retaining their signed identity. Logout requires same-origin Origin and cancels terminals for that login; epoch/password changes, deletion or access revocation cancel affected terminals through `supervisor/internal/server/terminal_connections.go`. A socket retains its original expiry even if the cookie is later refreshed.

Accounts also persist agent-token authorization revisions: role changes advance an account-wide revision, and removal of a desktop grant advances that desktop's revision without resetting it on regrant. Account creation time distinguishes recreated usernames. These revisions are independent of browser session epochs and password changes; see [[Architecture/Agent tokens]].
