---
displayName: Audio and ports plan
tags: plan
status: active
date: "2026-10-22"
spec: "[[superpowers/specs/2026-10-22-audio-and-ports-design|Audio, mic, size lock and port mapping design]]"
---
# Status

- **Execution:** subagent-driven.
- **Image size:** the audio packages may grow the image; report the measured increase.
- **Lag gate:** if Task 3's checkpoint measures more than 200 ms of sound lag on OrbStack, stop and ask before Task 4.

# Audio, Mic, Size Lock and Port Mapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four things to Deskyard:
- desktop sound for every viewer, kept in sync with the picture;
- an optional mic from viewers into the desktop;
- a per-viewer Lock size checkbox;
- `deskyard forward` to map TCP ports both ways, with active mappings shown to viewers.

**Architecture:**
- **In the desktop.** PipeWire (with its PulseAudio layer) provides a virtual speaker and microphone. A hidden subcommand of the in-image `deskyard`, `audio-serve`, streams the speaker as Opus in Ogg via `gst-launch-1.0`, splits it into packets, and serves them on a loopback WebSocket that Caddy exposes at `/_audio/`.
- **In the supervisor.** It keeps one upstream connection per desktop and fans packets out to viewers on `/<name>/audio`. Mic audio goes the other way; each live mic plays as its own stream into the virtual mic's sink, so PipeWire does the mixing.
- **Port mapping.** It runs over `/api/agent/v1/forward`. The supervisor relays frames of a small multiplexing protocol (`internal/forward`) to a second hidden subcommand, `forward-helper`, which it starts by exec as `user`.

**Tech Stack:** as the supervisor plan (Go 1.27, Preact + TypeScript strict, Tailwind, pytest + Playwright), plus:
- in the image: PipeWire, WirePlumber and GStreamer (`opusenc`, `opusdec`, `oggmux`, `oggdemux`, `pulsesrc`, `pulsesink`);
- in the browser: AudioWorklet, WebCodecs `AudioEncoder` for the mic, and the `opus-decoder` npm package (WASM) for playback.

**Spec:** `docs/superpowers/specs/2026-10-22-audio-and-ports-design.md`

## Global Constraints

- **Contract.** Image contract label is `deskyard.contract=2`; the supervisor accepts only `"2"`.
- **No root.** Every new process in the desktop runs as `user`; nothing new runs as root. The only new desktop-side endpoint is behind Caddy's `X-Deskyard-Token` check.
- **No new ports** on the host, the supervisor or the desktop network. Mapped ports listen on loopback only.
- **Sound.** Opus 48 kHz stereo, about 96 kbit/s, encoded once per desktop and only while someone listens.
- **Sync targets:** sound at most 45 ms before and 125 ms after the picture. Sound adds at most 200 ms of lag on a local network. When the target can't be met, sound runs late, never early.
- **Mic.** Never remembered and always off on load or reconnect. Echo cancellation, noise suppression and auto gain stay on. Mic audio goes only into the desktop. Agent tokens get no audio.
- **Lock size.** Per browser, off by default. On is `resize=scale`, off is `resize=remote`.
- **`forward` reconnects.** It retries with backoff from 1 s to 30 s, waiting through a stopped desktop. It exits only on Ctrl-C or a 401.
- **Desktop-side ports below 1024** are refused with a clear error. A port already in use fails only that mapping.
- **`Desktop.mics`** is a list of usernames.
- **`Desktop.forwards`** is a list of `{"token": name, "direction": "in"|"out", "port": n}`. `port` is always the desktop's port.
- **Both** go in `Desktop` JSON and in `viewers` events, which become `{"name","viewers","mics","forwards"}`.
- **Storage keys** (browser `localStorage`, wrapped in try/catch): `deskyard.sound` (`{on, volume, output, delayMs}`), `deskyard.mic.input`, `deskyard.lockSize`.

## Review Focus

- **Background tab.** A tab with sound on goes to the background and comes back, or the AudioContext is suspended. Playback must resume at the current target delay, dropping the backlog, not play seconds of stale audio. Test: Task 3.
- **Desktop restart with sound or mic on.** The audio WebSocket reconnects. Sound resumes if it was on; the mic does not, and the user's `mics` entry disappears. Test: Task 5.
- **Access revoked while someone listens or talks.** Their audio WebSocket closes and their mic badge disappears for everyone. Test: Task 5.
- **Five viewers listening.** There is exactly one upstream audio connection and one encoder pipeline in the desktop. Test: Task 2.
- **A mapping whose local port is taken** (`forward 3000` while something listens on 3000 locally). That mapping reports the error and the others keep working. Test: Task 8.

