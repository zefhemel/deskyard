---
displayName: Desktop suspend checkpoint spike
date: "2026-10-23"
tags: "spike"
status: "concluded"
conclusion: "Minimal container checkpoint/restore passed, but the full XFCE desktop with sandboxed Chromium could not be checkpointed on the tested stack."
---
Can a Deskyard desktop save its memory to disk and resume later? **A minimal container passed suspend/resume, but the normal XFCE desktop with sandboxed Chromium could not be checkpointed on the tested runtime.** This is experimental feasibility evidence, not an implemented suspend feature or a guarantee about other CRIU versions and Linux hosts.

# Environment and isolation

- macOS ARM64, Docker context `orbstack`, Docker Engine 29.8.2 with experimental features enabled.
- Linux `7.2.9-orbstack-00391-g2b298d05fbd4`, aarch64; OrbStack CRIU 4.1; runc 1.5.2. `criu check` reported `Looks good.`
- Desktop image `deskyard-debian-xfce:test`, image ID `sha256:cf3604a5f919387c488de2afad71ca34f5af19dfb2d70be2e92640e50e9fb0f3`.
- Fresh desktop and named workspace volume, ordinary Flatpak seccomp/AppArmor/system-path options, 1 GiB shared memory, restart policy `no`, and only port 8080 published on a random macOS localhost port. No real desktop or workspace was changed.
- The successful host-network comparison ran only a shell counter, with no listening services. The desktop retained isolated networking throughout.
- A patched CRIU was built in a disposable Debian container and selected through PATH for individual host-namespace runc invocations. OrbStack's installed CRIU and daemon configuration were not changed.

# Results

| Probe | Observed result |
| --- | --- |
| Stock Docker checkpoint of Alpine shell counter, bridge networking | Failed during socket collection |
| Same counter with `--network=none` | Same failure |
| Counter sharing another disposable container's network namespace | Same failure |
| Counter with host networking | Docker checkpoint and restore passed; counter stopped while suspended and continued from its saved value |
| Counter with bridge networking and patched CRIU | Leave-running checkpoint passed; no restore tested for this variant |
| XFCE and Chromium with stock CRIU | Same initial socket collection failure |
| Normal XFCE/Chromium tree with patched CRIU | Failed: `Can't dump nested pid namespace` for Chromium's sandboxed zygote |
| XFCE with Chromium closed, patched CRIU and filesystem hints | Failed resolving an XFCE session inotify file handle |

One repeat of the host-network counter observed `973` before suspension, `973` after three seconds suspended, and `974` after restoring. The saved standalone diagnostic also passed with `2 → 2 → 3`, verifying log continuity without a counter reset. A browser page held a random JavaScript-only nonce, a timer and unsaved textarea text. After the failed normal-browser checkpoint, its nonce and text remained intact and its timer advanced from 0 to 27. **That verifies failure recovery, not successful browser restoration.** No complete desktop checkpoint was created, and desktop resume was therefore not tested.

# Diagnosed blockers

## Runtime compatibility

Stock CRIU logged `Sockects collect procedure family AF_INET proto IPPROTO_UDPLITE: -2` and the corresponding IPv6 failure, followed by `Dumping FAILED`. CRIU 4.1's `collect_err` treats the missing UDP-Lite protocol as fatal. The upstream implementation handles its absence as optional and notes its removal in Linux 7.1. Applying that specific change to CRIU 4.1 made the bridge-network baseline checkpoint pass.

The diagnostic source was CRIU tag `v4.1`, commit `b6059ff193a9b0dff98e997134d662c3ccfd1600`. Its only functional patch changed `if (gr->protocol == IPPROTO_RAW)` to `if (gr->protocol == IPPROTO_RAW || gr->protocol == IPPROTO_UDPLITE)` in `criu/sockets.c`. This backport establishes the cause of the baseline failure; it does not establish full support for this kernel.

