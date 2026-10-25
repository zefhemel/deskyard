---
displayName: "Docker daemon access"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "An administrator can explicitly mount the engine-host Docker socket into a desktop; access defaults off."
outcome: "Allow trusted desktop users to build and run Docker workloads."
acceptance:
  - "Opt-in access works as the desktop user with suitable client tools."
  - "Disabling access removes the socket from the recreated desktop."
limitations:
  - "Every desktop user and program then has host-administrator-equivalent daemon access."
  - "Custom/rootless socket paths are unsupported; client tools are supplied separately."
components:
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Desktop runtime]]"
  - "[[Architecture/Engine adapter]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Docker access 2026-10-24]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/desktops/docker_access.go`
- `supervisor/internal/desktops/host_preflight.go`
- `images/common/rootfs/usr/local/bin/workspace-init`
- `examples/docker/README.md`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
