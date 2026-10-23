---
displayName: Desktop image variants plan
tags: plan
status: done
date: "2026-10-23"
spec: "[[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]]"
---
**Goal:** Offer XFCE, KDE Plasma and MATE with one shared desktop runtime.

**Architecture:** `images/Dockerfile` branches from `runtime` into three final targets. Common rootfs/patches are shared; desktop adapters own native configuration, start, logout and readiness. Bake builds all targets in one graph per architecture.

**Tech Stack:** Debian trixie, X11/Xvfb, existing Selkies/PipeWire stack, Bash, Docker Buildx Bake, pytest/Playwright.

**Spec:** [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]].

# Global constraints

- Preserve contract 3, UID/GID 1000, `/workspace`, display `:1`, port 8080, the proxy token boundary and the current Python 3.13 Selkies client path.
- Keep XFCE's published name/default and existing home settings.
- No duplicate codec/tool builds per variant, new root services, display managers or sudo.
- Separate measured results from expectations, and native local checks from amd64/arm64 CI coverage.

# Tasks

- [x] Add a failing container test proving desktop identity/readiness and isolation of desktop package sets; run against the existing XFCE image.
- [x] Extract `images/common/rootfs`, `images/common/patches` and `images/Dockerfile`; create KDE/MATE overlays and a shared session lifecycle; verify all three native builds.
- [x] Add `docker-bake.hcl`; update image CI to build the group once per architecture and publish each digest; validate Bake output and workflow syntax.
- [x] Move tests to `images/tests`, select `AC_DESKTOP`, make readiness/security checks desktop-independent, retain XFCE-specific layout checks, and run core suites on all variants.
- [x] Verify browser and native session persistence, desktop switching, default browser and custom-image compatibility; fix any regressions before claiming support.
- [x] Ship graphical Flatpak stores (Discover in KDE; GNOME Software in MATE/XFCE), verify user-scope GUI installation and LibreOffice discovery.
- [x] Measure footprints and image sizes; update README, affected architecture pages and licensing assessment with actual behavior and remaining limitations.
- [x] Check documentation links/metadata and SilverBullet queries, review the implementation/docs diff, and report verification evidence.

# Validation and remaining release checks

Native ARM64 results, screenshots, footprint samples and known limitations are recorded in [[Testing/Desktop variants 2026-10-23|Desktop variants testing — 23 October 2026]]. All three images built locally; selected shared and desktop-specific runtime suites passed after the browser/session/opener corrections. The shared-browser shutdown regression was observed failing before its fix and passing afterward. The document-portal FUSE limitation and one initial blank-frame observation remain documented. amd64 runtime checks, the full optional download/capacity matrix and registry publication are separate release checks, not implied by local completion.

# Appearance follow-up — 23 October 2026

- [x] Apply the selected MATE preview as native defaults, keep KDE unchanged, verify fresh appearance and preserved user settings, and update documentation.

# Catalog discovery follow-up — 23 October 2026

- [x] Expose built-in XFCE/KDE/MATE metadata before download, preserve saved defaults and custom images, support explicit and first-use installation, and skip undownloaded images during updates.
- [x] Verify API authorization, deduplication, failure/retry state and local-version tracking; check the UI install/picker flows and update catalog/API documentation.

See [[Testing/Image catalog 2026-10-23|Image catalog testing — 23 October 2026]] for evidence and screenshots.
