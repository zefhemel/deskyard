---
tags: component
status: built
uses:
  - "[[Architecture/Agent API]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The `deskyard` command run outside a desktop: remotes (`deskyard remote add alice URL --token …`), `exec`, `shell`, `cp`, `browser connect` (a local CDP port for Playwright and friends) and every screen action, all built on the agent API's exec WebSocket and CDP. Self-describing (`--help`, `guide`, `describe --json`) so an agent can learn it alone. The same binary as the [[Architecture/Deskyard CLI]].
