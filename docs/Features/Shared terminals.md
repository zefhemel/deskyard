---
displayName: "Shared terminals"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Named tmux-backed shells are shared by native desktop terminals, browser clients and remote CLI attachments."
outcome: "Resume the same running shell and jobs from another client."
acceptance:
  - "Create and join an exact named shell from the supported clients."
  - "Detaching an attachment leaves the shell and jobs alive."
limitations:
  - "Desktop stop or restart ends shells and jobs."
  - "Native mobile evidence uses simulators/emulators; physical-device and broader platform coverage remain scoped."
components:
  - "[[Architecture/Shared terminal]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Shared terminal 2026-10-23]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/terminal/terminal.go`
- `supervisor/internal/server/terminal.go`
- `supervisor/internal/cli/app/cmd_terminal.go`
- `supervisor/web/src/terminal/terminal.tsx`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
