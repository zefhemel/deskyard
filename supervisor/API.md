# Supervisor HTTP API

The contract between the supervisor (Go, `internal/server`), its web UI (`web/`) and the `deskyard` command (`cmd/deskyard`). JSON everywhere; times are RFC 3339 strings; sizes in bytes; errors are `{"error": "message"}` with a 4xx/5xx status.

People use the API through the web UI (a session cookie) and the `deskyard` CLI (a client credential, obtained with `deskyard login` or `deskyard auth`); scripts and integrations use the CLI's credential the same way. `agents` fields keep their wire name for compatibility. Per-environment access tokens (`dyt_…`), their `/api/environments/{name}/tokens` routes and the `/api/agent/v1/…` routes are removed without aliases and answer 404: remote access lives under `/api/environments/{name}/…` (see Remote access API). The former `/api/desktops/…` routes and `desktop`/`desktops`/`desktopId` fields were replaced by `/api/environments/{name}/…` and `environment`/`environments` names without aliases; the old routes answer 404.

Common shapes used below:

- `Level` = `"full" | "observe"`. Every grant of an environment to an account has one; admins are full on every environment.
- `Grant` = `{"environment","level":Level}`: an account's grant.
- `AccessGrant` = `{"username","level":Level}`: an environment's grant to an account.
- `Sharer` = `{"username","id"}`: the account (by username and account ID) that made a share.
- A **share** is a token account (no password) with exactly one grant and `sharedBy` set, made by **Share** on an environment.

## Authentication

Every `/api/*` route except `POST /api/login`, `POST /api/logout`, `POST /api/links/inspect`, `POST /api/links/redeem`, `POST /api/cli-login` and `POST /api/cli-login/{id}/token` needs a browser session or a client credential, and answers 401 `{"error":"sign in first"}` with neither.

### Browser sessions

The UI authenticates with the `deskyard_session` cookie (HttpOnly, SameSite=Strict). The cookie names the account by its ID and session epoch, so it stops working when the account is deleted (also when one of the same name is created again), expires, or bumps its epoch. Password and token accounts get the same 30-day sliding session; the session (and the cookie's own expiry) never outlives an expiring account's `expires`. With a cookie, state-changing requests (POST, PUT, PATCH, DELETE) must carry an `Origin` matching the supervisor's own (browsers' `fetch` does this); otherwise 403 `cross-origin request refused`.

### Client credentials

A client credential is a bearer secret `dyc_<id>_<secret>` (`id` 8 hex characters, `secret` 32 random bytes in unpadded base64url), issued once per signed-in client by `POST /api/links/redeem` (`client:"cli"`) or `POST /api/cli-login/{id}/token`. It is sent as `Authorization: Bearer dyc_…` and is accepted on every `/api/` route. Credentials have a `kind`: `cli`, or `webdav` for an account's WebDAV password (`POST /api/me/webdav-password`), which is accepted only as the password of WebDAV's Basic auth (see Desktop) and answers 401 as a bearer.

- A request with an `Authorization: Bearer` header authenticates by the credential only (a cookie sent along is ignored). An unknown, malformed or revoked credential, or one whose account was deleted, recreated under the same name or expired, answers 401 `{"error":"the credential is unknown, revoked or expired"}`.
- Bearer requests skip the same-origin check (there is no ambient credential to forge) and never set or clear cookies, also on routes that re-issue a browser session.
- Access is checked on every request against the account's current grants and admin flag; there is no per-credential scope.
- A credential stays valid until it is deleted (`DELETE /api/me/credentials/{id}`, `DELETE /api/accounts/{username}/credentials/{id}`) or its account is deleted, recreated or expires. Deleting a credential closes its live connections (exec, forward, CDP, and the `/api/events` and `screen-events` streams it opened) at once.
- WebSocket routes that check the `Origin` themselves (`/api/events`, `/api/environments/{name}/terminal`) stay browser-only in practice: a request without a same-origin `Origin` gets 403 there.

