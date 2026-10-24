---
displayName: Recipes (removed)
---
The supervisor recipe editor, structured recipe APIs and image-from-desktop export were removed on 24 October 2026 at the user's request. This page preserves the old component's identity for historical links; it is no longer a component in the architecture diagrams. The historical design is [[superpowers/specs/2026-10-22-custom-images-design|Custom images design]], now superseded. The proposed Dockerfile editor was cancelled before shipment; see [[superpowers/specs/2026-10-24-image-dockerfiles-design|Image folders and Dockerfile editor design]].

The supervisor no longer reads or migrates `.deskyard/recipes.json` or image source folders. Existing source files, built Docker images, catalog references and desktops remain intact. Build custom Dockerfiles separately and add the resulting references through [[Architecture/Image catalog]].

The image runtime still supports system-wide mise tools in `/opt/mise` with executables linked into `/opt/deskyard/bin`, system Flatpak apps and `/opt/deskyard/home-template`. `images/common/rootfs/usr/local/bin/workspace-init` copies the template as `user` into an empty workspace before filling remaining skeleton files, without overwriting existing files. The supervisor retains the `deskyard.secrets=true` label check and confirmation when sharing or changing to an image marked with secrets.

`examples/retroarch/` remains a standalone Dockerfile example. It installs RetroArch, source-builds five pinned cores and seeds their per-user files only into new workspaces. Build/test instructions, mutable inputs, offline acceptance checks and non-commercial core restrictions are documented in its README; see [[Licensing]].
