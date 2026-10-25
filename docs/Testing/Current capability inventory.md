---
displayName: "Current capability inventory and scope"
tags: "verification"
result: "partial"
scope: "Source-backed inventory of the sixteen implemented product capabilities and documented loose ends; existing runtime observations retain their original scope."
environment: "Current shared checkout and retained local/native/CI verification records; no new Docker or application acceptance run was performed for this documentation migration."
---
The inventory in [[Features]] was reconciled against README, current component pages and implementation paths. Sixteen existing capabilities are marked `implemented`, with `availability` distinguishing available interfaces from the experimental mobile application view. This records code that exists without retrospectively claiming broad acceptance criteria were met on every platform. Agents verify future feature delivery against agreed criteria; the owner accepts milestones. No next milestone is prepared or selected.

# What was inspected
- Desktop/account/token lifecycle and recreation/recovery in `supervisor/internal/desktops/`, `accounts/`, `auth/` and server routes.
- Browser stream, file exchange, native/browser/CLI named terminals, CDP and forwarding handlers and UI components.
- Resource/activity/event tracking, memory reclaim, host-source preflights and opt-in Docker access.
- Saved VPN configuration, fail-closed runtime policy and durable recovery boundaries.
- Shared image runtime, XFCE/MATE layering, workspace initialization, browser/session restore and Flatpak support.
- Existing mobile/native, startup, transport, shared-terminal, image-catalog, Docker and VPN evidence, including their explicit exclusions.

# Limits and loose ends
This audit did not rerun the Go, browser, image, VPN or physical-device test suites. Availability is based on actual interfaces in code and retained observations, not a fresh production certification. [[Loose ends]] records concrete remaining work and verification gaps; parked suspend findings remain separate. Discovery entries are not a new priority order.

Historical code-quality job ownership, duplicate preflight and event-test findings were fixed by commit `02898bb`: the shared `JobTracker`, `checkHostSources` and focused tests are present. The original review remains evidence of that point in time, not a current defect list. The release coverage gaps in later evidence remain visible.

# Record migration
Completed and cancelled execution plans are removed after useful findings were retained in current specs, architecture, feature records and verification. The mobile prototype's media is retained under its verification record. Selkies performance experiments preserve their measured settings; the historically tested audio-idle gate is disabled by default today. Removed image-authoring and obsolete workspace/custom-audio designs are pruned rather than presented as current behavior. Git retains the removed documents.
