# Supervisor HTTP API

The contract between the supervisor (Go, `internal/server`), its web UI (`web/`) and the `deskyard` command (`cmd/deskyard`). JSON everywhere; times are RFC 3339 strings; sizes in bytes; errors are `{"error": "message"}` with a 4xx/5xx status.

Remote control is available to people through the `deskyard` CLI, and to scripts and optional integrations through the same API. Create access tokens on an environment’s **Remote access** tab. Existing `/api/agent/v1/` routes and `agents` fields retain their wire names for compatibility. The former `/api/desktops/…` routes and `desktop`/`desktops`/`desktopId` fields were replaced by `/api/environments/{name}/…` and `environment`/`environments` names without aliases; the old routes answer 404.

## Sessions (web UI)

The UI authenticates with the `deskyard_session` cookie (HttpOnly, SameSite=Strict). Every `/api/*` route except `POST /api/login` and `POST /api/logout` answers 401 without a session. State-changing requests (POST, PUT, PATCH, DELETE) must carry an `Origin` matching the supervisor's own (browsers' `fetch` does this); otherwise 403.

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/login` | `{"username","password"}` | 200 `Me`, sets the cookie; 401 wrong; 429 throttled |
| `POST /api/logout` | | 204, clears the cookie and closes this browser login’s terminals; requires same-origin Origin, including when already signed out |
| `GET /api/me` | | `Me` = `{"username","admin":bool}` |
| `POST /api/me/password` | `{"current","new"}` | 204 (signs out everywhere else; the response re-issues this session) |
| `GET /api/host` | | `{"cpus":n,"memBytes":n,"running":n,"total":n,"memUsed":n}` (counts over environments the account can see) |

`GET /api/host` also includes `memoryReclaim: {"enabled":bool,"reason"?:string}`, the cached startup capability result. The probe checks the full helper path using a disposable container, without reclaiming any existing environment. Failures disable the optional action and supply a reason; capability detection is retried on supervisor restart. A disabled action returns 501 immediately. Per-environment checks still run on enabled hosts and do not change the cached host status.

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
  "memoryReclaim": true, "terminal": true,
  "terminals": [{"name": "main", "clients": 2, "command": "vim"}, {"name": "terminal-1", "clients": 0, "command": "bash"}],
  "settings": {"memoryBytes": 0, "nanoCpus": 0, "dockerAccess": false, "vpnConnectionId": null, "lanExceptions": [], "mounts": [], "volumes": []},
  "vpn": {"state":"disabled","changedAt":"0001-01-01T00:00:00Z","exitIpObservedAt":"0001-01-01T00:00:00Z","desiredRevision":0,"appliedRevision":0,"idle":false},
  "access": ["anna"],
  "viewers": ["zef", "anna"],
  "mics": ["anna"],
  "forwards": [{"token": "My laptop", "direction": "in", "port": 3000}, {"token": "My laptop", "direction": "out", "port": 5173}],
  "agents": [{"id": "k3j9…", "name": "My laptop", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…"}],
  "tokens": 2,
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`terminal` reports image support (`deskyard.terminal=tmux-v1`), independent of running state. Custom images may opt in only when they supply the shared helper and tmux runtime.

`terminals` are the environment's shared terminal sessions, as listed by `GET /api/environments/{name}/terminal/sessions` (below): never null, and empty unless the environment is running with a `tmux-v1` image. The supervisor lists every such running environment's sessions every 10 seconds, and right after a session is created, ended, attached or detached through it; a change of the list (a session, its `clients` or its `command`) is sent as an `environment` event. Sessions of a previous running lifetime (before a stop or restart) are never shown.

`state` is observed; `power` is the desired state saved in the environment's definition (`environments.json`), set by start, stop and restart. `revision` counts the definition's container-affecting changes (image, limits, network, mounts, volumes, Docker access); `appliedRevision` is the revision the current container was built from, 0 when the environment has no container. `revision` ahead of `appliedRevision` is a change not yet applied: a stopped environment applies it on start, and a failed apply keeps the old container and reports why in `error`. `error` also reports a missing home folder, a granted volume that doesn't exist or whose folder is missing, or a VPN connection that no longer exists; `""` when there is none. `conflict` is set when a container with the environment's name isn't matched to it (another supervisor's, or several claiming it); lifecycle operations are then refused with 409 until an administrator resolves it. A stopped environment may have no container; it is created on start.

`memoryReclaim` is the saved preference in the definition, independent of the host capability flag; changing it never bumps `revision` or recreates the container. On supported hosts the supervisor checks every 30 seconds for running environments with no viewers for 15 continuous minutes, making one 128 MiB attempt per absence (including failed/partial attempts). Viewer joins, even between checks, reset the timer; supervisor/container restarts and re-enabling the preference start fresh. Active tracked CLI clients (recent actions, running commands, browser use or forwards), lifecycle operations and unknown/stale inside status delay the attempt without consuming it. Automatic work uses the same lifecycle reservation as the manual action; it rechecks viewers, identity, policy and activity after reservation. Apps keep running and a viewer arriving during reclamation can reconnect normally. Idle status must come from a current environment's activity service (`commandsRunning:false` heartbeat less than 45 seconds old); missing/legacy/disconnected activity skips automatic work. Unreported shell/background work cannot be detected; opt out for such workloads.

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the environment's CPU limit, or to all host CPUs without one (0–100). `access` lists regular accounts only (admins can use everything) and is only filled for admins. `viewers` are the accounts with a desktop stream or browser terminal open (each once, sorted); `mics` those of them with a mic on, as their pages report it (see `POST /api/environments/{name}/mic`). `forwards` are the port mappings access tokens have open on the environment (`deskyard forward`): `in` maps a port of the client’s machine into the environment, `out` an environment port out to the client’s machine; `port` is always the environment's port (the client's own port numbers aren't shown). Each entry is listed once, sorted by token, direction and port; an `in` port appears once the environment is listening on it. `created` is when the definition was made and survives upgrades and settings changes. `job` is the running create, upgrade or settings job, which anyone who can see the environment may follow. `agents` are the CLI clients and local programs present in the environment (always a list, sorted by name then id): access tokens (`id` is the token's id, `owner` the account that made it) and programs running inside the environment (`inside: true`, `id` `inside:<program>`, `owner` empty). A client is present while something it started is running, a browser connection or port mapping is open, or for 5 minutes after its last action; `active` means an action in the last 10 seconds; `color` is fixed per id. An environment's page is at `/<name>/`, with its desktop as the default tab and a terminal session's tab at `/<name>/?terminal=<session>` (there is no separate terminal page); the UI's management page for it is `/environments/<name>`.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/environments` | | `{"environments":[Environment],"conflicts":[Conflict]}`: the environments the account can see, sorted by name, including stopped ones without a container. `conflicts` is always `[]` for regular accounts |
| `GET /api/environments/{name}` | | `Environment`; 404 `no such environment` if missing or not visible |
| `POST /api/environments` (admin) | `{"name","image","memoryBytes","nanoCpus","access":[usernames],"mounts"?:[HostMount],"volumes"?:[VolumeGrant],"dockerAccess"?:bool}` | 202 `{"job": id}`; the definition is saved (revision 1, power running) before the container is made and removed again, with whatever creation made, when creation fails or the desktop isn't ready within two minutes; 409 for a name in use, including one claimed by a conflicting container; 400 `no volume <name>` for a grant of a volume that doesn't exist |
| `POST /api/environments/{name}/start` \| `stop` \| `restart` | | 204; saves `power`. Start creates the container when there is none or its revision is behind; stop keeps the container |
| `PUT /api/environments/{name}/memory-policy` (admin) | `{"memoryReclaim":bool}` | 204. Saves `memoryReclaim` in the definition without a new revision and without recreating/restarting it; default true. Session and same-origin only. 400 missing/invalid boolean, 403 regular accounts with access, 404 without access, 500 persistence failure. |
| `POST /api/environments/{name}/reclaim-memory` (admin) | | 200 `{"requestedBytes","beforeBytes","afterBytes","swapBeforeBytes","swapAfterBytes","partial":bool}`. Requests 128 MiB of reclaim while apps run. The before/after values are resident cgroup bytes including file cache; swap values are logical swapped bytes, which can still use compressed RAM. `partial` means the kernel reclaimed less than requested; observed resident memory can grow again. 409 stopped or busy, 501 unsupported engine/kernel or unavailable/disabled swap, 502 helper failure. Session and same-origin only; 403 for regular accounts with access, 404 without access. |
| `DELETE /api/environments/{name}?files=1` (admin) | | 204; deletes the definition, container, network, grants and tokens; `files=1` also deletes `home/<name>/` (without it, a later environment of that name reuses the folder) |
| `PATCH /api/environments/{name}` (admin) | `{"image"?, "memoryBytes"?, "nanoCpus"?, "dockerAccess"?:bool, "mounts"?:[HostMount], "volumes"?:[VolumeGrant]}` | 202 `{"job": id}`: saves the change as a new revision, then recreates the container (a stopped environment stays stopped); 400 `no volume <name>` when a newly added grant names a volume that doesn't exist |
| `PUT /api/environments/{name}/access` (admin) | `{"usernames":[…]}` | 204 |
| `GET /api/environments/{name}/screen.png` | `?full=1` for full size | PNG, 480 px wide by default; 409 when stopped |
| `GET /api/environments/{name}/processes` | | `{"titles":[…],"rows":[[…]]}` (columns PID, USER, %CPU, RSS (KiB), ELAPSED, COMMAND) |
| `GET /api/environments/{name}/stats` | | `{"history":[{"t":"…","cpuPercent":n,"memBytes":n}], "memLimit":n}` (15 min at 5 s) |
| `GET /api/environments/{name}/folder` | | `{"path","bytes","complete"}`; `complete` is false when some directories couldn't be read, so `bytes` is a lower bound |
| `GET /api/environments/{name}/activity` | | `Activity` (below) |
| `GET /api/environments/{name}/screen-events` | | server-sent events (below) |
| `GET /api/environments/{name}/files` | `?path=` (default `/workspace`; relative paths start there) | `{"path","entries":[{"name","dir","size","modified"}],"truncated"}`, newest first, at most 2,000 entries; `path` is the resolved folder. Anyone who can see the environment; session only (an access token gets 401). 403 outside `/workspace` (symlinks and `..` are resolved inside the environment first), 404 missing, 400 not a folder, 409 stopped |
| `GET /api/environments/{name}/download` | `?path=` | Streams a file, or a folder as a zip (links inside it are left out). Same access and errors as `files`; 400 for anything that is neither a file nor a folder |
| `POST /api/environments/{name}/upload` | `?name=<file name>`, the raw file as the body (one request per file; `Content-Length` or chunked) | `{"path":"/workspace/Desktop/<final name>","bytes":n}`. Written as `user` into `~/Desktop` through a hidden `.part` file, renamed when complete; a taken name becomes `name (2).ext`, `name (3).ext`… Names with `/` or NUL, empty, `.`/`..` or over 200 bytes: 400. Over 2 GB (declared or counted while streaming): 413. An error or a client that leaves removes the `.part`. Anyone who can see the environment; session and same-origin only (an access token gets 401: CLI clients use `deskyard cp`); 404 without access, 409 stopped |
| `POST /api/environments/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/environments/{name}/mic` | `{"on":bool,"tab":id,"seq"?:n}` | 204; 400 without a valid `tab` or with a negative `seq`; 409 when the account has no stream open from that tab. Sets the mic badge of the stream opened with `?tab=<id>` (see Desktop page below); the badge goes when that stream closes. `seq` numbers a page's reports: one numbered no higher than the last applied is ignored (still 204), so a late report never undoes a newer one |
| `POST /api/environments/{name}/upgrade` (admin) | | 202 `{"job": id}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}`; `folder` is `home/<name>/`, and `folderExists` warns that creation will reuse it |

