---
tags: component
status: external
uses:
  - "[[Architecture/Web UI]]"
  - "[[Architecture/Desktop proxy]]"
---
A person's web browser. Today it opens a computer directly at its port 8080 ([[Architecture/Caddy]]); with the supervisor it opens the supervisor's UI and each computer's desktop at `/<name>/` through the [[Architecture/Desktop proxy]], and never reaches a computer directly.
