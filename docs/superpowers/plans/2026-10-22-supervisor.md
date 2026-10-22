---
displayName: Supervisor plan
tags: plan
status: active
date: "2026-10-22"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
# Supervisor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the supervisor and the reshaped computer image described in the [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]], phase by phase.

**Architecture:** One Go module in `supervisor/` with two binaries (`supervisor`, `computer`); the web UI (Preact + TypeScript + Tailwind, built with Vite) is embedded in the supervisor. The computer image moves to `images/debian-xfce/` and only runs behind the supervisor (token-checking Caddy on 8080). State: container labels for computers, JSON files for accounts, tokens and the image catalog.

**Tech Stack:** Go 1.27 (Docker SDK `github.com/moby/moby/client`, `golang.org/x/crypto/argon2`, `github.com/coder/websocket`, `github.com/caddyserver/certmagic`), Node 24 + Vite + Preact + `preact-iso` + Tailwind v4 + TypeScript (strict), pytest + Playwright for integration tests, Debian trixie image as today.

**Spec:** `docs/superpowers/specs/2026-10-21-supervisor-design.md`

## Global Constraints

- Nothing agent- or internet-reachable in a computer runs as root; there is no sudo in computers; exec through the engine always runs as `user` (UID 1000).
- Every supervisor route except `/login` and its assets needs a session; the agent API takes only `Authorization: Bearer dyt_…`, everything else only the session cookie.
- The supervisor never proxies HTTP content from a computer: only `/<name>/websockify` and the agent CDP route are proxied; the KasmVNC client is served from files extracted from the computer's **image**.
- Labels: `deskyard.managed=true`, `deskyard.name`, `deskyard.id`, `deskyard.setting.<key>`, `deskyard.port` (host mode); image labels `deskyard.contract=1`, `org.opencontainers.image.title|version|description`.
- Names `^[a-z][a-z0-9-]{0,30}[a-z0-9]$`; reserved `api login logout assets static healthz favicon.ico robots.txt` and `_*`. Usernames `^[a-z][a-z0-9._-]{0,31}$`.
- Engine objects: container `deskyard-<name>`, network `deskyard-<name>` with a /28 from `DESKYARD_SUBNET` (default `10.213.0.0/16`).
- Config: `DESKYARD_LISTEN` (default `127.0.0.1:8080`), `DESKYARD_DOMAIN`, `DESKYARD_DATA_DIR` (default `~/deskyard`), `DESKYARD_ENGINE_HOST`, `DESKYARD_SUBNET`, `DESKYARD_ADMIN_USER` (default `admin`), `DESKYARD_ADMIN_PASSWORD`, `DESKYARD_IMAGE` (default first catalog entry `ghcr.io/zefhemel/deskyard-debian-xfce:latest`).
- State files in `$DESKYARD_DATA_DIR/.deskyard/`, mode 0600, written atomically (temp file + rename) under a lock.
- Computer containers: `shm_size` 1g, 30 s stop timeout, restart `unless-stopped`, security options `seccomp=<flatpak profile>`, `apparmor=unconfined`, `systempaths=unconfined`; no memory or CPU limit unless set.
- All frontend code is TypeScript in strict mode; no hand-written JavaScript.

## Review Focus

- A name that matches an existing folder in the data folder: create reuses it (and its owner must end up UID 1000) instead of failing.
- The supervisor restarting while computers run: it must rediscover computers, networks and published ports from labels alone, and proxies must work without recreating anything.
- A browser tab open on a computer that gets stopped or removed: the desktop page must show "stopped" or a 404 page, not a blank frame or an endless spinner.
- Two computers whose KasmVNC clients share one origin's `localStorage`: opening one must never connect to the other (the frame URL's `path` wins).
- A slow or failing image pull during create: the job must report the error and leave no half-created container, network or label behind.

---

# Phase 1: foundation

### Task 1: Split the repository and reshape the image

