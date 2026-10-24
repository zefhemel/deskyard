---
displayName: Workspace Sandbox plan
tags: plan
status: done
date: "2026-10-21"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One multi-arch Docker image: a persistent, browser-accessible XFCE computer shared by a human and an AI agent, with Chromium, persistent user installs in `/workspace`, Flatpak, a `computer` CLI, agent skills, and a test suite that proves it all.

**Architecture:** Debian 13 slim image; a root entrypoint (`workspace-init`) prepares the user, volumes and auth, then execs supervisord, which runs session D-Bus, TigerVNC `Xvnc :1`, XFCE, Chromium (CDP), websockify+noVNC, a CDP forwarder, and Caddy (password + optional HTTPS). Tests drive real containers through the docker CLI from pytest + Playwright on the host.

**Tech Stack:** Docker/buildx, Debian trixie, TigerVNC, XFCE4, noVNC/websockify, Chromium, Caddy 2, supervisord, socat, xdotool, maim, mise, uv, Flatpak; Python 3.12 + pytest + Playwright (via uv) for tests; bash for in-image scripts.

**Spec:** [[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]

# Global Constraints

- Base image `debian:trixie-slim`; must build for `linux/arm64` and `linux/amd64` from one Dockerfile.
- User `workspace`, `HOME=/workspace` (volume). UID/GID from `PUID`/`PGID`, default 1000/1000.
- System state volume `/var/lib/workspace-system` (root-owned): auth hash, generated-password marker, Caddy data.
- Display `:1`, default geometry `1280x800` (env `WORKSPACE_RESOLUTION`).
- Container ports: Caddy `8080` (no domain) or `80`/`443` (with `WORKSPACE_DOMAIN`); VNC `5901`; CDP `9222` (forwarder) → Chromium on `127.0.0.1:9221`; noVNC/websockify on `127.0.0.1:6080` only.
- Host publishing in shipped compose: VNC and CDP always on `127.0.0.1`; web on `${WORKSPACE_WEB_BIND:-127.0.0.1}`; all host ports configurable for side-by-side instances.
- Auth: `WORKSPACE_PASSWORD` env (overrides); else generate once, store bcrypt hash in system volume, log once. Basic auth user name: `workspace`.
- Compose sets `mem_limit: 2g`, `shm_size: 1g`, `cpus: 2`.
- Flatpak on by default in compose; off by changing `COMPOSE_FILE` in `.env`.
- Passwordless sudo for `workspace`.
- Trust model: shared computer, not a boundary against the agent.
- Pin every external download/image to an exact version current at build time (no `latest`).
- User request: as each visible milestone first works (desktop renders, Chromium on screen, computer-use round trip, Flatpak app on screen, footprint run, each acceptance step), send the user a screenshot of the running desktop (`SendUserFile`, from `tests/artifacts/`).

# Review Focus

- `docker restart` leaves `/tmp/.X1-lock` and `/tmp/.X11-unix/X1` behind → Xvnc must still start (Task 1 test).
- Recreating a container on the same volume leaves Chromium `Singleton*` files from another hostname → Chromium must still start with CDP (Task 3 test).
- Volume created with one `PUID`, container restarted with another → user can still write to `/workspace` (Task 1 test).
- `docker exec` without a login shell (`docker exec -u workspace c node --version`) must still find mise/npm/uv-installed tools (Task 5 test).
- Passwords containing `$`, spaces and quotes must work end to end (Task 2 test).

# File Structure

```
Dockerfile
.dockerignore
compose.yaml                  # base service, Docker default security
compose.flatpak.yaml          # adds security_opt for Flatpak
.env.example                  # COMPOSE_FILE, ports, password, domain, PUID/PGID
seccomp/flatpak.json          # generated: moby default + namespace/mount syscalls
scripts/gen-seccomp.py        # regenerates seccomp/flatpak.json from pinned moby profile
scripts/install-host-skills.sh
rootfs/                       # COPY'd to / in the image
  usr/local/bin/workspace-init      # entrypoint (root)
  usr/local/bin/start-xvnc
  usr/local/bin/start-session       # XFCE
  usr/local/bin/start-chromium      # sandbox detection + flags
  usr/local/bin/chromium            # wrapper so menu/CLI use the shared profile
  usr/local/bin/start-caddy
  usr/local/bin/computer            # computer-use CLI
  usr/local/bin/flatpak             # wrapper with clear error (see Task 6)
  etc/supervisor/conf.d/workspace.conf
  etc/caddy/Caddyfile
  etc/profile.d/workspace.sh
  etc/workspace/skel/...            # seeded into /workspace on first init
  etc/sudoers.d/workspace
  usr/local/share/applications/chromium.desktop
  opt/workspace/skills/<skill>/SKILL.md
skills/host/workspace-sandbox/SKILL.md
tests/
  pyproject.toml, conftest.py, sandbox.py
  test_boot.py test_web.py test_chromium.py test_computer.py
  test_persistence.py test_flatpak.py test_skills.py test_footprint.py test_agents.py
README.md
```

## Test harness (used by every task)

`tests/sandbox.py` — plain docker CLI via `subprocess` (Claude's call over testcontainers: mirrors the compose flags 1:1 and is easier to debug).

```python
class Sandbox:
    name: str
    def exec(self, cmd: str, user: str = "workspace", login: bool = True, check: bool = True) -> subprocess.CompletedProcess  # bash -lc when login
    def port(self, container_port: int) -> int        # host port via `docker port`
    web_url: str          # http://127.0.0.1:<port(8080)>
    cdp_url: str          # http://127.0.0.1:<port(9222)>
    def logs(self) -> str
    def restart(self) -> None                          # docker restart, then wait_ready()
    def wait_ready(self, timeout: float = 60) -> None  # web 401 + CDP /json/version 200
    def remove(self) -> None

def start_sandbox(image: str, *, home: str, system: str, flatpak: bool = True,
                  env: dict[str, str] | None = None, name: str | None = None) -> Sandbox
```

`start_sandbox` runs `docker run -d` with `--shm-size 1g --memory 2g`, `-v home:/workspace -v system:/var/lib/workspace-system`, `-p 127.0.0.1::8080 -p 127.0.0.1::5901 -p 127.0.0.1::9222`, and — when `flatpak` — `--security-opt seccomp=seccomp/flatpak.json --security-opt apparmor=unconfined --security-opt systempaths=unconfined`. Default `env` includes `WORKSPACE_PASSWORD=test-password`.

`conftest.py` fixtures: `image` (session; builds `agent-computer:test` once unless `AC_IMAGE` set), `volumes` (fresh named volumes, removed after), `sandbox` (started + ready, removed after), `artifacts_dir` (`tests/artifacts/<test name>/`, git-ignored). Markers: `network` (downloads from internet), `slow`. Default run: `uv run pytest -m "not slow"`.

---

## Task 1: Base image boots with Xvnc under supervisord

**Files:**

- Create: `Dockerfile`, `.dockerignore`, `.gitignore`, `rootfs/usr/local/bin/workspace-init`, `rootfs/usr/local/bin/start-xvnc`, `rootfs/etc/supervisor/conf.d/workspace.conf`, `rootfs/etc/sudoers.d/workspace`, `tests/pyproject.toml`, `tests/conftest.py`, `tests/sandbox.py`
- Test: `tests/test_boot.py`

**Interfaces:**

- Produces: harness above; `workspace-init` as `ENTRYPOINT` (runs as root, ends with `exec supervisord -n -c /etc/supervisor/supervisord.conf`); supervisord program names used later: `dbus`, `xvnc`, `session`, `chromium`, `novnc`, `cdp`, `caddy`.
- `Sandbox.wait_ready` checks only what exists so far; extend it in Tasks 2–3.

- [x] **Step 1: Write failing tests** in `tests/test_boot.py`:
  - `test_user_and_home`: `exec("id -un; echo $HOME; echo $DISPLAY")` → `workspace`, `/workspace`, `:1`.
  - `test_xvnc_listening`: `exec("xdpyinfo -display :1 | grep dimensions")` contains `1280x800`; host TCP connect to `port(5901)` reads `RFB 003.` banner.
  - `test_resolution_env`: start with `WORKSPACE_RESOLUTION=1024x768` → dimensions `1024x768`.
  - `test_restart_with_stale_x_lock`: `sandbox.restart()` twice; `xdpyinfo` still succeeds.
  - `test_puid_change_keeps_home_writable`: start with `PUID=1000`, write `/workspace/a`; remove; start on same volumes with `PUID=1001,PGID=1001`; `exec("touch /workspace/a /workspace/b")` succeeds and `id -u` is `1001`.
  - `test_passwordless_sudo`: `exec("sudo -n true")` rc 0.
- [x] **Step 2: Run** `cd tests && uv run pytest test_boot.py -v` → FAIL (no image).
- [x] **Step 3: Implement.** Dockerfile installs `tigervnc-standalone-server x11-utils supervisor sudo ca-certificates curl dbus-x11 procps`; creates user `workspace` with home `/workspace`; `ENV HOME=/workspace DISPLAY=:1 XDG_RUNTIME_DIR=/run/user/1000`; `VOLUME /workspace /var/lib/workspace-system`. `workspace-init`: apply `PUID`/`PGID` (`usermod`/`groupmod`); if `/workspace` top-level owner ≠ PUID, `chown -R`; recreate `/run/user/$PUID` and reset `XDG_RUNTIME_DIR`/`DBUS_SESSION_BUS_ADDRESS` to it (export so supervisord children inherit); remove `/tmp/.X1-lock /tmp/.X11-unix/X1`. `start-xvnc`: `exec Xvnc :1 -geometry "$WORKSPACE_RESOLUTION" -depth 24 -rfbport 5901 -SecurityTypes None -localhost=0 -AlwaysShared -AcceptSetDesktopSize=0`. Supervisord programs run as `workspace` (`user=workspace`, `environment=HOME=...`).
- [x] **Step 4: Run** tests → PASS.
- [x] **Step 5: Commit** `feat: base image with Xvnc, supervisord and test harness`.

## Task 2: XFCE desktop, noVNC and Caddy auth

**Files:**

- Create: `rootfs/usr/local/bin/start-session`, `rootfs/usr/local/bin/start-caddy`, `rootfs/etc/caddy/Caddyfile`
- Modify: `Dockerfile`, `workspace-init`, `workspace.conf`, `tests/sandbox.py` (`wait_ready` checks web 401)
- Test: `tests/test_web.py`

**Interfaces:**

- Produces: web on container `8080`; `GET /` (authed) redirects to `/vnc.html?autoconnect=1&resize=scale&path=websockify`; WebSocket at `/websockify`. Session bus at `unix:path=$XDG_RUNTIME_DIR/bus` (program `dbus`, `dbus-daemon --session --address=... --nofork`), used by XFCE, Chromium and Flatpak.
- Log line on generation, exact: `WORKSPACE PASSWORD (generated): <password>`.

- [x] **Step 1: Write failing tests:**
  - `test_requires_auth`: GET `web_url` no creds → 401; wrong password → 401; `workspace:test-password` → 200/302.
  - `test_desktop_renders`: Playwright (host Chromium, `http_credentials`) opens `web_url`, waits for `canvas` and 3 s, screenshots to artifacts; image has > 50 distinct colours (not blank/black).
  - `test_generated_password`: start with no `WORKSPACE_PASSWORD`; parse log line; that password → 200; restart → same password still 200 and the log line not repeated after restart.
  - `test_special_char_password`: `WORKSPACE_PASSWORD='p@ss w$rd"x'\''y'` → authed 200.
  - `test_domain_mode_config_valid`: `exec("WORKSPACE_DOMAIN=example.test start-caddy --validate-only", user="root")` rc 0 (add `--validate-only` flag: render config and run `caddy validate`).
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement.** Install `xfce4 xfce4-terminal novnc python3-websockify` (no recommends; add `xfce4-goodies` pieces only if needed); Caddy binary `COPY --from=caddy:<pinned 2.x> /usr/bin/caddy`. `workspace-init` computes the hash with `caddy hash-password --plaintext` into `/var/lib/workspace-system/auth.hash` (from env each start, or generated once with `openssl rand -base64 18`/`/dev/urandom`). `start-caddy` exports `WORKSPACE_AUTH_HASH` from that file and `XDG_DATA_HOME=/var/lib/workspace-system/caddy`, then `exec caddy run --config /etc/caddy/Caddyfile`. Caddyfile uses `{$WORKSPACE_SITE}` (`:8080` or the domain) and `basic_auth { workspace {$WORKSPACE_AUTH_HASH} }`, `reverse_proxy 127.0.0.1:6080`. Caddy runs as root (needs 80/443 and system volume; Claude's call). `novnc` program: `websockify --web /usr/share/novnc 127.0.0.1:6080 127.0.0.1:5901`. `start-session`: wait for X socket, `exec startxfce4`.
- [x] **Step 4: Run** → PASS; look at the screenshot artifact.
- [x] **Step 5: Commit** `feat: XFCE desktop behind noVNC and Caddy auth`.

## Task 3: Shared Chromium with CDP

**Files:**

- Create: `rootfs/usr/local/bin/start-chromium`, `rootfs/usr/local/bin/chromium`, `rootfs/usr/local/share/applications/chromium.desktop`
- Modify: `Dockerfile`, `workspace-init`, `workspace.conf`, `tests/sandbox.py` (`wait_ready` also checks CDP)
- Test: `tests/test_chromium.py`

**Interfaces:**

- Produces: CDP at container `9222` (`socat TCP-LISTEN:9222,fork,reuseaddr TCP:127.0.0.1:9221`); profile dir `/workspace/.config/chromium-shared` — **not** `~/.config/chromium`, because Chromium ignores `--remote-debugging-port` on its default profile dir. `chromium` wrapper (first in PATH) and desktop entry launch into this same profile, so a second launch just opens a window in the running instance.
- `start-chromium` flags: `--user-data-dir=... --remote-debugging-port=9221 --no-first-run --no-default-browser-check --password-store=basic`, plus `--no-sandbox` only when `unshare -Ur true` fails. `workspace-init` deletes `Singleton{Lock,Socket,Cookie}` in the profile dir.

- [x] **Step 1: Write failing tests:**
  - `test_cdp_version`: GET `cdp_url + "/json/version"` → JSON with `Browser` starting `Chrome/`.
  - `test_cdp_page_on_shared_display`: Playwright `connect_over_cdp(cdp_url)`, `browser.contexts[0].new_page()`, goto `data:text/html,<title>cdp-probe-123</title>`; `exec("xdotool search --name cdp-probe-123")` rc 0.
  - `test_chromium_restarts_when_closed`: `exec("pkill -f chromium-shared")`; within 20 s `/json/version` answers again.
  - `test_recreate_on_same_volume`: remove container, start new one on same volumes → `wait_ready()` succeeds (Singleton cleanup).
  - `test_sandbox_flag_matches_mode`: in flatpak mode `ps` shows no `--no-sandbox`; in default-security mode it does.
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement** (install `chromium socat xdotool`; supervisord `autorestart=true` for `chromium` and `cdp`).
- [x] **Step 4: Run** → PASS.
- [x] **Step 5: Commit** `feat: shared Chromium with CDP forwarder`.

## Task 4: `computer` CLI

**Files:**

- Create: `rootfs/usr/local/bin/computer`
- Modify: `Dockerfile` (install `maim`)
- Test: `tests/test_computer.py`

**Interfaces (exact CLI; skills document it verbatim):**

```
computer screenshot [FILE|-]          PNG; stdout when omitted or '-'
computer size                         prints "W H"
computer cursor                       prints "X Y"
computer move X Y
computer click X Y [left|right|middle]
computer double-click X Y
computer drag X1 Y1 X2 Y2
computer type TEXT                    (xdotool type --delay 12)
computer key COMBO...                 xdotool key syntax, e.g. ctrl+l Return
computer scroll X Y up|down|left|right [N=3]
```

Usage errors → stderr help, exit 2. Works with `DISPLAY` unset (defaults to `:1`).

- [x] **Step 1: Write failing tests:**
  - `test_screenshot_png`: `computer screenshot` stdout starts with `\x89PNG`, decodes to `size` dimensions.
  - `test_size_and_cursor`: `size` → `1280 800`; `move 100 200` then `cursor` → `100 200`.
  - `test_click_and_type_roundtrip`: via CDP open a page whose `<body>` fills the viewport, has an autofocused `<input id=i>` and sets `document.title='clicked'` on any click; `page.bring_to_front()`; `computer click 640 500`; `computer key ctrl+a`… then `computer type hello`; assert via CDP `title=='clicked'` and input value `hello` (focus input via `page.focus` after click if needed).
  - `test_usage_error`: `computer bogus` rc 2.
- [x] **Step 2–4:** run → FAIL, implement as bash over xdotool/maim, run → PASS.
- [x] **Step 5: Commit** `feat: computer CLI for screenshot/mouse/keyboard`.

## Task 5: Persistent installs (mise, uv, npm) and skeleton

**Files:**

- Create: `rootfs/etc/profile.d/workspace.sh`, `rootfs/etc/workspace/skel/.bashrc`, `rootfs/etc/workspace/skel/.profile`
- Modify: `Dockerfile`, `workspace-init`
- Test: `tests/test_persistence.py`

**Interfaces:**

- Image provides `/usr/local/bin/mise` (pinned release) and `uv`/`uvx` (`COPY --from=ghcr.io/astral-sh/uv:<pinned>`).
- Dockerfile `ENV`: `MISE_DATA_DIR=/workspace/.local/share/mise`, `NPM_CONFIG_PREFIX=/workspace/.npm-global`, `PATH=/workspace/.local/bin:/workspace/.npm-global/bin:/workspace/.local/share/mise/shims:$PATH` (so non-login exec works). `profile.d/workspace.sh` repeats PATH for login shells; skel `.bashrc` runs `eval "$(mise activate bash)"`.
- Seeding: if `/workspace/.workspace-seeded` is absent, `cp -an /etc/workspace/skel/. /workspace/` then create the marker. Never overwrite afterwards.

- [x] **Step 1: Write failing tests** (`@pytest.mark.network`):
  - `test_installs_survive_image_upgrade`: in sandbox A run `mise use -g node@22`, `npm i -g cowsay`, `uv tool install pycowsay`, `mise use -g jq`, `echo hi > ~/note`. Build `agent-computer:test-next` (`FROM agent-computer:test` + `RUN echo next > /etc/workspace/build-id`). Remove A; start B from `test-next` on same volumes; `node --version` starts `v22`, `cowsay hi`, `pycowsay hi`, `jq --version`, `cat ~/note` all succeed.
  - `test_non_login_exec_finds_tools`: after `mise use -g node@22`, `exec("node --version", login=False)` succeeds.
  - `test_skeleton_not_overwritten`: edit `~/.bashrc`, restart and recreate → edit preserved.
- [x] **Step 2–4:** FAIL → implement → PASS.
- [x] **Step 5: Commit** `feat: persistent user installs via mise, uv and npm`.

## Task 6: Flatpak support with on/off switch

- [x] Startup correction (2026-10-24): backport the upstream libglnx `openat2()` `EAGAIN` retry into Debian’s current Flatpak CLI and library, ship corresponding source, and verify fault injection against both the original and patched code. Acceptance evidence: [[Testing/Flatpak startup 2026-10-24]].

**Files:**

- Create: `scripts/gen-seccomp.py`, `seccomp/flatpak.json`, `rootfs/usr/local/bin/flatpak`
- Modify: `Dockerfile`, `workspace-init`, `rootfs/etc/profile.d/workspace.sh`
- Test: `tests/test_flatpak.py`

**Interfaces:**

- `gen-seccomp.py` downloads moby's `profiles/seccomp/default.json` at a pinned tag and moves `clone`, `clone3`, `unshare`, `setns`, `mount`, `umount2`, `pivot_root` into the unconditional allow list. Output committed.
- Flatpak installed in image; `workspace-init` runs (as workspace) `flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo`.
- `XDG_DATA_DIRS=/workspace/.local/share/flatpak/exports/share:/usr/local/share:/usr/share` (ENV + profile.d).
- Wrapper `/usr/local/bin/flatpak` (earlier in PATH than `/usr/bin`; exported desktop files call `/usr/bin/flatpak`, so also `dpkg-divert` the real binary to `/usr/bin/flatpak.real` and install the wrapper at `/usr/bin/flatpak`): for `run`, first probe `bwrap --unshare-user --unshare-pid --ro-bind / / --proc /proc --dev /dev true`; on failure print exactly the message below to stderr and exit 1; otherwise `exec flatpak.real "$@"`.
  > `flatpak: apps can't run here because this container uses Docker's default security settings. Start the sandbox with Flatpak support enabled (see README, "Flatpak").`
- If the custom seccomp profile proves unworkable, fall back to `seccomp=unconfined` and record why in README.

- [x] **Step 1: Write failing tests:**
  - `test_bwrap_works_in_flatpak_mode`: the probe command rc 0.
  - `test_clear_error_without_flatpak_mode`: `start_sandbox(flatpak=False)`; `flatpak run org.example.App` rc 1, stderr equals the message.
  - `test_flathub_remote_configured`: `flatpak remotes --user` lists `flathub`.
  - `test_silverbullet_edge_flatpak` (`slow`, `network`): `flatpak install --user -y --noninteractive https://release.silverbullet.md/flatpak/silverbullet-edge.flatpakref`; launch detached via `setsid flatpak run <app-id> &`; within 60 s `xdotool search --name -i silverbullet` finds a window; screenshot artifact; recreate container → `flatpak list --user` still has it.
- [x] **Step 2–4:** FAIL → implement → PASS. On an Ubuntu 24.04+ host, check whether `kernel.apparmor_restrict_unprivileged_userns=0` is required and record the answer for README (if no such host is available, record it as untested).
- [x] **Step 5: Commit** `feat: Flatpak support with seccomp profile and clear fallback error`.

## Task 7: Agent skills (inside and host)

**Files:**

- Create: `rootfs/opt/workspace/skills/workspace-computer/SKILL.md`, `.../workspace-browser/SKILL.md`, `.../workspace-computer-use/SKILL.md`, `skills/host/workspace-sandbox/SKILL.md`, `scripts/install-host-skills.sh`
- Modify: `workspace-init`
- Test: `tests/test_skills.py`

**Interfaces:**

- Frontmatter: `name` equals the directory name; `description` says when to use it (one or two sentences, starts with "Use when").
- `workspace-init` (as workspace), for each dir in `~/.claude/skills ~/.codex/skills ~/.agents/skills ~/.gemini/skills`: `mkdir -p`; for each shipped skill, create/refresh symlink `<dir>/<skill> -> /opt/workspace/skills/<skill>` unless `<dir>/<skill>` exists and is not a symlink. Verify each agent's current skill path against its docs while implementing; adjust the list if one has moved.
- Content per spec "Agent skills". Every command example must be one the tests run (CDP URL inside: `http://127.0.0.1:9221`; outside: the instance's host port; `computer` usage exactly as Task 4).
- `install-host-skills.sh [--claude] [--codex] [--agents]` symlinks `skills/host/*` into the chosen host dirs (default: all that exist).

- [x] **Step 1: Write failing tests:**
  - `test_skill_links_present`: for each agent dir × skill, `readlink -f` → `/opt/workspace/skills/<skill>`, `SKILL.md` readable.
  - `test_frontmatter_valid` (host-side, parse every `SKILL.md` in `rootfs/opt/workspace/skills` and `skills/host`): has `name` == dirname and non-empty `description`.
  - `test_user_override_kept`: replace `~/.claude/skills/workspace-browser` link with a real dir containing a custom `SKILL.md`; restart; it's still a dir with the custom content.
  - `test_browser_skill_example_runs`: run the Python/Playwright snippet from the browser skill inside the sandbox (after `uv` provides Playwright, `network`) and assert it prints the page title.
- [x] **Step 2–4:** FAIL → implement → PASS.
- [x] **Step 5: Commit** `feat: agent skills for in-sandbox and host agents`.

## Task 8: Compose, README, multi-arch build, footprint and agent CLIs

**Files:**

- Create: `compose.yaml`, `compose.flatpak.yaml`, `.env.example`, `README.md`
- Test: `tests/test_footprint.py`, `tests/test_agents.py`, `tests/test_compose.py`

**Interfaces:**

- `.env.example`: `COMPOSE_FILE=compose.yaml:compose.flatpak.yaml` (Flatpak on; set to `compose.yaml` to turn off), `WORKSPACE_PASSWORD=`, `WORKSPACE_DOMAIN=`, `WORKSPACE_WEB_BIND=127.0.0.1`, `WORKSPACE_WEB_PORT=8080`, `WORKSPACE_VNC_PORT=5901`, `WORKSPACE_CDP_PORT=9222`, `PUID=1000`, `PGID=1000`, `WORKSPACE_RESOLUTION=1280x800`. Domain mode publishes 80/443 instead of the web port (document; single instance per host).
- Multiple instances: one directory/`.env` per instance or `docker compose -p <name>` with different ports; README shows both.
- README sections: Quick start, Access (web, password, HTTPS), Agents (docker exec, CDP, computer, VNC, skills incl. host install), Installing software (what persists, what doesn't — apt), Flatpak (flags, on/off, host caveat), Multiple instances, Upgrading (forward only; `mise install` after a Debian major bump), Testing.

- [x] **Step 1: Write failing tests:**
  - `test_compose_up` : with a temp `.env` (unique project name/ports), `docker compose up -d --wait` (healthcheck in compose: `curl -so /dev/null -w '%{http_code}' localhost:8080` → 401), authed GET 200, `down -v`.
  - `test_three_instances_fit` (`slow`): start 3 sandboxes, open a local `data:` page with some text and images via CDP in each, wait 20 s, `docker stats --no-stream`; save stats to artifacts; each ≤ 1.2 GiB, sum ≤ 3.6 GiB.
  - `test_claude_code_and_codex_install` (`slow`, `network`): `curl -fsSL https://claude.ai/install.sh | bash` then `claude --version`; `mise use -g node@22 && npm i -g @openai/codex` then `codex --version`; recreate container → both still report versions.
- [x] **Step 2–4:** FAIL → implement → PASS.
- [x] **Step 5:** `docker buildx build --platform linux/amd64,linux/arm64 .` succeeds (amd64 under emulation; no push).
- [x] **Step 6:** Full suite `uv run pytest -m ""` passes; commit `feat: compose setup, README, footprint and agent CLI tests`.

## Task 9: Agent-driven SilverBullet acceptance run (Claude, interactive)

Not a code task; done by Claude acting as an agent inside a fresh compose-started sandbox, using only the in-sandbox skills, `computer`, and `docker exec`.

- [x] Install SilverBullet Desktop **edge** via the edge flatpakref (fallback: edge AppImage for the container's arch from https://silverbullet.md/desktop/download, run with `--appimage-extract-and-run`).
- [x] Launch it on `:1`; via screenshots + `computer` actions, create a new space, open the index page, make several edits, confirm they render/save. Save a screenshot per step to `tests/artifacts/acceptance/`.
- [x] Restart the container, then recreate it on `agent-computer:test-next`; confirm the app and the space with edits survive.
- [x] Fold anything that was hard or unclear back into the skills (commit `docs(skills): lessons from SilverBullet run`); report to the user with screenshots.
