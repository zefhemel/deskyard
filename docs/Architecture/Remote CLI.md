---
tags: component
status: built
uses:
  - "[[Architecture/Remote access API]]"
  - "[[Architecture/Port forwarding]]"
  - "[[Architecture/Shared terminal]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The `deskyard` command run outside a desktop: remotes (`deskyard remote add alice URL --token …`), `exec`, `shell`, `cp`, `browser connect` (a local CDP port for Playwright and friends) and every screen action, all built on the remote access API's exec WebSocket and CDP. Includes command help (`--help`), a practical manual (`guide`) and machine-readable command descriptions (`describe --json`) for scripts and integrations. The same binary as the [[Architecture/Deskyard CLI]].

`deskyard terminal [SESSION]` runs the fixed helper argv through the existing interactive exec transport. Disconnecting detaches the client while the shared Bash login shell and jobs keep running until the container stops.

The supervisor recognizes the exact TTY `deskyard terminal-helper SESSION` request and runs its non-TTY lease plus attach-only TTY internally (`supervisor/internal/server/terminal_process.go`). Remote disconnect, stdin EOF and supervisor death remove only that client’s private grouped session; the base shell and other clients survive. Stdin EOF detaches cleanly, while an actual Ctrl-D byte still reaches the shell.

Native-created sessions have readable names such as `terminal-1`; `deskyard -d NAME terminal terminal-1` joins that exact shell. The authenticated browser’s running-session list uses a separate View-scoped session API; remote CLI attachment continues using the existing remote exec transport.

Within an existing native shared terminal, press **Ctrl-B then s** and choose the named session; this switches only that native client. The chooser shows public sessions, including browser-created shells, and the native title follows the selected name. Outside tmux, `deskyard terminal NAME` joins as before. Browser and remote CLI attachments keep their connection-owned groups: use browser **Sessions**, or detach and reconnect with `deskyard terminal NAME`, rather than the native chooser.
