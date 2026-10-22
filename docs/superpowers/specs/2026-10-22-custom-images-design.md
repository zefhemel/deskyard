---
displayName: Custom images design
tags: spec
status: implemented
plan: "[[superpowers/plans/2026-10-22-custom-images|Custom images plan]]"
date: "2026-10-22"
---
Admins can make their own desktop images in the supervisor's UI, in two ways:
- **Recipe:** a base image plus mise tools, Flatpak apps (from Flathub or any other remote) and optional build commands.
- **From a desktop:** a copy of what someone installed and configured in an existing desktop's home folder, baked in as the starting home for new desktops.

Both build a new layer on a base image with Docker on the supervisor's machine. The result joins the image catalog like any other image. Builds on the [[superpowers/specs/2026-10-21-supervisor-design|Supervisor design]] (image catalog, contract, upgrades).

# Intent

People keep setting up the same things by hand in every new desktop: Codex, SilverBullet, a pinned Node, their dotfiles, their app settings, sometimes their logins. The desktop image is fixed, and today the only way to change it is to edit its Dockerfile.

A custom image makes "a desktop with my tools, ready" one click. Two starting points fit how people actually work:
- **Declarative:** "this base, plus these tools", easy to change and rebuild.
- **By example:** "set up a desktop the way you like, then make an image of it".

# Goals

- **Recipe images.** Without leaving the UI, an admin creates an image from:
  - a base (any catalog image, including other custom ones);
  - mise tools;
  - Flatpak apps, from Flathub by ID, from another remote given by its `.flatpakrepo` URL (or repository URL and GPG key), or from a `.flatpakref` URL;
  - optional build commands run as root, so `apt-get install` works.
- **Desktop images.** An admin makes an image from an existing desktop. They choose which parts of its home folder go in:
  - installed tools;
  - Flatpak apps and the remotes they came from;
  - app settings;
  - dotfiles;
  - browser profile;
  - other files.

  That home becomes the starting home for new desktops made from the image.
- **Secrets are left out by default.** The builder recognises the usual places secrets live and leaves them unticked. Including any is a deliberate act, and it marks the image as "contains secrets" everywhere it appears.
- **Layering.** Every custom image is one or a few layers on its base, so bases are shared on disk.
- **Base updates.** When the base updates, the custom image says so and can be rebuilt. Its desktops then get the usual "update available" and upgrade.
- **Desktops stay free to change.** On a custom image, a desktop can still install, upgrade and override anything in its home folder:
  - mise tools and `npm -g` packages;
  - per-user Flatpak apps, including a newer per-user copy of an app baked into the image, which then runs instead;
  - any file from the starting home.

  Only what the image installs system-wide (baked Flatpak apps, build-command packages) needs an image rebuild to change.

# Non-goals

- Pushing custom images to a registry, or sharing them between supervisors.
- Editing Dockerfiles directly. The build commands box is the escape hatch.
- Snapshotting a desktop's whole container. Desktops have no root, so everything a person changes is in the home folder, and that's what "from a desktop" copies.
- Automatic rebuilds. Rebuilds run the admin's commands again, so the admin chooses when.
- Changing existing desktops' home folders. A starting home only fills a new, empty folder.

# Anti-goals

These would make it a failure even if it worked:

- A secret baked into an image without the admin having ticked it, or an image with secrets that doesn't say so where people choose images or give access.
- A custom image whose desktops give `user` a way to root: sudo, setuid binaries, writable root-owned paths that root runs. The supervisor design's root rule holds in custom images too.
- An image upgrade or rebuild that overwrites anything in an existing desktop's folder.
- A build that blocks the supervisor or leaves half-built images in the catalog.
- Build commands running anywhere but in an isolated Docker build.

# Decided

## Recipes

- **Images page.** The Images page gets **New image**. A recipe has:
  - a **name** (shown as the image title);
  - a **base**, chosen from the catalog;
  - **mise tools**, one per line in mise syntax (`node@24`, `npm:@openai/codex`, `pipx:…`);
  - **Flatpak remotes** besides Flathub, each a name plus a `.flatpakrepo` URL (or a repository URL plus a GPG key);
  - **Flatpak apps**: an app ID with its remote (Flathub by default, with search), or a `.flatpakref` URL;
  - **build commands**, an optional box with one shell command per line. They run as root during the build, after the tools and apps.
