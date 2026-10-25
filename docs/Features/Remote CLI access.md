---
displayName: "Remote CLI access"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Desktop-scoped access tokens provide remote exec, shell, file transfer and browser access with revocation checks."
outcome: "Control an authorized desktop from a terminal."
acceptance:
  - "Tokens remain desktop-scoped and respect creator access changes and expiry."
  - "Remote operations preserve the documented stream, cancellation and file semantics."
limitations:
  - "Tokens do not administer accounts or desktop lifecycle."
  - "Removing Deskyard access does not revoke credentials in external applications."
components:
  - "[[Architecture/Access tokens]]"
  - "[[Architecture/Remote access API]]"
  - "[[Architecture/Remote CLI]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/server/tokens.go`
- `supervisor/internal/server/agent.go`
- `supervisor/internal/cli/remote/client.go`
- `supervisor/internal/cli/app/cmd_exec.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
