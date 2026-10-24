---
tags: component
status: built
partOf: "[[Architecture/Image catalog]]"
uses:
  - "[[Architecture/Recipes]]"
  - "[[Architecture/Engine adapter]]"
  - "[[Architecture/Desktop]]"
spec: "[[superpowers/specs/2026-10-22-custom-images-design|Custom images design]]"
---
Builds a custom image from a [[Architecture/Recipes|recipe]] with the engine's image build on the supervisor's machine, as a cancellable job with a 30-minute limit (`internal/server/build.go`, `checks.go`, `fromdesktop.go`).

A build:
1. Takes the base's image ID, pulling the base first on a rebuild unless it is custom. The base must carry the contract.
2. Builds `FROM <base id>` from the generated Dockerfile, with no cache on a rebuild. It runs on the engine's default bridge, not attached to the supervisor's network, with no Docker socket or host mounts. The log streams to the Build dialog.
3. Runs the root check. The supervisor's static scanner (`cmd/deskyard-image-scan`, embedded through `internal/scanbin`) is copied into a throwaway container of the base and of the new image, with no network and the healthcheck off, and run as root. The new image must not add or change sudo or doas, setuid or setgid files, file capabilities, files `user` can write under root-run paths, symlinks from those into writable places, or privileged account entries. Any scan failure fails the build. `/opt/mise` and `/var/lib/flatpak` are not scanned for writability.
4. Starts a throwaway desktop (`build-check-…`) and waits until it is ready.
5. Tags `deskyard-custom/<slug>:<time>` and `:latest`, adds the catalog entry, and deletes older timestamp tags no container uses.

A failed build leaves no tag and no entry. The daily update check skips custom images; desktops on a rebuilt image show "update available".

For an image from a desktop, the supervisor first copies the chosen files into the build context. Where it can read everything it walks the folder through an `os.Root`, otherwise through an `alpine:3.22` helper container, and the build copies the result with `COPY --chown=1000:1000`. The helper archive can store repeated file contents as hard links (for example npm packages linked to a cache). The supervisor rereads those contents and exports each selected name as a regular file, even when the first name is in an excluded cache; selection and secret filtering still apply to each exported name. Known limits: helper-backed exports reject hard-link targets containing a newline with an explicit error, rather than silently omitting their aliases. Flatpak apps with extra data (`apply_extra`) may fail to build, and a build can reach whatever the engine's default bridge reaches, the supervisor through the host included. Both are in `supervisor/API.md`.

Flatpak discovery in `supervisor/internal/server/fromdesktop.go` loads remote keys before returning, for running and stopped desktops alike. Recipe assembly uses the loaded data after discovery ends, without borrowing a canceled request context or a closed filesystem root. Missing keys still produce explicit omission notes; running key-read failures also retain their cause in discovery notes.
