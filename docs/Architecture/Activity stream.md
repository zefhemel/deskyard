---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Caddy]]"
  - "[[Architecture/Deskyard CLI]]"
spec: "[[superpowers/specs/2026-10-22-agent-activity-design|Agent activity design]]"
---
What programs inside a desktop did, kept for the last 15 minutes so the supervisor can show it next to the outside agents. The hidden command `deskyard activity-serve` runs in the desktop as `user` (a supervisord program, `[program:activity]`), never as root. It listens on `127.0.0.1:6092`: UDP takes events in, HTTP gives them out.

In: any program in the desktop can send one JSON event per UDP datagram (at most 4,096 bytes). It is untrusted input. Each event goes through `activity.Sanitize` (fields capped, control characters removed, commands masked again, typed text never kept); anything that fails is dropped, as are unparsable and oversized datagrams. A sender never waits: UDP just drops when nobody listens. Accepted events go into a ring (`activity.Ring`) of the last 15 minutes and at most 5,000 events, each stamped with a sequence number from 1 and a time. Nothing is written to disk.

Out: `GET /events?epoch=E&after=N` answers `200` with `X-Activity-Epoch` and `application/x-ndjson`: one event per line, first everything newer than `N` (everything when `E` isn't the ring's current random epoch, which changes when the process restarts), then live events as they arrive, with an empty line every 15 seconds. Any other path is 404. The desktop's [[Architecture/Caddy]] serves it as `/_activity/events` behind the `X-Deskyard-Token` check, unbuffered. The image contract stays `1`: a supervisor reading an image without the route gets 404 and backs off.

Files: `supervisor/internal/activity/ring.go`, `supervisor/internal/cli/app/cmd_activity.go`, `images/debian-xfce/rootfs/etc/supervisor/conf.d/workspace.conf`, `images/debian-xfce/rootfs/etc/caddy/Caddyfile`.
