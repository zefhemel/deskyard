---
name: workspace-computer-use
description: "Use when you need to see or operate the shared desktop in the workspace sandbox by screenshot, mouse and keyboard: native and Flatpak apps, dialogs, or browser UI that CDP can't reach."
---

# Computer use

The `computer` command controls the shared display (`:1`). Its size follows
the human's browser window (1280x800 until they connect), so it can change
between screenshots; `computer size` tells you. Coordinates are screen pixels from the
top-left corner.

```
computer screenshot [FILE|-]          PNG; stdout when omitted or '-' (waits COMPUTER_SETTLE=0.3s first)
computer zoom X0 Y0 X1 Y1 [FILE|-]    PNG of one region at full resolution, to read small text
computer size                         prints "W H"
computer cursor                       prints "X Y"
computer move X Y
computer click X Y [left|right|middle]
computer double-click X Y
computer triple-click X Y             select a whole line/paragraph
computer mouse-down [button]          / computer mouse-up [button], for custom drags
computer drag X1 Y1 X2 Y2
computer type TEXT                    types into the focused window
computer key COMBO...                 xdotool key names: ctrl+l Return Escape alt+F4 ctrl+shift+t
computer hold-key KEY SECONDS
computer scroll X Y up|down|left|right [N=3]
computer wait SECONDS                 let an app or page load before the next screenshot
```

## The loop

1. `computer screenshot /tmp/s.png`, then look at the image. Text too small
   to read? `computer zoom` on that region.
2. Act on what you saw: one click, a `type`, a `key`.
3. Screenshot again and check the action landed before the next one. UIs
   animate and load; if nothing changed yet, wait a second and look again.

Click into a text field before typing. Prefer keyboard shortcuts when they
exist (fewer coordinates to get wrong). Chain several actions without a
screenshot only when you already know what each one does: clicking a field
can open a picker or dialog that swallows whatever you type next.

## Windows

- List visible windows: `xdotool search --onlyvisible --name . getwindowname %@`.
  Titles often show the document or page, not the app name.
- Raise one: `xdotool search --name '<title>' windowactivate`.
- Dialogs can be taller than the screen, with their buttons below the bottom
  edge. Maximize the window (its title-bar button, or
  `xdotool getactivewindow windowsize 100% 100%`) and screenshot again
  before clicking near an edge.
- Start GUI apps detached so they outlive your command:
  `setsid -f flatpak run <app-id> >/tmp/app.log 2>&1`.

## Sharing the screen

The human sees every move and may be using the mouse too. If the screen
changes in a way you didn't cause, take a fresh screenshot before acting.
For web pages, the `workspace-browser` skill (CDP) is faster and more
reliable than clicking; use pixels for everything CDP can't reach.
