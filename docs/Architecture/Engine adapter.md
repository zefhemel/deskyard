---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Container engine]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The supervisor's own interface to the container engine, with one implementation now (Docker, via its Go SDK) and room for Podman. Nothing else in the supervisor talks to the engine.

The engine foundations for the [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]] are implemented in `supervisor/internal/engine/engine.go` and `supervisor/internal/engine/docker/docker.go`: `ContainerSpec.NetworkContainer` shares an existing container namespace, `CapAdd` grants only explicitly requested capabilities, inspection exposes the health state (without raw health logs), namespace owner and restart policy, and `UpdateRestartPolicy` changes that policy. Namespace sharing rejects independent networks, aliases and port publication and omits exposed ports and network endpoints. Ordinary desktop creates retain their existing networking and privileges. [[Architecture/VPN runtime]] policy enforcement and [[Architecture/Lifecycle|paired lifecycle integration]] are implemented. The opt-in lifecycle fixture wraps engine lists, events and mutations with a random fixture label so reconciliation cannot touch unrelated shared-engine desktops.

Exec attachments now follow the calling context through output collection and exit inspection. `engine.Output` reads stdout/stderr and observes exit concurrently, closes the process attachment on cancellation and returns its context error rather than waiting indefinitely for stream EOF. Docker closes its hijacked connection and demultiplexing pipe writers on context cancellation or explicit Close; exit polling uses the same cancellable context. `Process.Close` implementations must unblock readers/writers and Wait. Direct HTTP fixture tests cover an open attachment, unread demultiplexed output and a stalled exit inspection.

`supervisor/internal/integration/scope.go` provides the full-supervisor fixture wrapper under `vpnintegration`. It adds a unique ownership label on creation, filters lists and events, checks container/network ownership before mutation and uses checked IDs, and rejects shared image build/tag/removal. Global network inventory remains visible read-only for policy reservation. The production adapter is unchanged by that test-only wrapper.