`Conflict` = `{"name","kind":"unknown"|"duplicate"|"misnamed","containers":[names],"message"}`: a container of this supervisor that it can't match to a definition. `unknown` claims a name with no definition, `duplicate` is several containers claiming one environment, and `misnamed` one claiming it under another container name. The supervisor neither adopts nor removes them; an administrator removes the extra containers. Conflicts come from the reconcile pass, which runs at startup, every 30 seconds and on engine `die`/`destroy`/`create` events. Other supervisors' containers on the same engine are ignored.

#### Browser terminal

`GET /api/environments/{name}/terminal?session=NAME[&create=1]` upgrades to a WebSocket using the login cookie and View permission. `session` is required; session names match `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`. Without `create=1` the attachment only joins an existing session, so a deep link or reconnect never recreates a session someone ended; only explicit opens send `create=1`, which creates the session when it is missing. Missing login returns 401, invalid/missing Origin 403, inaccessible environment 404, missing or invalid session 400, stopped/busy/changed environment or unsupported image 409, and a revoked login or connection limit 429. Unsupported images receive an explicit upgrade instruction. Preflight with the existing environment JSON (`state`, `terminal`) before opening a socket.

Origin must contain an HTTP(S) scheme and the exact request Host, with no userinfo, path, query or fragment. TLS listeners and Secure deployments require HTTPS. Plain listeners also accept HTTPS origins so a trusted TLS proxy can preserve Host while forwarding over HTTP; forwarded headers are not trusted. The proxy must preserve the public Host and enforce its public TLS policy.

