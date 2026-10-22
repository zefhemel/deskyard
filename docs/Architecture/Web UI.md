---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/API]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Preact + TypeScript (strict) + Tailwind, built with Vite and embedded in the binary: the desktops wall, a desktop's details page, images, accounts, login, and the desktop page at `/<name>/`, our own chrome (clipboard, keys, screenshot, quality, who's viewing) around the KasmVNC client in an iframe with its control bar hidden. Updates live over server-sent events.
