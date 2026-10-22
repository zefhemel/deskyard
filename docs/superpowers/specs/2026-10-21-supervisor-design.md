---
displayName: Supervisor design
tags: spec
status: draft
date: "2026-10-21"
---
The supervisor is one Go binary that creates, runs and shares many container-computers ("computers") through the container engine (Docker now, Podman later). It has accounts, and each computer has a list of accounts that may use it. Agents elsewhere use a computer through the same `computer` command that's inside it, with a token an admin hands out. The repository splits into the supervisor and the computer images. Mockups are in the UI section; their HTML sources are in the folder next to this page.

# Intent

Today one `docker compose up` gives one container-computer: a persistent Linux desktop shared by a human and an AI agent. Running several means juggling project names, ports, home folders and passwords by hand, and sharing one with another person means sharing its password.

With this design the supervisor becomes **the only way to run computers** (decided). The standalone `docker compose` setup, with its password, basic auth, own HTTPS and host ports for agents, goes away; running a single computer means running the supervisor with one computer. That leaves one auth model, one network setup and one way in for agents, instead of two of each.

The supervisor turns this into something like a small VMware for these desktops: one web app where you sign in with your own account and create, open, start, stop, restart, clone, upgrade and remove **computers**, and see what each one is doing (CPU, memory, processes, logs). Computers are **shared**: everyone with access to a computer opens the same desktop, at the same time if they like, the way two people look at one screen. Each computer is reached in the browser at `https://host/<name>/`.

Some users are agents running elsewhere (on a laptop, in CI, in another sandbox). An admin gives such an agent a token for one computer; with it, the `computer` command gets a shell, the shared browser and the desktop of that computer over HTTPS.

It ships as one binary, run directly on a machine with Docker or in a small Alpine container. The audience is a person or a small team running a handful to a few dozen computers on a laptop, a home server or a VPS.

# Vocabulary