---

### Task 1: Sound in the image, contract 2, `audio-serve` (sound down)

**Files:**
- Modify: `images/debian-xfce/Dockerfile`
  - Packages: `pipewire`, `pipewire-pulse`, `wireplumber`, `pulseaudio-utils`, `gstreamer1.0-tools`, `gstreamer1.0-plugins-base`, `gstreamer1.0-plugins-good`, `gstreamer1.0-pulseaudio`.
  - Label `deskyard.contract="2"`.
- Create: `images/debian-xfce/rootfs/etc/pipewire/pipewire.conf.d/deskyard.conf`. It defines:
  - the virtual speaker, a null sink `deskyard-speaker`, which is the default sink;
  - the mic sink, a null sink `deskyard-mic-sink`;
  - the virtual mic, a source `deskyard-mic` that remaps the mic sink's monitor and is the default source.
- Modify: `images/debian-xfce/rootfs/etc/supervisor/conf.d/workspace.conf`.
  - Programs `pipewire`, `wireplumber`, `pipewire-pulse` and `audio`. All run as `user`, with `XDG_RUNTIME_DIR` set as for the session, priority 25, before the session, so apps find the sound server.
  - `audio` runs `/usr/local/bin/deskyard audio-serve --listen 127.0.0.1:6091`.
- Modify: `images/debian-xfce/rootfs/etc/caddy/Caddyfile`: inside the token route, `handle_path /_audio/* { reverse_proxy 127.0.0.1:6091 }`.
- Create: `images/debian-xfce/rootfs/opt/workspace/avsync/index.html`. The sync test page: full-screen black that flashes white for 100 ms while a 1 kHz beep plays for 100 ms. Both start at the same `AudioContext` time, once per second. It plays only after a click (autoplay).
- Create: `supervisor/internal/audio/wire.go`: the packet format shared by `audio-serve`, the supervisor and (mirrored in TypeScript) the page.
- Create: `supervisor/internal/cli/app/cmd_audio.go`: hidden `audio-serve`.
- Modify: `supervisor/internal/cli/app/spec.go`. Add `Hidden bool` to `Command`; hidden commands are left out of `help`, `guide` and `describe`.
- Test:
  - `supervisor/internal/audio/wire_test.go`
  - `supervisor/internal/audio/ogg_test.go`
  - `images/debian-xfce/tests/test_audio.py`

**Interfaces:**
- Produces, in `internal/audio`:
  - `type Packet struct { Seq uint32; Capture int64 /* µs since Unix epoch, desktop clock */; Opus []byte }`
  - `func (p Packet) Marshal() []byte`, which gives `0x01 | seq u32 BE | capture i64 BE | opus`.
  - `func ParsePacket(b []byte) (Packet, error)`.
  - `type OggReader` with `func NewOggReader(r io.Reader) *OggReader` and `func (o *OggReader) Next() (opus []byte, granule int64, err error)`. It skips the OpusHead and OpusTags packets.
- Produces, in `audio-serve`:
  - `ws://127.0.0.1:6091/`: each binary message is one `Packet`, of 20 ms.
  - Capture starts with the first connection and stops 5 s after the last one closes.
  - The pipeline is `gst-launch-1.0 -q pulsesrc device=deskyard-speaker.monitor latency-time=10000 buffer-time=20000 ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2 ! opusenc bitrate=96000 frame-size=20 ! oggmux max-delay=0 max-page-delay=0 ! fdsink fd=1`.
  - `Capture` is the wall clock when the pipeline started plus `granule/48`. Re-anchor it when drift exceeds 20 ms.

- [ ] **Step 1: Write failing Go tests.**
  - `TestPacketRoundTrip`: marshal then parse gives the same fields; `ParsePacket` rejects short input and a wrong type byte.
  - `TestOggReader`: feed `testdata/tone.opus.ogg` (generate it once with `gst-launch-1.0 audiotestsrc num-buffers=50 ! opusenc frame-size=20 ! oggmux ! filesink` and commit it). Expect 50 ± 1 packets, increasing granules, and no header packets.
- [ ] **Step 2: Run them.** `go test ./internal/audio`. Expect FAIL (package missing).
- [ ] **Step 3: Implement** `wire.go`, `ogg.go`, `cmd_audio.go` and `Hidden`.
- [ ] **Step 4: Write a failing image test.** `test_audio.py::test_sound_server_and_stream`.
  - `pactl info` as `user` shows `Default Sink: deskyard-speaker` and `Default Source: deskyard-mic`.
  - Chromium (native) and a Flatpak app both see the PulseAudio server: `flatpak run --command=pactl <app> info` exits 0.
  - With `paplay` of a 1 kHz tone running, a WebSocket to Caddy `/_audio/` with the token gets at least 40 packets in 1 s. Decoding them (`opusdec` via gst in the test container, or Python `opuslib`) gives a dominant frequency of 1000 ± 20 Hz.
  - Without the token, the result is 401.