- **Where things are installed.**
  - mise tools go into the image, where the base puts Node and Python today (`/opt/mise`, on `PATH` after the desktop's own mise shims).
  - Flatpak apps and remotes are installed system-wide in the image (`/var/lib/flatpak`), calling `flatpak.real --system`, past the wrapper.
  - Each extra remote's `.flatpakrepo` is also saved in `/etc/deskyard/flatpak-remotes/`. `workspace-init` adds those at user scope on every start, the way it adds Flathub, so a desktop can `flatpak install` and update from them. *(My call.)*
  - A desktop's own installs go into its home folder and come first, so they override the image's. The image's `flatpak` wrapper already sends installs and updates to the per-user installation.
- **Build.** Build runs a Docker build on this machine from a Dockerfile the supervisor generates: `FROM <base image ID>`, then the steps above. The build log streams into the Build dialog. Builds are jobs, like pulls.
  - **If Flatpak can't install inside a plain build:** the Flatpak step instead runs in a temporary container with the desktop's security options, which is then committed as a layer. The builder checks which works. *(My call.)*
  - **Limits.** Builds can be cancelled from the dialog, and time out after 30 minutes. They have network access (they download packages) and Docker's default isolation: no Docker socket, no supervisor network, no host mounts. *(My call.)*
- **Result.** A successful build is tagged `deskyard-custom/<slug>:<YYYYMMDD-HHMMSS>` and `deskyard-custom/<slug>:latest`. The catalog entry's ref is the `:latest` one, so desktops store it, and "update available" and upgrades work as for pulled images. *(My call.)*
  - Rebuilds move `:latest`. Older timestamp tags are deleted once no desktop's container uses their image.
  - The daily update check skips custom entries: there is nothing to pull.
- **Labels.** The image carries:
  - `deskyard.contract` (inherited);
  - `org.opencontainers.image.title` (the recipe name) and `org.opencontainers.image.version` (the build time);
  - `deskyard.base` (the base's image ID);
  - `deskyard.recipe` (the recipe as JSON, so "Edit and rebuild" starts from it);
  - `deskyard.secrets=true` when it contains secrets.
- **Storage.** The supervisor keeps recipes in `.deskyard/recipes.json`. Catalog entries for custom images carry `"custom": true`.
- **Visible to anyone who can inspect the image:** build commands and remote URLs are saved in the `deskyard.recipe` label. The recipe form says so under the build commands box. *(My call.)*
- **Rebuild, any time.** Every custom image has a **Rebuild** button, on the Images page and in "Edit and rebuild".
  - A rebuild first pulls the base again (unless the base is itself a custom image), then runs the recipe from scratch with no build cache. So unpinned tools (`npm:@openai/codex`), Flatpak apps and `apt` packages come out at their newest versions.
  - Its desktops then get "update available" as usual.
- **Changing the base.** "Edit and rebuild" lets the admin pick a different base, for example another distribution's image or another custom image. The rest of the recipe is kept.
  - A base without the Deskyard contract is refused, as when adding an image.
  - A base with the wrong contract version is also refused.
- **Base updated.** When the base's catalog entry has a newer image than the custom image's `deskyard.base`, the custom image shows a "Base updated" badge next to its Rebuild button. This applies down a chain: rebuilding a custom image marks the custom images built on it.

## From a desktop

- **Entry point.** A desktop's details page (admin) gets **New image from this desktop**. The supervisor scans the desktop's home folder and groups what it finds, with sizes:
  - **Installed tools:** `~/.local/share/mise`, `~/.local/bin`, `~/.npm-global`.
  - **Flatpak apps and remotes:** read from the desktop's per-user installation (`flatpak list --user`, `flatpak remotes --user`, through exec). They're installed again system-wide in the image, like a recipe's apps, rather than copying the OSTree repository. *(My call.)*
  - **App settings:** `~/.config`, except what's listed as a secret below.
  - **Dotfiles:** top-level dotfiles.
  - **Browser profile:** the shared Chromium's profile: extensions, bookmarks, settings.
  - **Other files.**
  - **Always left out:** caches (`~/.cache`, package caches) and Deskyard's own runtime state.
- **Secrets.** Within those groups, the dialog lists what looks like a secret separately, each unticked:
  - browser cookies and logins;
  - `~/.ssh`;
  - git credentials;
  - shell history;
  - known tool logins (Codex's and Claude's auth files, `gh`, cloud CLIs);
  - `.env` files;
  - anything named like a token or key;
  - known credential files: `~/.npmrc`, `~/.netrc`, `~/.git-credentials`, `~/.config/gh/hosts.yml`, cloud CLI config folders.

  Detection goes by path and name only; file contents aren't scanned. *(My call.)* A group that contains secrets shows as partly ticked, with the secrets carved out. Ticking any secret makes the image "contains secrets".
- **Running desktops.** Scanning and copying work on a running desktop. The dialog suggests stopping it first for a consistent copy, with a Stop button, and shows the total size. It warns above 5 GB, but sets no hard limit. *(My call.)*
- **Reading the folder.** The supervisor reads and copies the folder directly where it can, and through an `alpine:3.22` helper container where file permissions require it, as it does for deleting folders.
- **Where the home goes.** The chosen files go into the image as a starting home at `/opt/deskyard/home-template`, owned by UID 1000. The base is the desktop's own image.
- **First start.** `workspace-init` copies the template inside its existing one-time block (the `.workspace-seeded` marker).
  - The copy runs as `user` (`runuser -u user -- cp -an`) and never overwrites an existing file. So a reused folder keeps its own files and gains only what's missing.
  - The template is copied before the image's own skeleton, so the template wins where both have a file.
  - Nothing root runs is ever taken from `/workspace`, as today. *(My call.)*
- **Chains.** An image made from a desktop that runs on a custom image keeps the parent's template underneath: the parent's layer stays, and the new files overlay it. The "contains secrets" flag passes down a chain: an image built on a secret image contains secrets too. *(My call.)*
- **It's a recipe too.** An image made from a desktop is saved as a recipe whose parts are:
  - the desktop's base image;
  - the Flatpak apps and remotes it found;
  - a home template.

  So it can be rebuilt, or moved to another base, like any recipe. On a rebuild, the template comes from the current image's `/opt/deskyard/home-template`, which is copied out of that image; no separate copy is kept. *(My call.)*
- **Save as recipe.** The dialog can also save the result as a recipe whose base is the new image, so tools can be added on top later without starting over.

## Images with secrets

- **Lock badge.** The catalog, the New desktop dialog and a desktop's image line show a lock badge, with "Desktops made from this start signed in as whoever made it".
- **Who can use them.** Only admins can create desktops, so only admins pick these images. Giving another account access to a desktop on such an image asks for confirmation first, naming the image.
- **Never leaves the machine.** These images are never pushed anywhere. There is no push anyway, and this stays true if pushing is added later.

## Checks on every custom image

- **Contract.** It must still carry the contract label (contract 2 once the audio work lands). The supervisor refuses a build that drops it.
- **Root check.** The supervisor runs a scan in a throwaway container of the new image and of its base, and compares the two. The new image must add none of these:
  - a sudo or doas binary;
  - a setuid or setgid file;
  - a file that `user` can write in a place root runs from: `/usr`, `/etc`, `/opt/workspace`, `/opt/deskyard` except the template, `/usr/local/bin`, and supervisord's config.

  Build commands could add any of these; the build is then rejected with the offending paths. *(My call.)*
- **Smoke test.** After building, the supervisor starts a throwaway desktop from the new image and waits for it to be ready (as `WaitReady` does), then removes it. *(My call.)*
- **Failed builds** leave no tag and no catalog entry. The log stays in the job.

## Seeing what's yours

A desktop's details page shows where its mise tools come from, so overrides are visible. For example "node 24.22 (this desktop) · 24.30 in the image". It reads `mise ls` through exec.

## Who

Everything here (recipes, builds, rebuilds, images from desktops) is admin-only, like the rest of the Images page.

# Left to the builder

- **Generating the Dockerfile and running the build:** through the engine interface (`ImageBuild` in the Docker implementation, prepared for Podman), with the build context assembled by the supervisor.
- **Copying a desktop's home folder into the build context:** directly from the data folder in direct mode, or through a helper container where permissions require it. Watch the size.
- **The exact secret-detection list.** Keep it in one file with tests.
- **Removing custom images:** the same as removing catalog images today (refused while desktops use them), plus deleting the recipe.
- **API shape:** under `/api/images`, documented in `supervisor/API.md`.
- **Tests:**
  - a recipe with a mise tool, a Flathub app, an app from another remote via `.flatpakref`, and an `apt-get install` command builds and works in a desktop;
  - an image from a desktop starts new desktops with the chosen files and without unticked secrets;
  - a build command that adds sudo or a setuid binary is rejected;
  - an existing desktop upgraded to a rebuilt image keeps its own files and its own tool versions.