**Files:**
- Move: `Dockerfile`, `rootfs/`, `tests/` → `images/debian-xfce/`; `seccomp/flatpak.json` → `supervisor/internal/engine/docker/flatpak-seccomp.json` (`scripts/gen-seccomp.py` writes there)
- Delete: `compose.yaml`, `compose.domain.yaml`, `compose.no-flatpak.yaml`, `.env.example`, `skills/`, `scripts/install-host-skills.sh`, `rootfs/etc/workspace/cache-paths`, `rootfs/etc/xdg/autostart/workspace-terminal.desktop`, `rootfs/usr/local/bin/workspace-autostart-terminal`, `images/debian-xfce/tests/test_compose.py`, `test_cache.py`
- Modify: `images/debian-xfce/Dockerfile`, `rootfs/usr/local/bin/workspace-init`, `start-caddy`, `rootfs/etc/caddy/Caddyfile`, `rootfs/etc/supervisor/conf.d/workspace.conf`, every `rootfs` file naming the user `workspace`, the skills under `rootfs/opt/workspace/skills/`, `images/debian-xfce/tests/sandbox.py` and the tests
- Image changes: user `workspace` → `user` (UID/GID 1000, home `/workspace`; PUID/PGID gone); no `sudo` package or sudoers; no websocat, `vnc` and `cdp-host` programs; Chromium `autostart=false`; no cache volume (`XDG_CACHE_HOME`, `NPM_CONFIG_CACHE` back under `/workspace`); no system volume, password or HTTPS; `VOLUME ["/workspace"]`; labels `deskyard.contract=1` and OCI title/description/version (`ARG VERSION=dev`).
- Caddy: `:8080`, every request needs header `X-Deskyard-Token` equal to `{$WORKSPACE_PROXY_TOKEN}` (else 401); `/_cdp/*` strips the prefix and proxies to `127.0.0.1:9221` with `Host: localhost`; everything else to `127.0.0.1:6080`. `workspace-init` refuses to start (exit 1, message) without `WORKSPACE_PROXY_TOKEN`.

- [x] **Step 1:** `git mv` the files; delete the dropped ones; update `.dockerignore`.
- [x] **Step 2:** Change `tests/sandbox.py`: start with `-e WORKSPACE_PROXY_TOKEN=test-token`, publish only 8080, `exec` default user `user`; `web_url` requests carry the token header; readiness = display up and `GET /` with token → 200 and without → 401; CDP checks go through `/_cdp/json/version`.
- [x] **Step 3:** Adjust tests: `test_web.py` (401 without token, 200 with, WebSocket via token), `test_privileges.py` (no sudo, `id -u user` = 1000, caddy runs as `caddy`, nothing listens on 5901/9222), `test_chromium.py` (Chromium not running after boot; starts on first CDP connection), `test_boot.py` (missing token → container exits with the message).
- [x] **Step 4:** Change the image and rootfs as listed.
- [x] **Step 5:** Run `cd images/debian-xfce/tests && uv run pytest -x -q` — all pass.
- [x] **Step 6:** Commit `image: supervisor-only computer image in images/debian-xfce`.

### Task 2: Go module, config and state files

**Files:** `supervisor/go.mod` (module `github.com/zefhemel/deskyard/supervisor`), `supervisor/cmd/deskyard/main.go`, `supervisor/internal/config/config.go`, `supervisor/internal/store/store.go` (+ `_test.go`)

**Interfaces:**
- Produces: `config.Load() (config.Config, error)` with fields `Listen, Domain, DataDir, StateDir, EngineHost, Subnet, AdminUser, AdminPassword, DefaultImage string; InContainer bool`; `store.ReadJSON(path string, v any) error` (missing file → `fs.ErrNotExist`), `store.WriteJSON(path string, v any) error` (0600, temp + rename), `store.Locked(fn func() error) error`.

