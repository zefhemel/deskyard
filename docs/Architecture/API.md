---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Access tokens]]"
  - "[[Architecture/Accounts]]"
  - "[[Architecture/Shared terminal]]"
  - "[[Architecture/Lifecycle]]"
  - "[[Architecture/Monitoring]]"
  - "[[Architecture/Image catalog]]"
  - "[[Architecture/VPN connections]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The JSON API behind the UI, plus login and sessions. Every handler checks the signed-in account through one function, `can(account, action, desktop)`, and state-changing calls check `Origin`.

`POST /api/desktops/{name}/reclaim-memory` (`supervisor/internal/server/memory.go`) is a synchronous administrator-only action for running desktops. It excludes concurrent lifecycle/reclaim operations and returns resident memory and logical swap before/after a 128 MiB request. Unsupported hosts return 501, stopped/busy desktops 409 and helper failures 502; access tokens cannot use this session route. See `supervisor/API.md` for the JSON contract.

The server probes memory reclamation before accepting requests, logs enabled requirements or the disabling reason, and caches the result for this process. `GET /api/host` exposes it as `memoryReclaim: {enabled, reason?}`. A failed startup probe gates the action with 501; runtime per-desktop failures do not disable the host-wide capability. Startup cancellation is propagated from the supervisor's signal context.

`PUT /api/desktops/{name}/memory-policy` (`supervisor/internal/server/memory_policy.go`) saves the administrator's `autoReclaim` preference by stable desktop ID without operating on its container. Desktop JSON/SSE carries the preference; [[Architecture/Lifecycle]] uses it to gate automatic reclamation independently of manual requests.

Browser terminal WebSockets at `GET /api/desktops/{name}/terminal` use cookie authentication, View permission, strict HTTP(S) Origin checks, fixed helper execution and bounded raw-binary transport (`supervisor/internal/server/terminal.go`, `terminal_flow.go`, `terminal_connections.go`). Desktop JSON includes image capability `terminal`, independent of running state. Viewer registration prevents idle reclamation; account, logout, lifecycle, expiry and shutdown hooks cancel attachments. Runtime generation combines container ID and start time. See `supervisor/API.md` for sizes, ACK credit, deadlines and proxy Origin policy.

Remote terminal process lifetime is owned by `supervisor/internal/server/terminal_process.go`: non-TTY lease EOF removes a private grouped session while an attach-only TTY supplies raw terminal data. It gates input until tmux readiness and is also used for the exact owned CLI helper request. Other remote exec commands keep their existing semantics.

`GET /api/desktops/{name}/terminal/sessions` lists sorted running base-session names for signed-in View users, including detached shells/jobs and excluding private attachment groups. `terminal_list.go` uses fixed non-TTY `deskyard terminal-list` execution with a five-second request deadline and 64 KiB output limit. Stopped/unsupported desktops return 409; helper/data failures return 502. The helper enforces its own four-second deadline. See `supervisor/API.md` for the minimal JSON contract.

`supervisor/internal/server/vpn.go` implements administrator-only saved connections, candidate-test jobs and confirmed shared revision rollouts. Desktop settings accept explicit null versus omission for VPN selection and expose cached, sanitized VPN status to accounts with desktop access. The exact public DTO, country/reference preview and job-result shapes are documented in `supervisor/API.md`; provider credentials, paths, raw logs and management tokens remain private.

The typed client in `supervisor/web/src/api.ts` consumes these VPN routes and optional job results. Structured conflict references are retained by `ApiError` so the UI can explain deletion blockers and refresh shared-edit previews. `/_vpn` is the reserved-namespace administration route; `/vpn/` still resolves a desktop named `vpn`.
