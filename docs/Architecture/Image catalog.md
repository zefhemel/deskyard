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
The desktop images desktops can be made from (`images.json`), their versions from the image contract labels, update checks against the registry, and upgrades. Custom images, built here from a [[Architecture/Recipes|recipe]] or from a desktop by the [[Architecture/Image builder]], sit in the catalog with `custom: true`, tagged `deskyard-custom/<slug>:latest`. They are never pulled, show a lock badge when they contain secrets, and show "Base updated" when their base has moved on.