- [ ] **Step 5: Rebuild the image and run.** Build with `docker build -t deskyard-debian-xfce:test -f images/debian-xfce/Dockerfile .`, then `AC_IMAGE=deskyard-debian-xfce:test uv run pytest test_audio.py test_privileges.py test_footprint.py`. Expect PASS.
  - Note the image size change in the commit message.
  - Update `test_footprint.py`'s limits only if they fail, and by no more than the measured increase.
- [ ] **Step 6: Commit.** Message: "image: PipeWire sound server, audio-serve, contract 2".

### Task 2: Supervisor audio hub and `/<name>/audio` (sound down), contract 2

**Files:**
- Modify: `supervisor/internal/desktops/service.go`: `Contract = "2"`.
- Create: `supervisor/internal/server/audio.go`: the hub and the viewer handler.
- Modify: `supervisor/internal/server/desktop.go`: route `rest == "audio"` with the same session, access and Origin checks as `websockify`.
- Modify: `supervisor/API.md`: the audio WebSocket section.
- Test:
  - `supervisor/internal/server/audio_test.go`
  - `supervisor/tests/test_audio.py`

**Interfaces:**
- Consumes: `audio.Packet` and `ParsePacket` (Task 1).
- Produces: `type audioHub struct` on `Server` as `s.audio`, with:
  - `func (h *audioHub) join(name string, v *audioViewer)` and `leave(...)`;
  - `func (h *audioHub) mics(name string) []string`, sorted and unique, used in Task 5;
  - `func (h *audioHub) closeUser(name, username string)`, used by revocation.
- Upstream: one WebSocket per desktop to `<desktop target>/_audio/` with `X-Deskyard-Token`. It's opened while at least one viewer has sound on or mic on, and closed 5 s after the last one.
- Viewer protocol on `/<name>/audio`:
  - **Down:** binary `Packet`s. These go only to viewers with sound on.
  - **Up, text:** `{"sound":bool}` and `{"mic":bool}`.
  - **Up, binary:** `0x02 | fmt u8 | payload`, the mic. `fmt` is 1 for Opus (one packet, 20 ms, 48 kHz mono) or 2 for PCM (s16le, 24 kHz mono, 20 ms).
  - **Down, text:** `{"error":string}` before closing.
- Slow viewers: each viewer has a bounded queue of 25 packets (500 ms). On overflow, drop the oldest; never block other viewers.

- [ ] **Step 1: Write failing Go tests.** These use a fake upstream `httptest` WebSocket that sends numbered packets.
  - `TestAudioFanOut`: five viewers with sound on all receive packets 1..50 in order, and the fake upstream saw exactly one connection.
  - `TestAudioOnlyWhenListening`: a viewer with sound off gets nothing. No viewer with sound means no upstream connection; one appears within 1 s of `{"sound":true}`.
  - `TestAudioSlowViewerDoesntBlock`: a viewer that never reads doesn't delay the others by more than 50 ms.
- [ ] **Step 2: Run.** `go test ./internal/server -run Audio`. Expect FAIL.
- [ ] **Step 3: Implement** `audio.go` and the route. Bump `Contract`. Fix `server_test`'s fake engine image label to `"2"`.
- [ ] **Step 4: Write a failing integration test.** `supervisor/tests/test_audio.py::test_tone_reaches_every_viewer`.
  - Two signed-in clients open `/<name>/audio` and send `{"sound":true}`.
  - Run `paplay` of a 1 kHz tone in the desktop through the agent exec API.
  - Both clients get packets that decode to 1000 ± 20 Hz.
  - `docker exec … pgrep -c gst-launch` is 1.
  - With a foreign `Origin` the result is 403; without a session, 401.
  - With an agent token (`Authorization: Bearer dyt_…`) and no session, the result is 401: agents get no audio.
- [ ] **Step 5: Run** `CC_TEST_IMAGE=deskyard-debian-xfce:test uv run pytest test_audio.py test_api.py`. Expect PASS (`test_image_without_contract_refused` still passes with "2").
- [ ] **Step 6: Commit.** Message: "supervisor: audio hub, /<name>/audio, contract 2".

### Task 3: Playback in the desktop page, the Audio panel's sound half, and the sync readout

