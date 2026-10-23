<img src="assets/logo/deskyard-mark.svg" width="72" alt="">

# Deskyard

Small, persistent Linux desktops in containers, each shared between humans
and AI agents alike, run and shared by one app: the **supervisor**.

![The desktop: Chromium on this project's GitHub page, and a terminal using node (installed with mise) and the deskyard command](docs/screenshot.png)

The supervisor is one Go binary with its web UI built in. You sign in with
your own account and create, open, start, stop, restart, upgrade and remove
**desktops**, and see what each one is doing. Every desktop's desktop is
in the browser at `http://host/<name>/`, and everyone with access to it
opens the same desktop, at the same time if they like.

What's in a desktop:

- **XFCE desktop in the browser**, streamed with
  [Selkies](https://github.com/selkies-project/selkies): picture, sound,
  your mic, gamepads, and your webcam where the host has a virtual camera.
  It resizes to fit your browser window (or keeps a locked size), and the
  bar above it has the clipboard, keys the browser would otherwise take
  (Ctrl+Alt+Del, Alt+Tab, ...), quality presets, sound and mic, and a
  Download button. Drop files on the screen to copy them onto the desktop.
  Badges show who has their mic or camera on. On phones and narrow tablets,
  Lock size, Keys, Quality and Download are in the scrollable More menu.
- **One shared Chromium** with one profile: log in once on the desktop and
  the agent is logged in too. It starts when someone opens it or an agent
  connects to it.
- **Computer use**: a `deskyard` command for screenshots, clicks and typing
  on the same screen, inside the desktop and, with a token, from anywhere
  else.
- **Persistent installs**: `/workspace` (the user's home) is a plain folder
  on the host (`~/deskyard/<name>`). Tools installed with mise, npm, uv or
  Flatpak live there and survive restarts and image upgrades.
- **Agent skills** for Claude Code, Codex and other agents, so they know how
  to use all of the above.
- **Agent presence**: agents working on a desktop, inside it or from
  elsewhere, show up next to the people viewing it, with what they're
  running and have just done, and marks on the screen where they click and
  type. Typed text and secrets on command lines are hidden.

The desktop image is Debian 13, `linux/arm64` and `linux/amd64`. About 0.4 GB
of RAM per running desktop with Chromium open, so several fit on an 8 GB
machine.

## Install

There are no versioned releases yet. Everything below is **edge**: built
from `main` on every change, in the
[edge pre-release](https://github.com/zefhemel/deskyard/releases/tag/edge)
and the images' `latest` tags.

### The `deskyard` command

For agents and people outside a desktop (inside one, it's already there).
On Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/zefhemel/deskyard/main/install.sh | sh
```

It downloads `deskyard-<os>-<arch>` from the edge release, checks it
against `SHA256SUMS` and installs it to `~/.local/bin` (or, if that isn't
writable, to `/usr/local/bin` with sudo, saying so first). Set
`DESKYARD_VERSION` to install another release tag, and
`DESKYARD_INSTALL_DIR` to install elsewhere:

```bash
curl -fsSL https://raw.githubusercontent.com/zefhemel/deskyard/main/install.sh | DESKYARD_INSTALL_DIR=~/bin sh
```

On Windows, download `deskyard-windows-amd64.exe` (or `-arm64.exe`) from
the edge release.

### The supervisor

You need Docker (or OrbStack, or Docker Desktop) on Linux or macOS.

**The binary.** Download `deskyard-supervisor-<os>-<arch>` (`linux-amd64`,
`linux-arm64`, `darwin-amd64`, `darwin-arm64`) from the edge release and run
it:

```bash
curl -fsSLo deskyard-supervisor https://github.com/zefhemel/deskyard/releases/download/edge/deskyard-supervisor-linux-amd64
chmod +x deskyard-supervisor
./deskyard-supervisor
```

It finds the engine at `DESKYARD_ENGINE_HOST`, else `DOCKER_HOST`, else
the platform's default socket, keeps everything in `~/deskyard` (set
`DESKYARD_DATA_DIR` for another folder) and listens on `127.0.0.1:8080`. Its
user must be able to use the Docker socket. More settings: Configuration.

**In a container.** The image `ghcr.io/zefhemel/deskyard` (amd64 and arm64)
needs the Docker socket and the data folder, mounted **at the same path** as
on the host (see Storage):

```bash
docker run -d --name deskyard --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /srv/deskyard:/srv/deskyard -e DESKYARD_DATA_DIR=/srv/deskyard \
  -p 127.0.0.1:8080:8080 \
  ghcr.io/zefhemel/deskyard:latest
```

Or, from a checkout, `supervisor/compose.yaml` does the same:

```bash
cd supervisor
DESKYARD_DATA_DIR=/srv/deskyard docker compose up -d
```

### Desktop images

Nothing to install: the supervisor pulls
`ghcr.io/zefhemel/deskyard-debian-xfce:latest` when you create the first
desktop (see Images and upgrades).

## First start

On first start the supervisor creates the account `admin` and
prints its generated password once in its log (`docker logs deskyard`, or
`docker compose logs supervisor`, in a container), unless you set
`DESKYARD_ADMIN_PASSWORD`.
Open <http://localhost:8080>, sign in, and create a desktop: give it a name
like `alice` and its desktop opens at <http://localhost:8080/alice/>.

**On a server with a domain.** Set `DESKYARD_DOMAIN=desktops.example.com`,
point its DNS at the host and let ports 80 and 443 through: the supervisor
gets a certificate itself and serves HTTPS. In a container, publish `80:80`
and `443:443` instead of `127.0.0.1:8080:8080` (the compose file shows
where). Run directly on Linux, let the binary bind low ports with
`sudo setcap cap_net_bind_service=+ep ./deskyard-supervisor`. Without a domain, keep
the default localhost binding and put your own HTTPS proxy or Tailscale in
front.

### On a LAN

To reach Deskyard from other machines at home or in an office, either
serve it over https or accept what plain http costs.

**Plain http** (`DESKYARD_LISTEN=0.0.0.0:8080`, then
`http://192.168.1.20:8080`): browsers treat any address but `localhost`
over http as insecure and turn features off. The desktop page says so in
its bar ("Not secure: some features off"). What still works: the picture,
keyboard and mouse, sound, the Clipboard button's text box (both ways),
Lock size, Quality, upload, download and the agent overlay. Gamepads
should too in Chromium and Safari, which keep the Gamepad API over http. What doesn't:

- **Mic and camera**: the browser has no `getUserMedia`. The Audio panel
  shows them off, with "Needs https".
- **Automatic clipboard sync**: copy and paste go through the Clipboard
  button instead.
- **H.264 in Chrome and Firefox**: without WebCodecs the picture is JPEG.
  At 1920x1080 and the Medium preset that is about 40 Mbit/s while a
  terminal scrolls (H.264: about 6), and next to nothing on a still
  screen. Fine on wired gigabit, workable on good Wi-Fi with Low (about
  18 Mbit/s); not for the internet. When one viewer is on JPEG the whole
  desktop streams JPEG, also to viewers on https.
- **Passwords travel in clear text.** Only on a network you trust.

**https** keeps everything. Without a public domain, the simplest is
Tailscale: run the supervisor on its default `127.0.0.1:8080`, then
`tailscale serve --bg 8080` on the same machine, and open
`https://<machine>.<tailnet>.ts.net` from any device on your tailnet. Or
put any reverse proxy with a certificate in front (Caddy:
`reverse_proxy 127.0.0.1:8080`); it must pass WebSockets through and keep
the `Host` header. With a public domain, `DESKYARD_DOMAIN` does it
itself (above).

## Desktops

Admins create desktops with a name, an image from the catalog, and
optionally a memory and CPU limit (by default a desktop may use all of the
host's). Names are lowercase letters, digits and dashes, start with a letter
and can't be changed later. Each desktop is a container `deskyard-<name>` on its
own network `deskyard-<name>`, so desktops can't reach each other.

### What persists

Only `/workspace`. It's the folder `<data folder>/<name>` on the host, a
plain folder you can browse and back up with host tools. Creating a desktop
with the name of a folder that already exists reuses that folder, so to copy
a desktop, copy its folder and create a desktop with the new name.
Removing a desktop keeps its folder unless you tick "also delete files".

| Need | Command |
|---|---|
| Runtimes and CLI tools | `mise use -g node@22 python@3.12 go jq ripgrep` |
| npm CLIs | `npm i -g <pkg>` |
| Python CLIs | `uv tool install <pkg>` |
| Desktop apps | `flatpak install --user flathub <app-id>` |
| Anything else | put binaries in `~/.local/bin` |

These all land in `/workspace` and are on `PATH`. Node and Python come
preinstalled with the image (outside `/workspace`, so they update with it);
`mise use` wins over them. There's no `sudo`, and anything installed with
`apt` or changed outside `/workspace` is lost when the image is updated.

### Desktop state across restarts

When a desktop stops (stop, restart, upgrades), its native session manager
logs out and saves the session; Chromium saves its tabs independently.
Native session restoration applies to applications that support that
desktop's session protocol. XFCE uses Settings > Session and Startup;
KDE Plasma and MATE use their own session settings. Most Electron and
Flatpak apps do not restore their windows through this protocol.

Switching desktop images keeps your files, installed Flatpak apps, tools
and Chromium profile. Each desktop keeps its own settings; open windows
are not migrated between different desktop environments.

### Reclaiming memory

Admins can click **Reclaim memory** on a running desktop's details page to
ask the host to release idle memory while its apps keep running. The result
shows resident memory before and after; this includes file cache, and swapped
pages may still use compressed RAM. Savings vary and apps may briefly respond
more slowly when accessed again.

On supported hosts, each desktop also gets one automatic 128 MiB reclaim
attempt after 15 minutes with no viewers. A returning viewer resets the timer,
even if they leave again between checks. Active agents, desktop operations,
and missing or stale activity reports delay reclamation without consuming the
attempt. Failed attempts are not retried during that absence. Checks run every
30 seconds and requests run one at a time. Supervisor/container restarts start
a fresh timer. Admins can turn off **Automatically reclaim idle memory** in a
desktop's Settings; this saves immediately without a restart and survives
container recreation. The manual button remains available.

Automatic reclamation requires the desktop's activity service to send current
command-status heartbeats. Older desktop binaries with only blank heartbeats
keep running normally but skip automatic reclamation; update the desktop image
to use it. Activity detection covers operations recorded by Deskyard, not plain
shell commands or other unreported background work; use the opt-out for those.

This requires a rootful Linux Docker engine with cgroup v2 and swap available
to the desktop. It works through the Docker socket, including with the
supervisor running on macOS against OrbStack. The supervisor pulls a small
Alpine helper image if needed and removes its temporary helper containers
after each request. At boot it checks the engine's memory/swap controls and
tests the full helper path, including `memory.reclaim` with swappiness, on a
tiny disposable container. The log reports whether reclamation is enabled
and why it is disabled if a check fails. A failed check leaves the rest of
the supervisor running and disables the button with a reason. Restart the
supervisor after fixing host support to run the check again. Each request
still checks the selected desktop's swap settings; a failure there does not
disable reclamation for other desktops. No memory limits or host swap
configuration change.

### Images and upgrades

The **Images** page holds the catalog of images desktops can be created
from. XFCE, KDE Plasma and MATE are built-in choices, visible before download.
They share one Debian runtime; XFCE remains the default on first start:


| Desktop | Image | Graphical app installer |
| --- | --- | --- |
| XFCE (default) | `ghcr.io/zefhemel/deskyard-debian-xfce:latest` | Software |
| KDE Plasma | `ghcr.io/zefhemel/deskyard-debian-kde:latest` | Discover |
| MATE | `ghcr.io/zefhemel/deskyard-debian-mate:latest` | Software |

Choose **Install** on an available image to download it, or select it when
creating a desktop to download it on first use. Browsing does not download
images. Existing saved images and the configured default retain their order;
custom images remain alongside the built-in choices. Official choices stay
in the catalog and cannot be removed. Other contract-compatible references
can still be added with **Add image**.

MATE uses Arc, Papirus icons, Noto fonts and a blue wallpaper, with a single
dark bottom panel containing Files, Chromium and Software shortcuts. KDE keeps
its original Debian/Breeze appearance. Saved user appearance and panel settings
take precedence over the image defaults.

Open **Discover** or **Software** from the desktop application menu to find
and install Flatpak apps such as LibreOffice. Flathub is configured for the
desktop user; apps install in `/workspace` without an administrator password
and survive image upgrades. The first catalog refresh needs internet access.
LibreOffice is optional and is not bundled in the base images. Admins can
also preinstall apps through custom image recipes.

LibreOffice installation, launch and saving into the workspace have been
verified. Apps requiring document-portal file grants have a current limitation:
the container configuration does not expose the FUSE device needed by the
document portal.

In local ARM64 samples, idle memory without a viewer was about 213 MiB for
XFCE, 258 MiB for MATE and 522 MiB for KDE. See the [desktop validation
report](docs/Testing/Desktop%20variants%202026-10-23.md) for conditions,
viewer/browser measurements and screenshots. Those samples predate MATE's
theme refresh. New registry tags become
available after the image workflow publishes from `main`; local Bake
builds use `deskyard-debian-<desktop>:dev`.

CI rebuilds all three weekly (Debian security updates) and on every change, tagged `latest` and with the date
(`2026.10.22`). The supervisor checks downloaded images for newer versions daily (or on "Check
now"); available images stay undownloaded. The catalog shows **Available**,
**Installed** or **Update available** when desktops are behind.

Upgrading a desktop recreates its container on the new image with the same
settings and folder; the Images page can upgrade all desktops on an image
at once. Switching a desktop to another image works the same way. Upgrades
go forward only: Chromium won't open a profile written by a newer version.
Tools installed with mise, uv and npm bring their own runtimes and should
keep working across Debian versions; if one breaks, reinstall it.

Any image can be added to the catalog if it follows the image contract
(label `deskyard.contract=3`, user `user` with home `/workspace`,
Selkies behind Caddy on 8080); see the design in `docs/`.

Images this project builds carry the label `deskyard.kind`: `desktop` for
the desktop image and `supervisor` for the supervisor. List them with
`docker images --filter label=deskyard.kind`.

### Custom images

Admins can make their own images on the Images page with **New image**:
a base image plus mise tools, Flatpak apps (from Flathub, another remote or
a `.flatpakref`) and optional build commands. Or use **New image from this
desktop** on a desktop's details page to bake in tools, apps, settings and
dotfiles from its home folder; new desktops start with those files, and
nothing is overwritten. Logins and other secrets are left out unless you
tick them, and the image then shows a lock badge. Builds run in Docker on the
supervisor's machine and are checked so `user` gains no way to root. Rebuild
a custom image to pick up newer tools or a newer base. Desktops can still
install and override tools in their own home folder.

### Flatpak

Flatpak apps run inside bubblewrap, which needs namespaces that Docker's
defaults block. Every desktop therefore runs with Docker's default seccomp
profile plus the namespace and mount syscalls, `apparmor=unconfined` and
`systempaths=unconfined`. These are per-container options; nothing in
Docker's configuration changes. Chromium also uses them to run with its own
sandbox.

`systempaths=unconfined` leaves `/proc/sys` writable, and root in the
container is root on the host for those files. So nothing reachable by the
agent or the internet runs as root in a desktop: there is no `sudo`, Caddy
runs as its own user, and the supervisor runs every command as `user`.

On Ubuntu 24.04+ hosts, unprivileged user namespaces may be restricted by
AppArmor (`kernel.apparmor_restrict_unprivileged_userns`); this hasn't been
tested yet. If bubblewrap fails there, try
`sudo sysctl kernel.apparmor_restrict_unprivileged_userns=0`.

## Sharing

Accounts are a username and a password; there are no email resets or SSO.
Admins create accounts, set passwords and make accounts admin or not, and
choose per desktop which accounts may use it. Everyone can change their own
password.

| | Admin | Regular account |
| --- | --- | --- |
| See, open, start, stop, restart desktops | all | those it has access to |
| Create, remove, change settings, upgrade | yes | no |
| Reclaim memory | yes | no |
| Create agent tokens | yes | for desktops it has access to |
| Accounts, access, image catalog | yes | no |

Everyone with access to a desktop opens the **same desktop**: one screen,
one mouse, one keyboard, like two people at one desk. The desktop follows
the browser window of whoever resized last. The supervisor shows who's
viewing on the tiles, the details page and the desktop page's bar, but nothing
arbitrates between people (or agents) driving it at the same time.

## Agents

**Inside a desktop.** Install an agent into `/workspace` from the desktop
terminal and run it there:

```bash
curl -fsSL https://claude.ai/install.sh | bash          # Claude Code
npm i -g @openai/codex                                   # Codex
```

The image links its skills into `~/.claude/skills`, `~/.codex/skills`,
`~/.agents/skills` and `~/.gemini/skills` on every start:

- `workspace-computer`: what this machine is, how to install things so they
  persist, what not to touch.
- `workspace-browser`: drive the shared Chromium over CDP (on
  `127.0.0.1:9221` inside); ask the human to log in.
- `workspace-computer-use`: the `deskyard` command.

They update with the image. To customise one, replace its link with a real
directory; it won't be touched again.

**From elsewhere** (a laptop, CI, another sandbox), the only way in is the
`deskyard` command with an **agent token**. Anyone with access to a desktop
creates one on its Agents tab, with a name and an optional expiry; it's
shown once, with the line to paste on the agent's machine. Install
`deskyard` there (see Install), then:

```bash
deskyard remote add alice https://desktops.example.com/alice --token dyt_...
deskyard -d alice exec -- ls ~            # one command; exit code passed through
deskyard -d alice shell                   # interactive shell
deskyard cp ./notes.md alice:/workspace/notes.md    # and back
deskyard -d alice screenshot > s.png      # every screen action works remotely
deskyard -d alice click 640 400
deskyard -d alice browser connect --port 9222   # the shared Chromium on local 127.0.0.1:9222
```

With `browser connect` running, Playwright's
`chromium.connect_over_cdp("http://127.0.0.1:9222")` (use
`browser.contexts[0]`, the shared profile), Puppeteer or chrome-devtools
tools work as if the browser were local. `deskyard remote default alice`
saves typing `-d`; `DESKYARD_URL` and `DESKYARD_TOKEN` work without a config
file. The command explains itself to agents: `deskyard --help`, `desktop
guide` (a short manual) and `deskyard describe --json` (every command as a
tool schema).

A token reaches one desktop's shell, files, browser and desktop, nothing
else: no other desktops, no lifecycle, settings or accounts. Agents can't
start a stopped desktop. Revoking a token, or its expiry, closes its open
connections at once, and a token dies with its creator's access.

### Port mapping

Reach a server on one machine from the other, with the same agent token:

```bash
deskyard -d alice forward 3000 &      # your 127.0.0.1:3000 is localhost:3000 in the desktop
deskyard -d alice forward -R 5173 &   # the desktop's localhost:5173 is your 127.0.0.1:5173
```

The first lets the desktop (or an agent in it) call a service running on your machine; the second lets you open a dev server running in the desktop in your own browser. To use different port numbers, write `LOCAL:DESKTOP` for a plain mapping (`8080:3000`: your 8080 is the desktop's 3000) and `DESKTOP:LOCAL` after `-R` (`-R 80:8080`: the desktop's 80 is your 8080). Several mappings can share one command (at most 64 plain ones and 256 in all), and one token can hold four such links per desktop. Everything goes over the existing HTTPS connection (no extra ports are opened on the desktop host) and listens only on `127.0.0.1`. Only *listening* in the desktop is limited to ports from 1024 up, so plain `forward 80` is refused while `-R 80:8080` works (the helper just dials). If the connection drops or the desktop restarts, `deskyard` reconnects by itself with backoff (up to 30 s): `-R` local ports stay bound meanwhile, and plain mappings come back in the desktop once the link is back. People watching the desktop see which ports your token maps (up to 64 per token are listed). "Linked to …" and the `out` lines are printed when the link is accepted, a moment before the desktop helper is known to be running.

## Configuration

Environment variables of the supervisor:

| Variable | Default | |
|---|---|---|
| `DESKYARD_LISTEN` | `127.0.0.1:8080` (`0.0.0.0:8080` in the image) | address for plain HTTP |
| `DESKYARD_DOMAIN` | | serve HTTPS for this domain on 80/443, with an automatic certificate |
| `DESKYARD_DATA_DIR` | `~/deskyard` (required in a container) | workspace folders, and `.deskyard/` for the supervisor's own state |
| `DESKYARD_ENGINE_HOST` | `DOCKER_HOST`, else the default socket | the engine to drive |
| `DESKYARD_SUBNET` | `10.213.0.0/16` | each desktop's network gets a /28 from it |
| `DESKYARD_ADMIN_USER` | `admin` | first start only: the first admin |
| `DESKYARD_ADMIN_PASSWORD` | generated, printed once | first start only: its password |
| `DESKYARD_IMAGE` | `ghcr.io/zefhemel/deskyard-debian-xfce:latest` | the catalog's first image |

### Storage

`DESKYARD_DATA_DIR` holds each desktop's folder (`<name>/`, owned by UID
1000) and `.deskyard/` with `accounts.json`, `tokens.json`, `images.json`, `memory-policy.json`,
the session key and certificates (all mode 0600). The supervisor keeps no
list of desktops of its own: desktops are containers with
`deskyard.*` labels.

In a container, the data folder must be mounted **at the same path** as on
the host (`-v /srv/desktops:/srv/desktops`), because the supervisor hands
those paths to the engine as bind mounts for desktops. It checks this on
startup and refuses to run, with an explanation, if they don't match.

## Security

- **The engine socket is root on the host.** Whoever can use it owns the
  machine, so the supervisor is the most privileged thing there. Run
  directly on Linux, its user needs the `docker` group, which is
  root-equivalent; in a container, mounting the socket is the same.
- Every route but the login page needs a signed-in account; agent tokens
  work only on the agent API. Failed logins are throttled.
- Desktops have no password and nothing else of their own facing the
  network: only Caddy on 8080, which wants a per-desktop token that only
  the supervisor has. With the supervisor in a container, desktops publish
  no ports; run directly, each publishes only 8080 on host `127.0.0.1`.
- The supervisor never serves pages from a desktop: it serves the desktop
  client itself (taken from the desktop's image) and proxies only the
  desktop's WebSocket and, for agents, the browser's CDP.
- No `sudo` in desktops, and every command the supervisor runs in one runs
  as `user`.
- Over plain HTTP, passwords travel in clear text: keep the default
  `127.0.0.1` binding unless the network is trusted, or set
  `DESKYARD_DOMAIN` for HTTPS.

## Repository layout

```
supervisor/
  cmd/deskyard-supervisor/     the supervisor
  cmd/deskyard/     the deskyard command (inside desktops and for agents elsewhere)
  internal/         engine interface and Docker implementation, accounts, auth,
                    desktops, the HTTP server and proxy, the embedded UI
  web/              the UI: Preact + TypeScript + Tailwind, built with Vite
  tests/            integration tests against a real engine
  Dockerfile        node build -> go build -> alpine
  compose.yaml      the supervisor in a container
images/
  Dockerfile       common build stages and three final desktop targets
  common/          shared rootfs and streaming patches
  debian-xfce/     XFCE rootfs overlay
  debian-kde/      KDE Plasma rootfs overlay
  debian-mate/     MATE rootfs overlay
  tests/           shared desktop image tests
docs/               design notes (a SilverBullet space)
```

CI: `.github/workflows/images.yml` builds all `docker-bake.hcl` targets together for
amd64 and arm64 on native runners, weekly and on change;
`.github/workflows/supervisor.yml` tests the supervisor, publishes its image,
builds the `deskyard-supervisor` and `deskyard` binaries and puts them in the
`edge` pre-release (main) or the GitHub release (tags `v*`);
`.github/workflows/tests.yml` runs the integration suites (see Tests).
`install.sh` installs `deskyard` from a release.

## Development

The supervisor needs Go and Node 24:

```bash
cd supervisor
make build          # the UI, then bin/deskyard-supervisor and bin/deskyard
make test           # Go unit tests, with the race detector
make test-engine    # the tests that drive a real Docker engine (builds the
                    # desktop image first, unless CC_TEST_IMAGE names one)
make typecheck      # the UI's TypeScript and its tests
make release VERSION=v1.2.3   # cross-compiled binaries in dist/
make image          # the supervisor's container image
```

### Tests

From the top folder; the pytest suites need Docker, [uv](https://docs.astral.sh/uv/)
and, once, `uv run playwright install chromium` in each of
`images/tests` and `supervisor/tests`:

| Command | Runs | When |
|---|---|---|
| `make test` | Go (vet, race) and engine tests, the UI's, and both pytest suites without what's slow, needs the internet, builds images, measures timing, or runs WebKit. About 7 minutes on a quiet Mac, 10 under load. | Before every commit. |
| `make test-ci` | `make test` plus WebKit and Firefox: what CI runs on a pull request. | To check a browser difference. |
| `make test-all` | Everything, the supervisor suite also with the supervisor in a container. About 25 minutes; needs the internet. | Before a release, or after touching recipes, image builds (`recipes/`, `imagescan/`, image from a desktop), audio timing, or how the supervisor runs in a container. CI runs it on main and nightly. |
| `make test-image`, `test-supervisor`, `test-container`, `test-go`, `test-web`, `test-engine` | One part. | While working on it. |

While working on one area, narrow a suite: `make test-supervisor PYTEST_ARGS="-k drop"`,
or run pytest in the suite's folder (`uv run pytest test_ui_audio.py`).
`MARKS="-m ''"` lifts the default selection; markers: `slow`, `network`,
`build`, `perf`, `webkit` (left out by default) and `serial`.

Both suites run under pytest-xdist (`WORKERS`, default 4), then run the
timing-sensitive tests, marked `serial`, on their own (`pytest --only-serial`).
Each test gets a new desktop, taken from a few started ahead of it. The
supervisor suite runs a supervisor per worker, each on its own slice of
`DESKYARD_TEST_SUBNET` (default 10.215.0.0/20): give checkouts that test at the
same time different ones. The supervisor suite rebuilds the web UI first when
its sources are newer than the build. `TEST_IMAGE` names the desktop image the
suites build and test (default `deskyard-debian-xfce:test`); outside make,
`AC_IMAGE` (image suite) and `CC_TEST_IMAGE` (supervisor suite) name it.
Firefox is skipped where Playwright's Firefox can't start (on some Macs).

```bash
docker buildx bake --load     # all desktop targets, shared stages built once
cd images/tests
uv run playwright install chromium   # once
uv run pytest -m "not slow"
uv run pytest                        # also: memory budget, Flatpak app, agent CLIs
```

Screenshots and stats land in `images/tests/artifacts/<desktop>/`.
`AC_DESKTOP=kde` or `AC_DESKTOP=mate` selects another target (default: `xfce`);
`AC_IMAGE=<tag>` skips the build and should match `AC_DESKTOP`. The root Makefile
also accepts `make test-image AC_DESKTOP=mate` or `kde` and builds the matching
target. The supervisor integration suite still uses XFCE-specific terminal and
panel assertions, so full `make test` requires `AC_DESKTOP=xfce`. For example:

```bash
AC_DESKTOP=mate AC_IMAGE=deskyard-debian-mate:dev uv run pytest -m "not slow and not network"
```

To build only one image from the repository root:
`docker build --target debian-kde -f images/Dockerfile -t deskyard-debian-kde:dev .`.
XFCE panel/layout tests run only for XFCE; shared boot, security, stream,
Flatpak and persistence checks apply to each variant.
