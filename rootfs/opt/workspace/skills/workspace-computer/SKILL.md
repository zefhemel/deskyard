---
name: workspace-computer
description: "Use when working inside the workspace sandbox (HOME=/workspace) and you need to install software, find where things live, or understand what persists and what you share with the human."
---

# This computer

You are on a Debian Linux container that a human shares with you. The human
reaches the same desktop (XFCE, display `:1`, sized to their browser window) through a browser and
looks over your shoulder: they see every window you open, and they may be
using the mouse and keyboard at the same time as you.

## What persists

Only `/workspace` (your `HOME`) survives restarts and image upgrades.
Everything else (`/usr`, `/etc`, `/opt`, apt packages) is reset when the
image is updated. `sudo` is usually unavailable (it's off whenever Flatpak
support is on); where it works, treat `apt-get install` as scratch: it
disappears on the next upgrade. Use the table below instead.

Install anything that should last into `/workspace`:

| Need | Command | Lands in |
|---|---|---|
| Language runtimes, CLI tools | `mise use -g node@22`, `mise use -g python@3.12 go jq ripgrep` | `~/.local/share/mise` (on PATH via shims) |
| npm CLIs | `npm i -g <pkg>` (after `mise use -g node`) | `~/.npm-global` |
| Python CLIs | `uv tool install <pkg>` | `~/.local/bin` |
| Single binaries / scripts | copy into `~/.local/bin` | `~/.local/bin` |
| Desktop (GUI) apps | `flatpak install --user -y --noninteractive flathub <app-id>` | `~/.local/share/flatpak` |

Node 24 and Python 3.13 come preinstalled with the image (and update with
it). For Python projects use `uv venv` / `uv run` / `uv tool`: uv puts its
own Python in `/workspace`, while a `python -m venv` built on the image's
Python breaks when the image updates.

All of these are already on `PATH`, also for non-login `docker exec`.
Flatpak apps show up in the XFCE Applications menu; start one from a shell
with `setsid -f flatpak run <app-id>` so it outlives your command. If
`flatpak run` says apps can't run here, the human started the container
without Flatpak support: tell them, don't work around it.

## Sharing the screen

- The browser on screen is the shared Chromium. Drive it with the
  `workspace-browser` skill; never start a second browser.
- To see or click anything else on screen, use the `workspace-computer-use`
  skill (`computer screenshot`, `computer click X Y`, ...).
- When something needs the human (a login, a 2FA code, a CAPTCHA, a
  decision), say exactly what you need and wait; they do it on the same
  desktop.

## Leave alone

The web password and HTTPS setup (`/var/lib/workspace-system`, Caddy, the
`supervisord` services: KasmVNC, Chromium, D-Bus). Restarting or
killing them cuts the human off. If the human closes Chromium it stays
closed until something connects to its CDP port (see `workspace-browser`).
