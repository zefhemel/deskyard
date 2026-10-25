---
displayName: Supervisor design
tags: spec
status: implemented
date: "2026-10-21"
features:
  - "[[Features/Desktop lifecycle]]"
  - "[[Features/Accounts and access]]"
  - "[[Features/Persistent workspace]]"
  - "[[Features/Images and reusable environments]]"
  - "[[Features/File exchange]]"
  - "[[Features/Remote CLI access]]"
  - "[[Features/Port forwarding]]"
  - "[[Features/Host folder mounts]]"
  - "[[Features/Docker daemon access]]"
---
Deskyard is one Go supervisor with an embedded Preact/TypeScript UI, running persistent Linux desktops in Docker containers. People share one display, pointer, keyboard, Chromium profile and workspace per desktop through the browser or `deskyard` CLI. There is no independent input seat or arbitration between users.

# Access and trust
Username/password accounts are administrator or regular accounts. Administrators manage accounts, images, desktop settings and access; regular accounts can view and power desktops granted to them. Session epochs invalidate old logins after authorization changes. Anyone with desktop access can create a named, optionally expiring access token for that desktop. Tokens reach its shell, files, browser and screen through the compatible `/api/agent/v1/` API, without lifecycle, account or settings privileges. Revocation, expiry and creator authorization loss close affected connections. Token authority does not revive after regrant or account recreation.

The supervisor owns the engine socket, which is root-equivalent. Ordinary desktops have no sudo. Flatpak namespace support requires relaxed container security options; user-facing services and supervisor-issued commands run as `user`, not root. Explicit Docker daemon access is a separate administrator opt-in granting root-equivalent engine access; it is not part of the default desktop.

All desktops share the supervisor origin. The supervisor serves its own UI and the Selkies client extracted from an immutable image, never pages from a running desktop. Authenticated, same-origin stream connections reach token-protected Caddy; file operations use owned helpers as `user`. Arbitrary desktop web servers must not be published under supervisor-origin paths. See [[Architecture/Desktop proxy]], [[Architecture/Session]] and [[Architecture/Access tokens]].

# Runtime and storage
The engine supplies desktop discovery and settings through managed-container labels. Stable desktop IDs survive recreation. Accounts/access, tokens, image catalog, memory policy, session keys and certificates live in private supervisor files. Direct desktop replacement also uses a durable recovery journal; VPN assignments and complete network transitions have separate durable intent under `.deskyard/vpn/`. Labels alone are not the authority for interrupted operations.

Each desktop has a plain host folder mounted as `/workspace`, owned by UID/GID 1000. Removing a desktop preserves it unless deletion is explicitly requested. Image upgrades and desktop switches preserve existing files. In a containerized supervisor, its data folder must have the same host and container path because mount sources are interpreted by the engine.

Direct desktops have a per-desktop bridge with a /28 from `DESKYARD_SUBNET`. A native supervisor reaches Caddy through loopback-only port publication; a containerized supervisor joins desktop networks and desktops publish no host ports. OrbStack/Docker Desktop bridge routing does not itself isolate desktop services: the private per-desktop proxy token remains essential. VPN-enabled desktops instead share a dedicated managed gateway namespace; see [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]].

Settings changes and upgrades reserve the desktop, preflight requested host folders/socket, pin the prior immutable image and persist recovery intent before removing the old container. Replacement requires readiness; failure or interrupted startup restores the previous direct desktop with bounded recovery. Failed recovery retains its record and blocks conflicting operations. VPN-selected recovery never falls back to direct networking. See [[Architecture/Lifecycle]].

# Interfaces and scope
The UI provides desktop listing, lifecycle, monitoring, accounts, access, image catalog and per-desktop settings. Browser event feeds use WebSockets, with SSE retained for HTTP consumers and job streams. Shared terminal, CLI activity, files, application/window views, mobile touch controls and online Home Screen installation are implemented; their component pages and topic specs define the detailed contracts.

Images use contract 3 and the common Debian runtime with XFCE and MATE final layers. Administrators add separately built local or registry images to the catalog. Supervisor image authoring is removed. The CLI supports local desktop commands and remote exec/shell/shared terminal/files/CDP/screen operations plus loopback TCP forwarding.

Docker is the only implemented engine adapter; Podman remains a possible extension, not supported behavior. No SSO, email reset, per-tool token restrictions, backup service or arbitrary supervisor-origin application hosting is promised. HTTPS uses CertMagic when `DESKYARD_DOMAIN` is configured; plain HTTP defaults to loopback. The original public-host certificate issuance check was not demonstrated in the retained local evidence. Current commands and configuration are in `README.md`; API contracts are in `supervisor/API.md`.
