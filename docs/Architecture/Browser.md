---
tags: component
status: external
uses:
  - "[[Architecture/Web UI]]"
  - "[[Architecture/Desktop proxy]]"
---
A person's web browser. It opens the supervisor's UI and each desktop's desktop at `/<name>/`, which the [[Architecture/Desktop proxy]] connects to the desktop; it never reaches a desktop directly.
