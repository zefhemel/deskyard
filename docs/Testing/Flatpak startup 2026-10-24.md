---
tags: "verification"
result: "passed"
scope: "Deterministic Flatpak openat2 regression and the recorded local image acceptance test."
displayName: "Flatpak startup 2026-10-24"
---
Nightly Tests run [40057546598](https://github.com/zefhemel/deskyard/actions/runs/40057546598) failed in `test_silverbullet_edge_flatpak` with `Extension org.freedesktop.Platform.GL.default has invalid merge-dirs`. This was reproduced with Debian Flatpak `1.16.6-1~deb13u3` and traced to the missing upstream libglnx retry described in [Flatpak issue 6783](https://github.com/flatpak/flatpak/issues/6783).

# Deterministic regression

`images/tests/native/flatpak-openat2.c` links against the exact libglnx static objects built into Flatpak. With the original code it exits 1: `EAGAIN was not retried: fd=-1 errno=11 calls=1`. The backported build exits 0: `Flatpak openat2 retry and confinement checks passed`. It also verifies that `EACCES` is preserved without retry and `RESOLVE_BENEATH` rejects a parent-directory escape. The test runs in the image builder on each target architecture.

# Real application reproduction

In an isolated ARM64 desktop using the production Flatpak security options, install SilverBullet edge and its runtime. Run the parent Flatpak process under `strace -e trace=openat2 -e inject=openat2:error=EAGAIN:when=9` (without `-f`), targeting the GL extension’s `vulkan/icd.d` lookup. The original package exits 1 with the exact nightly `invalid merge-dirs` error. With patched CLI and library packages, the trace shows the injected `EAGAIN`, followed immediately by a successful retry of the same path with unchanged `RESOLVE_NO_MAGICLINKS|RESOLVE_BENEATH` flags. `xdotool search --onlyvisible --name SilverBullet` confirms the application window opens. A normal launch also opens the window. An initial root diagnostic launch polluted the disposable runtime; restarting that desktop restored the expected user launch environment before these successful checks.

# Packaging

The builder uses the signed Debian source repositories and preserves Debian’s security patch stack. The strict backport check must be removed or refreshed when Debian incorporates the fix. The CLI, `libflatpak0` and GIR packages share the local `+deskyard1` version. Complete original source, Debian patch archive and `.dsc` are copied into `/usr/share/doc/flatpak/deskyard-source/`; normal Debian license files remain installed. See [[Architecture/Desktop runtime]] and [[Licensing]].

# Full image acceptance

`docker build --target debian-xfce` passed. The resulting image contains all three packages at `1.16.6-1~deb13u3+deskyard1`, the diverted real CLI, normal copyright files and the complete source bundle. `dpkg-source -x` successfully reconstructs the source and applies the security patch stack plus this backport.

The exact nightly acceptance test, `AC_IMAGE=deskyard-flatpak-fix:test uv run pytest -q -m '' test_flatpak.py::test_silverbullet_edge_flatpak`, passed in 57.70 seconds, including installation, visible launch, container recreation with the same home and another visible launch. The required repository gate, `BUILD_IMAGE=0 TEST_IMAGE=deskyard-flatpak-fix:test DESKYARD_TEST_SUBNET=10.222.0.0/20 WORKERS=2 make test`, exited 0: Go vet/race tests, 40 UI tests, Docker engine tests, 99 parallel plus one serial image test, and 149 parallel plus seven serial supervisor tests passed. Three MATE-only checks were skipped on XFCE; nine Firefox checks were skipped because local Playwright Firefox could not create its profile. CI installs and runs those browsers on Linux. `make build` in `supervisor/` passed for the UI, scanner and binaries.

All changed documentation pages passed SilverBullet lint; the runtime component query retained its built status and expected dependencies. Relative links, wiki links, frontmatter and `git diff --check` passed. A rendered report screenshot was inspected. An independent code review approved the final patch, packaging and regression after correcting the test-only auxiliary syscall forwarding. Native local verification is ARM64; AMD64 runtime and the complete browser matrix are delegated to the fresh GitHub run after push.
