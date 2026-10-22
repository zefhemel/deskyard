# container-computer

A Docker image that is a small, persistent Linux computer possibly shared between human
and AI agent alike.

![The desktop: Chromium on this project's GitHub page, and a terminal using node (installed with mise) and the computer command](docs/screenshot.png)

What's in it:

- **XFCE desktop in the browser** (KasmVNC), behind a password, with
  optional automatic HTTPS (Caddy). The desktop resizes to fit your browser
  window, and the clipboard is shared with your computer (in browsers that
  allow it: over HTTPS or on localhost).
- **One shared Chromium** with one profile: log in once on the desktop and the
  agent is logged in too. Agents drive it over CDP.
- **Computer use**: a `computer` command for screenshots, clicks and typing on
  the same screen.
- **Persistent installs**: `/workspace` (the user's home) is a folder on your
  machine (`./workspace` by default). Tools
  installed with mise, npm, uv or Flatpak live there and survive restarts and
  image upgrades.
- **Agent skills** for Claude Code, Codex and other agents, so they know how
  to use all of the above.

Debian 13, `linux/arm64` and `linux/amd64`. About 0.4 GB of RAM per running
instance with Chromium open, so several fit on an 8 GB machine.

## Quick start

```bash
cp .env.example .env        # optional: set WORKSPACE_PASSWORD, ports, ...
docker compose up -d
docker compose logs workspace | grep "WORKSPACE PASSWORD"   # if you didn't set one
```

Open <http://127.0.0.1:8080> and log in as `workspace` with that password.
The sandbox's home folder is `./workspace` next to the compose file (set
`WORKSPACE_HOME_DIR` to use another folder).

## Use the published image

Every push to `main` publishes a multi-arch image (amd64 and arm64) to
GitHub Container Registry: `ghcr.io/zefhemel/container-computer`. Tags:
`latest` (main), `sha-<commit>`, and `1.2.3` / `1.2` for release tags
`v1.2.3`.

With this repo's compose file, skip the local build:

```bash
cp .env.example .env
# in .env:
#   WORKSPACE_IMAGE=ghcr.io/zefhemel/container-computer:latest
#   WORKSPACE_PULL_POLICY=always
docker compose up -d
```

`WORKSPACE_PULL_POLICY=always` checks for a newer image on every `up`; use
`missing` to only pull once. Without cloning the repo, `docker run` needs
the Flatpak seccomp profile next to it:

```bash
curl -fsSLO https://raw.githubusercontent.com/zefhemel/container-computer/main/seccomp/flatpak.json
docker run -d --name workspace --shm-size 1g --memory 2g \
  -e WORKSPACE_PASSWORD='change-me' \
  -v "$PWD/workspace:/workspace" -v workspace-system:/var/lib/workspace-system \
  -v workspace-cache:/var/cache/workspace \
  -p 127.0.0.1:8080:8080 -p 127.0.0.1:5901:5901 -p 127.0.0.1:9222:9222 \
  --security-opt seccomp=flatpak.json --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  ghcr.io/zefhemel/container-computer:latest
```

The workflow is `.github/workflows/publish.yml`: each architecture builds
on a native GitHub runner (no emulation), then one job merges them into a
multi-arch tag. Pull requests build both without publishing.

## Where data lives

| Path in the container | On the host | Holds |
|---|---|---|
| `/workspace` | `WORKSPACE_HOME_DIR` (default `./workspace`) | everything the user and agent create or install: dotfiles, mise/npm/uv tools, Flatpak apps (several GB), the Chromium profile with its logins |
| `/var/lib/workspace-system` | the `system` named volume | the web password's hash and Caddy's HTTPS certificates |
| `/var/cache/workspace` | the `cache` named volume | re-downloadable data: `~/.cache` (mise, uv, pip, Chromium's disk cache), `~/.npm`, Codex's cache and temp folders, Chromium's component downloads |

The **cache volume** keeps caches out of the home folder. The paths listed
in `/etc/workspace/cache-paths` (in the image) are symlinks from
`/workspace` into it, re-created on every start, and existing contents are
moved there, not deleted. Losing it is harmless: remove it
(`docker compose down && docker volume rm <project>_cache`) to reclaim
space, and things are downloaded again as needed.

The **system volume** is kept apart from the home folder on purpose. It's
owned by the container's `caddy` user, so the agent (which has no root in
Flatpak mode) can't read or change the login; wiping `/workspace` doesn't
reset your password; and HTTPS certificates survive recreating the
container (re-requesting them on every start runs into Let's Encrypt rate
limits). `docker compose down -v` deletes it (new password, new
certificates); it never touches the home folder.

On Linux, set `PUID`/`PGID` to your own user and group ids so files in the
home folder stay yours. On macOS (Docker Desktop, OrbStack) ownership maps
to your user automatically.

## Access

| What | Where | Auth |
|---|---|---|
| Web desktop | `WORKSPACE_WEB_BIND:WORKSPACE_WEB_PORT` (default `127.0.0.1:8080`) | basic auth, user `workspace` |
| CDP (Chromium) | `127.0.0.1:WORKSPACE_CDP_PORT` (default 9222) | none, host localhost only |
| Raw VNC | `127.0.0.1:WORKSPACE_VNC_PORT` (default 5901) | none, host localhost only |
| Shell | `docker compose exec -u workspace workspace bash -l` | Docker |

**Password.** Set `WORKSPACE_PASSWORD`, or leave it empty and a password is
generated on first start and printed once in the logs. It's stored (hashed)
in the `system` volume, so it survives restarts and recreating the container.
To get a new generated one, delete it and restart:
`docker compose exec workspace rm /var/lib/workspace-system/auth.hash && docker compose restart`.

**Exposing it on the internet.** Either put your own proxy or Tailscale in
front (and keep the default localhost binding), or let Caddy get a
certificate: set `WORKSPACE_DOMAIN`, point its DNS at the host and use
`COMPOSE_FILE=compose.yaml:compose.domain.yaml` (publishes 80/443; one
instance per host). Setting `WORKSPACE_WEB_BIND=0.0.0.0` without HTTPS sends
the password in clear text; only do that on a trusted network.

The VNC and CDP ports have no password and give full control of the
desktop and browser. Compose binds them to host localhost and the image
only `EXPOSE`s the web port, so `docker run -P` won't publish them. Inside
Docker they listen on the container's network, so other containers on the
same Docker network can reach them: keep sandboxes on their own network
(compose does this per project) and don't attach untrusted containers.

The desktop's WebSocket only accepts requests whose `Origin` matches the
host the page was served from, so other websites can't reuse your
logged-in browser to open the desktop. If you put a proxy in front that
rewrites the `Host` header, make it preserve the original host.

## Agents

**Inside the sandbox.** Install an agent into `/workspace` from the desktop
terminal and run it there:

```bash
curl -fsSL https://claude.ai/install.sh | bash          # Claude Code
mise use -g node@22 && npm i -g @openai/codex            # Codex
```

The image links its skills into `~/.claude/skills`, `~/.codex/skills`,
`~/.agents/skills` and `~/.gemini/skills` on every start:

- `workspace-computer`: what this machine is, how to install things so they
  persist, what not to touch.
- `workspace-browser`: drive the shared Chromium over CDP; ask the human to
  log in.
- `workspace-computer-use`: the `computer` command.

They update with the image. To customise one, replace its link with a real
directory; it won't be touched again.

**From the host.** Install the host skill into your local agents with
`scripts/install-host-skills.sh` (or `--claude`, `--codex`, `--agents`,
`--gemini`). Agents then use:

```bash
docker exec -u workspace <container> bash -lc 'ls ~'           # commands
docker exec -u workspace <container> computer screenshot > s.png
docker exec -u workspace <container> computer click 640 400
```

and Playwright `chromium.connect_over_cdp("http://127.0.0.1:9222")` for the
browser (use `browser.contexts[0]`, the shared profile).

If you close Chromium on the desktop it stays closed; the next connection to
the CDP port (9221 inside, 9222 on the host) starts it again.

The human and the agent share one mouse and keyboard; nothing arbitrates
between them.

## Installing software

| Need | Command |
|---|---|
| Runtimes and CLI tools | `mise use -g node@22 python@3.12 go jq ripgrep` |
| npm CLIs | `npm i -g <pkg>` (needs node from mise) |
| Python CLIs | `uv tool install <pkg>` |
| Desktop apps | `flatpak install --user flathub <app-id>` |
| Anything else | put binaries in `~/.local/bin` |

These all land in `/workspace` and are on `PATH`, including for
non-login `docker exec`.

`apt-get install` writes outside `/workspace` and **is lost when the image
is updated**. Whether the user has passwordless `sudo` depends on Flatpak
support (see below): by default it's on only when Flatpak support is off.
From the host you can always run `docker exec -u root <container> ...`.

## Flatpak

Flatpak apps run inside bubblewrap, which needs namespaces that Docker's
defaults block. The default `compose.yaml` therefore runs the container with:

```yaml
security_opt:
  - seccomp=./seccomp/flatpak.json   # Docker's default profile + namespace/mount syscalls
  - apparmor=unconfined
  - systempaths=unconfined
```

or, with plain `docker run`:

```bash
docker run --security-opt seccomp=seccomp/flatpak.json \
  --security-opt apparmor=unconfined --security-opt systempaths=unconfined ...
```

These are per-container options; nothing in Docker's configuration changes.
Chromium also uses them to run with its own sandbox enabled (without them it
runs with `--no-sandbox`).

**Security consequence.** `systempaths=unconfined` leaves `/proc/sys` and
`/proc/sysrq-trigger` writable, and without user-namespace remapping, root
in the container is root on the host for those files: it can change host
kernel settings, which is a known way to take over the host. So in this
mode nothing reachable by the agent or the internet runs as root: the user
gets no `sudo` (unless you set `WORKSPACE_SUDO=1`, which effectively hands
the agent root on your Docker host), Caddy runs as its own user, and root
processes never run programs from the user's folders. If you need both
Flatpak and root inside, run Docker with `userns-remap` or rootless Docker.

**Turn it off** with `COMPOSE_FILE=compose.yaml:compose.no-flatpak.yaml`
(needs Docker Compose 2.24 or newer for `!reset`). `flatpak run` then
explains why apps can't start, and the user gets `sudo` back.

`seccomp/flatpak.json` is generated by `scripts/gen-seccomp.py` from the
profile Docker ships (github.com/moby/profiles).

Host notes: works on OrbStack and Docker Desktop. On Ubuntu 24.04+ hosts,
unprivileged user namespaces may be restricted by AppArmor
(`kernel.apparmor_restrict_unprivileged_userns`); this hasn't been tested
yet. If bubblewrap fails there, try
`sudo sysctl kernel.apparmor_restrict_unprivileged_userns=0`.

## Multiple instances

Each instance needs its own ports, home folder and project name:

```bash
WORKSPACE_HOME_DIR=./alice WORKSPACE_WEB_PORT=8081 WORKSPACE_VNC_PORT=5902 \
  WORKSPACE_CDP_PORT=9223 docker compose -p alice up -d
```

or keep one directory (with its own `.env`) per instance. The system volume
is per project. The compose file caps each instance at 2 GB RAM and 2 CPUs.

## Desktop state across restarts

When the container stops (`docker compose stop`/`restart`/`down`, image
upgrades), the desktop logs out of XFCE properly so it can save its session,
and Chromium saves its tabs. On the next start you get back:

- Chromium with its open tabs (a managed policy sets "continue where you
  left off"),
- terminals with their folders and tabs, file manager windows, and window
  positions (XFCE session saving; change it in Settings > Session and
  Startup).

Not restored: apps that don't support XFCE's session protocol, such as most
Electron and Flatpak apps (SilverBullet included). The saved session lives
under `~/.cache`, i.e. on the cache volume, so a fresh cache volume starts
a fresh desktop. A hard kill (`docker kill`) skips saving.

## Upgrading

Pull the repo and `docker compose up -d` (it rebuilds the image). `/workspace` and the
password are kept. Upgrades go forward only: Chromium won't open a profile
written by a newer version, so don't roll the image back. After a future
Debian major-version bump, tools installed with mise/uv/npm should keep
working (they bring their own runtimes); if one breaks, reinstall it
(`mise install`, `uv tool install --reinstall <pkg>`).

## Configuration

See `.env.example`. Container-level variables: `WORKSPACE_PASSWORD`,
`WORKSPACE_DOMAIN`, `WORKSPACE_RESOLUTION` (default `1280x800`; the size
the desktop starts at, until a browser window resizes it), `WORKSPACE_SUDO` (`auto`, `1`, `0`),
`PUID`/`PGID`.

In `.env`, keep `WORKSPACE_PASSWORD` in single quotes: Compose expands `$`
in unquoted values. The password is visible to anyone who can run
`docker inspect` or `docker exec` (including the agent, via its
environment).

## How it works

`workspace-init` (the entrypoint, as root) applies `PUID`/`PGID`, seeds
`/workspace` on first start, links the skills, prepares the password, then
runs supervisord with: system and session D-Bus, KasmVNC's `Xkasmvnc :1` (X
server, VNC server and web client on 127.0.0.1:6080), XFCE, Chromium (CDP on
an internal port; 9221 inside and 9222 for the host start it on demand),
websocat (KasmVNC speaks VNC only over WebSocket; this bridges it to plain
VNC on 5901), and Caddy in front.

## Testing

```bash
cd tests
uv run playwright install chromium   # once
uv run pytest -m "not slow"          # ~3 minutes, needs Docker and internet
uv run pytest                        # also: 3-instance memory budget, Flatpak app, agent CLIs
```

Tests build `container-computer:test` and drive real containers. Screenshots
and stats land in `tests/artifacts/`. `AC_IMAGE=<tag>` skips the build.
