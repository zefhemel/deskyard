---
tags: component
status: external
uses:
  - "[[Architecture/Desktop]]"
---
The container engine on the host: Docker now, Podman later. It runs every [[Architecture/Desktop]]; only the supervisor's [[Architecture/Engine adapter]] talks to its socket, which is root-equivalent on the host.
