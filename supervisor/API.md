# Supervisor HTTP API

The contract between the supervisor (Go, `internal/server`), its web UI (`web/`) and the `deskyard` command (`cmd/deskyard`). JSON everywhere; times are RFC 3339 strings; sizes in bytes; errors are `{"error": "message"}` with a 4xx/5xx status.

Remote control is available to people through the `deskyard` CLI, and to scripts and optional integrations through the same API. Create access tokens on a desktop’s **Remote access** tab. Existing `/api/agent/v1/` routes and `agents` fields retain their wire names for compatibility.

## Sessions (web UI)

The UI authenticates with the `deskyard_session` cookie (HttpOnly, SameSite=Strict). Every `/api/*` route except `POST /api/login` and `POST /api/logout` answers 401 without a session. State-changing requests (POST, PUT, PATCH, DELETE) must carry an `Origin` matching the supervisor's own (browsers' `fetch` does this); otherwise 403.

| Method and path | Body | Response |
| --- | --- | --- |
| `POST /api/login` | `{"username","password"}` | 200 `Me`, sets the cookie; 401 wrong; 429 throttled |
| `POST /api/logout` | | 204, clears the cookie and closes this browser login’s terminals; requires same-origin Origin, including when already signed out |
| `GET /api/me` | | `Me` = `{"username","admin":bool}` |
| `POST /api/me/password` | `{"current","new"}` | 204 (signs out everywhere else; the response re-issues this session) |
| `GET /api/host` | | `{"cpus":n,"memBytes":n,"running":n,"total":n,"memUsed":n,"cameras":[{"device","taken","desktop"?}] or null}` (counts over desktops the account can see). `cameras` are the host's virtual cameras (v4l2loopback: `/dev/videoN` under `/sys/devices/virtual/video4linux`) and whether a desktop has each (named when the account can see it); null when the engine isn't on this machine (not a unix socket), whose cameras can't be seen |

`GET /api/host` also includes `memoryReclaim: {"enabled":bool,"reason"?:string}`, the cached startup capability result. The probe checks the full helper path using a disposable container, without reclaiming any existing desktop. Failures disable the optional action and supply a reason; capability detection is retried on supervisor restart. A disabled action returns 501 immediately. Per-desktop checks still run on enabled hosts and do not change the cached host status.

### Desktops

`Desktop`:

