---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Selkies]]"
  - "[[Architecture/Shared terminal]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The selected XFCE or MATE session on display `:1`, as user `user` in `/workspace`. The common lifecycle waits for Xvfb, exports gamepad support, restores Chromium only if it was open, and requests native logout on container stop. Desktop adapters supply configuration, start, logout and readiness functions. XFCE retains its existing panel layout migrations and session saving. MATE uses Marco without compositing, Caja and native session saving. Native restoration only covers applications that support the corresponding session protocol; switching desktop environments does not promise cross-desktop window restoration.

Files: `images/common/rootfs/usr/local/bin/start-session`, `desktop-ready`; `images/debian-xfce/rootfs/etc/workspace/desktop-session.sh`, `images/debian-mate/rootfs/etc/workspace/desktop-session.sh`. XFCE layout files remain under `images/debian-xfce/rootfs/etc/xdg/`. See [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]].

MATE's `session_configure` also seeds its panel stylesheet as the desktop user, preserving existing files and symlinks. Appearance defaults and the initial panel layout are image-owned schema/layout data, not settings commands rerun at every login.

The common native Deskyard Terminal launcher joins [[Architecture/Shared terminal]] shells, also reachable through the authenticated supervisor browser transport and CLI. Terminal jobs survive individual attachment disconnects and end when this container session stops.

The common `deskyard-terminal-configure` runs before native session startup and migrates saved terminal command defaults. Standard emulator windows and tabs create separate discoverable shared sessions; file-manager working directories are preserved. Explicit emulator commands and restored explicit commands keep their original semantics. The default command migration is deliberate for existing workspaces; appearance and unrelated desktop preferences are preserved.
