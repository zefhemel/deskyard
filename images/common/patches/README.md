# Runtime patches

## Keyframe repair budget

`selkies-keyframe-budget.diff` checks Selkies 2.1.0's display-wide 250 ms
keyframe interval before consuming a secondary viewer's one-second repair
allowance. A request coalesced with a recent display keyframe must not spend
that allowance: otherwise the viewer's next repair is suppressed for a second
without receiving a replacement picture. Both existing rate limits remain.
`images/tests/native/selkies-keyframe-budget.py` executes the installed request
branch with a controlled clock and checks rejection, the next accepted repair,
and the retained per-viewer limit; `test_selkies.py` runs it in the image suite.

## X input recovery

`selkies-x-input-recovery.diff` fixes Selkies 2.1.0's input backend remaining disconnected after an X reply timeout. The event watcher retries missing connections every 500 ms, with one worker at a time and the existing 20-second X reply/handshake bound. The worker prepares the display, keyboard and mouse before publishing them together. Initial connection failures also enter recovery; shutdown invalidates in-flight results, and a later lifecycle cannot inherit an earlier worker's connection. Cursor negotiation/cache and tracked pointer coordinates are refreshed with the connection. All event-reader X calls are inside its recovery boundary.

The image's existing `selkies-*.diff` build step applies this patch after the idle key-timer patch and fails if it cannot apply. `images/tests/test_selkies.py::test_input_recovers_after_x_connection_failure` runs `images/tests/native/selkies-input-recovery.py` against the installed package and a private Xvfb server. It holds a real server grab past the first reconnect timeout, checks delivered mouse-button and keyboard events after release, covers initial connection failure and closed-connection recovery, and rejects late reconnections after shutdown. It does not mock the X protocol or touch the desktop's session display. The native timeout is shortened only inside the test process. It runs with the rest of the image suite in CI.

This fixes permanent input loss after any X stall. The stall that exposed it was in the retired MATE desktop's settings daemon; the SilverBullet pages "Testing/MATE stream startup 2026-10-24" and "Testing/MATE first stream stall 2026-10-26" record the reproductions and verification.

## Selkies idle-CPU patches

Three patches that cut what an idle desktop costs. Without them, a desktop
with one idle viewer used about 11 % of a core, nearly all of it spent
finding out that nothing on screen or in the speaker had changed. A desktop
nobody watched used about 1.4 %. The Dockerfile applies them at build time,
and the build fails if one doesn't apply exactly (`git apply` allows no
fuzz). A version bump that moves the patched code then stops the build,
instead of shipping without the patch. Where the CPU went, and how each
patch was measured: `docs/superpowers/spikes/2026-10-22-selkies-cpu.md` on
the `claude/selkies-spike-98a338` branch.

| Patch | Against | What it does |
| --- | --- | --- |
| `selkies-key-timers.diff` | Selkies 2.1.0 (`selkies/input_handler.py`) | Selkies' key auto-repeat loop (50 Hz) and stale-key sweep (10 Hz) run from startup, viewer or not. They now sleep while no key is held: the first waits for a key press, the second checks once a second for gamepad heartbeats. Nothing changes while keys are held. This matters for desktops nobody watches. |
| `pixelflux-idle-grab.diff` | pixelflux 2.2.1 (`pixelflux/src/x11/mod.rs`, `pixelflux/src/encoders/software.rs`) | pixelflux copies the whole screen out of Xvfb 60 times a second and hashes every copy, even when nothing changed. It now skips a timer tick's grab when X DAMAGE reported nothing and the encoder has settled (its last frames came out empty, with every paint-over sent), and still grabs once a second as a keepalive. Any damage wakes the next frame at once. It doesn't apply when pixelflux draws the cursor into frames (off in Selkies by default). Turbo's encoder never settles, so with Turbo the grabs stop after 2 s without damage. `PIXELFLUX_IDLE=0` turns it off. |
| `selkies-audio-gate.diff` | Selkies 2.1.0 (`selkies/websockets_mode.py`) | While a viewer is connected, Selkies records the speaker all the time, even in silence, which keeps PipeWire and pcmflux's capture thread busy. With the gate on, it stops the capture once no stream has played into the speaker for `DESKYARD_AUDIO_IDLE_S` seconds, and starts it again when one does. It watches PulseAudio's sink-input events to know. **Off by default:** start-selkies sets `DESKYARD_AUDIO_IDLE_S=0` unless the image's environment sets it, because the first ~90 ms of a sound after silence is lost while the capture restarts, which swallows short sounds (chat pings, the terminal bell) for a saving of 0.6–1.4 % of a core per watched desktop. An image built on this one switches it on with `ENV DESKYARD_AUDIO_IDLE_S=3`. |

