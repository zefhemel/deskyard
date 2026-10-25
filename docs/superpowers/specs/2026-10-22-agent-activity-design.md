---
displayName: CLI activity design
tags: spec
status: implemented
date: "2026-10-22"
features:
  - "[[Features/Monitoring and CLI activity]]"
  - "[[Features/Memory reclamation]]"
---
CLI activity shows who is operating a desktop through Deskyard, their current/recent work, and live screen-action marks. It supports remote token clients and local programs using the in-desktop CLI. This is an ephemeral shared-desktop aid, not an audit log or a monitor of arbitrary shell commands.

# Sources and privacy
Remote operations are recorded by supervisor handlers and keyed by token ID. Local `deskyard` operations emit to the desktop activity service, identified by calling program and visibly labelled as local. Supervisor-issued execs set `DESKYARD_ACTIVITY=off` to prevent duplicate and housekeeping events. Shell-wrapped screen commands remain ordinary command entries; local activity never changes remote-token identities.

Typed text and command output are absent. Type events contain counts; key events contain key names. Display command lines are bounded and secret-like arguments masked. Browser use is summarized without page URLs/content. File transfers expose direction/path/size rather than file contents. Desktop reports are untrusted: sanitize fields and secrets again, bound input, limit local programs/running commands and apply a per-desktop rate limit. Recording is best effort and must not fail the user's CLI command.

# Presence and lifetime
The supervisor keeps 15 minutes of event history in memory, plus running commands. Presence lasts while work runs and for five minutes after activity; the active indication lasts ten seconds. Restart loses this ephemeral history. Revoked/expired remote tokens disappear immediately.

The desktop ring retains a cursor/epoch and up to 5,000 events. The supervisor resumes readers, backfills available history, and discards obsolete runtime state after container/runtime replacement. Local starts outlive history expiry until an end, explicit idle status or epoch reset. Explicit 15-second idle heartbeats include long-running command state; plain legacy keepalives cannot authorize idle reclamation. Lost ends reconcile on an explicit idle status. Any subsequent activity invalidates readiness until a fresh status arrives.

Readers follow running desktops while UI pages are open, for three minutes afterwards, and while automatic reclamation needs activity evidence. Automatic reclaim requires an explicit report younger than 45 seconds and checks running/recent command, browser and forward activity. Activity outside Deskyard remains unobserved. Runtime-owned workers serialize cursor handoff and prevent obsolete cleanup or epoch responses from resetting replacements.

# Delivery and display
Existing compatibility fields retain `Desktop.agents` and `/api/desktops/{name}/activity`; user-facing wording is CLI activity. Presence, current commands, browser sessions, port mappings and recent summaries appear beside viewers. Browser `/api/events` and per-desktop `screen-events` feeds use same-origin session-authenticated WebSockets with named JSON envelopes; ordinary HTTP consumers retain SSE. Permission checks filter each desktop and revoke idle subscriptions. Overflow closes the feed so reconnect obtains an authoritative snapshot.

The screen overlay is browser-local, mapped from remote screen coordinates into the displayed Selkies surface; it never enters desktop screenshots. Click/double/triple-click ripples, drag/scroll marks, shortcut caps and typing indicators are transient. Move-only events do not create noise. Screenshot/zoom events count in summaries without a mark. Screen events are produced only for subscribers.

Implementation: `supervisor/internal/activity/`, `supervisor/internal/server/activity*.go`, `ui_events.go`, and the UI activity components. [[Architecture/CLI activity]], [[Architecture/Activity stream]] and [[Architecture/Web UI]] define component ownership. Existing heartbeat/write-failure and shared job-state review gaps were covered by later repairs; [[Testing/CI transport regressions 2026-10-24]] records their scoped checks and remaining Linux media verification.
