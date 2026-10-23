---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/API]]"
  - "[[Architecture/Agent activity]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Preact + TypeScript (strict) + Tailwind, built with Vite and embedded in the binary: the desktops wall, a desktop's details page, images, accounts, login, and the desktop page at `/<name>/`: our own bar around [[Architecture/Selkies]]' client in an iframe (its sidebar hidden), driven through `web/src/desktop/selkies.ts`. The bar has who's viewing (with mic and camera badges), Audio (sound, volume, output, mic and webcam), Lock size, Clipboard, Keys, Quality and Download (a small file browser of `/workspace`); dropping files on the screen uploads them to `~/Desktop`. Agents' on-screen actions are drawn over the picture. Updates live over server-sent events.

The desktop toolbar uses its compact layout below 1024 CSS pixels: Lock size, Keys, Quality and Download move into More while Audio and Clipboard remain directly accessible. Shared menus scroll within the remaining visual viewport and recalculate their height on viewport changes (`web/src/components/menu.tsx`). Clipboard keeps drafts through unrelated window blurs and dismisses on a real interaction inside the screen frame (`web/src/desktop/clipboard.tsx`). See [[Testing/Mobile 2026-10-23|Mobile testing — 23 October 2026]] for the mobile checks and remaining device limitations.