The Selkies patches are Python, applied to the installed package in
`/opt/selkies/lib/python3.13/site-packages`. The pixelflux patch is Rust, so
the image builds pixelflux itself instead of installing the PyPI wheel (the
`pixelflux` stage in the Dockerfile):

- the source is the `2.2.1` tag's commit, checked by its SHA-256;
- the codec libraries (libvpx, SVT-AV1, dav1d, libde265, x264, x265) are
  built from source by pixelflux's own wheel-build script, which pins each by
  commit. This is the same GPL build the PyPI wheel ships;
- Rust is pinned in `RUST_VERSION`, and the crates by pixelflux's
  `Cargo.lock`.

All three are Deskyard-local until upstream takes them, or something like
them. When bumping Selkies or pixelflux, refresh the patches against the new
source, or drop the ones that upstream has absorbed.

## Flatpak directory-resolution retry

`flatpak-openat2.diff` backports the upstream libglnx retry for transient `openat2()` `EAGAIN` failures into Debian trixie's security-patched Flatpak source. Without it, a valid GL-extension directory can intermittently produce `Extension org.freedesktop.Platform.GL.default has invalid merge-dirs`, preventing apps from starting. The upstream diagnosis and patch are in [Flatpak issue 6783](https://github.com/flatpak/flatpak/issues/6783). Only `EAGAIN` is retried; all resolution flags, permanent failures and Debian's security fixes remain intact.

The `flatpak` stage builds the CLI and shared library together, runs the deterministic regression in `images/tests/native/flatpak-openat2.c`, and installs the resulting Debian packages into the common runtime. `git apply --check` requires an exact-context match before Debian's patch stack applies the fix. A future Debian source version containing the fix must remove this backport instead of silently dropping it. The image carries the complete corresponding Debian source package, including the added patch, under `/usr/share/doc/flatpak/deskyard-source/`.

## XFCE HiDPI scaling

`selkies-xfce-hidpi.diff` extends Selkies 2.1.0's XFCE DPI application to set
`Gdk/WindowScalingFactor` and `Gdk/UnscaledDPI` alongside font DPI and cursor
size. This makes GTK panels and widgets follow the viewer's 1×/2× desktop
scaling without applying font scaling twice. It uses the existing session-bus
lookup and bounded command execution, and reverses both settings at 1×.

## Mac Alt modifiers

`client_hooks.py` removes the pinned client's Mac/iOS Option-to-Mode_switch/ISO_Level3_Shift mapping in both bundles. Both Option keys retain ordinary held Linux Alt key-down/key-up semantics for mouse clicks and drags; Command mappings and Ctrl/Shift remain unchanged. This is an image-client change, so existing desktops need an image upgrade. It chooses remote Alt semantics over macOS alternate-character modifier behavior.

With a downloaded Selkies 2.1.0 wheel, run `SELKIES_WHEEL=/path/to/selkies-2.1.0-py3-none-any.whl python3 -m unittest discover -s images/common/patches -p 'test_client_hooks.py'`. The package test patches both SHA-checked bundles and runs `test_mac_alt.js` against each: the actual key handlers and wire-send method must retain Alt until key-up on Mac, iOS, Linux and Windows; Ctrl/Shift checks cover unaffected paths. It also tests the native hook defaults and rejects changed client bundles. These checks require Node and do not replace physical Mac browser verification.
