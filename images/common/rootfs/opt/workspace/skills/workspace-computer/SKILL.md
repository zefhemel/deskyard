---
name: workspace-computer
description: "Use when working inside the workspace sandbox (HOME=/workspace) and you need to install software, find where things live, or understand what persists and what you share with the human."
---

# This desktop

You are on a Debian Linux container that a human shares with you. The human
reaches the same desktop (XFCE, KDE Plasma or MATE, display `:1`, sized to their browser window) through a browser and
looks over your shoulder: they see every window you open, and they may be
using the mouse and keyboard at the same time as you.

## What persists

Only `/workspace` (your `HOME`) survives restarts and image upgrades.
Everything else (`/usr`, `/etc`, `/opt`, apt packages) is reset when the
image is updated. There is no `sudo` and no `apt-get install`: install what
you need into `/workspace` with the tools below.

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

All of these are already on `PATH`, also for non-login shells.
Flatpak apps show up in the desktop application menu; start one from a shell
with `setsid -f flatpak run <app-id>` so it outlives your command.

## Sharing the screen

- The browser on screen is the shared Chromium. Drive it with the
  `workspace-browser` skill; never start a second browser.
- To see or click anything else on screen, use the `workspace-computer-use`
  skill (`deskyard screenshot`, `deskyard click X Y`, ...).
- When something needs the human (a login, a 2FA code, a CAPTCHA, a
  decision), say exactly what you need and wait; they do it on the same
  desktop.

## Leave alone

The services that keep the desktop reachable (Caddy, the `supervisord`
services: the X server, Selkies, PipeWire, Chromium, D-Bus). Restarting or
killing them cuts the human off. If the human closes Chromium it stays
closed until something connects to its CDP port (see `workspace-browser`).
