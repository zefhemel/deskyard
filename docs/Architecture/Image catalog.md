---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Image builder]]"
  - "[[Architecture/Recipes]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The desktop images desktops can be made from (saved entries in `images.json` plus built-in choices in `supervisor/internal/server/official_images.go`), their versions from the image contract labels, update checks against the registry, and upgrades. Custom images, built here from a [[Architecture/Recipes|recipe]] or from a desktop by the [[Architecture/Image builder]], sit in the catalog with `custom: true`, tagged `deskyard-custom/<slug>:latest`. They are never pulled, show a lock badge when they contain secrets, and show "Base updated" when their base has moved on.

XFCE remains the first-start default. The list merges saved entries with official XFCE, KDE Plasma and MATE metadata, deduplicating normalized references and preserving saved order and `DESKYARD_IMAGE`. Browsing neither persists these choices nor pulls them; titles and descriptions exist before download. `installed` reports local engine availability and `official` identifies a built-in reference. This is a static application catalog, not enumeration of the remote registry. Desktop choice and custom-image application sets are independent. See [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]].

Admins can explicitly install a catalog image through `POST /api/images/install`, or let desktop creation or a recipe build pull it on first use. Pulls retain the existing contract validation and job progress/error flow; a failed download remains available for retry. Successful local use records the resolved version without pretending a registry check occurred. Daily/manual update checks skip missing and custom images, so discovery does not cause background downloads. The Images page shows Available, Installed or Update available (when desktops are behind); built-in choices cannot be removed, while arbitrary saved refs and custom-image workflows retain their existing removal behavior. The implementation is in `supervisor/internal/server/images.go`, `official_images.go` and `supervisor/web/src/pages/images.tsx`; the shared desktop picker indicates first-use downloads.