The first client text frame is `{"cols":80,"rows":24}`. Valid dimensions are 2–500 columns and 1–300 rows. The supervisor starts two owned commands as `user` in `/workspace`, with `TERM=xterm-256color`, proxy credentials stripped and activity recording disabled: non-TTY `deskyard terminal-lease SESSION ATTACHMENT_ID [join]` (`join` unless `create=1`), then TTY `deskyard terminal-attach ATTACHMENT_ID`. The 32-hex-digit attachment ID is generated by the server. The browser cannot supply argv, workdir, environment or user.

Subsequent client binary frames are raw input bytes (64 KiB maximum per frame); text frames are exactly `{"resize":[80,24]}` or `{"ack":N}`. Server text frames are `{"ready":true,"generation":"ID:STARTED_AT"}`, `{"exit":CODE}`, `{"ended":true}` or `{"error":"MESSAGE"}`; binary frames are raw output with no stream prefix. Generation combines container ID with UTC `StartedAt` in RFC3339Nano form, changing after restart or recreation. Ready follows registration of the exact tmux attachment and its first terminal output (retained for the renderer), so immediate input cannot race outer-PTY initialization. Exit ends the attachment; opening another terminal is an explicit client action. `{"ended":true}`, followed by a normal close (1000, reason `session ended`), means the session no longer exists: a join found it missing (instead of `ready`), or it was ended through `DELETE …/terminal/sessions/{session}` on this supervisor (instead of `exit`). Clients close the session's view rather than reconnecting.

