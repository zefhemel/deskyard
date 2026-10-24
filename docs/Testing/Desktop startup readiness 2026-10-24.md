MATE's intermittent black screen investigation and startup barrier correction on 24 October 2026. This distinguishes the observed display-size race from the still-unidentified operation that caused the original startup delay. See [[Architecture/Desktop runtime]] and [[Architecture/Session]].

# Observed failure and temporary recovery

The user opened the local `/mate/` viewer after the supervisor was upgraded to commit `02898bb`. Its container restarted at 15:32:51 UTC. `start-selkies` waited ten seconds, then explicitly started despite missing `xvfb-ready`. A viewer selected a 1470×728 capture (1472×728 RandR mode). Native session starts then failed every ten seconds while the readiness marker remained absent. The marker appeared at 15:35:06.907665 UTC, 135.8 seconds after startup, and the delayed X initialization applied the initial 1280×800 mode. The native panel was below the viewer's 728-pixel capture. CLI screenshots showed an initially black root, followed by the wallpaper and bottom panel at the larger root height. Selecting the already-created 1472×728 mode recovered the panel in the viewer. That command was a temporary correction, not the permanent fix.

X accepted Selkies connections and a successful RandR request before the marker appeared, so the display was not continuously unresponsive throughout the 135-second interval. No trace was available for the original blocked command. Forty-two isolated X starts and one full normal startup did not reproduce that delay. The earlier input-recovery investigation is distinct from this late initial-mode overwrite; this change does not claim to identify the historical blocked operation.

# Corrected invariants

`images/common/rootfs/usr/local/bin/start-xvfb` gives each handshake a one-second deadline within a ten-second connection period, bounds mode generation/selection/verification, reports the failed stage, and verifies actual requested dimensions before publishing readiness. It does not silently accept failed RandR operations. Every exit removes the marker; cleanup terminates, then kills and reaps a stubborn owned Xvfb child. Supervisord retries failed initialization.

`start-selkies` requires both the initial X readiness marker and the existing `desktop-ready` probe before entering audio or streamer startup. Missing readiness causes a supervised retry. A responsive but empty root window is insufficient; the native panel and window manager must be running. Viewer configuration therefore follows initial display/session initialization rather than racing it.

# Verification

The script tests run through Python unittest without Docker. They exercise actual shell branches and blocking subprocesses at the external command boundary: missing X readiness, missing native panel, ready barrier admission, failed handshake, a hung handshake, failed mode selection, incorrect actual dimensions and ignored TERM cleanup. The unsafe branches were observed failing before implementation. Native GNU timeout, actual Xvfb and clean shutdown were also verified in a disposable Linux container at 1024×768. No test changed the user's display.

Real full-desktop acceptance uses a startup-script-only overlay over the published immutable MATE tag `sha-02898bb`, independently of unrelated uncommitted Selkies patches. Verification uses new private workspace volumes and uniquely labeled containers, removes only those resources, and covers cold boot, restart, real browser rendering/input/resizing and a deliberately delayed X startup. Results are recorded below after completion.

The real checks passed on ARM64: cold boot 4.12 seconds, second cold boot 2.55 seconds plus a successful restart, and forced delayed startup 18.08 seconds. Before X started, the delayed case had neither a readiness marker nor a streamer process. Each case successfully rendered, resized from 1000×640 to 1400×900 and then 1470×728, opened Run Application through browser keyboard input, typed test text and dismissed the dialog. Actual RandR root dimensions ended at 1472×728 in all three cases, preserving the capture height and visible panel. All private containers and workspace volumes were removed. [Machine-readable results](Desktop%20startup%20readiness%202026-10-24/results.json).

![Real browser viewer after deliberately delayed startup](Desktop%20startup%20readiness%202026-10-24/delayed-viewer.png)

# Local rollout

A second two-script overlay was built on the live MATE container's existing immutable base, preserving its other image layers and retaining `deskyard-debian-mate:before-startup-readiness` as a rollback tag. The first local build attempt used a raw config ID in `FROM`; BuildKit treated it as a registry reference and failed. Catalog admission therefore failed before any replacement. Tagging the existing image locally resolved the build boundary. The completed image `deskyard-debian-mate:startup-readiness-local` (`sha256:b543952acd20cd7a69fcfa93aa8f0929684d11b4a7dd56de1a5ed51323882761`) was admitted and applied through the normal authenticated settings job.

Only `mate` was recreated; stable identity, workspace and settings were preserved. After replacement, the log records initial readiness at 1280×800 before viewer capture starts at 1470×728. The actual live root is then 1472×728, both native panel and Marco are running, and the CLI screenshot shows the panel inside the captured area. The supervisor on port8080 remains commit `02898bb`; the application Go/UI source did not change in this startup-script follow-up. A separate local testing admin's credentials are stored outside Git; no passwords or access tokens are included in this record.

![Live MATE after the corrected startup ordering](Desktop%20startup%20readiness%202026-10-24/live-fixed.png)

Final local checks passed: full Go race suite, TypeScript and 64 UI unit tests, production builds, eight standalone startup cases and six Chromium transport cases. The first UI verification found the shared `node_modules` directory empty; restoring locked dependencies with `npm ci` resolved that environmental failure. Independent review found no blocking startup ordering/deadlock issue. The native readiness probe is the existing panel/window-manager contract; it does not claim every saved application setting is quiescent. Native AMD64 CI and the full Linux native/container suites remain separate verification.
