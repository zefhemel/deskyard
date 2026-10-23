---
displayName: Desktop variants testing — 23 October 2026
date: "2026-10-23"
---
Local validation of [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]] on native ARM64 Docker (OrbStack, 15.66 GiB VM memory). All three final targets build together with `VERSION=test docker buildx bake --load`. Expensive shared dependency stages are reused; changing common startup scripts retains desktop package caches. amd64 runtime checks and registry publication were not performed locally.

# Graphical applications

KDE Discover and GNOME Software both displayed the Flathub catalog and LibreOffice. Installing LibreOffice through Software required no administrator password; `flatpak list --user` contained the app and the system installation remained empty. Recreating the container as MATE with the same home preserved the installation. Writer launched, accepted text and saved `family-note.odt` in `/workspace` through its graphical file dialog. A stale LibreOffice hostname lock required confirmation after switching the test container; arbitrary Flatpak window restoration is not promised.

![LibreOffice in KDE Discover](Desktop%20variants%202026-10-23/kde-discover.png)

![A document saved in MATE Writer](Desktop%20variants%202026-10-23/mate-writer.png)

Initial catalog downloads can take minutes. GNOME Software requires explicit `iso-codes` and `librsvg2-common` with the curated package set for language data and application icons. Both are included. Debian Discover's required PackageKit daemon/backend and optional desktop host-management D-Bus helpers are disabled; tests check the process/privilege boundary.

The document portal cannot mount its FUSE filesystem because the engine does not expose `/dev/fuse`. LibreOffice's permitted workspace access worked, but this does not establish compatibility with apps requiring document-portal grants. No engine privileges were expanded.

# Footprints

These samples predate the selected MATE appearance update (Arc/Papirus/Noto). They remain historical measurements, not a fresh footprint or image-size measurement of the themed MATE build.

Each measurement used a fresh home at 1280×800. After desktop startup, allow 20 seconds to settle and take three `docker stats --no-stream` samples separated by two seconds. Connect one Selkies viewer, settle 15 seconds and sample again; then open shared Chromium, settle 15 seconds and repeat. Values below are medians. Other local workloads were present; these short samples are observations rather than capacity budgets or CPU rankings. Image sizes are Docker's uncompressed per-image sizes; shared layers are counted in each image, and registry downloads differ.

| Desktop | Image GiB | No viewer MiB | Viewer MiB | Viewer + Chromium MiB | CPU %, no viewer / viewer / browser |
| --- | ---: | ---: | ---: | ---: | --- |
| XFCE | 2.43 | 213 | 275 | 540 | 0.31 / 12.46 / 8.21 |
| MATE | 2.82 | 258 | 299 | 520 | 0.17 / 7.05 / 14.39 |
| KDE Plasma | 3.83 | 522 | 579 | 793 | 0.39 / 4.57 / 5.90 |

Docker CPU percentages use 100% for one logical CPU. Short activity spikes and background startup/network work make the connected CPU samples unsuitable for ranking the environments. The extra KDE memory is visible even without a viewer; MATE's idle baseline is close to XFCE. Raw samples: [footprints.json](Desktop%20variants%202026-10-23/footprints.json).

# Verification

Selected native runs are recorded below; counts overlap and final corrections received targeted reruns rather than repeating every suite. The all-file regression runs excluded marked network/slow tests; selected store/custom-image runs included their network checks. Manual LibreOffice GUI installation supplies application-install evidence independently of the optional SilverBullet/agent download tests.

| Desktop | Verified runs |
| --- | --- |
| XFCE | Identity/layout: 15 passed. Browser/native restoration: 14 passed. Final profile-scoped shutdown and native restoration: 4 passed. Streaming, viewer/resize and default-browser checks: 28 passed. |
| KDE | Boot, identity, privileges, store, streaming and custom-image checks: 54 passed, 1 deselected. Browser/native restoration: 14 passed. Viewer/resize/persistence recheck: 10 passed. Final identity, default browser and terminal restoration: 4 passed. Profile-scoped shutdown also passed in its targeted run. |
| MATE | Streaming, stores, custom-image compatibility, viewer/resize, persistence and profile-scoped shutdown: 50 passed, 1 deselected. Browser/native restoration: 14 passed. Default browser: 1 passed. Earlier core runs also exercised boot, token/bubblewrap and root-process restrictions. |

The default-browser test opens a local HTML file with `xdg-open` and checks the shared Chromium target, rather than merely checking MIME configuration. KDE initially selected an obsolete helper for exec-launched commands; exporting its version in the image fixed that test. Native terminal restart tests verify each terminal's working directory. Chromium restoration tests cover open tabs, deliberate closure, on-demand reopening and container recreation.

One initial KDE streamed-render assertion captured a black frame during concurrent integration runs. A fresh rerun passed rendering, resize and persistence; the first failure's cause was not established. Keep this observation visible rather than claiming that all initial runs passed.

BuildKit's checks for all three targets with both configured platforms reported no warnings; these checks do not execute amd64 runtime behavior. Actionlint, shell syntax, Python compilation, Bake target/tag checks and `git diff --check` passed. Documentation links resolved, SilverBullet indexed the spec/component metadata, and the validation page rendered with its screenshot. Code review reported no remaining substantive findings after the scoped shutdown fix. Go/UI suites were not rerun for path/comment-only supervisor changes. Optional agent/Flatpak download suites, the three-instance capacity test and registry publishing were not run as a complete release matrix.
