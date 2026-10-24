---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Shared terminal]]"
  - "[[Architecture/Session]]"
  - "[[Architecture/Selkies]]"
  - "[[Architecture/Chromium]]"
  - "[[Architecture/Caddy]]"
  - "[[Architecture/Desktop tools]]"
spec: "[[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]]"
---
The common Debian trixie image layer beneath XFCE and MATE. It provides streaming and audio, the shared browser, desktop tools, user/workspace initialization, Flatpak and service supervision. `images/Dockerfile` contains the shared build stages and the two final desktop targets; `docker-bake.hcl` builds them together once per architecture, reusing the expensive CLI and patched codec dependencies. The common layer is an internal build stage, not a separately published or selectable desktop.

Shared configuration is copied after desktop package installation so a startup-script change does not rebuild desktop packages. Shared files live in `images/common/rootfs/` and patches in `images/common/patches/`. Desktop packages, native session adapters and defaults live in the respective final layers and `images/debian-{xfce,mate}/rootfs/`. XFCE and MATE ship GNOME Software and its Flatpak plugin. The stores use libflatpak directly and the per-user Flathub remote, keeping installations in `/workspace`; the CLI wrapper alone does not enforce GUI installation scope. Matching GTK portal packages are installed, but the document portal cannot mount its FUSE filesystem under the current engine configuration. Apps depending on document-portal file grants remain a limitation; LibreOffice's permitted workspace access and graphical saving were verified.

A build-time finalizer disables optional host-management D-Bus helpers and any installed PackageKit daemon. No sudo command is available to the user.

Contract 3, the Selkies client path, proxy boundary, UID/GID 1000, display `:1` and `/workspace` match the existing XFCE image. Shared tests live in `images/tests/`; XFCE panel migrations remain desktop-specific. Native ARM64 builds and runtime checks are recorded in [[Testing/Desktop variants 2026-10-23|Desktop variants testing — 23 October 2026]]; registry publication and amd64 runtime checks remain separate release checks.

MATE's overlay provides the selected Arc/Papirus/Noto appearance, original blue SVG wallpaper and native `deskyard.layout` single bottom panel. Compiled GSettings defaults apply without rewriting saved user values or panel layouts. Its user-mode session adapter seeds the scoped panel CSS only when `~/.config/gtk-3.0/gtk.css` does not already exist (including symlinks). See [[Testing/Theme previews 2026-10-23|Desktop theme previews — 23 October 2026]] for the selected visual direction.

All desktop variants use the common `deskyard-terminal-configure` startup migration and Deskyard Terminal desktop entry. Default native shell launches create independent shared sessions; saved terminal command preferences are migrated on every desktop start. XFCE and MATE retain their native emulators, option parsing, explicit command execution and native restoration. See [[Architecture/Shared terminal]] for runtime and discovery ownership.

The `flatpak` build stage rebuilds Debian's current signed Flatpak source package with the upstream libglnx `openat2(EAGAIN)` retry backport (`images/common/build-flatpak.sh`, `images/common/patches/flatpak-openat2.diff`). This prevents intermittent GL-extension `invalid merge-dirs` launch failures while retaining Debian's security patches and directory-confinement flags. Both the CLI and `libflatpak` used by graphical stores come from the rebuilt packages. The build links `images/tests/native/flatpak-openat2.c` against those same libglnx objects and verifies retry, permission-error propagation and rejection of parent-directory escape. The build fails if the patch no longer applies, so an upstream package that incorporates it requires removing the backport. Corresponding Debian source, including this patch, is shipped in `/usr/share/doc/flatpak/deskyard-source/`; extract its `.dsc` with `dpkg-source -x` and rebuild with Debian's normal package tools.

The opt-in **Docker daemon access** desktop setting supplies the daemon-side `/var/run/docker.sock` and `DESKYARD_DOCKER_ACCESS=1`. Trusted root startup adds `user` to the mounted socket's numeric group before any user services start, reusing existing groups and preserving other memberships. It never changes socket ownership or permissions. Ordinary desktops receive neither mount nor grant. This needs an updated base image; installing Docker client tools alone does not provide access. `examples/docker/` is a standalone Dockerfile build context with client tools and a build/run demonstration.

Startup readiness is a barrier, not merely an available X socket. `start-xvfb` bounds the initial handshake and every RandR operation, verifies the requested actual dimensions, and only then publishes `xvfb-ready`. Failure reports the stage, removes readiness and terminates/reaps its owned X server before supervisord retries. `start-selkies` requires both that barrier and `desktop-ready` (the native panel/window manager) before audio/stream setup; it exits for a supervised retry instead of serving an empty or unfinished desktop. This prevents delayed initial mode selection from overwriting a viewer-selected size. Local command-failure tests and isolated delayed-start browser verification are recorded in [[Testing/Desktop startup readiness 2026-10-24|Desktop startup readiness — 24 October 2026]].
