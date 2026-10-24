---
displayName: Image folders and Dockerfile editor plan
tags: plan
status: done
date: "2026-10-24"
spec: "[[superpowers/specs/2026-10-24-image-dockerfiles-design|Image folders and Dockerfile editor design]]"
---

# Closed — 24 October 2026

The user cancelled the Dockerfile editor and requested removal of supervisor image authoring. Existing images, desktops, JSON and source files are preserved; standalone Dockerfiles remain supported through manual builds and [[Architecture/Image catalog|Add image]]. This plan is closed as cancelled, not implemented. The editor/backend work was removed before shipment and its planned demonstration was not performed.

# Image folders and Dockerfile editor implementation

**Goal:** Make each image an inspectable source folder, edit its Dockerfile in CodeMirror, and demonstrate a real RetroArch build through that flow.

**Architecture:** Extend the existing recipe/build pipeline with persistent folder-backed Dockerfile sources and saved-only API operations. Keep legacy recipe callers compatible and preserve existing image checks. Replace the structured UI form with the agreed Dockerfile-only editor.

**Tech stack:** Go, Docker engine API, Preact, CodeMirror 6, Playwright and existing image acceptance tests.

**Spec:** [[superpowers/specs/2026-10-24-image-dockerfiles-design|Image folders and Dockerfile editor design]].

# Constraints

- Do not overwrite unrelated working-tree changes or restart/reconcile unrelated desktops.
- Accounts/tokens stay in private JSON; every known image gets a folder under `.deskyard/images/`.
- Save never starts a build; Build saves visible edits then builds an immutable snapshot with caching.
- First version edits Dockerfile only; real supporting context files remain available on disk.
- Tests first for behavior; no fake build result used for the RetroArch demonstration.

# Tasks

- Cancelled: Backend: folder persistence and migration, Dockerfile/save APIs, safe context snapshots, build arguments/cache, compatibility tests.
- Cancelled: UI: CodeMirror editor and template, Save/Build flows, unbuilt definitions, mock fixtures and browser tests.
- Cancelled: Integration: resolve interface issues, review implementation and documentation, run Go/UI/build/image checks.
- Cancelled: Demonstration: stage RetroArch folder, edit/save/build through UI, validate resulting image from an empty workspace, save evidence.

# Execution decisions

The user's prior UI design approval and explicit instruction to build authorize implementation; no second approval prompt is needed. Work remains in the shared checkout because it contains the already tested RetroArch example and prior fixes; changes are scoped by ownership and no unrelated files are reset or committed. The backend and frontend tasks share an API contract and are reviewed together before the real demonstration. Registry-definition folders describe pulling/extending their upstream image and do not pretend to contain upstream source history.
