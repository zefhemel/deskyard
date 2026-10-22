---
tags: component
status: planned
uses:
  - "[[Architecture/Agent API]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The `computer` command run outside a computer: remotes (`computer remote add alice URL --token …`), `exec`, `shell`, `cp`, `browser` (a local CDP port, plus direct commands) and every desktop action, over HTTPS and WebSockets. Self-describing (`--help`, `guide`, `describe --json`) so an agent can learn it alone. The same binary as the [[Architecture/Computer CLI]].
