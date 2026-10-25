---
displayName: MATE stream startup investigation — 24 October 2026
date: "2026-10-24"
tags: "verification"
result: "partial"
scope: "MATE video and input recovery after X display timeouts on the tested local images; native amd64 execution remains pending."
---
Investigation of `http://localhost:8080/mate/` remaining on “Waiting for stream” against the local supervisor and `ghcr.io/zefhemel/deskyard-debian-mate:latest` (reported image version `2026.10.24`). This records recovery and remaining uncertainty, not a permanent fix. See [[Architecture/Selkies]] and [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]].

# Observed startup failure

The container started at 13:16:04 UTC. Selkies accepted a viewer and began applying a change from 96 to 192 DPI around 13:16:07. Subsequently `xrdb -merge` timed out after five seconds; the RandR root physical-size update and input/cursor X connections also timed out. Docker health probes exceeded their five-second deadline and the container became unhealthy. Selkies reported that capture had delivered no frame after five seconds. This establishes an unresponsive X display during initial DPI configuration; it does not establish which X client or operation caused the display to stop responding.

At 13:17:14 UTC, capture initialized, the requested RandR mode applied and MATE panel applets restarted. The next health probe passed. Subsequent DPI changes to 120 and 96 completed. The existing browser tab visibly rendered the wallpaper and bottom panel with a Connected status. Recovery occurred before any service restart or source change.

# Reconnection checks

Reloading that tab subsequently left its screen iframe on `about:blank` with the outer page showing Connecting. A separate direct navigation to `/mate/screen/` loaded the client and started capture successfully. Closing that diagnostic tab, returning to Desktops in the original tab and reopening MATE restored the embedded stream, with visible wallpaper/panel and Connected status at approximately 13:22:22 UTC. The container remained healthy and that capture initialization did not report another X timeout. The cause of the pending iframe navigation remains unconfirmed.

The live UI bundle matched the freshly rebuilt bundle (`index-B6hcl_Xk.js`), including the existing WebSocket event-feed implementation. An initial suspicion that the supervisor still served the old permanent SSE feeds was disproved by this comparison. The supervisor and desktop container were not restarted or replaced during this investigation.

# Verification and limits

`make typecheck` passed TypeScript checking and all 46 UI unit tests. `go test -race ./internal/server` passed, and `make build` completed. Browser checks used the user's existing Chrome tab and a temporary direct-screen tab, which was closed afterward. Existing unrelated checkout changes were preserved. These checks verify recovered rendering and current build health; they do not reproduce the initial X stall deterministically or prove that a fresh MATE startup cannot stall again. No runtime code was changed.

# Input remained broken after video recovery

The user subsequently reported that the visible desktop did not respond to clicks. A browser click reproduced the lack of response. Selkies had logged `Could not reconnect input X display: Display connection closed by server: request reply timed out` during the startup stall and subsequently repeated `Cannot reset keyboard, X display or keyboard controller not available`. Reconnecting the browser and recovering capture had therefore not established input recovery. The rendering-only checks above were insufficient to establish a usable desktop.

At approximately 13:24:48 UTC, `docker exec deskyard-mate supervisorctl restart selkies` restarted only the streamer/input process. The desktop session and container remained running. The browser reconnected automatically. Clicking the bottom-left launcher then visibly opened MATE's applications menu; Escape followed by Alt+F2 opened Run Application, and typing `input-check` visibly populated its entry. Escape closed the test dialog without executing the text. This verifies both browser mouse and keyboard delivery after the service restart. A service restart is the observed workaround for this failed input state; automatic recovery after an X display timeout remains unverified and no permanent code fix is claimed.

# Recovery implementation and local deployment

The subsequent fix is `images/common/patches/selkies-x-input-recovery.diff`. Selkies' previous reconnect worker attempted one handshake and returned permanently on failure; the input event watcher idled while its display remained absent. It could also install a late reconnect after handler shutdown. The common image now retries missing input connections from the event watcher, prepares the complete backend off-loop and checks lifecycle generations before installing results. See [[Architecture/Selkies]] for the current implementation.

The installed-backend regression uses a private real Xvfb server, with its reply timeout shortened in the test process. The original image failed reconnect-after-timeout, late-shutdown-installation and initial-handshake recovery checks. The patched backend verifies delivered X button/key events after recovery, closed-connection recovery, failed initial handshake recovery and rejection of late shutdown results. The private X server uses `-noreset`, matching production: the first normal-image run exposed a fixture race in which Xvfb reset between cases and rejected the next observer connection; correcting that fixture made the normal-image regression pass. No test changes the user's session display or mocks the X protocol.

A derived patched MATE image passed all 20 selected tests in `test_selkies.py`, `test_web.py` and `test_variants.py`. The normal repository build also completed with `docker build --target debian-mate -f images/Dockerfile -t deskyard-debian-mate:input-recovery .`; the new patch applied cleanly after the existing patches. All three variant identity/browser checks passed against this normal image, and the corrected four-case input recovery regression passed on rerun. The shared CI smoke matrix now collects six tests per desktop, including recovery. Actionlint, Python compilation, documentation link/metadata checks and `git diff --check` passed. Code review reported no actionable findings. Native amd64 Actions execution remains pending.

At the user's request, the supervisor was rebuilt with `make build` and restarted on port 8080 using the existing `/Users/zef/deskyard` data directory and `0.0.0.0:8080` listen address. The normal MATE build was tagged locally as `ghcr.io/zefhemel/deskyard-debian-mate:latest`, retaining the old image as `deskyard-debian-mate:before-input-recovery`. These are local Docker tags; nothing was published to the registry. The normal authenticated upgrade flow recreated only `mate` on image `sha256:1f4a1ffa3842ecd688ef36fa01d0469e8b0e9868032b91210d41d9919f83383c`, with its workspace preserved. A future registry pull can replace the local latest tag until this fix is published.

The final supervisor listener was PID 10297; `/login` returned HTTP 200, MATE's container was healthy and its page showed Connected. Browser clicks opened the MATE menu, and typed text appeared in Run Application; verification text was cleared without executing it. No X timeout or reconnect failure appeared in the recreated container's startup/viewer logs, including 96→192→96 DPI changes. Five earlier standalone DPI changes on a fresh unmodified MATE desktop also completed in 0.17–0.26 seconds, so the original transient X stall's cause remains unconfirmed. The permanent input-loss path is reproduced and covered independently of that uncertainty.

The rebuilt supervisor also logged an existing `VPN recovery record missing; administrator repair required` warning for the separate VPN test state. It did not block MATE's non-VPN upgrade or input verification; VPN state was not repaired as part of this task.