**Files:**
- Modify: `supervisor/web/package.json`: add `opus-decoder` (exact version, MIT).
- Create: `supervisor/web/src/desktop/audio/wire.ts`. It mirrors `internal/audio`: `parsePacket(buf: ArrayBuffer): {seq: number; capture: number /* ms */; opus: Uint8Array}` and `micFrame(fmt: 1 | 2, payload: Uint8Array): ArrayBuffer`.
- Create: `supervisor/web/src/desktop/audio/player-worklet.ts`, built as a separate Vite worklet entry. It holds a ring buffer of decoded float32 stereo.
- Create: `supervisor/web/src/desktop/audio/player.ts`: `class Player`.
- Create: `supervisor/web/src/desktop/audio/connection.ts`: `class AudioLink`, which wraps the WebSocket and reconnects with backoff from 1 s to 10 s.
- Create: `supervisor/web/src/desktop/audio/panel.tsx`: the Audio button and popover (layout B in `docs/superpowers/specs/2026-10-22-audio-and-ports-design/audio-bar.html`). In this task it has only the sound half.
- Create: `supervisor/web/src/desktop/avsync.ts`: the `?avsync=1` readout.
- Modify: `supervisor/web/src/desktop/desktop.tsx`: mount the Audio button before Clipboard.
- Test: `supervisor/tests/test_ui.py`.

**Interfaces:**
- `class Player`:
  - `constructor(ctx: AudioContext)`;
  - `push(p: Packet): void`;
  - `setTargetDelay(ms: number): void`;
  - `setVolume(v: number): void`;
  - `setOutput(deviceId: string): Promise<void>` (`AudioContext.setSinkId`; a no-op where it's missing);
  - `outputLatencyMs(): number`;
  - `onPlayed: (capture: number, playedAt: number) => void`. `playedAt` is `performance.timeOrigin + ctx time` when the packet's first sample reaches the output.
- `Player` behaviour:
  - It keeps the buffered amount near the target delay by resampling between 0.98× and 1.02×.
  - If more than 300 ms is buffered beyond the target (after a background tab or a resume), it drops to the target at once.
  - Its default target is 80 ms until Task 4 sets it.
- `class AudioLink`: `setSound(on: boolean)`, `setMic(on: boolean)`, `sendMic(frame: ArrayBuffer)`, `onPacket`, `onState("connecting" | "open" | "closed")`.
- `avsync.ts`: `startAvSync(frame: HTMLIFrameElement, player: Player): () => void`.
  - The flash: it samples the KasmVNC canvas (same origin) every animation frame and detects a mean-luminance rise above 0.7.
  - The beep: it detects the beep onset from the worklet's per-block RMS (posted with the block's output time).
  - It shows the median sync offset (sound minus picture, ms) and the sound lag (`playedAt` minus `capture`, ms) over the last 10 beeps, in a corner overlay.
  - It exposes `window.__avsync = {offsets: number[], lags: number[]}` for tests.

- [ ] **Step 1: Write failing UI tests** in `test_ui.py`.
  - `test_sound_toggle_plays`: Chromium is launched with `--autoplay-policy=no-user-gesture-required` and fake media. Open the desktop page, open Audio, switch Desktop sound on, run `paplay` of a tone in the desktop. `window.__deskyardAudio.played` (a counter the Player exposes in dev and test builds) rises above 20 within 5 s.
  - `test_sound_resumes_without_backlog`: sound on, then `ctx.suspend()` for 3 s, then `ctx.resume()`. Within 1 s the buffered amount (`window.__deskyardAudio.bufferedMs`) is under 300.
  - `test_avsync_readout`: open `/<name>/?avsync=1` and start the test page in the desktop. Use `deskyard exec -- chromium /opt/workspace/avsync/index.html`, then click it through the agent API. `window.__avsync.offsets` gets at least 5 values within 20 s.
- [ ] **Step 2: Run.** `uv run pytest test_ui.py -k "sound or avsync"`. Expect FAIL.
- [ ] **Step 3: Implement** the files above.
  - Sound settings persist under `deskyard.sound`; sound is off on first visit.
  - The output device list is hidden when `AudioContext.prototype.setSinkId` is missing.
- [ ] **Step 4: Run.** Typecheck, build, then the tests from Step 1. Expect PASS.
- [ ] **Step 5: Checkpoint: measure and record.** Open `?avsync=1` on a desktop on OrbStack in Chromium, Firefox and Safari. Run the test page for 60 s each.
  - Write the median offset and lag per browser into this plan's Status section (create it at the top).
  - This is the spec's "spike": if the lag alone exceeds 200 ms, stop and report before Task 4.
- [ ] **Step 6: Commit.** Message: "web: desktop sound playback, Audio panel, A/V sync readout".

