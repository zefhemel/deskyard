---
displayName: Agent activity design
tags: spec
status: implemented
date: "2026-10-22"
plan: "[[superpowers/plans/2026-10-22-agent-activity|Agent activity plan]]"
---
People watching a desktop can see which agents are working on it and what they're doing:
- **Presence.** Agents appear next to the people viewing it, as "zef's agent", presence inferred from recent activity.
- **Summary.** Hovering or clicking an agent shows what it's doing now and what it did recently.
- **On the screen.** Its clicks and typing are drawn on the screen as they happen.

This covers agents connecting from outside through the agent API and agents running inside the desktop that use the in-desktop `deskyard` command. Builds on the [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]] (agent tokens, the agent API, who's viewing).

# Intent

Agents operate desktops in two ways:
- **Visual:** clicking, typing, using the browser.
- **In the background:** shell commands.

Today a person watching sees the mouse jump and windows change, with no idea who is doing it, and sees nothing of background work. The point is curiosity and a sense of what's happening on a shared desktop, not auditing or control.

# Goals

- **Agents show up as present** on a desktop, next to the people viewing it, as acting for an account. "zef's agent", labelled with the token's name. They look distinct from people.
- **Presence is inferred:**
  - present while something is running;
  - present for 5 minutes after the agent's last action;
  - then gone.
- **A summary of each agent.** Hovering, or tapping on a phone, shows:
  - **Now:** running commands with elapsed time, "using the browser", and port mappings.
  - **Recently** (last 15 minutes):
    - finished commands with their exit status and duration;
    - screen actions summarised by kind and count;
    - files copied in and out.
- **Screen actions drawn live** on the desktop for everyone viewing it:
  - click ripples with the agent's name;
  - marks for double-clicks, drags and scrolls;
  - a "<agent> is typing…" tag;
  - key caps for shortcuts.
- **Agents inside the desktop are covered too,** for everything they do through `deskyard`. They're named after the program that called it ("Claude Code", "Codex", or "a terminal") with "(in this desktop)".

# Non-goals

- **Auditing.** No permanent log, no export, no history beyond about 15 minutes, and nothing kept across supervisor restarts.
- **Showing contents:**
  - typed text;
  - command output;
  - browser URLs or page actions; browser use is just "using the browser";
  - file contents.
- **Controlling agents** from this view: pausing, stopping, revoking. Revoking stays on the Agents tab.
- **Seeing what inside agents do without `deskyard`:** plain shell commands, or their own xdotool.

# Anti-goals

These would make it a failure even if it worked:

- **Leaking secrets:** a typed password, a token on a command line, or anything typed becoming visible to viewers.
- **Overlay in the agent's own view:** overlay marks showing up in the agent's own screenshots or confusing its view. They're drawn in the viewer's page only.
- **Slowing agents down:** recording activity must never block, fail or slow an agent's command.
- **A way for a desktop to fake activity for an outside agent's name.** Inside reports are always labelled as inside.

# Decided

## What is recorded

- **Outside agents.** The supervisor records each agent API operation as it happens, keyed by token:
  - **exec:** the start and end of each command (a dropped connection ends it as "disconnected", with no exit code), with a display form of its command line (see Secrets), working directory, duration and exit code. Screen-action execs are the in-desktop `deskyard click|double-click|triple-click|mouse-down|mouse-up|move|drag|scroll|type|key|hold-key|screenshot|zoom`. They become screen events with their coordinates or key names, not command entries.
  - **browser:** "using the browser" while any CDP connection is open, or during an HTTP call to the CDP endpoint, and for 30 s after the last message. Browser activity keeps the agent present like any other action. Each session adds one "Recently" line ("used the browser · 4 min"). *(My call.)*
  - **cp:** direction, path and size.
  - **forward:** the active mappings (already shown with viewers).
- **Inside agents.** The in-desktop `deskyard`, acting locally, appends one JSON line per screen action, and per `exec`-like command it runs, to an activity stream in the desktop. That stream is a small ring buffer served by a local process.
  - The supervisor reads it through the desktop's Caddy at `/_activity/`, behind the per-desktop token like `/_audio/`, only while someone is viewing the desktop.
  - The inside agent's name comes from the calling process tree: `claude` gives "Claude Code", `codex` gives "Codex", and an interactive shell gives "a terminal". Otherwise it's the parent program's name.
  - Writing an event never blocks: if the stream isn't there or is full, the event is dropped.
- **Kept in memory only.** The supervisor keeps, per desktop, the last 15 minutes of events and what's running now. Nothing is written to disk.

## Telling the sources apart *(my calls)*

- **No double-counting.**
  - Every exec the supervisor starts sets `DESKYARD_ACTIVITY=off` in the command's environment: agent API execs, thumbnails, `deskyard size`, and the bar's key presses. The in-desktop `deskyard` records nothing when it's set.
  - So an outside agent's actions are counted once, by the supervisor, and the supervisor's own housekeeping never shows up as an agent.
  - The variable is inherited, so a `deskyard click` nested inside an outside agent's shell command isn't recorded inside either. The supervisor shows that command as a plain command.
- **Recognising screen actions from outside.**
  - An exec is a screen action when `argv[0]`'s base name is `deskyard` and the first non-flag argument is one of the screen subcommands. The global `-d NAME` flag is skipped.
  - Anything wrapped in a shell (`sh -c "deskyard click …"`) is a plain command.
  - Whatever the form, everything after a `type` word of `deskyard` or `xdotool` is masked in the display form and in logs, to the end of the display string: wrapped in sudo, env or time, in a shell string or subshell, or rejected by the parser (`deskyard type -- pw -h`).
  - Coordinates are absolute desktop pixels, as the CLI takes them, and are parsed per subcommand: `click x y [button]`, `drag x1 y1 x2 y2`, `scroll x y dir [n]`, and so on.
  - Marks are drawn when the action starts, whatever its exit code.
  - `screenshot` and `zoom` draw nothing; they count in the summary ("took 3 screenshots").
- **Inside agents.** There's one entry per program name per desktop: id `inside:<name>`, colour from that id, no owner. Two Claude Code sessions in one desktop share one avatar.
- **Outside agents** are keyed by token ID, so two tokens with the same name stay separate. Revoking or expiring a token removes its presence at once.
- **Commands from inside agents** that are recorded: `deskyard exec` and `cp` as commands, `deskyard shell` as one long-running command, and the screen subcommands. `deskyard` with `-d` uses a remote, so it goes through the supervisor and is counted there.

## Inside activity stream *(my calls)*

- **Format.** Events carry a sequence number and a timestamp. The in-desktop ring holds the last 15 minutes, capped at 5,000 events. When it's full, the oldest events are dropped.
- **Reading it.** The supervisor reads with a cursor and resumes where it left off. On its first connection it backfills the whole ring, so "Recently" and presence also cover what happened before anyone looked.
- **When it's read.** The supervisor reads while the desktop is running and at least one UI page is open (plus 3 minutes afterwards), or automatic memory reclamation is eligible. That's the same rule as stats collection, so wall tiles show inside agents too.
- **Pairing starts and ends.** A command's start and end events share an id. Superseding the original 15-minute expiry (2026-10-23): starts remain until their ends, an explicit idle status or an epoch reset, bounded by the existing count caps. The desktop ring separately retains running keys past history expiry and sends `{"kind":"status","commandsRunning":bool}` heartbeats every 15 seconds of inactivity; overflow stays busy until stream restart. Automatic memory reclamation requires a fresh explicit idle report, reconciles missing ends on idle, and invalidates readiness on subsequent activity. Legacy blank heartbeats cannot authorize automatic reclamation.
- **Untrusted input.** Masking runs in `deskyard` before an event is written. The stream is writable by anything running as `user`, so the supervisor treats it as untrusted:
  - it caps the length of every field;
  - it masks again;
  - it allows at most 50 events a second per desktop, after a burst as large as the ring (so a backfill is taken whole), dropping the rest;
  - it never uses inside events to change outside agents.

## Secrets

- **Typed text** is never recorded, from inside or outside. `type` events carry a character count only.
- **Key events** carry key names (`ctrl+l`, `Return`).
- **The display form of a command line:**
  - the program and its arguments, cut at 120 characters;
  - arguments that look secret masked as `•••`: values after `--password`, `--token`, `-p`, `--secret`, or `--key`; `KEY=value` with a secret-looking key; strings that look like tokens (`dyt_…`, `sk-…`, `ghp_…`, long base64 or hex);
  - environment variables passed to exec are never shown.

## Presence and the summary

- **Where agents appear.** `Desktop.agents` lists the agents present: `{"id","name","owner","inside":bool,"color","active":bool,"lastActive":time}`. It goes in Desktop JSON and in `viewers` events, next to `viewers`, `mics` and `forwards`.
  - Present means something is running, or there was activity in the last 5 minutes.
  - `active` means activity in the last 10 seconds.
- **How they look.** Agent avatars sit after the people's avatars:
  - the owner's initial with a small robot badge, for an outside agent;
  - a robot glyph, for an inside agent;
  - a dashed ring that pulses while `active`;
  - colour taken from the token ID, so it stays stable.
- **The summary.** Hover, or tap, opens a popover:
  - the name ("deploy-bot · zef's agent", or "Claude Code · in this desktop");
  - **Now:** running commands with elapsed time, "using the browser", and port mappings;
  - **Recently:** up to 20 lines, newest first, with similar screen actions merged ("clicked 14 times", "typed 3 times").
- **API.** `GET /api/desktops/{name}/activity` returns the summary data for every signed-in account that can see the desktop; agent tokens can't use it.
  - Live summary changes arrive as `activity` events on `/api/events`, at most once a second per desktop.
  - An agent with no events in the last 5 minutes and nothing running isn't listed. *(My call.)*

## On the screen

- **The overlay.** The desktop page draws an overlay above the screen frame, mapping desktop coordinates to the frame's displayed area. This works for both remote-resize and scaled (Lock size) display, and doesn't depend on the streamer (KasmVNC today, possibly Selkies).
- **The marks**, in the agent's colour:
  - **click:** a ripple, plus a name label that fades after 1.5 s;
  - **double and triple click:** two and three rings;
  - **drag:** a line from start to end;
  - **scroll:** an arrow in its direction;
  - **move:** nothing, to avoid noise;
  - **key:** a key cap ("⌃L") near the last click point, for 1 s;
  - **type:** "<name> is typing…" near the last click point, or at the bottom edge when there wasn't one. It shows while type events keep arriving, and hides 1.5 s after the last.
- **Live delivery.** Screen events go on their own stream, `GET /api/desktops/{name}/screen-events` (SSE), which only the desktop page opens. The supervisor produces them only while that stream has subscribers. They're not delayed for sync; a mark appearing slightly before the picture changes is fine. *(My call.)*
- **Placing marks.** The page reads the KasmVNC canvas inside the same-origin frame. It doesn't change the client's files. It takes the remote screen size from the canvas's own size and the displayed area from its on-screen position. Marks are placed at draw time; a mark outside the current screen is dropped. A future streamer needs the same two numbers. *(My call.)*

# Left to the builder

- The in-desktop activity process: a hidden `deskyard` subcommand under supervisord, its socket path, and the ring size.
- The supervisor's reader, including when it connects (only while someone views or the activity endpoint is asked for).
- The exact process-tree detection for inside agents' names.
- The secret-masking patterns. Keep them in one file, with tests.
- The overlay's drawing details, within the marks above.
- Tests:
  - an outside agent's exec, click and type show up as presence, summary and screen events, with typed text absent;
  - an inside `deskyard click` shows up as "a terminal (in this desktop)";
  - a command line with `--token abc` is masked;
  - presence disappears 5 minutes after the last action (with a shortened clock in tests).
