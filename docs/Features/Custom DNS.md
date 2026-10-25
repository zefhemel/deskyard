---
displayName: "Custom DNS"
tags: "feature"
status: "discovery"
kind: "capability"
availability: "unavailable"
summary: "Saved DNS service selection independent of VPN is not implemented."
outcome: "Administrators can select a DNS service independently of the desktop VPN connection."
acceptance:
  - "Default or saved DNS services work with and without VPN."
  - "Outage behavior, local resolver access and bypass limitations are explicitly designed and verified."
next: "Design resolver reachability, failure policy and enforcement limits before proposing implementation."
components:
  - "[[Architecture/VPN runtime]]"
---
The current runtime supports public tunneled DNS only. [[Use cases#F5: Manage internet access outside the child's desktop|The family networking use case]] identifies the desired capability; no milestone or implementation scope is selected.

# Implementation boundary
- `supervisor/internal/vpn/runtime_config.go`
