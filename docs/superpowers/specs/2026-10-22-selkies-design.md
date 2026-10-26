---
displayName: Selkies screen design
tags: spec
status: implemented
date: "2026-10-22"
features:
  - "[[Features/Shared desktop streaming]]"
  - "[[Features/Mobile access]]"
---
Selkies streams the shared X11 desktop, sound, microphone, camera and gamepads over WebSocket through the supervisor. A supervisor-owned toolbar controls the embedded client. WebRTC/UDP, GPU encoding and separate per-viewer desktops are outside the current design.

# Image and trust boundary
Contract 3 images use Debian Xvfb on `:1`, Selkies 2.1.0, pixelflux 2.2.1 built from pinned source and pcmflux 2.2.0. They run behind token-protected Caddy, with Selkies on loopback. PipeWire supplies `deskyard-speaker.monitor` and Selkies supplies the virtual microphone. The shared image owns X/desktop readiness barriers before starting streaming; a listening X socket alone is insufficient.

The supervisor serves client assets extracted from the image, not HTTP pages from the running desktop. It proxies only allowed stream/health behavior and owns workspace upload/download endpoints. Selkies' native file transfers, sidebar, printing, sharing and screenshots are disabled. Uploads go to the desktop; file browsing/download is limited to `/workspace`, as `user`. Tokens use the remote files API rather than browser-session routes.

Pinned client patches expose a versioned v1 hook contract with exact bundle SHA/replacement validation at image build. A strictly typed supervisor adapter owns transport, input/media integration and browser fallbacks without replacing browser primitives. Native pass-through defaults keep direct Caddy clients functional. Pre-contract Selkies images use the explicitly retained legacy bridge; unknown advertised versions fail with actionable upgrade guidance. Upgrades must review the pinned integration points.

# Browser behavior
The toolbar provides clipboard, keys, quality, resolution lock/presets, audio/capture controls and files. The first connected page owns remote resizing; other viewers follow the shared screen. A locked viewer scales locally. Resolution preferences survive reload. Focused application/window views crop the same shared stream and input; they cannot create an independent seat or change resolution independently.

Sound requires browser activation and remembers browser-local playback settings. Mic/camera start off on load/reconnect, require appropriate secure-context permission, and report capture badges tied to the actual viewer connection. A failed badge acknowledgment must keep capture off. Disconnect removes its badge. Several viewers' microphones mix into the desktop's single virtual microphone. One viewer stopping capture or leaving must not remove another's audio; `test_two_mics` checks both tones, independent stopping and departure. Virtual cameras require configured host loopback devices; unavailable devices disable capture rather than silently activating it.

Plain LAN HTTP uses JPEG video and WASM Opus playback with a scoped main-thread audio fallback when secure WebCodecs/AudioWorklet paths are unavailable. The real insecure-context state remains intact; mic/camera show Needs https and automatic clipboard remains constrained by browser security. Manual clipboard is available. JPEG consumes substantially more bandwidth and main-thread audio can stutter under load.

Touch users have Tap/Trackpad modes, browser-local 1×–4× magnification, two-finger panning, fit controls and software-keyboard viewport sizing. Compact controls follow the visual viewport; clipboard drafts survive unrelated focus changes. The online Home Screen app has safe-area/standalone corrections and no offline cache. [[Testing/Mobile 2026-10-23]] and [[Testing/PWA iOS 27]] distinguish native evidence from emulation and retain device limitations.

# Recovery and performance
A background viewer must not stall another viewer. Served additions suppress destructive STOP_VIDEO and recover missing first pictures through bounded replacement-keyframe requests, stopping on picture arrival or transport change. The serial regression includes deliberately withheld initial video.

Build-time key-timer and pixelflux idle-grab patches reduce idle work; damage, keyframes and viewer joins wake capture. The audio gate is shipped but off by default: its modest CPU saving loses the first roughly 90ms after silence, swallowing short sounds. Measurements with all three patches enabled must not be represented as the default image footprint. [[Architecture/Selkies]] and `images/common/patches/README.md` hold current settings and patch rationale.

X recovery must restore input as well as video. The installed input backend retries missing connections, including failed initial handshakes, with at most one reconnect worker. It prepares display/keyboard/mouse before publishing them together; lifecycle generations discard late results after shutdown/restart. Private-Xvfb tests verify actual key/button delivery after failed reconnect, initial recovery and shutdown safety. These changes preserve the uncommitted input-recovery work. Native amd64 Actions execution after publication remains unverified. The original transient MATE startup X stall remains a separate unconfirmed cause; see [[Testing/MATE stream startup 2026-10-24]].

The retained sync target is sound no more than 45ms early or 125ms late, measured as the median over 30 beeps after warm-up on clean and 60±20ms delayed links. [[superpowers/spikes/2026-10-22-selkies|The Selkies spike]] records the comparison that motivated replacing untimed screen updates. Local Chromium timing evidence does not certify every browser/device. Existing Firefox startup failures, native Safari differences and Linux media rerun limitations remain scoped in the testing records, including [[Testing/CI transport regressions 2026-10-24]].
