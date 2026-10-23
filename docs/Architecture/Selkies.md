---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
spec: "[[superpowers/specs/2026-10-22-selkies-design|Selkies screen design]]"
---
Streams the desktop's screen, sound, mic, webcam and gamepads to browsers: Selkies 2.1.0 (with pinned pixelflux and pcmflux wheels in `/opt/selkies`) in WebSocket mode, as `user`, on `127.0.0.1:6080` behind [[Architecture/Caddy]], which passes only its stream (`/api/websockets`) and health probe through. The X server is Debian's Xvfb on display `:1` (a 3840x2160 framebuffer, modes added with `xrandr`); sound comes from the PipeWire speaker's monitor (`deskyard-speaker.monitor`), and Selkies' virtual mic is the default source. Pixelflux and pcmflux only run while someone is viewing. Settings: Turbo off, CSS scaling on, browser cursors, keyboard shortcuts, sidebar, printing, sharing and Selkies' own file transfers off (uploads and downloads are the supervisor's), mic and gamepads on (through Selkies' LD_PRELOAD joystick interposer in the session), and the webcam on only when the desktop has a virtual camera. The supervisor serves Selkies' web client from the desktop's image, not from the desktop (see [[Architecture/Desktop proxy]]). Files: `images/debian-xfce/rootfs/usr/local/bin/start-xvfb`, `images/debian-xfce/rootfs/usr/local/bin/start-selkies`, `images/debian-xfce/rootfs/etc/supervisor/conf.d/workspace.conf`.