### Task 4: Keeping sound in step with the picture

**Files:**
- Create: `supervisor/web/src/desktop/audio/sync.ts`.
- Modify: `supervisor/web/src/desktop/audio/panel.tsx`: the "Sound delay" slider (−300 to +300 ms, step 10, under the volume slider, stored in `deskyard.sound.delayMs`).
- Create: `supervisor/tests/delayproxy.py`: a TCP proxy for tests that adds a fixed delay plus uniform jitter in each direction.
- Test: `supervisor/tests/test_avsync.py`.

**Interfaces:**
- Consumes: `Player.setTargetDelay` and `outputLatencyMs`, and `startAvSync`'s data (Task 3).
- Produces: `class SyncController`:
  - `constructor(player: Player, frame: HTMLIFrameElement, link: AudioLink)`;
  - `setManualOffset(ms: number)`;
  - `stop()`.
- How it estimates the picture's delay:
  - **(a) Network:** half the round trip of `{"ping":t}` text messages on the audio WebSocket, which the supervisor echoes as `{"pong":t}`. That's one ping every 2 s, smoothed with an EWMA (α 0.2). Add this echo to `audio.go` and API.md.
  - **(b) The picture's own processing:** the time between KasmVNC frame updates arriving and being drawn. Look in the image's `/usr/share/kasmvnc/www/assets/ui-*.js` for frame statistics the client exposes, and use them if they're there. Otherwise use a constant of 40 ms, measured at the Task 3 checkpoint and written next to the constant.
- How it sets the sound delay:
  - Picture delay = a + b. The sound's own delay is buffering plus output latency.
  - It sets `target = max(minBuffer, pictureDelay − soundPathDelay + manualOffset)`, where `minBuffer` is 40 ms.
  - It changes the target by at most 20 ms per second; the Player's resampling makes that inaudible.
  - If the result would put sound ahead of the picture, it raises the target. If raising it would break the 200 ms lag budget, it lets sound run late.

- [ ] **Step 1: Write failing tests.** In `test_avsync.py`, play the test page as in Task 3 and read `window.__avsync` over 30 beeps. Two tests:
  - `test_sync_direct`: median offset between −45 and +125 ms; median lag at most 200 ms.
  - `test_sync_with_delay`: the browser reaches the supervisor through `delayproxy.py` with 60 ms ± 20 ms each way. Median offset between −45 and +125 ms.
  - Print the measured numbers in both, so CI logs keep them.
- [ ] **Step 2: Run.** Expect FAIL, or PASS by luck. Record the numbers either way.
- [ ] **Step 3: Implement** `sync.ts`, the ping echo and the slider.
- [ ] **Step 4: Run** `uv run pytest test_avsync.py -v -s`. Expect PASS. Add the numbers to the plan's Status section.
- [ ] **Step 5: Commit.** Message: "web: keep sound in step with the picture".

### Task 5: Mic into the desktop, and `mics`

**Files:**
- Modify: `supervisor/internal/cli/app/cmd_audio.go`.
  - `audio-serve` takes mic frames: `0x02 | voice u16 | fmt u8 | payload` from the supervisor, and `0x03 | voice u16` to end a voice.
  - Each voice gets its own `gst-launch-1.0` playing into `deskyard-mic-sink`, so PipeWire mixes them. The pipeline is `fdsrc ! … ! opusdec ! pulsesink device=deskyard-mic-sink` for Opus, or `rawaudioparse … ! audioresample ! pulsesink …` for PCM.
  - A voice idle for 2 s is stopped.
- Modify: `supervisor/internal/server/audio.go`.
  - It assigns a voice number per viewer while their mic is on, and forwards frames.
  - It tracks `mics(name)`. It publishes on changes through `publishViewers`.
  - Closing a viewer's WebSocket sends `0x03` and removes them from `mics`.
- Modify:
  - `supervisor/internal/server/viewers.go` (event payload `{name, viewers, mics, forwards}`);
  - `supervisor/internal/server/desktops.go` (`Mics []string json:"mics"`);
  - `supervisor/internal/server/server.go` (`viewers.revoke` also calls `s.audio.closeUser`).
- Create: `supervisor/web/src/desktop/audio/mic.ts`: `class Mic`.
  - `start(deviceId?: string): Promise<void>`.
  - It uses `getUserMedia` with `echoCancellation`, `noiseSuppression` and `autoGainControl` true.
  - It encodes with WebCodecs `AudioEncoder` (`opus`, 48 kHz mono, 20 ms frames). Where that isn't available, it sends PCM 24 kHz.
  - `stop(): void` stops the tracks, so the browser indicator goes away.
  - `level(): number` gives 0..1 for the meter.