Output chunks are at most 16 KiB, with at most 256 KiB awaiting acknowledgement. ACK is a positive count of consumed bytes, no larger than outstanding output; the renderer sends it only after its write callback. Unknown controls, invalid sizes or ACKs close the connection. Sixteen queued input/resize messages bound pending input (room for the burst of replies a terminal sends to tmux's queries on attach); a full queue closes the connection. A separate input pump keeps the reader available for ACKs even if the PTY blocks. Initial frame and socket writes have five-second deadlines; exec startup has a ten-second deadline. At most 16 connections per account and 32 per environment are allowed.

The non-TTY lease creates a private `_deskyard_<ID>` grouped tmux session sharing the named base session’s windows. It emits internal `prepared` and `ready` lines on its own stdout; these are never part of the browser byte stream. Docker propagates non-TTY stdin EOF, including after supervisor process death. EOF triggers bounded removal of only that random grouped session, detaching its client while the base session and other clients retain the same panes/jobs. The EOF watcher starts before creation; bounded creation completes before cleanup. Attach-only execution never recreates a canceled group. There are no additional listeners or terminal services.

The remote CLI still sends the owned `deskyard terminal-helper SESSION` request over the existing remote exec API. Only this exact three-argument TTY request is recognized and executed through the same lease/attach pair, as an explicit open that creates the session when missing; arbitrary remote commands retain existing exec behavior. A remote terminal’s stdin EOF detaches cleanly instead of injecting Ctrl-D into the shared shell. Actual Ctrl-D input bytes still go to the shell. Local/native terminal helpers keep direct tmux attachments.

Browser terminals count as viewers, preventing automatic idle memory reclamation. Logout cancels the exact browser login, identified by a random signed session ID preserved across cookie refresh; legacy signed cookies migrate with a stable identity. Open sockets expire at their original cookie expiry even if another request refreshes that cookie. Account deletion, View revocation, epoch/password changes, stop/delete/restart, external engine stop events and server shutdown cancel attachments. The shell survives attachment loss until it exits or the container stops. Terminal input/output is never recorded in activity or logs.

#### Docker daemon access

`settings.dockerAccess` is always a boolean, defaulting to false. Admin create accepts optional `dockerAccess`; PATCH accepts an optional boolean, with omission or null preserving the previous value. Enabling preflights the daemon-host `/var/run/docker.sock` as a Unix socket before the current container stops, then mounts it writable at the same path. Unsupported or missing sockets fail the accepted job. The base image must include the socket-group startup support; the CLI can be added using `examples/docker/`. Socket mode and ownership remain unchanged. Custom/rootless socket paths are not configurable.

Changing the permission recreates the container; turning it off removes the mount. The definition keeps the boolean across upgrades and rollback. Everyone who can use an enabled environment can control the shared daemon, including other containers and host mounts, with host administrator capability. This permission is separate from folder mounts below.

#### Host folder mounts

`HostMount` is `{"source":"/absolute/engine-host/folder","target":"/mnt/photos","readOnly":true}`. `settings.mounts` is always an array (empty without mounts). `POST /api/environments` accepts optional `mounts`; `PATCH /api/environments/{name}` replaces the complete list when an array is supplied. Omission or null preserves the current list on PATCH; `[]` detaches all. The API's omitted `readOnly` defaults to false; the UI defaults new entries to true.

Only admins can configure mounts. Everyone able to use an environment can access the mounted files with that environment's permissions. Up to 16 existing source directories are allowed. Source paths must be clean absolute engine-host paths; target paths must be clean paths below `/mnt/`, with no duplicate or nested mount targets. Paths are at most 4096 bytes and may not contain NUL. Invalid configuration is 400; mount-source existence/type failures are reported by the accepted job. The helper verifies sources on the engine host before stopping/replacing a container, with read-only strict bind mounts that never create missing directories. The source need not be visible in the supervisor container. Existing host permissions still apply; no source chown/chmod is performed. Strict read-only binds require recursive read-only enforcement, including nested mounts; unsupported engines/kernels fail preflight rather than silently allowing writes. Docker API 1.44 or newer is required so this option cannot be silently ignored. This capability is required even for a requested writable mount because source preflight is read-only. Containers with host mounts set `no-new-privileges` to prevent setuid/file-capability executables from granting new privileges.

Mount changes use container recreation: running environments restart and stopped ones stay stopped. Mounts persist in the definition and survive upgrades and unrelated settings changes. On a failed replacement, the old container is restored and restarted if it was running, while the definition keeps the new mounts as a revision ahead of the applied one. Removing a mount does not delete its source; environment file deletion remains limited to the private workspace. Host folders elsewhere are not deleted. Files/download HTTP routes remain scoped to `/workspace`.

#### Volume grants

`VolumeGrant` is `{"name":"photos","readOnly":true}`, mounted at `/share/<name>`; the path can't be changed. `settings.volumes` is always an array. `POST /api/environments` accepts optional `volumes`; `PATCH /api/environments/{name}` replaces the complete list when an array is supplied, and omission or null keeps it. Up to 16 grants, each volume once; an omitted `readOnly` is false. Every PATCH validates the whole list's shape, but only newly added grants must name an existing volume. An environment that still grants a volume since deleted reports it in `error`; a change that keeps the grant is accepted, and its job fails with that error while the environment has a container (nothing is saved), so drop the grant with a PATCH of `volumes` first. Grant changes recreate the container like mount changes, and roll back the same way. Volume mounts are strict binds of `volume/<name>/` in the data folder, and an environment with volumes sets `no-new-privileges`, as with host folders. Removing an environment never touches its volumes. Volumes themselves are managed under "Volumes (admin)".

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

`agents` has the same entries, in the same order, as `Environment.agents`, each with `now` (what it is doing: running commands, browser use, its port mappings, `in` and `out` as in `Environment.forwards`) and `recent` (at most 20 lines, newest first; adjacent screen actions of one kind are merged into one line with a `count`). Summaries cover the last 15 minutes; they are kept in memory only, nothing is stored, and a supervisor restart forgets them. Command texts are masked (secrets show as `•••`) and typed text is never recorded. Signed-in accounts that can see the environment may read it; access tokens can't (401), others get 404.

`GET /api/environments/{name}/screen-events` supports a WebSocket event feed (see Events below), with SSE retained for clients making ordinary HTTP requests. It streams the screen actions CLI clients make, for drawing them over the screen: `event: screen` with data a `ScreenEvent`, `{"agent":{"id","name","color","inside"},"t":"…","action":"click","pos":[100,120]}`, plus `button`, `dir`, `n`, `keys` and `chars` where the action has them (`pos` is `[x1,y1,x2,y2]` for `drag`; `type` carries only `chars`, the number of characters). The actions sent are `click`, `double-click`, `triple-click`, `drag`, `scroll`, `key`, `hold-key` and `type`. A comment line keeps the stream alive every 25 s. Who may open it is as for `Activity`; the stream ends when the account loses access or the environment is removed. Events are only produced while someone is subscribed, and a slow reader misses some.

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

Jobs add optional `result`: a successful test returns the public status (`state`, `reason?`, `changedAt`, `currentExitIp?`, `lastExitIp?`, `exitIpObservedAt`); a completed rollout returns `{connection:VPNConnection,environments:[{id,name,error?}]}`. The job can be `done` with per-environment errors, so inspect every result and display a retry option. Retry a failed environment with start or save/restart; its desired revision is retained. Test/validation failures use sanitized service-credential/country/connectivity guidance, never provider logs. Failed recovery retains a fixed phase in the cached `vpn.reason` and private journal (for example `consumer-stop`, `consumer-create`, or `runtime-ensure`); these are diagnostics, not new status states. No raw engine/provider cause, token or path appears in phase messages.

Environment create and PATCH accept `vpnConnectionId` (string or explicit `null` for no VPN) and `lanExceptions`. Omitted PATCH fields retain their values; disabling VPN also retains omitted exception rules for later reuse. Rules are `{destination,protocol:"tcp"|"udp",ports:[{start,end}]}`; `end:0` means a single input port, output is normalized. Destinations must be supported private IPv4 addresses/CIDRs and cannot overlap mandatory reserved/environment networks. Exceptions intentionally permit only the selected direct LAN traffic.

`Environment.settings` adds `vpnConnectionId:string|null` and `lanExceptions:[]`. `Environment.vpn` is `{connectionId?,name?,provider?,country?,desiredRevision,appliedRevision,state,reason?,changedAt,currentExitIp?,lastExitIp?,exitIpObservedAt,idle}`. States are `disabled`, `stopped`, `connecting`, `connected`, `reconnecting`, `blocked`; only connected reports a current exit IP. An unknown observation uses the zero RFC3339 timestamp (`0001-01-01T00:00:00Z`). `idle:true` with `stopped` means the gateway is kept running and may consume a provider connection. A failed/interrupted transition stays visible by name even while the environment's container is absent. This includes explicit No VPN: `blocked` with no `connectionId` and a safe `reason` means the selected network transition is unfinished, not that a VPN connection is required. Poll retries pending transitions/removals and desired-power mismatches; restoring consumer power retains a healthy unchanged gateway. Health passes start centrally on a five-second interval and owned engine events; list/get/events only read cached observations. Eight shared observation/repair slots skip records when saturated, and repairs can last up to three minutes. There is no bounded fairness or fleet-wide five-second freshness guarantee, and skipped public connected/current-IP observations have no independent expiry. Scheduling/freshness improvements are deferred beyond the validated two-environment scope. Tunnel health never changes an environment's power by itself. Caddy targets the gateway endpoint, while exec/files/stats/activity still target the environment's container.

### Jobs

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"environment"?:name,"result"?:object}`. The stream sends every update in order, starting with the oldest the job keeps (the last 2,000), and ends after `done` or `failed`. The account that started a job, admins, and (for an environment's job) anyone who can see that environment may follow it; others get 404.

`POST /api/jobs/{id}/cancel` cancels a running VPN candidate test/validation (its owner or an admin): 204, and the job then fails with error `"cancelled"`. 409 for a job that has finished or can't be cancelled (VPN validation can; a committed VPN rollout completes its durable transition); 404 as above.

### Events

`GET /api/events` supports a same-origin, session-authenticated WebSocket upgrade for the environments the account can see. Each text message is `{"type":"environment","data":{…}}`, using the event names and payloads below. `/api/environments/{name}/screen-events` uses the same envelope with type `screen`. The server sends WebSocket pings every 25 seconds of inactivity; application messages from clients are refused. The UI reconnects with exponential backoff up to ten seconds and refreshes its list after reconnection. These permanent feeds use WebSockets so multiple viewer tabs do not exhaust the browser’s HTTP/1 fetch connections.

Ordinary HTTP requests retain the SSE format below for existing clients; job feeds remain SSE because they end when the job finishes.

A queue overflow or failed authoritative snapshot closes the stream so the client reconnects and obtains current state. Queued lifecycle/removal notifications resolve current environment state when sent; they do not replay stale container snapshots.


- `event: environment` — an `Environment` that appeared or changed (including one being created)
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running environment
- `event: viewers` — `{"name","viewers":[usernames],"mics":[usernames],"forwards":[…],"agents":[…]}` (as in `Environment`), when someone starts or stops viewing, turns a mic on or off, or a mapping opens or closes (sent at most every 250 ms per environment; the last state always follows), and at once when the clients present change (one arrives or leaves, or turns active or idle)
- `event: activity` — an `Activity` (as from `GET /api/environments/{name}/activity`), when what CLI clients do in the environment changes (sent at most once a second per environment; the last state always follows)

### Accounts (admin)

`Account` = `{"username","admin":bool,"environments":[names],"created","lastLogin"|null}`; `environments` are the environments it was granted (names of defined environments only)

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/accounts` | | `[Account]` (any signed-in account may list usernames, for avatars; non-admins get `[{"username"}]`) |
| `POST /api/accounts` | `{"username","password","admin","environments":[names]}` | 201 `Account`; 400 for a name that isn't defined |
| `PATCH /api/accounts/{username}` | `{"password"?, "admin"?, "environments"?:[names]}` | `Account`; 409 when it would leave no admin |
| `DELETE /api/accounts/{username}` | | 204; 409 for the last admin |

