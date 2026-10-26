---
displayName: "Images and reusable environments"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "The catalog offers XFCE and MATE images, missing-image installation, custom image references and upgrades."
outcome: "Reuse a prepared desktop environment without rebuilding personal work."
acceptance:
  - "Supported catalog images can be installed, selected and upgraded."
  - "Externally built custom images can be added by reference and obey the runtime contract."
limitations:
  - "Custom Dockerfiles are built externally."
  - "Supervisor recipe editing, image building and image-from-desktop export are removed."
components:
  - "[[Architecture/Image catalog]]"
  - "[[Architecture/Desktop runtime]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
  - "[[Testing/Image catalog 2026-10-23]]"
  - "[[Testing/RetroArch Dockerfile example 2026-10-24]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

At the owner's request, the `deskyard.secrets` image label, its lock badges and its sharing/upgrade confirmations were removed on 26 October 2026. Images have no secrets semantics; an image that bakes in someone's sign-ins shares them with everyone who can use desktops made from it.

# Implementation
- `supervisor/internal/server/images.go`
- `supervisor/internal/server/official_images.go`
- `supervisor/web/src/pages/images.tsx`
- `images/Dockerfile`
- `examples/retroarch/Dockerfile`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