### Signing in and the signed-in account

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/login` | `{"username","password"}` | 200 `Me`, sets the cookie (a browser login of another account it replaces ends as on logout, closing its terminals); 401 `wrong username or password`, also for token accounts (no password); 403 cross-origin; 429 throttled |
| `POST /api/logout` | | 204, clears the cookie and closes this browser login’s terminals; requires same-origin Origin, including when already signed out. It never touches client credentials |
| `GET /api/me` | | `Me` (below) |
| `POST /api/me/password` | `{"current","new"}` | 204 (signs out everywhere else; the response re-issues this session); 400 `a new password is required`; 400 `token accounts have no password` for a token account; 403 `the current password is wrong` |
| `POST /api/me/sign-out-everywhere` | | 204; bumps the account's session epoch, ending every browser session of the account and closing their desktop streams and terminals; a cookie request gets this session re-issued. Client credentials are not affected, except that the WebDAV password is removed |
| `GET /api/me/credentials` | | `[Credential]` of the signed-in account, newest first |
| `DELETE /api/me/credentials/{id}` | | 204; deletes one of the account's own credentials (the requesting one included, which is how `deskyard logout` revokes itself) and closes its connections; 404 `no such credential` for an unknown id or another account's; 500 when the deletion can't be saved, in which case the credential is held deleted in memory (refused, its connections closed) until it is saved |
| `POST /api/me/webdav-password` | | 201 `{"password":"dyc_…"}`, shown only here: makes the account's WebDAV password (a `webdav` credential named `WebDAV password`, listed in `GET /api/me/credentials` and revocable there), replacing the one it had. While it exists, WebDAV takes it instead of the account password. 403 `only accounts with a password can use WebDAV` for token accounts and shares |
| `DELETE /api/me/webdav-password` | | 204; removes the WebDAV password (none is fine), so WebDAV takes the account password again |
| `GET /api/host` | | `{"cpus":n,"memBytes":n,"running":n,"total":n,"memUsed":n}` (counts over environments the account can see) |

`Me` = `{"username","admin":bool,"kind":"password"|"token","share":bool}`: `kind` is `token` for accounts without a password; `share` is true for an account made by Share.

`Credential` = `{"id","kind":"cli","name","created","lastUsed"|null,"current":bool}`. `name` describes the client (such as `deskyard CLI on zef-mbp`); `lastUsed` is updated at most once a minute; `current` is true for the credential making this request. The secret is only ever returned when the credential is issued.

`GET /api/host` also includes `memoryReclaim: {"enabled":bool,"reason"?:string}`, the cached startup capability result. The probe checks the full helper path using a disposable container, without reclaiming any existing environment. Failures disable the optional action and supply a reason; capability detection is retried on supervisor restart. A disabled action returns 501 immediately. Per-environment checks still run on enabled hosts and do not change the cached host status.

## Routes

### Environments

An environment is identified by its name everywhere; there is no separate ID. `Environment`:

```json
{
  "name": "alice",
  "state": "running" | "stopped" | "starting" | "creating" | "upgrading" | "removing",
  "power": "running" | "stopped",
  "revision": 4, "appliedRevision": 4,
  "error": "", "conflict": "",
  "startedAt": "…", "created": "…" | null, "job": "a1b2c3d4" | null,
  "folder": "/Users/zef/deskyard/home/alice",
  "image": "ghcr.io/zefhemel/deskyard-debian-xfce:latest",
  "imageTitle": "Debian XFCE", "imageVersion": "2026.10.15",
  "updateAvailable": "2026.10.22" | null,
  "memoryReclaim": true, "webdav": false, "terminal": true,
  "terminals": [{"name": "main", "clients": 2, "command": "vim"}, {"name": "terminal-1", "clients": 0, "command": "bash"}],
  "settings": {"memoryBytes": 0, "nanoCpus": 0, "dockerAccess": false, "vpnConnectionId": null, "lanExceptions": [], "volumes": []},
  "vpn": {"state":"disabled","changedAt":"0001-01-01T00:00:00Z","exitIpObservedAt":"0001-01-01T00:00:00Z","desiredRevision":0,"appliedRevision":0,"idle":false},
  "level": "full" | "observe",
  "access": [{"username": "anna", "level": "full"}, {"username": "bob", "level": "observe"}],
  "viewers": ["zef", "anna", "bob"],
  "observers": ["bob"],
  "mics": ["anna"],
  "forwards": [{"token": "My laptop", "direction": "in", "port": 3000}, {"token": "My laptop", "direction": "out", "port": 5173}],
  "agents": [{"id": "k3j9…", "name": "My laptop", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…"}],
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`terminal` reports image support (`deskyard.terminal=tmux-v1`), independent of running state. Custom images may opt in only when they supply the shared helper and tmux runtime.

`terminals` are the environment's shared terminal sessions, as listed by `GET /api/environments/{name}/terminal/sessions` (below): never null, and empty unless the environment is running with a `tmux-v1` image. The supervisor lists an environment's sessions right after a session is created, ended, attached or detached through it, and when the desktop reports a change on its activity stream (its tmux hooks send a `terminals` event for any session opened, ended or renamed and any client attaching or detaching, so native and CLI sessions show within about a second while anyone has the UI open). Every such running environment is also listed every 60 seconds as a safety net, for example to pick up a newly started `command`; a change of the list (a session, its `clients` or its `command`) is sent as an `environment` event. Sessions of a previous running lifetime (before a stop or restart) are never shown.

`state` is observed; `power` is the desired state saved in the environment's definition (`environments.json`), set by start, stop and restart. `revision` counts the definition's container-affecting changes (image, limits, network, volume grants, Docker access); `appliedRevision` is the revision the current container was built from, 0 when the environment has no container. `revision` ahead of `appliedRevision` is a change not yet applied: a stopped environment applies it on start, and a failed apply keeps the old container and reports why in `error`. `error` also reports a missing home folder, a granted volume that doesn't exist or whose folder is missing, or a VPN connection that no longer exists; `""` when there is none. `conflict` is set when a container with the environment's name isn't matched to it (another supervisor's, or several claiming it); lifecycle operations are then refused with 409 until an administrator resolves it. A stopped environment may have no container; it is created on start.

`memoryReclaim` is the saved preference in the definition, independent of the host capability flag; changing it never bumps `revision` or recreates the container. On supported hosts the supervisor checks every 30 seconds for running environments with no viewers for 15 continuous minutes, making one 128 MiB attempt per absence (including failed/partial attempts). Viewer joins, even between checks, reset the timer; supervisor/container restarts and re-enabling the preference start fresh. Active tracked CLI clients (recent actions, running commands, browser use or forwards), lifecycle operations and unknown/stale inside status delay the attempt without consuming it. Automatic work uses the same lifecycle reservation as the manual action; it rechecks viewers, identity, policy and activity after reservation. Apps keep running and a viewer arriving during reclamation can reconnect normally. Idle status must come from a current environment's activity service (`commandsRunning:false` heartbeat less than 45 seconds old); missing/legacy/disconnected activity skips automatic work. Unreported shell/background work cannot be detected; opt out for such workloads.

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the environment's CPU limit, or to all host CPUs without one (0–100). `level` is the requesting account's level on the environment (`full` for admins). `access` is `[AccessGrant]`: the regular (non-admin) accounts granted the environment with their level, sorted by username, leaving out shares (they are listed by `GET /api/environments/{name}/shares`); it is only filled for admins and is `[]` for everyone else. `viewers` are the accounts with a desktop stream or browser terminal open (each once, sorted); `observers` those of them watching at observe level (always a list, a subset of `viewers`); `mics` those of them with a mic on, as their pages report it (see `POST /api/environments/{name}/mic`). `forwards` are the port mappings CLI clients have open on the environment (`deskyard forward`): `in` maps a port of the client’s machine into the environment, `out` an environment port out to the client’s machine; `port` is always the environment's port (the client's own port numbers aren't shown). `token` keeps its wire name and holds the client credential's `name`. Each entry is listed once, sorted by token, direction and port; an `in` port appears once the environment is listening on it. `created` is when the definition was made and survives upgrades and settings changes. `job` is the running create, upgrade or settings job, which anyone who can see the environment may follow. `agents` are the CLI clients and local programs present in the environment (always a list, sorted by name then id): client credentials (`id` is the credential's id, `name` its name, `owner` its account's username) and programs running inside the environment (`inside: true`, `id` `inside:<program>`, `owner` empty). A client is present while something it started is running, a browser connection or port mapping is open, or for 5 minutes after its last action; `active` means an action in the last 10 seconds; `color` is fixed per id. An environment's page is at `/<name>/`, with its desktop as the default tab and a terminal session's tab at `/<name>/?terminal=<session>` (there is no separate terminal page); the UI's management page for it is `/environments/<name>`.

#### Observe level

An account with an `observe` grant may watch an environment and nothing else. The supervisor enforces this on every route, not only in the UI. Observers may use:

- `GET /api/environments` and `GET /api/environments/{name}` (redacted, below), and the environment's `environment`, `removed`, `stats` and `viewers` events on `GET /api/events` (`activity` events are only sent to accounts with full access);
- `GET /api/environments/{name}/stats`, `GET /api/environments/{name}/screen.png` and `GET /api/environments/{name}/screen-events`;
- the environment's page `/<name>/` and its desktop stream (view only: the supervisor drops the observer's keyboard, mouse, clipboard, resize and settings messages before they reach the desktop, and the desktop's clipboard and printed documents before they reach the observer, see Desktop).

The environment's jobs (`GET /api/jobs/{id}`) are not among them: their progress and results describe the environment's configuration and files.

Every other route of the environment answers 403 to an observer (`{"error":"observe-only access can't do that"}` on the routes full access allows; admin-only routes keep their admin refusal), among them power actions (`start`, `stop`, `restart`, `upgrade`, `keys`), `mic`, `terminal` and `terminal/sessions`, `processes`, `folder`, `activity`, `upload`, `shares`, `status`, `exec`, `forward` and `cdp`. Accounts without any grant of the environment get 404 as before.

The `Environment` an observer receives has `level: "observe"` and its configuration blanked: `folder` is `""`; `settings` is `{"memoryBytes":0,"nanoCpus":0,"dockerAccess":false,"vpnConnectionId":null,"lanExceptions":[],"volumes":[]}`; `vpn` is `{"state":"disabled","changedAt":"0001-01-01T00:00:00Z","exitIpObservedAt":"0001-01-01T00:00:00Z","desiredRevision":0,"appliedRevision":0,"idle":false}`; `memoryReclaim` and `webdav` are false; `job` is null; `access`, `terminals`, `forwards` and `agents` are `[]`. The other fields (name, state, power, revisions, error, conflict, times, image fields, `updateAvailable`, `terminal`, `viewers`, `observers`, `mics`, `stats`) are as for full access. Lowering a grant from `full` to `observe`, or removing it, closes the account's open desktop streams, terminals and CLI connections (exec, forward, CDP) on the environment at once; the page reconnects at the new level.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/environments` | | `{"environments":[Environment],"conflicts":[Conflict]}`: the environments the account can see, sorted by name, including stopped ones without a container. `conflicts` is always `[]` for regular accounts |
| `GET /api/environments/{name}` | | `Environment`; 404 `no such environment` if missing or not visible |
| `POST /api/environments` (admin) | `{"name","image","memoryBytes","nanoCpus","access":[AccessGrant],"volumes"?:[VolumeGrant],"dockerAccess"?:bool}` | 202 `{"job": id}`; the definition is saved (revision 1, power running) before the container is made and removed again, with whatever creation made, when creation fails or the desktop isn't ready within two minutes; 400 `"<name>" is reserved by the supervisor` for the supervisor's own top-level routes and UI pages (`api`, `login`, `logout`, `assets`, `static`, `healthz`, `desktops`, `environments`, `images`, `accounts`, `volumes`, `link`, `cli-login`, `account`, and any name starting with `_`); 409 for a name in use, including one claimed by a conflicting container; 400 `no volume <name>` for a grant of a volume that doesn't exist; 400 `no account <username>` for an unknown account in `access`, 400 for a `level` other than `full` or `observe`, and 400 `<username> is a share; it can only have the environment it was shared for` for a share |
| `POST /api/environments/{name}/start` \| `stop` \| `restart` | | 204; saves `power`. Start creates the container when there is none or its revision is behind; stop keeps the container |
| `PUT /api/environments/{name}/webdav` (admin) | `{"webdav":bool}` | 204. Saves `webdav` in the definition without a new revision and without touching the container; default false. Switching it off drops the environment's WebDAV locks. 400 missing/invalid boolean, 403 regular accounts with access, 404 without access. |
| `PUT /api/environments/{name}/memory-policy` (admin) | `{"memoryReclaim":bool}` | 204. Saves `memoryReclaim` in the definition without a new revision and without recreating/restarting it; default true. A session (same-origin) or a client credential. 400 missing/invalid boolean, 403 regular accounts with access, 404 without access, 500 persistence failure. |
| `POST /api/environments/{name}/reclaim-memory` (admin) | | 200 `{"requestedBytes","beforeBytes","afterBytes","swapBeforeBytes","swapAfterBytes","partial":bool}`. Requests 128 MiB of reclaim while apps run. The before/after values are resident cgroup bytes including file cache; swap values are logical swapped bytes, which can still use compressed RAM. `partial` means the kernel reclaimed less than requested; observed resident memory can grow again. 409 stopped or busy, 501 unsupported engine/kernel or unavailable/disabled swap, 502 helper failure. A session (same-origin) or a client credential; 403 for regular accounts with access, 404 without access. |
| `DELETE /api/environments/{name}?files=1` (admin) | | 204; deletes the definition, container, network, every account's grant of it and the shares of it (with their links and credentials, closing their connections); `files=1` also deletes `home/<name>/` (without it, a later environment of that name reuses the folder) |
| `PATCH /api/environments/{name}` (admin) | `{"image"?, "memoryBytes"?, "nanoCpus"?, "dockerAccess"?:bool, "volumes"?:[VolumeGrant]}` | 202 `{"job": id}`: saves the change as a new revision, then recreates the container (a stopped environment stays stopped); 400 `no volume <name>` when a newly added grant names a volume that doesn't exist |
| `PUT /api/environments/{name}/access` (admin) | `{"grants":[AccessGrant]}` | 204; exactly the listed regular accounts get the given level and every other regular account loses its grant of the environment; shares and admins are not affected. Saved before the response; a lowered or removed grant closes that account's connections the new level doesn't allow. When saving fails (500) a lowered or removed grant still holds: the supervisor keeps it in memory, refuses what it no longer allows, and saves it once writing works again. 400 `no account <username>`, 400 for a `level` other than `full` or `observe`, 400 `<username> is a share; change it under Shares on the Access tab` for a share, 400 for a username listed twice |
| `GET /api/environments/{name}/screen.png` | `?full=1` for full size | PNG, 480 px wide by default; 409 when stopped |
| `GET /api/environments/{name}/processes` | | `{"titles":[…],"rows":[[…]]}` (columns PID, USER, %CPU, RSS (KiB), ELAPSED, COMMAND) |
| `GET /api/environments/{name}/stats` | | `{"history":[{"t":"…","cpuPercent":n,"memBytes":n}], "memLimit":n}` (15 min at 5 s) |
| `GET /api/environments/{name}/folder` | | `{"path","bytes","complete","diskFree","diskSize"}`; `path` is the home folder on the engine host; `complete` is false when some directories couldn't be read, so `bytes` is a lower bound; `diskFree` and `diskSize` describe the disk holding the folder and are omitted when it can't be read |
| `GET /api/environments/{name}/activity` | | `Activity` (below) |
| `GET /api/environments/{name}/screen-events` | | server-sent events (below) |
| `POST /api/environments/{name}/upload` | `?name=<file name>` (Desktop default), or `?path=<absolute or ~/ file path>`, the raw file as the body (one request per file; `Content-Length` or chunked) | `{"path":"/workspace/Desktop/<final name>","bytes":n}`. Written as `user` into `~/Desktop` by default, or the specified path (creating parent folders), through a hidden `.part` file, renamed when complete; a taken name becomes `name (2).ext`, `name (3).ext`… Names with `/` or NUL, empty, `.`/`..` or over 200 bytes: 400. Over 2 GB (declared or counted while streaming): 413. An error, a client that leaves, or access that ends during the upload (lowered or removed, the account deleted or expired, its browser sessions ended or its client credential deleted) removes the `.part`. Full access; a session (same-origin) or a client credential (the CLI uses `deskyard cp` over exec instead); 404 without access, 409 stopped |
| `POST /api/environments/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/environments/{name}/mic` | `{"on":bool,"tab":id,"seq"?:n}` | 204; 400 without a valid `tab` or with a negative `seq`; 409 when the account has no stream open from that tab. Sets the mic badge of the stream opened with `?tab=<id>` (see Desktop page below); the badge goes when that stream closes. `seq` numbers a page's reports: one numbered no higher than the last applied is ignored (still 204), so a late report never undoes a newer one |
| `POST /api/environments/{name}/upgrade` (admin) | | 202 `{"job": id}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}`; `folder` is `home/<name>/` in the data folder, as its path on the engine host, and `folderExists` warns that creation will reuse it |

`Conflict` = `{"name","kind":"unknown"|"duplicate"|"misnamed","containers":[names],"message"}`: a container of this supervisor that it can't match to a definition. `unknown` claims a name with no definition, `duplicate` is several containers claiming one environment, and `misnamed` one claiming it under another container name. The supervisor neither adopts nor removes them; an administrator removes the extra containers. Conflicts come from the reconcile pass, which runs at startup, every 30 seconds and on engine `die`/`destroy`/`create` events. Other supervisors' containers on the same engine are ignored.

#### Browser terminal

`GET /api/environments/{name}/terminal?session=NAME[&create=1]` upgrades to a WebSocket using the login cookie and full access to the environment (observers get 403). `session` is required; session names match `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`. Without `create=1` the attachment only joins an existing session, so a deep link or reconnect never recreates a session someone ended; only explicit opens send `create=1`, which creates the session when it is missing. Missing login returns 401, invalid/missing Origin 403, inaccessible environment 404, missing or invalid session 400, stopped/busy/changed environment or unsupported image 409, and a revoked login or connection limit 429. Unsupported images receive an explicit upgrade instruction. Preflight with the existing environment JSON (`state`, `terminal`) before opening a socket.

Origin must contain an HTTP(S) scheme and the exact request Host, with no userinfo, path, query or fragment. TLS listeners and Secure deployments require HTTPS. Plain listeners also accept HTTPS origins so a trusted TLS proxy can preserve Host while forwarding over HTTP; forwarded headers are not trusted. The proxy must preserve the public Host and enforce its public TLS policy.

The first client text frame is `{"cols":80,"rows":24}`. Valid dimensions are 2–500 columns and 1–300 rows. The supervisor starts two owned commands as `user` in `/workspace`, with `TERM=xterm-256color`, proxy credentials stripped and activity recording disabled: non-TTY `deskyard terminal-lease SESSION ATTACHMENT_ID [join]` (`join` unless `create=1`), then TTY `deskyard terminal-attach ATTACHMENT_ID`. The 32-hex-digit attachment ID is generated by the server. The browser cannot supply argv, workdir, environment or user.

Subsequent client binary frames are raw input bytes (64 KiB maximum per frame); text frames are exactly `{"resize":[80,24]}` or `{"ack":N}`. Server text frames are `{"ready":true,"generation":"ID:STARTED_AT"}`, `{"exit":CODE}`, `{"ended":true}` or `{"error":"MESSAGE"}`; binary frames are raw output with no stream prefix. Generation combines container ID with UTC `StartedAt` in RFC3339Nano form, changing after restart or recreation. Ready follows registration of the exact tmux attachment and its first terminal output (retained for the renderer), so immediate input cannot race outer-PTY initialization. Exit ends the attachment; opening another terminal is an explicit client action. `{"ended":true}`, followed by a normal close (1000, reason `session ended`), means the session no longer exists: a join found it missing (instead of `ready`), or it was ended through `DELETE …/terminal/sessions/{session}` on this supervisor (instead of `exit`). Clients close the session's view rather than reconnecting.

Output chunks are at most 16 KiB, with at most 256 KiB awaiting acknowledgement. ACK is a positive count of consumed bytes, no larger than outstanding output; the renderer sends it only after its write callback. Unknown controls, invalid sizes or ACKs close the connection. Sixteen queued input/resize messages bound pending input (room for the burst of replies a terminal sends to tmux's queries on attach); a full queue closes the connection. A separate input pump keeps the reader available for ACKs even if the PTY blocks. Initial frame and socket writes have five-second deadlines; exec startup has a ten-second deadline. At most 16 connections per account and 32 per environment are allowed.

The non-TTY lease creates a private `_deskyard_<ID>` grouped tmux session sharing the named base session’s windows. It emits internal `prepared` and `ready` lines on its own stdout; these are never part of the browser byte stream. Docker propagates non-TTY stdin EOF, including after supervisor process death. EOF triggers bounded removal of only that random grouped session, detaching its client while the base session and other clients retain the same panes/jobs. The EOF watcher starts before creation; bounded creation completes before cleanup. Attach-only execution never recreates a canceled group. There are no additional listeners or terminal services.

The remote CLI still sends the owned `deskyard terminal-helper SESSION` request over the existing remote exec API. Only this exact three-argument TTY request is recognized and executed through the same lease/attach pair, as an explicit open that creates the session when missing; arbitrary remote commands retain existing exec behavior. A remote terminal’s stdin EOF detaches cleanly instead of injecting Ctrl-D into the shared shell. Actual Ctrl-D input bytes still go to the shell. Local/native terminal helpers keep direct tmux attachments.

Browser terminals count as viewers, preventing automatic idle memory reclamation. Logout cancels the exact browser login, identified by a random signed session ID preserved across cookie refresh; legacy signed cookies migrate with a stable identity. Open sockets expire at their original cookie expiry even if another request refreshes that cookie. Account deletion or expiry, removing or lowering the account's grant to `observe`, epoch/password changes, stop/delete/restart, external engine stop events and server shutdown cancel attachments. The shell survives attachment loss until it exits or the container stops. Terminal input/output is never recorded in activity or logs.

#### Docker daemon access

`settings.dockerAccess` is always a boolean, defaulting to false. Admin create accepts optional `dockerAccess`; PATCH accepts an optional boolean, with omission or null preserving the previous value. Enabling preflights the daemon-host `/var/run/docker.sock` as a Unix socket before the current container stops, then mounts it writable at the same path. Unsupported or missing sockets fail the accepted job. The base image must include the socket-group startup support; the CLI can be added using `examples/docker/`. Socket mode and ownership remain unchanged. Custom/rootless socket paths are not configurable.

Changing the permission recreates the container; turning it off removes the mount. The definition keeps the boolean across upgrades and rollback. Everyone who can use an enabled environment can control the shared daemon, including other containers and host folders, with host administrator capability. This permission is separate from volumes below.

#### Volume grants

`VolumeGrant` is `{"name":"photos","path":"/share/photos","readOnly":true}`, mounted at `path`; an omitted or empty `path` is `/share/<name>`, and responses always spell it out. `settings.volumes` is always an array. `POST /api/environments` accepts optional `volumes`; `PATCH /api/environments/{name}` replaces the complete list when an array is supplied, and omission or null keeps it. Each volume once, with no limit on the number of grants; an omitted `readOnly` is false. `path` is any clean absolute path except `/`, `/workspace` or anything inside it (the desktop's start-up takes over the home folder), and the system folders `/bin`, `/boot`, `/dev`, `/etc`, `/lib`, `/lib32`, `/lib64`, `/libx32`, `/opt`, `/proc`, `/run`, `/sbin`, `/sys`, `/usr`, `/var` or anything inside them; no grant's path may equal or contain another's (400 naming the rule). Every PATCH validates the whole list's shape, but only newly added grants must name an existing volume. An environment that still grants a volume since deleted reports it in `error`; a change that keeps the grant is accepted, and its job fails with that error while the environment has a container (nothing is saved), so drop the grant with a PATCH of `volumes` first. Grant changes recreate the container: running environments restart and stopped ones stay stopped. On a failed replacement the old container is restored and restarted if it was running, while the definition keeps the new grants as a revision ahead of the applied one. Volume mounts are strict binds that never create a missing folder: `volume/<name>/` in the data folder for a local volume, the host folder for a host volume. Host volume folders are checked on the engine host before a container is created or replaced, with read-only strict binds in a networkless helper (Docker API 1.44 or newer and recursively read-only mounts are required); a missing folder or a file there fails the job naming the volume and path, before anything stops. An environment with volumes sets `no-new-privileges`. Host folders (`mounts`) were replaced by host volumes: a create or PATCH with a non-empty `mounts` is refused with 400, and a supervisor whose `environments.json` still has them stops at start-up naming the environment. Removing an environment never touches its volumes. Volumes themselves are managed under "Volumes (admin)".

#### Activity

`Activity` is what CLI clients and local programs in an environment are doing:

```json
{
  "name": "alice",
  "agents": [{
    "id": "k3j9…", "name": "My laptop", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…",
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

`agents` has the same entries, in the same order, as `Environment.agents`, each with `now` (what it is doing: running commands, browser use, its port mappings, `in` and `out` as in `Environment.forwards`) and `recent` (at most 20 lines, newest first; adjacent screen actions of one kind are merged into one line with a `count`). Summaries cover the last 15 minutes; they are kept in memory only, nothing is stored, and a supervisor restart forgets them. Command texts are masked (secrets show as `•••`) and typed text is never recorded. Accounts with full access may read it, with a session or a client credential; observers get 403, others 404.

`GET /api/environments/{name}/screen-events` supports a WebSocket event feed (see Events below), with SSE retained for clients making ordinary HTTP requests. It streams the screen actions CLI clients make, for drawing them over the screen: `event: screen` with data a `ScreenEvent`, `{"agent":{"id","name","color","inside"},"t":"…","action":"click","pos":[100,120]}`, plus `button`, `dir`, `n`, `keys` and `chars` where the action has them (`pos` is `[x1,y1,x2,y2]` for `drag`; `type` carries only `chars`, the number of characters). The actions sent are `click`, `double-click`, `triple-click`, `drag`, `scroll`, `key`, `hold-key` and `type`. A comment line keeps the stream alive every 25 s. Anyone who can see the environment (observers included) may open it; the stream ends when the account loses access or the environment is removed. Events are only produced while someone is subscribed, and a slow reader misses some.

### VPN connections (admin)

The web UI uses `/_vpn` for saved connections and the environment create/Settings forms for assignment and LAN rules. The underscore route leaves `/vpn/` available for an environment named `vpn`. Credential inputs start blank; omitted edit values retain saved secrets. Shared edits preview exact affected names and submit those names plus the expected revision; conflicts refresh the preview and require confirmation again. UI job results report each affected environment separately, including partial failures, with a link to retry its start/restart.

All five routes require an administrator session; mutations also require the usual same-origin header. Regular accounts may read the sanitized `vpn` object only on environments they can access. Credential values, credential paths, runtime IDs/addresses and control tokens are never returned. Filesystem and engine failures at VPN CRUD/test/rollout boundaries and environment network-journal persistence failures (including power, create/recreate jobs and final removal) use fixed public messages; private state paths and raw causes are not job output.

`VPNInput` is `{name,provider:"nordvpn",country,username?,password?}`. Create requires both service credentials; each omitted credential on an edit/test retains its saved value. Country must be one of the returned `countries`. `VPNConnection` is `{id,name,provider,country,revision,credentialsConfigured}`. A reference is `{id,name}`; both are the environment's name.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/vpn/connections` | | 200 `{connections:[VPNConnection & {environments:[Reference]}],countries:[string]}` |
| `POST /api/vpn/connections` | `VPNInput` | 201 `VPNConnection`; 400 invalid input; 409 duplicate name |
| `PUT /api/vpn/connections/{id}` | `VPNInput` plus `expectedRevision`, `affectedEnvironments:[name]`, `confirmRestart:true` when referenced | 202 `{job,environments:[Reference]}`; 409 `{error,environments:[Reference]}` when confirmation is missing; 409 if the revision/reference preview changed or a selected environment needs recovery-record repair |
| `DELETE /api/vpn/connections/{id}` | | 204; 409 `{error,environments:[Reference]}` if any environment still selects it |
| `POST /api/vpn/test` | `VPNInput`, optional `connectionId` to retain saved omitted credentials | 202 `{job}` |

Missing selected IDs produce 400. A PUT tests its candidate in a disposable runtime before changing the saved revision or environments. A failed test leaves the saved connection unchanged. The accepted reference set is rechecked atomically with assignments/deletions after validation. Successful validation commits the revision and applies it through durable per-environment transitions; partial failures retain that selected VPN policy. Candidate tests have a two-minute execution deadline with separately bounded cleanup. Cancellation remains effective during candidate validation and waits for environment operations or reference coordination. The revision commit and disabling cancellation share one boundary: an accepted cancellation leaves the saved revision and environment application unchanged; after commit, the cancel endpoint returns 409 and recovery completes the durable rollout. Viewers disconnect only after commit. A selected environment without its recovery record rejects PUT before commit with 409 and `"VPN recovery record is missing; administrator repair required before editing connection"`; it is never reported as a successful skipped rollout.

Jobs add optional `result`: a successful test returns the public status (`state`, `reason?`, `changedAt`, `currentExitIp?`, `lastExitIp?`, `exitIpObservedAt`); a completed rollout returns `{connection:VPNConnection,environments:[{id,name,error?}]}`. The job can be `done` with per-environment errors, so inspect every result and display a retry option. Retry a failed environment with start or save/restart; its desired revision is retained. Test/validation failures use sanitized service-credential/country/connectivity guidance, never provider logs. Failed recovery retains a fixed phase in the cached `vpn.reason` and private journal (for example `consumer-stop`, `consumer-create`, or `runtime-ensure`); these are diagnostics, not new status states. A pinned image that is gone from the engine and can't be pulled again by its reference fails at `image` before anything is torn down, with a reason naming the reference and image ID; the poll leaves it, and an upgrade (or a settings change to another image) recovers it. No raw engine/provider cause, token or path appears in phase messages.

Environment create and PATCH accept `vpnConnectionId` (string or explicit `null` for no VPN) and `lanExceptions`. Omitted PATCH fields retain their values; disabling VPN also retains omitted exception rules for later reuse. Rules are `{destination,protocol:"tcp"|"udp",ports:[{start,end}]}`; `end:0` means a single input port, output is normalized. Destinations must be supported private IPv4 addresses/CIDRs and cannot overlap mandatory reserved/environment networks. Exceptions intentionally permit only the selected direct LAN traffic.

`Environment.settings` adds `vpnConnectionId:string|null` and `lanExceptions:[]`. `Environment.vpn` is `{connectionId?,name?,provider?,country?,desiredRevision,appliedRevision,state,reason?,changedAt,currentExitIp?,lastExitIp?,exitIpObservedAt,idle}`. States are `disabled`, `stopped`, `connecting`, `connected`, `reconnecting`, `blocked`; only connected reports a current exit IP. An unknown observation uses the zero RFC3339 timestamp (`0001-01-01T00:00:00Z`). `idle:true` with `stopped` means the gateway is kept running and may consume a provider connection. A failed/interrupted transition stays visible by name even while the environment's container is absent. This includes explicit No VPN: `blocked` with no `connectionId` and a safe `reason` means the selected network transition is unfinished, not that a VPN connection is required. Poll retries pending transitions/removals and desired-power mismatches; restoring consumer power retains a healthy unchanged gateway. Health passes start centrally on a five-second interval and owned engine events; list/get/events only read cached observations. Eight shared observation/repair slots skip records when saturated, and repairs can last up to three minutes. There is no bounded fairness or fleet-wide five-second freshness guarantee, and skipped public connected/current-IP observations have no independent expiry. Scheduling/freshness improvements are deferred beyond the validated two-environment scope. Tunnel health never changes an environment's power by itself. Caddy targets the gateway endpoint, while exec/files/stats/activity still target the environment's container.

### Jobs

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"environment"?:name,"result"?:object}`. The stream sends every update in order, starting with the oldest the job keeps (the last 2,000), and ends after `done` or `failed`. The account that started a job, admins, and (for an environment's job) anyone with full access to that environment may follow it; others, observers included, get 404.

`POST /api/jobs/{id}/cancel` cancels a running VPN candidate test/validation (its owner or an admin): 204, and the job then fails with error `"cancelled"`. 409 for a job that has finished or can't be cancelled (VPN validation can; a committed VPN rollout completes its durable transition); 404 as above.

### Events

`GET /api/events` supports a same-origin, session-authenticated WebSocket upgrade for the environments the account can see. Each text message is `{"type":"environment","data":{…}}`, using the event names and payloads below. `/api/environments/{name}/screen-events` uses the same envelope with type `screen`. The server sends WebSocket pings every 25 seconds of inactivity; application messages from clients are refused. The UI reconnects with exponential backoff up to ten seconds and refreshes its list after reconnection. These permanent feeds use WebSockets so multiple viewer tabs do not exhaust the browser’s HTTP/1 fetch connections.

Ordinary HTTP requests retain the SSE format below for existing clients; job feeds remain SSE because they end when the job finishes.

A queue overflow or failed authoritative snapshot closes the stream so the client reconnects and obtains current state. Queued lifecycle/removal notifications resolve current environment state when sent; they do not replay stale container snapshots.


- `event: environment` — an `Environment` that appeared or changed (including one being created)
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running environment
- `event: viewers` — `{"name","viewers":[usernames],"observers":[usernames],"mics":[usernames],"forwards":[…],"agents":[…]}` (as in `Environment`; `forwards` and `agents` are `[]` for observers), when someone starts or stops viewing, turns a mic on or off, or a mapping opens or closes (sent at most every 250 ms per environment; the last state always follows), and at once when the clients present change (one arrives or leaves, or turns active or idle)
- `event: activity` — an `Activity` (as from `GET /api/environments/{name}/activity`), when what CLI clients do in the environment changes (sent at most once a second per environment; the last state always follows)

### Accounts (admin)

`Account` = `{"username","kind":"password"|"token","admin":bool,"description","grants":[Grant],"sharedBy":Sharer|null,"expires"|null,"linkPending":bool,"created","lastLogin"|null}`. `kind` is `token` for accounts without a password; `description` is `""` when there is none; `grants` are the account's grants of defined environments, sorted by environment; `sharedBy` is set on shares only; `linkPending` is true while the account has an unused login link; `lastLogin` is the last password sign-in or link redemption.

An account past its `expires` is treated as deleted at once (its cookies and credentials get 401, its link 410) and is removed, like a deletion, within 15 seconds. Admins with a password never expire (see the last-admin guard below), so the removal never meets the last-admin guard and one expired account can't hold up the others.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/accounts` | | `[Account]` sorted by username, shares included (non-admins get `[{"username"}]` of the accounts that aren't shares and of their own shares, for avatars; a share gets only its own) |
| `POST /api/accounts` | `{"username","password"?,"description"?,"admin"?:bool,"grants"?:[Grant],"expires"?:time\|null}` | 201 `Account`. Without `password` (or with `""`) it is a token account, which signs in through a login link (`POST /api/accounts/{username}/link`); token accounts may be admins. 400 for an invalid username, 400 `no environment <name>` for a grant of an undefined environment, 400 for a `level` other than `full` or `observe` or an environment listed twice, 400 `the expiry must be in the future`, 400 `admins with a password can't expire` for an `expires` on an admin with a password, 400 for a description over 200 characters; 409 `an account with that name already exists` |
| `PATCH /api/accounts/{username}` | `{"password"?,"admin"?:bool,"description"?,"grants"?:[Grant],"expires"?:time\|null}` | `Account`. Omitted fields keep their value; `expires: null` removes the expiry; `grants` replaces the whole list. A password or admin change ends the account's browser sessions (bumps its session epoch; one's own session is re-issued); any change closes the connections the new access doesn't allow, and a lowered or removed grant ends only those. When saving fails (500) a lowered or removed grant, or a removed admin role, holds anyway, as for `PUT /api/environments/{name}/access`. 400 `the password can't be empty`; 400 `token accounts have no password` when setting a password on a token account; 400 `a share's access is set when it is shared` for `admin` or `grants` on a share; 400 `admins with a password can't expire` when the account would end up an admin with a password and an expiry (setting `expires` on one, or making an expiring password account an admin); other 400s as for create; 404 `no such account`; 409 when it would leave no admin with a password |
| `DELETE /api/accounts/{username}` | | 204; also deletes its login link and credentials, and the shares it made (with theirs), closing all their live connections; 404 `no such account`; 409 when it would leave no admin with a password; 500 when the deletion can't be saved, in which case the account is held deleted in memory (its live connections close and its cookies, link, credentials and password are refused) until the deletion is saved |
| `POST /api/accounts/{username}/link` | | 200 `{"link":"https://host/link#dyl_…"}`: a new login link for a token account (shares included), replacing its unused one; 400 `password accounts sign in with their password`; 404 `no such account` |
| `GET /api/accounts/{username}/credentials` | | `[Credential]` of that account, newest first (`current` is true only for the requester's own credential); 404 `no such account` |
| `DELETE /api/accounts/{username}/credentials/{id}` | | 204; deletes it and closes its connections; 404 `no such credential` for an unknown id or another account's; a 500 holds the deletion in memory as for `DELETE /api/me/credentials/{id}` |

The last-admin guard counts only admins with a password, so the instance always keeps a password sign-in: deleting or demoting the last of them, or removing its password, is refused with 409 `the last admin with a password can't be deleted, made a regular account or lose the password`. Admins with a password can't have an expiry (400 above), so none of them is ever removed by expiry.

### Login links

A login link is a single-use way into an account, for token accounts (including shares): `<base>/link#dyl_<id>_<secret>`, where `<base>` is the scheme and host the request that made it came to, `id` 8 hex characters and `secret` 32 random bytes in unpadded base64url. The **code** is the part after `#`, `dyl_<id>_<secret>`; browsers never send the fragment, so fetching the URL (a link preview, a scanner) gets the UI's static page and consumes nothing. The `/link` page reads the code from the fragment and calls the routes below.

- An account has at most one unused link: issuing one (a share's first link, `POST /api/shares/{username}/link`, `POST /api/accounts/{username}/link`) deletes the previous unused one, which then answers 410.
- Unused links never expire and survive supervisor restarts. Deleting, recreating or expiring the account ends its link.
- Redemption is atomic: the link is deleted and that deletion saved before the cookie or credential is issued. Of concurrent redemptions exactly one succeeds; the others get 410. A failed save answers 500 and keeps the link.

Neither route needs a session or a client credential. Both are throttled per client IP, counting failures: 429 `{"error":"too many attempts: wait a few seconds"}`.

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/links/inspect` | `{"link":"dyl_…"}` (the code) | 200 `LinkInfo`, without side effects; 400 bad request; 410 `this link was already used or replaced` for a used, replaced, unknown or malformed code, or one whose account was deleted, recreated or expired |
| `POST /api/links/redeem` | `{"link":"dyl_…","client":"browser"\|"cli","name"?}` | `client: "browser"`: requires a same-origin `Origin` (403 otherwise); 200 `{"username","next"}` and sets the session cookie, replacing any session of another account in this browser (that browser login ends as on logout, closing its terminals); `next` is `/<environment>/` when the account is not an admin and has exactly one grant, else `/`. `client: "cli"`: no `Origin` needed; 200 `{"credential":"dyc_…","username","environments":[{"name","level":Level}]}`, where `environments` are the account's grants of defined environments (every environment at `full` for an admin), sorted by name, and `name` (optional, at most 100 characters, default `deskyard CLI`) names the new credential. An account that is neither admin nor has any `full` grant gets 403 `observe-only access is for the browser` and the link is kept. 400 for a `client` other than `browser` or `cli` or a longer `name`; 410 as for inspect; 500 when the redemption can't be saved (the link is kept) |

`LinkInfo` = `{"username","description","grants":[Grant],"sharedBy":Sharer|null,"expires"|null}`, for "Sign in as *sanne* to Deskyard: observe *alice*, shared by *zef*".

### Sharing

**Share** on an environment makes a token account with one grant of that environment (`full` or `observe`), `sharedBy` set to the account sharing it and an optional expiry, and returns its first login link. Admins may share any environment; other accounts may share environments they have `full` access to, unless they are themselves shares. A share ends (is deleted with its link and credentials, closing its connections) when it is revoked, expires, or its sharer is deleted, recreated, or no longer has full access to the environment (an admin sharer keeps their shares while they remain an admin).

`Share` = `{"username","environment","level":Level,"sharedBy":Sharer,"expires"|null,"created","lastLogin"|null,"lastUsed"|null,"linkPending":bool,"connected":bool}`. `lastLogin` is the last redemption of a link; `lastUsed` is the latest of that, a request with one of its client credentials, and a request of its browser since the supervisor started; `connected` is true while the share has a desktop stream, browser terminal or CLI connection open on the environment.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/environments/{name}/shares` | | `[Share]` of the environment, sorted by username; full access (observers 403, others 404) |
| `POST /api/environments/{name}/shares` | `{"username","level":Level,"expires"?:time\|null}` | 201 `{"share":Share,"link":"https://host/link#dyl_…"}`; the link is shown only here. 400 for an invalid username (the UI lowercases it first), a `level` other than `full` or `observe`, or `the expiry must be in the future`; 403 `shares can't share` for a share, 403 observe-only for an observer, 404 without access; 409 `an account with that name already exists` |
| `POST /api/shares/{username}/link` | | 200 `{"link":"https://host/link#dyl_…"}`, replacing the share's unused link; only its sharer (same account ID) or an admin, otherwise 403 `only the person who shared it or an admin can do that`; 404 `no such share` when the account doesn't exist or isn't a share |
| `DELETE /api/shares/{username}` | | 204; revokes the share: deletes the account, its link and credentials and closes its live desktop streams, terminals and CLI connections. Same 403 and 404 as above; a 500 holds the revocation in memory as for `DELETE /api/accounts/{username}` |

All four take a browser session or a client credential.

### CLI login

`deskyard login URL` is a device-authorisation flow (after RFC 8628): the CLI starts a request, a signed-in browser approves it, and the CLI's poll receives a client credential for the approving account. Pending requests live in memory only: they expire 10 minutes after they start and a supervisor restart drops them. The approval page is `/cli-login#<id>` in the UI.

`Code` is 8 characters from `BCDFGHJKLMNPQRSTVWXZ`, shown as `XXXX-XXXX`; `pollSecret` is a random secret known only to the CLI.

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/cli-login` | `{"client"}`, the client's name such as `deskyard CLI on zef-mbp` (1–100 characters) | 200 `{"id","code","approveURL":"https://host/cli-login#<id>","pollSecret","interval":2,"expiresIn":600}` (`interval` and `expiresIn` in seconds). No session or `Origin` needed; 400 for a missing or longer `client`; 429 throttled per IP (starts count as attempts: 5, then one per 10 seconds); 429 `too many logins are pending: try again in a few minutes` while 1000 requests are pending |
| `GET /api/cli-login/{id}` | | 200 `{"client","ip","code","created"}`, `ip` being the address that started the request; 404 `no such login request` when unknown, expired or already approved or denied |
| `POST /api/cli-login/{id}/approve` | | 204; the request will be answered with a credential for the signed-in account. Needs a browser session and a same-origin `Origin` (a client credential gets 403 `approve in the browser`); 403 `observe-only access is for the browser` for an account that is neither admin nor has any `full` grant; 404 as above |
| `POST /api/cli-login/{id}/deny` | | 204; same access and errors as approve, without the observe check |
| `POST /api/cli-login/{id}/token` | `{"pollSecret"}` | 202 `{"status":"pending"}` until decided; 200 `{"credential":"dyc_…","username","environments":[{"name","level":Level}]}` once after approval (the credential is created now, named by `client`, after checking the approving account still exists with the same ID and may still sign in a CLI; `environments` as for link redemption); 403 `observe-only access is for the browser` once when the approving account has since lost every `full` grant (as approving would refuse it); 403 `{"error":"denied"}` once after a denial; 410 `this login request expired or was already used; run deskyard login again` when unknown, expired, already answered, its account is gone, or `pollSecret` is wrong (a wrong secret leaves the request pending for the right one); 500 when the credential can't be saved, which keeps the approval for the next poll (the CLI polls on). No session or `Origin` needed; 429 throttled per IP. Poll no faster than `interval` |

An approved or denied request is deleted when its answer is taken. For an account with both `full` and `observe` grants the credential is issued anyway and each request is checked against the environment's level, so CLI commands on an observed environment get 403.

### Volumes (admin)

`Volume` = `{"name","created","kind":"local"|"host","hostPath"?,"folder","environments":[{"name","readOnly"}]}`. `folder` is `volume/<name>/` in the data folder for a local volume, as its path on the engine host, and `hostPath`, a folder on the engine host, for a host volume; `environments` lists the environments whose definitions grant the volume, running or not, sorted by name.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/volumes` | | `{"volumes":[Volume]}`, sorted by name |
| `POST /api/volumes` | `{"name","hostPath"?}` | 201 `{"volume":Volume,"adopted":bool}`; 400 for an invalid or reserved name, 409 for one in use, or when `volume/<name>` in the data folder is something other than a folder. Without `hostPath`, a local volume: a new folder is owned by UID/GID 1000 with mode 0755; an existing `volume/<name>/` (such as one kept by a delete) is adopted untouched, with `adopted: true`. With `hostPath`, a host volume of that existing engine-host folder: 400 for a path that isn't clean and absolute, that is the data folder or inside its `home`, `volume`, `secret`, `state` or `cache` folders (as `/data/...` or by its host path), or that the engine-host check finds missing or not a folder. Deskyard never creates, chowns, chmods or deletes a host volume's folder |
| `DELETE /api/volumes/{name}?deleteFiles=true\|false` | | 204; 404 for an unknown volume; 409 `{"error":"photos is used by alice, bob","environments":[{"id","name"}]}` while any definition grants it; 409 `{"error"}` while a container of an environment still mounts it, because a grant's removal hasn't been applied yet (it is already gone from the definition: wait, or start or restart the environment to apply it). The folder is kept unless `deleteFiles=true`; a failure deleting the files is a 500, with the volume already deleted. `deleteFiles=true` on a host volume is a 409 (its folder is never deleted). 409 too while a backup or restore of the volume runs |
| `GET /api/volumes/{name}/usage` | | 200 `{"bytes","complete","diskFree"?,"diskSize"?}`: the size of the volume's files, walked on each request (`complete` is false when some folders couldn't be read, so `bytes` is a lower bound), and the free and total bytes of the filesystem holding it, omitted when they can't be read (a host volume is measured only when the supervisor can read its path itself; otherwise `bytes` is 0 and `complete` false); 404 for an unknown volume |

Volume names follow the environment name rules (2–32 characters) and can't be changed. Volumes are recorded in `volumes.json` in the data folder; grants are recorded only in environment definitions.

### Storage remotes (admin)

A storage remote is a curated rclone connection; it knows nothing of restic. Backup repositories (below) are on remotes; sync and mounts may use them later.

`Remote` = `{"name","type":"s3"|"b2"|"sftp"|"webdav","created","root"?,` plus the fields of its type: S3 `"provider":"AWS"|"Cloudflare"|"Minio"|"Wasabi"|"Other","endpoint"?,"region"?,"accessKeyId"`; B2 `"keyId"`; SFTP `"host","port"?,"user","hostKey"?` (a `known_hosts` line); WebDAV `"url","vendor"?:"nextcloud"|"owncloud"|"other","user"?` `}`, and in responses `"secretsSet":[names]` and `"repositories":[names on it]`. `root` is the path users of the remote address folders below: for S3 and B2 it starts with the bucket. `Secrets` = `{"secretAccessKey"?,"applicationKey"?,"password"?,"privateKey"?}` (S3, B2, SFTP or WebDAV password, SFTP PEM key): write-only, never returned, kept on an update when left out. Names follow the environment name rules and can't change; neither can the type.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/remotes` | | `{"remotes":[Remote],"s3Providers":[…],"webdavVendors":[…]}` |
| `POST /api/remotes` | `{"remote":Remote,"secrets":Secrets}` | 201 `Remote` once a connection test (listing the root with rclone, bounded to about a minute) passed; 400 with rclone's error when it fails or the settings are invalid, 409 for a name in use |
| `POST /api/remotes/test` | `{"remote","secrets"}` | 204 when an unsaved remote connects; 400 with the error |
| `GET /api/remotes/{name}` | | `Remote` |
| `PUT /api/remotes/{name}` | `{"remote","secrets"}` | 200 `Remote` after a successful test; 409 `{"error","code":"busy"}` while a backup or restore uses it |
| `POST /api/remotes/{name}/test` | `{"remote","secrets"}` | 204 when the saved remote, as edited, connects |
| `DELETE /api/remotes/{name}` | | 204; 409 `{"code":"inUse"}` while a backup repository is on it. Nothing on the remote is touched |

Public settings are kept in `storage-remotes.json`; secrets in `secret/remotes/<name>/secrets.json`, with a generated `rclone.conf` beside them (passwords obscured as rclone wants). rclone runs only in short-lived helper containers that mount that folder read-only; secrets never go in a helper's environment or arguments.

### Backups (admin)

A backup repository is a restic repository in a folder on a remote, with its own recovery key (restic's password). Each environment (its definition and home folder) and each volume can have a backup policy. Snapshots are tagged `deskyard`, `environment:<name>` or `volume:<name>`, and `scheduled`, `manual` or `pre-restore`; their restic host is `deskyard-` and the first 12 characters of the supervisor's instance ID, so several Deskyards can share a repository.

`Repository` = `{"name","remote","folder","created"}`. `Policy` = `{"item":"volume:photos","repository","enabled","schedule":{"every":"hourly"|"daily"|"weekly","at":"HH:MM","weekday":0-6},"retention":{"last","hourly","daily","weekly","monthly","yearly"},"updated"}` (local time; hourly uses the minutes of `at`; weekday 0 is Sunday; at least one count is above 0). `Item` = `{"item","policy":Policy|null,"state":{"lastAttempt","lastSuccess","snapshot"?,"error"?},"activity":""|"backing up"|"restoring"|"restoring files","nextRun"|null}`. `Snapshot` = restic's `{"id","short_id","time","hostname","tags","paths","summary"?:{"data_added","total_bytes_processed"}}` plus `"repository"` and `"reason"`.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/backups[?catalog=1]` | | `{"repositories":[Repository],"items":[Item] (those with a policy),"host"}`; with `catalog=1` also `"catalog":[{"repository","host","item","snapshots","latest","latestId","local"}]` (every item in every repository, by host; `local` is this Deskyard's snapshots of an item it has) and `"catalogErrors":{repository: error}` |
| `POST /api/backups/repositories` | `{"name","remote","folder"?,"recoveryKey"?}` | 201 `{"repository","recoveryKey"?}`. Without a key: an empty folder (default `deskyard-backups`) gets a new repository and a generated key, returned this once; 409 `{"code":"needsKey"}` when a repository is already there. With a key: connects the repository there, 409 `{"code":"wrongKey"}` when the key doesn't open it, 400 when there is none |
| `DELETE /api/backups/repositories/{name}` | | 204; 409 `{"code":"inUse"}` while a policy uses it. Forgets the repository and its local key; its snapshots stay on the remote |
| `GET /api/backups/repositories/{name}/recovery-key` | | `{"recoveryKey"}` |
| `GET /api/backups/{kind}/{name}` | | `Item`; `kind` is `environment` or `volume` |
| `PUT /api/backups/{kind}/{name}` | `{"repository","enabled","schedule","retention"}` | 200 `Item`; 404 for an item that doesn't exist |
| `DELETE /api/backups/{kind}/{name}` | | 204: the policy is gone; snapshots stay |
| `POST /api/backups/{kind}/{name}/run` | | 202 `{"job"}`: backs up now, forgets snapshots beyond the retention and prunes the repository at most weekly; 409 `{"code":"busy"}` while another backup or restore holds the item |
| `GET /api/backups/{kind}/{name}/snapshots` | | `{"snapshots":[Snapshot]}`: this Deskyard's, from every repository, newest first (listings are cached for 30 seconds and after each operation) |
| `GET /api/backups/{kind}/{name}/snapshots/{id}/files?path=&repository=` | | `{"entries":[{"name","path","type","size","mtime"}]}`: one folder of the snapshot, paths relative to the item's root (the home folder for an environment); 400 for an absolute path or one with `..` |
| `GET /api/backups/{kind}/{name}/snapshots/{id}/download?path=&dir=1&repository=` | | the file, or with `dir=1` the folder as a zip (the whole item with an empty path) |
| `POST /api/backups/{kind}/{name}/restore` | `{"snapshot","repository"?,"mode":"here"|"new"|"folder","newName"?,"newImage"?,"backupFirst"?,"paths"?}` | 202 `{"job"}` whose result is `{"created"?,"dropped"?,"folder"?}` |

`repository` defaults to the item's policy's; a snapshot from another machine or of a removed item names it. Restore modes:
- `here` replaces the item's files with the snapshot's (`restic restore --delete` into the existing folder). The environment, or every running environment granting the volume, is stopped first and started again afterwards; if the restore fails they stay stopped and the job says so. `backupFirst` takes a `pre-restore` snapshot just before. An environment's settings don't change.
- `new` restores into a new environment or volume `newName`, which must not exist, nor its folder. A new environment takes the saved image (or `newImage`), memory and CPU limits and the grants of volumes that exist here; the VPN, LAN exceptions, Docker access and other grants are dropped and listed in `dropped`. It is created and left stopped.
- `folder` restores `paths` (relative to the item's root) into a new folder `Restored <YYYY-MM-DD HHMM>` (numbered when taken) inside the item, without touching other files or stopping anything.

While a backup or restore holds an item, removing the environment, deleting the volume or another backup or restore of it is refused with 409. Repositories and policies are kept in `backups.json`, run state in `state/backups.json`, recovery keys in `secret/backups/<repository>/restic-password` and restic's cache in `cache/restic/`.

### Images (admin)

`CatalogImage` = `{"ref","title","version","description","environments":[names],"behind":[names],"checked"|null,"installed":bool,"official":bool,"size"?,"built"?}`; `size` (bytes on disk) and `built` (the image's creation time) are present once the image is downloaded

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New environment dialog); `environments` and `behind` name only the environments the account can see |
| `POST /api/images/install` | `{"ref"}` | 202 `{"job"}` (uses a known catalog image locally, pulling only when missing, and checks the contract) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (uses a local image or pulls a missing reference, then checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 400 for built-in official refs; 409 while any definition uses it, including stopped environments without a container |
| `POST /api/images/check` | | 202 `{"job"}` (pull installed non-custom refs, recompute updates, delete unused old desktop images; job messages include image index/reference, Docker status and transferred MB, with current-image progress when totals are known) |
| `POST /api/images/upgrade` | `{"ref"}` | 202 `{"job"}` (upgrades every environment behind on it, then deletes unused old desktop images) |

`GET /api/images` merges saved entries with the built-in XFCE choice without pulling. `installed` means the reference exists locally; `official` identifies built-in refs; retired KDE and MATE references remain only where saved. Static titles/descriptions are supplied before download. Saved order and the configured default are preserved. Environment creation can pull available images on first use; successful use records the resolved local version. An unavailable registry or failed pull is reported through the job, leaving the choice available for retry.

Adding or installing an image validates the Deskyard contract even when the reference is already present locally. Local references, including `deskyard-custom/…`, can be added without contacting a registry. Missing references are pulled through the normal job flow. An incompatible local image fails validation instead of being replaced by a pull.

Unused old desktop images are untagged images labelled `deskyard.kind=desktop` that this supervisor's catalog replaced (a newer pull or build of the same reference became its newest version), that no container on the engine (of any state or supervisor) uses and that no environment definition or catalog entry pins. Untagged versions this catalog never had as a reference's newest are left alone (another supervisor on the same engine may still remove a version its own catalog replaced; an environment pinned to it then pulls its reference again). Removal never forces, and failures are only logged; the next check tries again.

`DELETE /api/images?ref=…` removes only the catalog entry. It does not delete Docker tags, images, legacy recipe JSON or source folders. Existing legacy locally built images remain usable and keep their exclusion from registry update checks. The daily update check (and `POST /api/images/check`) also skips missing images.

Image authoring is removed: `/api/recipes`, its template/edit/rebuild/delete subroutes, `/api/environments/{name}/image-scan` and `/api/environments/{name}/image` return 404. Build images separately with Docker and add their resulting references to the catalog. Existing image labels and home-template runtime support remain compatible; the supervisor does not migrate old authoring state.


## Desktop

- `/link` and `/cli-login` serve the UI's `index.html` without a session (`/account`, the signed-in account's page, needs one): the link sign-in page (the code is in the fragment, see Login links) and the CLI login approval page (`/cli-login#<id>`, which sends the browser to `/login?next=…` when the API answers 401).
- `/<name>/` serves the UI's desktop page (the SPA's `index.html`); the page frames `/<name>/screen/`, Selkies' web client, and drives it through `window.deskyardScreen` (new images use `web/src/selkies/adapter.ts`; pre-contract images use the legacy `internal/server/screen_additions.js`).
- `/<name>/screen/…` are Selkies' web client files, extracted from the environment's image (once per image ID, never from the running container). `index.html` and the selected adapter are served `no-store`; the other files `no-cache`. Any other spelling of the page (`/<name>/screen/index.html/`, `/<name>/screen/./index.html`, …) redirects to `/<name>/screen/`. The image hook marker selects the typed v1 adapter (`deskyard-screen.js`, inserted as a module before the client modules) or the legacy `deskyard.js` bridge. The v1 client exposes explicit transport/input/media/focus hooks and disables raw pointer updates directly; the legacy bridge intercepts the equivalent APIs. Unknown advertised hook versions return an actionable upgrade error. Both adapters
  - hide `pointerrawupdate` from the client (Chrome reports its coordinates relative to the top-level page inside an iframe, so drags landed off by our bar);
  - never send `STOP_VIDEO` (a background tab mustn't stop the desktop's stream for everyone);
  - remove Selkies' own tab id (`sessionStorage` `selkies_tab_id`) before Selkies reads it, so every load is its own Selkies client (a duplicated browser tab copies `sessionStorage`, and Selkies lets a page with the owner's tab id take the display over);
  - add `?tab=<id>` to the stream URL, an id made afresh on every load of the client (so a duplicated browser tab gets its own); the desktop page reads it as `frame.contentWindow.deskyardScreen.tab`;
  - expose `window.deskyardScreen`: `tab`, `post(msg)` (a message to Selkies' client), `on(type, fn)` (messages of that type from it; returns an unsubscribe function; `"deskyard.state"` gets each new state), `state()` (`"connecting" | "connected" | "disconnected"`), `video()` (the `<video>` or `<canvas>` showing the picture, or null).
- `/<name>/screen/api/websockets` (also with a trailing `/`) is the desktop's stream, proxied to the environment's Selkies with its proxy token and `Origin: http://supervisor.deskyard.invalid`, without the session cookie or the query. The WebSocket needs a session (401), a grant of the environment at either level (404) and a same-origin `Origin` (403); 409 when the environment isn't running; 400 for a bad `tab` (letters, digits, `-`, `_`, at most 64). An open stream counts as viewing (and as observing at `observe` level). For an observer the supervisor forwards only the client messages needed to start and keep receiving video (`START_VIDEO`, at most one every 2 s, `REQUEST_KEYFRAME` and `LOST_FRAME`), and WebSocket control frames; keyboard, mouse, clipboard, resize, settings, `START_AUDIO`/`STOP_AUDIO` (which Selkies applies to the whole session) and every other client message, and all binary client messages, are dropped before they reach Selkies. On the way back it drops Selkies' clipboard messages (`clipboard…`, which Selkies sends every controller) and printed-document announcements. An observer hears the desktop's sound whenever the session has it on, but can't turn it on or off for others; its sound toggle mutes only its own page. The UI hides input controls for observers and shows "Observing". A plain `GET` is the client's reconnect probe: 204 when Selkies answers, else 503 (never 409, which Selkies' client takes as "switch to WebRTC").
- Every other `/<name>/screen/api/…` is 404: Selkies' upload, files, screenshots, recordings, status and the rest are never served. Selkies refuses (close 4029) a stream arriving within 500 ms of the previous one from the same address, and every viewer reaches it from the desktop's Caddy, so the supervisor spaces each desktop's stream dials: a dial waits until 600 ms after Selkies answered the previous one. Viewers opening together, or reconnecting together after a restart, each wait up to 0.6 s per viewer ahead of them.
- `/<name>/dav/…` serves the environment's files over WebDAV (class 1 and 2, `golang.org/x/net/webdav` with in-memory locks per environment) when its `webdav` setting is on, whether it runs or not. The tree is the environment's own paths: `workspace/` (its home) and each local volume at its grant path (default `share/<volume>/`), read-only where the grant is, under synthesized parent folders that can't be written; host volumes and the rest of the container are not included. Only HTTP Basic auth is accepted, never the session cookie: the username and the account password, or its WebDAV password while it has one; a `cli` credential is refused. 401 with `WWW-Authenticate: Basic realm="Deskyard", charset="UTF-8"` without or with wrong credentials (failures count towards the login throttle, 429); 404 when the setting is off, the environment doesn't exist, or the account isn't a password account with full use of it (observe grants, token accounts and shares). Writes to read-only or synthesized places answer 403. A PUT writes a hidden `.dav-<random>.part` beside the target and renames it over the target only when the whole body arrived (keeping an existing file's mode). A MOVE between the home and a volume, or between volumes, copies and then removes the source (what of the source can't be removed stays; the move still succeeds). A COPY or MOVE onto an existing name sets that aside and puts it back if the copy or move fails. LOCK answers 403 on the tree's own folders, mounts and read-only places; a lock lasts at most an hour without a refresh (a longer or `Infinite` Timeout is capped to `Second-3600`). `.DS_Store` and `._*` files are accepted and dropped: writes, moves and deletes succeed, nothing is stored, and they are reported missing. Every GET and HEAD carries `Content-Disposition: attachment`, `Content-Security-Policy: sandbox` and `X-Content-Type-Options: nosniff`, so a file is never rendered on this origin. A supervisor running as root gives what it creates to UID 1000. Revoking the WebDAV password ends its open requests, and an upload it was making doesn't land; a remembered account password stops working when the password or session epoch changes.

## Remote access API

The routes the `deskyard` CLI uses to work in an environment: `GET /api/environments/{name}/status`, `GET /api/environments/{name}/exec`, `GET /api/environments/{name}/forward` and `/api/environments/{name}/cdp` with `/api/environments/{name}/cdp/{rest…}`. They are authenticated with a client credential only (`Authorization: Bearer dyc_…`, see Client credentials); there is no Origin check.

- A request with a session cookie and no bearer gets 401 `{"error":"use a client credential (deskyard login)"}`; neither gets 401 `sign in first`; a bad credential 401 `the credential is unknown, revoked or expired`.
- The credential's account needs full access to the environment: observers get 403 `observe-only access can't do that`, accounts without a grant 404 `no such environment`.
- `exec`, `forward` and `cdp` answer 409 `{"error":"alice is stopped; someone with access can start it"}` when the environment isn't running; `status` always answers 200 with its `state`.
- Each connection is tracked by credential and environment. It closes at once when the credential is deleted, its account is deleted, recreated or expires, its grant of the environment is removed or lowered to `observe`, or the environment stops, restarts or is upgraded.
- Activity and presence (`Environment.agents`, `Activity`) record these connections under the credential: `id` is the credential's id, `name` its name, `owner` its account's username.

### `GET /api/environments/{name}/status`

```json
{"name":"alice","state":"running","image":"ghcr.io/…","imageTitle":"Debian XFCE","imageVersion":"2026.10.22","screen":{"width":1280,"height":800},"api":1}
```

`screen` is null when the environment isn't running or its size can't be read.

### `GET /api/environments/{name}/exec` (WebSocket)

1. The client sends one **text** message: `{"argv":["bash","-lc","ls"],"cwd":"/workspace","env":{"K":"V"},"tty":false,"cols":80,"rows":24}` (`cwd`, `env`, `tty`, `cols`, `rows` optional; `cwd` defaults to `/workspace`).
2. Then **binary** messages, each starting with one stream byte: `0` stdin (client → server), `1` stdout, `2` stderr (server → client). With `tty`, all output is stream 1.
3. **Text** messages from the client: `{"resize":[cols,rows]}`, `{"eof":true}` (closes stdin).
4. The server's last message is **text** `{"exit":code}`, then it closes the WebSocket. An error before starting is text `{"error":"…"}`.

Commands run as `user` (UID 1000) with the environment's image environment (PATH includes the user's tool folders; `argv[0]` is looked up on it; `bash -lc` for a login shell is the client's choice). Details:

- No message size limit beyond 64 MB per message; send stdin in chunks.
- `cols`/`rows` are optional with `tty` (default 80×24); `{"resize"}` without `tty` is ignored.
- `{"eof"}` closes stdin; with `tty` it sends Ctrl-D. The server sends `{"exit"}` when the process ends, whether or not stdin was closed.
- A process killed by a signal exits with 128 + the signal number.
- `DESKYARD_ACTIVITY=off` is always set in the command's environment and can't be overridden by `env`: the supervisor records the command itself (see the activity API), so the in-environment `deskyard` must not record it again.
- `{"error"}` only comes instead of running the command (bad first message, the environment stopped); once output started, the last message is always `{"exit"}`.

### `GET /api/environments/{name}/forward` (WebSocket)

TCP port mappings between the client’s machine and the environment, many connections multiplexed on one WebSocket (`deskyard forward`). The supervisor runs `deskyard forward-helper` in the environment as `user` (cwd `/workspace`, no TTY) and relays frames between the WebSocket and the helper's stdio, so the environment side listens on and dials `127.0.0.1` inside the environment. A credential with full access can therefore reach any loopback port in the environment (ports from 1024 up for listening), as its exec can. Like any local process in the environment, pages in the environment's browser can reach the in-mapped ports. A credential may have 4 forward links open on an environment; a fifth gets `{"error":"…"}` and a close before the helper runs. `Environment.forwards` lists at most 64 mappings per credential (the rest still work). The CLI prints `Linked to …` and the `out` lines when the link is accepted, before the helper is known to run.

1. The client sends one **text** message: `{"forwards":[{"direction":"in"|"out","port":n}]}`, the mappings it will set up, `port` being the environment's port (1–65535; at most 256 entries). The `out` ports are listed in `Environment.forwards` while the WebSocket is open; the `in` ports once the environment listens on them.
2. Then **binary** messages, one frame each in both directions: `type u8 | stream u32 BE | payload`, at most 32 KiB of payload:

| Type | Name | Payload | Meaning |
| --- | --- | --- | --- |
| 1 | Listen | port u16 BE | client → environment: listen on 127.0.0.1:port; `stream` is the listener id. Refused below 1024 (`port below 1024`) or when taken (`port in use`). |
| 2 | Listening | | environment → client: the listen worked |
| 3 | Open | port u16 BE | a new connection on `stream`: from the client, the environment dials 127.0.0.1:port; from the environment, a connection accepted on a listener on that port |
| 4 | Data | bytes | data on a connection |
| 5 | Close | | half-close of the sender's direction; on a listener id, stop listening |
| 6 | Reset | UTF-8 text | the listen or dial failed, or the connection was aborted |
| 7 | Window | n u32 BE | the sender may receive n more bytes on this connection |

   The client picks odd stream ids, the environment even ones; an id in use can't be opened again (the environment side ends the connection). Each connection starts with a 256 KiB window in each direction. A side may have at most 64 listeners and 256 connections at once; beyond that it answers with Reset.
3. A **text** message from the server is `{"error":"…"}` (the helper couldn't start or failed), followed by the close. The server closes the WebSocket with a reason (`environment stopped`, …) when the environment side ends, and with status 1008 when the client sends a text message after the first, a frame shorter than 5 bytes or longer than 32 KiB + 5 that is still within the read limit, a frame of an unknown type, or a Listen beyond 64 listeners pending or open. A single WebSocket message over 64 KiB is closed with 1009 (message too big) by the read limit, before it is looked at.

The connection closes as described above for every remote access connection (credential deleted, access removed or lowered, environment stopped, restarted or upgraded); open connections through it are reset. The server pings every 30 s and drops a client that doesn't answer.

### `/api/environments/{name}/cdp/…` (HTTP and WebSocket)

The shared Chromium's DevTools protocol: `GET /api/environments/{name}/cdp/json/version`, `/json/list`, `PUT /json/new?url`, and WebSocket `/api/environments/{name}/cdp/devtools/…`. In `/json*` responses (including `/json/version`), `webSocketDebuggerUrl` is absolute, `ws(s)://<host>/api/environments/{name}/cdp/devtools/…`, and the `ws=` parameter in `devtoolsFrontendUrl` is `<host>/api/environments/{name}/cdp/devtools/…` (Chromium's own `ws=`/`wss=` choice). The first request may take several seconds while Chromium starts.

### Running shared terminal sessions

`GET /api/environments/{name}/terminal/sessions` requires full access to the environment (observers get 403), with a session or a client credential. Returns `{"sessions":[{"name":"main","clients":2,"command":"vim"},{"name":"terminal-1","clients":0,"command":"bash"}]}` sorted by name, or `{"sessions":[]}` when there are no live shells. Each row is one base session, including detached shells/jobs; private attachment groups are omitted. `clients` counts every client attached to the session, natively or through its private attachment groups, including this supervisor's own browser attachments (a page subtracts its own to count others). `command` is the foreground program of the session's active pane (`bash` when idle). Names use the terminal session validation rule above. The response contains no terminal content, process arguments, input or environment and has `Cache-Control: no-store`. The request also refreshes the `terminals` list of the environment JSON.

The supervisor executes only `deskyard terminal-list` as `user` in `/workspace`, without a TTY or stdin. The request has a five-second deadline and 64 KiB output limit; the helper has its own four-second deadline. Anonymous requests return 401, missing/inaccessible environments 404, stopped environments and images without `tmux-v1` 409, and helper failures or invalid/oversized data 502, using the usual `{"error":"message"}` response.

`POST /api/environments/{name}/terminal/sessions` (View permission, same-origin) opens a new session: the image's `deskyard terminal-create` starts the next free `terminal-N` detached in `/workspace`, the naming native terminal windows use. Returns 201 `{"name":"terminal-2"}`; attach to it with the terminal WebSocket. 409 when the environment isn't running or its image lacks `tmux-v1`, 502 when the helper fails.

`PATCH /api/environments/{name}/terminal/sessions/{session}` (View permission, same-origin) accepts `{"name":"build"}` and renames the public tmux session through `deskyard terminal-rename SESSION NAME`. Returns 200 `{"name":"build"}`, 400 for invalid names, 404 for a missing session, and 409 for a stopped/unsupported environment or failed rename (including a taken name or unavailable helper). Names use the same 1–64 character rule as creation. Windows, processes and grouped attachments survive. The environment's session list refreshes; old URLs no longer join. Requires an updated desktop image containing the rename helper.

`DELETE /api/environments/{name}/terminal/sessions/{session}` (View permission, same-origin) ends a session for everyone: `deskyard terminal-kill SESSION` kills the session's windows (so no attachment group, even one joining meanwhile, keeps the shell alive), then any private attachment groups left and the session itself, ending its shell and every program in it; it never touches another session and exits 3 when the session doesn't exist. Returns 204 when ended, 400 for an invalid name, 404 when the session doesn't exist, 409 when the environment isn't running or its image lacks `tmux-v1`, and 502 when the helper fails. This supervisor's browser attachments to the session receive `{"ended":true}` and a normal close; native and remote CLI clients see their tmux client exit.

## Replacement recovery

Settings changes and upgrades save the new definition revision first, then replace the container. Before the old container is touched, a mode 0600 record `state/recovery/<name>.json` (`{version,name,revision,parked,wasRunning}`, fsynced with its folders) notes the replacement; it holds transition state only, never settings or the proxy token. The old container is stopped and renamed `deskyard-<name>.parked`, not removed, until the new one is started and ready (when it should run). If the new container fails, it is removed, the parked one is renamed back and restarted if it was running, and the environment reports the failure in `error` with `revision` ahead of `appliedRevision`. A failed apply is retried only on start, restart or another edit, never by the reconcile loop. Interrupted replacements are finished or reverted at supervisor startup, after network reconnection; a record whose recovery fails is kept and the environment's lifecycle operations return 409 until startup can finish it. VPN environments never fall back to direct networking.
