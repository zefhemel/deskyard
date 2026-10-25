---
displayName: State ownership and recovery design
tags: spec
status: implemented
date: "2026-10-24"
features:
  - "[[Features/Desktop lifecycle]]"
  - "[[Features/Accounts and access]]"
  - "[[Features/Shared desktop streaming]]"
  - "[[Features/Host folder mounts]]"
---
These contracts keep failed persistence, process disposal, runtime replacement and interrupted desktop operations from publishing misleading state. The implementation uses focused component boundaries rather than a generic storage/job framework.

# Persistent authorization and stores
Account, token and image-catalog mutations prepare independent next state, persist, then publish memory/notifications. Persistence failures remain visible. Account-triggered authorization loss closes live remote access even if token-file cleanup fails. Durable creator identity, role/grant revisions and current-authorization checks serialize correctly with token creation, preventing old-token resurrection after regrant, account recreation or restart. Removed image-authoring discovery/scanner code has no current design contract.

# Process and observer ownership
The engine runner owns concurrent draining, bounded capture, cancellation disposal, consumer failures and exit handling. HTTP helpers share that lifetime; consuming stdout on the caller goroutine preserves HTTP panic recovery. Disposal closes attachments but does not promise to terminate arbitrary commands. Shared-terminal leases separately own attachment process termination.

Collector handles belong to desktop/runtime identities. Canceled readers hand off cursors before same-runtime replacement; obsolete sample publication, cleanup, status and epoch responses cannot affect newer workers. Shared state is synchronized. Browser event WebSockets and HTTP SSE overflow close the feed for an authoritative reconnect snapshot, avoiding unsafe application of queued lifecycle deltas. Job SSE remains a separate transport.

The UI's `JobTracker` owns one subscription and retained state per job across local forms, discovered jobs and remounts. Terminal failure remains available to Settings rather than disappearing when the running-job map drops it. This and unified host-source preflight resolve the earlier review findings in commit `02898bb`; [[Testing/Code quality review 2026-10-24]] describes the historical audit scope.

# Replacement and host preflight
Direct replacement pins the previous immutable image while retaining its logical reference/settings. Recovery intent is privately, atomically written and synchronized before removing the authoritative container. Startup reconnects managed networks before restoring. Restoration has bounded independent contexts, requires readiness and retains failed journals. Acquire the canonical desktop reservation before checking recovery intent so a paused caller cannot bypass a newly retained record.

One networkless helper checks requested host folders and optional Docker socket together before disruption. Strict read-only mounts require existing sources; emitted checks distinguish folders from Unix sockets. Cleanup is independently bounded, survives request cancellation and reports its own failure. Direct and VPN create/recreate share this preflight. VPN policy recovery remains separate and never restores direct networking after an enabled selection fails.

# Streaming compatibility and evidence
Exact pinned Selkies source exposes versioned explicit input/media/transport hooks and a typed supervisor adapter. Build-time hashes/replacement counts guard compatibility. Native pass-through defaults retain image-served operation; legacy images retain the deliberate compatibility bridge. Unknown contract versions require an actionable upgrade response. New clients preserve browser constructors/prototypes, plain-HTTP fallbacks, capture authorization/badges, display ownership, clipboard and touch behavior.

Deterministic failure/ownership tests cover stores, authorization revisions, runner cancellation/draining, worker replacement, recovery ordering and preflight cleanup. Docker/browser evidence covers actual tmux/PTY, engine replacement, media and gestures. Supported-image smoke coverage is XFCE/MATE. Event-writer tests now cover heartbeat success, closed/non-reading peers, malformed JSON and write failure; [[Testing/CI transport regressions 2026-10-24]] records repeated race checks and their scope. Those old coverage findings are resolved, not a standing backlog.

Local checks do not establish every architecture/browser or Linux CI outcome. Dispatcher-aware microphone test transport changes still need their recorded native/container Linux rerun; Selkies X-input recovery has separate pending amd64 execution. Keep those verification limits with their evidence rather than treating the older repair completion as broad platform certification.
