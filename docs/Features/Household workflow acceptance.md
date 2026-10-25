---
displayName: "Household workflow acceptance"
tags: "feature"
status: "discovery"
kind: "improvement"
availability: "unavailable"
summary: "Individual mechanisms exist, but one real household setup has not been accepted as an end-to-end outcome."
outcome: "A household can provision, use, reconnect to, assist and recover its chosen desktops."
acceptance:
  - "Actual users, applications, client devices and concurrent usage are specified."
  - "The agreed provision/use/reconnect/help/recovery scenarios are demonstrated with explicit limitations."
next: "When household acceptance is chosen, establish the real users, apps, devices and privacy/help expectations."
components:
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Web UI]]"
  - "[[Architecture/Lifecycle]]"
---
[[Use cases]] records open household questions. Administrator access is implemented; join consent, content/time policies and recovery expectations have not been designed as complete product workflows. This is a loose end, not a selected milestone.

# Implementation boundary
- `supervisor/internal/server/routes.go`
- `supervisor/internal/accounts/accounts.go`
