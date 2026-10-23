---
displayName: Selkies screen design
tags: spec
status: implemented
plan: "[[superpowers/plans/2026-10-22-selkies|Selkies plan]]"
date: "2026-10-22"
---
Desktops stream their screen, sound and mic with Selkies instead of KasmVNC. Deskyard's own desktop page drives Selkies through our bar:
- Clipboard, Keys, Quality and Lock size;
- an Audio panel with sound, mic and webcam;
- file upload by dropping onto the screen, and a Download button.

This builds on the [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]] and replaces its KasmVNC parts. It takes over the sync targets and Lock size from the [[superpowers/specs/2026-10-22-audio-and-ports-design|Audio and ports design]], whose own sound path it supersedes. The prototype and its measurements are in the [[superpowers/spikes/2026-10-22-selkies|Selkies spike]].

# Intent

People watch videos and join calls in shared desktops. KasmVNC sends the picture as untimed screen updates, so our own sound path could only estimate sync. It measured sound 97 ms behind the picture on a clean network, and further out under jitter.

Selkies streams H.264 video and Opus sound over one WebSocket with short paths on both sides: sound 21 ms behind the picture on a clean network, at half the lag. The user watched YouTube on it and found the sync good. Video is also much smoother than VNC. The switch is decided; this design says how to do it properly.

# Goals

- **Selkies replaces KasmVNC** as each desktop's screen, sound and mic.
- **It keeps everything the desktop page does today, driven from our own bar:**
  - several viewers on one desktop;
  - the clipboard both ways;
  - Keys;
  - Quality presets;
  - the connection status;
  - reconnecting after restarts and upgrades;
  - the remote screen following the browser window.
- **New features in our bar:**
  - **Lock size:** keep the remote screen's size and scale it to fit;
  - **Audio panel** (layout B of the [[superpowers/specs/2026-10-22-audio-and-ports-design|audio mockup]]): sound on/off with volume and output device; mic on/off with input device; webcam on/off with camera device;
  - **file upload** by dropping files onto the screen;
  - **a Download button** that browses the desktop's files and downloads one file, or a folder as a zip;
  - **gamepads** passed through;
  - **mic badges:** everyone on a desktop sees whose mic is on.
- **Sync stays within the audio design's targets** (sound at most 45 ms early and 125 ms late) on a clean network and with 60 ± 20 ms delay. It's measured by an automated test with the A/V test page.
- **Agent tools keep working** on X11: `deskyard click|type|key|screenshot|zoom|size`, and `xclip`.

# Non-goals

- Printing, Selkies' screenshot endpoint, and its sharing links. They're turned off.
- Selkies' own sidebar and UI. Our bar is the only UI.
- WebRTC/UDP transport. WebSocket only, through the supervisor; no new ports.
- GPU encoding. CPU x264 only, for now.
- Sharp HiDPI (2× scaling). The screen is in CSS pixels, as with KasmVNC.
- Our own sound path. `audio-serve`, the supervisor's audio hub and the browser Player on `audio-and-ports` are dropped.

# Anti-goals

These would make it a failure even if it worked:

- **The supervisor passing a desktop's web content to browsers.** The supervisor design's rule holds: the web client comes from the image, served by the supervisor, and only WebSockets and the file-transfer endpoints listed below are proxied.
- **A viewer stuck on "Waiting for stream"** because someone else's tab went to the background.
- **A mic or webcam that turns itself on,** or is on without everyone seeing it.
- **Anything new running as root in a desktop,** or any new port.
- **Drags or clicks landing in the wrong place** because the screen is in our frame.

# Decided

## In the image

- **Software:** Selkies 2.1.x, pinned to an exact version with pinned pixelflux and pcmflux wheels, installed into `/opt/selkies`. It runs on Debian's Xvfb, which replaces KasmVNC's X server, as in the spike.
  - `start-xvfb`: a 3840x2160 framebuffer, with modes added by `xrandr`.
  - `start-selkies`: as `user`, on 127.0.0.1:6080 behind Caddy, as today.
