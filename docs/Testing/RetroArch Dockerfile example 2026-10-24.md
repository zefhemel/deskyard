---
displayName: RetroArch Dockerfile example testing — 24 October 2026
date: "2026-10-24"
status: complete
---
The standalone `examples/retroarch/` build packages RetroArch and five source-built cores for a new Deskyard workspace. The ARM64 build from an extracted standalone folder and final offline empty-volume acceptance passed, including all five core ABI checks, menu startup, optional SNES content loading and configuration persistence across container replacement. This record includes the initial build failure and its correction.

This is separate from [[Testing/RetroArch fresh desktop 2026-10-24|RetroArch fresh desktop testing — 24 October 2026]], which installed RetroArch interactively into a fresh user's home and installed Snes9x through the application's Core Downloader. That earlier gameplay result does not prove this Dockerfile's system-wide application, compiled cores or empty-volume initialization work.

# Build inputs and implemented scope

The local ARM64 build uses `BASE_IMAGE=deskyard-debian-xfce:retroarch-fresh-20261024-01`, whose build digest is `sha256:82b644bec8c7336016f5cf040492e7cee54137d1440833f49b8a6980d8d798e1`. The base supplies the desktop runtime, not the previously installed user application or workspace. The example installs RetroArch at system scope, compiles cores from source and populates the existing `/opt/deskyard/home-template` mechanism. No live home folder is copied into the image.

The five repositories are Snes9x, FCEUmm, Genesis Plus GX, Gambatte and Beetle PCE Fast. Their full commit pins, archive SHA-256 checksums, makefile paths and output names are implemented in `examples/retroarch/cores.tsv`. All five downloaded archives matched those checksums. Native `platform=unix` makefile dry runs resolved their source dependencies successfully; this is source/build-path validation rather than proof that the resulting shared libraries load in Flatpak. The build uses Debian trixie and two compiler jobs and retains the verified source archives and their licenses in the final image.

The default Dockerfile instead resolves the published XFCE `latest` base. Flathub's stable RetroArch application and runtime, Debian builder tag and compiler packages are also moving inputs. The image is not bit-for-bit reproducible. Installed Flatpak refs/commits are recorded at `/usr/share/doc/deskyard-retroarch/flatpak-commits.txt`; the local base override fixes only one input for this run.

The original trial folder included build/test support files and a copied Flatpak seccomp profile with its upstream Apache license and modification notice. The later Docker-from-desktop validation moved all test-only files to `images/manual/retroarch/`; the distributed example and resulting image now contain only build/runtime files and notices. The manual ABI probe is supplied separately to the check rather than shipped in the desktop image. It adds no supervisor API or UI feature. This run preceded removal of supervisor image authoring later on 24 October. The standalone Dockerfile and runtime home-template mechanism remain supported; the recipe and UI build workflows are removed. See [[Architecture/Recipes]] and [[Licensing]] for the mechanism and optional non-commercial core restrictions.

# Regression baseline

Before building the example, the acceptance test was run against the unextended base image and failed at the expected missing system RetroArch application check. This establishes that the test distinguishes the base from the intended packaged image. It does not imply a defect in the base, which does not promise to bundle RetroArch.

# First build failure and correction

The first build completed its Flatpak downloads/installation but failed after approximately 1,178 seconds in the reporting command: `flatpak list --columns=ref,commit` rejected the unsupported `commit` column. Namespace warnings from Flatpak triggers were also present, but the reported fatal error was `Unknown column: commit`. This was a build-script error, not a passing image build or an application runtime failure.

Inspecting `flatpak list --help` identified the supported `active` column. Even `active:full` returned a shortened 12-character identifier in the installed Flatpak version, so the corrected inventory enumerates refs and calls `flatpak info --system --show-commit` for each. That command was checked against all five installed application/runtime/extension refs on the existing desktop and returned full commit IDs; this direct command check does not substitute for testing the newly built image.

The installation script now checks its list-column support before downloading, and the Dockerfile records the full commit inventory in a separate layer after installation. The test asserts that every recorded commit is 64 hexadecimal characters. Separating reporting from the expensive install means a future reporting-only failure can reuse a completed installation layer. The failed original RUN did not retain that successful install as a reusable completed layer, so correcting the script required repeating the downloads.

The corrected build was subsequently repeated from an extracted standalone copy of the example folder outside the repository. The general lesson is to validate cheap tool-format assumptions before a long installation and keep reporting in a separate cacheable step.

# Early compiled-core ABI preflight

An independent preflight extracted the five compiled cores and `verify-cores` executable from the completed Docker cores stage, tagged `deskyard-retroarch-cores:example`. The Deskyard CLI copied them only to temporary `/tmp` paths on the existing `retroarch-fresh` desktop, ran the probe inside that desktop's existing RetroArch Flatpak runtime, then removed the temporary files. The existing application GUI was left unchanged.

