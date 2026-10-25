---
displayName: Shared terminal design
tags: spec
status: implemented
date: "2026-10-23"
features:
  - "[[Features/Shared terminals]]"
---
Browser, mobile, remote CLI and native desktop clients can join the same named Bash shell through xterm.js and tmux. Disconnecting detaches one client; shells/jobs persist until their shell exits or the container stops/restarts. This is shared process state, not a separate shell per connection.

# Sessions and native defaults
`/<name>/terminal/?session=NAME` and `deskyard terminal [session]` select a named shell, default `main`. Names match `[A-Za-z0-9][A-Za-z0-9_-]{0,63}` and are targeted exactly, never by prefix. Ordinary native windows/tabs allocate independent readable `terminal-N` names and preserve the initial working directory. XFCE/MATE startup migrates default terminal preferences without changing explicit emulator command/restoration argv or personal tmux configuration.

The image owns a private runtime tmux socket/configuration with no status line and `window-size latest`. Other clients retain readable font sizes and can scroll when another client supplies shared dimensions. No separate terminal HTTP listener, credentials or patched terminal vendor assets are required. Current browser dependencies are xterm.js 6.0.0/addon-fit 0.11.0 with notices. History uses tmux copy mode, PgUp/PgDn and Esc; native touch scroll is not claimed fixed.

View-authorized `GET /api/desktops/{name}/terminal/sessions` returns sorted public names only, including detached live shells and excluding private attachment groups. It runs an owned bounded helper and exposes no input/content/environment. The Sessions picker offers explicit joins, active indication and separately validated creation. It refreshes on opening and every five seconds after completion while visible; closing/hiding/changing session cancels and invalidates stale requests. Errors retain useful last-known names and provide retry.

Within native tmux, Ctrl-B then s opens a public-session chooser and switches only that native client; its title follows the selected name. Browser and remote CLI groups retain connection ownership: switch through browser Sessions or detach/reconnect rather than the native chooser. Advanced explicit tmux commands remain trusted shell capabilities.

# Authorization and wire contract
`GET /api/desktops/{name}/terminal?session=main` upgrades only after login, View permission, running-state and explicit same-origin validation. Optional image label `deskyard.terminal=tmux-v1` advertises support without changing contract 3. Old images receive an actionable upgrade error before upgrade. Browser terminal clients count as viewers and inhibit automatic idle reclamation.

The first client text frame is `{"cols":80,"rows":24}`; subsequent text carries `{"resize":[80,24]}` or `{"ack":N}`. Binary frames carry raw stdin/stdout bytes. Server text reports ready with runtime generation, exit code or error. Sizes are 2–500 columns and 1–300 rows. Input frames are capped at 64 KiB; output uses 16 KiB chunks and at most 256 KiB unacknowledged per connection. Acknowledge only after xterm's write callback. Never queue/replay disconnected input. Generation includes container ID and StartedAt so same-container restarts explain lost jobs.

Startup/writes are bounded and canceled with the connection. Reconnect uses bounded backoff; authentication/protocol errors stop retrying. Shell exit offers explicit reopen rather than silently creating another shell. Logout, account/access/session changes, expiry, desktop lifecycle and shutdown close affected attachments; authorization is revalidated after registration to close races. Terminal content/input never enters logs or activity. Automatic clipboard escape handling and unsafe link schemes are disabled.

# Process ownership
Closing a Docker TTY exec socket alone leaves its tmux client alive. Each remote attachment therefore uses a non-TTY `terminal-lease SESSION ATTACHMENT_ID` and TTY `terminal-attach ATTACHMENT_ID`, with a supervisor-generated random ID and fixed owned argv, as `user` in `/workspace`.

The stdin lease owns a private `_deskyard_<ID>` grouped session sharing persistent base-session windows. EOF/cancellation removes only that exact group, not shared panes or other clients. Creation completes with bounded cleanup; attach cannot recreate a canceled group. Docker propagates non-TTY EOF even after supervisor death. Readiness and CLI input wait for exact-client registration plus first tmux output, preserving that output. Explicit stdin EOF detaches; Ctrl-D bytes retain shell behavior. Local/native attachments use direct tmux. Shared leases have a termination contract stronger than arbitrary exec disposal.

# Mobile layout and acceptance
Use xterm's composition-aware input, native selection/paste and one input stream. At least 16px mobile input avoids focus zoom. Ctrl-C/D/L, Esc, Tab, arrows and one-shot Ctrl/Alt helpers use accessible 44px controls, retain keyboard focus, and reset modifiers after use/disconnect. Do not autofocus after reconnect or picker use. Layout follows visualViewport/safe areas without recreating the terminal across rotation.

Short heights use one horizontally scrollable helper/navigation row. The minimum budget is 68px: 24px readable terminal plus 44px actions. Below it, an instruction asks for portrait or keyboard dismissal without unmounting transport/xterm or deliberately blurring native input. Native iPhone acceptance verified keyboard retention and usable portrait return. This does not promise usable terminal rows below the budget.

[[Testing/Shared terminal 2026-10-23]] records browser/CLI/native process identity, independent shells, editor interaction, bounded output/Ctrl-C, reconnect and native iOS/Android evidence. Native Unicode paste remains unverified; browser emulation is distinguished from native software-keyboard acceptance. Retained evidence and reproducible native harness files live in this spec's `2026-10-23-shared-terminal-design/media/` and `native/` directories.
