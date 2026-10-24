Review of commit `4a89e30` and a separate, user-requested removal of KDE support. The review did not modify the audited job-state or mount-validation implementation. KDE removal is in the working tree and has not been committed or pushed. `HEAD` and `origin/main` both remain `4a89e30`.

# Findings

- **P2 — One job should have one state owner.** `supervisor/web/src/pages/details/settings-tab.tsx:36` selects between the local `useJob` state and `LiveProvider.jobs`. After reload or tab remount only the shared tracker owns the job; that tracker deletes terminal states, while the form renders errors only from its new local hook. A resumed failure therefore disappears without an error. A temporary browser regression reused the committed reload/tab-switch scenario and delivered a failed terminal update; it failed because the error text was absent. Move job subscription, terminal state and completion/error handling into one shared job registry keyed by job ID; let local forms retain only the pending start request. Add permanent coverage for resumed failure, transport loss and successful completion after remount.

- **P2 — Reuse one daemon-host mount preflight.** `supervisor/internal/desktops/docker_access.go:21` duplicates `checkHostMountSources` in `host_mounts.go`: helper preparation, timeout, disposable container specification, start, root exec and cancellation-independent cleanup. Direct and VPN create/recreate call both sequentially. One preflight accepting the requested folder/socket checks can perform both checks in one container before stopping a desktop; keep the folder/socket validation rules explicit and retain their distinct error messages. This removes a duplicated resource lifecycle and prevents cleanup/timeout policy drifting between two implementations.

# Committed versus local files

- The landed commit contains 147 changed paths, including all ten RetroArch example files, all six Docker example files, and the six separate manual RetroArch test/support files. Examples have no external repository context requirement. No ROM files, node_modules or Python caches are tracked. No generated supervisor binaries or web output were added by that commit.
- Ten files under `images/debian-xfce/tests/artifacts/` are already tracked despite the artifact ignore rule. Four screenshots were modified before this review. These are routine generated results, distinct from deliberately selected evidence under `docs/Testing/`. Recommended cleanup: remove routine artifacts from the index in a separate scoped change; preserve curated documentation evidence.
- Before this turn there were 14 modified tracked paths and seven untracked files. They include the Selkies input-recovery patch/native regression, its test/workflow wiring and docs, suspend exploration, MATE testing notes and two Playwright snapshots. These remain local; local verification involving them cannot establish what a fresh clone of the pushed commit contains.
- The new KDE removal deliberately edits shared Dockerfile/workflow/docs files that also contain preexisting uncommitted changes. Those preexisting edits were preserved. Nothing was staged, committed or pushed during this review.

# Coverage and verification

| Check | Result |
| --- | --- |
| Committed-tree desktops package | 71.6% statement coverage |
| Committed-tree server package | 70.3% statement coverage |
| Docker socket preflight | 77.3% statements; preparation/create/start/cleanup error paths not fully covered |
| WebSocket setup / send / heartbeat | 91.7% / 62.5% / 0.0% statements |
| Catalog add / install / resolve / remove | 60.0% / 86.7% / 100.0% / 50.0% statements |
| Final KDE-removal tree: `make test typecheck build` in supervisor | Passed Go race suite, TypeScript, 60 UI unit tests and production builds |
| Mock browser catalog/Settings suite | 14 passed |
| Retired KDE catalog regression | Observed failing before removal; passes in final suite |
| Temporary resumed-job failure regression | Failed as expected: no error shown |
| Bake graph | Exactly debian-xfce and debian-mate; no builds performed |
| Python AST, finalizer shell syntax, diff whitespace | Passed |

Coverage is Go statement coverage from the two affected packages, not branch coverage, whole-repository coverage or a UI coverage percentage. The committed browser tests cover resumed success, but not resumed failure; the temporary failing regression stays outside the repository at `/tmp/test_deskyard_review_failure.py`. WebSocket heartbeat success/failure, stalled writes and socket-probe lifecycle errors need targeted automated coverage. The four-viewer regression exists in the real-engine suite but was not rerun here. RetroArch build/gameplay validation remains manual and outside default E2E. No desktop images were rebuilt or full shared-engine E2E run during this review.

# KDE removal

Removed the Dockerfile stage, Bake target, publish/cache overrides, CI matrix member, official catalog entry, mock choice, KDE rootfs and native terminal/session branches. XFCE remains the default; MATE remains supported. Existing saved KDE references are retained as ordinary prebuilt references. Updated README, API, AGENTS overlay paths, runtime/session/catalog/terminal/application architecture, licensing context and the variants spec/plan supersession notes. Historical KDE screenshots and testing reports remain historical evidence.

