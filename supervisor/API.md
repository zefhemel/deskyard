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
  "imageTitle": "Debian XFCE", "imageVersion": "2026.10.15", "imageSecrets": false,
  "updateAvailable": "2026.10.22" | null,
  "settings": {"memoryBytes": 0, "nanoCpus": 0},
  "access": ["anna"],
  "viewers": ["zef", "anna"],
  "forwards": [{"token": "deploy-bot", "direction": "in", "port": 3000}, {"token": "deploy-bot", "direction": "out", "port": 5173}],
  "tokens": 2,
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the desktop's CPU limit, or to all host CPUs without one (0–100). `access` lists regular accounts only (admins can use everything) and is only filled for admins. `forwards` are the port mappings agent tokens have open on the desktop (`deskyard forward`): `in` maps a port of the agent's machine into the desktop, `out` a desktop port out to the agent's machine; `port` is always the desktop's port (the agent's own port numbers aren't shown). Each entry is listed once, sorted by token, direction and port; an `in` port appears once the desktop is listening on it. `imageSecrets` is true when the desktop's image contains secrets (its starting home holds someone's sign-ins). `created` survives upgrades and settings changes (null for desktops made before it was recorded). `job` is the running create, upgrade or settings job, which anyone who can see the desktop may follow. A desktop's own page is at `/<name>/`.

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
| `POST /api/desktops/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/desktops/{name}/upgrade` (admin) | | 202 `{"job": id}` |
| `GET /api/desktops/{name}/image-scan` (admin) | | `ImageScan` (see [Images from a desktop](#images-from-a-desktop-admin)) |
| `POST /api/desktops/{name}/image` (admin) | `{"name","slug","groups":[…],"include":[…]}` | 202 `{"job"}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}` |

### Jobs

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"desktop"?:name}`. The stream sends every update in order, starting with the oldest the job keeps (the last 2,000), and ends after `done` or `failed`. The account that started a job, admins, and (for a desktop's job) anyone who can see that desktop may follow it; others get 404.

`POST /api/jobs/{id}/cancel` stops a running image build (its owner or an admin): 204, and the job then fails with error `"cancelled"`. 409 for a job that has finished or can't be cancelled (only builds can); 404 as above.

### Events

`GET /api/events` is a server-sent event stream for the desktops the account can see:

- `event: desktop` — a `Desktop` that appeared or changed
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running desktop
- `event: viewers` — `{"name","viewers":[usernames],"forwards":[…]}` (as in `Desktop`), when someone starts or stops viewing or a mapping opens or closes (sent at most every 250 ms per desktop; the last state always follows)

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

`CatalogImage` = `{"ref","title","version","description","desktops":[names],"behind":[names],"checked"|null,"custom":bool,"secrets":bool,"baseUpdated":bool,"recipe":slug|null}`

`custom` images are built here from a recipe (`recipe` is its slug); their ref is `deskyard-custom/<slug>:latest`. `secrets` is true when the image's starting home contains secrets (label `deskyard.secrets=true`, passed down to images built on it). `baseUpdated` is true when the custom image's base has a newer image in the catalog than the one it was built on (so rebuilding a custom image marks those built on it).

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New desktop dialog) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (pulls and checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 409 while desktops use it |
| `POST /api/images/check` | | 202 `{"job"}` (pull every ref, recompute updates) |
| `POST /api/images/upgrade` | `{"ref"}` | 202 `{"job"}` (upgrades every desktop behind on it) |

`DELETE /api/images?ref=…` on a custom image does what `DELETE /api/recipes/{slug}` does. The daily update check (and `POST /api/images/check`) never pulls custom images; `POST /api/images` refuses `deskyard-custom/…` refs.

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
- Always left out: caches (`~/.cache`, `~/.npm`, Chromium's caches, `~/.var/app/*/cache`, package caches such as `~/.cargo/registry` or `go/pkg/mod`), the Flatpak installation itself (`~/.local/share/flatpak`), and Deskyard's own state (`.workspace-seeded`, `~/.config/workspace`, X and VNC files).
- `secrets` are found by path and name only (`internal/recipes/secrets.go`): `~/.ssh`, `~/.gnupg`, keyrings, git credentials, shell history, tool logins (Codex, Claude, Gemini, `gh`, Docker, cloud CLIs' folders), `.npmrc`/`.netrc`/`.pypirc`/`.pgpass`, `.env` files, browser sign-ins (in a Chromium-style profile folder each of `Cookies`, `Network`, `Login Data`, `Web Data`, `Local Storage`, `Session Storage`, `IndexedDB`, `Service Worker`, `Sessions`, `Extension Cookies`, `Local Extension Settings`, `Extension State` and similar; in a Firefox profile `cookies.sqlite`, `logins.json`, `key4.db`, `cert9.db`, `storage`, `sessionstore*`, `webappsstore.sqlite`, `formhistory.sqlite`, `signedInUser.json` and extension storage), and those file names anywhere else, SSH private keys, key and certificate files, and names with a word like token, secret, credential, password or auth. Names inside installed software (`~/.local/share/mise`, `~/.npm-global`, `node_modules`, …) don't count. A secret folder is listed once and covers what's in it.
- `totalBytes` is everything that could be copied, secrets included. The UI warns above 5 GB; there is no limit.
- The Flatpak apps and remotes come from `flatpak list --user --app --columns=application,origin` and `flatpak remotes --user --columns=name,url` in a running desktop, and from the installation's repository in its folder when it is stopped.

`POST /api/desktops/{name}/image` with `{"name","slug","groups":[group names],"include":[secrets, as the scan lists them],"recipe"?:{"slug","name"}}` saves and builds the recipe `{"slug","name","base": <the desktop's image>,"homeFrom":"image","flatpaks","remotes","secrets"}` (202 `{"job"}`, a build as above). With `flatpak` ticked, the desktop's apps are installed again system-wide from their remotes; a remote other than Flathub goes in with the GPG key from the desktop's installation (for a stopped desktop, its config and keys are read through the same `os.Root`, regular files only), and one without a key is left out with its apps. The build context's `home-template/` (always there, empty if nothing is ticked) gets the files of the ticked groups without their secrets, plus each included secret whatever its group; files keep their permission bits (no setuid, setgid or sticky), and the image gives them to UID 1000. The image is marked `secrets` when any secret is included or its base has secrets, which passes down to images built on it. 400 for an unknown group or an `include` that isn't a secret; 409 if the slug (or the `recipe` slug) exists. With `recipe`, once the image is built a recipe `{"slug","name","base": "deskyard-custom/<slug>:latest"}` is saved on it (not built: it builds when first edited with `PUT`), to add tools on top later. The folder is read as the build runs: directly where the supervisor can read every file (walked through an `os.Root`, so a symlink a running desktop swaps in never leads outside the folder; links that lead outside are refused and links that stay inside are followed, which reaches nothing the desktop couldn't copy itself; files are opened without blocking, and only regular files are read; symlinks are copied as links, FIFOs, sockets and devices are left out), otherwise through a helper container of `alpine:3.22` with no network that mounts it read-only and streams it as root. The image's build copies the template with `COPY --chown=1000:1000`, so no root step runs over it. A running desktop's files can change while they're copied; stopping it first gives a consistent copy. Rebuilding such a recipe takes the template from its current image.


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
