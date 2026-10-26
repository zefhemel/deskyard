---
displayName: Desktop image protocol
tags: spec
status: implemented
date: "2026-10-26"
features:
  - "[[Features/Images and reusable environments]]"
---
What a container image has to provide for the supervisor to run it as a Deskyard desktop, and what each Deskyard feature additionally expects from it. This describes the contract as implemented today (image contract 3), not a proposed redesign. The built-in XFCE and MATE images implement all of it; see [[Architecture/Desktop runtime]] for how they do so.

The practical rule for custom images: build `FROM` an official Deskyard image (`ghcr.io/zefhemel/deskyard-debian-xfce` or `-mate`), add software, and leave the entrypoint, services, users and the paths below alone. `examples/retroarch/` and `examples/docker/` follow this pattern. Writing a compatible image from another base means reproducing everything in the next two sections.

# Required core
Without these the supervisor refuses the image, or the desktop never becomes usable.

| Requirement | Detail | Checked by |
| --- | --- | --- |
| Contract label | `deskyard.contract="3"` | `EnsureImage` in `supervisor/internal/desktops/service.go` refuses images without it when adding, installing or creating. |
| Entrypoint runs as root and takes the proxy token | The supervisor sets `WORKSPACE_PROXY_TOKEN` and passes no command. The entrypoint must refuse to start without it, make it readable only by the proxy, remove it from the environment of every user process, then start the desktop services. | `images/common/rootfs/usr/local/bin/workspace-init` |
| One HTTP door on port 8080 | Every request without `X-Deskyard-Token: <token>` gets 401. Nothing else listens on the container network. | `images/common/rootfs/etc/caddy/Caddyfile` |
| Readiness | `GET /` through port 8080 answers 200 only once the screen can be streamed. The supervisor polls it after create, start and recreate (`WaitReady`). The built-in images gate it on `xvfb-ready` and `desktop-ready` so a viewer never joins a half-started desktop. | `start-xvfb`, `start-selkies` |
| Stream | Selkies 2.1.0 in WebSocket mode behind the door: `/api/websockets` and `/api/health` reach it; other Selkies `/api/*` routes answer 404. | Caddyfile, [[Architecture/Selkies]] |
| Web client in the image | Selkies' web client at `/opt/selkies/lib/python3.13/site-packages/selkies/selkies_web`. The supervisor copies it out of the *image* (not the running container) and serves it with its own additions. A page advertising `<meta name="deskyard-client-contract" content="1">` gets the explicit-hook adapter; a page without the marker gets the legacy bridge; any other version is refused with an upgrade message. | `supervisor/internal/server/desktop.go` (`clientPath`, `screenAdapterTag`) |
| Desktop user | User `user`, UID and GID 1000, home `/workspace`. The entrypoint makes `/workspace` owned by 1000:1000; the supervisor's exec calls run as `user` with working directory `/workspace`. | `images/Dockerfile`, `workspace-init` |
| X display | The session runs on X display `:1`; tools in the image find it through `DISPLAY=:1`. | `images/Dockerfile` `ENV` |

# What the supervisor gives the container
The image can rely on these and should not need anything else from the host:

- `/workspace` bind-mounted from `<data>/<name>`. It is the only persistent storage; everything outside it resets on upgrade or recreation.
- `WORKSPACE_PROXY_TOKEN`; `DESKYARD_DOCKER_ACCESS=1` plus `/var/run/docker.sock` only when Docker daemon access is enabled.
- Administrator host mounts at their configured targets, a selected virtual camera device, 1 GiB of `/dev/shm`, memory and CPU limits.
- A seccomp profile that permits Flatpak's bubblewrap sandbox (`supervisor/internal/engine/docker/`), `unless-stopped` restart, and its own network, or a VPN gateway's network namespace when a VPN is selected.

# Per-feature expectations
An image can meet the core and still lack some of these; the listed feature then fails or is hidden while the rest of the desktop keeps working.