All five shared libraries loaded with `RTLD_NOW`, reported Libretro API version 1 and returned complete system information:

| Core | Reported version |
| --- | --- |
| FCEUmm | `(SVN)` |
| Gambatte | `v0.5.0-netlink` |
| Genesis Plus GX | `v1.7.4` |
| Beetle PCE Fast | `v1.31.0.0` |
| Snes9x | `1.63` |

The [saved preflight output](RetroArch%20Dockerfile%20example%202026-10-24/core-preflight.txt) includes a document-portal warning followed by all five successful library reports. This establishes early compatibility of the compiled artifacts with the existing desktop's Flatpak runtime. It does not establish successful final-image assembly, initialization of an empty volume, compatibility with a separately resolved runtime in the new image, menu startup or game loading.

# Offline acceptance scope

The original `examples/retroarch/test-image.py` (now `images/manual/retroarch/test-image.py`, outside the distributable example) creates a dedicated new Docker volume with `volume-nocopy`, verifies it is empty before boot, and runs the image with `--network=none`. It checks system-wide RetroArch installation, absence of a user-installed application, seeded launcher/configuration/core ownership and absence of inherited `Games` or `Downloads` directories. Its C ABI probe loads all five cores from inside RetroArch's Flatpak runtime, rather than relying on the host loader. It then opens the menu and saves a screenshot, removes/recreates the container with the same volume and checks that a user's configuration edit remains.

An optional `--rom` argument copies an existing local Super Mario World fixture into the temporary test workspace only after the empty-home checks. This fixture is not included in the build context or distributed image. The optional check waits for video initialization and records a game screenshot/log; it does not independently establish interactive gameplay or input quality.

The test removes its temporary container and volume. It uses the copied profile and the same per-container namespace/security options needed by Deskyard's Flatpak support.

# Final build and acceptance evidence

The standalone-folder ARM64 build produced `deskyard-retroarch:example`, image ID `sha256:923bcba281283a52079a5a73c2dad51ff8c0746d3c0a80a0316ffda283507e92`, with an engine-reported size of 6,313,755,248 bytes. This is the packaged image, distinct from both the base image and the earlier interactive desktop.

The final test process exited 0, and cleanup left its temporary container and volume absent. Its [acceptance output](RetroArch%20Dockerfile%20example%202026-10-24/acceptance.json) reports an empty initial workspace, offline execution, all five loaded cores, retained user configuration after container removal/recreation and successful game loading. The test copied a local SNES fixture only after its empty-home checks; that game is absent from the image and distributable source folder. The fixture had mode 0600, and the game-loading check exercised its use within the temporary test workspace.

The [core reports](RetroArch%20Dockerfile%20example%202026-10-24/cores.txt) contain all five expected cores loaded inside the packaged application's Flatpak runtime. The [Flatpak inventory](RetroArch%20Dockerfile%20example%202026-10-24/flatpak-commits.txt) records six installed refs with full 64-character commit IDs: RetroArch stable/aarch64, KDE Platform and Adwaita 6.11, and the Freedesktop 25.08 GL/GL-extra/codecs-extra extensions.

The final menu screenshot shows RetroArch 1.22.2 with its complete Ozone menu. The game screenshot shows Super Mario World's title screen, corroborating the [video initialization log](RetroArch%20Dockerfile%20example%202026-10-24/game.log). This is content-loading proof, not interactive gameplay acceptance.

![RetroArch menu from the final standalone image test](RetroArch%20Dockerfile%20example%202026-10-24/menu.png)

![Super Mario World title from the optional local-fixture check](RetroArch%20Dockerfile%20example%202026-10-24/game.png)

Four existing image regressions also passed in 28.50 seconds: user/home boot, one-time skeleton seeding and persistence, Flatpak/bubblewrap operation, and absence of sudo privilege access. They supplement the example's dedicated test rather than replacing the five-core/offline checks.

The distributable archive was inspected and contains 14 readable support files, including hidden template paths and `.dockerignore`, with no ROM archive or game binary. The Dockerfile's allow-listed build context excludes test outputs and desktop state. Source/license and non-commercial restrictions remain as documented in `examples/retroarch/NOTICES.md`.

# Limits and product scope

amd64 runtime behavior, sustained performance, audio and native mobile input remain outside the current acceptance scope. The optional SNES check verifies loading and video initialization; movement/gameplay evidence in the earlier interactive trial belongs to that separate environment. Mutable base, package and Flathub inputs mean a rebuild still requires its own acceptance check.

This change supplies a standalone Dockerfile example and support files. It does not implement a Dockerfile editor, auxiliary-file editing, image folders, or a Save/Build UI workflow. Discussion of that product direction is separate from this completed artifact's implementation and runtime evidence.
