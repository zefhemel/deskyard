# Computer manager — design

Date: 2026-10-21
Status: draft, awaiting approval
Mockups: `docs/manager/mockups/` (HTML sources and rendered PNGs)

## Intent

Today one `docker compose up` gives one container-computer: a persistent
Linux desktop shared by a human and an AI agent. Running several means
juggling project names, ports, home folders and passwords by hand.

The manager turns this into something like a small VMware for these
desktops: one web app, behind one admin login, where you create, open,
start, stop, restart, clone and remove **computers**, and see what each one
is doing (CPU, memory, processes, logs). It runs as its own container and
drives Docker through the Docker socket. Each computer is an ordinary
container-computer container with its own workspace folder on the host,
reached in the browser at `https://host/<name>/`.

The audience is one admin (the owner of the machine), typically running a
handful to a few dozen computers on a laptop, a home server or a VPS.

## Vocabulary

- **Computer**: one container-computer instance (container + its workspace
  folder + its cache volume). Has a name, e.g. `alice`.
- **Manager**: this new app and its container.
- **Workspace folder**: the computer's `/workspace`, a folder on the host
  under the manager's data folder, e.g. `~/computers/alice`.

## Goals

- One admin username and password, entered on a real login page (a form,
  so password managers fill it), with a session cookie. Replaces basic auth
  for managed computers.
- A list of all computers with a live screen thumbnail, status, CPU and
  memory; one click to open a computer's desktop.
- Lifecycle: create, start, stop, restart, remove, clone.
- Per computer: live CPU and memory (current and last 15 minutes), process
  list, container logs, settings (memory/CPU limits, start size, Flatpak
  support, sudo, agent ports).
- Each computer's desktop at `/<name>/` on the manager's address (decided:
  path prefixes, not subdomains).
- Workspace folders are plain folders on the host (decided), so the admin
  can browse and back them up.
- Works on a laptop out of the box (`http://localhost:8080`), and on a
  server with automatic HTTPS when given a domain (decided).
- Upgrading the computer image is a manager action, keeping each
  computer's folder and settings.
- Backend in Go, frontend in Preact (decided), shipped as one image
  `ghcr.io/zefhemel/container-computer-manager`, multi-arch like the
  computer image.
- The computer image keeps working standalone with `docker compose`
  exactly as today.

## Non-goals (for now)

- Multiple admin users, roles, per-user access to individual computers,
  SSO/OAuth.
- Managing more than one Docker host.
- Snapshots/backups beyond clone (the workspace folder is on the host; use
  host tools).
