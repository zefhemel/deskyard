---
tags: component
status: built
partOf: "[[Architecture/Supervisor]]"
uses:
  - "[[Architecture/Engine adapter]]"
spec: "[[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]]"
---
The desktop images desktops can be made from (saved entries in `images.json` plus built-in choices in `supervisor/internal/server/official_images.go`), their versions from image labels, registry update checks and upgrades. Image authoring has been removed; custom images are built independently with Docker and added by reference. Existing locally built images and desktops remain usable. The `deskyard.secrets=true` label still drives the lock badge and secret-sharing confirmation.

XFCE remains the first-start default. The list merges saved entries with official XFCE and MATE metadata, deduplicating normalized references and preserving saved order and `DESKYARD_IMAGE`. Browsing neither persists these choices nor pulls them; titles and descriptions exist before download. `installed` reports local engine availability and `official` identifies a built-in reference. This is a static application catalog, not enumeration of the remote registry. Desktop choice and custom-image application sets are independent. See [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]].

Admins can install a catalog image through `POST /api/images/install`, add a local or registry reference through `POST /api/images`, or let desktop creation pull an available image on first use. Add/install uses a present local image and validates its contract; it pulls only missing references. Local `deskyard-custom/…` tags are accepted. A failed download remains available for retry. Successful local use records the resolved version. Daily/manual update checks skip missing images and retain the exclusion for legacy locally built entries. The Images page shows Available, Installed or Update available when desktops are behind. Built-in choices cannot be removed; removing any other unused catalog entry leaves Docker tags, images and old source files intact. The implementation is in `supervisor/internal/server/images.go`, `official_images.go` and `supervisor/web/src/pages/images.tsx`; the shared desktop picker indicates first-use downloads.

Catalog additions, removals and version updates prepare a separate next list, persist it atomically, then publish it. Persistence failure leaves the current catalog unchanged. Install and update jobs report write failures; background local-image observations log them.

The `deskyard.secrets=true` image label still requires confirmation when sharing or switching to an image marked with secrets. Removing image-authoring APIs did not remove that protection. Old authoring JSON and source folders are left untouched at startup, without migration; catalog removal preserves existing engine tags and saved files. Supervisor builds and releases contain no root scanner.
