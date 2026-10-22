---
name: workspace-sandbox
description: "Use when driving a workspace sandbox container from the Docker host: running commands in it, controlling its Chromium or desktop, or finding which container is which."
---

# Driving a workspace sandbox from the host

A sandbox is a container from the `container-computer` image: a Linux desktop
shared with a human (who watches through a browser), a Chromium with their
logins, and a persistent home at `/workspace`.

## Find the sandbox

```bash
docker ps --format '{{.Names}}\t{{.Image}}\t{{.Ports}}'
```

Sandboxes started with the project's compose file are named
`<project>-workspace-1`. The `Ports` column shows each one's host ports:
container `9222` is CDP, `5901` is raw VNC, `8080` the password-protected web
desktop.

## Run commands

```bash
docker exec -u workspace <name> bash -lc 'cd ~ && ls'
```

Always pass `-u workspace` (files you create stay owned by the user). The
environment matches a terminal on the desktop: tools installed with mise,
npm, uv or Flatpak are on PATH. Install things the way the in-sandbox
`workspace-computer` skill describes (into `/workspace`); apt installs are
lost on upgrade.

## Drive the browser

Connect Playwright to the CDP port published on the host, e.g.
`chromium.connect_over_cdp("http://127.0.0.1:9222")` (use the instance's
own port from `docker ps`). Use `browser.contexts[0]` (the shared, logged-in
profile) and open your own tab with `new_page()`. Never `launch()` a
separate browser. When a site needs a login, tell the human to log in on the
sandbox desktop and wait. If the human closed the browser, connecting to
the CDP port starts it again (the first connection takes a few seconds).

## See and click the desktop

```bash
docker exec -u workspace <name> computer screenshot > screen.png
docker exec -u workspace <name> computer click 640 400
docker exec -u workspace <name> computer type 'hello'
docker exec -u workspace <name> computer key ctrl+l
```

`computer` with no arguments prints all actions (screenshot, zoom, size,
cursor, move, click, double-click, triple-click, mouse-down/up, drag, type,
key, hold-key, scroll, wait). Screenshot, act, then
screenshot again to check. Agents that speak VNC can use the raw VNC port
instead; it has no password and listens on host localhost only.