- Modify:
  - `supervisor/web/src/desktop/audio/panel.tsx`: the mic half. It has the switch, the input list (`enumerateDevices` after permission), the meter, and the line "Everyone on <name> sees when your mic is on". The button gets a red mic icon while the mic is on.
  - `supervisor/web/src/components/avatar.tsx`: a `mic` badge prop.
  - Tiles and the desktop bar: pass `mics`.
  - `api.ts`: `mics: string[]`.
  - `live.tsx`: the `viewers` event carries `mics`.
  - `dev/mock*.ts`.
- Test: `supervisor/internal/server/audio_test.go` and `supervisor/tests/test_audio.py`.

- [ ] **Step 1: Write failing tests.**
  - Go:
    - `TestMicVoicesAndMics`: two viewers turn mic on, and the fake upstream sees voices 1 and 2. `mics` is sorted and holds both usernames. One closes: `0x03` for its voice, and it's gone from `mics`.
  - Integration, in `test_audio.py`:
    - `test_mic_reaches_the_desktop`: a client sends 2 s of 440 Hz as PCM frames. `parecord -d deskyard-mic` in the desktop, through exec, records a dominant 440 ± 10 Hz.
    - `test_two_mics_mix`: 440 Hz and 660 Hz from two accounts both show up in the recording's spectrum.
    - `test_mic_badge_and_revocation`: with anna's mic on, the admin sees `mics == ["anna"]`. Revoking anna's access closes her audio WebSocket, and `mics` becomes `[]` within 2 s.
    - `test_restart_turns_mic_off`: restart the desktop with anna's mic on. After it's running, `mics == []`, and anna's link reconnects with sound on (as before) and mic off.
  - UI, in `test_ui.py`:
    - `test_mic_off_after_reload`: launch with `--use-fake-ui-for-media-stream --use-fake-device-for-media-stream`, turn the mic on, and the badge shows. Reload: the switch is off and the badge is gone.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement.** Rebuild the image, since `audio-serve` changed.
- [ ] **Step 4: Run** the Go tests, the integration and UI tests above, and image `test_audio.py`. Expect PASS.
- [ ] **Step 5: Commit.** Message: "Mic into the desktop; mics on desktops and viewers events".

### Task 6: Lock size

**Files:**
- Modify: `supervisor/web/src/desktop/kasm.ts`:
  - `export function sendResize(frame: HTMLIFrameElement | null, mode: "remote" | "scale"): void` posts `{action: "resize", value: mode}`.
  - `frameSrc(name, resize)` takes the initial mode.
  - Update the header comment: `resize` is now used.
- Modify: `supervisor/web/src/desktop/desktop.tsx`: a **Lock size** checkbox between the Audio button and Clipboard, stored in `deskyard.lockSize`. In the phone layout it moves into the ⋯ menu as a checkable item.
- Test: `supervisor/tests/test_ui.py`.

- [ ] **Step 1: Write a failing test.** `test_lock_size`, with the viewport at 1280×860:
  - Read `deskyard -d … size`, giving the remote size A.
  - Check Lock size, then resize the viewport to 900×700. After 3 s, the remote size is still A, and the frame's canvas fits inside the frame with its aspect ratio kept (ratio within 1 %).
  - Uncheck it. Within 5 s the remote size changes to fit 900 px wide.
  - Reload: the checkbox is still unchecked.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expect PASS.
- [ ] **Step 5: Commit.** Message: "web: Lock size".

### Task 7: The forward protocol (`internal/forward`)

**Files:**
- Create:
  - `supervisor/internal/forward/frame.go`
  - `supervisor/internal/forward/mux.go`
  - `supervisor/internal/forward/mux_test.go`

**Interfaces:**
- Frame: `type u8 | stream u32 BE | payload`. It travels as one WebSocket binary message, or length-prefixed (`u32 BE`) on stdio. Types:
  - `Listen=1`: payload `port u16`. It asks the desktop side to listen on `127.0.0.1:port`; `stream` is the listener's id.
  - `Listening=2`: empty; the listen worked.
  - `Open=3`: payload `port u16`. A new connection; the receiver dials `127.0.0.1:port` on its side. The opener picks the stream id. Ids are odd from the CLI side and even from the desktop side.
  - `Data=4`.
  - `Close=5`: a half-close of the sender's direction.
  - `Reset=6`: payload a UTF-8 error. For a listener id it means the listen failed (`port in use`, `port below 1024`). For a stream it means the dial failed.
  - `Window=7`: payload `n u32`. It grants n more bytes. The initial window is 256 KiB per stream.