### Access tokens

`Token` = `{"id","name","createdBy","created","expires"|null,"lastUsed"|null,"expired":bool}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/environments/{name}/tokens` | | `[Token]` (anyone with access) |
| `POST /api/environments/{name}/tokens` | `{"name","expires": "…"|null}` | 201 `Token` plus `"token":"dyt_…"` (shown once) and `"remoteAdd":"deskyard remote add alice https://host/alice --token dyt_…"` |
| `DELETE /api/environments/{name}/tokens/{id}` | | 204 (own tokens; admins any) |

### Volumes (admin)

`Volume` = `{"name","created","kind":"local","folder","environments":[{"name","readOnly"}]}`. `folder` is `volume/<name>/` in the data folder; `environments` lists the environments whose definitions grant the volume, running or not, sorted by name.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/volumes` | | `{"volumes":[Volume]}`, sorted by name |
| `POST /api/volumes` | `{"name"}` | 201 `{"volume":Volume,"adopted":bool}`; 400 for an invalid or reserved name, 409 for one in use, or when `volume/<name>` in the data folder is something other than a folder. A new folder is owned by UID/GID 1000 with mode 0755; an existing `volume/<name>/` (such as one kept by a delete) is adopted untouched, with `adopted: true` |
| `DELETE /api/volumes/{name}?deleteFiles=true\|false` | | 204; 404 for an unknown volume; 409 `{"error":"photos is used by alice, bob","environments":[{"id","name"}]}` while any definition grants it; 409 `{"error"}` while a container of an environment still mounts it, because a grant's removal hasn't been applied yet (it is already gone from the definition: wait, or start or restart the environment to apply it). The folder is kept unless `deleteFiles=true`; a failure deleting the files is a 500, with the volume already deleted |
| `GET /api/volumes/{name}/usage` | | 200 `{"bytes","complete","diskFree"?,"diskSize"?}`: the size of the volume's files, walked on each request (`complete` is false when some folders couldn't be read, so `bytes` is a lower bound), and the free and total bytes of the filesystem holding it, omitted when they can't be read; 404 for an unknown volume |

Volume names follow the environment name rules (2–32 characters) and can't be changed. Volumes are recorded in `volumes.json` in the data folder; grants are recorded only in environment definitions.

### Images (admin)

`CatalogImage` = `{"ref","title","version","description","environments":[names],"behind":[names],"checked"|null,"installed":bool,"official":bool,"size"?,"built"?}`; `size` (bytes on disk) and `built` (the image's creation time) are present once the image is downloaded

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New environment dialog) |
| `POST /api/images/install` | `{"ref"}` | 202 `{"job"}` (uses a known catalog image locally, pulling only when missing, and checks the contract) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (uses a local image or pulls a missing reference, then checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 400 for built-in official refs; 409 while any definition uses it, including stopped environments without a container |
| `POST /api/images/check` | | 202 `{"job"}` (pull installed non-custom refs, recompute updates, delete unused old desktop images) |
| `POST /api/images/upgrade` | `{"ref"}` | 202 `{"job"}` (upgrades every environment behind on it, then deletes unused old desktop images) |

`GET /api/images` merges saved entries with the built-in XFCE choice without pulling. `installed` means the reference exists locally; `official` identifies built-in refs; retired KDE and MATE references remain only where saved. Static titles/descriptions are supplied before download. Saved order and the configured default are preserved. Environment creation can pull available images on first use; successful use records the resolved local version. An unavailable registry or failed pull is reported through the job, leaving the choice available for retry.

Adding or installing an image validates the Deskyard contract even when the reference is already present locally. Local references, including `deskyard-custom/…`, can be added without contacting a registry. Missing references are pulled through the normal job flow. An incompatible local image fails validation instead of being replaced by a pull.

Unused old desktop images are untagged images labelled `deskyard.kind=desktop` that no container on the engine (of any state or supervisor) uses and that no environment definition or catalog entry pins. Removal never forces, and failures are only logged; the next check tries again.

`DELETE /api/images?ref=…` removes only the catalog entry. It does not delete Docker tags, images, legacy recipe JSON or source folders. Existing legacy locally built images remain usable and keep their exclusion from registry update checks. The daily update check (and `POST /api/images/check`) also skips missing images.

Image authoring is removed: `/api/recipes`, its template/edit/rebuild/delete subroutes, `/api/environments/{name}/image-scan` and `/api/environments/{name}/image` return 404. Build images separately with Docker and add their resulting references to the catalog. Existing image labels and home-template runtime support remain compatible; the supervisor does not migrate old authoring state.


## Desktop

- `/<name>/` serves the UI's desktop page (the SPA's `index.html`); the page frames `/<name>/screen/`, Selkies' web client, and drives it through `window.deskyardScreen` (new images use `web/src/selkies/adapter.ts`; pre-contract images use the legacy `internal/server/screen_additions.js`).
- `/<name>/screen/…` are Selkies' web client files, extracted from the environment's image (once per image ID, never from the running container). `index.html` and the selected adapter are served `no-store`; the other files `no-cache`. Any other spelling of the page (`/<name>/screen/index.html/`, `/<name>/screen/./index.html`, …) redirects to `/<name>/screen/`. The image hook marker selects the typed v1 adapter (`deskyard-screen.js`, inserted as a module before the client modules) or the legacy `deskyard.js` bridge. The v1 client exposes explicit transport/input/media/focus hooks and disables raw pointer updates directly; the legacy bridge intercepts the equivalent APIs. Unknown advertised hook versions return an actionable upgrade error. Both adapters
  - hide `pointerrawupdate` from the client (Chrome reports its coordinates relative to the top-level page inside an iframe, so drags landed off by our bar);
  - never send `STOP_VIDEO` (a background tab mustn't stop the desktop's stream for everyone);
  - remove Selkies' own tab id (`sessionStorage` `selkies_tab_id`) before Selkies reads it, so every load is its own Selkies client (a duplicated browser tab copies `sessionStorage`, and Selkies lets a page with the owner's tab id take the display over);
  - add `?tab=<id>` to the stream URL, an id made afresh on every load of the client (so a duplicated browser tab gets its own); the desktop page reads it as `frame.contentWindow.deskyardScreen.tab`;
  - expose `window.deskyardScreen`: `tab`, `post(msg)` (a message to Selkies' client), `on(type, fn)` (messages of that type from it; returns an unsubscribe function; `"deskyard.state"` gets each new state), `state()` (`"connecting" | "connected" | "disconnected"`), `video()` (the `<video>` or `<canvas>` showing the picture, or null).
- `/<name>/screen/api/websockets` (also with a trailing `/`) is the desktop's stream, proxied to the environment's Selkies with its proxy token and `Origin: http://supervisor.deskyard.invalid`, without the session cookie or the query. The WebSocket needs a session (401), access to the environment (404) and a same-origin `Origin` (403); 409 when the environment isn't running; 400 for a bad `tab` (letters, digits, `-`, `_`, at most 64). An open stream counts as viewing. A plain `GET` is the client's reconnect probe: 204 when Selkies answers, else 503 (never 409, which Selkies' client takes as "switch to WebRTC").
- Every other `/<name>/screen/api/…` is 404: Selkies' upload, files, screenshots, recordings, status and the rest are never served. Selkies refuses (close 4029) a stream arriving within 500 ms of the previous one from the same address, and every viewer reaches it from the desktop's Caddy, so the supervisor spaces each desktop's stream dials: a dial waits until 600 ms after Selkies answered the previous one. Viewers opening together, or reconnecting together after a restart, each wait up to 0.6 s per viewer ahead of them.

