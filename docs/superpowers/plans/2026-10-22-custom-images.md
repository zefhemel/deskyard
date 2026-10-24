---
displayName: Custom images plan
tags: plan
status: done
date: "2026-10-22"
spec: "[[superpowers/specs/2026-10-22-custom-images-design|Custom images design]]"
---

# Closed — 24 October 2026

The user cancelled the Dockerfile editor and requested removal of supervisor image authoring. Existing images, desktops, JSON and source files are preserved; standalone Dockerfiles remain supported through manual builds and [[Architecture/Image catalog|Add image]]. This completed historical implementation plan is retained below; its authoring code has now been removed.


# CI follow-up — 23 October 2026

- [x] Preserve hard-linked package files when a Linux permission helper reads the workspace; cover excluded-cache targets and multiple selected aliases in a regression test.
- [x] Seed integration fixtures through the desktop user and scope build cancellation assertions to the test’s own build container.

The original implementation checklist below is retained as historical context; this follow-up records the CI corrections separately.

Image paths in this completed plan describe the original layout. The [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]] moves shared files to `images/common/`, the Dockerfile to `images/Dockerfile` and image tests to `images/tests/`.
# Custom Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let admins build their own desktop images in the supervisor's UI, in two ways:
- **recipes:** a base plus mise tools, Flatpak apps and remotes, and build commands;
- **from an existing desktop:** its home folder becomes the starting home for new desktops.

The images can be rebuilt, moved to another base, and clearly marked when they contain secrets.

**Architecture:**
- **Recipes and Dockerfiles.** A new package `internal/recipes` holds the recipe model, its validation, Dockerfile generation and the secret-path list.
- **Builds.** The supervisor builds through a new `Engine.Build` (and `Commit`, for the Flatpak fallback) as a cancellable job. It then runs two checks (root scan and smoke test), tags the result `deskyard-custom/<slug>:<stamp>` and `:latest`, and records it in the catalog as `custom`.
- **The image side** is small: `workspace-init` copies `/opt/deskyard/home-template` on first start, and adds the remotes in `/etc/deskyard/flatpak-remotes/` at user scope.

**Tech Stack:** as the supervisor (Go 1.27, moby client, Preact + TypeScript strict, pytest + Playwright).

**Spec:** `docs/superpowers/specs/2026-10-22-custom-images-design.md`

## Global Constraints

