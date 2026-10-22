---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Tokens an admin creates on a computer's Agents tab, in `tokens.json`: one computer each, a name, an optional expiry, last use, stored as a SHA-256 of the secret. Revoking or expiry closes the token's open connections.