- `type Mux struct`:
  - `func NewMux(rw io.ReadWriteCloser, framed bool, side Side) *Mux`. `framed` is true for WebSocket message framing and false for the length prefix. `Side` is `CLI` or `Desktop`.
  - `func (m *Mux) Listen(port int) (*Listener, error)`: the CLI side, for in mappings.
  - `func (m *Mux) Dial(port int) (net.Conn, error)`: the CLI side, for out mappings.
  - `func (m *Mux) Serve(ctx context.Context) error`: the desktop side. It honours Listen (refusing ports below 1024) and Open (dialing loopback).
  - `func (m *Mux) Close() error`.
  - A `Listener` delivers `net.Conn`s as the desktop side opens them (`Open` from the desktop).
- Produces, for the supervisor: `func Peek(b []byte) (typ byte, stream uint32, payload []byte, err error)`, so it can observe Listen and Listening without running a Mux.

- [ ] **Step 1: Write failing tests** over `net.Pipe`, both framings:
  - `TestDialEcho`: a desktop-side echo server on a loopback port, then `Dial`. 1 MiB round-trips intact.
  - `TestListenAccept`: `Listen(p)` on the CLI side, then a TCP connection to `127.0.0.1:p` on the desktop side. It arrives on the `Listener`, and data flows both ways.
  - `TestListenErrors`: port 80 gives `Reset` "below 1024". A port already bound gives "in use", and a second, free mapping on the same mux still works.
  - `TestBackpressure`: a reader that stalls on one stream doesn't stop another stream from moving 1 MiB.
  - `TestHalfClose`: CLI closes its write side, and the desktop side reads EOF but can still write.
- [ ] **Step 2: Run.** `go test ./internal/forward`. Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run with `-race`.** Expect PASS.
- [ ] **Step 5: Commit.** Message: "forward: multiplexing protocol for port mapping".

### Task 8: `deskyard forward`, `forward-helper`, `/api/agent/v1/forward`, and `forwards`

**Files:**
- Create: `supervisor/internal/cli/app/cmd_forward.go`, containing:
  - `forward`, which is visible, `whereRemote`, group `shell`;
  - `forward-helper`, which is hidden and runs `Mux.Serve` on stdio, length-prefixed, as the desktop side.
- Modify: `supervisor/internal/cli/app/spec.go`:
  - Usage `forward [-R] PORT[:PORT]...`. Each argument is a mapping; `-R` before an argument makes it reverse.
  - Usage text and an example: `deskyard -d alice forward 3000 -R 5173`.
  - Regenerate the golden files.
- Create: `supervisor/internal/server/forward.go`: `agentForward`.
- Modify:
  - `supervisor/internal/server/agent.go`: route `forward`.
  - `supervisor/internal/server/viewers.go` and `desktops.go`: `Forwards []forwardJSON json:"forwards"`.
  - `supervisor/API.md`.
  - `supervisor/internal/cli/app/guide.txt`: a short "Ports" section.
- Test:
  - `supervisor/internal/cli/app/app_test.go` (with `clitest` fake: add `/forward`);
  - `supervisor/tests/test_cli.py`.

**Interfaces:**
- Consumes: `forward.Mux` and `Peek` (Task 7), and the agent token checks in `agent.go`.
- The CLI ↔ supervisor connection:
  - The first text message is `{"forwards":[{"direction":"in"|"out","port":n}]}`, where `port` is the desktop's port. After it, `Mux` binary frames, WebSocket-framed.
  - The supervisor starts `deskyard forward-helper` by exec as `user` (cwd `/workspace`, no TTY). It relays frames, converting WebSocket framing to length-prefixed and back.
  - It records the token's forwards in `s.forwards` for the desktop: the declared out ports, and the in ports once `Listening` is seen. It removes them when the WebSocket closes, and publishes `viewers` events on changes.
  - Revoking or expiring the token closes the WebSocket, as exec does.
- CLI behaviour:
  - **Inside a desktop without `-d`.** It exits 2 with "forward maps ports between this machine and a desktop; choose one with -d NAME".
  - **Local listeners.** It binds its local listeners first (`127.0.0.1` only) and keeps them across reconnects. For an `out` mapping, a local connection waits up to 10 s for the link, then is closed.
  - **Reconnect.** The backoff is 1, 2, 4, 8, 16, 30, 30 s, and so on. A 409 (stopped desktop) waits like any other failure. A 401 exits 1 with the server's message.
  - **Output.**
    - On stderr, one line per state change: `Linked to alice: 3000 → alice:3000, alice:5173 → 127.0.0.1:5173`, `Lost alice (desktop stopped), retrying in 4 s`, and `alice:3000: port in use`.
    - On stdout, one line per mapping once it's live: `in 3000 3000` or `out 5173 127.0.0.1:5173`, so scripts can parse it.