## Remote access API

Authenticated with `Authorization: Bearer dyt_<id>_<secret>` only (cookies are ignored here). The environment is the token's; an environment name in a remote's URL is only a check on the client side. A token also records when its environment was created, and stops working if the environment is removed and a new one made under the same name. 401 for a missing, unknown, revoked or expired token; 409 `{"error":"alice is stopped; someone with access can start it"}` from `exec`, `forward` and `cdp` when the environment isn't running (`GET /api/agent/v1` always answers 200 with its `state`). No Origin checks: CLI clients authenticate with bearer tokens.

### `GET /api/agent/v1`

```json
{"name":"alice","state":"running","image":"ghcr.io/…","imageTitle":"Debian XFCE","imageVersion":"2026.10.22","screen":{"width":1280,"height":800},"api":1}
```

### `GET /api/agent/v1/exec` (WebSocket)

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

### `GET /api/agent/v1/forward` (WebSocket)

TCP port mappings between the client’s machine and the environment, many connections multiplexed on one WebSocket (`deskyard forward`). The supervisor runs `deskyard forward-helper` in the environment as `user` (cwd `/workspace`, no TTY) and relays frames between the WebSocket and the helper's stdio, so the environment side listens on and dials `127.0.0.1` inside the environment. A forward token can therefore reach any loopback port in its environment (ports from 1024 up for listening), as its exec can. Like any local process in the environment, pages in the environment's browser can reach the in-mapped ports. A token may have 4 forward links open on an environment; a fifth gets `{"error":"…"}` and a close before the helper runs. `Environment.forwards` lists at most 64 mappings per token (the rest still work). The CLI prints `Linked to …` and the `out` lines when the link is accepted, before the helper is known to run.

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

