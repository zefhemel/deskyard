---
displayName: Image folders and Dockerfile editor design
tags: spec
status: superseded
date: "2026-10-24"
plan: "[[superpowers/plans/2026-10-24-image-dockerfiles|Image folders and Dockerfile editor plan]]"
---

# Superseded — 24 October 2026

The user cancelled the Dockerfile editor and requested removal of all supervisor image authoring. Recipe/build/image-from-desktop APIs, UI and scanner code are removed. Existing images, desktops and saved authoring files remain intact. Build Dockerfiles separately and add their local or registry references through [[Architecture/Image catalog]]. This proposal did not ship; its source-folder migration and editor behavior below are historical plans, not current behavior.

The user approved replacing the structured image recipe editor with a CodeMirror Dockerfile editor, separate Save and Build actions, and an inspectable folder per known image. This supersedes the Dockerfile-editing non-goal in [[superpowers/specs/2026-10-22-custom-images-design|Custom images design]] for this flow; existing image validation, cancellation, catalog updates and desktop persistence remain required.

# Source and storage

`DESKYARD_DATA_DIR/.deskyard/images/` contains image definition folders, including definitions for the built-in XFCE, KDE and MATE catalog choices and other added registry images. Each folder has a readable `Dockerfile`, minimal JSON metadata and any build-context files. A pulled image's definition references its upstream image with `FROM`; it does not claim to recreate the upstream build. Custom images use their saved Dockerfile and context as their source of truth. Global account/token JSON storage is unchanged.

Legacy recipes and catalog state migrate without rebuilding images. Migration preserves structured install steps, remote keys and image-from-desktop home templates. Missing legacy home-template material must produce an actionable error rather than silently dropping it. Migration never overwrites an existing edited Dockerfile or auxiliary file. Original JSON remains available as a migration input/backup, not the canonical editable image source.

# Editor

The first version edits only the Dockerfile in CodeMirror with Dockerfile syntax highlighting, line numbers, keyboard editing and a visible accessible label. Auxiliary files may be managed on the supervisor filesystem; their paths/build-context membership are explained, but there is no file-tree editor in this version. Name and a catalog base chooser remain. New definitions start from a readable template with a clearly marked section for additional `RUN` instructions and commented Flatpak, mise and apt installation examples. The examples install into image-owned paths, not the runtime `/workspace` mount.

Save persists the exact editor contents without starting a build and keeps the definition visible before its first build. Build saves the currently visible contents, snapshots the folder, then starts the existing cancellable build log. A failed build preserves saved source and the previous successful image. Existing custom images reopen their saved Dockerfile; structured fields must not regenerate edits. Registry base definitions can be inspected and used to create an editable custom image rather than retagging an upstream registry reference.

# Build and compatibility

Image builds use Docker layer caching by default. The template uses `BASE_IMAGE` as the selected catalog base; the builder resolves that base, supplies its image identity as a build argument and records provenance. Multi-stage Dockerfiles and `COPY` support the RetroArch example. Build contexts are immutable snapshots of the image folder; concurrent Save cannot change an active build's bytes. Context collection confines paths to the image folder, rejects unsafe filesystem entries/escapes and respects `.dockerignore` so excluded content does not reach the engine.

All edit/save/build APIs remain admin-only. Existing contract validation, root scanner, smoke desktop, timeout, job cancellation and tag/catalog rollback remain in force. Legacy API clients can still submit structured recipes while the UI uses Dockerfile definitions. Context metadata is not treated as a secret-free export guarantee; existing secret marking and inherited secret behavior remain intact.

# Acceptance

Tests must prove exact save/reopen without an engine build; building unsaved editor contents through Save-then-Build; cached rebuilds; safe immutable contexts including an auxiliary file; legacy conversion; folders for built-in and added images; failure retention; and non-admin refusal. UI tests verify real CodeMirror highlighting/editing and distinct Save/Build behavior. The RetroArch folder is staged as a managed image definition, edited/saved/built through the UI, and its resulting image is tested from an empty workspace. Demonstration must include real build output and a visible screenshot; no mocked success is evidence for the real build.
