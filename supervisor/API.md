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
| `GET /api/host` | | `{"cpus":n,"memBytes":n,"running":n,"total":n,"memUsed":n,"cameras":[{"device","taken","desktop"?}] or null}` (counts over desktops the account can see). `cameras` are the host's virtual cameras (v4l2loopback: `/dev/videoN` under `/sys/devices/virtual/video4linux`) and whether a desktop has each (named when the account can see it); null when the engine isn't on this machine (not a unix socket), whose cameras can't be seen |

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
  "settings": {"memoryBytes": 0, "nanoCpus": 0, "camera": ""},
  "access": ["anna"],
  "viewers": ["zef", "anna"],
  "mics": ["anna"],
  "cams": [],
  "webcam": false,
  "forwards": [{"token": "deploy-bot", "direction": "in", "port": 3000}, {"token": "deploy-bot", "direction": "out", "port": 5173}],
  "agents": [{"id": "k3j9…", "name": "deploy-bot", "owner": "zef", "inside": false, "color": "#2457c5", "active": true, "lastActive": "…"}],
  "tokens": 2,
  "stats": {"cpuPercent": 34.2, "memBytes": 1503238553, "memLimit": 2147483648} | null
}
```

`memoryBytes`/`nanoCpus` 0 mean no limit; `memLimit` is then the host's memory. `cpuPercent` is relative to the desktop's CPU limit, or to all host CPUs without one (0–100). `access` lists regular accounts only (admins can use everything) and is only filled for admins. `viewers` are the accounts with a stream open (each once, sorted); `mics` and `cams` those of them with a mic or camera on, as their pages report it (see `POST /api/desktops/{name}/mic`). `settings.camera` is the host's virtual camera the desktop has to itself ("" for none; an admin gives one in the settings), and `webcam` is true while it runs with it, for viewers' webcams to feed. `forwards` are the port mappings agent tokens have open on the desktop (`deskyard forward`): `in` maps a port of the agent's machine into the desktop, `out` a desktop port out to the agent's machine; `port` is always the desktop's port (the agent's own port numbers aren't shown). Each entry is listed once, sorted by token, direction and port; an `in` port appears once the desktop is listening on it. `imageSecrets` is true when the desktop's image contains secrets (its starting home holds someone's sign-ins). `created` survives upgrades and settings changes (null for desktops made before it was recorded). `job` is the running create, upgrade or settings job, which anyone who can see the desktop may follow. `agents` are the agents present on the desktop (always a list, sorted by name then id): agent tokens (`id` is the token's id, `owner` the account that made it) and programs running inside the desktop (`inside: true`, `id` `inside:<program>`, `owner` empty). An agent is present while something it started is running, a browser connection or port mapping is open, or for 5 minutes after its last action; `active` means an action in the last 10 seconds; `color` is fixed per id. A desktop's own page is at `/<name>/`.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/desktops` | | `[Desktop]` the account can see, sorted by name |
| `GET /api/desktops/{name}` | | `Desktop`; 404 if missing or not visible |
| `POST /api/desktops` (admin) | `{"name","image","memoryBytes","nanoCpus","access":[usernames]}` | 202 `{"job": id}` |
| `POST /api/desktops/{name}/start` \| `stop` \| `restart` | | 204 |
| `DELETE /api/desktops/{name}?files=1` (admin) | | 204; `files=1` also deletes the workspace folder |
| `PATCH /api/desktops/{name}` (admin) | `{"image"?, "memoryBytes"?, "nanoCpus"?, "camera"?, "confirmSecrets"?}` | 202 `{"job": id}` (recreates the container); 409 without `confirmSecrets` (see below); 400 for a camera that isn't one of the host's, 409 for one another desktop has. `camera` is a device from `GET /api/host`'s `cameras`, or "" for none. A desktop whose camera has gone from the host is recreated without it, which is logged: when it's started, restarted or recreated, and when the supervisor starts (then started again if the engine had tried to start it and failed; one a user stopped stays stopped) |
| `PUT /api/desktops/{name}/access` (admin) | `{"usernames":[…]}` | 204 |
| `GET /api/desktops/{name}/screen.png` | `?full=1` for full size | PNG, 480 px wide by default; 409 when stopped |
| `GET /api/desktops/{name}/processes` | | `{"titles":[…],"rows":[[…]]}` (columns PID, USER, %CPU, RSS (KiB), ELAPSED, COMMAND) |
| `GET /api/desktops/{name}/tools` | | `[{"tool","version","source":"desktop"\|"image"}]`: mise tools of a running desktop; `image` when installed under `/opt/mise`. `[]` when stopped, without mise, or unreadable |
| `GET /api/desktops/{name}/stats` | | `{"history":[{"t":"…","cpuPercent":n,"memBytes":n}], "memLimit":n}` (15 min at 5 s) |
| `GET /api/desktops/{name}/folder` | | `{"path","bytes","complete"}`; `complete` is false when some directories couldn't be read, so `bytes` is a lower bound |
| `GET /api/desktops/{name}/activity` | | `Activity` (below) |
| `GET /api/desktops/{name}/screen-events` | | server-sent events (below) |
| `GET /api/desktops/{name}/files` | `?path=` (default `/workspace`; relative paths start there) | `{"path","entries":[{"name","dir","size","modified"}],"truncated"}`, newest first, at most 2,000 entries; `path` is the resolved folder. Anyone who can see the desktop; session only (an agent token gets 401). 403 outside `/workspace` (symlinks and `..` are resolved inside the desktop first), 404 missing, 400 not a folder, 409 stopped |
| `GET /api/desktops/{name}/download` | `?path=` | Streams a file, or a folder as a zip (links inside it are left out). Same access and errors as `files`; 400 for anything that is neither a file nor a folder |
| `POST /api/desktops/{name}/upload` | `?name=<file name>`, the raw file as the body (one request per file; `Content-Length` or chunked) | `{"path":"/workspace/Desktop/<final name>","bytes":n}`. Written as `user` into `~/Desktop` through a hidden `.part` file, renamed when complete; a taken name becomes `name (2).ext`, `name (3).ext`… Names with `/` or NUL, empty, `.`/`..` or over 200 bytes: 400. Over 2 GB (declared or counted while streaming): 413. An error or a client that leaves removes the `.part`. Anyone who can see the desktop; session and same-origin only (an agent token gets 401: agents use `deskyard cp`); 404 without access, 409 stopped |
| `POST /api/desktops/{name}/keys` | `{"keys":"ctrl+alt+Delete"}` (xdotool syntax) | 204 |
| `POST /api/desktops/{name}/mic`, `…/cam` | `{"on":bool,"tab":id,"seq"?:n}` | 204; 400 without a valid `tab` or with a negative `seq`; 409 when the account has no stream open from that tab. Sets the mic or camera badge of the stream opened with `?tab=<id>` (see Desktop below); the badge goes when that stream closes. `seq` numbers a page's reports: one numbered no higher than the last applied for that badge is ignored (still 204), so a late report never undoes a newer one |
| `POST /api/desktops/{name}/upgrade` (admin) | `{"confirmSecrets"?}` (or no body) | 202 `{"job": id}`; 409 without `confirmSecrets` (see below) |
| `GET /api/desktops/{name}/image-scan` (admin) | | `ImageScan` (see [Images from a desktop](#images-from-a-desktop-admin)) |
| `POST /api/desktops/{name}/image` (admin) | `{"name","slug","groups":[…],"include":[…],"recipe"?:{"slug","name"}}` | 202 `{"job"}` |
| `GET /api/validate-name?name=x` (admin) | | `{"ok":bool,"error"?:string,"folder":path,"folderExists":bool}` |

Moving desktops onto an image that contains secrets (an upgrade, or `PATCH` with another `image`) while accounts other than admins can use them is refused with 409 unless the body has `"confirmSecrets": true`: those accounts could then use the image's sign-ins. The UI asks first, naming the image and the accounts, as it does when giving access to such a desktop.

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

`GET /api/jobs/{id}` is a server-sent event stream; each `data:` line is `{"status":"running"|"done"|"failed","message":"Pulling image","progress":0.38|null,"error"?:string,"desktop"?:name}`. The stream sends every update in order, starting with the oldest the job keeps (the last 2,000), and ends after `done` or `failed`. The account that started a job, admins, and (for a desktop's job) anyone who can see that desktop may follow it; others get 404.

`POST /api/jobs/{id}/cancel` stops a running image build (its owner or an admin): 204, and the job then fails with error `"cancelled"`. 409 for a job that has finished or can't be cancelled (only builds can); 404 as above.

### Events

`GET /api/events` is a server-sent event stream for the desktops the account can see:

- `event: desktop` — a `Desktop` that appeared or changed
- `event: removed` — `{"name"}`
- `event: stats` — `{"name","cpuPercent","memBytes","memLimit","t"}` every 5 s per running desktop
- `event: viewers` — `{"name","viewers":[usernames],"mics":[usernames],"cams":[usernames],"forwards":[…],"agents":[…]}` (as in `Desktop`), when someone starts or stops viewing, turns a mic on or off, or a mapping opens or closes (sent at most every 250 ms per desktop; the last state always follows), and at once when the agents present change (one arrives or leaves, or turns active or idle)
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

`CatalogImage` = `{"ref","title","version","description","desktops":[names],"behind":[names],"checked"|null,"custom":bool,"secrets":bool,"baseUpdated":bool,"recipe":slug|null}`

`custom` images are built here from a recipe (`recipe` is its slug); their ref is `deskyard-custom/<slug>:latest`. `secrets` is true when the image's starting home contains secrets (label `deskyard.secrets=true`, passed down to images built on it). `baseUpdated` is true when the custom image's base has a newer image in the catalog than the one it was built on (so rebuilding a custom image marks those built on it).

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /api/images` | | `[CatalogImage]` (non-admins may read it, for the New desktop dialog) |
| `POST /api/images` | `{"ref"}` | 202 `{"job"}` (pulls and checks the contract) |
| `DELETE /api/images?ref=…` | | 204; 409 while desktops use it |
| `POST /api/images/check` | | 202 `{"job"}` (pull every ref, recompute updates) |
| `POST /api/images/upgrade` | `{"ref","confirmSecrets"?}` | 202 `{"job"}` (upgrades every desktop behind on it); 409 without `confirmSecrets` (see below) |

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
- Always left out: caches (`~/.cache`, `~/.npm`, Chromium's caches, `~/.var/app/*/cache`, package caches such as `~/.cargo/registry` or `go/pkg/mod`), the Flatpak installation itself (`~/.local/share/flatpak`), and Deskyard's own state (`.workspace-seeded`, X files). `~/.config/workspace`, where the panel's one-time migrations mark that they ran, is part of `settings`, so it goes with the panel settings and a desktop made from the image doesn't redo them over the user's panel.
- `secrets` are found by path and name only (`internal/recipes/secrets.go`): `~/.ssh`, `~/.gnupg`, keyrings, git credentials, shell history, tool logins (Codex, Claude, Gemini, `gh`, `glab`, Docker, cloud CLIs' folders, Deskyard's own `~/.config/deskyard` with its remotes' tokens), `.npmrc`/`.netrc`/`.pypirc`/`.pgpass`, `.env` files, browser sign-ins (in a Chromium-style profile folder each of `Cookies`, `Network`, `Login Data`, `Web Data`, `Local Storage`, `Session Storage`, `IndexedDB`, `Service Worker`, `Sessions`, `Extension Cookies`, `Local Extension Settings`, `Extension State` and similar; in a Firefox profile `cookies.sqlite`, `logins.json`, `key4.db`, `cert9.db`, `storage`, `sessionstore*`, `webappsstore.sqlite`, `formhistory.sqlite`, `signedInUser.json` and extension storage), and those file names anywhere else, SSH private keys, key and certificate files, and names with a word like token, secret, credential, password or auth. Names inside installed software (`~/.local/share/mise`, `~/.npm-global`, `node_modules`, …) don't count. A secret folder is listed once and covers what's in it.
- `totalBytes` is everything that could be copied, secrets included. The UI warns above 5 GB; there is no limit.
- The Flatpak apps and remotes come from `flatpak list --user --app --columns=application,origin` and `flatpak remotes --user --columns=name,url` in a running desktop, and from the installation's repository in its folder when it is stopped.

`POST /api/desktops/{name}/image` with `{"name","slug","groups":[group names],"include":[secrets, as the scan lists them],"recipe"?:{"slug","name"}}` saves and builds the recipe `{"slug","name","base": <the desktop's image>,"homeFrom":"image","flatpaks","remotes","secrets"}` (202 `{"job"}`, a build as above). If that first build fails the recipe is removed again (it couldn't be rebuilt without its first image), so the same name can be tried again. With `flatpak` ticked, the desktop's apps are installed again system-wide from their remotes; a remote other than Flathub goes in with the GPG key from the desktop's installation (for a stopped desktop, its config and keys are read through the same `os.Root`, regular files only), and one without a key is left out with its apps. The build context's `home-template/` (always there, empty if nothing is ticked) gets the files of the ticked groups without their secrets, plus each included secret whatever its group; files keep their permission bits (no setuid, setgid or sticky), and the image gives them to UID 1000. The image is marked `secrets` when any secret is included or its base has secrets, which passes down to images built on it. 400 for an unknown group or an `include` that isn't a secret; 409 if the slug (or the `recipe` slug) exists. With `recipe`, once the image is built a recipe `{"slug","name","base": "deskyard-custom/<slug>:latest"}` is saved on it (not built: it builds when first edited with `PUT`), to add tools on top later. The folder is read as the build runs: directly where the supervisor can read every file (walked through an `os.Root`, so a symlink a running desktop swaps in never leads outside the folder; links that lead outside are refused and links that stay inside are followed, which reaches nothing the desktop couldn't copy itself; files are opened without blocking, and only regular files are read; symlinks are copied as links, FIFOs, sockets and devices are left out), otherwise through a helper container of `alpine:3.22` with no network that mounts it read-only and streams it as root. The image's build copies the template with `COPY --chown=1000:1000`, so no root step runs over it. A running desktop's files can change while they're copied; stopping it first gives a consistent copy. Rebuilding such a recipe takes the template from its current image.


## Desktop

- `/<name>/` serves the UI's desktop page (the SPA's `index.html`); the page frames `/<name>/screen/`, Selkies' web client, and drives it through `window.deskyardScreen` (see `internal/server/screen_additions.js`).
- `/<name>/screen/…` are Selkies' web client files, extracted from the desktop's image (once per image ID, never from the running container). `index.html` and `deskyard.js` are served `no-store`; the other files `no-cache`. Any other spelling of the page (`/<name>/screen/index.html/`, `/<name>/screen/./index.html`, …) redirects to `/<name>/screen/`. `index.html` gets `<script src="deskyard.js"></script>` first in its `<head>`: our additions, which
  - hide `pointerrawupdate` from the client (Chrome reports its coordinates relative to the top-level page inside an iframe, so drags landed off by our bar);
  - never send `STOP_VIDEO` (a background tab mustn't stop the desktop's stream for everyone);
  - remove Selkies' own tab id (`sessionStorage` `selkies_tab_id`) before Selkies reads it, so every load is its own Selkies client (a duplicated browser tab copies `sessionStorage`, and Selkies lets a page with the owner's tab id take the display over);
  - add `?tab=<id>` to the stream URL, an id made afresh on every load of the client (so a duplicated browser tab gets its own); the desktop page reads it as `frame.contentWindow.deskyardScreen.tab`;
  - expose `window.deskyardScreen`: `tab`, `post(msg)` (a message to Selkies' client), `on(type, fn)` (messages of that type from it; returns an unsubscribe function; `"deskyard.state"` gets each new state), `state()` (`"connecting" | "connected" | "disconnected"`), `video()` (the `<video>` or `<canvas>` showing the picture, or null).
- `/<name>/screen/api/websockets` (also with a trailing `/`) is the desktop's stream, proxied to the desktop's Selkies with the desktop's token and `Origin: http://supervisor.deskyard.invalid`, without the session cookie or the query. The WebSocket needs a session (401), access to the desktop (404) and a same-origin `Origin` (403); 409 when the desktop isn't running; 400 for a bad `tab` (letters, digits, `-`, `_`, at most 64). An open stream counts as viewing. A plain `GET` is the client's reconnect probe: 204 when Selkies answers, else 503 (never 409, which Selkies' client takes as "switch to WebRTC").
- Every other `/<name>/screen/api/…` is 404: Selkies' upload, files, screenshots, recordings, status and the rest are never served. Selkies refuses (close 4029) a stream arriving within 500 ms of the previous one from the same address, and every viewer reaches it from the desktop's Caddy, so the supervisor spaces each desktop's stream dials: a dial waits until 600 ms after Selkies answered the previous one. Viewers opening together, or reconnecting together after a restart, each wait up to 0.6 s per viewer ahead of them.

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

## Experimental application/window bridge

Requires the prototype desktop image. All routes require a signed-in account
with access to the desktop; POST requires the existing same-origin check.

- `GET /api/desktops/{name}/applications`: `{apps: [{id, name}], windows: [{id, title, app, parent, active, minimized, x, y, width, height}]}`. IDs are strings; `parent` is a transient parent window ID or null. `?windows=1` skips application enumeration and returns an empty `apps` array.
- `POST /api/desktops/{name}/applications`: `{action: "launch" | "activate", id: string}`. Launch accepts a registered `.desktop` ID; activate accepts a decimal X11 window ID. Returns `{id: string | null}`; a null launch result means no matching window was found within six seconds, not that launch failed.
- A stopped desktop returns 409; malformed action/ID 400; absent helper or invalid/oversized response 502. Helper output is capped at 1 MiB with a 12-second deadline.

These operations share the existing session and its input focus. Window pixels
are still delivered through the full desktop Selkies stream.