- **Selkies settings:**
  - WebSocket mode;
  - no basic auth (Caddy's token is the auth);
  - allowed origins set to the supervisor's own origins, not `*`;
  - audio from `deskyard-speaker.monitor`;
  - CSS scaling on, browser cursors off;
  - Turbo off (`SELKIES_VIDEO_STREAMING_MODE=false`);
  - keyboard shortcuts off, sidebar hidden;
  - microphone on;
  - file transfers on, with uploads going to `~/Desktop`;
  - gamepads on;
  - webcam on where the host provides a virtual camera device;
  - printing, screenshots and sharing off.
- **One virtual mic: Selkies'.** The `deskyard-mic` source from the audio branch isn't carried over. The PipeWire virtual speaker (`deskyard-speaker`, the default sink) is.
- **Webcam** needs a virtual camera, `v4l2loopback`, on the host. The supervisor passes `/dev/video*` loopback devices into desktops only when they exist. Without one, the webcam switch is disabled, with "This machine has no virtual camera for desktops".
- **Contract:** the image contract becomes `3`, a Selkies desktop. The supervisor accepts only `3`. Nothing has been released, so there's no migration. `audio-and-ports`' contract 2 is never merged.
- **KasmVNC is removed** from the image, along with its tests.

## Through the supervisor

- **The web client.** The supervisor serves Selkies' web client from files extracted from the image, per image ID, as it does for KasmVNC's today, at `/<name>/screen/…`. Our small additions are served alongside:
  - the pointer fix, which hides `pointerrawupdate` from the client, so drags inside our frame land right;
  - the adapter hooks our bar uses.
  - They are added by the supervisor when serving, not by changing the image. The page and the additions are served `no-store`; the client's other files `no-cache` (revalidated each time).
- **Proxied routes,** with the same session, access and same-origin checks as `websockify` today:
  - `/<name>/screen/api/websockets`, the stream, which counts as viewing;
  - Selkies' file-transfer endpoints (upload and download).
  - Every other Selkies HTTP API is refused.
- **Retired:** `/<name>/websockify` and the KasmVNC client extraction.
- **Mic badges.** The desktop page reports mic on/off over the events channel (`POST /api/desktops/{name}/mic {"on": bool, "tab": id}`, the tab id being the one on that viewer's stream URL), tied to that viewer's stream connection. `Desktop.mics` lists the usernames, in Desktop JSON and `viewers` events, next to `viewers` and `forwards`. When the viewer's stream closes, their entry goes away.
- **Download.**
  - `GET /api/desktops/{name}/files?path=…` lists a folder in `/workspace`.
  - `GET /api/desktops/{name}/download?path=…` streams a file, or a folder as a zip.
  - Both read as `user` through exec, the way `deskyard cp` does, and are limited to `/workspace`.
  - This is ours, not Selkies' download feature, so it doesn't depend on the streamer and goes through the access checks.

## The desktop page

- **`kasm.ts` becomes `selkies.ts`,** an adapter over Selkies' client messages. It drives:
  - clipboard send and receive;
  - quality presets (Low…Lossless mapped to bitrate and frame rate, written in one table);
  - Lock size (`setManualResolution` + `setScaleLocally`, and back with `resetResolutionToWindow`);
  - sound on/off, volume, mute and output device;
  - mic and webcam on/off with their devices;
  - focus;
  - the connection status for the dot, derived from the stream's state, since Selkies posts no connection event.
- **Our bar,** left to right:
  - Back, the name and status, avatars with mic badges, and the ⇄ chip;
  - Audio, Lock size, Clipboard, Keys, Quality, Download, and ⋯.
  - On a phone, Lock size, Keys, Quality and Download move into ⋯.
- **Upload.** Dropping files on the screen shows a "Drop to copy into <name>" overlay, then uploads with a progress toast. Several files at once are fine. Uploads go to `~/Desktop`.
- **Download** opens a small file browser of `/workspace`: folders, a path box, recent files first. Picking a file downloads it; picking a folder downloads a zip.
- **Mic and webcam** are always off on load or reconnect. The browser's echo cancellation and noise suppression stay on for the mic.
- **Defaults per browser:**
  - Sound starts off on the first visit (the browser needs a click), then is remembered.
  - Lock size, volume and devices are remembered per browser.

## Selkies' rough edges

- **Background tabs.** A viewer's tab going to the background must never stop the stream for others, or for later viewers. The served client additions stop the client from sending `STOP_VIDEO`. If Selkies stops anyway, the supervisor restarts the stream when a viewer connects. A test covers it: the only viewer's tab goes hidden, a second viewer joins and sees the picture within 5 s.
- **Idle CPU.** About 12% of a core per idle desktop with a viewer is accepted for now. A separate investigation (the selkies-cpu session) looks at reducing it, and especially at the cost with no viewer. Its findings may add settings or patches before shipping. (They did: three patches shipped; see the plan's "Idle-CPU patches" entry and [[Architecture/Selkies]]. The audio gate is off by default.)
- **Upstream.** We report the `pointerrawupdate` and background-tab problems upstream, but don't wait for them.

## Tests

- **Image tests:** the stream comes up behind the token, agent tools work on X11, and the other Selkies endpoints are refused.
- **Supervisor UI tests:**
  - the desktop page shows the stream;
  - clipboard both ways;
  - Lock size;
  - upload by drop and Download;
  - mic badge on, then gone on reload;
  - the background-tab case.
- **A/V sync test:** the A/V test page plus the readout on Selkies' video. It runs on a clean network and with 60 ± 20 ms through the delaying proxy, must stay within the targets, and records its numbers.

## Settled after review *(my calls)*

- **Code base.** Work starts from `main`, which has port mapping. Only what's needed is cherry-picked or rewritten from `audio-and-ports` and the spike branch:
  - the PipeWire virtual speaker;
  - the A/V test page (with its epoch mode);
  - the `?avsync=1` readout;
  - `supervisor/tests/delayproxy.py`;
  - the Audio panel's UI.

  `audio-serve`, the audio hub and the Player are not brought over. `audio-and-ports` is left unmerged.
- **Several mics at once.** If Selkies' single virtual mic takes several viewers at once, they're mixed. If it can't, the last viewer to turn their mic on gets it, the others' switches turn off, and the badge follows whoever is live. One viewer turning off never tears down the mic for another. The builder checks which Selkies does first.
- **Tying a mic to a viewer.** The served client adds a per-tab id to the stream URL (`?tab=<random>`), and `POST /api/desktops/{name}/mic {"on", "tab"}` carries the same id. So the supervisor ties the badge to that viewer's stream, and drops it when the stream closes.
- **Who can upload and download.** Anyone who can view the desktop, because they can already use its terminal. Agent tokens use `deskyard cp`, not these routes.
  - Upload is capped at 2 GB per file.
  - Downloads stream, with no cap.
  - Selkies' own download is turned off in favour of ours.
  - If Selkies uploads inside the stream WebSocket rather than over HTTP, that's fine, since the viewer is already trusted with the desktop. The builder may replace it with our own upload endpoint if that's simpler to gate.
- **Browsers.** Chrome, Firefox and Safari must all work: picture, sound, input, clipboard, upload and download. Selkies uses its canvas path where `MediaStreamTrackGenerator` is missing. Sync is measured in Chromium. Mic and webcam device pickers may be missing where a browser can't list devices. A browser without WebCodecs shows "This browser can't show Deskyard desktops; use a current Chrome, Firefox or Safari".
- **CPU work.** The selkies-cpu findings are not a dependency. Settings it finds are folded in at the end; patches to Selkies or pixelflux were planned as a follow-up, but shipped with this work (see Idle CPU above).
- **Selkies version.** Pin the latest 2.1.x release at the time the plan starts, and write our client additions against that pinned source.

# Left to the builder

- **How Selkies' client is extracted** from the image, and exactly how our additions are added when serving.
- **The exact adapter messages,** read from Selkies' client source, which is pinned.
- **The quality preset table.**
- **How the connection status is derived.**
- **The webcam device detection.**
- **Docs:** architecture pages, retiring the KasmVNC ones, and the README.

# Mobile controls follow-up, 23 October 2026
The implemented compact toolbar also applies to narrow tablets and phone landscape, below 1024 CSS pixels. More must keep its lower actions reachable by scrolling within the visual viewport and update when that viewport changes. Clipboard drafts survive browser/keyboard focus changes; returning to the screen dismisses the panel. The [[Testing/Mobile 2026-10-23|mobile assessment and fix follow-up]] records reproduced failures, regression checks and the remaining simulator/device limitations.
