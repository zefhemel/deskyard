---
displayName: Code quality and recovery design
tags: spec
status: implemented
date: "2026-10-24"
plan: "[[superpowers/plans/2026-10-24-code-quality-recovery|Code quality and recovery plan]]"
---
Implement the repository-wide audit remedies approved in conversation on 24 October 2026. Preserve public behavior except where failed operations, missing recovery, or stale state currently violate their contracts.

# State and discovery
Accounts, token revocation, and image catalog mutations prepare independent next state, persist it, and only then publish memory changes and notifications. Persistence failures remain visible to callers. Account-triggered authorization loss closes live agent access independently of token-file cleanup; persistent creator identity and retained grant revisions prevent resurrection after regrant, account recreation or restart. Delayed callbacks evaluate current authorization while serialized with token creation. Flatpak discovery returns fully loaded key data, with no deferred I/O borrowing a canceled request context.

# Process ownership
One engine-level runner owns process I/O closure on cancellation, concurrent draining, bounded capture, consumer failure and exit handling. HTTP command helpers use this runner rather than reimplementing its lifetime. Cancellation disposes I/O; it does not promise to terminate arbitrary desktop processes. Shared-terminal leases retain their explicit termination contract.

# Runtime observers
Collectors own worker handles tied to desktop and runtime identity; an old worker cannot cancel, publish into, or remove a replacement. Shared state is synchronized. SSE queue overflow triggers a fresh authoritative snapshot rather than silently trusting an unrelated subsequent event.

# Desktop replacement
Lifecycle recovery pins the previous immutable image ID and handles replacement creation, start and readiness failures. A durable journal records recovery intent before deleting the authoritative container and startup reconciliation restores interrupted operations. Recovery records include only the necessary prior identity, settings, creation time, running intent and proxy token; files are private and atomically written. Recovery attempts have bounded independent contexts. Desktop operations expose a canonical reservation helper while preserving existing callers during migration.

# Selkies integration
Expose explicit hooks in the pinned client for the adapter's transport, input and media integration, with a versioned contract and build-time compatibility validation. Build the supervisor adapter as typed source. Replace private-method and browser-prototype interception where explicit hooks can own the behavior. Preserve plain HTTP playback, capture permissions/badges, display ownership, focus, clipboard and mobile gestures. Existing images need an explicit compatibility route or actionable upgrade behavior; silent feature loss is unacceptable.

# Tests and delivery
Use deterministic failure and ownership tests at the component boundary, keeping Docker/browser tests for actual engine, tmux/PTY, permissions, media and gesture contracts. Add small XFCE/KDE/MATE runtime smoke coverage without duplicating the full browser matrix. Run Go race tests, vet, UI typecheck/tests, build and relevant available integration tests. Update affected architecture pages and describe any unverified platform limits. No automatic merge, deployment or release is included.
