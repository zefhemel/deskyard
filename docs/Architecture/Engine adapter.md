---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Container engine]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The supervisor's own interface to the container engine, with one implementation now (Docker, via its Go SDK) and room for Podman. Nothing else in the supervisor talks to the engine.

The engine foundations for the [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] are implemented in `supervisor/internal/engine/engine.go` and `supervisor/internal/engine/docker/docker.go`: `ContainerSpec.NetworkContainer` shares an existing container namespace, `CapAdd` grants only explicitly requested capabilities, inspection exposes the health state (without raw health logs), namespace owner and restart policy, and `UpdateRestartPolicy` changes that policy. Namespace sharing rejects independent networks, aliases and port publication and omits exposed ports and network endpoints. Ordinary desktop creates retain their existing networking and privileges. [[Architecture/VPN runtime]] lifecycle and policy enforcement are still planned.
