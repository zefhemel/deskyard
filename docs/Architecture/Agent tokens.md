---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Tokens anyone with access creates on a desktop's Agents tab, in `tokens.json`: one desktop each, a name, an optional expiry, last use, stored as a SHA-256 of the secret. Revoking or expiry closes the token's open connections.

`supervisor/internal/server/tokens.go` persists prepared token additions and explicit revocations before publishing state or closing connections. A failed explicit revocation returns an error and leaves the token and its connections unchanged; the delete API reports the failure. Last-use timestamps publish only after a successful best-effort write.

Each token binds its creator's account creation identity, account role revision and desktop access revision. `supervisor/internal/server/agent.go` checks current creator existence, access and these revisions before authentication and again after registering the connection. Account-change cleanup reads current creator identity and revisions while holding the token-store lock, so a delayed notification cannot revoke tokens created under a newer grant. Account deletion, role changes or desktop access revocation close affected agent streams immediately, even if token-file cleanup fails; failures are logged. Persisted account revisions prevent retained tokens from reviving after access is regranted or a username is recreated. Revoking one desktop's grant preserves tokens for other desktops; role changes invalidate the account's tokens, while password changes preserve them.

At startup, legacy tokens without creator bindings migrate only when the current creator predates the token, still has access and has no retained role or desktop revocation revision. Invalid bindings are purged. Migration and cleanup must persist before requests are accepted, so a failed startup write fails closed. Desktop removal reports failed token cleanup.
