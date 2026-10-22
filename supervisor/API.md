# Supervisor HTTP API

The contract between the supervisor (Go, `internal/server`), its web UI (`web/`) and the `deskyard` command (`cmd/deskyard`). JSON everywhere; times are RFC 3339 strings; sizes in bytes; errors are `{"error": "message"}` with a 4xx/5xx status.

## Sessions (web UI)

The UI authenticates with the `deskyard_session` cookie (HttpOnly, SameSite=Strict). Every `/api/*` route except `POST /api/login` answers 401 without a session. State-changing requests (POST, PUT, PATCH, DELETE) must carry an `Origin` matching the supervisor's own (browsers' `fetch` does this); otherwise 403.

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/login` | `{"username","password"}` | 200 `Me`, sets the cookie; 401 wrong; 429 throttled |
| `POST /api/logout` | | 204, clears the cookie |
| `GET /api/me` | | `Me` = `{"username","admin":bool}` |
| `POST /api/me/password` | `{"current","new"}` | 204 (signs out everywhere else; the response re-issues this session) |
| `GET /api/host` | | `{"cpus":n,"memBytes":n,"running":n,"total":n,"memUsed":n}` (counts over desktops the account can see) |

### Desktops

`Desktop`:

```json
{
  "id": "6f1c…", "name": "alice",
  "state": "running" | "stopped" | "starting" | "creating" | "upgrading" | "removing",
  "startedAt": "…", "created": "…" | null, "job": "a1b2c3d4" | null,
  "folder": "/Users/zef/deskyard/alice",
  "image": "ghcr.io/zefhemel/deskyard-debian-xfce:latest",
  "imageTitle": "Debian XFCE", "imageVersion": "2026.10.15",
  "updateAvailable": "2026.10.22" | null,
  "settings": {"memoryBytes": 0, "nanoCpus": 0},
  "access": ["anna"],
  "viewers": ["zef", "anna"],
  "forwards": [{"token": "deploy-bot", "direction": "in", "port": 3000}, {"token": "deploy-bot", "direction": "out", "port": 5173}],
  "agents": [{"id": "k3j9…", "name": "deploy-bot", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…"}],
  "tokens": 2,
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the desktop's CPU limit, or to all host CPUs without one (0–100). `access` lists regular accounts only (admins can use everything) and is only filled for admins. `forwards` are the port mappings agent tokens have open on the desktop (`deskyard forward`): `in` maps a port of the agent's machine into the desktop, `out` a desktop port out to the agent's machine; `port` is always the desktop's port (the agent's own port numbers aren't shown). Each entry is listed once, sorted by token, direction and port; an `in` port appears once the desktop is listening on it. `created` survives upgrades and settings changes (null for desktops made before it was recorded). `job` is the running create, upgrade or settings job, which anyone who can see the desktop may follow. `agents` are the agents present on the desktop (always a list, sorted by name then id): agent tokens (`id` is the token's id, `owner` the account that made it) and programs running inside the desktop (`inside: true`, `id` `inside:<program>`, `owner` empty). An agent is present while something it started is running, a browser connection or port mapping is open, or for 5 minutes after its last action; `active` means an action in the last 10 seconds; `color` is fixed per id. A desktop's own page is at `/<name>/`.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/desktops` | | `[Desktop]` the account can see, sorted by name |
| `GET /api/desktops/{name}` | | `Desktop`; 404 if missing or not visible |
| `POST /api/desktops` (admin) | `{"name","image","memoryBytes","nanoCpus","access":[usernames]}` | 202 `{"job": id}` |
| `POST /api/desktops/{name}/start` \| `stop` \| `restart` | | 204 |
| `DELETE /api/desktops/{name}?files=1` (admin) | | 204; `files=1` also deletes the workspace folder |
| `PATCH /api/desktops/{name}` (admin) | `{"image"?, "memoryBytes"?, "nanoCpus"?}` | 202 `{"job": id}` (recreates the container) |
| `PUT /api/desktops/{name}/access` (admin) | `{"usernames":[…]}` | 204 |
| `GET /api/desktops/{name}/screen.png` | `?full=1` for full size | PNG, 480 px wide by default; 409 when stopped |
| `GET /api/desktops/{name}/processes` | | `{"titles":[…],"rows":[[…]]}` (columns PID, USER, %CPU, RSS (KiB), ELAPSED, COMMAND) |
| `GET /api/desktops/{name}/stats` | | `{"history":[{"t":"…","cpuPercent":n,"memBytes":n}], "memLimit":n}` (15 min at 5 s) |
| `GET /api/desktops/{name}/folder` | | `{"path","bytes","complete"}`; `complete` is false when some directories couldn't be read, so `bytes` is a lower bound |
| `GET /api/desktops/{name}/activity` | | `Activity` (below) |
| `GET /api/desktops/{name}/screen-events` | | server-sent events (below) |
| `POST /api/desktops/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/desktops/{name}/upgrade` (admin) | | 202 `{"job": id}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}` |

#### Activity

`Activity` is what the agents on a desktop are doing:

```json
{
  "name": "alice",
  "agents": [{
    "id": "k3j9…", "name": "deploy-bot", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…",
    "now": [
      {"kind": "command", "text": "npm run build", "since": "…"},
      {"kind": "browser", "since": "…"},
      {"kind": "forward", "text": "3000 in · 5173 out", "since": "…"}
    ],
    "recent": [
      {"kind": "screen", "t": "…", "action": "click", "count": 14},
      {"kind": "command", "t": "…", "text": "sh -c 'exit 3'", "exit": 3, "durationMs": 12},
      {"kind": "command", "t": "…", "text": "sleep 30", "disconnected": true, "durationMs": 4100},
      {"kind": "browser", "t": "…", "durationMs": 240000},
      {"kind": "copy", "t": "…", "direction": "in", "path": "/workspace/f.bin", "size": 1000}
    ]
  }]
}
```

`agents` has the same entries, in the same order, as `Desktop.agents`, each with `now` (what it is doing: running commands, browser use, its port mappings, `in` and `out` as in `Desktop.forwards`) and `recent` (at most 20 lines, newest first; adjacent screen actions of one kind are merged into one line with a `count`). Summaries cover the last 15 minutes; they are kept in memory only, nothing is stored, and a supervisor restart forgets them. Command texts are masked (secrets show as `•••`) and typed text is never recorded. Signed-in accounts that can see the desktop may read it; agent tokens can't (401), others get 404.

`GET /api/desktops/{name}/screen-events` is a server-sent event stream of the screen actions agents make, for drawing them over the screen: `event: screen` with data a `ScreenEvent`, `{"agent":{"id","name","color","inside"},"t":"…","action":"click","pos":[100,120]}`, plus `button`, `dir`, `n`, `keys` and `chars` where the action has them (`pos` is `[x1,y1,x2,y2]` for `drag`; `type` carries only `chars`, the number of characters). The actions sent are `click`, `double-click`, `triple-click`, `drag`, `scroll`, `key`, `hold-key` and `type`. A comment line keeps the stream alive every 25 s. Who may open it is as for `Activity`; the stream ends when the account loses access or the desktop is removed. Events are only produced while someone is subscribed, and a slow reader misses some.

### Jobs

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"desktop"?:name}`. The stream ends after `done` or `failed`. The account that started a job, admins, and (for a desktop's job) anyone who can see that desktop may follow it; others get 404.

### Events

`GET /api/events` is a server-sent event stream for the desktops the account can see:

- `event: desktop` — a `Desktop` that appeared or changed
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running desktop
- `event: viewers` — `{"name","viewers":[usernames],"forwards":[…],"agents":[…]}` (as in `Desktop`), when someone starts or stops viewing or a mapping opens or closes (sent at most every 250 ms per desktop; the last state always follows), and at once when the agents present change (one arrives or leaves, or turns active or idle)
- `event: activity` — an `Activity` (as from `GET /api/desktops/{name}/activity`), when what agents do on the desktop changes (sent at most once a second per desktop; the last state always follows)

### Accounts (admin)

`Account` = `{"username","admin":bool,"desktops":[names],"created","lastLogin"|null}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/accounts` | | `[Account]` (any signed-in account may list usernames, for avatars; non-admins get `[{"username"}]`) |
| `POST /api/accounts` | `{"username","password","admin","desktops":[names]}` | 201 `Account` |
| `PATCH /api/accounts/{username}` | `{"password"?, "admin"?, "desktops"?:[names]}` | `Account`; 409 when it would leave no admin |
| `DELETE /api/accounts/{username}` | | 204; 409 for the last admin |

### Agent tokens

`Token` = `{"id","name","createdBy","created","expires"|null,"lastUsed"|null,"expired":bool}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/desktops/{name}/tokens` | | `[Token]` (anyone with access) |
| `POST /api/desktops/{name}/tokens` | `{"name","expires": "…"|null}` | 201 `Token` plus `"token":"dyt_…"` (shown once) and `"remoteAdd":"deskyard remote add alice https://host/alice --token dyt_…"` |
| `DELETE /api/desktops/{name}/tokens/{id}` | | 204 (own tokens; admins any) |

### Images (admin)

`CatalogImage` = `{"ref","title","version","description","desktops":[names],"behind":[names],"checked"|null}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New desktop dialog) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (pulls and checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 409 while desktops use it |
| `POST /api/images/check` | | 202 `{"job"}` (pull every ref, recompute updates) |
| `POST /api/images/upgrade` | `{"ref"}` | 202 `{"job"}` (upgrades every desktop behind on it) |

## Desktop

- `/<name>/` serves the UI's desktop page (the SPA's `index.html`); the page frames `/<name>/vnc/vnc.html?path=<name>/websockify&autoconnect=1&resize=remote&reconnect=1&clipboard_up=1&clipboard_down=1&clipboard_seamless=1`. Framed without `show_control_bar` (any value, even `0`, shows it), KasmVNC hides its own controls and turns the clipboard off unless the `clipboard_*` parameters say otherwise; the page talks to the frame with `postMessage` (see `web/src/desktop/kasm.ts`).
- `/<name>/vnc/*` are the KasmVNC client files (from the desktop's image), `/<name>/websockify` the desktop WebSocket.

## Agent API

Authenticated with `Authorization: Bearer dyt_<id>_<secret>` only (cookies are ignored here). The desktop is the token's; a desktop name in a remote's URL is only a check on the client side. 401 for a missing, unknown, revoked or expired token; 409 `{"error":"alice is stopped; someone with access can start it"}` from `exec`, `forward` and `cdp` when the desktop isn't running (`GET /api/agent/v1` always answers 200 with its `state`). No Origin checks: agents aren't browsers.

### `GET /api/agent/v1`

```json
{"name":"alice","state":"running","image":"ghcr.io/…","imageTitle":"Debian XFCE","imageVersion":"2026.10.22","screen":{"width":1280,"height":800},"api":1}
```

### `GET /api/agent/v1/exec` (WebSocket)

1. The client sends one **text** message: `{"argv":["bash","-lc","ls"],"cwd":"/workspace","env":{"K":"V"},"tty":false,"cols":80,"rows":24}` (`cwd`, `env`, `tty`, `cols`, `rows` optional; `cwd` defaults to `/workspace`).
2. Then **binary** messages, each starting with one stream byte: `0` stdin (client → server), `1` stdout, `2` stderr (server → client). With `tty`, all output is stream 1.
3. **Text** messages from the client: `{"resize":[cols,rows]}`, `{"eof":true}` (closes stdin).
4. The server's last message is **text** `{"exit":code}`, then it closes the WebSocket. An error before starting is text `{"error":"…"}`.

Commands run as `user` (UID 1000) with the desktop's image environment (PATH includes the user's tool folders; `argv[0]` is looked up on it; `bash -lc` for a login shell is the client's choice). Details:

- No message size limit beyond 64 MB per message; send stdin in chunks.
- `cols`/`rows` are optional with `tty` (default 80×24); `{"resize"}` without `tty` is ignored.
- `{"eof"}` closes stdin; with `tty` it sends Ctrl-D. The server sends `{"exit"}` when the process ends, whether or not stdin was closed.
- A process killed by a signal exits with 128 + the signal number.
- `DESKYARD_ACTIVITY=off` is always set in the command's environment and can't be overridden by `env`: the supervisor records the command itself (see the activity API), so the in-desktop `deskyard` must not record it again.
- `{"error"}` only comes instead of running the command (bad first message, the desktop stopped); once output started, the last message is always `{"exit"}`.

### `GET /api/agent/v1/forward` (WebSocket)

TCP port mappings between the agent's machine and the desktop, many connections multiplexed on one WebSocket (`deskyard forward`). The supervisor runs `deskyard forward-helper` in the desktop as `user` (cwd `/workspace`, no TTY) and relays frames between the WebSocket and the helper's stdio, so the desktop side listens on and dials `127.0.0.1` inside the desktop. A forward token can therefore reach any loopback port in its desktop (ports from 1024 up for listening), as its exec can. Like any local process in the desktop, pages in the desktop's browser can reach the in-mapped ports. A token may have 4 forward links open on a desktop; a fifth gets `{"error":"…"}` and a close before the helper runs. `Desktop.forwards` lists at most 64 mappings per token (the rest still work). The CLI prints `Linked to …` and the `out` lines when the link is accepted, before the helper is known to run.

1. The client sends one **text** message: `{"forwards":[{"direction":"in"|"out","port":n}]}`, the mappings it will set up, `port` being the desktop's port (1–65535; at most 256 entries). The `out` ports are listed in `Desktop.forwards` while the WebSocket is open; the `in` ports once the desktop listens on them.
2. Then **binary** messages, one frame each in both directions: `type u8 | stream u32 BE | payload`, at most 32 KiB of payload:

| Type | Name | Payload | Meaning |
| --- | --- | --- | --- |
| 1 | Listen | port u16 BE | client → desktop: listen on 127.0.0.1:port; `stream` is the listener id. Refused below 1024 (`port below 1024`) or when taken (`port in use`). |
| 2 | Listening | | desktop → client: the listen worked |
| 3 | Open | port u16 BE | a new connection on `stream`: from the client, the desktop dials 127.0.0.1:port; from the desktop, a connection accepted on a listener on that port |
| 4 | Data | bytes | data on a connection |
| 5 | Close | | half-close of the sender's direction; on a listener id, stop listening |
| 6 | Reset | UTF-8 text | the listen or dial failed, or the connection was aborted |
| 7 | Window | n u32 BE | the sender may receive n more bytes on this connection |

   The client picks odd stream ids, the desktop even ones; an id in use can't be opened again (the desktop side ends the connection). Each connection starts with a 256 KiB window in each direction. A side may have at most 64 listeners and 256 connections at once; beyond that it answers with Reset.
3. A **text** message from the server is `{"error":"…"}` (the helper couldn't start or failed), followed by the close. The server closes the WebSocket with a reason (`desktop stopped`, …) when the desktop side ends, and with status 1008 when the client sends a text message after the first, a frame shorter than 5 bytes or longer than 32 KiB + 5 that is still within the read limit, a frame of an unknown type, or a Listen beyond 64 listeners pending or open. A single WebSocket message over 64 KiB is closed with 1009 (message too big) by the read limit, before it is looked at.

The connection closes when the token is revoked or expires, or the desktop stops, restarts or is upgraded; open connections through it are reset. The server pings every 30 s and drops a client that doesn't answer.

### `/api/agent/v1/cdp/…` (HTTP and WebSocket)

The shared Chromium's DevTools protocol: `GET /api/agent/v1/cdp/json/version`, `/json/list`, `PUT /json/new?url`, and WebSocket `/api/agent/v1/cdp/devtools/…`. In `/json*` responses (including `/json/version`), `webSocketDebuggerUrl` is absolute, `ws(s)://<host>/api/agent/v1/cdp/devtools/…`, and the `ws=` parameter in `devtoolsFrontendUrl` is `<host>/api/agent/v1/cdp/devtools/…` (Chromium's own `ws=`/`wss=` choice). The first request may take several seconds while Chromium starts.
