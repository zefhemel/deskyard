---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent API]]"
  - "[[Architecture/Activity stream]]"
  - "[[Architecture/Web UI]]"
spec: "[[superpowers/specs/2026-10-22-agent-activity-design|Agent activity design]]"
---
Who is acting on a desktop and what they did, shown as presence in the viewers popover and as marks on the screen. The supervisor keeps it in memory only (`activity.Tracker`): per desktop, the last 15 minutes of events and what is running now. An agent is present while something runs and for 5 minutes after its last action; it is active for 10 seconds after an action.

Outside agents (agent tokens) are recorded by the supervisor's agent API handlers: exec, browser (CDP) and port forwards. Every exec the supervisor starts sets `DESKYARD_ACTIVITY=off`, so the in-desktop `deskyard` does not record it a second time.

Inside agents are programs in the desktop (`claude`, `codex`, a shell, anything else): the in-desktop `deskyard` sends their actions to the [[Architecture/Activity stream]]. While any UI page is open, and 3 minutes after the last one closes, the supervisor follows the stream of each running desktop through `/_activity/events` (`internal/server/activity_inside.go`), resuming from a per-desktop cursor (epoch and sequence number) so a reconnect loses nothing and a restarted stream is noticed. A desktop that stops drops its running inside commands. An image without the route answers 404; the supervisor tries again after a minute, and after other errors with a back-off from 1 to 30 seconds.

The stream is fed by any process in the desktop, so it is hostile input: lines over 8 KiB are skipped, every event goes through `activity.Sanitize`, and the tracker takes inside events from a token bucket per desktop: a burst of 5,000 (the whole ring, so a backfill or catch-up is taken in full), then 50 a second, and holds at most 32 inside agents and 256 running inside commands. Inside events never touch outside agents.

The result is `Desktop.agents`, `GET /api/desktops/{name}/activity`, `activity` events on `/api/events`, and the screen-events stream the [[Architecture/Web UI]] draws marks from.
