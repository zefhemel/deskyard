# Workspace sandbox image — design

Date: 2026-10-21
Status: draft, awaiting approval

## Intent

A Docker image that acts as a small, persistent Linux computer shared by a
human and an AI agent. The human reaches it through a web browser (a full,
lightweight desktop) mostly to *maintain* the environment — install tools,
configure things — and to look over the agent's shoulder, e.g. to log into
a website in the shared browser on the agent's behalf. Conversation with the
agent happens elsewhere.

Agents use it in two ways:

1. **From outside**: run commands (`docker exec`), drive the browser (Chrome
   DevTools Protocol), or do "computer use" (screenshot / click / type) on the
   same screen the human sees.
2. **From inside**: the human installs e.g. Claude Code or Codex in the
   sandbox and runs it there.

The key property: anything installed by either party survives container
restarts **and** upgrades of the base image.

## Goals

- Browser-accessible XFCE desktop; human and agent share one display, one
  Chromium, one Chromium profile (cookies/logins shared).
- Home folder `/workspace` is a volume. All user-installed software and
  configuration lives there and survives image upgrades.
- Software installation into `/workspace` via mise, npm (global prefix in
  home), uv tools, and Flatpak (`--user`) for GUI apps.
- Lightweight: target 2–3 instances on an 8 GB host, each capped at ~2 GB.
- Safe to expose the web desktop to the internet behind a password with
  HTTPS.
- Multi-arch: linux/arm64 and linux/amd64 from one Dockerfile.
- An automated test suite that builds the image, runs real containers, and
  verifies each capability end to end, so the builder (human or AI) can
  self-evaluate every change.

## Non-goals (for now)

- Nix, Homebrew.
- Google Chrome specifically — Chromium is fine (Chrome-like capability is
  what matters).
- Audio, GPU acceleration, Wayland.
- Multi-user accounts, SSO/OAuth login, per-person access control.
- A conversational UI for talking to the agent.
- CI pipelines (may follow; tests must be runnable locally first).
- An MCP server or HTTP API for agents (the CLI helper below is the
  interface; an API can wrap it later).

## Anti-goals (would count as failure even if it "works")

- Installs that silently vanish or break after pulling a new image.
- Any control surface (VNC, CDP, shell) reachable from the internet without
  authentication.
- A footprint that makes 2–3 instances on 8 GB impractical.
- Relying on poorly maintained or exotic components.

## Decisions made with the user

| Topic | Decision |
|---|---|
| Desktop stack | Hand-built: Debian 13 (trixie) slim + KasmVNC + XFCE. Started on TigerVNC + noVNC/websockify; KasmVNC replaced them for its web client (seamless clipboard, resize to fit, WebP; ~2x the frame rate of noVNC on a 4 Mbit/s link) at the same footprint. linuxserver/webtop and Selkies were considered and rejected for footprint; Selkies remains an upgrade path. |
| Browser | Chromium from Debian (same on arm64 and amd64). |
| Home folder | `/workspace` (`HOME=/workspace`), a volume. User name: `workspace`. |
| Package managers | mise for runtimes and CLI tools; npm global prefix and uv tools in home; Flatpak `--user` for GUI apps. No Nix, no Homebrew. |
| Flatpak | Supported and **on by default** in the shipped compose/run setup, with a switch to turn it off. |
| Remote access | May be exposed directly on the internet. Caddy in front with password auth; automatic HTTPS when given a domain. |
| Trust model | A convenient **shared computer**, not a security boundary against the agent. The agent is trusted roughly like the human (passwordless sudo, Flatpak relaxations on, CDP/VNC unauthenticated on host-localhost). Only the internet-facing door is hardened. |
| Multiple instances | Each instance gets its own configurable host ports and does its own password auth. HTTPS for several instances on one host comes from the user's own proxy/Tailscale in front. Built-in Let's Encrypt is for the single-instance-per-host case. |
| Outside agents | Run on the same host as Docker for now (`docker exec`, CDP/VNC on host-localhost). Remote agent access is a later addition. |

## Architecture

One container, one image. A process supervisor runs:

| Process | Runs as | Purpose |
|---|---|---|
| `Xkasmvnc :1` (KasmVNC) | workspace | X server, VNC server and web client in one process. Starts at 1280x800 (env-configurable); the web client resizes it to fit its window. Web client + WebSocket on 127.0.0.1:6080. |
| XFCE session (`startxfce4` under `dbus-launch`) | workspace | Desktop, panel, file manager, terminal. Provides the session D-Bus. |
| Chromium | workspace | Started with the session, profile in `/workspace/.config/chromium`, remote debugging enabled. Restarted if it exits. |
| websocat | workspace | KasmVNC speaks VNC only over WebSocket; websocat bridges it to plain VNC on 5901. |
| CDP forwarder (socat) | workspace | Chromium only binds its debugging port to 127.0.0.1; a forwarder exposes it on a container port for the host. |
| Caddy | workspace | The only service meant for public exposure. Password auth, optional automatic HTTPS, reverse-proxies to KasmVNC. |

### Ports