# Working-tree inventory at review completion

This inventory includes both preexisting work and the KDE removal. It is not a proposed staging list.

```text
 M .github/workflows/images.yml
 M .github/workflows/tests.yml
 M AGENTS.md
 M README.md
 M docker-bake.hcl
 M docs/AGENTS.md
 M docs/Architecture/Application bridge.md
 M docs/Architecture/Desktop runtime.md
 M docs/Architecture/Desktop.md
 M docs/Architecture/Image catalog.md
 M docs/Architecture/Lifecycle.md
 M docs/Architecture/Selkies.md
 M docs/Architecture/Session.md
 M docs/Architecture/Shared terminal.md
 M docs/Licensing.md
 M docs/index.md
 M docs/superpowers/plans/2026-10-22-selkies.md
 M docs/superpowers/plans/2026-10-23-desktop-variants.md
 M docs/superpowers/specs/2026-10-22-selkies-design.md
 M docs/superpowers/specs/2026-10-23-desktop-variants-design.md
 M images/Dockerfile
 M images/common/finalize.sh
 M images/common/patches/README.md
 M images/common/rootfs/opt/workspace/skills/workspace-computer/SKILL.md
 M images/common/rootfs/usr/local/bin/deskyard-terminal-configure
 D images/debian-kde/rootfs/etc/workspace/desktop-session.sh
 D images/debian-kde/rootfs/etc/xdg/kscreenlockerrc
 D images/debian-kde/rootfs/etc/xdg/kwinrc
 D images/debian-kde/rootfs/etc/xdg/startkderc
 M images/debian-xfce/tests/artifacts/test_cdp_page_on_shared_display/chromium.png
 M images/debian-xfce/tests/artifacts/test_click_and_type_roundtrip/roundtrip.png
 M images/debian-xfce/tests/artifacts/test_terminals_restored_with_their_folders/after-restart.png
 M images/debian-xfce/tests/artifacts/test_top_panel_has_switcher_and_clock/top-panel.png
 M images/tests/conftest.py
 M images/tests/sandbox.py
 M images/tests/test_flatpak.py
 M images/tests/test_restore.py
 M images/tests/test_selkies.py
 M images/tests/test_terminal.py
 M supervisor/API.md
 M supervisor/internal/server/official_images.go
 M supervisor/internal/server/official_images_test.go
 M supervisor/tests/test_ui_images.py
 M supervisor/web/src/dev/mock-data.ts
?? .playwright-mcp/page-2026-10-22T12-02-20-086Z.yml
?? .playwright-mcp/page-2026-10-22T12-02-25-026Z.yml
?? docs/Testing/MATE stream startup 2026-10-24.md
?? docs/superpowers/spikes/2026-10-23-desktop-suspend.md
?? docs/superpowers/spikes/2026-10-23-desktop-suspend/baseline-probe.py
?? images/common/patches/selkies-x-input-recovery.diff
?? images/tests/native/selkies-input-recovery.py
```

# Remediation — 24 October 2026

The user subsequently requested fixes. Both P2 findings above are resolved in the working tree: one shared job registry retains terminal errors and owns deduplicated subscriptions across forms; one engine-host preflight container validates folders and the Docker socket before direct/VPN create/recreate. Permanent mobile/desktop browser cases cover resumed success, reported failure and dropped transport; tracker tests cover duplicate followers, terminal replay, stale completions and cleanup. Preflight tests cover combined probes, actual directory/socket types, engine failure, cancellation and independent cleanup deadlines. WebSocket heartbeat and failed-write cases now have real transport coverage. See [[Testing/CI transport regressions 2026-10-24|CI transport regressions — 24 October 2026]] for the separate GitHub failure causes and fixes.

Generated image-test artifacts are removed from version control while preserving the local files and existing ignore rule. Curated documentation evidence remains tracked. The inventory and coverage percentages above describe the initial review, before remediation; final verification is recorded below once complete.

Final local verification passed: the full untagged Go race suite, TypeScript and 64 UI unit tests, production supervisor/CLI/UI builds, 24 mock browser/transport cases (plus eight policy subtests), and six additional delayed-first-event browser cases after the independent reviewer caught an insertion-publication gap. The new regression was observed failing before correction. No shared-engine production fixture or heavyweight RetroArch build was run. GitHub Linux native/container reruns provide the remaining original-failure verification.