```json
{
  "id": "6f1c…", "name": "alice",
  "state": "running" | "stopped" | "starting" | "creating" | "upgrading" | "removing",
  "startedAt": "…", "created": "…" | null, "job": "a1b2c3d4" | null,
  "folder": "/Users/zef/deskyard/alice",
  "image": "ghcr.io/zefhemel/deskyard-debian-xfce:latest",
  "imageTitle": "Debian XFCE", "imageVersion": "2026.10.15", "imageSecrets": false,
  "updateAvailable": "2026.10.22" | null,
  "autoReclaim": true, "terminal": true,
  "settings": {"memoryBytes": 0, "nanoCpus": 0, "camera": "", "vpnConnectionId": null, "lanExceptions": [], "mounts": []},
  "vpn": {"state":"disabled","changedAt":"0001-01-01T00:00:00Z","exitIpObservedAt":"0001-01-01T00:00:00Z","desiredRevision":0,"appliedRevision":0,"idle":false},
  "access": ["anna"],
  "viewers": ["zef", "anna"],
  "mics": ["anna"],
  "cams": [],
  "webcam": false,
  "forwards": [{"token": "My laptop", "direction": "in", "port": 3000}, {"token": "My laptop", "direction": "out", "port": 5173}],
  "agents": [{"id": "k3j9…", "name": "My laptop", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…"}],
  "tokens": 2,
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`terminal` reports image support (`deskyard.terminal=tmux-v1`), independent of running state. Custom images may opt in only when they supply the shared helper and tmux runtime.

`autoReclaim` is the saved preference, keyed by stable desktop ID in `memory-policy.json`, independent of the host capability flag. On supported hosts the supervisor checks every 30 seconds for running desktops with no viewers for 15 continuous minutes, making one 128 MiB attempt per absence (including failed/partial attempts). Viewer joins, even between checks, reset the timer; supervisor/container restarts and re-enabling the preference start fresh. Active tracked CLI clients (recent actions, running commands, browser use or forwards), lifecycle operations and unknown/stale inside status delay the attempt without consuming it. Automatic work uses the same lifecycle reservation as the manual action; it rechecks viewers, identity, policy and activity after reservation. Apps keep running and a viewer arriving during reclamation can reconnect normally. Idle status must come from a current desktop activity service (`commandsRunning:false` heartbeat less than 45 seconds old); missing/legacy/disconnected activity skips automatic work. Unreported shell/background work cannot be detected; opt out for such workloads.

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the desktop's CPU limit, or to all host CPUs without one (0–100). `access` lists regular accounts only (admins can use everything) and is only filled for admins. `viewers` are the accounts with a desktop stream or browser terminal open (each once, sorted); `mics` and `cams` those of them with a mic or camera on, as their pages report it (see `POST /api/desktops/{name}/mic`). `settings.camera` is the host's virtual camera the desktop has to itself ("" for none; an admin gives one in the settings), and `webcam` is true while it runs with it, for viewers' webcams to feed. `forwards` are the port mappings access tokens have open on the desktop (`deskyard forward`): `in` maps a port of the client’s machine into the desktop, `out` a desktop port out to the client’s machine; `port` is always the desktop's port (the client's own port numbers aren't shown). Each entry is listed once, sorted by token, direction and port; an `in` port appears once the desktop is listening on it. `imageSecrets` is true when the desktop's image contains secrets (its starting home holds someone's sign-ins). `created` survives upgrades and settings changes (null for desktops made before it was recorded). `job` is the running create, upgrade or settings job, which anyone who can see the desktop may follow. `agents` are the CLI clients and local programs present on the desktop (always a list, sorted by name then id): access tokens (`id` is the token's id, `owner` the account that made it) and programs running inside the desktop (`inside: true`, `id` `inside:<program>`, `owner` empty). A client is present while something it started is running, a browser connection or port mapping is open, or for 5 minutes after its last action; `active` means an action in the last 10 seconds; `color` is fixed per id. A desktop's own page is at `/<name>/`.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/desktops` | | `[Desktop]` the account can see, sorted by name |
| `GET /api/desktops/{name}` | | `Desktop`; 404 if missing or not visible |
| `POST /api/desktops` (admin) | `{"name","image","memoryBytes","nanoCpus","access":[usernames],"mounts"?:[HostMount]}` | 202 `{"job": id}` |
| `POST /api/desktops/{name}/start` \| `stop` \| `restart` | | 204 |
| `PUT /api/desktops/{name}/memory-policy` (admin) | `{"autoReclaim":bool}` | 204. Save the per-desktop automatic policy without recreating/restarting it; default true. Session and same-origin only. 400 missing/invalid boolean, 403 regular accounts with access, 404 without access, 500 persistence failure. |
| `POST /api/desktops/{name}/reclaim-memory` (admin) | | 200 `{"requestedBytes","beforeBytes","afterBytes","swapBeforeBytes","swapAfterBytes","partial":bool}`. Requests 128 MiB of reclaim while apps run. The before/after values are resident cgroup bytes including file cache; swap values are logical swapped bytes, which can still use compressed RAM. `partial` means the kernel reclaimed less than requested; observed resident memory can grow again. 409 stopped or busy, 501 unsupported engine/kernel or unavailable/disabled swap, 502 helper failure. Session and same-origin only; 403 for regular accounts with access, 404 without access. |
| `DELETE /api/desktops/{name}?files=1` (admin) | | 204; `files=1` also deletes the workspace folder |
| `PATCH /api/desktops/{name}` (admin) | `{"image"?, "memoryBytes"?, "nanoCpus"?, "camera"?, "mounts"?:[HostMount], "confirmSecrets"?}` | 202 `{"job": id}` (recreates the container); 409 without `confirmSecrets` (see below); 400 for a camera that isn't one of the host's, 409 for one another desktop has. `camera` is a device from `GET /api/host`'s `cameras`, or "" for none. A desktop whose camera has gone from the host is recreated without it, which is logged: when it's started, restarted or recreated, and when the supervisor starts (then started again if the engine had tried to start it and failed; one a user stopped stays stopped) |
| `PUT /api/desktops/{name}/access` (admin) | `{"usernames":[…]}` | 204 |
| `GET /api/desktops/{name}/screen.png` | `?full=1` for full size | PNG, 480 px wide by default; 409 when stopped |
| `GET /api/desktops/{name}/processes` | | `{"titles":[…],"rows":[[…]]}` (columns PID, USER, %CPU, RSS (KiB), ELAPSED, COMMAND) |
| `GET /api/desktops/{name}/tools` | | `[{"tool","version","source":"desktop"\|"image"}]`: mise tools of a running desktop; `image` when installed under `/opt/mise`. `[]` when stopped, without mise, or unreadable |
| `GET /api/desktops/{name}/stats` | | `{"history":[{"t":"…","cpuPercent":n,"memBytes":n}], "memLimit":n}` (15 min at 5 s) |
| `GET /api/desktops/{name}/folder` | | `{"path","bytes","complete"}`; `complete` is false when some directories couldn't be read, so `bytes` is a lower bound |
| `GET /api/desktops/{name}/activity` | | `Activity` (below) |
| `GET /api/desktops/{name}/screen-events` | | server-sent events (below) |
| `GET /api/desktops/{name}/files` | `?path=` (default `/workspace`; relative paths start there) | `{"path","entries":[{"name","dir","size","modified"}],"truncated"}`, newest first, at most 2,000 entries; `path` is the resolved folder. Anyone who can see the desktop; session only (an access token gets 401). 403 outside `/workspace` (symlinks and `..` are resolved inside the desktop first), 404 missing, 400 not a folder, 409 stopped |
| `GET /api/desktops/{name}/download` | `?path=` | Streams a file, or a folder as a zip (links inside it are left out). Same access and errors as `files`; 400 for anything that is neither a file nor a folder |
| `POST /api/desktops/{name}/upload` | `?name=<file name>`, the raw file as the body (one request per file; `Content-Length` or chunked) | `{"path":"/workspace/Desktop/<final name>","bytes":n}`. Written as `user` into `~/Desktop` through a hidden `.part` file, renamed when complete; a taken name becomes `name (2).ext`, `name (3).ext`… Names with `/` or NUL, empty, `.`/`..` or over 200 bytes: 400. Over 2 GB (declared or counted while streaming): 413. An error or a client that leaves removes the `.part`. Anyone who can see the desktop; session and same-origin only (an access token gets 401: CLI clients use `deskyard cp`); 404 without access, 409 stopped |
| `POST /api/desktops/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/desktops/{name}/mic`, `…/cam` | `{"on":bool,"tab":id,"seq"?:n}` | 204; 400 without a valid `tab` or with a negative `seq`; 409 when the account has no stream open from that tab. Sets the mic or camera badge of the stream opened with `?tab=<id>` (see Desktop below); the badge goes when that stream closes. `seq` numbers a page's reports: one numbered no higher than the last applied for that badge is ignored (still 204), so a late report never undoes a newer one |
| `POST /api/desktops/{name}/upgrade` (admin) | `{"confirmSecrets"?}` (or no body) | 202 `{"job": id}`; 409 without `confirmSecrets` (see below) |
| `GET /api/desktops/{name}/image-scan` (admin) | | `ImageScan` (see [Images from a desktop](#images-from-a-desktop-admin)) |
| `POST /api/desktops/{name}/image` (admin) | `{"name","slug","groups":[…],"include":[…],"recipe"?:{"slug","name"}}` | 202 `{"job"}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}` |

Moving desktops onto an image that contains secrets (an upgrade, or `PATCH` with another `image`) while accounts other than admins can use them is refused with 409 unless the body has `"confirmSecrets": true`: those accounts could then use the image's sign-ins. The UI asks first, naming the image and the accounts, as it does when giving access to such a desktop.

#### Browser terminal

`GET /api/desktops/{name}/terminal?session=main` upgrades to a WebSocket using the login cookie and View permission. Session names match `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`. Missing login returns 401, invalid/missing Origin 403, inaccessible desktop 404, invalid session 400, stopped/busy/changed desktop or unsupported image 409, and a revoked login or connection limit 429. Unsupported images receive an explicit upgrade instruction. Preflight with the existing desktop JSON (`state`, `terminal`) before opening a socket.

Origin must contain an HTTP(S) scheme and the exact request Host, with no userinfo, path, query or fragment. TLS listeners and Secure deployments require HTTPS. Plain listeners also accept HTTPS origins so a trusted TLS proxy can preserve Host while forwarding over HTTP; forwarded headers are not trusted. The proxy must preserve the public Host and enforce its public TLS policy.

The first client text frame is `{"cols":80,"rows":24}`. Valid dimensions are 2–500 columns and 1–300 rows. The supervisor starts two owned commands as `user` in `/workspace`, with `TERM=xterm-256color`, proxy credentials stripped and activity recording disabled: non-TTY `deskyard terminal-lease SESSION ATTACHMENT_ID`, then TTY `deskyard terminal-attach ATTACHMENT_ID`. The 32-hex-digit attachment ID is generated by the server. The browser cannot supply argv, workdir, environment or user.

Subsequent client binary frames are raw input bytes (64 KiB maximum per frame); text frames are exactly `{"resize":[80,24]}` or `{"ack":N}`. Server text frames are `{"ready":true,"generation":"ID:STARTED_AT"}`, `{"exit":CODE}` or `{"error":"MESSAGE"}`; binary frames are raw output with no stream prefix. Generation combines container ID with UTC `StartedAt` in RFC3339Nano form, changing after restart or recreation. Ready follows registration of the exact tmux attachment and its first terminal output (retained for the renderer), so immediate input cannot race outer-PTY initialization. Exit ends the attachment; opening another terminal is an explicit client action.

Output chunks are at most 16 KiB, with at most 256 KiB awaiting acknowledgement. ACK is a positive count of consumed bytes, no larger than outstanding output; the renderer sends it only after its write callback. Unknown controls, invalid sizes or ACKs close the connection. Four queued input/resize messages bound pending input; a full queue closes the connection. A separate input pump keeps the reader available for ACKs even if the PTY blocks. Initial frame and socket writes have five-second deadlines; exec startup has a ten-second deadline. At most 16 connections per account and 32 per desktop are allowed.

The non-TTY lease creates a private `_deskyard_<ID>` grouped tmux session sharing the named base session’s windows. It emits internal `prepared` and `ready` lines on its own stdout; these are never part of the browser byte stream. Docker propagates non-TTY stdin EOF, including after supervisor process death. EOF triggers bounded removal of only that random grouped session, detaching its client while the base session and other clients retain the same panes/jobs. The EOF watcher starts before creation; bounded creation completes before cleanup. Attach-only execution never recreates a canceled group. There are no additional listeners or terminal services.

The remote CLI still sends the owned `deskyard terminal-helper SESSION` request over the existing remote exec API. Only this exact three-argument TTY request is recognized and executed through the same lease/attach pair; arbitrary remote commands retain existing exec behavior. A remote terminal’s stdin EOF detaches cleanly instead of injecting Ctrl-D into the shared shell. Actual Ctrl-D input bytes still go to the shell. Local/native terminal helpers keep direct tmux attachments.

Browser terminals count as viewers, preventing automatic idle memory reclamation. Logout cancels the exact browser login, identified by a random signed session ID preserved across cookie refresh; legacy signed cookies migrate with a stable identity. Open sockets expire at their original cookie expiry even if another request refreshes that cookie. Account deletion, View revocation, epoch/password changes, stop/delete/restart, external engine stop events and server shutdown cancel attachments. The shell survives attachment loss until it exits or the container stops. Terminal input/output is never recorded in activity or logs.

#### Host folder mounts

`HostMount` is `{"source":"/absolute/engine-host/folder","target":"/mnt/photos","readOnly":true}`. `settings.mounts` is always an array (empty for existing desktops without mounts). `POST /api/desktops` accepts optional `mounts`; `PATCH /api/desktops/{name}` replaces the complete list when an array is supplied. Omission or null preserves the current list on PATCH; `[]` detaches all. The API's omitted `readOnly` defaults to false; the UI defaults new entries to true.

Only admins can configure mounts. Everyone able to use a desktop can access the mounted files with that desktop's permissions. Up to 16 existing source directories are allowed. Source paths must be clean absolute engine-host paths; target paths must be clean paths below `/mnt/`, with no duplicate or nested mount targets. Paths are at most 4096 bytes and may not contain NUL. Invalid configuration is 400; mount-source existence/type failures are reported by the accepted job. The helper verifies sources on the engine host before stopping/replacing a desktop, with read-only strict bind mounts that never create missing directories. The source need not be visible in the supervisor container. Existing host permissions still apply; no source chown/chmod is performed. Strict read-only binds require recursive read-only enforcement, including nested mounts; unsupported engines/kernels fail preflight rather than silently allowing writes. Docker API 1.44 or newer is required so this option cannot be silently ignored. This capability is required even for a requested writable mount because source preflight is read-only. Desktops with host mounts set `no-new-privileges` to prevent setuid/file-capability executables from granting new privileges.

Mount changes use container recreation: running desktops restart and stopped desktops stay stopped. Settings persist in the `deskyard.setting.mounts` container label and survive upgrades and unrelated settings changes. Corrupt saved mount configuration prevents recreation rather than silently dropping mounts. On a failed replacement, rollback restores the old mount configuration and attempts to restart it if it was previously running. Removing a mount does not delete its source; desktop file deletion remains limited to the private workspace. Host folders elsewhere are not deleted or copied into images made from a desktop. Files/download HTTP routes remain scoped to `/workspace`.

#### Activity

`Activity` is what CLI clients and local programs on a desktop are doing:

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

`agents` has the same entries, in the same order, as `Desktop.agents`, each with `now` (what it is doing: running commands, browser use, its port mappings, `in` and `out` as in `Desktop.forwards`) and `recent` (at most 20 lines, newest first; adjacent screen actions of one kind are merged into one line with a `count`). Summaries cover the last 15 minutes; they are kept in memory only, nothing is stored, and a supervisor restart forgets them. Command texts are masked (secrets show as `•••`) and typed text is never recorded. Signed-in accounts that can see the desktop may read it; access tokens can't (401), others get 404.

`GET /api/desktops/{name}/screen-events` is a server-sent event stream of the screen actions CLI clients make, for drawing them over the screen: `event: screen` with data a `ScreenEvent`, `{"agent":{"id","name","color","inside"},"t":"…","action":"click","pos":[100,120]}`, plus `button`, `dir`, `n`, `keys` and `chars` where the action has them (`pos` is `[x1,y1,x2,y2]` for `drag`; `type` carries only `chars`, the number of characters). The actions sent are `click`, `double-click`, `triple-click`, `drag`, `scroll`, `key`, `hold-key` and `type`. A comment line keeps the stream alive every 25 s. Who may open it is as for `Activity`; the stream ends when the account loses access or the desktop is removed. Events are only produced while someone is subscribed, and a slow reader misses some.

### VPN connections (admin)

The web UI uses `/_vpn` for saved connections and the desktop create/Settings forms for assignment and LAN rules. The underscore route leaves `/vpn/` available for a desktop named `vpn`. Credential inputs start blank; omitted edit values retain saved secrets. Shared edits preview exact affected names and submit stable IDs plus the expected revision; conflicts refresh the preview and require confirmation again. UI job results report each affected desktop separately, including partial failures, with a link to retry its start/restart.

All five routes require an administrator session; mutations also require the usual same-origin header. Regular accounts may read the sanitized `vpn` object only on desktops they can access. Credential values, credential paths, runtime IDs/addresses and control tokens are never returned. Filesystem and engine failures at VPN CRUD/test/rollout boundaries and desktop network-journal persistence failures (including power, create/recreate jobs and final removal) use fixed public messages; private state paths and raw causes are not job output.

`VPNInput` is `{name,provider:"nordvpn",country,username?,password?}`. Create requires both service credentials; each omitted credential on an edit/test retains its saved value. Country must be one of the returned `countries`. `VPNConnection` is `{id,name,provider,country,revision,credentialsConfigured}`. A reference is `{id,name}` using the stable desktop ID.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/vpn/connections` | | 200 `{connections:[VPNConnection & {desktops:[Reference]}],countries:[string]}` |
| `POST /api/vpn/connections` | `VPNInput` | 201 `VPNConnection`; 400 invalid input; 409 duplicate name |
| `PUT /api/vpn/connections/{id}` | `VPNInput` plus `expectedRevision`, `affectedDesktopIds:[id]`, `confirmRestart:true` when referenced | 202 `{job,desktops:[Reference]}`; 409 if revision/reference preview changed, confirmation is missing, or a selected desktop needs recovery-record repair |
| `DELETE /api/vpn/connections/{id}` | | 204; 409 `{error,desktops:[Reference]}` if any desktop still selects it |
| `POST /api/vpn/test` | `VPNInput`, optional `connectionId` to retain saved omitted credentials | 202 `{job}` |

Missing selected IDs produce 400. A PUT tests its candidate in a disposable runtime before changing the saved revision or desktops. A failed test leaves the saved connection unchanged. The accepted reference set is rechecked atomically with assignments/deletions after validation. Successful validation commits the revision and applies it through durable per-desktop transitions; partial failures retain that selected VPN policy. Candidate tests have a two-minute execution deadline with separately bounded cleanup. Cancellation remains effective during candidate validation and waits for desktop operations or reference coordination. The revision commit and disabling cancellation share one boundary: an accepted cancellation leaves the saved revision and desktop application unchanged; after commit, the cancel endpoint returns 409 and recovery completes the durable rollout. Viewers disconnect only after commit. A selected labeled desktop without its recovery record rejects PUT before commit with 409 and `"VPN recovery record is missing; administrator repair required before editing connection"`; it is never reported as a successful skipped rollout.

Jobs add optional `result`: a successful test returns the public status (`state`, `reason?`, `changedAt`, `currentExitIp?`, `lastExitIp?`, `exitIpObservedAt`); a completed rollout returns `{connection:VPNConnection,desktops:[{id,name,error?}]}`. The job can be `done` with per-desktop errors, so inspect every result and display a retry option. Retry a failed desktop with start or save/restart; its desired revision is retained. Test/validation failures use sanitized service-credential/country/connectivity guidance, never provider logs. Failed recovery retains a fixed phase in the cached `vpn.reason` and private journal (for example `consumer-stop`, `consumer-create`, or `runtime-ensure`); these are diagnostics, not new status states. No raw engine/provider cause, token or path appears in phase messages.

Desktop create and PATCH accept `vpnConnectionId` (string or explicit `null` for no VPN) and `lanExceptions`. Omitted PATCH fields retain their values; disabling VPN also retains omitted exception rules for later reuse. Rules are `{destination,protocol:"tcp"|"udp",ports:[{start,end}]}`; `end:0` means a single input port, output is normalized. Destinations must be supported private IPv4 addresses/CIDRs and cannot overlap mandatory reserved/desktop networks. Exceptions intentionally permit only the selected direct LAN traffic.

`Desktop.settings` adds `vpnConnectionId:string|null` and `lanExceptions:[]`. `Desktop.vpn` is `{connectionId?,name?,provider?,country?,desiredRevision,appliedRevision,state,reason?,changedAt,currentExitIp?,lastExitIp?,exitIpObservedAt,idle}`. States are `disabled`, `stopped`, `connecting`, `connected`, `reconnecting`, `blocked`; only connected reports a current exit IP. An unknown observation uses the zero RFC3339 timestamp (`0001-01-01T00:00:00Z`). `idle:true` with `stopped` means the gateway is kept running and may consume a provider connection. A failed/interrupted transition stays visible by stable ID even while its desktop container is absent. This includes explicit No VPN: `blocked` with no `connectionId` and a safe `reason` means the selected network transition is unfinished, not that a VPN connection is required. Poll retries pending transitions/removals and desired-power mismatches; restoring consumer power retains a healthy unchanged gateway. Health passes start centrally on a five-second interval and owned engine events; list/get/events only read cached observations. Eight shared observation/repair slots skip records when saturated, and repairs can last up to three minutes. There is no bounded fairness or fleet-wide five-second freshness guarantee, and skipped public connected/current-IP observations have no independent expiry. Scheduling/freshness improvements are deferred beyond the validated two-desktop scope. Tunnel health never changes desktop power by itself. Caddy targets the gateway endpoint, while exec/files/stats/activity still target the desktop.

### Jobs

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"desktop"?:name,"result"?:object}`. The stream sends every update in order, starting with the oldest the job keeps (the last 2,000), and ends after `done` or `failed`. The account that started a job, admins, and (for a desktop's job) anyone who can see that desktop may follow it; others get 404.

`POST /api/jobs/{id}/cancel` cancels a running image build or VPN candidate test/validation (its owner or an admin): 204, and the job then fails with error `"cancelled"`. 409 for a job that has finished or can't be cancelled (builds and VPN validation can; a committed VPN rollout completes its durable transition); 404 as above.

### Events

`GET /api/events` is a server-sent event stream for the desktops the account can see:

A queue overflow or failed authoritative snapshot closes the stream so EventSource reconnects and obtains current state. Queued lifecycle/removal notifications resolve current desktop state when sent; they do not replay stale container snapshots.


- `event: desktop` — a `Desktop` that appeared or changed
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running desktop
- `event: viewers` — `{"name","viewers":[usernames],"mics":[usernames],"cams":[usernames],"forwards":[…],"agents":[…]}` (as in `Desktop`), when someone starts or stops viewing, turns a mic on or off, or a mapping opens or closes (sent at most every 250 ms per desktop; the last state always follows), and at once when the clients present change (one arrives or leaves, or turns active or idle)
- `event: activity` — an `Activity` (as from `GET /api/desktops/{name}/activity`), when what CLI clients do on the desktop changes (sent at most once a second per desktop; the last state always follows)

### Accounts (admin)

`Account` = `{"username","admin":bool,"desktops":[names],"created","lastLogin"|null}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/accounts` | | `[Account]` (any signed-in account may list usernames, for avatars; non-admins get `[{"username"}]`) |
| `POST /api/accounts` | `{"username","password","admin","desktops":[names]}` | 201 `Account` |
| `PATCH /api/accounts/{username}` | `{"password"?, "admin"?, "desktops"?:[names]}` | `Account`; 409 when it would leave no admin |
| `DELETE /api/accounts/{username}` | | 204; 409 for the last admin |

### Access tokens

`Token` = `{"id","name","createdBy","created","expires"|null,"lastUsed"|null,"expired":bool}`

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/desktops/{name}/tokens` | | `[Token]` (anyone with access) |
| `POST /api/desktops/{name}/tokens` | `{"name","expires": "…"|null}` | 201 `Token` plus `"token":"dyt_…"` (shown once) and `"remoteAdd":"deskyard remote add alice https://host/alice --token dyt_…"` |
| `DELETE /api/desktops/{name}/tokens/{id}` | | 204 (own tokens; admins any) |

### Images (admin)

`CatalogImage` = `{"ref","title","version","description","desktops":[names],"behind":[names],"checked"|null,"installed":bool,"official":bool,"custom":bool,"secrets":bool,"baseUpdated":bool,"recipe":slug|null}`

`custom` images are built here from a recipe (`recipe` is its slug); their ref is `deskyard-custom/<slug>:latest`. `secrets` is true when the image's starting home contains secrets (label `deskyard.secrets=true`, passed down to images built on it). `baseUpdated` is true when the custom image's base has a newer image in the catalog than the one it was built on (so rebuilding a custom image marks those built on it).

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New desktop dialog) |
| `POST /api/images/install` | `{"ref"}` | 202 `{"job"}` (downloads a known non-custom catalog image and checks the contract) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (pulls and checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 400 for built-in official refs; 409 while desktops use it |
| `POST /api/images/check` | | 202 `{"job"}` (pull installed non-custom refs, recompute updates) |
| `POST /api/images/upgrade` | `{"ref","confirmSecrets"?}` | 202 `{"job"}` (upgrades every desktop behind on it); 409 without `confirmSecrets` (see below) |

`GET /api/images` merges saved entries with built-in XFCE, KDE Plasma and MATE choices without pulling. `installed` means the reference exists locally; `official` identifies these built-in refs. Static titles/descriptions are supplied before download. Saved order and the configured default are preserved. Desktop creation and recipe builds can pull available images on first use; successful use records the resolved local version. An unavailable registry or failed pull is reported through the job, leaving the choice available for retry.

`DELETE /api/images?ref=…` on a custom image does what `DELETE /api/recipes/{slug}` does. The daily update check (and `POST /api/images/check`) never pulls missing or custom images; `POST /api/images` refuses `deskyard-custom/…` refs.

### Recipes (admin)

`Recipe` = `{"slug","name","base","mise"?:[specs],"remotes"?:[Remote],"flatpaks"?:[Flatpak],"commands"?:string,"homeFrom"?:"image","secrets"?:bool}`, with `Remote` = `{"name","repoFile"}` or `{"name","url","gpgKey"}` (base64) and `Flatpak` = `{"id","remote"?}` (Flathub when `remote` is empty) or `{"ref"}` (a `.flatpakref` URL). `slug` follows the desktop-name rule; `base` must be a catalog ref, and not one built on this recipe. `homeFrom` and `secrets` are the server's: ignored on `POST` (a recipe's `secrets` comes from its base) and kept from the saved recipe on `PUT` (where a base with secrets adds it).

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/recipes` | | `[Recipe]`, sorted by slug |
| `POST /api/recipes` | `Recipe` | 202 `{"job"}`: validates, saves and builds; 400 for an invalid recipe, 409 if the slug exists |
| `PUT /api/recipes/{slug}` | `Recipe` | 202 `{"job"}`: saves the edit and rebuilds; 404 for an unknown slug |
| `POST /api/recipes/{slug}/rebuild` | | 202 `{"job"}` |
| `DELETE /api/recipes/{slug}` | | 204: removes the recipe, its catalog entry and every `deskyard-custom/<slug>` tag; 409 while a desktop uses any of them |

Each recipe builds one at a time: a second build, edit or delete while one runs gets 409 `already building`.

A build is a cancellable job (30-minute limit). Each line of the build log comes as one update, with `message` the line and `progress` null. The build:

1. Takes the base's image ID, pulling the base first on a rebuild or edit unless it is a custom image (a base that can't be pulled but is on this machine is used as it is). The base must carry the Deskyard contract.
2. Builds `FROM <base image ID>` with the recipe's mise tools, Flatpak remotes and apps, home template and build commands (no build cache on a rebuild or edit), with network access but no Docker socket, host mounts or supervisor network. The image keeps its base's `USER`.
3. Checks the result. It must still carry the contract. Then the supervisor's own static scanner (`deskyard-image-scan`, embedded in the supervisor and copied into a throwaway container with no network and the image's healthcheck off, so nothing from the image runs) scans the base and the new image as root, walking everything but `/proc`, `/sys`, `/dev` and `/workspace`. Compared with the base, the new image must not add or change:
   - a `sudo`, `sudoedit` or `doas` executable, anywhere;
   - a setuid or setgid file that is new, or whose content (sha256) differs from the base's at that path;
   - a file with file capabilities (`security.capability`) that is new or changed;
   - a file or folder UID 1000 owns, or that its group or everyone may write, under `/usr` (so `/usr/local/bin`), `/etc` (so supervisord's `/etc/supervisor`), `/opt/workspace` or `/opt/deskyard` (except `/opt/deskyard/home-template`), and the folders above those (`/`, `/opt`);
   - a symlink there whose target resolves under `/workspace`, `/tmp`, `/var/tmp`, `/home`, `/dev/shm` or `/run/workspace`, or to a place UID 1000 can write;
   - in `/etc/passwd`, `/etc/shadow` and `/etc/group`: a new UID 0 or GID 0 account, a new GID 0 group, `user` not UID 1000 (or missing), `user` added to a group, or a UID 0 account with no password or a password changed from the base.

   Every finding is named in the error. A scan that fails, exits non-zero, or whose output is cut short or unreadable fails the build. `/opt/mise` and `/var/lib/flatpak` are not checked for writability by design: root doesn't run anything from them (only the setuid, capability and sudo checks cover them). Last, a throwaway desktop of the image (`build-check-…`, a prefix no one else's desktop may use; removed afterwards) must become ready within 2 minutes.
4. Tags it `deskyard-custom/<slug>:<YYYYMMDD-HHMMSS>` and `:latest`, puts `deskyard-custom/<slug>:latest` in the catalog, and removes older timestamp tags no container uses. Desktops on the image then show `updateAvailable`.

Known limits: Flatpak apps that download extra data while installing (`apply_extra`, e.g. Spotify) may fail to build, since that step needs Flatpak's sandbox, which a plain build doesn't allow. A build's network is the engine's default bridge, so its commands reach the internet and whatever else such a container can: on Docker Desktop and OrbStack that includes services on the host's loopback (`host.docker.internal`), the supervisor's own port among them, which still requires signing in.

A failed or cancelled build leaves no tag, no catalog entry and no image; the recipe stays saved. Labels on a custom image: `deskyard.kind=custom`, `org.opencontainers.image.title` (the recipe name), `org.opencontainers.image.version` (build time, `YYYY-MM-DD HH:MM`), `deskyard.base` (the base's image ID), `deskyard.recipe` (the recipe JSON, so anyone who can inspect the image can read its build commands and remote URLs), and `deskyard.secrets=true` when it has secrets.

### Images from a desktop (admin)

A desktop's home folder becomes the starting home (`/opt/deskyard/home-template`) of a new image built on the desktop's own image, as a recipe with `homeFrom: "image"`.

`GET /api/desktops/{name}/image-scan` reads the folder and gives:

```json
{
  "groups": [{"name": "tools", "bytes": 1395864371, "paths": [".local/share/mise", ".npm-global"], "secrets": []},
             {"name": "dotfiles", "bytes": 41984, "paths": [".bashrc", ".codex"], "secrets": [".codex/auth.json", ".ssh"]}, …],
  "flatpaks": [{"id": "org.gimp.GIMP", "remote": "flathub"}],
  "remotes": [{"name": "flathub", "url": "https://dl.flathub.org/repo/"}],
  "totalBytes": 8804682956,
  "notes": ["Leaving out the Flatpak remote nightly: it has no GPG key, so the image couldn't verify it."]
}
```

- `groups` are always `tools` (`~/.local/share/mise`, `~/.local/bin`, `~/.npm-global`, `~/.local/share/uv`, `~/.config/mise`), `flatpak` (the app data in `~/.var`), `settings` (`~/.config`), `dotfiles` (other top-level entries starting with a dot), `browser` (browser profiles, from an allow-list in `internal/recipes/browsers.go`: the shared Chromium's `~/.config/chromium-shared`, and `~/.config/chromium`, `~/.config/google-chrome`, `~/.config/BraveSoftware/Brave-Browser` and `~/.mozilla/firefox`. In each profile folder, bookmarks, preferences and the extensions' code are kept; each sign-in store is a secret; history, caches and the browser's own state are left out) and `other`, in that order. `bytes` is what the group copies, without its secrets; `paths` are its folders and files that hold more than secrets. All paths are relative to the home folder.
- Always left out: caches (`~/.cache`, `~/.npm`, Chromium's caches, `~/.var/app/*/cache`, package caches such as `~/.cargo/registry` or `go/pkg/mod`), the Flatpak installation itself (`~/.local/share/flatpak`), and Deskyard's own state (`.workspace-seeded`, X files). `~/.config/workspace`, where the panel's one-time migrations mark that they ran, is part of `settings`, so it goes with the panel settings and a desktop made from the image doesn't redo them over the user's panel.
- `secrets` are found by path and name only (`internal/recipes/secrets.go`): `~/.ssh`, `~/.gnupg`, keyrings, git credentials, shell history, tool logins (Codex, Claude, Gemini, `gh`, `glab`, Docker, cloud CLIs' folders, Deskyard's own `~/.config/deskyard` with its remotes' tokens), `.npmrc`/`.netrc`/`.pypirc`/`.pgpass`, `.env` files, browser sign-ins (in a Chromium-style profile folder each of `Cookies`, `Network`, `Login Data`, `Web Data`, `Local Storage`, `Session Storage`, `IndexedDB`, `Service Worker`, `Sessions`, `Extension Cookies`, `Local Extension Settings`, `Extension State` and similar; in a Firefox profile `cookies.sqlite`, `logins.json`, `key4.db`, `cert9.db`, `storage`, `sessionstore*`, `webappsstore.sqlite`, `formhistory.sqlite`, `signedInUser.json` and extension storage), and those file names anywhere else, SSH private keys, key and certificate files, and names with a word like token, secret, credential, password or auth. Names inside installed software (`~/.local/share/mise`, `~/.npm-global`, `node_modules`, …) don't count. A secret folder is listed once and covers what's in it.
- `totalBytes` is everything that could be copied, secrets included. The UI warns above 5 GB; there is no limit.
- The Flatpak apps and remotes come from `flatpak list --user --app --columns=application,origin` and `flatpak remotes --user --columns=name,url` in a running desktop, and from the installation's repository in its folder when it is stopped.

`POST /api/desktops/{name}/image` with `{"name","slug","groups":[group names],"include":[secrets, as the scan lists them],"recipe"?:{"slug","name"}}` saves and builds the recipe `{"slug","name","base": <the desktop's image>,"homeFrom":"image","flatpaks","remotes","secrets"}` (202 `{"job"}`, a build as above). If that first build fails the recipe is removed again (it couldn't be rebuilt without its first image), so the same name can be tried again. With `flatpak` ticked, the desktop's apps are installed again system-wide from their remotes; a remote other than Flathub goes in with the GPG key from the desktop's installation (for a stopped desktop, its config and keys are read through the same `os.Root`, regular files only), and one without a key is left out with its apps. The build context's `home-template/` (always there, empty if nothing is ticked) gets the files of the ticked groups without their secrets, plus each included secret whatever its group; files keep their permission bits (no setuid, setgid or sticky), and the image gives them to UID 1000. The image is marked `secrets` when any secret is included or its base has secrets, which passes down to images built on it. 400 for an unknown group or an `include` that isn't a secret; 409 if the slug (or the `recipe` slug) exists. With `recipe`, once the image is built a recipe `{"slug","name","base": "deskyard-custom/<slug>:latest"}` is saved on it (not built: it builds when first edited with `PUT`), to add tools on top later. The folder is read as the build runs: directly where the supervisor can read every file (walked through an `os.Root`, so a symlink a running desktop swaps in never leads outside the folder; links that lead outside are refused and links that stay inside are followed, which reaches nothing the desktop couldn't copy itself; files are opened without blocking, and only regular files are read; symlinks are copied as links, FIFOs, sockets and devices are left out), otherwise through a helper container of `alpine:3.22` with no network that mounts it read-only and streams it as root. The image's build copies the template with `COPY --chown=1000:1000`, so no root step runs over it. A running desktop's files can change while they're copied; stopping it first gives a consistent copy. Rebuilding such a recipe takes the template from its current image.


## Desktop

- `/<name>/` serves the UI's desktop page (the SPA's `index.html`); the page frames `/<name>/screen/`, Selkies' web client, and drives it through `window.deskyardScreen` (new images use `web/src/selkies/adapter.ts`; pre-contract images use the legacy `internal/server/screen_additions.js`).
- `/<name>/screen/…` are Selkies' web client files, extracted from the desktop's image (once per image ID, never from the running container). `index.html` and the selected adapter are served `no-store`; the other files `no-cache`. Any other spelling of the page (`/<name>/screen/index.html/`, `/<name>/screen/./index.html`, …) redirects to `/<name>/screen/`. The image hook marker selects the typed v1 adapter (`deskyard-screen.js`, inserted as a module before the client modules) or the legacy `deskyard.js` bridge. The v1 client exposes explicit transport/input/media/focus hooks and disables raw pointer updates directly; the legacy bridge intercepts the equivalent APIs. Unknown advertised hook versions return an actionable upgrade error. Both adapters
  - hide `pointerrawupdate` from the client (Chrome reports its coordinates relative to the top-level page inside an iframe, so drags landed off by our bar);
  - never send `STOP_VIDEO` (a background tab mustn't stop the desktop's stream for everyone);
  - remove Selkies' own tab id (`sessionStorage` `selkies_tab_id`) before Selkies reads it, so every load is its own Selkies client (a duplicated browser tab copies `sessionStorage`, and Selkies lets a page with the owner's tab id take the display over);
  - add `?tab=<id>` to the stream URL, an id made afresh on every load of the client (so a duplicated browser tab gets its own); the desktop page reads it as `frame.contentWindow.deskyardScreen.tab`;
  - expose `window.deskyardScreen`: `tab`, `post(msg)` (a message to Selkies' client), `on(type, fn)` (messages of that type from it; returns an unsubscribe function; `"deskyard.state"` gets each new state), `state()` (`"connecting" | "connected" | "disconnected"`), `video()` (the `<video>` or `<canvas>` showing the picture, or null).
- `/<name>/screen/api/websockets` (also with a trailing `/`) is the desktop's stream, proxied to the desktop's Selkies with the desktop's token and `Origin: http://supervisor.deskyard.invalid`, without the session cookie or the query. The WebSocket needs a session (401), access to the desktop (404) and a same-origin `Origin` (403); 409 when the desktop isn't running; 400 for a bad `tab` (letters, digits, `-`, `_`, at most 64). An open stream counts as viewing. A plain `GET` is the client's reconnect probe: 204 when Selkies answers, else 503 (never 409, which Selkies' client takes as "switch to WebRTC").
- Every other `/<name>/screen/api/…` is 404: Selkies' upload, files, screenshots, recordings, status and the rest are never served. Selkies refuses (close 4029) a stream arriving within 500 ms of the previous one from the same address, and every viewer reaches it from the desktop's Caddy, so the supervisor spaces each desktop's stream dials: a dial waits until 600 ms after Selkies answered the previous one. Viewers opening together, or reconnecting together after a restart, each wait up to 0.6 s per viewer ahead of them.

## Remote access API

Authenticated with `Authorization: Bearer dyt_<id>_<secret>` only (cookies are ignored here). The desktop is the token's; a desktop name in a remote's URL is only a check on the client side. 401 for a missing, unknown, revoked or expired token; 409 `{"error":"alice is stopped; someone with access can start it"}` from `exec`, `forward` and `cdp` when the desktop isn't running (`GET /api/agent/v1` always answers 200 with its `state`). No Origin checks: CLI clients authenticate with bearer tokens.

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

TCP port mappings between the client’s machine and the desktop, many connections multiplexed on one WebSocket (`deskyard forward`). The supervisor runs `deskyard forward-helper` in the desktop as `user` (cwd `/workspace`, no TTY) and relays frames between the WebSocket and the helper's stdio, so the desktop side listens on and dials `127.0.0.1` inside the desktop. A forward token can therefore reach any loopback port in its desktop (ports from 1024 up for listening), as its exec can. Like any local process in the desktop, pages in the desktop's browser can reach the in-mapped ports. A token may have 4 forward links open on a desktop; a fifth gets `{"error":"…"}` and a close before the helper runs. `Desktop.forwards` lists at most 64 mappings per token (the rest still work). The CLI prints `Linked to …` and the `out` lines when the link is accepted, before the helper is known to run.

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

### Running shared terminal sessions

`GET /api/desktops/{name}/terminal/sessions` requires a signed-in account with View permission. Returns `{"sessions":[{"name":"main"},{"name":"terminal-1"}]}` sorted by name, or `{"sessions":[]}` when there are no live shells. Each row is one base session, including detached shells/jobs; private attachment groups are omitted. Names use the terminal session validation rule above. The response contains no terminal content, process arguments, input or environment and has `Cache-Control: no-store`.

The supervisor executes only `deskyard terminal-list` as `user` in `/workspace`, without a TTY or stdin. The request has a five-second deadline and 64 KiB output limit; the helper has its own four-second deadline. Anonymous requests return 401, missing/inaccessible desktops 404, stopped desktops and images without `tmux-v1` 409, and helper failures or invalid/oversized data 502, using the usual `{"error":"message"}` response. Existing terminal WebSocket and remote exec contracts are unchanged.

## Experimental application/window bridge

Requires the prototype desktop image. All routes require a signed-in account
with access to the desktop; POST requires the existing same-origin check.

- `GET /api/desktops/{name}/applications`: `{apps: [{id, name}], windows: [{id, title, app, parent, active, minimized, x, y, width, height}]}`. IDs are strings; `parent` is a transient parent window ID or null. `?windows=1` skips application enumeration and returns an empty `apps` array.
- `POST /api/desktops/{name}/applications`: `{action: "launch" | "activate", id: string}`. Launch accepts a registered `.desktop` ID; activate accepts a decimal X11 window ID. Returns `{id: string | null}`; a null launch result means no matching window was found within six seconds, not that launch failed.
- A stopped desktop returns 409; malformed action/ID 400; absent helper or invalid/oversized response 502. Helper output is capped at 1 MiB with a 12-second deadline.

These operations share the existing session and its input focus. Window pixels
are still delivered through the full desktop Selkies stream.


## Replacement recovery

Settings changes and upgrades persist private recovery intent before stopping/removing the old container. Failure during create/start/readiness/final inspection restores the previous immutable image, logical update reference, identity, settings and workspace; a previously running desktop must become ready again before recovery completes. Interrupted replacements are rolled back at supervisor startup, after network reconnection. Records are mode 0600 under `.deskyard/recovery/`; failed recovery retains its record and operation reservations return conflict until startup can restore it. The filesystem and engine may still prevent restoration; the supervisor logs that failure rather than discarding recovery intent. These records contain the proxy token and belong in private supervisor-state backups.
