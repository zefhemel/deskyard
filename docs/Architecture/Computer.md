---
tags: component
status: built
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
One container-computer: a container from a computer image and its [[Architecture/Workspace folder]], run only by the supervisor. The image (`images/debian-xfce`) is Debian trixie with XFCE; `supervisord` (`rootfs/etc/supervisor/conf.d/workspace.conf`) starts its services, all as the single user `user` except D-Bus' system bus, Caddy (as `caddy`) and supervisord itself. No sudo.
