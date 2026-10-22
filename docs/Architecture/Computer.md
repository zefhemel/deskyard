---
tags: component
status: built
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
One container-computer: a container from a computer image, and its [[Architecture/Workspace folder]] (today also a cache volume, which the supervisor drops). The image is Debian trixie with XFCE; `supervisord` (`rootfs/etc/supervisor/conf.d/workspace.conf`) starts its services, all as the single desktop user except D-Bus' system bus and supervisord itself. Moves to `images/debian-xfce/` and renames its user from `workspace` to `user` with the supervisor (see the Supervisor design).