| Port | Service | Exposure |
|---|---|---|
| 8080 (HTTP) / 443+80 when a domain is set | Caddy → KasmVNC | Public-capable, always password-protected. |
| 5901 | raw VNC | Host-localhost only (`127.0.0.1:` publish in compose). No internet exposure. |
| 9222 (container) | Chromium CDP via forwarder | Host-localhost only. |

### Authentication & TLS

- `WORKSPACE_PASSWORD` env var. If unset, the container generates a strong
  random password on first start, stores it (hashed) in a location that
  persists across restarts, and prints it once to the log.
- Caddy uses HTTP basic auth (bcrypt hash). Browsers resend it on the
  KasmVNC WebSocket, so one login covers the session.
- `WORKSPACE_DOMAIN` env var: if set, Caddy obtains a Let's Encrypt
  certificate automatically and serves HTTPS on 443 (80 for ACME/redirect).
  If unset, plain HTTP on 8080 — intended for localhost or behind the user's
  own proxy/Tailscale.
- VNC's own password mechanism is not relied on for security.

### Persistence model

- `/workspace` volume holds everything user-owned: dotfiles, mise installs
  (`~/.local/share/mise`), `~/.local/bin`, npm global prefix
  (`~/.npm-global`), uv tools, Flatpak (`~/.local/share/flatpak`), Chromium
  profile, XFCE settings.
- The image provides tooling binaries (mise, uv, flatpak, chromium, etc.)
  and system config; these update with the image.
- On first start with an empty `/workspace`, seed it from a skeleton
  (shell rc, sensible XFCE defaults). Never overwrite existing user files on
  later starts.
- Shell environment (login and interactive, and for `docker exec`): PATH
  includes `~/.local/bin`, `~/.npm-global/bin`, mise shims; mise activated;
  `XDG_DATA_DIRS` includes Flatpak export dirs so Flatpak apps appear in the
  XFCE menu.
- UID/GID of `workspace` configurable (e.g. `PUID`/`PGID`) so bind-mounted
  host folders have sane ownership.
- Things installed with `apt`/`sudo` into the image filesystem do **not**
  persist; documented as such.