References: [CRIU 4.1 socket handling](https://github.com/checkpoint-restore/criu/blob/v4.1/criu/sockets.c), [upstream socket handling](https://github.com/checkpoint-restore/criu/blob/criu-dev/criu/sockets.c).

## Application namespaces and process ancestry

The normal Chromium launch via the desktop's CDP service is a descendant of supervisord. CRIU 4.1 rejected its nested PID namespace before completing the dump. Browser sandboxes and other nested namespace workloads must be supported by the chosen runtime before transparent full-desktop suspend is viable. Flatpak applications were not exercised.

An earlier probe launched Chromium through detached `docker exec`. Those processes belonged to the container shim rather than the init process tree, and D-Bus held a pidfd for the excluded browser process. The probe was corrected to use the normal CDP launch before recording the nested-namespace result. A production design must also account for live exec sessions, including agents and terminals, rather than assuming every process is an init descendant.

## Filesystem watches

The overlay filesystem's inotify handles could not always be reopened directly. Adding CRIU `irmap-scan-path` hints for `/usr/share/dbus-1`, `/usr/local/share`, `/workspace`, `/run` and `/tmp` got past an initial D-Bus watch failure. The browser-closed comparison subsequently failed on a watch held by `xfce4-session`. The diagnostic did not exhaustively map or fix every watched path.

# Reproducing the probes

The adjacent [baseline-probe.py](2026-10-23-desktop-suspend/baseline-probe.py) is a **throwaway diagnostic**, not a product implementation or a desktop compatibility test. From the repository root, outside the sandbox:

```sh
python3 docs/superpowers/spikes/2026-10-23-desktop-suspend/baseline-probe.py
```

It creates a uniquely named Alpine counter with host networking, checkpoints it with Docker, verifies it remains stopped for three seconds, restores it, checks the next counter value, and removes its container and checkpoints. It requires an engine with experimental checkpoint support and CRIU installed. It does not modify daemon settings.

For the full-desktop probe, start a fresh desktop using `images/tests/sandbox.py`'s security and mount settings. Launch Chromium through Caddy's `/_cdp/` route, seed a page with an in-memory JavaScript nonce and unsaved textarea text, and disconnect CDP before checkpointing. A stock Docker checkpoint reproduces the UDP-Lite failure on the runtime above.

The patched diagnostic used `runc --root /run/docker/runtime-runc/moby checkpoint --leave-running --tcp-established --file-locks --image-path <fresh-host-directory> <disposable-container-id>`. It ran from the Linux host's mount, network, IPC, UTS and cgroup namespaces, using a wrapper that selected the private patched binary and libraries. This bypasses Docker's checkpoint bookkeeping and was used only for leave-running diagnostic dumps. Do not treat it as a production suspend implementation. Build commands, wrapper, browser probes and full logs remain in the throwaway macOS directory `/tmp/deskyard-criu-spike`; these temporary files are not durable project dependencies.

# Recommendation and limits

Do not add a Suspend button around stock Docker checkpointing yet. On this machine, a runtime compatibility fix is necessary but insufficient: the tested CRIU cannot checkpoint the normal sandboxed browser, and filesystem-watch handling needs more work even with the browser closed. Do not disable application sandboxes to make this feature appear to work.

If preserving arbitrary applications and browser memory is a hard requirement, evaluate a VM per desktop with VM snapshot/suspend support as an alternative architecture. If containers remain the requirement, first qualify a CRIU/runtime/kernel combination against nested namespaces, desktop filesystem watches and live exec processes on the actual Linux deployment host. This spike does not establish whether a newer CRIU solves all of those problems.

No supervisor APIs, lifecycle state, UI behavior or desktop images were changed. Existing [[Architecture/Lifecycle]] responsibilities remain implemented as documented. Container/host reboot, long suspension, viewer and agent reconnection, restore failures, GPU devices, KDE, MATE and Flatpak applications remain untested. Temporary probe containers, workspace volume, build image and Linux-side dump directories were removed after collecting evidence.
