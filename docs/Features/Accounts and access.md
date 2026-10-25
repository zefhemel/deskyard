---
displayName: "Accounts and access"
tags: "feature"
status: "implemented"
kind: "capability"
availability: "available"
summary: "Administrators manage accounts and per-desktop access; all viewers of a desktop share its operating-system session."
outcome: "Give people explicit access to persistent desktops."
acceptance:
  - "Only authorized accounts can reach a desktop or change its administration."
  - "Access changes revoke affected sessions and desktop-scoped token use according to the API contract."
limitations:
  - "Sharing a desktop also shares its files and signed-in applications."
  - "No SSO, groups, delegated administration or designed join-consent flow is implemented."
components:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Access tokens]]"
evidence:
  - "[[Testing/Current capability inventory|Current capability inventory and scope]]"
---
Current implementation is present in this checkout. This inventory records existing behavior; it is not a fresh end-to-end household acceptance result.

# Implementation
- `supervisor/internal/accounts/accounts.go`
- `supervisor/internal/auth/session.go`
- `supervisor/internal/server/accounts.go`
- `supervisor/internal/server/routes.go`

# Verification scope
The source audit and linked observations establish the stated implementation scope. Historical evidence retains its original environment and limitations; broader device and release checks are documented as loose ends.
