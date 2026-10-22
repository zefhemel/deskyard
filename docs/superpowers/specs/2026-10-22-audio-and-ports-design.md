---
displayName: Audio, mic, size lock and port mapping design
tags: spec
status: approved
plan: "[[superpowers/plans/2026-10-22-audio-and-ports|Audio and ports plan]]"
date: "2026-10-22"
---
Four additions to Deskyard, built on the [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]:
- sound from a desktop plays for everyone who has it open;
- viewers can turn on their mic, which is off by default;
- a "Lock size" checkbox controls whether a viewer's browser resizes the remote screen;
- the `deskyard` CLI can map ports in both directions between the agent's machine and a desktop.

# Intent

People watch videos and join calls in shared desktops, and today those are silent. KasmVNC 1.5, the remote desktop server in the image, has no audio streaming: Kasm's own product does it with a separate service. So Deskyard builds its own audio path.

Remote resize is always on today, and the last viewer to resize wins. Someone on a small laptop window then shrinks the screen for everyone. A viewer needs a way to keep the remote screen as it is and scale it locally.

Agents and people using the CLI work on code on their own machine and in the desktop. They need to reach each other's servers in both directions:
- the desktop's browser opening a dev server running on the laptop;
- the laptop's browser opening a dev server an agent started in the desktop.

Today there is no way to do either short of opening ports on the host, which the supervisor design forbids.

# Goals

- **Sound.** Desktop sound reaches every viewer who turned it on, in step with the picture: an added delay of about 0.1–0.2 s.
- **Cost.** Sound is encoded once per desktop, whatever the number of viewers.
- **Mic.** A viewer can send their mic into the desktop, where apps see an ordinary microphone. When several mics are on, they're mixed into that one microphone, like people sharing a meeting room.
- **Seeing who's on mic.** Everyone on a desktop can see whose mic is on.
- **Device choice.** Each viewer picks their own speaker and mic device, where the browser allows it.
- **Lock size.** Per viewer, the browser either scales a fixed remote screen (keeping the aspect ratio) or resizes the remote screen to fit.
- **Port mapping.** `deskyard` maps ports in both directions over the connection it already has. No new port is published anywhere.
- **Seeing mappings.** Everyone on a desktop sees which agent tokens are mapping which ports, next to who's viewing.

# Non-goals

- Agent access to audio. Agent tokens and the CLI get no sound and no mic.
- Recording, or keeping audio anywhere.
- Video from a viewer's camera.
- Starting or stopping port mappings from the web UI. Mappings are made with the CLI only; the UI just shows them.
- UDP forwarding. Port mapping carries TCP only.

# Anti-goals

These would make it a failure even if it worked:

- A mic that turns itself on: after a reload or reconnect, or because someone else turned theirs on. A mic that's live without its owner's avatar showing it.
- Sound that plays before the viewer clicked to allow it. The browser blocks it anyway, but the UI shouldn't fight that.
- A process in the desktop that agents can reach and that runs as root. The supervisor design's root rule holds.
- Any new port on the host, the supervisor or the desktop's network. A mapped port that listens on anything but loopback.
- A token for one desktop being able to map ports on another.

# Decided

## Image contract

The image contract goes from `deskyard.contract=1` to `2`. Version 2 means the image has:
- the sound server;
- the audio stream process behind its Caddy route;
- an in-image `deskyard` that can act as the forward helper.

The supervisor accepts only contract 2. Nothing has been released, so there are no contract-1 desktops to keep working, and there is no per-desktop feature detection. *(My call.)*

## Audio in the desktop

- **Sound server.** The image gets a sound server, PipeWire with its PulseAudio layer, so native apps, Chromium and Flatpak apps all find it. It runs as `user`, in the session, like the other session programs.
- **Default devices.** It has a virtual speaker, which is the default output, and a virtual microphone, which is the default input.
- **Stream process.** One process per desktop, running as `user`, reads the virtual speaker and encodes Opus at 48 kHz stereo, about 96 kbit/s. It writes what viewers send into the virtual microphone, decoding and mixing their Opus streams.
- **No idle work.** It encodes only while someone is listening, and plays mic audio only while someone's mic is on.
- **Reaching it.** It's served through the desktop's Caddy under one path, behind the same `X-Deskyard-Token` check as everything else. So only the supervisor reaches it.

## The audio WebSocket

