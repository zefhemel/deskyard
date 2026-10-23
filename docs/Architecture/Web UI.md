---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/API]]"
  - "[[Architecture/Agent activity]]"
  - "[[Architecture/VPN connections]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Preact + TypeScript (strict) + Tailwind, built with Vite and embedded in the binary: the desktops wall, a desktop's details page, images, accounts, login, and the desktop page at `/<name>/`: our own bar around [[Architecture/Selkies]]' client in an iframe (its sidebar hidden), driven through `web/src/desktop/selkies.ts`. The bar has who's viewing (with mic and camera badges), Audio (sound, volume, output, mic and webcam), Lock size, Clipboard, Keys, Quality and Download (a small file browser of `/workspace`); dropping files on the screen uploads them to `~/Desktop`. Agents' on-screen actions are drawn over the picture. Updates live over server-sent events.

The administrator-only VPN connections page is `/_vpn` (`web/src/pages/vpn.tsx`), leaving the desktop name `vpn` available at `/vpn/`. It provides named NordVPN configurations, service-credential help, canonical country choices, disposable test/validation jobs and cancellation, reference-aware deletion and confirmed shared edits using the preview revision and stable desktop IDs. It shows each rollout result, including failures in a completed job, with links to retry the affected desktop. Secret inputs always start blank and omit blank edit values. Desktop creation and Settings use `web/src/components/vpn.tsx` for saved selections and port-specific local-network exceptions; `web/src/lib/vpn-form.ts` validates form payloads and distinguishes omitted settings from explicit No VPN. VPN health appears separately on desktop tiles, details and the desktop toolbar; regular accounts have read-only status. Current exit IP is shown only while connected, historical IP has its observation time, and idle status explains continued provider usage.