The connection closes when the token is revoked or expires, or the environment stops, restarts or is upgraded; open connections through it are reset. The server pings every 30 s and drops a client that doesn't answer.

### `/api/agent/v1/cdp/…` (HTTP and WebSocket)

The shared Chromium's DevTools protocol: `GET /api/agent/v1/cdp/json/version`, `/json/list`, `PUT /json/new?url`, and WebSocket `/api/agent/v1/cdp/devtools/…`. In `/json*` responses (including `/json/version`), `webSocketDebuggerUrl` is absolute, `ws(s)://<host>/api/agent/v1/cdp/devtools/…`, and the `ws=` parameter in `devtoolsFrontendUrl` is `<host>/api/agent/v1/cdp/devtools/…` (Chromium's own `ws=`/`wss=` choice). The first request may take several seconds while Chromium starts.

### Running shared terminal sessions

`GET /api/environments/{name}/terminal/sessions` requires a signed-in account with View permission. Returns `{"sessions":[{"name":"main","clients":2,"command":"vim"},{"name":"terminal-1","clients":0,"command":"bash"}]}` sorted by name, or `{"sessions":[]}` when there are no live shells. Each row is one base session, including detached shells/jobs; private attachment groups are omitted. `clients` counts every client attached to the session, natively or through its private attachment groups, including this supervisor's own browser attachments (a page subtracts its own to count others). `command` is the foreground program of the session's active pane (`bash` when idle). Names use the terminal session validation rule above. The response contains no terminal content, process arguments, input or environment and has `Cache-Control: no-store`. The request also refreshes the `terminals` list of the environment JSON.

