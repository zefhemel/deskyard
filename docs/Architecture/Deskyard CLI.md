---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Session]]"
  - "[[Architecture/Port forwarding]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The `deskyard` command: screenshots, mouse and keyboard on the shared display, for agents that work by looking at the screen. One Go binary (`supervisor/cmd/desktop`) that also works remotely (see [[Architecture/Remote CLI]]), with the same subcommands. The supervisor runs it through exec for thumbnails and remote screen actions, so it's part of the image contract. Remotely it also maps ports with `forward` (see [[Architecture/Port forwarding]]); inside the desktop the hidden `forward-helper` is its other end. Files: `supervisor/cmd/desktop`, `supervisor/internal/cli/`.