- [ ] **Step 1: Write failing tests.**
  - Go, against the `clitest` fake: `TestForwardArgs` (parsing `3000`, `3000:8000`, `-R 5173:5174`; bad specs are usage errors with exit 2), and `TestForwardReconnects`. In the second, the fake drops the WebSocket, then answers 409 twice, then accepts. The command reconnects and its local listener stays bound throughout. It also exits 1 on 401.
  - Integration, in `test_cli.py`:
    - `test_forward_in`: a local HTTP server on a free port P. Run `deskyard forward P`. Then `deskyard exec -- curl -s 127.0.0.1:P` returns its body.
    - `test_forward_out`: `python3 -m http.server 8765` in the desktop, through exec in the background. Run `deskyard forward -R 8765:Q`. Then `GET 127.0.0.1:Q` locally returns the listing.
    - `test_forward_listed`: while both run, `Desktop.forwards` lists `{"token":"cli test","direction":"in","port":P}` and `{"…","direction":"out","port":8765}`. After Ctrl-C, `[]` within 2 s.
    - `test_forward_port_in_use`: occupy local port Q, then `forward -R 8765:Q 9000`. The first prints "in use" and the second still works.
    - `test_forward_low_port`: `forward 80:80` reports "below 1024" in the desktop.
    - `test_forward_survives_restart`: restart the desktop while `forward P` runs. Within 60 s, the exec curl works again without restarting the command.
    - `test_forward_other_desktop_refused`: start `python3 -m http.server 8766` only in desktop B. With a token for desktop A, `forward -R 8766:Q` connects, but `GET 127.0.0.1:Q` fails (the connection is reset), because the helper runs in A.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement.** Rebuild the image, since the in-image `deskyard` gains `forward-helper`.
- [ ] **Step 4: Run.** Go tests, then `uv run pytest test_cli.py`. Expect PASS.
- [ ] **Step 5: Commit.** Message: "deskyard forward: map ports both ways over the agent API".

### Task 9: Mappings in the UI

**Files:**
- Modify:
  - `supervisor/web/src/api.ts` (`forwards: Forward[]`);
  - `live.tsx` (`viewers` events carry `forwards`);
  - `dev/mock*.ts`.
- Create: `supervisor/web/src/components/viewers-popover.tsx`. It lists the viewers (avatar, name, mic badge), then one line per token: `deploy-bot: 3000 in · 5173 out`.
- Modify:
  - `supervisor/web/src/desktop/desktop.tsx`: the avatars and a `⇄ N` chip (N = number of mappings, shown only when N > 0) open the popover.
  - `supervisor/web/src/pages/details.tsx`: the "viewing" card adds the same token lines under the viewers.
- Test: `supervisor/tests/test_ui.py`.

- [ ] **Step 1: Write a failing test.** `test_forwards_shown`: while `deskyard forward P` runs with token "cli test":
  - the desktop page shows the chip "⇄ 1";
  - clicking it shows `cli test: P in`;
  - the details page's viewing card shows the same line;
  - after the command stops, the chip disappears within 3 s.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expect PASS.
- [ ] **Step 5: Commit.** Message: "web: show port mappings with the viewers".

### Task 10: Docs and the full run

**Files:**
- Create architecture pages under `docs/Architecture/`, each with `partOf` and `uses` frontmatter:
  - `Sound server.md` (PipeWire, the virtual speaker and mic);
  - `Audio stream.md` (`audio-serve`);
  - `Audio proxy.md` (the supervisor hub);
  - `Port forwarding.md` (`forward`, `forward-helper`, `/api/agent/v1/forward`).
- Modify:
  - `docs/Architecture/Desktop.md`, `Deskyard CLI.md` and `Supervisor.md`: links and `uses`;
  - `README.md`: a sound and mic paragraph, and `deskyard forward`;
  - the spec's `status: implemented`, and this plan's `status: done`.

- [ ] **Step 1: Run every suite:**
  - `go test -race ./...`
  - `go test -tags engine ./...`
  - image `uv run pytest -m "not slow and not network"`
  - `supervisor/tests` in direct mode and with `DESKYARD_TEST_MODE=container`

  Expect all PASS. Put the A/V sync numbers from Task 4 in the plan's Status section.
- [ ] **Step 2:** Check the new architecture pages render with their diagrams: `sb --space "Container Computer" …` per `docs/AGENTS.md`.
- [ ] **Step 3: Commit.** Message: "docs: audio and port forwarding".
