# Storage

Volumes shared between environments, WebDAV drives, backups, and the supervisor's data folder.

## Volumes

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
the machine running the container engine; on macOS the container runtime must
share the folder. A host folder can't be the data folder or one of its own
folders (`home`, `volume`, `secret`, `state`, `cache`), whether named by its
host path or under `/data`.

Deleting a volume is refused while any environment's settings grant it, running
or stopped, and the refusal names those environments; remove the grants first.
It is also refused while a container still mounts it because a grant's removal
hasn't been applied yet, and while a backup or restore of it runs. Deleting
keeps the files unless you tick "Delete the files", which isn't offered for a
host folder. Removing an environment, even with its files, never touches
volumes. If a volume's folder goes missing, the environment reports an error and
doesn't start until the folder is back; Deskyard never recreates it. Volumes are
never written by file uploads, which go into `/workspace/Desktop`. There is no CLI for volumes yet; they are managed in the web UI
or through `/api/volumes`.

Per-environment host folders were replaced by host volumes. A data folder whose
`computers.json` still has host folders (`mounts`) isn't migrated: the
supervisor refuses to start and names the environment; remove its folders with
the previous version, or delete the `mounts` entries, then create host volumes.

## WebDAV drives

An environment's files can be mounted as a drive on your own computer,
also while the environment is stopped. An admin switches it on per
environment on its **Access** tab, with **Serve files over WebDAV**; it is
off by default. Others with full access see the drive's address there
while it is on. The drive is at `https://<your Deskyard>/<name>/dav/` and
shows the environment's own paths: `workspace/` (the home folder) and each
local volume at its path, such as `share/photos/`. Read-only volumes stay
read-only. Host volumes aren't included.

Anyone with full access and a password can connect, with their username
and password; observers and accounts that sign in with links can't. Your
page (the account menu → **Account and devices**) lists your drives and can
make a separate **WebDAV password**: while you have one, WebDAV takes only
that password, so your account password isn't stored in WebDAV clients.
Removing it, or signing out everywhere, disconnects them.

- **macOS:** in Finder, **Go → Connect to Server**, enter the address and
  sign in. Finder's `.DS_Store` and `._` files are accepted but not kept,
  so Finder tags don't travel with files. WebDAV has no symlinks: Finder
  gives up on a folder that contains one ("One or more items can't be
  copied", and Skip copies nothing more of it). Remove the links first, or
  copy in Terminal with `cp -R`, which skips them and copies the rest.
- **iPhone and iPad:** the Files app's own Connect to Server speaks SMB
  only; use a WebDAV app such as Owlfiles, which adds the drive to Files.
- **Windows:** **Map network drive** with the address. Windows refuses
  passwords over plain HTTP, so it needs HTTPS.
- **Linux:** your file manager's **Connect to Server** with `davs://` (or
  `dav://` on plain HTTP), davfs2, or rclone's WebDAV remote.

Without HTTPS the password crosses the network readable; the settings and
your page warn when that is the case. Files opened from the drive in a
browser are always downloaded rather than shown.

## Backups

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

**Other backups** on the Backups tab lists what the repositories hold besides
this Deskyard's current environments and volumes (removed ones, other
machines'), to restore as new. rclone and restic run in short-lived helper containers
(`rclone/rclone:1.75.2`, with restic 0.19.1), which get credentials only as
read-only files. To read a repository without Deskyard:
`restic -r rclone:<remote>:<root>/<folder> snapshots`, with the remote's
`secret/remotes/<name>/rclone.conf` as `RCLONE_CONFIG` and the recovery key as
the password. Snapshots are tagged `computer:<name>` or `volume:<name>`
(an environment's snapshots taken before it was called a computer keep the tag
`environment:<name>`, and are listed, restored and pruned with the newer ones);
an environment's are under `/deskyard/environment/<name>/` (`home/` and
`definition.json`), a volume's under `/deskyard/volume/<name>/`.

## The data folder

The data folder is `/data` inside the supervisor's container: any host
folder, bind-mounted there (`-v /srv/deskyard:/data`). It keeps everything in
plain view; there is no hidden folder:

```
<data>/
  computers.json      environment definitions (no secrets)
  accounts.json       accounts, password hashes and environment grants (full or observe)
  links.json          unused login links (hashed)
  credentials.json    signed-in CLIs' client credentials (hashed)
  networks.json       saved VPN connections (no credentials)
  images.json         the image catalog
  volumes.json        named volumes (grants live in computers.json)
  storage-remotes.json  storage remotes (no credentials)
  backups.json        backup repositories and policies
  secret/             session key, per-environment proxy tokens (proxy/<name>),
                      VPN service credentials (vpn/<connection-id>/), TLS certificates (certmagic/),
                      storage remote credentials and rclone.conf (remotes/<name>/),
                      backup recovery keys (backups/<repository>/)
  state/              this supervisor's instance ID, the ownership marker (owner), replacement records (recovery/<name>.json),
                      VPN network transitions (network/transitions.json), VPN runtime scratch (vpn/),
                      backup run state (backups.json)
  cache/              Selkies web clients extracted from images, restic's cache and binary; safe to delete
  home/<name>/        each environment's /workspace, owned by UID 1000
  volume/<name>/      each local volume's files
```

JSON files are written atomically with mode 0600, and existing `accounts.json`,
`links.json` and `credentials.json` files are tightened to 0600 at startup. A
`tokens.json` left from a version with per-environment access tokens is deleted
at startup with a log line: those tokens are replaced by accounts, and an
`environments` list in `accounts.json` is read as full grants; `secret/` and `state/` are
owner-only (0700). Host administrators can read these secrets. Back up the
whole data folder; `cache/` can be left out. Deskyard's own backups cover
environments and volumes, not this folder's settings and secrets: keep the
recovery keys somewhere else.

`computers.json` is the only place an environment's settings live (an older
`environments.json` is renamed to it at start). The
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
mount it. Remove those (or mount a fresh folder at `/data`); this
mostly affects development checkouts.

The folder must be a bind mount, not a named volume. The supervisor finds its
host path by inspecting its own container and translates the paths it hands the
engine for environments' bind mounts, so the host path and `/data` can differ.
The web interface and the API show homes' and volumes' folders by their host
paths, where you find the files.
`home/` and `volume/` folders it creates are owned by UID 1000; on first start
with a data folder from an older version it re-owns the supervisor's files to
1000:1000 once (marked by `state/owner`). A `DESKYARD_DATA_DIR` set to anything
but `/data` is refused at start with the mount to use instead; see
[Running Deskyard](running.md#the-container).
