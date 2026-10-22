# Supervisor HTTP API

The contract between the supervisor (Go, `internal/server`), its web UI (`web/`) and the `computer` command (`cmd/computer`). JSON everywhere; times are RFC 3339 strings; sizes in bytes; errors are `{"error": "message"}` with a 4xx/5xx status.

## Sessions (web UI)

The UI authenticates with the `deskyard_session` cookie (HttpOnly, SameSite=Strict). Every `/api/*` route except `POST /api/login` answers 401 without a session. State-changing requests (POST, PUT, PATCH, DELETE) must carry an `Origin` matching the supervisor's own (browsers' `fetch` does this); otherwise 403.

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/login` | `{"username","password"}` | 200 `Me`, sets the cookie; 401 wrong; 429 throttled |
| `POST /api/logout` | | 204, clears the cookie |
| `GET /api/me` | | `Me` = `{"username","admin":bool}` |
| `POST /api/me/password` | `{"current","new"}` | 204 (signs out everywhere else; the response re-issues this session) |
| `GET /api/host` | | `{"cpus":n,"memBytes":n,"running":n,"total":n,"memUsed":n}` (counts over computers the account can see) |

### Computers

`Computer`:

```json
{
  "id": "6f1c…", "name": "alice",
  "state": "running" | "stopped" | "starting" | "creating" | "upgrading" | "removing",
  "startedAt": "…", "folder": "/Users/zef/computers/alice",
  "image": "ghcr.io/zefhemel/deskyard-debian-xfce:latest",
  "imageTitle": "Debian XFCE", "imageVersion": "2026.10.15",
  "updateAvailable": "2026.10.22" | null,
  "settings": {"memoryBytes": 0, "nanoCpus": 0},
  "access": ["anna"],
  "viewers": ["zef", "anna"],
  "tokens": 2,
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the computer's CPU limit, or to all host CPUs without one (0–100). `access` lists regular accounts only (admins can use everything) and is only filled for admins. The desktop of a computer is at `/<name>/`.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/computers` | | `[Computer]` the account can see, sorted by name |
| `GET /api/computers/{name}` | | `Computer`; 404 if missing or not visible |
| `POST /api/computers` (admin) | `{"name","image","memoryBytes","nanoCpus","access":[usernames]}` | 202 `{"job": id}` |
| `POST /api/computers/{name}/start` \| `stop` \| `restart` | | 204 |
| `DELETE /api/computers/{name}?files=1` (admin) | | 204; `files=1` also deletes the workspace folder |
| `PATCH /api/computers/{name}` (admin) | `{"image"?, "memoryBytes"?, "nanoCpus"?}` | 202 `{"job": id}` (recreates the container) |
| `PUT /api/computers/{name}/access` (admin) | `{"usernames":[…]}` | 204 |
| `GET /api/computers/{name}/screen.png` | `?full=1` for full size | PNG, 480 px wide by default; 409 when stopped |
| `GET /api/computers/{name}/processes` | | `{"titles":[…],"rows":[[…]]}` (columns PID, USER, %CPU, RSS (KiB), ELAPSED, COMMAND) |
| `GET /api/computers/{name}/stats` | | `{"history":[{"t":"…","cpuPercent":n,"memBytes":n}], "memLimit":n}` (15 min at 5 s) |
| `POST /api/computers/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/computers/{name}/upgrade` (admin) | | 202 `{"job": id}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}` |

### Jobs

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"computer"?:name}`. The stream ends after `done` or `failed`.

### Events

`GET /api/events` is a server-sent event stream for the computers the account can see:

- `event: computer` — a `Computer` that appeared or changed
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running computer
- `event: viewers` — `{"name","viewers":[usernames]}`

### Accounts (admin)

`Account` = `{"username","admin":bool,"computers":[names],"created","lastLogin"|null}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/accounts` | | `[Account]` (any signed-in account may list usernames, for avatars; non-admins get `[{"username"}]`) |
| `POST /api/accounts` | `{"username","password","admin","computers":[names]}` | 201 `Account` |
| `PATCH /api/accounts/{username}` | `{"password"?, "admin"?, "computers"?:[names]}` | `Account`; 409 when it would leave no admin |
| `DELETE /api/accounts/{username}` | | 204; 409 for the last admin |

### Agent tokens

`Token` = `{"id","name","createdBy","created","expires"|null,"lastUsed"|null,"expired":bool}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/computers/{name}/tokens` | | `[Token]` (anyone with access) |
| `POST /api/computers/{name}/tokens` | `{"name","expires": "…"|null}` | 201 `Token` plus `"token":"dyt_…"` (shown once) and `"remoteAdd":"computer remote add alice https://host/alice --token dyt_…"` |
| `DELETE /api/computers/{name}/tokens/{id}` | | 204 (own tokens; admins any) |

### Images (admin)

`CatalogImage` = `{"ref","title","version","description","computers":[names],"behind":[names],"checked"|null}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New computer dialog) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (pulls and checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 409 while computers use it |
| `POST /api/images/check` | | 202 `{"job"}` (pull every ref, recompute updates) |
| `POST /api/images/upgrade` | `{"ref"}` | 202 `{"job"}` (upgrades every computer behind on it) |

## Desktop

- `/<name>/` serves the UI's desktop page (the SPA's `index.html`); the page frames `/<name>/vnc/vnc.html?path=<name>/websockify&autoconnect=1&resize=remote&reconnect=1&clipboard_up=1&clipboard_down=1&clipboard_seamless=1`. Framed without `show_control_bar` (any value, even `0`, shows it), KasmVNC hides its own controls and turns the clipboard off unless the `clipboard_*` parameters say otherwise; the page talks to the frame with `postMessage` (see `web/src/desktop/kasm.ts`).
- `/<name>/vnc/*` are the KasmVNC client files (from the computer's image), `/<name>/websockify` the desktop WebSocket.

## Agent API

Authenticated with `Authorization: Bearer dyt_<id>_<secret>` only (cookies are ignored here). The computer is the token's; a computer name in a remote's URL is only a check on the client side. 401 for a missing, unknown, revoked or expired token; 409 `{"error":"alice is stopped; someone with access can start it"}` from `exec` and `cdp` when the computer isn't running (`GET /api/agent/v1` always answers 200 with its `state`). No Origin checks: agents aren't browsers.

### `GET /api/agent/v1`

```json
{"name":"alice","state":"running","image":"ghcr.io/…","imageTitle":"Debian XFCE","imageVersion":"2026.10.22","screen":{"width":1280,"height":800},"api":1}
```

### `GET /api/agent/v1/exec` (WebSocket)

1. The client sends one **text** message: `{"argv":["bash","-lc","ls"],"cwd":"/workspace","env":{"K":"V"},"tty":false,"cols":80,"rows":24}` (`cwd`, `env`, `tty`, `cols`, `rows` optional; `cwd` defaults to `/workspace`).
2. Then **binary** messages, each starting with one stream byte: `0` stdin (client → server), `1` stdout, `2` stderr (server → client). With `tty`, all output is stream 1.
3. **Text** messages from the client: `{"resize":[cols,rows]}`, `{"eof":true}` (closes stdin).
4. The server's last message is **text** `{"exit":code}`, then it closes the WebSocket. An error before starting is text `{"error":"…"}`.

Commands run as `user` (UID 1000) with the computer's image environment (PATH includes the user's tool folders; `argv[0]` is looked up on it; `bash -lc` for a login shell is the client's choice). Details:

- No message size limit beyond 64 MB per message; send stdin in chunks.
- `cols`/`rows` are optional with `tty` (default 80×24); `{"resize"}` without `tty` is ignored.
- `{"eof"}` closes stdin; with `tty` it sends Ctrl-D. The server sends `{"exit"}` when the process ends, whether or not stdin was closed.
- A process killed by a signal exits with 128 + the signal number.
- `{"error"}` only comes instead of running the command (bad first message, the computer stopped); once output started, the last message is always `{"exit"}`.

### `/api/agent/v1/cdp/…` (HTTP and WebSocket)

The shared Chromium's DevTools protocol: `GET /api/agent/v1/cdp/json/version`, `/json/list`, `PUT /json/new?url`, and WebSocket `/api/agent/v1/cdp/devtools/…`. In `/json*` responses (including `/json/version`), `webSocketDebuggerUrl` is absolute, `ws(s)://<host>/api/agent/v1/cdp/devtools/…`, and the `ws=` parameter in `devtoolsFrontendUrl` is `<host>/api/agent/v1/cdp/devtools/…` (Chromium's own `ws=`/`wss=` choice). The first request may take several seconds while Chromium starts.
