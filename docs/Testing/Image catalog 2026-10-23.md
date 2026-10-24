---
displayName: Image catalog testing — 23 October 2026
date: "2026-10-23"
---
The approved discovery follow-up to [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]] adds official choices before download, an Install action and a first-use hint in the desktop picker. Saved entries/defaults and custom images remain intact.

# Verification

The discovery tests cover browsing without downloads or saved-state changes, normalized-reference deduplication, update checks skipping missing images, successful on-demand installation, failed downloads staying available, administrator/known-reference/custom-image restrictions, and local-version recording without changing the default or check timestamp. Missing discovery/install behavior failed before implementation; local-version recording also failed before its fix.

The real UI was exercised through the existing Vite development API fixture: all three statuses rendered, clicking KDE Install changed its row from Available through Installing to Installed, and an undownloaded MATE choice could be selected in New desktop with the first-use hint. These screenshots use fixture data; they do not represent a registry download or a published release. Backend download behavior is covered separately by the engine-backed API tests.

Before download:

![Available official images](Image%20catalog%202026-10-23/available.png)

After clicking KDE Install:

![KDE installed, MATE available](Image%20catalog%202026-10-23/installed.png)

First-use selection:

![MATE selected before download](Image%20catalog%202026-10-23/picker.png)

The image pipeline configuration builds all three variants for amd64/arm64 and publishes from main. Registry publication and amd64 runtime execution have not been performed in this local task. SilverBullet's registered space points to the original checkout, so worktree documentation is checked statically rather than indexed/rendered there.

Final validation: `make -C supervisor test`, `make -C supervisor typecheck` (including 16 UI tests), and `make -C supervisor build` all passed. The seven focused catalog tests passed, and an independent code review found no remaining actionable findings. `git diff --check` passed; local Markdown/wiki links resolved across all 25 changed/new documentation pages (excluding the API’s literal `[[…]]` syntax example).

# Landing verification

Reconciled current main's parallel test harness and release changes with the shared image layout. Root Make targets and the integration workflow now use `images/Dockerfile` and `images/tests`; image-only tests accept `AC_DESKTOP`, while the supervisor suite explicitly requires XFCE because it contains XFCE terminal/panel assertions. Generated artifacts remain ignored. Actionlint and Bake's three-target graph checks passed.

The full root `make test` passed: Go vet/race and engine tests, 16 UI tests, 92 image tests plus one serial restoration test, and 113 supervisor integration tests plus five serial tests. Three MATE-only checks were skipped in the XFCE run; eight Firefox checks were skipped because Playwright Firefox cannot start on this host. The separate MATE identity/appearance/browser/restoration selection passed all eight checks. KDE passed all five identity/browser/restoration checks when run in isolation. An earlier KDE serial test was incorrectly run alongside the supervisor suite and observed extra restored windows; isolated unchanged-runtime reproductions and the final isolated selection passed, so no speculative runtime delay or weakened assertion was introduced.