- **Computer**: one container-computer: a container, its workspace folder and its cache volume. Has a name, e.g. `alice`.
- **Supervisor**: this app: one Go binary with its web UI built in. (Not to be confused with `supervisord`, which runs the services inside every computer.)
- **Account**: a person who signs in to the supervisor. Either an **admin** or a regular account.
- **Access**: the accounts allowed to use a computer, set per computer by admins.
- **Viewer**: one open desktop connection to a computer (a browser tab). One account can be several viewers.
- **Computer image**: an OCI image that follows the image contract (see Images). The repository starts with one, `debian-xfce`, today's image.
- **Engine**: the container engine the supervisor drives: Docker now, Podman later.
- **Workspace folder**: the computer's `/workspace`, its user's home: a folder on the host under the supervisor's data folder, e.g. `~/computers/alice`.
- **`user`**: the single Linux user inside every computer. Everyone who opens the computer works as `user`.
- **Agent token**: a secret an admin creates for one computer, so an agent elsewhere can use that computer through the agent API.
- **`computer` command**: one Go binary. Inside a computer it controls that computer; given a **remote** (a computer's address and an agent token) it does the same over HTTPS.

# Goals

- **One binary.** Go backend with the Preact + Tailwind frontend compiled in. Runs directly on Linux and macOS (amd64, arm64), or as the image `ghcr.io/zefhemel/container-computer-supervisor` (Alpine-based, multi-arch).
- **Accounts.** Username and password, stored hashed in a JSON file. Admins create accounts and set passwords; there's no reset flow, email or SSO. An account is an admin or not.
- **Access per computer.** Admins choose which accounts may use each computer. Admins can use every computer.
- **Shared desktops.** Several viewers on one computer at once, all on the same desktop. While there's one viewer the desktop follows its browser window; with more it keeps its size and each browser scales it.
- A real login page (a form, so password managers fill it) and a session cookie. Computers have no password of their own under the supervisor.
- A wall of all computers you can use, with a live screen thumbnail, status, CPU, memory and who's viewing; one click to open a desktop.
- Lifecycle: create, start, stop, restart, remove, clone.
- Per computer: live CPU and memory (current and last 15 minutes), processes, container logs, settings, access.
- **Several computer images.** Pick one when creating a computer; see when a newer version of a computer's image is available; upgrade computers to it (security updates, new features) keeping their folder and settings, or switch a computer to another image.
- Each computer's desktop at `/<name>/` on the supervisor's address (decided: path prefixes, not subdomains).
- Workspace folders are plain folders on the host (decided), so they can be browsed and backed up with host tools.
- Works on a laptop out of the box (`http://localhost:8080`), and on a server with automatic HTTPS when given a domain (decided).
- **Agents from elsewhere.** Admins create agent tokens per computer (named, optional expiry, revocable). With one, the `computer` command gives an agent a shell (single commands and interactive), file upload and download, the shared browser (CDP, plus a few direct commands) and every desktop action, over HTTPS and WebSockets only. The command describes itself well enough for an agent to learn it from `--help`, `computer guide` and `computer describe --json`.
- Prepared for Podman: everything goes through an engine interface with one implementation (Docker) for now.

# Non-goals (for now)

- Podman support itself (the design keeps the door open; see Engine).
- Running computers without the supervisor (decided: the standalone `docker compose` mode is removed).
- An MCP server mode for the `computer` command (decided: never; the self-describing CLI is the interface).
- Per-tool permissions on tokens: a shell can do everything the other tools can, so a token is all or nothing for its computer.
- Self-service: regular accounts can't create, clone, remove or change computers, or manage accounts.
- Password resets by email, SSO/OAuth, two-factor login, roles beyond admin/regular.
- Telling viewers apart on a shared desktop (one mouse, one keyboard; the supervisor knows who is connected, not who clicked).
- Managing more than one engine host.
- Snapshots/backups beyond clone.
- Publishing arbitrary ports from computers (an agent's dev server) on the supervisor's address. See "Path prefixes and the shared origin".
- Managing containers the supervisor didn't create.

# Anti-goals (failure even if it works)

- Any route of the supervisor reachable without signing in, other than the login page and its static assets.
- An account reaching a computer it has no access to, through the UI, the API or the desktop WebSocket.
- An agent token reaching anything but its own computer's shell, files, browser and desktop: no other computer, no lifecycle, settings or accounts, and nothing as root.
- A computer (i.e. an agent inside it) able to reach another computer's desktop, VNC or CDP, or the engine socket.
- Losing a workspace folder through a misclick: removing a computer keeps its folder unless the admin explicitly ticks "also delete files".
- Stored state that can drift from what the engine says exists.

# Constraints and facts this design rests on

- **The engine socket is root on the host** (rootful Docker and rootful Podman). Whoever can use it owns the machine, so the supervisor is the most privileged thing on it. It never runs agent-provided code, and every request needs a signed-in account.
- **Computers in Flatpak mode are dangerous as root** (`systempaths=unconfined`: container root can write host `/proc/sys`). The existing rule stays: nothing agent- or internet-reachable in a computer runs as root; the supervisor starts computers with the same security options compose uses today.
- **Bind-mount paths given to the engine are host paths.** When the supervisor itself runs in a container, its data folder must be mounted at the same path (see Storage).
- **KasmVNC already allows shared sessions**: the image starts it with `-AlwaysShared`, so several connections see and drive the same desktop.
- **The KasmVNC web client** loads its assets with relative URLs (works under a prefix), but opens its WebSocket at `/<path setting>` with `path` defaulting to `websockify`, i.e. at the site root. It stores its settings in `localStorage`, per origin; URL query parameters override them.
- **The web client's settings are per origin**, so with path prefixes all computers share one set (quality, clipboard). Acceptable. The resize mode is set by the supervisor (see Shared desktops), not by stored settings.
- **Container labels can't be changed** without recreating the container. Anything that changes often (access) therefore lives in the supervisor's own files, not in labels.

# Architecture

```
browser ──HTTPS──▶ supervisor (one Go binary, on the host or in its own container)
                    ├─ /login, /logout, /api/*, /     UI (Preact + Tailwind, embedded)
                    ├─ /<name>/                       KasmVNC web client, served by the supervisor
                    │                                 (extracted from the computer's image)
                    ├─ /<name>/websockify ──WebSocket──▶ computer <name>:8080 (Caddy ─▶ KasmVNC)
                    ├─ /api/agent/v1/*                agent API (token): exec, shell, files, CDP
                    └─ engine socket ─▶ create/start/stop/stats/top/logs/exec/pull

agent elsewhere ── computer -c alice … ──HTTPS/WebSocket──▶ /api/agent/v1/*
```

- **One Go binary** serves the UI (built assets embedded with `embed`), the JSON API, the login, and the proxy to computers. HTTPS via CertMagic (Caddy's ACME library) when `SUPERVISOR_DOMAIN` is set; otherwise plain HTTP on `SUPERVISOR_LISTEN` (default `127.0.0.1:8080`).
- **The engine is the database for computers.** Every computer is a container labelled `container-computer.managed=true`, `container-computer.name=<name>` and `container-computer.id=<uuid>`; its settings are labels too (see Settings). The supervisor keeps no list of computers of its own.
- **The supervisor's own state** is small and lives in its data folder: accounts and access (`accounts.json`), agent tokens (`tokens.json`), the image catalog (`images.json`), the session signing key and ACME certificates.

## Running it

- **Directly:** download the binary for your OS and run `container-computer-supervisor`. It finds the engine at `SUPERVISOR_ENGINE_HOST`, else `DOCKER_HOST`, else the platform default (`/var/run/docker.sock`, or Docker Desktop's/OrbStack's socket on macOS). The user running it must be able to use that socket (on Linux: the `docker` group, which is root-equivalent; the docs say so). For HTTPS on 80/443 on Linux, the docs show `setcap cap_net_bind_service` or a systemd socket.
- **In a container:** `ghcr.io/zefhemel/container-computer-supervisor`, Alpine-based, the binary plus CA certificates and tzdata. Mount the engine socket and the data folder (at the same path). A `compose.yaml` in `supervisor/` shows it.

## Engine

All supervisor logic talks to an engine interface of its own (`internal/engine`), never to an engine SDK directly. Roughly: containers (create, start, stop, remove, inspect, list by label, exec, top, stats stream, logs), networks, volumes, images (pull, inspect, copy files out), events. One implementation now, `internal/engine/docker`, using the Docker Go SDK with API version negotiation; no shelling out to CLIs.

To keep the door open for Podman:

- Everything the supervisor stores on containers is labels, which both engines support the same way.
- Engine-specific spellings stay in the engine package: security options (Flatpak mode's `systempaths=unconfined`, seccomp profile, devices, capabilities), restart policy, how a computer is reached (see below).
- No Docker-only conveniences in the design: no `host.docker.internal`, no Compose at runtime.
- One engine test suite runs against the interface. Adding Podman means a second implementation passing it. Podman's Docker-compatible API (`podman system service`) may let the Docker implementation work as it is; that's the first thing to try.
- Rootless Podman would change the risk picture for the better (container root isn't host root, which defuses the Flatpak-mode `/proc/sys` issue). Workspace folder ownership then goes through a UID mapping, so ownership is the engine's business, not a hard-coded `PUID:PGID`.
- Known Podman differences to handle then: restarting computers after a reboot (systemd/quadlet instead of a daemon restart policy), networking in rootless mode, security option names.

## Networks and reaching a computer

Each computer gets its own bridge network `cc-<name>`, so computers can't reach each other. The supervisor reaches a computer one of two ways, decided by where it runs:

- **Supervisor in a container:** it connects itself to every `cc-<name>` network and reaches `alice` at `cc-alice:8080`. Computers publish nothing. On startup it reconnects to any network of a managed computer it's missing from.
- **Supervisor on the host:** a host process can't reliably reach container addresses (Docker Desktop, OrbStack, rootless Podman), so each computer publishes its port 8080 on host `127.0.0.1` at a port the supervisor picks, stored as a label.

In both cases the per-computer token (see The computer behind the supervisor) is what lets the supervisor in. On Docker Desktop and OrbStack a computer can reach the host's `127.0.0.1` ports (and so another computer's published port), and without the token it gets 401.

Docker's default address pools only hold about 30 bridge networks, so the supervisor gives each network an explicit /28 from `SUPERVISOR_SUBNET` (default `10.213.0.0/16`, room for 4096 computers).

## The computer behind the supervisor

A computer is only ever run by the supervisor, so the image's front door gets simpler:

- Caddy (still on 8080, still as user `caddy`) requires the header `X-Container-Computer-Token: <token>` on every request, where the token is 32 random bytes the supervisor generates per computer and passes in `WORKSPACE_PROXY_TOKEN`. Without the variable the computer refuses to start. Caddy then serves KasmVNC's WebSocket and the `/_cdp/` route, and nothing else.
- Gone from the image: `WORKSPACE_PASSWORD` and its generated password, basic auth, Caddy's HTTPS and domain support (`compose.domain.yaml`), the Origin check (the supervisor does it), and the compose files.

There's no per-computer password: the supervisor login is the only way in. `sudo` behaves as today (`WORKSPACE_SUDO`, off in Flatpak mode).

**Only one port.** Today a computer publishes 8080 and, for agents on the host, VNC (5901) and CDP (9222) on `127.0.0.1`. Agents now use the agent API instead, so a computer publishes **nothing** when the supervisor runs in a container, and only 8080 on host `127.0.0.1` when it runs directly (see Networks). The listeners that only existed for those host ports (the VNC bridge on 5901 and CDP on 9222, both on all interfaces) are removed from the image; what's left on the network is Caddy on 8080, behind the token. Inside the computer, CDP on `127.0.0.1:9221` stays for agents running there.

## `/<name>/`: our desktop page, the web client in a frame, only the WebSocket proxied

A computer can't be trusted to serve pages on the supervisor's origin: the agent runs as the same user as KasmVNC, so it can replace KasmVNC's web server. Script from such a page would run with a signed-in account's cookie, and an admin's cookie drives the engine socket. So the supervisor **never proxies HTTP content from a computer**:

- The supervisor serves the KasmVNC web client itself, extracted from the computer's **image** (never from the running container): on first use of an image ID it copies `/usr/share/kasmvnc/www` out of a never-started container of that image and caches it per image ID. Client and server versions always match.
- `/<name>/` is the supervisor's own **desktop page** (part of the web UI, see Desktop page): our chrome around the desktop, with the KasmVNC client in an iframe at `/<name>/vnc/`. We own this UI from day one: the client's own control bar is never shown, and anything people need from it moves into our chrome.
- The iframe loads `/<name>/vnc/vnc.html?path=<name>/websockify&autoconnect=1` (plus the resize mode, see Shared desktops), so each computer connects to its own WebSocket even though they share stored settings. Query parameters win over the client's stored settings, so a viewer can't end up with the control bar or another computer's path.
- `/<name>` redirects to `/<name>/`.
- `/<name>/websockify` is the only thing proxied: a WebSocket to the computer's port 8080, with the token header added and the session cookie removed (the computer never sees it). Anything else under `/<name>/vnc/` that isn't a client file is a 404.
- Every request under `/<name>/` checks that the signed-in account has access to that computer (admins always do); otherwise 404, so names of other computers don't leak.
- WebSocket upgrades are rejected (403) unless `Origin` matches the supervisor's own origin.
- A stopped computer's desktop page shows "alice is stopped" with a Start button (for anyone with access) instead of the frame.

## Path prefixes and the shared origin

All computers and the UI share one origin, so any script on it can call the API with the viewer's cookie. Every page on the origin therefore comes from the supervisor (its own UI, and web client files taken from images). What a malicious computer can still do over its WebSocket: send arbitrary screen data to the client (parsed by decoders, no script), and put text on a viewer's clipboard while its desktop is open (as today). Accepted.

- No "open port 3000 of alice at /alice/3000". If publishing agent-controlled web content is wanted later, it gets a separate origin (another port, or `*.apps.example.com`), not a path.
- The session cookie is `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` under HTTPS.

## Names

`^[a-z][a-z0-9-]{0,30}[a-z0-9]$` (DNS label rules, so it also works as a container, network and folder name). Reserved, because they're supervisor routes: `api`, `login`, `logout`, `assets`, `static`, `healthz`, `favicon.ico`, `robots.txt`, and anything starting with `_`. Names can't be changed after creation (rename = clone + remove).

Engine names: container `cc-<name>`, network `cc-<name>`, cache volume `cc-<name>-cache`.

# Accounts and access

- `accounts.json` in the supervisor's state folder, mode 0600, written atomically (temporary file + rename) under a lock:

```json
{
  "accounts": [
    {
      "username": "zef",
      "passwordHash": "$argon2id$v=19$m=65536,t=3,p=2$…",
      "admin": true,
      "sessionEpoch": 3,
      "computers": ["6f1c…", "a03e…"],
      "created": "2026-10-22T09:12:00Z"
    }
  ]
}
```

- **Usernames**: `^[a-z][a-z0-9._-]{0,31}$`, case-insensitive unique.
- **First start**: if there's no `accounts.json`, the supervisor creates an admin from `SUPERVISOR_ADMIN_USER` (default `admin`) and `SUPERVISOR_ADMIN_PASSWORD`, or a generated password printed once in its log, as the computer image does today.
- **Admins** create and delete accounts, set any account's password, and make accounts admin or not. The last admin can't be deleted or demoted. Every account can change its own password.
- **Access** is the `computers` list: computer IDs (the `container-computer.id` label), not names, so a new computer that reuses a removed computer's name doesn't inherit its access. Removing a computer removes its ID from every account; IDs of computers that no longer exist are pruned on startup. A clone gets a new ID and starts with the same access as its source.
- **Sessions**: signed cookie carrying username and `sessionEpoch`; 30 days, sliding. Changing a password, deleting an account or demoting it bumps the epoch, which signs that account out everywhere. Revoking access to a computer also closes that account's open desktop connections to it.

What each kind of account can do:

| | Admin | Regular account |
| --- | --- | --- |
| See and open computers | all | those it has access to |
| Start, stop, restart | all | those it has access to |
| Processes, logs, activity | all | those it has access to |
| Create, clone, remove, change settings, upgrade | yes | no |
| Accounts, access, image catalog | yes | no |
| Change own password | yes | yes |

# Shared desktops

Everyone with access opens the same desktop. KasmVNC already supports this (`-AlwaysShared`); the supervisor adds two things:

- **Who's viewing.** The supervisor knows which account each desktop WebSocket belongs to. Tiles show a viewer count, the computer page lists the names, and the desktop page's chrome shows who's here and a short "anna joined" note when someone joins.
- **Screen size.** One desktop has one size, so viewers with different windows would keep resizing it. The rule: **with one viewer, the desktop follows that browser window (remote resize); when a second viewer joins, it keeps its current size and every viewer's browser scales it to fit; when only one is left, it follows that viewer's window again.** The supervisor counts WebSockets per computer and pushes the count and names to each open desktop page (server-sent events at `/api/computers/<name>/viewers`); the page switches its frame's resize setting between `remote` and `scale` live. Prototype this first in phase 2: if the client can't switch live, the page reloads the frame with `resize=scale` or `resize=remote` (a reconnect of about a second).

Not in scope: per-viewer cursors, input locks ("only one person drives"), or telling who typed what.

# Desktop page

The page at `/<name>/`: a thin bar of our own above the desktop, and the KasmVNC client filling the rest in an iframe with its control bar hidden. The page talks to the client through the client's `postMessage` API, and, since both are on the supervisor's origin, through its settings where that API falls short. Nothing of ours is added to the client's own files.

The bar, left to right:

- **Back** to Computers, the computer's name and status, and who's viewing (avatars; "shared, scaled to fit" while there are several).
- **Clipboard**: shows the desktop's clipboard text as it changes (from the client's `clipboardrx` message) with Copy, and a box to send text to the desktop's clipboard (`clipboardsnd`). Over HTTPS, copy and paste also go straight to the system clipboard (the iframe gets `allow="clipboard-read; clipboard-write"`).
- **Keys**: keys the browser would otherwise take: Ctrl+Alt+Del, Super, Alt+Tab, Ctrl+W, Esc, F11.
- **Screenshot**: downloads a PNG (from `computer screenshot`).
- **Quality**: a few presets (`setvideoquality`).
- **Fullscreen** of the whole page, bar hidden until the pointer reaches the top edge.
- **⋯**: Restart and Stop (for anyone with access), Settings and the computer page.

Connection state (`connection_state`, `disconnectrx`) shows in the bar: Connecting, Connected, Reconnecting, and a Reconnect button after a failure. Keyboard focus stays in the frame unless the bar is being used.

![The desktop page](2026-10-21-supervisor-design/desktop.png)

# Agent access

An agent that runs somewhere else uses a computer through the `computer` command, the same command and subcommands it would use inside the computer, plus a few that only make sense remotely. Everything goes over the supervisor's HTTPS port: plain HTTP for short calls, WebSockets for anything interactive or streaming.

## Agent tokens

- An admin creates a token on a computer's Agents tab: a name ("claude on zef's laptop") and an optional expiry (never, 7, 30 or 90 days, or a date). The token is shown once, with the `computer remote add` line to paste into the agent's machine.
- A token reaches exactly one computer, and only the agent API: shell, files, browser, desktop. Not the UI, not lifecycle, settings, accounts or other computers. A shell can do everything the other tools can, so there are no finer permissions.
- Format `cct_<id>_<secret>` (32 random bytes). `tokens.json` in the state folder keeps the ID, name, computer ID, a SHA-256 of the secret (the secret is random, so a slow hash adds nothing), who created it and when, the expiry and when it was last used. Written atomically like `accounts.json`.
- An expired or revoked token stops working at once, and the supervisor closes its open shells, browser connections and streams. Expired tokens stay listed as "Expired" until deleted. Removing a computer deletes its tokens; a clone gets none.
- Every agent API call is logged by the supervisor with the token's name: the command line for exec, the path for files, connect and disconnect for shells and the browser.

## The agent API

All under `/api/agent/v1/`, authenticated with `Authorization: Bearer <token>` (no cookies, so no CSRF checks; the session cookie is never accepted here). The computer is the token's; a stopped computer answers 409 with "alice is stopped; an admin or someone with access can start it".

| Endpoint | What |
| --- | --- |
| `GET /api/agent/v1` | The computer's name, status, image and version, screen size, API version: what `computer remote add` and `computer status` show |
| `POST /exec` | Run one command: `argv`, optional `stdin`, `cwd`, `env`, `timeout`; returns stdout, stderr (as base64 when not UTF-8) and exit code. Output over 16 MB is cut off and says so |
| `GET /exec` (WebSocket) | The same, streamed: stdin in, stdout and stderr out as they come, exit code at the end. For long-running commands |
| `GET /shell` (WebSocket) | An interactive terminal: a PTY running `bash -l`, with resize messages |
| `GET /files?path=…`, `PUT /files?path=…` | Download and upload one file as a stream (directories as tar with `?tar=1`) |
| `/cdp/…` (HTTP and WebSocket) | The shared Chromium's DevTools protocol, proxied |

How the supervisor carries these out:

- **Exec, shell and files** use the engine's exec as `user` (UID 1000) in `/workspace`, never as root, with the computer's normal environment. Files go through exec too (`cat`, `tar` as `user`), not the engine's copy API, which writes as root and could change anything in the container.
- **Desktop actions** need no endpoint of their own: the remote `computer` runs the in-computer `computer` (screenshot, click, type, …) through exec. Every desktop tool works remotely as soon as it exists inside.
- **CDP** goes to the computer's Caddy: Caddy serves `/_cdp/` (HTTP and WebSocket), checks the per-computer proxy token like everything else, and forwards to Chromium's CDP port inside (`cdp-connect`, which starts Chromium if it isn't running). The supervisor rewrites `webSocketDebuggerUrl` and friends in `/json` responses so they point back through itself.

## The `computer` command

One Go binary (`supervisor/cmd/computer`) replacing today's bash script inside the image, and released for Linux, macOS and Windows for agents elsewhere.

- **Remotes**: `computer remote add alice https://host/alice --token cct_…` checks the token against `GET /api/agent/v1` and saves the remote in `~/.config/container-computer/remotes.json` (mode 0600). `computer remote list`, `remove`, and `default alice`. `-c alice` (or `COMPUTER_REMOTE=alice`) picks one; `COMPUTER_URL` and `COMPUTER_TOKEN` work without a config file. Inside a computer with no remote chosen, it acts locally.
- **Shell**: `computer exec -- cmd args` (exit code passed through; `--json` for stdout, stderr and exit code as one object), `computer shell` (interactive), `computer cp ./local alice:/workspace/x` and back.
- **Browser**: `computer browser connect [--port 9222]` listens on `127.0.0.1` and forwards to the computer's CDP, so Playwright's `connectOverCDP`, Puppeteer or chrome-devtools tools work as if the browser were local. Direct commands for agents without those: `browser tabs`, `open URL`, `text` (readable page text), `snapshot` (accessibility tree with refs), `click REF`, `type REF TEXT`, `screenshot`, `eval JS`, built on CDP in the CLI.
- **Desktop**: every `computer` desktop action (`screenshot`, `zoom`, `click`, `type`, `key`, `scroll`, `drag`, …) with the same arguments and output, remote or local.
- **Self-describing**:
  - `computer --help` and every subcommand's `--help` say what it does, its arguments, an example and what it prints;
  - `computer guide` prints a short manual for agents: how the computer is laid out, the three ways to work (shell, browser, desktop) and when to use which;
  - `computer describe --json` lists every command with its arguments, types and output shape, so an agent can load it as a tool schema;
  - errors say what to do next ("no remote chosen: run `computer remote list`, then use -c NAME"), and exit codes are documented.

# Images

The repository builds computer images under `images/`; it starts with one, `images/debian-xfce` (today's image).

**The image contract** is what the supervisor relies on in any computer image, declared with labels so the supervisor can check it before using an image:

- `container-computer.contract=1` (the supervisor refuses images without it, or with a contract version it doesn't know), plus `org.opencontainers.image.title`, `.version` and `.description` for the UI.
- One user, `user`, UID 1000, home `/workspace`. Everything that should survive an upgrade lives under `/workspace`.
- Port 8080: Caddy in front of KasmVNC, checking `WORKSPACE_PROXY_TOKEN` as above; the web client at `/usr/share/kasmvnc/www`.
- The `computer` command, in particular `computer screenshot` for thumbnails and the desktop actions the agent API runs through exec.
- Caddy's `/_cdp/` route to the shared Chromium's CDP.
- Settings via the `WORKSPACE_*` environment variables (resolution, sudo, Flatpak mode), and a container health check.

**The image catalog** (`images.json`, edited by admins on the Images page) lists the image references computers can be created from, e.g. `ghcr.io/zefhemel/container-computer:latest`. The default catalog has the one image. For each entry the supervisor shows title, version and description from the labels, and how many computers use it.

**Updates.** CI rebuilds each image weekly (Debian security updates) and on changes, tagging `latest` and a dated version (`2026.10.22`). The supervisor checks the catalog's tags against the registry daily and on "Check now" (pull, compare image IDs). A computer whose image ID differs from the newest pulled image for its reference shows "Update available".

**Upgrading** a computer recreates its container on the new image with the same labels, settings, folder and cache; a running computer is stopped and started again (its desktop session and browser tabs are restored, as on any restart). The Images page can upgrade all computers on an image at once. **Switching** a computer to a different image in the catalog is the same operation. What survives is exactly what's under `/workspace`: tools installed with mise, uv and npm, Flatpak apps (installed `--user`), the browser profile, files. Anything installed with `apt` or changed elsewhere in the container is lost; the upgrade dialog says so.

# Storage

- `SUPERVISOR_DATA_DIR` (default `~/computers` when run directly, required in a container) holds:
  - `<name>/`: each computer's workspace folder, owned by UID 1000 (on Linux; irrelevant on Docker Desktop and OrbStack);
  - `.supervisor/`: `accounts.json`, `tokens.json`, `images.json`, `session.key`, `certmagic/`.
- In a container, the data folder must be mounted **at the same path** (`-v /srv/computers:/srv/computers`), so a path means the same thing to the supervisor and the engine. On startup the supervisor checks this with a marker file and a short-lived helper container, and refuses to start with an explanation if it doesn't match. Run directly, this check is skipped.
- Cache: a named volume per computer (`cc-<name>-cache`), as today; never cloned or kept on remove.

# Settings per computer

Chosen at creation by an admin, editable later. Applying a change recreates the container right away (a restart; the UI says so):

| Setting | Default | Notes |
| --- | --- | --- |
| Image | the catalog's first entry | see Images |
| Memory limit | none | the computer may use all the host's memory; presets 1, 2, 4, 8 GB or a custom value |
| CPUs | none | the computer may use all the host's CPUs; presets 1, 2, 4 or a custom value |
| Start size | 1280×800 | `WORKSPACE_RESOLUTION`; the size before any viewer resizes it |
| Flatpak apps | on | off = no special security options, `sudo` possible |
| Admin rights (sudo) | auto | `WORKSPACE_SUDO`; the UI explains the Flatpak trade-off |

Stored as labels `container-computer.setting.<key>=<value>`, plus environment variables on the container (as `compose.yaml` sets them today). `shm_size` 1g and a 30 s stop timeout as in `compose.yaml`. Access isn't a setting (it lives in `accounts.json`) and changing it doesn't restart anything.

# Operations

Long operations (create with image pull, clone, upgrade) run as jobs: the API returns a job ID and progress streams to the UI. One job per computer at a time.

- **Create** (admin): validate name; pull the image if missing and check its contract; make folder, cache volume, network; create the container (restart policy `unless-stopped`); connect or publish (see Networks); start; grant access to the accounts chosen in the dialog.
- **Start / stop / restart** (anyone with access): engine API; stop with a 30 s timeout.
- **Remove** (admin): stop; remove container, network, cache volume; remove the computer's ID from all accounts. The folder is kept unless "also delete files" was ticked (typed-name confirmation).
- **Clone** `alice` → `alice-2` (admin): needs `alice` stopped (the dialog offers "stop, clone, start again"). Copies the folder with `cp -a --reflink=auto` (instant on Btrfs/XFS, a full copy elsewhere) in a helper container of the computer's image, showing bytes copied, then creates `alice-2` with the same settings and access. Cache not copied.
- **Upgrade / switch image** (admin): see Images.

# Monitoring

- **Stats**: one stats stream per running computer while any UI page is open (and a few minutes after). CPU % and memory (usage minus page cache), against the computer's limits when it has them and the host's CPUs and memory when it doesn't, kept 15 minutes at 5 s resolution in memory. Pushed to the UI over server-sent events (`/api/events`), filtered to the computers the account can see, together with status changes from the engine's event stream and viewer counts.
- **Processes**: `top` with `-eo pid,user,pcpu,rss,etime,args`, polled every 3 s while the Processes tab is open, sorted by CPU. No kill action in v1.
- **Logs**: tail 500 lines and follow while the tab is open.
- **Screen thumbnails**: `computer screenshot` via exec as `user`, scaled to 480 px wide in Go, cached, refreshed every 10 s for running computers while a list showing them is open. Served at `/api/computers/<name>/screen.png`, with the same access check.

# Login

- Login form with `autocomplete="username"` / `"current-password"`; sessions as in Accounts; Logout clears the cookie.
- Throttling: after 5 failed logins for one username or from one IP, 1 attempt per 10 s for it.
- State-changing API calls also require a matching `Origin` (CSRF defence on top of `SameSite=Strict`).
- The agent API takes only Bearer tokens; everything else only the session cookie.
- Every API handler checks the account's role and access through one function, `can(account, action, computer)`, so the table in Accounts is enforced in one place.

# UI

Preact + TypeScript + Tailwind CSS, built with Vite into the binary; `preact-iso` for routing. All frontend code is TypeScript in strict mode, including the desktop page; no hand-written JavaScript. The mockups below are plain HTML and CSS, rendered from the sources in the folder next to this page; they show layout and content, and the real UI rebuilds them with Tailwind.

The top bar has the wordmark, **Computers**, and for admins **Images** and **Accounts**; on the right the host's memory in use and the signed-in account (menu: Change password, Sign out).

**Desktop page** at `/<name>/`: see Desktop page above.

**Computers**, the home page: a wall of live screens, one tile per computer the account can use, with thumbnail, name, status, image, CPU and memory, who's viewing, Open (running) or Start (stopped), and a menu (Restart, Stop; for admins also Clone, Settings, Remove). Admins get New computer.

![The computers page, as an admin](2026-10-21-supervisor-design/computers.png)

A regular account sees only its computers, with no New computer and a shorter menu:

![The computers page, as a regular account](2026-10-21-supervisor-design/computers-user.png)

**Computer**: bigger live screen with Open desktop; who's viewing; facts (status, image and version, folder, limits, access, agent tokens); charts; tabs Processes, Logs, and for admins Settings, Access and Agents.

![One computer's page](2026-10-21-supervisor-design/computer.png)

**Agents** tab (admin): the computer's agent tokens with name, who created them, expiry and last use; New token; Revoke.

![A computer's agent tokens](2026-10-21-supervisor-design/agents.png)

**New token**: name and expiry; after creating, the token once, with the line to paste on the agent's machine.

![The New token dialog](2026-10-21-supervisor-design/new-token.png)

![A created token, shown once](2026-10-21-supervisor-design/token.png)

**New computer** (admin): name (with a live `/<name>/` preview and validation), image from the catalog, memory, CPUs, Flatpak, and who gets access.

![The New computer dialog](2026-10-21-supervisor-design/new.png)

**Clone** and **Remove** dialogs (admin).

![The Clone dialog](2026-10-21-supervisor-design/clone.png)

![The Remove dialog](2026-10-21-supervisor-design/remove.png)

**Images** (admin): the catalog, each image with its version, the computers on it, and Upgrade when computers are behind; Add image; Check now.

![The Images page](2026-10-21-supervisor-design/images.png)

**Accounts** (admin): accounts with role, the computers each can use, and last sign-in; New account; per account Edit (password, admin, computers) and Delete.

![The Accounts page](2026-10-21-supervisor-design/accounts.png)

![Editing an account](2026-10-21-supervisor-design/account.png)

**Login**: wordmark, username, password, Sign in.

![The login page](2026-10-21-supervisor-design/login.png)

The UI must work on a phone (the wall becomes one column; the computer page stacks).

# Repository layout

```
supervisor/
  cmd/supervisor/     the supervisor
  cmd/computer/       the computer command (inside computers and for agents elsewhere)
  internal/
    engine/          the engine interface and its test suite
    engine/docker/   the Docker implementation
    accounts/  auth/  tokens/  agentapi/  computers/  images/  proxy/  stats/  jobs/  web/
  web/               Preact + Tailwind app (Vite), built into internal/web/dist, embedded
  tests/             integration tests (pytest + Playwright) against a real engine
  Dockerfile         node build → go build → alpine
  compose.yaml       the supervisor in a container, with socket and data folder
images/
  debian-xfce/       today's image: Dockerfile, rootfs/, seccomp/, tests/
docs/                this SilverBullet space
```

Today's image moves into `images/debian-xfce/`; its compose files go, and its tests start the container directly with a test token (as the supervisor would). The README's getting-started becomes the supervisor's. CI has two workflows: images (a matrix over `images/*`, weekly and on change; each image builds `computer` from `supervisor/`) and the supervisor (`supervisor` and `computer` binaries attached to GitHub releases, plus the supervisor image).

Changes to the computer image for this design: the Go `computer` command replaces the bash script; Caddy's token check and `/_cdp/` route, with basic auth, password, HTTPS and the Origin check removed; the VNC (5901) and host CDP (9222) listeners removed; rename the user `workspace` to `user` (same UID 1000, home stays `/workspace`, so existing folders keep working); the contract labels.

# Testing

- Go unit tests: name and username validation, reserved names, label ↔ settings round-trip, `accounts.json` round-trip and atomic writes, `can()` for every row of the permissions table, sessions and epochs, throttling, Origin checks, proxy paths and the `?path=` redirect, the resize rule (viewer counts → mode).
- The engine test suite, run against Docker in CI (and later Podman).
- Integration tests in `supervisor/tests` (pytest + Playwright, as today), against a real supervisor, both run directly and in its container:
  - login, wrong password, logout; every route but login is 401/redirect without a session;
  - a regular account doesn't see, open or control a computer it has no access to (UI, API, `/<name>/`, WebSocket all refuse); revoking access closes its open desktop;
  - create → desktop opens at `/<name>/` in our page, without KasmVNC's control bar; clipboard text goes both ways through the bar; two computers open side by side each show their own desktop;
  - two viewers on one computer see the same desktop; with two the desktop keeps its size, with one it follows the window;
  - stop/start/restart; remove keeps the folder; remove with "delete files" removes it;
  - clone copies a file from the source folder and copies access;
  - a computer publishes no host ports (or only 8080 on `127.0.0.1` with the supervisor run directly) and listens on nothing but 8080 on the network; it can't reach another computer's 8080, nor the supervisor without a session;
  - a computer that replaces its own web server can't get its page served at `/<name>/`;
  - WebSocket with a foreign Origin is refused;
  - upgrade recreates a computer and keeps its folder; an image without the contract label is refused;
  - an agent token works for its computer only: exec, shell, files, CDP and desktop actions work; another computer's API, the UI API and lifecycle calls refuse it; a revoked or expired token is refused and its open shell is closed;
  - exec runs as `user` (`id -u` is 1000), and an uploaded file is owned by `user`;
  - `computer browser connect` lets Playwright drive the shared browser; `computer -c alice screenshot` returns a PNG of alice's screen.
- The `computer` command: golden tests for `--help`, `guide` and `describe --json`, so changes to its self-description are reviewed.

# Phases

1. Split the repository (`images/debian-xfce`, CI), rename the user to `user`, token-only Caddy, remove standalone mode and the agent ports, contract labels. Supervisor skeleton: engine interface + Docker, first-start admin, login, list, create, start/stop/restart/remove, proxy, the desktop page (frame, back, status, clipboard); run directly and in a container. Tests.
2. Accounts and access (pages, `can()`, epochs), shared desktops (viewers, resize rule), the desktop page's full bar.
3. Monitoring: events stream, stats, thumbnails, processes, logs; the computer page.
4. Agent access: tokens and the Agents tab, the agent API, the Go `computer` command (local first, replacing the bash script; then remotes).
5. Clone, settings editing, image catalog, updates and upgrades, jobs UI.
6. HTTPS via CertMagic, phone layout, docs, release binaries.

# Left to the builder

- Exact Go package boundaries, API route shapes, error formats.
- Charts (hand-rolled SVG is fine).
- How the engine's event stream and stats streams are multiplexed.
- How the desktop page switches the client's resize mode, and which client settings it sets directly rather than through `postMessage` (see Shared desktops and Desktop page).
- The exact wire format of the exec, shell and file WebSockets.
- Visual polish beyond the mockups.
