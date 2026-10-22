---
tags: component
status: external
uses:
  - "[[Architecture/Computer]]"
---
The container engine on the host: Docker now, Podman later. It runs every [[Architecture/Computer]]. Today `docker compose` drives it directly; with the supervisor only the [[Architecture/Engine adapter]] talks to its socket, which is root-equivalent on the host.
