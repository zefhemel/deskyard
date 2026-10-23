---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/API]]"
  - "[[Architecture/Agent activity]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
Preact + TypeScript (strict) + Tailwind, built with Vite and embedded in the binary: the desktops wall, a desktop's details page, images, accounts, login, and the desktop page at `/<name>/`: our own bar around [[Architecture/Selkies]]' client in an iframe (its sidebar hidden), driven through `web/src/desktop/selkies.ts`. The bar has who's viewing (with mic and camera badges), Audio (sound, volume, output, mic and webcam), Lock size, Clipboard, Keys, Quality and Download (a small file browser of `/workspace`); dropping files on the screen uploads them to `~/Desktop`. Agents' on-screen actions are drawn over the picture. Updates live over server-sent events.

The desktop toolbar uses its compact layout below 1024 CSS pixels: Lock size, Keys, Quality and Download move into More while Audio and Clipboard remain directly accessible. Shared menus scroll within the remaining visual viewport and recalculate their height on viewport changes (`web/src/components/menu.tsx`). Clipboard keeps drafts through unrelated window blurs and dismisses on a real interaction inside the screen frame (`web/src/desktop/clipboard.tsx`). See [[Testing/Mobile 2026-10-23|Mobile testing — 23 October 2026]] for the mobile checks and remaining device limitations.

A running desktop's details page gives admins a **Reclaim memory** button (`web/src/components/reclaim-memory.tsx`). One click starts the request, disables the button while pending, and shows progress followed by measured resident bytes before/after or an error. The result explains that swapped pages may still use compressed RAM and identifies partial reclamation; shared toasts accommodate multiple lines within the phone viewport (`web/src/components/toast.tsx`). The button has no confirmation dialog; its tooltip notes that subsequent app interaction can be slower.

The button also depends on the host's cached boot-probe result from `GET /api/host`: it stays disabled while host capabilities load or if the startup probe failed. A failed probe shows its reason alongside the disabled button. The UI does not launch probes when opening a details page or polling host stats.

Desktop Settings has an **Automatically reclaim idle memory** checkbox (`web/src/pages/details/settings-tab.tsx`), separate from settings that recreate the container. It saves immediately through `api.setMemoryPolicy`, survives reloads/upgrades and shows the host's capability failure reason when unavailable. It explains the 15-minute/no-viewers one-shot policy, busy skips and potential interaction delay; manual reclamation is independent of the opt-out.