The supervisor executes only `deskyard terminal-list` as `user` in `/workspace`, without a TTY or stdin. The request has a five-second deadline and 64 KiB output limit; the helper has its own four-second deadline. Anonymous requests return 401, missing/inaccessible environments 404, stopped environments and images without `tmux-v1` 409, and helper failures or invalid/oversized data 502, using the usual `{"error":"message"}` response.

`POST /api/environments/{name}/terminal/sessions` (View permission, same-origin) opens a new session: the image's `deskyard terminal-create` starts the next free `terminal-N` detached in `/workspace`, the naming native terminal windows use. Returns 201 `{"name":"terminal-2"}`; attach to it with the terminal WebSocket. 409 when the environment isn't running or its image lacks `tmux-v1`, 502 when the helper fails.

`DELETE /api/environments/{name}/terminal/sessions/{session}` (View permission, same-origin) ends a session for everyone: `deskyard terminal-kill SESSION` kills the session's windows (so no attachment group, even one joining meanwhile, keeps the shell alive), then any private attachment groups left and the session itself, ending its shell and every program in it; it never touches another session and exits 3 when the session doesn't exist. Returns 204 when ended, 400 for an invalid name, 404 when the session doesn't exist, 409 when the environment isn't running or its image lacks `tmux-v1`, and 502 when the helper fails. This supervisor's browser attachments to the session receive `{"ended":true}` and a normal close; native and remote CLI clients see their tmux client exit.

## Replacement recovery

Settings changes and upgrades save the new definition revision first, then replace the container. Before the old container is touched, a mode 0600 record `state/recovery/<name>.json` (`{version,name,revision,parked,wasRunning}`, fsynced with its folders) notes the replacement; it holds transition state only, never settings or the proxy token. The old container is stopped and renamed `deskyard-<name>.parked`, not removed, until the new one is started and ready (when it should run). If the new container fails, it is removed, the parked one is renamed back and restarted if it was running, and the environment reports the failure in `error` with `revision` ahead of `appliedRevision`. A failed apply is retried only on start, restart or another edit, never by the reconcile loop. Interrupted replacements are finished or reverted at supervisor startup, after network reconnection; a record whose recovery fails is kept and the environment's lifecycle operations return 409 until startup can finish it. VPN environments never fall back to direct networking.