- Publishing arbitrary ports from computers (an agent's dev server) on the
  manager's address. See "Path prefixes and the shared origin".
- A conversation UI for agents.
- Managing containers the manager didn't create.

## Anti-goals (failure even if it works)

- Any route of the manager reachable without the admin login, other than
  the login page itself and its static assets.
- A computer (i.e. an agent inside it) able to reach another computer's
  desktop, VNC or CDP, or the Docker socket.
- Losing a workspace folder through a UI misclick: removing a computer
  keeps its folder unless the admin explicitly ticks "also delete files".
- Stored state that can drift from what Docker says is running.

## Constraints and facts this design rests on

- **The Docker socket is root on the host.** The manager is the most
  privileged thing on the machine. It never runs agent-provided code and
  requires the admin session on every request.
- **Computers in Flatpak mode are dangerous as root** (`systempaths=
  unconfined`: container root can write host `/proc/sys`). The existing
  rule stays: nothing agent- or internet-reachable in a computer runs as
  root; the manager starts computers with the same security options
  compose uses today.
- **Bind-mount sources given to the Docker API are host paths**, not paths
  inside the manager container. The manager must know the host path of its
  data folder (see Storage).
- **The KasmVNC web client** loads its assets with relative URLs (works
  under a prefix), but opens its WebSocket at `/<path setting>` with
  `path` defaulting to `websockify`, i.e. at the site root. It stores its
  settings (including `path`) in `localStorage`, per origin.
- **The web client's settings are per origin**, so with path prefixes all
  computers share one set (scaling mode, quality, clipboard). Acceptable.

## Architecture

```
browser ──HTTPS──▶ manager container (Go)
                    ├─ /login, /logout, /api/*, /  (admin UI, Preact)
                    ├─ /<name>/            KasmVNC web client, served by the manager
                    │                      (extracted from the computer's image)
                    ├─ /<name>/websockify  ──WebSocket──▶ computer <name>:8080 (Caddy ─▶ KasmVNC)
                    └─ Docker socket ─▶ create/start/stop/stats/top/logs/exec
```

- **One Go binary** serves the UI (built Preact assets embedded with
  `embed`), the JSON API, the login, and the reverse proxy to computers.
  HTTPS via CertMagic (Caddy's ACME library) when `MANAGER_DOMAIN` is set;
  otherwise plain HTTP on `MANAGER_BIND:MANAGER_PORT` (default
  `127.0.0.1:8080`).
- **Docker is the database.** Every computer is a container labelled
  `container-computer.managed=true` and `container-computer.name=<name>`;
  its settings are labels too (see Settings). The manager keeps no list of
  computers of its own, so it can't drift from Docker. The only manager
  state is the admin password hash, session signing key and ACME
  certificates in its own volume.
- **Go Docker client** (`github.com/docker/docker/client`, API version
  negotiation on) for everything. No shelling out to the `docker` CLI.

### Networks: one per computer

Each computer gets its own bridge network `cc-<name>`; the manager is
connected to every one of them and nothing else is. So:

- the manager reaches `alice` at `cc-alice:8080` (container alias);
- `alice` can't reach `bob` at all (different networks), nor the Docker
  socket;
- `alice` *can* reach the manager's port on its network. That's fine: the
  manager requires the admin session for everything, the same as from the
  internet. The manager listens on all interfaces inside its container;
  publishing to the host is what limits exposure.

Created when the computer is created, removed with it. Docker's default
address pools only hold about 30 bridge networks, so the manager allocates
each network an explicit small subnet (a /28) from its own range,
`MANAGER_SUBNET` (default `10.213.0.0/16`, room for 4096 computers). On startup the
manager reconnects itself to any `cc-*` network of a managed computer it's
missing from (e.g. after the manager container was recreated).

### Computers in "managed" mode

The computer image gets one new setting, `WORKSPACE_AUTH`:

- `password` (default, today's behaviour): Caddy basic auth.
- `manager`: Caddy drops basic auth and instead requires the header
  `X-Container-Computer-Token: <token>`, where the token is a random value
  the manager generates per computer and passes in `WORKSPACE_PROXY_TOKEN`.
  This is defence in depth: only the manager is on the network anyway.
  The Origin check moves to the manager (see Proxy).

In managed mode the computer publishes no ports by default.

### `/<name>/`: web client from the manager, only the WebSocket proxied

A computer can't be trusted to serve pages on the manager's origin: the
agent runs as the same user as KasmVNC, so it can stop KasmVNC and serve
anything on its web port (or, with sudo, rewrite the client's files).
Script from such a page would run with the admin's cookie and could drive
the Docker socket. So the manager **never proxies HTTP content from a
computer**:

- The manager serves the KasmVNC web client itself, extracted from the
  computer's **image** (not from the running container, which the agent
  controls): on first use of an image ID the manager copies
  `/usr/share/kasmvnc/www` out of a never-started container created from
  that image, and caches it per image ID. Client and server versions
  therefore always match, with no version pinning in the manager.
- `/<name>` redirects to `/<name>/`. A request for `/<name>/` without a
  `path` query redirects to `/<name>/?path=<name>/websockify`. The client
  reads the query parameter before `localStorage`, so each computer
  connects to its own WebSocket even though they share stored settings.
- `/<name>/websockify` is the only thing proxied: a WebSocket to
  `ws://cc-<name>:8080/websockify`, with the per-computer token header
  added and the admin cookie removed (the computer never sees it).
  Anything else under `/<name>/` that isn't a client file is a 404.
- WebSocket upgrades are rejected (403) unless `Origin` matches the
  manager's own origin, the same cross-site WebSocket hijacking protection
  the computer's Caddy does today.
- A stopped computer's `/<name>/` shows a small manager page: "alice is
  stopped" with a Start button, instead of a proxy error.

### Path prefixes and the shared origin

All computers and the admin UI share one origin, so any script on it can
call the admin API with the admin's cookie. Every page on the origin
therefore comes from the manager (its own UI, and web client files taken
from images, never from running containers). What a malicious computer can
still do over its WebSocket: send arbitrary screen data to the client
(parsed by the client's decoders, no script), and put text on the admin's
clipboard while its desktop is open (the same as today's standalone
setup). Accepted.

- No "open port 3000 of alice at /alice/3000".
- If publishing agent-controlled web content is wanted later, it gets a
  separate origin (another port, or `*.apps.example.com`), not a path.
- The admin session cookie is `HttpOnly`, `SameSite=Strict`, `Path=/`,
  `Secure` under HTTPS.

### Names

`^[a-z][a-z0-9-]{0,30}[a-z0-9]$` (DNS label rules, so it also works as a
container, network and folder name). Reserved, because they're manager
routes: `api`, `login`, `logout`, `assets`, `static`, `healthz`,
`favicon.ico`, `robots.txt`, and anything starting with `_`. Names can't
be changed after creation (rename = clone + remove).

Docker names: container `cc-<name>`, network `cc-<name>`, cache volume
`cc-<name>-cache`.

## Storage

- `MANAGER_DATA_DIR`: the host folder holding workspace folders, e.g.
  `/Users/zef/computers` or `/srv/computers`. The manager container must
  mount it **at the same path** (`-v /srv/computers:/srv/computers`), so a
  path means the same thing inside the manager and to the Docker daemon.
  On startup the manager checks this by writing a marker file and
  verifying a short-lived helper container sees it at the same path; if
  not, it refuses to start with an explanation.
- Each computer's workspace folder is `$MANAGER_DATA_DIR/<name>`, created
  by the manager, owned by `PUID:PGID` (manager-wide setting, default
  1000:1000; irrelevant on macOS Docker Desktop/OrbStack).
- Cache: named volume per computer (`cc-<name>-cache`), as today; never
  cloned.
- The computer's `system` volume isn't needed in managed mode (no
  password, no per-computer certificates).
- Manager's own state: named volume `manager-data` at `/var/lib/manager`:
  `admin.hash`, `session.key`, `certmagic/`.

## Settings per computer

Chosen at creation, editable later. Applying a change recreates the
container right away (a restart; the UI says so); there's no "apply on next
restart", which would need pending state outside Docker:

| Setting | Default | Notes |
|---|---|---|
| Image | manager's `COMPUTER_IMAGE` (`ghcr.io/zefhemel/container-computer:latest`) | per computer, so one can test a new image |
| Memory limit | 2 GB | presets 1, 2, 4, 8 GB |
| CPUs | 2 | |
| Start size | 1280×800 | `WORKSPACE_RESOLUTION` |
| Flatpak apps | on | off = no special security options, `sudo` possible |
| Admin rights (sudo) | auto | `WORKSPACE_SUDO`; the UI explains the Flatpak trade-off |
| Agent ports | off | when on, VNC and CDP published on host `127.0.0.1` at free ports the manager picks; shown in the UI. They have no password. On Linux other containers can't reach them, but on Docker Desktop and OrbStack containers can reach host-published ports (`host.docker.internal`), so other computers could take over this one: the UI says so when turning them on. A token-checking proxy in front is a possible later improvement |

Stored as labels `container-computer.setting.<key>=<value>`, plus env vars
on the container as compose sets them today. `shm_size` 1g and stop grace
period 30 s as in `compose.yaml`.

## Operations

All long operations (create with image pull, clone, upgrade) run as jobs:
the API returns a job id; progress streams to the UI. Only one job per
computer at a time.

- **Create**: validate name; pull image if missing; make folder, cache
  volume, network; create container (`restart: unless-stopped`); connect
  manager to the network; start.
- **Start / stop / restart**: Docker API, stop with 30 s timeout (desktop
  session and Chromium tabs are saved, as today).
- **Remove**: stop, remove container, network, cache volume. The folder is
  kept unless "also delete files" was ticked, in which case it's deleted
  after the container is gone. Typed-name confirmation when deleting files.
- **Clone** `alice` → `alice-2`: requires `alice` stopped (the UI offers
  "stop, clone, start again"). Copies the folder with `cp -a
  --reflink=auto` (instant on Btrfs/XFS; a full copy elsewhere, including
  Docker Desktop on macOS), shows bytes copied, then creates `alice-2` with
  the same settings. Cache not copied.
- **Upgrade image**: pull; for each selected computer, stop, recreate with
  the same labels/env/mounts on the new image, start. The list shows
  "update available" when a computer's image ID differs from the latest
  pulled image for its tag.

## Monitoring

- **Stats**: one `ContainerStats` stream per running computer, kept while
  any admin page is open (and a few minutes after). CPU % (of the
  computer's limit) and memory (usage minus page cache, against its limit),
  ring buffer of 15 minutes at 5 s resolution in memory. Pushed to the UI
  over server-sent events (`/api/events`), which also carry status changes
  from Docker's event stream.
- **Processes**: `ContainerTop` with `ps` args
  `-eo pid,user,pcpu,rss,etime,args`, polled every 3 s while the Processes
  tab is open. Sorted by CPU. A kill action is out of scope for v1.
- **Logs**: `ContainerLogs` tail 500 + follow while the tab is open.
- **Screen thumbnails**: `computer screenshot` via `ContainerExec` as user
  `workspace`, scaled down to 480 px wide in Go, cached, refreshed every
  10 s for running computers while the list is visible. Served at
  `/api/computers/<name>/screen.png`.

## Login

- Admin username `admin` (configurable, `MANAGER_USER`) and password from
  `MANAGER_PASSWORD`, or generated on first start and printed once in the
  logs, as the computer image does today. Stored as an argon2id hash.
- Login form with `autocomplete="username"` / `"current-password"`.
  Session: signed cookie, 30 days, sliding. Logout clears it.
- Throttling: after 5 failed logins from one IP, 1 attempt per 10 s for that
  IP.
- State-changing API calls also require `Origin` to match (CSRF defence on
  top of `SameSite=Strict`).

## UI

Preact + Vite + TypeScript, `preact-iso` for routing, plain CSS (no
framework). Mockups in `docs/manager/mockups/`:

1. **Login** (`login.png`): name of the app, username, password, Sign in.
2. **Computers** (`computers.png`): the home page. A wall of live screens,
   one tile per computer: thumbnail, name, status, CPU and memory bars,
   Open (running) or Start (stopped), and a menu with Restart, Stop, Clone,
   Remove. Header: host totals (running count, memory in use of the
   machine's total) and New computer.
3. **Computer** (`computer.png`): bigger live screen with Open desktop;
   facts (status, uptime, image, folder, agent ports); tabs Activity
   (CPU/memory charts), Processes, Logs, Settings.
4. **New computer** (`new.png`): name (with live `/<name>/` preview and
   validation), memory, CPUs, start size, Flatpak, sudo, agent ports.
5. **Clone** and **Remove** dialogs (`clone.png`, `remove.png`).

The UI must work on a phone (list becomes one column; computer page
stacks).

## Repository layout

```
manager/
  cmd/manager/main.go
  internal/{auth,docker,computers,proxy,stats,jobs,web}/
  web/            Preact app (Vite), built into internal/web/dist, embedded
  Dockerfile      multi-stage: node build → go build → distroless/static
  compose.yaml    the manager, with the socket and data folder mounted
```

The computer image changes stay in the existing tree (`WORKSPACE_AUTH`,
`WORKSPACE_PROXY_TOKEN` in `start-caddy`/Caddyfile). CI publishes the
second image from the same workflow.

## Testing

- Go unit tests: name validation, reserved names, label ↔ settings
  round-trip, auth (hashing, sessions, throttling, CSRF/Origin), proxy path
  rewriting and the `?path=` redirect.
- Integration tests in `tests/` (pytest + Playwright, as today), against a
  real manager container with the socket mounted:
  - login form, wrong password, logout; every route but login is 401/redirect without a session;
  - create → desktop opens at `/<name>/` and renders; two computers open
    side by side each show their own desktop;
  - stop/start/restart; remove keeps the folder; remove with "delete
    files" removes it;
  - clone copies a file from the source folder;
  - a computer can't reach another computer's 8080/5901/9222, nor the
    manager without a session;
  - a computer that replaces its own web server (kill Xkasmvnc, serve a
    page on 6080) can't get its page served at `/<name>/`;
  - WebSocket with a foreign Origin is refused;
  - stats and processes endpoints return data for a running computer;
  - upgrade recreates a computer and keeps its folder.

## Phases

1. Computer image: `WORKSPACE_AUTH=manager` + token. Manager skeleton:
   login, list, create, start/stop/restart/remove, proxy. Tests.
2. Monitoring: stats/events stream, thumbnails, processes, logs; computer
   page.
3. Clone, settings editing (recreate), image upgrade, jobs UI.
4. HTTPS via CertMagic, agent ports, phone layout polish, docs.

## Left to the builder

- Exact Go package boundaries, API route shapes, error formats.
- Chart implementation (hand-rolled SVG is fine; no chart library needed).
- How the Docker event stream and stats streams are multiplexed.
- Visual polish beyond the mockups.
