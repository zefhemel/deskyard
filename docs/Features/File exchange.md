---
displayName: "File exchange"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Browser uploads copy to the desktop, workspace downloads include folder archives, and the CLI transfers files."
outcome: "Move files between a client machine and a desktop."
acceptance:
  - "Uploads and downloads preserve the supported workspace confinement and access checks."
  - "CLI transfer supports the documented local and desktop paths."
limitations:
  - "The supervisor file browser is confined to /workspace."
  - "Mounted host folders require desktop applications or the CLI."
components:
  - "[[Architecture/Web UI]]"
  - "[[Architecture/Remote CLI]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/server/files.go`
- `supervisor/internal/server/upload.go`
- `supervisor/web/src/desktop/upload.tsx`
- `supervisor/internal/cli/transfer/cp.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
