---
displayName: Desktop image variants design
tags: spec
status: implemented
date: "2026-10-23"
features:
  - "[[Features/Images and reusable environments]]"
  - "[[Features/Persistent workspace]]"
---
XFCE and MATE are the two built-in Debian trixie desktops. One shared runtime supplies streaming/audio, Chromium/CDP, Flatpak, CLI/skills, users, workspace initialization and service supervision. KDE support was removed; saved KDE references remain ordinary prebuilt catalog entries, without current built-in support.

# Build and isolation
`images/Dockerfile` contains shared CLI/codec/runtime stages and final `debian-xfce` and `debian-mate` targets. `docker-bake.hcl` builds their shared graph together per architecture; the runtime stage is neither a published dependency nor a selectable image. Common files/patches live in `images/common/`; final overlays live in `images/debian-{xfce,mate}/rootfs/`. Shared configuration follows desktop package installation to preserve package-layer caching.

Both images retain contract 3, UID/GID 1000, `/workspace`, display `:1`, Caddy 8080 and the private proxy-token boundary. Desktop layers add their own packages/session defaults without new authentication or streaming protocols. Curated package sets avoid display managers, sudo and optional privileged host-management helpers. Both ship GNOME Software with the Flatpak plugin and per-user Flathub. Optional distro-management D-Bus activation is disabled.

# Sessions and persistence
A common `start-session` coordinates X readiness, shutdown and Chromium restoration through a fixed image-owned desktop adapter. Sessions run as `user`; no user-controlled startup file runs as root. Native logout gets time to save state. Shared Chromium closes through CDP with a bounded profile-scoped termination fallback before native logout, and its open marker determines restoration.

Existing workspaces preserve files, browser profile, installed user tools and native desktop settings when changing images. Defaults apply without rewriting saved choices; desktop-specific configuration remains namespaced. Cross-desktop window restoration is not promised. Native terminals use shared shells by default, with independent readable session names; explicit command/restoration argv retains its meaning.

MATE uses Arc/Papirus/Noto, the original blue wallpaper and a native 40px dark bottom-panel layout. Native GSettings defaults preserve existing values/layouts. User-mode scoped CSS seeding skips existing files and symlinks. See [[Testing/Theme previews 2026-10-23]].

# Catalog and applications
Static built-in metadata exposes Available, Installed and Update available choices before download. Browsing does not mutate the catalog or pull images. Saved entries retain ordering and normalized-reference deduplication; XFCE remains the seeded default and `DESKYARD_IMAGE` retains its meaning. Administrators can install built-ins or add arbitrary compatible prebuilt references. Supervisor recipes/build authoring are removed.

Flatpak installations remain per user in `/workspace`; graphical stores use libflatpak directly, so a CLI wrapper alone does not prove GUI scope. The current engine does not expose `/dev/fuse` for document-portal mounting. LibreOffice's allowed workspace access and graphical saving were verified; applications depending on document-portal grants remain limited. Debian Flatpak CLI/library builds retain security patches plus the temporary `openat2(EAGAIN)` retry backport and ship corresponding source.

# Verification
Shared image tests live in `images/tests/`, selected with `AC_DESKTOP=xfce|mate` and optionally an existing `AC_IMAGE`. Layout assertions remain desktop-specific. CI smoke checks each supported image's readiness, identity, browser, shared terminal and home persistence; full supervisor/browser suites continue to use XFCE. Native ARM64 measurements and scope are in [[Testing/Desktop variants 2026-10-23]]. Local builds do not establish amd64 runtime coverage or registry publication. Startup and X-input changes have separate evidence and pending coverage in [[Testing/Desktop startup readiness 2026-10-24]] and [[Testing/MATE stream startup 2026-10-24]].