- **Route.** `/<name>/audio` on the supervisor is a WebSocket with the same checks as `/<name>/websockify`:
  - a session;
  - access to the desktop;
  - a same-origin `Origin` header.

  Revoking access closes it, as it closes the screen's WebSocket.
- **Fan-out.** The supervisor keeps one connection per desktop to the desktop's stream process. It fans the sound out to all viewers who turned sound on, and passes each viewer's mic audio up, tagged with the viewer.
- **Messages.** One WebSocket per viewer carries everything:
  - sound down;
  - mic up;
  - small control messages: sound on/off, mic on/off.
- **Who's on mic.** The supervisor tracks whose mic is on and adds it to the viewers it already reports. `Desktop.viewers` stays a list of usernames; a new `Desktop.mics` lists the usernames whose mic is on. It's sent with `viewers` events and in `Desktop` JSON.

## The desktop page

- **Audio button.** The desktop bar gets one **Audio** button (layout B from the mockups). It shows a speaker icon, plus a red mic icon while your mic is on. It opens one panel:
  - **Desktop sound:** an on/off switch, a list of output devices (where the browser can choose one; Safari can't, and then the list is hidden) and a volume slider.
  - **My mic into <name>:** an on/off switch, a list of input devices and a level meter. The line "Everyone on <name> sees when your mic is on" sits under it.
- **Defaults.** Sound starts off on a viewer's first visit. After that it remembers on/off, volume and devices per browser. Turning it on is a click, which satisfies the browser's autoplay rule.
- **Mic is never remembered.** It's always off when the page loads or reconnects. Turning it on asks for the browser's microphone permission the first time.
- **Where mic audio goes.** Mic audio goes only into the desktop. Viewers don't hear each other's mics through Deskyard; a call app in the desktop does its own mixing. *(My call.)*
- **Echo.** The browser's echo cancellation, noise suppression and auto gain stay on for the mic.
- **Mic badge.** Avatars, in the bar and on wall tiles, get a small red mic badge for each username in `mics`.

## Lag and sync with the picture

Lag matters most for videos. Sound and picture take different paths:
- **Sound:** sound server → encoder → WebSocket → decoder → audio output.
- **Picture:** X → KasmVNC's encoder → its WebSocket → the canvas.

Each path has its own delay, and the picture's delay changes with what's on screen and with the network. So sync is measured and tuned, not assumed.

**Targets**
- **Sync:** sound arrives no more than 45 ms before the picture and no more than 125 ms after it. These are the broadcast thresholds (ITU-R BT.1359) where people start to notice; sound early is noticed sooner than sound late.
- **Total lag:** sound adds no more than 200 ms to the desktop on a local network.

**Measuring it**
- **The test pattern.** Build an A/V sync test pattern into the image as a local page. It flashes the screen white and beeps at exactly the same moment, once a second.
- **Measuring in the viewer.** The desktop page notes when each flash shows up on the KasmVNC canvas: it is served from the supervisor's own origin, so the page can read the canvas pixels. It also notes when each beep comes out of its own audio playback, from the decoded samples at their output time. The difference is the sync offset. The capture time in each audio packet against the viewer's clock gives the sound's total lag.
- **On demand.** A hidden switch on the desktop page (for example `?avsync=1`) shows the measured offset and lag live, so it can be checked on any desktop and network.
- **In the tests.** An automated integration test plays the pattern in a real desktop and fails outside the targets. It runs:
  - over a direct connection;
  - with extra delay and jitter added in the test (for example, a delaying proxy in front of the supervisor);

  and records the numbers each time.

**Keeping them in step**
- **Sound is the side that waits.** Viewers can't speed up the picture, so the player holds sound back by a target delay that follows the picture's measured delay.
- **Measuring the picture's delay without a test pattern.** For example, from KasmVNC's frame statistics, or from round trips on the screen's WebSocket. The builder picks the method; the test pattern proves it works.
- **Smooth changes.** The target delay changes smoothly, by stretching or squeezing playback slightly, never by jumps or clicks.
- **Never too far ahead.** If the target can't be met, sound falls behind rather than running ahead.
- **A manual nudge.** The Audio panel gets a small "Sound delay" adjustment (±300 ms, per browser), for setups the automatic tuning gets wrong. *(My call.)*

The plan's first step is a spike that builds the sound path and the measurement, and reports real numbers on OrbStack, before the rest of audio is built.

## Lock size

- **Checkbox.** The desktop bar gets a **Lock size** checkbox, placed as in the mockup (`2026-10-22-audio-and-ports-design/audio-bar.html`, layout B): between the Audio button and Clipboard. It's per viewer, remembered per browser, off by default.
  - **Off:** today's behaviour. Resizing the window resizes the remote screen (`resize=remote`).
  - **On:** the remote screen keeps its current size, and the frame scales it to fit the window with the aspect ratio kept, letterboxed (KasmVNC's `resize=scale`).
- **Other viewers.** With several viewers, anyone with Lock size off can still resize the screen for everyone, as today.

## Port mapping

- **Command.** `deskyard [-d NAME] forward SPEC...` runs until Ctrl-C. Each `SPEC` is one mapping:
  - `3000` or `3000:8000`: your machine's 127.0.0.1:3000 becomes the desktop's `localhost:3000` (or 8000). A listener in the desktop accepts connections and carries each back to your machine.
  - `-R 5173` or `-R 5173:5174` (reverse): a port bound inside the desktop (5173) becomes `127.0.0.1:5173` (or 5174) on your machine.
- **Transport.** All mappings go over one WebSocket on the agent API, `/api/agent/v1/forward`, with many TCP connections multiplexed on it. The token, revocation, expiry and the 409 for a stopped desktop work as they do for exec.
- **Limits.**
  - Both ends listen on loopback only.
  - The desktop side runs as `user`, so ports below 1024 there are refused with a clear error.
  - A port already in use on either side fails that mapping with a clear message, and the other mappings carry on.
- **Dropped connections.** When the connection drops (the desktop stops, restarts or is upgraded, the supervisor restarts, or the network blips), `forward` keeps its local listeners. It retries with backoff (1 s up to 30 s, waiting through a stopped desktop) and sets the mappings up again. It prints one line per state change. Open TCP connections through it are closed. It exits only on Ctrl-C, or when the token is refused (401: revoked or expired). A port that is taken after reconnecting is reported, and that mapping retries with the rest. *(My call: agents run it in the background, so it shouldn't quietly die.)*
- **Inside a desktop.** The `deskyard` command run inside a desktop has no remote, so `forward` needs `-d` there too. It's useful from one desktop to another only if someone sets that up on purpose.
- **Shown to viewers.** The supervisor tracks active mappings per desktop. `Desktop.forwards` lists them as `{"token": name, "direction": "in"|"out", "port": n}`:
  - `in`: the agent's machine into the desktop; `port` is the desktop's port.
  - `out`: the desktop's port out to the agent's machine.

  The list is sent in `Desktop` JSON and with `viewers` events. Where the UI shows who's viewing, it also shows mappings, one line per token, for example "deploy-bot: 3000 in · 5173 out":
  - the desktop bar's viewer avatars;
  - the details page's "viewing" card.

  The agent machine's own port numbers aren't shown.

  In the desktop bar, a small "⇄ 2" chip sits after the avatars while mappings are active. Clicking the avatars or the chip opens one popover listing the viewers (with mic badges) and then one line per token with mappings. On the details page, the "viewing" card gets the same lines under the viewers. *(My call.)*
- **Self-description.** It's listed in `deskyard describe --json` and `guide` like every command.

# Left to the builder

- **Audio pipeline.** How the stream process captures, encodes, decodes and mixes. GStreamer, a small Go program using libopus, or another way all fit. Keep the image's added size reasonable.
- **Playback.** How the page plays sound. WebCodecs with an AudioWorklet and a small jitter buffer is a good fit. It needs a fallback where WebCodecs' Opus decoder is missing.
- **Wire formats.** The framing of the audio and forward WebSockets. Document both in `supervisor/API.md`.
- **Forward protocol.** How `/api/agent/v1/forward` gets to the desktop's ports. For example, exec of a helper subcommand of `deskyard` as `user` that dials or listens on loopback, multiplexed over its stdio. Or a route through Caddy, if it keeps the token check.
- **Tests.**
  - Integration tests that play a known tone in a desktop and see it arrive (beyond the sync test above).
  - A test that sends a tone as mic and records it in the desktop.
  - Tests that map a port both ways and fetch through it.
  - A UI test that Lock size switches the frame between remote and scale.
- **Docs.** Architecture pages for the new parts (sound server, audio stream, audio proxy, port forwarding), linked with `partOf` and `uses`.
