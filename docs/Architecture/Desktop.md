---
tags: component
status: built
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
One desktop: a container from a desktop image and its [[Architecture/Workspace folder]], run only by the supervisor. The image (`images/debian-xfce`) is Debian trixie with XFCE; `supervisord` (`rootfs/etc/supervisor/conf.d/workspace.conf`) starts its services, all as the single user `user` except D-Bus' system bus, Caddy (as `caddy`) and supervisord itself. No sudo. Agents outside map ports into and out of it through `deskyard forward-helper`, run by the supervisor (see [[Architecture/Port forwarding]]). A custom image adds mise tools in `/opt/mise` (linked into `/opt/deskyard/bin`), system-wide Flatpak apps and, on first start, a home template from `/opt/deskyard/home-template` copied into the folder without overwriting anything (see [[Architecture/Recipes]]). The details page lists mise tools with where each comes from, the desktop or the image. What programs inside it do goes to its [[Architecture/Activity stream]], which the supervisor reads for [[Architecture/Agent activity]].
