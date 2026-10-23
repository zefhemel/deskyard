---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Session]]"
  - "[[Architecture/Selkies]]"
  - "[[Architecture/Chromium]]"
  - "[[Architecture/Caddy]]"
  - "[[Architecture/Agent tooling]]"
spec: "[[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]]"
---
The common Debian trixie image layer beneath XFCE, KDE Plasma and MATE. It provides streaming and audio, the shared browser, agent tools, user/workspace initialization, Flatpak and service supervision. `images/Dockerfile` contains the shared build stages and the three final desktop targets; `docker-bake.hcl` builds them together once per architecture, reusing the expensive CLI and patched codec dependencies. The common layer is an internal build stage, not a separately published or selectable desktop.

Shared configuration is copied after desktop package installation so a startup-script change does not rebuild desktop packages. Shared files live in `images/common/rootfs/` and patches in `images/common/patches/`. Desktop packages, native session adapters and defaults live in the respective final layers and `images/debian-{xfce,kde,mate}/rootfs/`. KDE ships Discover and the Flatpak backend; XFCE and MATE ship GNOME Software and its Flatpak plugin. The stores use libflatpak directly and the per-user Flathub remote, keeping installations in `/workspace`; the CLI wrapper alone does not enforce GUI installation scope. Matching GTK/KDE portal packages are installed, but the document portal cannot mount its FUSE filesystem under the current engine configuration. Apps depending on document-portal file grants remain a limitation; LibreOffice's permitted workspace access and graphical saving were verified.

A build-time finalizer disables optional host-management D-Bus helpers. Debian Discover requires PackageKit as a package dependency; its backend plugin and service activation are removed and its daemon is diverted into the root-only disabled directory with mode 000. KDE's required sudo package remains recorded in dpkg, but its executable is diverted into a root-only directory with mode 000; there is no sudo command available to the user.

Contract 3, the Selkies client path, proxy boundary, UID/GID 1000, display `:1` and `/workspace` match the existing XFCE image. Shared tests live in `images/tests/`; XFCE panel migrations remain desktop-specific. Native ARM64 builds and runtime checks are recorded in [[Testing/Desktop variants 2026-10-23|Desktop variants testing — 23 October 2026]]; registry publication and amd64 runtime checks remain separate release checks.

MATE's overlay provides the selected Arc/Papirus/Noto appearance, original blue SVG wallpaper and native `deskyard.layout` single bottom panel. Compiled GSettings defaults apply without rewriting saved user values or panel layouts. Its user-mode session adapter seeds the scoped panel CSS only when `~/.config/gtk-3.0/gtk.css` does not already exist (including symlinks). KDE keeps its original appearance. See [[Testing/Theme previews 2026-10-23|Desktop theme previews — 23 October 2026]] for the selected visual direction.
