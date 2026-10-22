---
tags: component
status: planned
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Container engine]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The supervisor's own interface to the container engine, with one implementation now (Docker, via its Go SDK) and room for Podman. Nothing else in the supervisor talks to the engine.
