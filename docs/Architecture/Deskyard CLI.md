---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Session]]"
  - "[[Architecture/Port forwarding]]"
  - "[[Architecture/Shared terminal]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The `deskyard` command: screenshots, mouse and keyboard on the shared display, for people controlling the desktop from a terminal or scripts. One Go binary (`supervisor/cmd/deskyard`) that also works remotely (see [[Architecture/Remote CLI]]), with the same subcommands. The supervisor runs it through exec for thumbnails and remote screen actions, so it's part of the image contract. Remotely it also maps ports with `forward` (see [[Architecture/Port forwarding]]); inside the desktop the hidden `forward-helper` is its other end. Files: `supervisor/cmd/deskyard`, `supervisor/internal/cli/`.

`deskyard terminal [SESSION]` joins the shared tmux terminal (default `main`), locally or through remote interactive exec. The hidden `terminal-helper SESSION` uses `supervisor/internal/terminal` to attach on a private runtime socket, with exact session names and context cancellation.

Hidden `terminal-lease SESSION ATTACHMENT_ID` and `terminal-attach ATTACHMENT_ID` commands support supervisor-owned remote attachment lifetimes. The non-TTY lease removes its private grouped session on EOF; the TTY helper only attaches an already-prepared group. Public/local terminal behavior and its validated session names remain unchanged.

Hidden local `terminal-new` allocates a new shared shell in the current directory for native emulator defaults; public `terminal` still defaults to `main`. Hidden `terminal-list` returns only validated live base-session names, including detached jobs, without starting tmux. It has a four-second deadline and bounded output and is consumed by supervisor session discovery.

Within an existing native shared terminal, press **Ctrl-B then s** and choose the named session; this switches only that native client. The chooser shows public sessions, including browser-created shells, and the native title follows the selected name. Outside tmux, `deskyard terminal NAME` joins as before. Browser and remote CLI attachments keep their connection-owned groups: use browser **Sessions**, or detach and reconnect with `deskyard terminal NAME`, rather than the native chooser.
