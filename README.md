<img src="assets/logo/deskyard-mark.svg" width="72" alt="">

# Deskyard

Persistent Linux desktops you can access, share and control from anywhere—
in your browser or terminal. One app, the **supervisor**, runs them in containers.

![The desktop: Chromium on this project's GitHub page, and a terminal using node (installed with mise) and the deskyard command](assets/screenshot.png)

The supervisor is one Go binary with its web UI built in. You sign in with
your own account and create, open, start, stop, restart, upgrade and remove
**environments**, and see what each one is doing. An environment is one
persistent Linux desktop with its own home folder; its desktop is
in the browser at `http://host/<name>/`, and everyone with access to it
opens the same desktop, at the same time if they like. Live UI updates and screen action marks use WebSockets, so multiple viewer tabs also work over plain HTTP without occupying the browser’s limited HTTP fetch connections.

What's in an environment:

- **XFCE desktop in the browser**, streamed with
  [Selkies](https://github.com/selkies-project/selkies): picture, sound,
  your mic and gamepads.
  It resizes to fit your browser window (or keeps a locked size). The
  bar above it shows the connection as a dot and the VPN as a shield, and
  has icon buttons (with tooltips) for sound and mic, Display (lock size,
  resolution and quality), the clipboard, keys the browser would otherwise take
  (Ctrl+Alt+Del, Alt+Tab, ...) and a file download browser. Drop files on the
  screen to copy them into the environment. Badges show who has their mic on.
  The bar's tabs show the desktop next to the environment's shared terminal
  sessions. On phones, Display, Keys and Download are in the scrollable More
  menu, and the tabs get a row of their own.
- **One shared Chromium** with one profile: your tabs and sign-ins stay
  with the environment. It starts when you open it or connect through browser tools.
- **Remote access and control**: use the `deskyard` command from your terminal
  for shells, shared terminals, file transfers, screenshots, clicks and typing.
  Map ports to reach services on either machine.
- **Persistent installs**: `/workspace` (the user's home) is a plain folder
  on the host (`~/deskyard/home/<name>`). Tools installed with mise, npm, uv or
  Flatpak live there and survive restarts and image upgrades.
- **CLI activity**: see terminal clients and programs working in an environment
  alongside browser viewers, with what they are running, recent actions,
  and marks on the screen where they click and type. Typed text and secrets
  on command lines are hidden.

The desktop image is Debian 13, `linux/arm64` and `linux/amd64`. About 0.4 GB
of RAM per running environment with Chromium open, so several fit on an 8 GB
machine.

## Install on an iPhone Home Screen

In Safari, open Deskyard, sign in, then choose **Share → View More → Add to
Home Screen** (the exact share-sheet layout depends on iOS). Leave **Open
as Web App** enabled and tap **Add**. Launch the Deskyard icon to open the
app without Safari's address bar; choose an environment from the list.

The installed app supports portrait and landscape, keeps controls clear of
the Dynamic Island and home indicator, and resizes around the software
keyboard. On iOS, a solid strip below the status bar keeps the system's
top-edge blur away from toolbar text. It needs the running supervisor and
a network connection; remote desktop sessions are not cached for offline use.
Installation and native keyboard input were verified in the iOS 27 iPhone
simulator, including on the plain HTTP LAN demo. HTTPS is still required
for the mic and automatic clipboard synchronization; see Running on a LAN.

## Mobile controls

The **Display** menu has **Lock size** and **Resolution:** choices that pick
a remote screen size and lock it, plus the quality presets.
On phones, open **⋯ More** for the same choices. Presets include
800×600, 1024×768, 1280×720, 1280×800, 1920×1080, and portrait sizes
600×900 and 900×1200. **Resolution: Auto fit** unlocks the size. The choice
is remembered in your browser; the first viewer controls the shared screen.
More explains when another viewer controls the shared resolution.

Touch controls are available on touch devices, including landscape phones
and tablets:

- **Tap** clicks where your finger touches; **Trackpad** moves the pointer
  relative to your finger. In a magnified view, moving the
  pointer to an edge pans your view to follow it. The choice is remembered
  in your browser.
- **Keyboard** shows or hides the phone's software keyboard. Esc, Tab,
  Enter and Paste are alongside it (swipe the extra keys if space is short).
- **Pinch** magnifies your own view, up to 4×. With the view magnified,
  move two fingers together to pan; **Fit screen** resets magnification.
  At fit, a two-finger swipe scrolls the remote application as usual.

Magnification is local: it does not zoom the application or resize the shared
remote screen. The controls remain outside the magnified picture. The page
follows the visible viewport when the software keyboard opens. These touch
controls are served by the supervisor and work with any Selkies desktop image.

## Install

There are no versioned releases yet. Everything below is **edge**: built
from `main` on every change, in the
[edge pre-release](https://github.com/zefhemel/deskyard/releases/tag/edge)
and the images' `latest` tags.

### The `deskyard` command

For access from your own terminal outside an environment (inside one, it's already there).
On Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/zefhemel/deskyard/main/install.sh | sh
```

It downloads `deskyard-<os>-<arch>` from the edge release, checks it
against `SHA256SUMS` and installs it to `~/.local/bin` (or, if that isn't
writable, to `/usr/local/bin` with sudo, saying so first). Set
`DESKYARD_VERSION` to install another release tag, and
`DESKYARD_INSTALL_DIR` to install elsewhere:

```bash
curl -fsSL https://raw.githubusercontent.com/zefhemel/deskyard/main/install.sh | DESKYARD_INSTALL_DIR=~/bin sh
```

On Windows, download `deskyard-windows-amd64.exe` (or `-arm64.exe`) from
the edge release.

### The supervisor

You need Docker (or OrbStack, or Docker Desktop) on Linux or macOS.

**The binary.** Download `deskyard-supervisor-<os>-<arch>` (`linux-amd64`,
`linux-arm64`, `darwin-amd64`, `darwin-arm64`) from the edge release and run
it:

```bash
curl -fsSLo deskyard-supervisor https://github.com/zefhemel/deskyard/releases/download/edge/deskyard-supervisor-linux-amd64
chmod +x deskyard-supervisor
./deskyard-supervisor
```

It finds the engine at `DESKYARD_ENGINE_HOST`, else `DOCKER_HOST`, else
the platform's default socket, keeps everything in `~/deskyard` (set
`DESKYARD_DATA_DIR` for another folder) and listens on `127.0.0.1:7343`. Its
user must be able to use the Docker socket. More settings: Configuration.

**In a container.** The image `ghcr.io/zefhemel/deskyard` (amd64 and arm64)
needs the Docker socket and the data folder, mounted **at the same path** as
on the host (see Storage):

```bash
docker run -d --name deskyard --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /srv/deskyard:/srv/deskyard -e DESKYARD_DATA_DIR=/srv/deskyard \
  -p 127.0.0.1:7343:7343 \
  ghcr.io/zefhemel/deskyard:latest
```

Or, from a checkout, `supervisor/compose.yaml` does the same:

```bash
cd supervisor
DESKYARD_DATA_DIR=/srv/deskyard docker compose up -d
```

### Desktop images

Nothing to install: the supervisor pulls
`ghcr.io/zefhemel/deskyard-debian-xfce:latest` when you create the first
environment (see Images and upgrades).

## First start

On first start the supervisor creates the account `admin` and
prints its generated password once in its log (`docker logs deskyard`, or
`docker compose logs supervisor`, in a container), unless you set
`DESKYARD_ADMIN_PASSWORD`.
Open <http://localhost:7343>, sign in, and create an environment: give it a name
like `alice` and its desktop opens at <http://localhost:7343/alice/>.

**On a server with a domain.** Set `DESKYARD_DOMAIN=desktops.example.com`,
point its DNS at the host and let ports 80 and 443 through: the supervisor
gets a certificate itself and serves HTTPS. In a container, publish `80:80`
and `443:443` instead of `127.0.0.1:7343:7343` (the compose file shows
where). Run directly on Linux, let the binary bind low ports with
`sudo setcap cap_net_bind_service=+ep ./deskyard-supervisor`. Without a domain, keep
the default localhost binding and put your own HTTPS proxy or Tailscale in
front.

### On a LAN

To reach Deskyard from other machines at home or in an office, either
serve it over https or accept what plain http costs.

**Plain http** (`DESKYARD_LISTEN=0.0.0.0:7343`, then
`http://192.168.1.20:7343`): browsers treat any address but `localhost`
over http as insecure and turn features off. The desktop page says so in
its bar ("Not secure: some features off"). What still works: the picture,
keyboard and mouse, sound, the Clipboard button's text box (both ways),
Lock size, Quality, upload, download and the CLI activity overlay. Gamepads
should too in Chromium and Safari, which keep the Gamepad API over http. What doesn't:

- **Mic**: the browser has no `getUserMedia`. The Audio panel shows it off,
  with "Needs https".
- **Automatic clipboard sync**: copy and paste go through the Clipboard
  button instead.
- **H.264 in Chrome and Firefox**: without WebCodecs the picture is JPEG.
  At 1920x1080 and the Medium preset that is about 40 Mbit/s while a
  terminal scrolls (H.264: about 6), and next to nothing on a still
  screen. Fine on wired gigabit, workable on good Wi-Fi with Low (about
  18 Mbit/s); not for the internet. When one viewer is on JPEG the whole
  desktop streams JPEG, also to viewers on https.
- **Passwords travel in clear text.** Only on a network you trust.

**https** keeps everything. Without a public domain, the simplest is
Tailscale: run the supervisor on its default `127.0.0.1:7343`, then
`tailscale serve --bg 7343` on the same machine, and open
`https://<machine>.<tailnet>.ts.net` from any device on your tailnet. Or
put any reverse proxy with a certificate in front (Caddy:
`reverse_proxy 127.0.0.1:7343`); it must pass WebSockets through and keep
the `Host` header. With a public domain, `DESKYARD_DOMAIN` does it
itself (above).

## Shared terminal

The environment's page (`http://host/alice/`) shows its desktop and its shared terminal sessions as tabs: **Desktop** first, then one tab per running session (including ones opened from the desktop's own terminal, the CLI or another browser; new ones appear within about 10 seconds), then **+**. A terminal tab's address is `http://host/alice/?terminal=build-2`, and a small count on the tab shows how many others are attached. **+** opens a new session named `terminal-1`, `terminal-2`, and so on. Switching tabs keeps each terminal connected and its scrollback; leaving the Desktop tab stops its stream, and returning reconnects it. Drag a tab, or press Alt+Shift+← or → on it, to reorder; the order is remembered in this browser. A terminal tab's bar has Paste, Keys and Text size.

**×** on a tab ends that session for everyone: its shell, every program in it, and every other client's view. It asks first when a program other than the shell is running in it, or when anyone else is attached; otherwise it ends at once. A link or a reconnect only joins: a session that no longer exists opens the Desktop tab with a "session ended" note and is never recreated. Each running environment's card lists **Desktop** and its sessions (the first three and "+N more" when there are more than four), and **+** opens `main`, creating it if needed, or a new session when `main` already exists.

The create/join walkthrough, screenshots, native keyboard evidence and known limits are in the project space's *Testing/Shared terminal 2026-10-23* page, and the tabs' checks in *Testing/Environment tabs 2026-10-26* (see [Project records](#project-records)). The page reconnects after brief network interruptions without replaying disconnected input. Restarting an environment ends its shells and jobs.

On phones, tap **Keyboard** to open the software keyboard. The scrollable key bar provides Ctrl+C/D/L, Esc, Tab, arrows, Home/End, PgUp/PgDn and Delete, plus one-shot Ctrl and Alt. Helper taps keep the keyboard open; paste and composition clear modifiers. On short keyboard viewports the bar moves into the key row as a horizontally scrollable strip. If the browser leaves less than the minimum space for a readable terminal row and 44px controls, the page asks you to rotate to portrait or dismiss the keyboard; the shell stays connected. **History** opens tmux history: use PgUp/PgDn, then Esc to return. The font stays readable when clients have different sizes; a passive smaller client may show a cropped tmux view until it interacts.

`deskyard -e alice terminal` joins the environment's shared Bash login shell in `/workspace`; `deskyard terminal build-2` selects another named shell. Inside the environment, ordinary terminal windows and tabs (including **Deskyard Terminal**, the default terminal shortcut and file-manager terminal actions) each open a new shared shell named `terminal-1`, `terminal-2`, and so on, preserving their starting directory. The window/tab title shows the session name, and the browser shows it as a tab. When sessions are already running, the desktop's **Deskyard Terminal** launcher first lists them with their attached clients and running program: type a number to join one, or press Enter for a new session. Within a native shared terminal, press **Ctrl-B then s** and choose its name; outside tmux, `deskyard terminal NAME` joins it. Browser clients switch tabs; remote CLI clients detach and reconnect with the chosen name. Saved emulator defaults migrate at desktop startup; explicit emulator command launches such as `xfce4-terminal -x PROGRAM` still run the requested program directly. Run `deskyard terminal` locally to join `main`. Session names contain 1–64 letters, digits, underscores or dashes and start with a letter or digit. Detach with Ctrl-B then D; reconnecting joins the same shell and running jobs. Sessions end when the environment stops or restarts. Existing environments need an updated image with tmux and the shared terminal helper.

## Environments

Admins create environments with a name, an image from the catalog, and
optionally a memory and CPU limit (by default an environment may use all of the
host's). Names are 2 to 32 lowercase letters, digits and dashes, start with a letter
and can't be changed later; the name is the environment's identity. Each
environment runs as a container `deskyard-<name>` on its own network
`deskyard-<name>`, so environments can't reach each other.

The supervisor keeps each environment's definition (image, limits, network,
volume grants, Docker access, memory preference and whether it should
run) in `environments.json`, not on its container. The container is
disposable: if it is lost, the supervisor recreates it from the definition
with the same name, home folder, access grants, tokens and settings. A stopped
environment may have no container; it gets one when started. Stopping keeps the
container, so changes outside `/workspace` survive until the container is next
recreated.

Every settings change that affects the container is saved as a new revision
and then applied by recreating the container. The old container is stopped and
set aside until the new one is ready; if the new one fails, the old one comes
back (running if it was) and the environment shows the error, with the saved
change still pending. A pending change is tried again only when you start or
restart the environment or edit its settings. Every 30 seconds, and when a
container exits or disappears, the supervisor starts environments that should
be running. Containers it can't match to a definition, or several claiming one
environment, are shown to administrators as conflicts on the Environments page;
the supervisor leaves them alone, and a duplicate blocks that environment's
start, stop and settings until an administrator removes the extra container. A
name claimed by such a container can't be used for a new environment until the
container is removed. If a new environment's desktop isn't ready within two
minutes of its first boot, the creation is undone. Removing an environment
stops it first, so an interrupted removal never brings it back.

### Docker daemon access

**Docker daemon access** is an administrator checkbox when creating an environment
or editing its Settings. It defaults off. Enable it on a desktop image with
the Docker CLI, such as the standalone [Docker example](examples/docker/README.md).
The supervisor checks and mounts the engine host's `/var/run/docker.sock`;
an updated desktop image gives `user` its socket group. Saving a changed
setting recreates the environment's container, preserving its workspace.

Everyone using that environment can then control the shared Docker daemon and
its containers, with host administrator privileges. Only enable this for
trusted users. This is separate from volumes;
custom/rootless daemon socket locations are not currently supported.

### Outbound VPN

Administrators can add named connections on **VPN connections** (`/_vpn`), choose a country and enter [NordVPN service credentials](https://support.nordvpn.com/hc/en-us/articles/19685514639633-How-to-find-your-service-credentials-for-manual-NordVPN-setup), which differ from the account login. **Test connection** checks a candidate without changing environments; blank credentials on an edit retain their saved values. Choose a saved connection or **No VPN** when creating an environment or on its Settings tab. The same operations are available through the [VPN API](supervisor/API.md#vpn-connections-admin). Each selected environment gets its own managed Gluetun gateway. Internet access stays blocked while the tunnel is unavailable; only an explicit selection of no VPN restores direct networking. Enabling, disabling or changing VPN settings recreates the environment's container while preserving its name and workspace. Saving settings warns about session disconnection and uses **Save and restart** for running environments; files remain intact.

VPN environment containers do not restart independently after an engine reboot: run the supervisor to recover their namespace and firewall before starting them. While the supervisor runs, background recovery restores the saved power state after an unexpected container exit or a failed stop, retaining a healthy unchanged gateway and starting a stopped one again in place; a container that keeps crashing is restarted with growing delays, up to five minutes. A stopped environment's gateway is not repaired until the environment is started. Interrupted network changes, including an explicit No VPN selection, stay visible with a safe diagnostic and are finished when the supervisor starts; a change that failed is retried on start, restart or another edit. Direct access returns only after that selected transition completes. Stopping an environment keeps its gateway idle and may continue consuming a provider connection. LAN access is denied except for administrator-configured destination, transport and port rules. Existing agent port forwarding remains available; this feature does not prevent deliberate proxies or tunnels through permitted applications.

Shared connection edits list the affected environments and require confirmation before validating and applying a new revision. Validation can be cancelled before commit; a failed validation keeps the saved revision. Each environment's rollout reports its own result and retains its selected policy on failure. Connections still used by an environment cannot be deleted. Users with access to an environment see separate VPN health, current exit IP only while connected, historical observations and idle status; only administrators change the configuration.

The OrbStack verification uses real Selkies desktops, the supervisor proxy, agent exec/files/activity and shared Chromium with two independent controlled WireGuard tunnels. It covers direct and containerized supervisors, blocked requests during tunnel loss, local access and recovery. Controlled WireGuard does not validate NordVPN service authentication. Live NordVPN service authentication, verified desktop egress and visible MATE desktop access were confirmed on OrbStack on 24 October 2026. Country/credential changes, provider limits, native Linux and a full engine/host reboot remain release checks; see the [verification guide](supervisor/tests/VPN.md) for the exact scope and outstanding observations.

Each enabled environment adds one long-lived gateway container with its own CPU/memory use and provider connection; stopping the environment does not release that cost. Environment resource limits apply to its container, not its gateway. Version one uses public DNS through the tunnel, blocks external IPv6 and Docker's embedded resolver, and has no custom DNS selector. LAN exceptions intentionally send only their configured private IPv4 destination, TCP/UDP and ports outside the tunnel; other environment/control networks and link-local destinations cannot be excepted.

### What persists

An environment's private persistent folder is `/workspace`: the folder
`<data folder>/home/<name>` on the host, which you can browse and back up with host tools. Creating an environment
with the name of a folder that already exists reuses that folder, so to copy
an environment, copy its folder and create an environment with the new name.
Removing an environment keeps its folder unless you tick "also delete files".
If an environment's folder goes missing, the supervisor reports an error and
does not start it; it never creates the folder again by itself.

| Need | Command |
|---|---|
| Runtimes and CLI tools | `mise use -g node@22 python@3.12 go jq ripgrep` |
| npm CLIs | `npm i -g <pkg>` |
| Python CLIs | `uv tool install <pkg>` |
| Desktop apps | `flatpak install --user flathub <app-id>` |
| Anything else | put binaries in `~/.local/bin` |

These all land in `/workspace` and are on `PATH`. Node and Python come
preinstalled with the image (outside `/workspace`, so they update with it);
`mise use` wins over them. There's no `sudo`, and anything installed with
`apt` or changed outside `/workspace` is lost when the image is updated.

Stopping, starting and restarting keep the environment's container, so changes
outside `/workspace` (including `/tmp`) last until the container is rebuilt:
by an upgrade or a settings change that needs it. Stopped environments keep
their container too. Nothing in `/workspace` or a shared folder is ever
cleaned up; only removing an environment with "also delete files" deletes its
folder.

uv defaults to its own Python installations in `/workspace` so installed
tools do not depend on the image's Python. This default comes from
`/etc/uv/uv.toml`; explicit uv flags and user configuration can override it.
Choosing the image's Python explicitly makes that environment dependent on
the image version.

### Desktop state across restarts

When an environment stops (stop, restart, upgrades), its native session manager
logs out and saves the session; Chromium saves its tabs independently.
Native session restoration applies to applications that support that
desktop's session protocol (XFCE: Settings > Session and Startup).
Most Electron and
Flatpak apps do not restore their windows through this protocol.

Switching desktop images keeps your files, installed Flatpak apps, tools
and Chromium profile. Each environment keeps its own settings; open windows
are not migrated between different desktop environments.

### Volumes

A volume is a named folder that admins share between selected environments.
Create one under **Storage → Volumes**. Its files live either in the
supervisor's data folder (`<data folder>/volume/<name>`) or, with **An
existing folder on the host**, in a folder on the machine running the
container engine, such as `/srv/photos`. Then grant it to an environment
under **Settings → Volumes**: choose the volume, its path in the environment
(by default `/share/<volume>`) and whether it is read-only (new grants default
to read-write). Environments without a grant don't see it. Read-only grants
are enforced by the kernel (recursively read-only bind mounts), and
environments with volumes disable privilege gains through setuid executables
and file capabilities; Flatpak's user-namespace sandbox keeps working.

A grant's path can be any absolute folder except `/`, the home folder
`/workspace` and anything in it (the desktop takes it over at start-up), and
the system folders `/bin`, `/boot`, `/dev`, `/etc`, `/lib*`, `/opt`, `/proc`,
`/run`, `/sbin`, `/sys`, `/usr` and `/var`; grant paths can't overlap. There
is no limit on the number of grants.

Grant changes apply by recreating the container, like other settings: saving
restarts a running environment with the new mounts, and a failed recreation
restores the old container. A stopped environment picks the change up at its
next start.

Volume names follow the environment name rules and can't be changed. A new
volume in the data folder is owned by the desktop user (UID 1000) with mode
0755, so every environment writes as the same user. If `volume/<name>` already
exists, for example kept from a deleted volume, creating the volume adopts it
untouched and the page says so. A host folder must already exist; Deskyard
checks it on the engine host (Docker API 1.44 or newer and recursively
read-only bind mounts are required), and never creates, chowns or deletes it:
existing host permissions must let UID 1000 use its files. Host paths refer to
the machine running the container engine, with the supervisor running directly
or in a container; on macOS the container runtime must share the folder.

Deleting a volume is refused while any environment's settings grant it, running
or stopped, and the refusal names those environments; remove the grants first.
It is also refused while a container still mounts it because a grant's removal
hasn't been applied yet, and while a backup or restore of it runs. Deleting
keeps the files unless you tick "Delete the files", which isn't offered for a
host folder. Removing an environment, even with its files, never touches
volumes. If a volume's folder goes missing, the environment reports an error and
doesn't start until the folder is back; Deskyard never recreates it. Volumes are
outside the supervisor's Files and Download views, which browse only
`/workspace`. There is no CLI for volumes yet; they are managed in the web UI
or through `/api/volumes`.

Per-environment host folders were replaced by host volumes. A data folder whose
`environments.json` still has host folders (`mounts`) isn't migrated: the
supervisor refuses to start and names the environment; remove its folders with
the previous version, or delete the `mounts` entries, then create host volumes.

### Backups

Environments (their settings and home folder) and volumes can be backed up with
[restic](https://restic.net) to storage reached through
[rclone](https://rclone.org). Everything is under **Storage** in the top bar,
for admins.

1. **Remotes** are connections to storage elsewhere: an S3-compatible service
   (AWS, Cloudflare R2, MinIO, Wasabi…), Backblaze B2, an SFTP server or NAS, or
   a WebDAV server (Nextcloud, ownCloud…). Deskyard connects before saving one.
   Credentials are kept in `secret/remotes/<name>/` and never shown again.
2. **Backup repositories** are encrypted restic repositories in a folder on a
   remote (`deskyard-backups` by default). Adding one shows its **recovery key**
   once: keep it in a password manager, because without it the backups can't be
   read, even with the remote's credentials. It can be shown again on the
   Backups tab. A folder that already holds a repository (after a reinstall, or
   from another Deskyard) is connected by entering its key.
3. **Backups are opt-in**: on an environment's **Backups** tab, or a volume's
   page, choose a repository, a schedule (hourly, daily or weekly, in the
   supervisor's local time; daily at 03:00 by default) and how many snapshots to
   keep (7 daily, 4 weekly and 12 monthly by default). **Back up now** runs one
   at once. A run missed while the supervisor was down runs once when it is
   back; a failed one waits for the next slot, and failures show on the tab, on
   the environment's tile or volume's card, and under Storage → Backups.

Backups run while environments keep running, so a file being written at that
moment may be saved half-written. Files that can't be read are left out and
listed as a warning. Each snapshot can be:

- **browsed**, with single files downloaded or folders downloaded as zip files;
- **restored to a folder**: selected files and folders go into a new
  `Restored <date time>` folder inside the home or volume, overwriting nothing
  and stopping nothing;
- **restored here**: the home's or volume's files are replaced with the
  snapshot's (newer files go, deleted ones come back). The environment, or every
  running environment using the volume, is stopped meanwhile and started again.
  **Back up the current files first** (on by default) makes it undoable. An
  environment's settings don't change;
- **restored as new**: a new environment (left stopped, with the saved image and
  resources; VPN, Docker access and grants of missing volumes are dropped) or
  volume, leaving the original alone.

**In the repositories** on the Backups tab lists everything the repositories
hold, including removed environments and other machines' backups, to restore as
new. rclone and restic run in short-lived helper containers
(`rclone/rclone:1.75.2`, with restic 0.19.1), which get credentials only as
read-only files. To read a repository without Deskyard:
`restic -r rclone:<remote>:<root>/<folder> snapshots`, with the remote's
`secret/remotes/<name>/rclone.conf` as `RCLONE_CONFIG` and the recovery key as
the password. Snapshots are tagged `environment:<name>` or `volume:<name>`; an
environment's are under `/deskyard/environment/<name>/` (`home/` and
`definition.json`), a volume's under `/deskyard/volume/<name>/`.

### Reclaiming memory

Admins can click **Reclaim memory** on a running environment's details page to
ask the host to release idle memory while its apps keep running. The result
shows resident memory before and after; this includes file cache, and swapped
pages may still use compressed RAM. Savings vary and apps may briefly respond
more slowly when accessed again.

On supported hosts, each environment also gets one automatic 128 MiB reclaim
attempt after 15 minutes with no viewers. A returning viewer resets the timer,
even if they leave again between checks. Active CLI clients, environment operations,
and missing or stale activity reports delay reclamation without consuming the
attempt. Failed attempts are not retried during that absence. Checks run every
30 seconds and requests run one at a time. Supervisor/container restarts start
a fresh timer. Admins can turn off **Automatically reclaim idle memory** in an
environment's Settings; this saves immediately in its definition without a restart and survives
container recreation. The manual button remains available.

Automatic reclamation requires the environment's activity service to send current
command-status heartbeats. Older desktop binaries with only blank heartbeats
keep running normally but skip automatic reclamation; update the desktop image
to use it. Activity detection covers operations recorded by Deskyard, not plain
shell commands or other unreported background work; use the opt-out for those.

This requires a rootful Linux Docker engine with cgroup v2 and swap available
to the environment. It works through the Docker socket, including with the
supervisor running on macOS against OrbStack. The supervisor pulls a small
Alpine helper image if needed and removes its temporary helper containers
after each request. At boot it checks the engine's memory/swap controls and
tests the full helper path, including `memory.reclaim` with swappiness, on a
tiny disposable container. The log reports whether reclamation is enabled
and why it is disabled if a check fails. A failed check leaves the rest of
the supervisor running and disables the button with a reason. Restart the
supervisor after fixing host support to run the check again. Each request
still checks the selected environment's swap settings; a failure there does not
disable reclamation for other environments. No memory limits or host swap
configuration change.

### Images and upgrades

The **Images** page holds the catalog of images environments can be created
from. The built-in choice, visible before download and the default on first
start, is Debian XFCE: `ghcr.io/zefhemel/deskyard-debian-xfce:latest`. The
MATE image is retired: it is no longer offered or rebuilt, though environments
that already use it keep running on the copy they downloaded.

Choose **Install** on an available image to download it, or select it when
creating an environment to download it on first use. Browsing does not download
images. Existing saved images and the configured default retain their order;
custom images remain alongside the built-in choices. Official choices stay
in the catalog and cannot be removed. Other contract-compatible references
can still be added with **Add image**.

The desktop comes with everyday apps that open their files by default:
Ristretto for images, Mousepad for text, Evince for PDFs, Xarchiver for
archives, and Chromium for video and audio. Galculator, a screenshot tool, a
task manager and a Trash in the file manager complete it. Fonts cover emoji,
most scripts (Noto) and Arial/Times-compatible layouts (Liberation); Chinese,
Japanese and Korean fonts are not included. One full-width taskbar at the
bottom holds the app menu, launchers, open windows, tray and clock. The look is
Arc Dark with Papirus icons; Greybird (Xubuntu's theme) can be chosen in
Settings › Appearance. Existing environments move to the taskbar and the new
look once, unless their owner has already changed those settings. Command-line basics are there too:
`vim`, `nano`, `less`, `htop`, `wget`, `rsync`, `jq`, `file`, `lsof`,
zip/xz/bzip2/7-Zip, `ping`, `dig` and `nc`.

Open **Software** from the desktop application menu to find
and install Flatpak apps such as LibreOffice. Flathub is configured for the
desktop user; apps install in `/workspace` without an administrator password
and survive image upgrades. The first catalog refresh needs internet access.
LibreOffice is optional and is not bundled in the base images. Admins can
also preinstall apps in a separately built Dockerfile image.

LibreOffice installation, launch and saving into the workspace have been
verified. Apps requiring document-portal file grants have a current limitation:
the container configuration does not expose the FUSE device needed by the
document portal.

In local ARM64 samples, idle memory without a viewer was about 213 MiB for
XFCE. The project space's *Testing/Desktop variants 2026-10-23* page has the
conditions, viewer/browser measurements and screenshots. New registry tags
become available after the image workflow publishes from `main`; local Bake
builds use `deskyard-debian-xfce:dev`.

CI rebuilds it weekly (Debian security updates) and on every change, tagged `latest` and with the date
(`2026.10.22`). The supervisor checks downloaded images for newer versions daily (or on "Check
now"); available images stay undownloaded. The catalog shows **Available**,
**Installed** or **Update available** when environments are behind.
After each check and each upgrade, the supervisor deletes old versions of
desktop images that nothing uses any more: images no tag names (a newer pull
or build replaced them), that no container runs from, stopped ones included,
and that no environment pins. Tagged images, such as local builds and custom
images, are never deleted.

Upgrading an environment recreates its container on the new image with the same
settings and folder; the Images page can upgrade all environments on an image
at once. Switching an environment to another image works the same way.
Each definition pins the image ID its container was built from; if that image
has disappeared from the engine when the container must be recreated, the
supervisor pulls the image reference again and records the new ID as a new
revision. Upgrades
go forward only: Chromium won't open a profile written by a newer version.
During a settings save, progress appears above the image picker and scrolls
into view. Image downloads show transferred MB and a percentage when available;
the progress returns after reloading or leaving and reopening Settings.
Tools installed with mise, uv and npm bring their own runtimes and should
keep working across Debian versions; if one breaks, reinstall it.

Any image can be added to the catalog if it follows the image contract
(label `deskyard.contract=3`, user `user` with home `/workspace`,
Selkies behind Caddy on 8080); see Custom images below.

Images this project builds carry the label `deskyard.kind`: `desktop` for
the desktop image and `supervisor` for the supervisor. List them with
`docker images --filter label=deskyard.kind`.

### Custom images

Build custom images with Docker outside the supervisor, then use **Add image**
with the resulting image tag. The supervisor uses a local image when available,
checks its Deskyard contract, and pulls the reference only when it is missing.
Existing locally built tags, including `deskyard-custom/…`, remain usable.
Removing a catalog entry leaves its Docker image and any old source files intact.

The supervisor has no image editor, recipe builder or image-from-desktop export.
Its account and catalog JSON storage is unchanged; old recipe/source files remain
on disk but are no longer read or migrated. Images can still install tools in
`/opt/mise` with executables linked into `/opt/deskyard/bin`, install system
Flatpak apps, and seed empty workspaces through `/opt/deskyard/home-template`.
What an image must provide is specified by the *Desktop image protocol* spec
in the project space; building `FROM` an official Deskyard image is the
supported way to meet it.

For a standalone Dockerfile you can copy and build separately, see the
[RetroArch example](examples/retroarch/README.md). It installs RetroArch and
five source-built cores for a new empty workspace, with no games or BIOS files.
Snes9x and Genesis Plus GX carry non-commercial restrictions; see the example's notices.

### Flatpak

Desktop images include the upstream Flatpak fix for intermittent GL-extension
`invalid merge-dirs` launch errors. It retries temporary directory-resolution
failures while retaining Debian's security patches. Existing environments receive
it through an image upgrade. The rebuilt packages' corresponding source is
included at `/usr/share/doc/flatpak/deskyard-source/`.

Flatpak apps run inside bubblewrap, which needs namespaces that Docker's
defaults block. Every environment therefore runs with Docker's default seccomp
profile plus the namespace and mount syscalls, `apparmor=unconfined` and
`systempaths=unconfined`. These are per-container options; nothing in
Docker's configuration changes. Chromium also uses them to run with its own
sandbox.

`systempaths=unconfined` leaves `/proc/sys` writable, and root in the
container is root on the host for those files. So nothing reachable by
desktop users or the internet runs as root in an environment: there is no `sudo`, Caddy
runs as its own user, and the supervisor runs every command as `user`.

On Ubuntu 24.04+ hosts, unprivileged user namespaces may be restricted by
AppArmor (`kernel.apparmor_restrict_unprivileged_userns`); this hasn't been
tested yet. If bubblewrap fails there, try
`sudo sysctl kernel.apparmor_restrict_unprivileged_userns=0`.

### AppImages

AppImages normally mount themselves with FUSE, which an unprivileged desktop
does not have. Desktop images set `APPIMAGE_EXTRACT_AND_RUN=1`, so an AppImage
unpacks itself to `/tmp` and runs from there instead. Each launch unpacks it
again, so large AppImages start more slowly; Flatpak is the better choice when
the app is available there.

Every home has an `~/Applications` folder, with a short `README.txt` the first
time it is created. To put an AppImage in the applications menu, save or move
it into `~/Applications`, or right-click it in the file manager and choose
**Add to Applications Menu**, which moves it there. A launcher with the app's
own name and icon appears within seconds; from the menu you can pin it to the
dock. Deleting the AppImage removes its launcher. `deskyard-appimage` does the
work (`add FILE`, `sync`); a background watcher runs it whenever
`~/Applications` changes. Launchers keep the app's MIME types, so an AppImage
can become the handler for the files it declares.

## Sharing

Accounts are a username and a password; there are no email resets or SSO.
Admins create accounts, set passwords and make accounts admin or not, and
choose per environment which accounts may use it. Everyone can change their own
password.

| | Admin | Regular account |
| --- | --- | --- |
| See, open, start, stop, restart environments | all | those it has access to |
| Create, remove, change settings, upgrade | yes | no |
| Reclaim memory | yes | no |
| Create access tokens | yes | for environments it has access to |
| Accounts, access, image catalog | yes | no |

Everyone with access to an environment opens the **same desktop**: one screen,
one mouse, one keyboard, like two people at one desk. The desktop follows
the browser window of whoever resized last. The supervisor shows who's
viewing on the tiles, the details page and the desktop page's bar, but nothing
arbitrates between people and CLI clients driving it at the same time.

## Remote access and control

Use `deskyard` from your laptop or another machine with an **access token**.
Anyone with access to an environment creates one on its **Remote access** tab,
with a name and optional expiry. Copy the setup command when it is shown;
the token is only displayed once. Install `deskyard` on your machine
(see Install), then:

```bash
deskyard remote add alice https://desktops.example.com/alice --token dyt_...
deskyard -e alice exec -- ls ~            # one command; exit code passed through
deskyard -e alice shell                   # interactive shell
deskyard -e alice terminal                # shared terminal; rejoin later
deskyard cp ./notes.md alice:/workspace/notes.md    # and back
deskyard -e alice screenshot > s.png      # every screen action works remotely
deskyard -e alice click 640 400
deskyard -e alice browser connect --port 9222   # the shared Chromium on local 127.0.0.1:9222
```

With `browser connect` running, Playwright's
`chromium.connect_over_cdp("http://127.0.0.1:9222")` (use
`browser.contexts[0]`, the shared profile), Puppeteer or chrome-devtools
tools work as if the browser were local. `-e NAME` (or `--environment NAME`)
goes before the command; `-d` is no longer accepted. `deskyard remote default alice`
saves typing `-e`; `DESKYARD_URL` and `DESKYARD_TOKEN` work without a config
file. For command help, use `deskyard --help`, `deskyard guide` (a short
manual) and `deskyard describe --json` (machine-readable command descriptions).

A token reaches one environment's shell, files, browser and desktop, nothing
else: no other environments, no lifecycle, settings or accounts. Start a stopped
environment from the web UI before connecting. Revoking a token, or its expiry,
closes its open connections at once, and a token dies with its creator's access.
A token also dies with its environment: removing an environment and creating a
new one with the same name does not bring back its old tokens or access grants.

### Port mapping

Reach a server on one machine from the other, with the same access token:

```bash
deskyard -e alice forward 3000 &      # your 127.0.0.1:3000 is localhost:3000 in the environment
deskyard -e alice forward -R 5173 &   # the environment's localhost:5173 is your 127.0.0.1:5173
```

The first lets the environment call a service running on your machine; the second lets you open a dev server running in the environment in your own browser. To use different port numbers, write `LOCAL:ENV` for a plain mapping (`8080:3000`: your 8080 is the environment's 3000) and `ENV:LOCAL` after `-R` (`-R 80:8080`: the environment's 80 is your 8080). Several mappings can share one command (at most 64 plain ones and 256 in all), and one token can hold four such links per environment. Everything goes over the existing HTTPS connection (no extra ports are opened on the engine host) and listens only on `127.0.0.1`. Only *listening* in the environment is limited to ports from 1024 up, so plain `forward 80` is refused while `-R 80:8080` works (the helper just dials). If the connection drops or the environment restarts, `deskyard` reconnects by itself with backoff (up to 30 s): `-R` local ports stay bound meanwhile, and plain mappings come back in the environment once the link is back. People watching the desktop see which ports your token maps (up to 64 per token are listed). "Linked to …" and the `out` lines are printed when the link is accepted, a moment before the helper in the environment is known to be running.

## Optional agent integrations

The desktop also ships skills for tools such as Claude Code and Codex.

**Inside an environment.** Install an agent into `/workspace` from its
terminal and run it there:

```bash
curl -fsSL https://claude.ai/install.sh | bash          # Claude Code
npm i -g @openai/codex                                   # Codex
```

Hermes Agent also runs inside an environment:

```bash
curl -fsSL https://hermes-agent.nousresearch.com/install.sh -o /tmp/hermes-install.sh
bash /tmp/hermes-install.sh --non-interactive --skip-browser --skip-computer-use
hermes config set skills.external_dirs '["/opt/workspace/skills"]'
LANG=C.UTF-8 hermes model   # choose a provider and sign in
LANG=C.UTF-8 hermes
```

This installs Hermes into `/workspace`, uses its local terminal backend,
and makes the image's three skills available to it. The optional Hermes
browser and computer-use downloads are skipped; the environment's shared
browser and `deskyard` command remain available through those skills.
The project space's Hermes Agent trial spike records the
verified versions, startup checks and limitations. A model conversation
still needs provider credentials.

Older desktop images export `UV_PYTHON_PREFERENCE`, which conflicts with
the installer's explicit Python flags. On those images, prefix the
`bash /tmp/hermes-install.sh ...` command with
`env -u UV_PYTHON_PREFERENCE`, or upgrade the desktop image.

The image links its skills into `~/.claude/skills`, `~/.codex/skills`,
`~/.agents/skills` and `~/.gemini/skills` on every start:

- `workspace-computer`: what this machine is, how to install things so they
  persist, what not to touch.
- `workspace-browser`: drive the shared Chromium over CDP (on
  `127.0.0.1:9221` inside); ask the human to log in.
- `workspace-computer-use`: the `deskyard` command.

They update with the image. To customise one, replace its link with a real
directory; it won't be touched again.

## Configuration

Environment variables of the supervisor:

| Variable | Default | |
|---|---|---|
| `DESKYARD_LISTEN` | `127.0.0.1:7343` (`0.0.0.0:7343` in the image) | address for plain HTTP |
| `DESKYARD_DOMAIN` | | serve HTTPS for this domain on 80/443, with an automatic certificate |
| `DESKYARD_DATA_DIR` | `~/deskyard` (required in a container) | environment definitions, home folders and the supervisor's own state (see Storage) |
| `DESKYARD_ENGINE_HOST` | `DOCKER_HOST`, else the default socket | the engine to drive |
| `DESKYARD_SUBNET` | `10.213.0.0/16` | each environment's network gets a /28 from it |
| `DESKYARD_ADMIN_USER` | `admin` | first start only: the first admin |
| `DESKYARD_ADMIN_PASSWORD` | generated, printed once | first start only: its password |
| `DESKYARD_IMAGE` | `ghcr.io/zefhemel/deskyard-debian-xfce:latest` | the catalog's first image |

### Storage

`DESKYARD_DATA_DIR` keeps everything in plain view; there is no hidden folder:

```
<data>/
  environments.json   environment definitions (no secrets)
  accounts.json       accounts, password hashes and environment grants
  tokens.json         access tokens (hashed)
  networks.json       saved VPN connections (no credentials)
  images.json         the image catalog
  volumes.json        named volumes (grants live in environments.json)
  storage-remotes.json  storage remotes (no credentials)
  backups.json        backup repositories and policies
  secret/             session key, per-environment proxy tokens (proxy/<name>),
                      VPN service credentials (vpn/<connection-id>/), TLS certificates (certmagic/),
                      storage remote credentials and rclone.conf (remotes/<name>/),
                      backup recovery keys (backups/<repository>/)
  state/              this supervisor's instance ID, replacement records (recovery/<name>.json),
                      VPN network transitions (network/transitions.json), VPN runtime scratch (vpn/),
                      backup run state (backups.json)
  cache/              Selkies web clients extracted from images, restic's cache and binary; safe to delete
  home/<name>/        each environment's /workspace, owned by UID 1000
  volume/<name>/      each local volume's files
```

JSON files are written atomically with mode 0600, and existing `accounts.json`
and `tokens.json` files are tightened to 0600 at startup; `secret/` and `state/` are
owner-only (0700). Host administrators can read these secrets. Back up the
whole data folder; `cache/` can be left out. Deskyard's own backups cover
environments and volumes, not this folder's settings and secrets: keep the
recovery keys somewhere else.

`environments.json` is the only place an environment's settings live. The
supervisor reads it strictly: an unknown field, a duplicate name or an invalid
entry stops it from starting rather than being ignored. Containers and networks
carry only matching labels: `deskyard.managed`, `deskyard.name`,
`deskyard.revision` (the definition revision the container was built from) and
`deskyard.instance` (the ID in `state/instance`), plus role labels for VPN
gateways. Labels are never read back as settings. A supervisor considers only
containers with its own instance label, so several supervisors with different
data folders can share one engine without seeing each other's containers.

During settings changes and upgrades, `state/recovery/<name>.json` records the
replacement in progress (the revision, the set-aside container
`deskyard-<name>.parked` and whether it was running). It never holds settings.
A failed replacement restores the old container; startup finishes or reverts an
interrupted one after reconnecting container networks. If that fails, the record
stays and operations on that environment are refused with an explanation. Fix
the reported engine, image or host-volume issue and restart the supervisor to
retry. VPN environments keep their transition state in
`state/network/transitions.json`, so interrupted network changes stay visible and
recoverable without falling back to direct networking.

Data folders from before environment definitions are not migrated. The
supervisor refuses to start, and says what to remove, when the data folder holds
a `.deskyard/` folder or when Deskyard containers without an instance label
mount it. Remove those (or point `DESKYARD_DATA_DIR` at a fresh folder); this
mostly affects development checkouts.

In a container, the data folder must be mounted **at the same path** as on
the host (`-v /srv/deskyard:/srv/deskyard`), because the supervisor hands
those paths to the engine as bind mounts for environments. It checks this on
startup and refuses to run, with an explanation, if they don't match.

## Security

- **The engine socket is root on the host.** Whoever can use it owns the
  machine, so the supervisor is the most privileged thing there. Run
  directly on Linux, its user needs the `docker` group, which is
  root-equivalent; in a container, mounting the socket is the same.
- Every route but the login page needs a signed-in account; access tokens
  work only on the remote access API. Failed logins are throttled.
- Environments have no password and nothing else of their own facing the
  network: only Caddy on 8080, which wants a per-environment token that only
  the supervisor has. With the supervisor in a container, environments publish
  no ports; run directly, each publishes only 8080 on host `127.0.0.1`.
- The supervisor never serves pages from an environment: it serves the desktop
  client itself (taken from the environment's image) and proxies only the
  desktop's WebSocket and, for CLI clients, the browser's CDP.
- No `sudo` in environments, and every command the supervisor runs in one runs
  as `user`.
- Over plain HTTP, passwords travel in clear text: keep the default
  `127.0.0.1` binding unless the network is trusted, or set
  `DESKYARD_DOMAIN` for HTTPS.

## Repository layout

```
supervisor/
  cmd/deskyard-supervisor/     the supervisor
  cmd/deskyard/     the deskyard command (inside environments and for remote access)
  internal/         engine interface and Docker implementation, accounts, auth,
                    environments, storage remotes and backups, the HTTP server
                    and proxy, the embedded UI
  web/              the UI: Preact + TypeScript + Tailwind, built with Vite
  tests/            integration tests against a real engine
  Dockerfile        node build -> go build -> alpine
  compose.yaml      the supervisor in a container
images/
  Dockerfile       build stages and the XFCE desktop target
  common/          shared rootfs and streaming patches
  debian-xfce/     XFCE rootfs overlay
  tests/           shared desktop image tests
docs/               local copy of the SilverBullet project space (ignored by git)
```

CI: `.github/workflows/images.yml` builds all `docker-bake.hcl` targets together for
amd64 and arm64 on native runners, weekly and on change;
`.github/workflows/supervisor.yml` tests the supervisor, publishes its image,
builds the `deskyard-supervisor` and `deskyard` binaries and puts them in the
`edge` pre-release (main) or the GitHub release (tags `v*`);
`.github/workflows/tests.yml` runs the integration suites (see Tests).
`install.sh` installs `deskyard` from a release.

## Development

The supervisor needs Go and Node 24:

```bash
cd supervisor
make build          # the UI, then bin/deskyard-supervisor and bin/deskyard
make test           # Go unit tests, with the race detector
make test-engine    # the tests that drive a real Docker engine (builds the
                    # desktop image first, unless CC_TEST_IMAGE names one)
make typecheck      # the UI's TypeScript and tests
make vet            # Go static checks
make release VERSION=v1.2.3   # cross-compiled binaries in dist/
make image          # the supervisor's container image
```

### Project records

Project documentation lives in the SilverBullet space "Container Computer", which is synced and versioned outside this repository. A local copy sits in `docs/`, which git ignores; edit pages there or in SilverBullet and let sync carry them, rather than committing them. Links from this README name pages in that space instead of linking files.

The space's *Operations manual* describes how the owner and agents plan, work, review feedback, verify outcomes and maintain the records. Its *index* page is the owner dashboard: requests, current capabilities and unscheduled loose ends through live SilverBullet views. No baked sections are used. The space tracks features, outcome-based milestones, design specs, implementation plans, components, spikes and verification evidence. Its *AGENTS* page (the documentation manual) defines their fields and lifecycle; its *CONFIG* page registers native SilverBullet schemas. Use SilverBullet comments addressed to `@agent` for contextual feedback; the *Agent inbox* page collects open requests for agents to check during work. Invoke `$sb-inbox` to process that queue explicitly; its repository skill lives in `.agents/skills/sb-inbox/`. Milestones have no target dates. Completed implementation plans are purged after durable decisions and evidence are retained; design specs stay while they describe the current system or agreed future work. The space's remote versioning preserves the history. From the repository root, with SilverBullet Desktop available, check metadata and relationships using `sb --space "Container Computer" script --file scripts/check-docs.lua --json`; run `sb` outside an agent sandbox so it can reach Desktop.

### Terminal access prototype

The historical terminal access experiment (a spike in the project space)
used a separate local demo. Shared terminal access above supersedes that experiment in the normal desktop image.

### Tests

From the top folder; the pytest suites need Docker, [uv](https://docs.astral.sh/uv/)
and, once, `uv run playwright install chromium` in each of
`images/tests` and `supervisor/tests`:

| Command | Runs | When |
|---|---|---|
| `make test` | Go (vet, race) and engine tests, the UI's, and both pytest suites without what's slow, needs the internet, builds images, measures timing, or runs WebKit. About 7 minutes on a quiet Mac, 10 under load. | Before every commit. |
| `make test-ci` | `make test` plus WebKit and Firefox: what CI runs on a pull request. | To check a browser difference. |
| `make test-all` | Everything, the supervisor suite also with the supervisor in a container. About 25 minutes; needs the internet. | Before a release, or after changing desktop Dockerfiles, image runtime behavior, audio timing, or how the supervisor runs in a container. CI runs it on main and nightly. |
| `make test-image`, `test-supervisor`, `test-container`, `test-go`, `test-web`, `test-engine` | One part. | While working on it. |

While working on one area, narrow a suite: `make test-supervisor PYTEST_ARGS="-k drop"`,
or run pytest in the suite's folder (`uv run pytest test_ui_audio.py`).
`MARKS="-m ''"` lifts the default selection; markers: `slow`, `network`,
`build`, `perf`, `webkit` (left out by default) and `serial`.

Both suites run under pytest-xdist (`WORKERS`, default 4), then run the
timing-sensitive tests, marked `serial`, on their own (`pytest --only-serial`).
Each test gets a new environment, taken from a few started ahead of it. The
supervisor suite runs a supervisor per worker, each on its own slice of
`DESKYARD_TEST_SUBNET` (default 10.215.0.0/20): give checkouts that test at the
same time different ones. Teardown removes only the containers and networks labelled with that run's supervisor instance, plus managed containers mounting its data folder. The supervisor suite rebuilds the web UI first when
its sources are newer than the build. `TEST_IMAGE` names the desktop image the
suites build and test (default `deskyard-debian-xfce:test`); outside make,
`AC_IMAGE` (image suite) and `CC_TEST_IMAGE` (supervisor suite) name it.
Firefox is skipped where Playwright's Firefox can't start (on some Macs).
On Linux, tests write workspace fixtures through the desktop as UID 1000,
since the runner may not own the workspace. In Chromium, loopback bootstrap API GETs, UI assets, login and mic badge POSTs use
Playwright's request transport to avoid Chromium's `ERR_NETWORK_CHANGED` when
Docker changes network interfaces. Each request is sent once, including login
POSTs; other mutations, downloads, live streams and Selkies' client use the browser. Documents stay on the native browser transport to preserve their network address space for event WebSockets. Navigation
on the browser transport retries that specific error; other browser errors still
fail. Engine tests pull their own auxiliary images,
including `python:3.13-alpine` for memory reclamation.
The plain-HTTP test hostnames forward only bootstrap API calls, preserving their
insecure document origin and native screen connections. Other browsers use their
native transport throughout.

```bash
docker buildx bake --load     # the desktop image
cd images/tests
uv run playwright install chromium   # once
uv run pytest -m "not slow"
uv run pytest                        # also: memory budget, Flatpak app, CLIs
```

Screenshots and stats land in `images/tests/artifacts/`. `AC_IMAGE=<tag>`
skips the build and tests that image instead, for example:

```bash
AC_IMAGE=deskyard-debian-xfce:dev uv run pytest -m "not slow and not network"
```

For shared-engine VPN verification, use the [scoped VPN test guide](supervisor/tests/VPN.md). Its `vpnintegration` test binary protects existing resources with a unique ownership label. Do not start an ordinary production supervisor or the full-main Python fixture against a Docker engine holding unrelated environments.
