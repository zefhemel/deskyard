# Images

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
Selkies behind Caddy on 8080); see [Custom images](#custom-images).

Images this project builds carry the label `deskyard.kind`: `desktop` for
the desktop image and `supervisor` for the supervisor. List them with
`docker images --filter label=deskyard.kind`.

## Custom images

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
[RetroArch example](../examples/retroarch/README.md). It installs RetroArch and
five source-built cores for a new empty workspace, with no games or BIOS files.
Snes9x and Genesis Plus GX carry non-commercial restrictions; see the example's notices.

## Flatpak

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

## AppImages

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
