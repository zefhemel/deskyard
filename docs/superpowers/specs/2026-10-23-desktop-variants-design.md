---
displayName: Desktop image variants design
tags: spec
status: implemented
date: "2026-10-23"
plan: "[[superpowers/plans/2026-10-23-desktop-variants|Desktop image variants plan]]"
---
Offer Debian XFCE, KDE Plasma and MATE desktops above one shared Debian trixie runtime. The user selected KDE and MATE and approved the shared layering approach after the image investigation. This extends the [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]] and preserves the implemented [[superpowers/specs/2026-10-22-selkies-design|Selkies screen design]] and [[superpowers/specs/2026-10-22-custom-images-design|Custom images design]].

# Images and builds

Use the Docker Official Image `debian:trixie-slim` for all three variants, on amd64 and arm64. One `images/Dockerfile` contains the existing CLI, gamepad and patched codec build stages, a desktop-independent `runtime` stage, and final `debian-xfce`, `debian-kde` and `debian-mate` targets. `docker-bake.hcl` builds the three targets together, sharing their dependency graph within each architecture's build. The runtime is an internal stage, not a catalog image or separately published release dependency.

Shared files and patches live in `images/common/`; desktop overlays live in `images/debian-<desktop>/rootfs/`. Final images carry contract 3 and titles Debian XFCE, Debian KDE Plasma and Debian MATE. Keep `ghcr.io/zefhemel/deskyard-debian-xfce` and add matching `deskyard-debian-kde` and `deskyard-debian-mate` repositories, with existing latest/date/commit tagging. Native CI runners build all variants per architecture; publish manifests only after all builds succeed. Pull requests build without publishing.

The shared runtime dependencies and common configuration own Xvfb, Selkies and its pinned patches/codecs, PipeWire, Caddy, Chromium/CDP, Flatpak, mise/uv/runtimes, CLI/skills, users, workspace initialization and service supervision. Variant layers install only their own desktop packages, app store and defaults. Apply shared rootfs configuration after package installation so changing a startup script does not invalidate the desktop package layers. The source remains shared and the expensive runtime dependency layer is reused. Use curated packages with `--no-install-recommends`, with no display manager, callable sudo or additional root services. Debian Plasma hard-depends on sudo through libkf6su6: retain consistent package metadata but divert its executable into a root-only directory and remove all its permissions. Disable optional system D-Bus activation for Accounts, UDisks2, UPower, PackageKit and MATE DateTimeMechanism in all final images. KDE uses Plasma's X11 session and KWin X11; MATE uses its core environment, Marco and Caja. The initial Debian appearance decision is superseded for MATE by the appearance selection below; keep KDE's original appearance, with Chromium as the shared default browser and power/lock settings appropriate to a remote container.

# Sessions and persistence

A common `start-session` waits for Xvfb, installs stop handling before launching the desktop, loads the gamepad environment and restores Chromium only when its existing open marker is present. A fixed, image-owned `/etc/workspace/desktop-session.sh` supplies desktop configuration, startup, logout and readiness functions. Export the desktop's identity to session processes so XDG helpers select the right browser and portal behavior. Supervision runs the session as UID 1000 in `/workspace`; it never executes user-controlled startup files as root.

XFCE retains existing panel migrations, saved-session settings and graceful logout. KDE and MATE use their native session managers and logout interfaces, with session saving enabled by default without overwriting later user choices. Container stop allows time for native session saving before terminating the session. Chromium restoration remains shared and independent of native session restore.

Implementation detail established during validation: close the shared Chromium through CDP before native logout, with a bounded SIGTERM fallback scoped to its profile. A previously open browser also needs Chromium's crash-recovery acknowledgment flag alongside last-session restore; otherwise crash state can disable later session saving. Native KDE test launches must inherit both `SESSION_MANAGER` and `ICEAUTHORITY` to authenticate with its session manager.

KDE's version and Qt software-rendering environment apply to the whole image, including agent/terminal exec commands. Exporting the version only inside the native session leaves `xdg-open` selecting obsolete KDE helpers for those commands.

Existing homes keep their files and settings when switching images. Desktop defaults apply independently of the global `.workspace-seeded` marker, using system defaults or per-desktop initialization that respects existing choices. Desktop configuration and session caches remain in their native namespaces. Restoring windows from another desktop is not promised; files, Chromium profile and user tools persist. Switching back restores that desktop's own configuration. Terminal/session restoration must be measured with each desktop's native terminal rather than inferred from XFCE.