| Feature | The image provides | Without it |
| --- | --- | --- |
| Screen control, thumbnails and the Keys menu | `/usr/local/bin/deskyard` from the same Deskyard release, plus `xdotool` and `maim` working on `:1`. The supervisor runs `deskyard screenshot -`, `deskyard size` and `deskyard key …`. | CLI screen commands, wall thumbnails and sent key combinations fail. |
| Remote exec, shell and file transfer | `sh`, `bash` (login shell for `deskyard shell`), `cat`, `mkdir` and GNU `tar`. Uploads dropped on the screen land in `/workspace/Desktop`. | Exec, `deskyard cp`, downloads and uploads fail. |
| Shared terminals | Label `deskyard.terminal="tmux-v1"`, `tmux`, and the `deskyard terminal-*` helpers. Desktop terminal launchers that join the same tmux server are optional (`deskyard-terminal-configure`). | The Terminal page and session list answer 409; the Terminal page explains that the image lacks shared terminals. |
| Port forwarding | `deskyard forward-helper` runnable as `user`. | `deskyard forward` fails. |
| Shared Chromium and CDP | A DevTools endpoint on `127.0.0.1:9221` that answers `Host: localhost`, reached through the door at `/_cdp/`. The built-in images start Chromium on the first connection (`cdp-connect`). | The agent browser API and `deskyard browser connect` fail. |
| CLI activity and idle memory reclaim | `deskyard activity-serve` on `127.0.0.1:6092`, reached unbuffered at `/_activity/`, sending `commandsRunning` heartbeats. Exec calls set `DESKYARD_ACTIVITY=off` for supervisor-initiated work. | Activity marks and agent presence are missing, and automatic memory reclaim skips the desktop. |
| Details page tools and processes | `ps` (procps), `timeout` and `mise`; image-level mise installs under `/opt/mise`. | The tools and process lists are empty or show an error. |
| Docker daemon access | At root startup, add `user` to the numeric group of the mounted socket when `DESKYARD_DOCKER_ACCESS=1` (`workspace-docker-access`), before user services start; Docker client tools for the user. | The setting has no effect inside the desktop. |
| Sound, mic, webcam and gamepads | PipeWire with its PulseAudio layer, Selkies' virtual speaker and mic, the Input Interposer preload in the session, and handing a passed virtual camera's `/dev/video*` to `user`. | The corresponding media control is silent or unavailable. |
| Flatpak | Flatpak with a per-user Flathub remote so installs land in `/workspace`. | Software and `flatpak install --user` do not work. |
| Catalog display | `org.opencontainers.image.title`, `.version` and `.description` labels. | The catalog shows the bare reference with no version; update checks still compare image IDs but announce only "newer version". |

# Extension points for custom images
These are the supported ways for an image to add to a desktop without breaking the contract. `workspace-init` applies them at startup.

- `/opt/deskyard/home-template/`: copied as `user` into an empty workspace on first start, without overwriting existing files.
- `/opt/mise` plus links in `/opt/deskyard/bin`: system-wide tools on the user's `PATH` after their own shims.
- System-scope Flatpak applications, and extra remotes as `/etc/deskyard/flatpak-remotes/<name>.flatpakrepo`.
- Agent skills in `/opt/workspace/skills/`, linked into the usual agent skill directories.
- `ENV` overrides documented by the base image, such as `DESKYARD_AUDIO_IDLE_S` ([[Architecture/Selkies]]).

Software that must survive upgrades belongs in the image or in `/workspace`, never in other paths of a running container. Images carry no access semantics: anything baked into the image, including sign-ins in a home template, is shared with everyone who can use desktops made from it.

# Security expectations
The protocol relies on the image to keep the desktop's boundary intact: only the proxy can read the token; user processes run as `user` without sudo; the proxy runs as a separate non-root user the desktop user cannot replace; no other port is exposed. Desktops run with a seccomp profile permissive enough for Flatpak, so root inside the container must not be reachable by the desktop user. An image that breaks these is not a Deskyard image, even if it passes the label check.

# Limits and open questions
- The supervisor checks only the contract label and the client-contract marker. Every other requirement is discovered when the feature is used; apart from `deskyard.terminal`, there are no capability labels.
- The in-image `deskyard` binary is built from this repository, and the supervisor calls its subcommands. Compatibility between a newer supervisor and an older image's binary is not versioned or tested beyond the client contract; upgrading desktop images alongside the supervisor is the supported path.
- The image `HEALTHCHECK` (X display plus Selkies health) is informational: the supervisor uses `WaitReady`, not engine health.
- The `deskyard.kind="custom"` label is a legacy marker that only excludes old locally built images from registry update checks.
