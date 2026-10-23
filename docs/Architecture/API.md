---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Agent tokens]]"
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Monitoring]]"
  - "[[Architecture/Image catalog]]"
  - "[[Architecture/VPN connections]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The JSON API behind the UI, plus login and sessions. Every handler checks the signed-in account through one function, `can(account, action, desktop)`, and state-changing calls check `Origin`.

`supervisor/internal/server/vpn.go` implements administrator-only saved connections, candidate-test jobs and confirmed shared revision rollouts. Desktop settings accept explicit null versus omission for VPN selection and expose cached, sanitized VPN status to accounts with desktop access. The exact public DTO, country/reference preview and job-result shapes are documented in `supervisor/API.md`; provider credentials, paths, raw logs and management tokens remain private.

The typed client in `supervisor/web/src/api.ts` consumes these VPN routes and optional job results. Structured conflict references are retained by `ApiError` so the UI can explain deletion blockers and refresh shared-edit previews. `/_vpn` is the reserved-namespace administration route; `/vpn/` still resolves a desktop named `vpn`.