- **Names and labels.**
  - Tags: `deskyard-custom/<slug>:<YYYYMMDD-HHMMSS>` and `deskyard-custom/<slug>:latest`. The catalog ref is the `:latest` one.
  - `<slug>` matches the desktop-name rule `^[a-z][a-z0-9-]{0,30}[a-z0-9]$`.
  - Labels: `deskyard.contract` (inherited, and must equal the supervisor's `desktops.Contract`), `org.opencontainers.image.title` (recipe name), `org.opencontainers.image.version` (build time `YYYY-MM-DD HH:MM`), `deskyard.base` (the base image ID), `deskyard.recipe` (recipe JSON), and `deskyard.secrets=true` when it contains secrets.
- **State.** `.deskyard/recipes.json` (mode 0600, atomic writes via `internal/store`). Catalog entries for custom images get `"custom": true`. The daily update check never pulls them.
- **Build environment.**
  - Builds can be cancelled, with a 30-minute timeout.
  - No build cache on Rebuild. Rebuild pulls the base first, unless the base is custom.
  - Builds get network access, but no Docker socket, no host mounts and no supervisor network.
- **Where things go.**
  - mise tools are installed into `/opt/mise`, and their executables are linked into `/opt/deskyard/bin`, which is on `PATH` right after the desktop's own mise shims. `/opt/mise/shims` is not on `PATH`.
  - Flatpak is installed system-wide with `flatpak.real --system`.
  - Extra remotes also go in `/etc/deskyard/flatpak-remotes/<name>.flatpakrepo`.
  - The home template goes in `/opt/deskyard/home-template`, owned by 1000:1000.
- **First start.** Inside `workspace-init`'s `.workspace-seeded` block, before the skel copy: `runuser -u user -- cp -an /opt/deskyard/home-template/. /workspace/`. Nothing root runs is taken from `/workspace`.
- **Checks on every build.**
  - **Root scan:** compared with the base, the new image adds no sudo/doas, no setuid/setgid file, and no file writable by UID 1000 under `/usr`, `/etc`, `/opt/workspace`, or `/opt/deskyard` outside `home-template`.
  - **Smoke test:** a throwaway desktop of the image becomes ready (`WaitReady`, 2 min).
  - A failure leaves no tag and no catalog entry.
- **Secrets.** Detection is by path and name only. "Contains secrets" passes down a chain.
- **Admin only:** every route and UI action here.
- **From a desktop:** a warning above 5 GB total, with no hard limit.

## Review Focus

- **A build command that never finishes** (`sleep infinity`) is cancelled at 30 min, or by Cancel, and leaves no container, image or tag behind. Test: Task 4.
- **Deleting a recipe while a desktop runs its image** is refused (409), like removing a catalog image in use. Test: Task 4.
- **A desktop folder reused by name** gains only missing template files; its own `.bashrc` stays. Test: Task 2.
- **A secret file inside a ticked group** (`~/.config/gh/hosts.yml` under App settings) stays out unless ticked itself. Test: Task 6.
- **Two builds of the same recipe at once:** the second is refused with 409 "already building". Test: Task 4.

---

### Task 1: `Engine.Build` and `Engine.Commit`

**Files:**
- Modify: `supervisor/internal/engine/engine.go`
- Modify: `supervisor/internal/engine/docker/docker.go`
- Modify: `supervisor/internal/engine/enginetest/suite.go`
- Modify: the fake engine in `supervisor/internal/server/server_test.go` (stubs)

**Interfaces:**
- Produces:
  - `type BuildSpec struct { Context io.Reader /* tar */; Dockerfile string; Tags []string; Labels map[string]string; NoCache, Pull bool }`
  - `Build(ctx context.Context, spec BuildSpec, log func(line string)) (imageID string, err error)`: streams BuildKit or classic build output lines. Cancelling ctx stops the build.
  - `Commit(ctx context.Context, containerID string, ref string, changes []string, labels map[string]string) (imageID string, err error)`
  - `RemoveImage(ctx context.Context, ref string) error`: `ErrNotFound` when missing; an error mentioning "in use" when a container uses it.
  - `Tag(ctx context.Context, imageID, ref string) error`

- [ ] **Step 1: Write failing engine-suite tests** (build tag `engine`).
  - `TestBuildAndLabels`: build `FROM alpine:3.22\nRUN echo hi > /x` with a label. The returned ID inspects with the label, and the log saw "hi" or the step text.
  - `TestBuildCancel`: `RUN sleep 600`, cancelled after 2 s, returns an error within 10 s.
  - `TestBuildFailure`: `RUN false` returns an error whose text includes the failing step.
  - `TestCommit`: commit a container that wrote a file; the image has it.
  - `TestTagAndRemoveImage`.
- [ ] **Step 2: Run** `go test -tags engine ./internal/engine/...`. Expect FAIL.
- [ ] **Step 3: Implement** with the moby client's `ImageBuild`, `ContainerCommit`, `ImageTag` and `ImageRemove`.
- [ ] **Step 4: Run** the engine and unit tests. Expect PASS.
- [ ] **Step 5: Commit.** Message: "engine: Build, Commit, Tag, RemoveImage".

### Task 2: The image side: home template and extra remotes

**Files:**
- Modify: `images/debian-xfce/rootfs/usr/local/bin/workspace-init`
  - The template copy, per Global Constraints.
  - For each `/etc/deskyard/flatpak-remotes/*.flatpakrepo`, add it as `runuser -u user -- flatpak remote-add --user --if-not-exists <basename> <file>`.
- Modify: `images/debian-xfce/Dockerfile`: `mkdir -p /opt/deskyard/home-template /etc/deskyard/flatpak-remotes` (empty), and `/opt/deskyard/bin` (empty, root-owned) on `PATH` after the user's mise shims; `/opt/mise/shims` stays off `PATH`.
- Test: `images/debian-xfce/tests/test_custom_base.py`.

- [ ] **Step 1: Write failing tests.** Each builds a tiny derived image in the test with `docker build` (`FROM deskyard-debian-xfce:test`, plus `COPY` of files into the template or remotes dirs).
  - `test_template_seeds_new_home`: a new desktop has `/workspace/.config/x/y` from the template, owned by 1000.
  - `test_template_keeps_existing_files`: a folder that already has `.bashrc` (with content A) and no seeded marker keeps A, and gains the template's other files.
  - `test_extra_remote_added_for_user`: `flatpak remotes --user` lists the remote.
  - `test_image_tool_on_path_after_user`: a real tool installed into `/opt/mise` and linked into `/opt/deskyard/bin` runs, and the user's own shim of the same name wins; `test_base_node_resolves_to_runtimes_not_mise`.
- [ ] **Step 2: Run.** Build the image, then `uv run pytest test_custom_base.py`. Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the tests above, plus `test_privileges.py` and `test_boot.py`. Expect PASS.
- [ ] **Step 5: Commit.** Message: "image: home template and extra Flatpak remotes for custom images".

### Task 3: Recipes and Dockerfile generation (`internal/recipes`)

**Files:**
- Create: `supervisor/internal/recipes/recipe.go`, `dockerfile.go`, `store.go`
- Test: `supervisor/internal/recipes/recipe_test.go`, `dockerfile_test.go` (golden files in `testdata/`)

**Interfaces:**
- `type Recipe struct`:
  - `Slug, Name, Base string`; `Base` is a catalog ref.
  - `Mise []string`
  - `Remotes []Remote`, with `Remote{Name, RepoFile /* .flatpakrepo URL */, URL, GPGKey string}`
  - `Flatpaks []Flatpak`, with `Flatpak{ID, Remote string; Ref string /* .flatpakref URL, alternative to ID+Remote */}`
  - `Commands string`
  - `HomeFrom string /* "" or "image" (copy home-template out of the current image on rebuild) */`
  - `Secrets bool`
- `func (r Recipe) Validate() error`.
  - Slug rule; mise specs match `^[a-z0-9:@/._+-]+$`; Flatpak IDs are reverse-DNS; URLs are `https:`.
  - Remote names match `^[A-Za-z0-9._-]+$`, and aren't `flathub`.
  - Each Flatpak has ID+Remote or Ref, not both.
- `func Dockerfile(r Recipe, baseID string, flatpakInBuild bool) string`. Its stages, in order:
  - `FROM <baseID>`;
  - `USER root`;
  - mise installs into `/opt/mise` (`MISE_DATA_DIR=/opt/mise`, as the base image's Dockerfile does), then each installed tool's executables are linked into `/opt/deskyard/bin`. Find them with `MISE_DATA_DIR=/opt/mise mise bin-paths <tool>@<version>`: npm tools keep theirs in `node_modules/.bin`, not `bin/`. A tool whose bin paths hold no executables fails the build, so no dangling links;
  - the remotes: each `.flatpakrepo` is fetched into `/etc/deskyard/flatpak-remotes/`, then `flatpak.real --system remote-add`;
  - the Flatpak installs, only if `flatpakInBuild`: `flatpak.real --system install -y --noninteractive <remote> <id>`, or for a `.flatpakref`, `flatpak.real --system install -y --noninteractive --from <url>` (whose remote is then saved like the others);
  - `COPY home-template/ /opt/deskyard/home-template/` (only when there's a template), then `chown -R 1000:1000`;
  - the commands, as one `RUN` with `set -eux` and the lines joined;
  - back to the base's `USER` and entrypoint, which are unchanged.

  It never sets `ENTRYPOINT`, `CMD` or `USER` differently from the base.
- `type Store` over `.deskyard/recipes.json`: `List`, `Get(slug)`, `Put(Recipe)`, `Delete(slug)`.

- [ ] **Step 1: Write failing tests.**
  - Validation table: every rule above, valid and invalid.
  - Golden Dockerfiles: `full.Dockerfile` (every field), `minimal.Dockerfile` (base only), `noflatpak.Dockerfile` (`flatpakInBuild=false`).
  - Commands with quotes and `$` survive unchanged, so no shell-quoting bugs.
  - Store round-trip.
- [ ] **Step 2: Run** `go test ./internal/recipes`. Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expect PASS.
- [ ] **Step 5: Commit.** Message: "recipes: model, validation, Dockerfile generation".

### Task 4: Build jobs, checks, catalog and API

**Files:**
- Create: `supervisor/internal/server/build.go` (the build job), `supervisor/internal/server/checks.go` (root scan and smoke test).
- Modify:
  - `supervisor/internal/server/jobs.go`: cancellable jobs. `start` gets a ctx it can cancel, and there's `POST /api/jobs/{id}/cancel` for the owner or an admin.
  - `supervisor/internal/server/images.go`: the catalog gains `Custom bool`. `checkImages` skips custom entries. `updateFor` works for `:latest`. "Base updated" is computed down chains. Remove is refused while in use and also deletes the recipe and its tags.
  - `supervisor/internal/server/routes.go`, `supervisor/API.md`.
- Test: `supervisor/internal/server/build_test.go` (fake engine), `supervisor/tests/test_custom_images.py`.

**Interfaces:**
- API (admin):
  - `GET /api/recipes` gives `[Recipe]`.
  - `POST /api/recipes` with a `Recipe` gives 202 `{"job"}` (validate, save, build).
  - `PUT /api/recipes/{slug}` with a `Recipe` gives 202 `{"job"}` (edit and rebuild).
  - `POST /api/recipes/{slug}/rebuild` gives 202 `{"job"}`.
  - `DELETE /api/recipes/{slug}` gives 204, or 409 if in use.
- `CatalogImage` gains:
  - `custom: bool`;
  - `secrets: bool`;
  - `baseUpdated: bool`;
  - `recipe: string | null` (the slug).
- The job's `message` lines carry the build log. Each build-log line is sent as an SSE `data` update with `message` set to that line and `progress` null.
- How a build runs:
  1. Resolve the base ref to an image ID (pulling it first on Rebuild if it isn't custom).
  2. Check the base's contract.
  3. Assemble the tar context.
  4. `Build`. If the Flatpak step fails with a sandbox error, rebuild without it, then run the Flatpak installs in a container of the result with the desktop's security options, and `Commit`.
  5. Root scan of new versus base: a throwaway container runs `find` as root over the listed paths, then the results are compared.
  6. Smoke test.
  7. Tag the timestamp and `:latest`.
  8. Update the catalog, and mark custom images built on this one as `baseUpdated`.
  9. Delete older timestamp tags of this recipe that no container uses.
- One build per recipe at a time; a second gets 409 "already building".

- [ ] **Step 1: Write failing tests.**
  - Go, with the fake engine:
    - `TestBuildFlowTagsAndCatalog`;
    - `TestBuildRefusedWhenAlreadyBuilding`;
    - `TestRootScanRejects` (fake scan output listing `/usr/bin/sudo`, `/usr/local/bin/x` setuid, and `/etc/foo` writable by 1000: each named in the error);
    - `TestFailedBuildLeavesNothing`;
    - `TestCustomSkippedByUpdateCheck`;
    - `TestBaseUpdatedChain`;
    - `TestCancelAndTimeout` (with a 30-minute constant overridable in tests).
  - Integration, in `test_custom_images.py`:
    - `test_recipe_builds_and_works`: base `deskyard-debian-xfce:test`; mise `npm:cowsay`; a Flathub app (`org.gnome.Calculator`), skipped with a clear reason when there's no network; and `apt-get install -y figlet`. A desktop on it has `cowsay`, `figlet`, and the app in `flatpak list`.
    - `test_build_with_sudo_rejected`: commands `apt-get install -y sudo` give a failed job naming `/usr/bin/sudo`, and no tag.
    - `test_rebuild_and_upgrade_keeps_home`: a desktop writes `~/mine`, then rebuild and upgrade the desktop. `~/mine` is still there, and the image version changed.
    - `test_delete_recipe_in_use_refused`.
    - `test_cancel_build`: commands `sleep 600`, then cancel. Within 15 s the job has failed with "cancelled", and `docker ps -a` shows no build containers left.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the Go tests and `test_custom_images.py`, plus `test_api.py` for regressions. Expect PASS.
- [ ] **Step 5: Commit.** Message: "supervisor: recipe builds, checks, custom catalog entries".

### Task 5: Recipes in the UI

**Files:**
- Create: `supervisor/web/src/dialogs/recipe.tsx`: New image, and Edit and rebuild.
  - Fields: name, base (from the catalog), mise tools (a textarea, one per line), Flatpak apps and remotes (rows; Flathub search uses `https://flathub.org/api/v2/search` from the browser, with a plain ID field if that fails), and build commands (a textarea).
  - The note under the build commands box: "Saved in the image's labels: anyone who can inspect the image can read them. Don't put tokens here."
- Create: `supervisor/web/src/dialogs/build-log.tsx`: the live log (monospace, autoscroll), Cancel, and the failure step highlighted.
- Modify:
  - `supervisor/web/src/pages/images.tsx`: custom rows show Rebuild, a "Base updated" badge, a lock badge for secrets, and Edit.
  - `supervisor/web/src/dialogs/new-desktop.tsx`: the lock badge, with the line "Desktops made from this start signed in as whoever made it".
  - `supervisor/web/src/api.ts`, `dev/mock*.ts`.
- Test: `supervisor/tests/test_ui.py`.

- [ ] **Step 1: Write a failing UI test.** `test_recipe_dialog_builds`: fill the dialog (base, `npm:cowsay`, command `true`) and click Build. The log shows lines; the row appears with Rebuild; Rebuild opens the log again.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement** (mockups aren't needed: follow the existing dialogs' look).
- [ ] **Step 4: Run.** Typecheck, build, then the test. Expect PASS.
- [ ] **Step 5: Commit.** Message: "web: recipes, builds and rebuilds on the Images page".

### Task 6: New image from a desktop

**Files:**
- Create:
  - `supervisor/internal/recipes/secrets.go`: the secret-path list from the spec, as patterns.
  - `supervisor/internal/recipes/scan.go`.
  - `supervisor/internal/server/fromdesktop.go`.
  - `supervisor/web/src/dialogs/image-from-desktop.tsx`.
- Modify:
  - `supervisor/web/src/pages/details.tsx`: an admin action "New image from this desktop".
  - The access dialog: confirm when the desktop's image has secrets.
- Test: `supervisor/internal/recipes/scan_test.go`, `supervisor/tests/test_custom_images.py`.

**Interfaces:**
- `func Scan(root fs.FS) (Groups, error)`. Groups are `tools`, `flatpak`, `settings`, `dotfiles`, `browser` and `other`, each with `Bytes`, its paths, and `Secrets []string`, which are left out by default. Caches are always left out.
- `GET /api/desktops/{name}/image-scan` gives `{"groups": [...], "flatpaks": [{"id","remote"}], "remotes": [{"name","url"}], "totalBytes"}`.
  - The Flatpak list comes from `flatpak list --user --app --columns=application,origin` through exec, and the remotes from `flatpak remotes --user --columns=name,url`.
  - The folder is read directly, or through `alpine:3.22` when permissions require it.
- `POST /api/desktops/{name}/image` with `{"name","slug","groups":[...],"include":[paths of ticked secrets]}` gives 202 `{"job"}`.
  - It builds a recipe with `Base` = the desktop's image ref, the found Flatpaks and remotes, and `HomeFrom: "image"`.
  - The context's `home-template/` gets the chosen files.
  - `Secrets` is true if any secret is included, or the base has secrets.
- On a rebuild, `HomeFrom: "image"` copies `/opt/deskyard/home-template` out of the current `:latest` (`CopyFromImage`).

- [ ] **Step 1: Write failing tests.**
  - `scan_test.go` (`fstest.MapFS`): grouping; caches excluded; `.config/gh/hosts.yml`, `.ssh/id_ed25519`, `.git-credentials`, `.bash_history`, `.codex/auth.json`, `.npmrc` and `foo/.env` are all secrets. A secret inside a ticked group stays out unless included.
  - Integration, `test_image_from_desktop`:
    - In a desktop: write `~/.config/app/settings.json`, `~/.ssh/id_test`, and `~/.codex/auth.json`, and install mise `npm:cowsay` at user level.
    - Build an image from it, ticking settings and tools only. A new desktop from it has `settings.json` and `cowsay`, but no `.ssh/id_test` and no `auth.json`, and the image isn't marked secret.
    - Build again with `auth.json` included: the image is marked secret, a new desktop has it, and the catalog shows `secrets: true`.
  - `test_secret_flag_passes_down`: a recipe on the secret image is also `secrets: true`.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement** the files above, and the dialog with groups, sizes, partly ticked groups, the secrets list (unticked), the 5 GB warning, and a "Stop it first" button when the desktop is running.
- [ ] **Step 4: Run.** Expect PASS.
- [ ] **Step 5: Commit.** Message: "Images from a desktop: scan, secrets, home template".

### Task 7: "Yours vs the image" on the details page

**Files:**
- Modify: `supervisor/internal/server/desktops.go`: `GET /api/desktops/{name}/tools` runs `mise ls --json` through exec as `user` and gives `[{"tool","version","source":"desktop"|"image"}]`. The source is `image` when the install path is under `/opt/mise`.
- Modify: `supervisor/web/src/pages/details.tsx`: a "Tools" line, for example "node 24.22 (this desktop) · 24.30 in the image" when both exist.
- Test: `supervisor/tests/test_custom_images.py::test_tools_show_overrides`.

- [ ] **Step 1: Write the failing test.** With an image that has `node@24.30` in `/opt/mise`, a desktop runs `mise use -g node@24.22`. The API lists both with their sources, and the page shows both.
- [ ] **Step 2: Run.** Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expect PASS.
- [ ] **Step 5: Commit.** Message: "Details: show which tools the desktop overrides".

### Task 8: Docs and the full run

- [ ] **Step 1: Architecture pages.** In `docs/Architecture/`: `Image builder.md` and `Recipes.md` (with `partOf` and `uses`), and updates to `Image catalog.md` and `Desktop.md`.
- [ ] **Step 2: Other docs.** A "Custom images" section in `README.md`; the spec's `status: implemented`; this plan's `status: done`.
- [ ] **Step 3: Run every suite.** Expect PASS:
  - `go test -race ./...`
  - `go test -tags engine ./...`
  - the image's fast suite
  - `supervisor/tests` in direct and container mode
- [ ] **Step 4: Commit.** Message: "docs: custom images".
