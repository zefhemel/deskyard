---
displayName: Image builder (removed)
---
The supervisor image builder was removed on 24 October 2026 at the user's request. It is no longer an architecture component. Historical behavior is recorded in [[superpowers/specs/2026-10-22-custom-images-design|Custom images design]] and [[superpowers/plans/2026-10-22-custom-images|Custom images plan]]. The later [[superpowers/specs/2026-10-24-image-dockerfiles-design|Dockerfile editor proposal]] was cancelled before shipment.

The recipe/build/image-from-desktop routes, root scanner, embedded scanner binaries and engine build/tag/image-deletion methods are removed. Supervisor builds and releases no longer compile or embed an image scanner. Starting the supervisor leaves old authoring JSON, source folders and build-context folders untouched.

Build images independently with Docker, including the standalone examples under `examples/`, then add the resulting local or registry reference through [[Architecture/Image catalog]]. Adding an already present image checks its contract without forcing a registry pull. Removing it from the catalog preserves its Docker tags and saved source. Existing desktops and image runtime home-template behavior remain supported.