- **System state volume** (Claude's call): a second, small volume
  (`/var/lib/workspace-system`, root-owned) holds the generated password hash
  and Caddy's certificates/ACME account, so recreating containers doesn't
  hit Let's Encrypt rate limits and wiping `/workspace` doesn't reset the
  password. Not a security measure (the agent has sudo), just separation.
- **What "upgrade" means** (Claude's call): rebuilds and updates within
  Debian 13 must preserve everything. A future Debian major-version bump is
  expected to keep working for mise/uv/npm installs (they bring their own
  runtimes) but is not guaranteed; the README documents `mise install` /
  reinstall as the recovery path. Rolling back to an *older* image is not
  supported (Chromium refuses profiles from newer versions). Test 6 upgrades
  forward only.

### Flatpak

- `flatpak` installed in the image; Flathub configured as a `--user` remote.
- Running apps needs bubblewrap to create namespaces, which Docker's defaults
  block. The shipped compose file (and documented `docker run` command)
  applies:
  - `seccomp=<shipped profile>` — Docker's default profile plus the
    namespace syscalls bubblewrap needs (prefer this over `unconfined`;
    fall back to `unconfined` only if a custom profile proves unworkable,
    and document why),
  - `apparmor=unconfined`,
  - `systempaths=unconfined`.
- Switch to disable (e.g. a compose profile/override or env-driven script)
  that runs with Docker defaults. In that mode, launching a Flatpak app
  prints a clear explanation instead of bubblewrap's raw error.
- Host caveat to verify and document: Ubuntu 24.04+ hosts may need
  `kernel.apparmor_restrict_unprivileged_userns=0`. OrbStack/Docker Desktop
  are expected to work as is.
- Chromium's own sandbox also uses namespaces: with Flatpak mode on it
  should run sandboxed; with it off, Chromium needs `--no-sandbox`. The
  launcher picks automatically.

### Agent interfaces

- **Commands**: `docker exec -u workspace <container> bash -lc '…'` gets the
  full user environment.
- **Browser**: CDP on host `127.0.0.1:<port>`; works with Playwright
  `connect_over_cdp` and drives the *visible* Chromium on the shared screen.
- **Computer use**: a `computer` CLI in the image (via `docker exec`, or
  from an agent running inside), wrapping xdotool and a screenshot tool on
  `DISPLAY=:1`. Actions mirror the common computer-use tool set: screenshot
  (PNG to stdout or file), mouse move, left/right/middle/double click, drag,
  type text, key combo, scroll, cursor position, screen size.
- **VNC**: raw VNC on host `127.0.0.1:5901` for agents/tools that speak it.
- **Inside agents**: Claude Code / Codex install into `/workspace` and run
  with `DISPLAY=:1` set, so they can use the same tools.
- Human and agent share one mouse/keyboard; no arbitration (documented).

### Agent skills

Skills that teach coding agents about this environment, in the
cross-agent "Agent Skills" format (a directory per skill with a `SKILL.md`
carrying `name`/`description` frontmatter), so they work in at least
Claude Code and Codex (and Gemini CLI etc. where cheap).

**Inside-the-sandbox skills** — shipped in the image (e.g.
`/opt/workspace/skills/`), and on every start symlinked into each agent's
user skill directory under `/workspace` (`~/.claude/skills/`,
`~/.codex/skills/`, `~/.agents/skills/`, `~/.gemini/skills/` — builder
verifies the current paths per agent). Because they're links, they update
with the image. If the user replaces a link with a real directory, it is
left alone.

1. **This computer** — what the machine is; a human shares the screen; how
   to install software so it persists (mise, npm global, uv tool, Flatpak
   `--user`; never apt for anything that should last); where things live;
   what not to touch (auth, Caddy, system state).
2. **Browser** — drive the *already-running* shared Chromium via CDP
   (Playwright `connect_over_cdp`), reuse its existing context and logins,
   don't launch a separate browser; when a site needs a login, ask the
   human to do it in the desktop and wait.
3. **Computer use** — the `computer` CLI: screenshot first, act by
   coordinates on the fixed screen, verify with another screenshot; when
   to prefer it over CDP (native/Flatpak apps, browser UI outside the page).

**Outside-the-sandbox skill(s)** — for agents on the Docker host driving a
sandbox: in the repo (not the image), with a simple way to install them
into the host agent's skill directory. Covers finding the right container,
`docker exec -u workspace … bash -lc`, running `computer` via `docker exec`,
connecting to CDP/VNC on the instance's localhost ports, and the same
browser/login etiquette.

Tests: each in-sandbox agent skill directory resolves to the shipped
skills; skill files have valid frontmatter; the commands shown in the
skills are exercised by the existing CDP/computer-use tests (builder keeps
skill examples and tests in sync).

### Resource limits

- Shipped compose sets `mem_limit: 2g`, `shm_size: 1g`, and a CPU cap.
- Host ports (web, VNC, CDP) are configurable per instance so several
  instances can run side by side; the footprint test starts three this way.
- Footprint target (to be measured, not assumed): idle with Chromium on a
  simple page ≤ ~1 GB per instance; 3 instances under ~6.5 GB total.

## Self-evaluation (test suite)

Runs locally against real containers, from the repo, with one command.
Python + pytest + testcontainers + Playwright (run via uv), plus small
shell checks where simpler. Tests must cover:

1. **Boot**: all supervised processes up; ports listening.
2. **Auth**: web port rejects missing/wrong password, accepts the right one;
   generated password appears in the log when none is set.
3. **Desktop renders**: Playwright logs into the web client, connects, and the canvas
   shows a non-blank desktop (screenshot saved as a test artifact).
4. **CDP**: `/json/version` answers on the host port; Playwright
   `connect_over_cdp` opens a page and it is visible on the shared display
   (verified via a `computer screenshot`).
5. **Computer use round-trip**: open a test page via CDP, click/type via the
   `computer` CLI, assert the DOM changed via CDP.
6. **Persistence across image upgrade**: install a mise tool, an npm global
   package, a uv tool, and write a file; destroy the container, start a new
   one from a *rebuilt/different-tag* image on the same volume; all still
   present and runnable.
7. **Flatpak**: in default mode, install a small Flathub app and confirm its
   window appears on the display; in disabled mode, confirm the clear error.
   (Network-heavy; may be marked slow/opt-in.)
8. **Agent CLIs installable**: Claude Code and Codex install into `/workspace`
   and report `--version` (slow/opt-in).
9. **Footprint**: start 3 instances, load a page in each, record
   `docker stats`; fail over budget.

Screenshots and stats from a run are saved so a human can eyeball them.

### End-to-end acceptance run (agent-driven)

After the automated suite passes, Claude acts as an agent *inside* a
running sandbox, using only the shipped skills and tools, and:

1. Installs the **SilverBullet Desktop edge build** from
   <https://silverbullet.md/desktop/download>. The page offers an edge
   Flatpak (`flatpak install --user
   https://release.silverbullet.md/flatpak/silverbullet-edge.flatpakref`)
   and edge AppImages for x86_64 and arm64. Preferred route: the edge
   Flatpak (exercises Flatpak support); the AppImage is the fallback.
2. Launches it on the shared display and confirms its window appears.
3. Explores the app via the `computer` CLI (screenshots + clicks/typing):
   creates a new space, opens the index page, makes a few edits, and
   confirms they render/save.
4. Restarts the container (and ideally recreates it on a rebuilt image),
   confirms the app is still installed and the space with its edits still
   exists.

Findings (what was hard, what the skills failed to explain) feed back into
the skills. Screenshots of each step are saved and shown to the user. A
scripted, repeatable slice (install edge Flatpak, launch, window appears)
joins the slow/opt-in test set.

## Left to the builder

- Process supervisor choice (supervisord vs s6-overlay etc.).
- Exact repo layout, script names, compose structure, how the Flatpak
  on/off switch is expressed.
- Which screenshot tool, which small Flatpak app to test with.
- Default CPU cap, XFCE theme/panel defaults, skeleton contents.
- Passwordless sudo for `workspace`: yes (follows from the trust model),
  with the non-persistence caveat documented.
- Image naming/tagging.