Preserve contract 3, UID/GID 1000, `/workspace`, display `:1`, port 8080, the proxy token boundary and the current Python 3.13 Selkies client path. No streaming protocol, authentication or engine security-option changes are required.

# Applications and graphical installation

The user prefers a friendly Flatpak installer over bundling LibreOffice and other large applications. Ship Discover with its Flatpak backend in KDE, and GNOME Software with its Flatpak plugin in MATE and XFCE. Keep Flathub configured per user, with installations in `/workspace/.local/share/flatpak`; these survive image changes and are available without root. The stores use libflatpak directly, so the existing CLI wrapper alone does not prove correct GUI install scope. Validate a GUI installation and its persistence, and check LibreOffice is discoverable on the target architecture.

Do not expose PackageKit/distro installation backends, automatic OS upgrades or privileged installation helpers. Debian Discover hard-depends on PackageKit: retain package metadata but remove its Discover backend and D-Bus activation, and divert the daemon into the root-only disabled directory with mode 000. GNOME Software uses the Flatpak plugin as its alternative backend dependency, so its distro plugin is not installed. Prefer the Flatpak plugin as the dependency provider for GNOME Software. Install the matching GTK or KDE desktop portal for Flatpak file dialogs. Expose the installer in the normal application menu and document its name. Initial app-catalog downloads may require network access; a failed refresh must not block desktop startup. Additional preinstalled application sets remain available through existing admin custom-image recipes, independently of desktop choice.

Validation found a remaining document-portal limitation: the current engine does not expose `/dev/fuse`, so its document filesystem cannot mount. Keep the existing engine security settings in this change and document this boundary rather than claiming general portal compatibility. LibreOffice's own allowed workspace access supports graphical document saving; apps relying on document-portal grants need follow-up work.

# Catalog and scope

The initial implementation used manual image-reference entry. The approved discovery follow-up supersedes that choice: offer static official XFCE/KDE/MATE metadata before download, merged after saved entries with normalized-reference deduplication. Preserve XFCE as the seeded default, existing catalog order and the meaning of `DESKYARD_IMAGE`. Show Available, Installed and Update available states. Admins can install explicitly, or download on first use from desktop creation or recipes. Browsing must not mutate saved state or download images; update checks must skip missing images. Preserve custom/arbitrary image workflows. Built-in choices remain visible and cannot be removed. Custom image recipes can use any of the three final images.

# Validation and release

Move image tests to `images/tests/`, select the build target using `AC_DESKTOP=xfce|kde|mate`, and retain `AC_IMAGE` to test an existing tag. Share boot, token/privilege, stream, clipboard, resize, browser/CDP, Flatpak, persistence and custom-image tests. Keep XFCE layout/migration tests explicitly XFCE-specific. Add native desktop/session checks and report native restoration limitations honestly.

Run runtime checks on all variants on the available native architecture. Validate both build platforms in CI; local validation on one architecture does not establish the other's runtime behavior. Record image sizes and settled memory/CPU with no viewer and an idle viewer at the same resolution, distinguishing browser-open measurements. Existing XFCE footprint limits remain XFCE requirements; do not invent measured budgets for new desktops. Review licensing impact when the actual package sets are known.

Update README development/image instructions and affected architecture pages with implemented paths and status. Preserve historical specs/plans as records; identify the new design as the source of layout changes.

Native ARM64 measurements, graphical application screenshots, runtime validation and remaining limitations are recorded in [[Testing/Desktop variants 2026-10-23|Desktop variants testing — 23 October 2026]]. The local implementation does not imply that new registry tags have been published or amd64 runtime checks have run.

# Appearance selection — 23 October 2026

After reviewing real previews, the user selected KDE's original appearance and MATE's themed preview. MATE uses Debian-packaged Arc, Papirus and Noto, an original blue SVG wallpaper, a dark 40-pixel bottom panel with Files/Chromium/Software launchers and a navy terminal. Implement these with native GSettings schema defaults and a MATE panel layout, preserving saved user choices and existing layouts. Seed scoped panel CSS as the desktop user only if no stylesheet or symlink exists. Keep all theme packages and assets in the MATE layer; KDE's preview settings are not applied. Flatpak theme extensions and application-specific LibreOffice appearance remain separate checks.