- [x] **Step 1:** Tests: `WriteJSON` then `ReadJSON` round-trips; file mode is 0600; a crash between temp write and rename leaves the old file (write temp, don't rename, read old).
- [x] **Step 2:** Implement; `config.Load` defaults per Global Constraints; `InContainer` = `/.dockerenv` exists or `DESKYARD_IN_CONTAINER=1`.
- [x] **Step 3:** `go test ./...` passes; commit.

### Task 3: Engine interface and Docker implementation

**Files:** `supervisor/internal/engine/engine.go`, `supervisor/internal/engine/docker/docker.go`, `supervisor/internal/engine/enginetest/suite.go`, `supervisor/internal/engine/docker/docker_test.go` (build tag `engine`)

**Interfaces:**
- Produces:
  - `type Engine interface { Create(ctx, ContainerSpec) (id string, err error); Start(ctx, id string) error; Stop(ctx, id string, timeout time.Duration) error; Restart(ctx, id string, timeout time.Duration) error; Remove(ctx, id string) error; Inspect(ctx, id string) (Container, error); List(ctx, labels map[string]string) ([]Container, error); Exec(ctx, id string, ExecSpec) (Process, error); Top(ctx, id string, args string) ([][]string, []string, error); Stats(ctx, id string) (<-chan Stats, error); CreateNetwork(ctx, name, subnet string, labels map[string]string) error; RemoveNetwork(ctx, name string) error; ConnectNetwork(ctx, network, container string, aliases []string) error; Networks(ctx, labels map[string]string) ([]Network, error); Pull(ctx, ref string, progress func(PullProgress)) error; ImageInspect(ctx, ref string) (Image, error); CopyFromImage(ctx, ref, path string) (io.ReadCloser /* tar */, error); Events(ctx) (<-chan Event, error); SelfID() string }`
  - `ContainerSpec{Name, Image string; Labels, Env map[string]string; Mounts []Mount; Network string; Aliases []string; PublishLocal *int /* host 127.0.0.1 port for 8080 */; MemoryBytes int64; NanoCPUs int64; ShmBytes int64; SecurityOpt []string; Restart string}`
  - `Container{ID, Name, Image, ImageID string; Labels map[string]string; State string /* running|exited|created|… */; StartedAt time.Time; IP map[string]string /* network → IP */}`
  - `ExecSpec{Cmd []string; User, WorkDir string; Env []string; Tty bool; Stdin bool}`; `Process{Stdin io.WriteCloser; Stdout, Stderr io.Reader; Resize func(w, h uint) error; Wait func() (int, error)}`
  - `Stats{CPUPercent float64; MemBytes, MemLimit uint64; Time time.Time}`
- `enginetest.Run(t, e engine.Engine, image string)` creates a container from a small image (`alpine`), checks labels round-trip, start/stop, exec as user 1000 with stdout and exit code, network create/connect/remove.

- [x] **Step 1:** Write the suite and the docker test calling it (`go test -tags engine ./internal/engine/...`), see it fail.
- [x] **Step 2:** Implement the Docker engine with API negotiation; `SecurityOpt` passes the seccomp profile JSON contents; `SelfID` reads the container ID from `/proc/self/mountinfo` or hostname when in a container.
- [x] **Step 3:** Suite passes against OrbStack; commit.

### Task 4: Accounts, sessions and login

**Files:** `supervisor/internal/accounts/accounts.go`, `supervisor/internal/auth/{password.go,session.go,throttle.go,can.go}` (+ tests)

**Interfaces:**
- Produces: `accounts.Store` with `Load(dir)`, `EnsureAdmin(user, password string) (generated string, err error)`, `Get(username) (Account, bool)`, `List() []Account`, `Put(Account) error`, `Delete(username) error`, `SetPassword(username, pw string) error` (bumps epoch), `Grant(username, computerID string)`, `Revoke(username, computerID string)`, `ForgetComputer(computerID string)`; `Account{Username, PasswordHash string; Admin bool; SessionEpoch int; Computers []string; Created, LastLogin time.Time}`.
- `auth.HashPassword(pw) string` (argon2id m=65536,t=3,p=2, PHC string), `auth.CheckPassword(hash, pw) bool`; `auth.Sessions{Key []byte}` with `Issue(w, username, epoch)`, `Read(r) (username string, epoch int, ok bool)`, cookie `deskyard_session` HttpOnly, SameSite=Strict, Path=/, Secure under HTTPS, 30 days sliding; `auth.Throttle` (5 failures per username or IP → 1 attempt / 10 s); `auth.Can(a Account, action Action, computerID string) bool` with actions `View, Power, Manage, ManageAccounts, CreateToken, RevokeAnyToken`.

- [x] **Step 1:** Tests: hash/verify; `EnsureAdmin` creates `admin` once with a 24-char generated password when none is given; the last admin can't be deleted or demoted; epoch bump invalidates a session; throttle; `Can` table from the spec's permissions table (every row).
- [x] **Step 2:** Implement; commit.

### Task 5: Computers: names, labels, networks, lifecycle

**Files:** `supervisor/internal/computers/{names.go,settings.go,network.go,service.go}` (+ tests)

**Interfaces:**
- Consumes: `engine.Engine`, `accounts.Store`.
- Produces: `computers.ValidName(string) error`; `computers.Settings{Image string; MemoryBytes, NanoCPUs int64}` ↔ labels; `computers.Service` with `List(ctx) ([]Computer, error)`, `Get(ctx, name) (Computer, error)`, `Create(ctx, CreateRequest, progress func(string)) (Computer, error)`, `Start/Stop/Restart/Remove(ctx, name string, deleteFiles bool)`, `Target(ctx, name) (addr string, token string, err error)` (where the proxy connects); `Computer{ID, Name, State, Image, ImageID string; Settings Settings; Folder string; StartedAt time.Time}`; `CreateRequest{Name string; Settings Settings; Access []string}`.
- Network allocation: list `cc-*` networks' subnets, pick the first free /28 in `DESKYARD_SUBNET`.
- Reach: in a container, connect self to `deskyard-<name>` and use `deskyard-<name>:8080` (alias); directly, publish 8080 on `127.0.0.1:<free port>` (label `deskyard.port`) and use that. On startup, reconnect self to every managed computer's network.
- The per-computer token is generated at create, passed as `WORKSPACE_PROXY_TOKEN` and read back from the container's env on inspect (no extra state).
- Same-path check (container mode): write a marker into `DataDir`, run `alpine` with the same path mounted, `test -f` it; refuse to start otherwise.

- [x] **Step 1:** Unit tests: names (valid, reserved, too long), settings ↔ labels round-trip, subnet allocation skips used /28s.
- [x] **Step 2:** Integration test (tag `engine`): create with an existing folder reuses it; create → running; stop; start; remove keeps the folder; remove with deleteFiles deletes it; a failed pull leaves nothing behind.
- [x] **Step 3:** Implement; commit.

### Task 6: HTTP server, desktop proxy and API

**Files:** `supervisor/internal/server/{server.go,api.go,desktop.go,clientfiles.go,login.go}` (+ tests), `supervisor/internal/web/embed.go`

**Interfaces:**
- Consumes: everything above.
- Produces routes: `GET/POST /login`, `POST /logout`, `GET /api/me`, `GET /api/computers`, `POST /api/computers`, `POST /api/computers/{name}/{start|stop|restart}`, `DELETE /api/computers/{name}?files=1`, `GET /api/jobs/{id}` (SSE progress), `/<name>` → `/<name>/`, `/<name>/` (the SPA's desktop page), `/<name>/vnc/*` (client files), `/<name>/websockify` (proxied WebSocket), `/` and `/assets/*` (SPA).
- Client files: on first use of an image ID, `CopyFromImage(image, "/usr/share/kasmvnc/www")` into `StateDir/clients/<imageID>/`.
- Proxy: `httputil.ReverseProxy` to `Target`, adds the token header, removes `Cookie`, rejects foreign `Origin` (403); 404 for computers the account can't see.

- [x] **Step 1:** Tests with a fake engine: unauthenticated `/api/computers` → 401 and `/alice/` → redirect to `/login`; state-changing call with foreign Origin → 403; `/alice/vnc/../x` can't escape; `/<reserved>` isn't a computer.
- [x] **Step 2:** Implement; commit.

### Task 7: Web UI (phase 1 pages)

**Files:** `supervisor/web/` (Vite + Preact + TS + Tailwind): `src/main.tsx`, `src/api.ts`, `src/pages/{Login,Computers,Desktop}.tsx`, `src/components/{TopBar,Tile,NewComputerDialog,RemoveDialog}.tsx`; build to `supervisor/internal/web/dist`

- Login form (`autocomplete` username/current-password); Computers wall (tiles with name, status, Open/Start, menu Restart/Stop/Remove for admins), New computer dialog (name with live `/<name>/` preview and validation, image, memory and CPU limits, default no limit), Remove dialog with typed-name confirmation for deleting files; Desktop page: bar with Back, name, status, Clipboard (send and receive via `clipboardsnd`/`clipboardrx`), iframe `/<name>/vnc/vnc.html?path=<name>/websockify&autoconnect=1&resize=remote&show_control_bar=false`; stopped → "alice is stopped" + Start.

- [ ] **Step 1:** `npm run build` and `npm run typecheck` pass; commit.

### Task 8: Packaging, CI and integration tests

**Files:** `supervisor/Makefile`, `supervisor/Dockerfile` (node build → go build → alpine), `supervisor/compose.yaml`, `.github/workflows/{images.yml,supervisor.yml}`, `supervisor/tests/{conftest.py,test_supervisor.py}` (+ `pyproject.toml`)

- [ ] **Step 1:** Integration tests (supervisor run directly against OrbStack with a temporary data dir and the test image): login and logout, every route but login needs a session; create → desktop page shows the desktop without KasmVNC's control bar; clipboard both ways; two computers side by side; stop/start/restart; remove keeps the folder, "delete files" removes it; reuse of an existing folder; a computer publishes only 8080 on 127.0.0.1 and can't reach another computer's 8080; foreign Origin WebSocket refused; replacing a computer's web server doesn't change what `/<name>/vnc/` serves.
- [ ] **Step 2:** The same tests pass with the supervisor in its container (`DESKYARD_TEST_MODE=container`).
- [ ] **Step 3:** CI: images matrix over `images/*` (weekly cron + on change, tags `latest` and date), supervisor binaries + image; commit.

# Phase 2: accounts, access, viewers

### Task 9: Accounts and access

- API: `GET/POST /api/accounts`, `PATCH/DELETE /api/accounts/{username}`, `PUT /api/computers/{name}/access`, `POST /api/me/password`; revoking access closes that account's open desktop WebSockets for the computer; create grants access to the chosen accounts; remove forgets the computer everywhere; startup prunes unknown IDs.
- UI: Accounts page and Edit dialog, Access tab, account menu (Change password, Sign out), regular accounts see only their computers with Restart/Stop.
- [x] Tests (Go + integration): regular account can't see, open, power or proxy to a computer without access; revoking closes the desktop; password change signs out everywhere.

### Task 10: Who's viewing and the full desktop bar

- The proxy tracks connections per computer (`account`, opened at); `GET /api/computers/{name}/viewers` (SSE) streams names; tiles and the desktop page show avatars and an "anna joined" note.
- Bar: Keys menu (Ctrl+Alt+Del, Super, Alt+Tab, Ctrl+W, Esc, F11 via the agent-free path: exec `computer key …` as `user`), Screenshot (`GET /api/computers/{name}/screen.png?full=1`), Quality presets (`setvideoquality`), ⋯ (Restart, Stop, computer page).
- [ ] Tests: two viewers see each other; screenshot returns a PNG.

# Phase 3: monitoring and the computer page

### Task 11: Events, stats, thumbnails, processes

- `GET /api/events` (SSE): computer status changes (engine events), stats every 5 s, viewer counts; filtered by access.
- Stats ring buffer 15 min × 5 s per running computer while any page is open (+3 min); CPU % and memory against limits or host totals.
- Thumbnails: `computer screenshot` via exec as `user`, scaled to 480 px wide, cached 10 s; `GET /api/computers/{name}/screen.png`.
- Processes: `GET /api/computers/{name}/processes` (top `-eo pid,user,pcpu,rss,etime,args`), polled by the tab every 3 s.
- Computer page: big screen, viewers, CPU and memory charts (SVG), facts, tabs Processes, Agents, Settings, Access.
- [x] Tests: stats arrive for a running computer; processes include `Xkasmvnc`; a thumbnail is a PNG 480 px wide.

# Phase 4: agent access

### Task 12: Tokens and the Agents tab

- `tokens.json`: `{id, name, computerID, hash, createdBy, created, expires, lastUsed}`; format `dyt_<id>_<secret>` (id 8 chars, secret 32 random bytes base64url); SHA-256 of the secret.
- API: `GET/POST /api/computers/{name}/tokens`, `DELETE /api/computers/{name}/tokens/{id}`; anyone with access creates and revokes own, admins any; expiry and revocation close open connections; tokens of a creator who loses access or is deleted are revoked.
- UI: Agents tab, New token dialog (name, expiry never/7/30/90 days/date), Token created dialog with the `computer remote add` line.
- [x] Tests: token lifecycle; expiry; creator losing access revokes.

### Task 13: Agent API

- `GET /api/agent/v1` (status JSON), `GET /api/agent/v1/exec` (WebSocket: first text message `{"argv":[…],"cwd":…,"env":{…},"tty":bool,"cols":n,"rows":n}`; then binary frames prefixed with one byte stream id: 0 stdin, 1 stdout, 2 stderr; text `{"resize":[c,r]}`, `{"eof":true}`; final text `{"exit":n}`), `/api/agent/v1/cdp/*` → computer Caddy `/_cdp/*` with `webSocketDebuggerUrl` rewritten.
- Exec as `user` in `/workspace` with the image's env; log every call with the token name.
- [x] Tests: exec output and exit code; tty shell; CDP version through the proxy; another computer's token refused; session cookie refused.

### Task 14: The `computer` command in Go

- `supervisor/cmd/computer`: local desktop actions (same as the bash script: screenshot, zoom, size, cursor, move, click, double-click, triple-click, mouse-down, mouse-up, drag, type, key, hold-key, scroll, wait) via `xdotool`/`maim`; remotes (`remote add|list|remove|default`, `-c`, `COMPUTER_REMOTE`, `COMPUTER_URL`+`COMPUTER_TOKEN`, `~/.config/deskyard/remotes.json` 0600); `exec`, `shell`, `cp`, `browser connect`, `status`, `guide`, `describe --json`; desktop actions remote = exec `computer …` in the computer.
- The image builds it from `supervisor/` (multi-stage) and drops the bash script.
- [x] Tests: golden `--help`, `guide`, `describe --json`; integration: `computer -c alice exec -- id -u` prints 1000; `cp` round-trip; `screenshot` PNG; `browser connect` + Playwright `connect_over_cdp`.

# Phase 5: settings and images

### Task 15: Settings editing, image catalog and upgrades

- `PATCH /api/computers/{name}` (image, memory, CPUs) recreates the container (same labels/ID/folder/token).
- `images.json` catalog (default one entry); `GET/POST/DELETE /api/images`; daily + "Check now" update check (pull, compare image IDs); "Update available" per computer; upgrade one or all (job with progress); contract check refuses images without `deskyard.contract=1`.
- UI: Settings tab, Images page, Upgrade dialog (what survives).
- [x] Tests: upgrade recreates and keeps the folder and ID; image without contract label refused.

# Phase 6: HTTPS, phone, docs, releases

### Task 16: Finish

- CertMagic HTTPS when `DESKYARD_DOMAIN` is set (ports 80/443).
- Phone layout: wall one column, computer page stacks, desktop bar collapses into a menu.
- README rewritten around the supervisor; in-computer skills mention `computer` remotes, not `docker exec`; architecture pages flip to `built`.
- Release workflow: `supervisor` and `computer` binaries for linux/darwin (amd64, arm64) and `computer` for windows, attached to tags.
- [ ] Tests: full integration suite green directly and in a container.
