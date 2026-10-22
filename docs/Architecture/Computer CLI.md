---
tags: component
status: built
partOf: "[[Architecture/Computer]]"
uses:
  - "[[Architecture/Desktop]]"
spec: "[[superpowers/specs/2026-10-21-workspace-sandbox-design|Workspace Sandbox design]]"
---
The `computer` command: screenshots, mouse and keyboard on the shared display, for agents that work by looking at the screen. One Go binary (`supervisor/cmd/computer`) that also works remotely (see [[Architecture/Remote CLI]]), with the same subcommands. The supervisor runs it through exec for thumbnails and remote desktop actions, so it's part of the image contract. Files: `supervisor/cmd/computer`, `supervisor/internal/cli/`.
