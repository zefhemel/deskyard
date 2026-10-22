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

- **XFCE desktop in the browser** (KasmVNC). It resizes to fit your browser
  window, and the bar above it has the clipboard, keys the browser would
  otherwise take (Ctrl+Alt+Del, Alt+Tab, ...), a screenshot button and
  quality presets.
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

The desktop image is Debian 13, `linux/arm64` and `linux/amd64`. About 0.4 GB
of RAM per running desktop with Chromium open, so several fit on an 8 GB
machine.

## Quick start

You need Docker (or OrbStack, or Docker Desktop) on Linux or macOS.

**The binary.** Download the supervisor for your machine from the
[releases](https://github.com/zefhemel/deskyard/releases)
(`deskyard-supervisor-linux-amd64`, `-linux-arm64`, `-darwin-amd64`, `-darwin-arm64`)
and run it:

```bash
curl -fsSLo deskyard-supervisor https://github.com/zefhemel/deskyard/releases/latest/download/deskyard-supervisor-linux-amd64
chmod +x supervisor
./supervisor
```

It finds the engine at `DESKYARD_ENGINE_HOST`, else `DOCKER_HOST`, else
the platform's default socket, and keeps everything in `~/deskyard`.

**In a container.** `supervisor/compose.yaml` runs the image
`ghcr.io/zefhemel/deskyard`:

```bash
cd supervisor
DESKYARD_DATA_DIR=/srv/desktops docker compose up -d
```

The data folder must be an absolute path; it's mounted at the same path
inside (see Storage).

**Then.** On first start the supervisor creates the account `admin` and
prints its generated password once in its log (`docker compose logs
supervisor` in a container), unless you set `DESKYARD_ADMIN_PASSWORD`.
Open <http://localhost:8080>, sign in, and create a desktop: give it a name
like `alice` and its desktop opens at <http://localhost:8080/alice/>.

**On a server with a domain.** Set `DESKYARD_DOMAIN=desktops.example.com`,
point its DNS at the host and let ports 80 and 443 through: the supervisor
gets a certificate itself and serves HTTPS. In a container, publish `80:80`
and `443:443` instead of `127.0.0.1:8080:8080` (the compose file shows
where). Run directly on Linux, let the binary bind low ports with
`sudo setcap cap_net_bind_service=+ep ./supervisor`. Without a domain, keep
the default localhost binding and put your own HTTPS proxy or Tailscale in
front.

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

When a desktop stops (stop, restart, upgrades), the desktop logs out of
XFCE properly so it can save its session, and Chromium saves its tabs. On
the next start you get back Chromium with its open tabs, and terminals,
file manager windows and window positions (XFCE session saving; change it in
Settings > Session and Startup). Apps that don't support XFCE's session
protocol, such as most Electron and Flatpak apps, aren't restored.

### Images and upgrades

The **Images** page holds the catalog of images desktops can be created
from; it starts with `ghcr.io/zefhemel/deskyard-debian-xfce:latest`, the
`debian-xfce` image in this repository. CI rebuilds it weekly (Debian
security updates) and on every change, tagged `latest` and with the date
(`2026.10.22`). The supervisor checks for newer versions daily (or on "Check
now") and marks desktops whose image is behind with "Update available".

Upgrading a desktop recreates its container on the new image with the same
settings and folder; the Images page can upgrade all desktops on an image
at once. Switching a desktop to another image works the same way. Upgrades
go forward only: Chromium won't open a profile written by a newer version.
Tools installed with mise, uv and npm bring their own runtimes and should
keep working across Debian versions; if one breaks, reinstall it.

Any image can be added to the catalog if it follows the image contract
(label `deskyard.contract=1`, user `user` with home `/workspace`,
KasmVNC behind Caddy on 8080); see the design in `docs/`.

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
shown once, with the line to paste on the agent's machine. Download
`deskyard` for that machine from the releases (Linux, macOS, Windows), then:

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
1000) and `.deskyard/` with `accounts.json`, `tokens.json`, `images.json`,
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
  cmd/deskyard-supervisor/     the supervisor (the deskyard binary)
  cmd/deskyard/     the deskyard command (inside desktops and for agents elsewhere)
  internal/         engine interface and Docker implementation, accounts, auth,
                    desktops, the HTTP server and proxy, the embedded UI
  web/              the UI: Preact + TypeScript + Tailwind, built with Vite
  tests/            integration tests against a real engine
  Dockerfile        node build -> go build -> alpine
  compose.yaml      the supervisor in a container
images/
  debian-xfce/      the desktop image: Dockerfile, rootfs/, tests/
docs/               design notes (a SilverBullet space)
```

CI: `.github/workflows/images.yml` builds every image under `images/` for
amd64 and arm64 on native runners, weekly and on change;
`.github/workflows/supervisor.yml` tests the supervisor, publishes its image
and, for tags `v*`, attaches the `supervisor` and `deskyard` binaries to the
GitHub release.

## Development

The supervisor needs Go and Node 24:

```bash
cd supervisor
make build          # the UI, then bin/deskyard-supervisor and bin/deskyard
make test           # Go unit tests
make test-engine    # also the tests that drive a real Docker engine
make typecheck      # the UI's TypeScript
make release VERSION=v1.2.3   # cross-compiled binaries in dist/
make image          # the supervisor's container image
```

The desktop image's tests build it and drive real containers (needs Docker
and internet):

```bash
cd images/debian-xfce/tests
uv run playwright install chromium   # once
uv run pytest -m "not slow"
uv run pytest                        # also: memory budget, Flatpak app, agent CLIs
```

Screenshots and stats land in `tests/artifacts/`. `AC_IMAGE=<tag>` skips the
build.
