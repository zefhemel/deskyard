---
displayName: Docker desktop access testing — 24 October 2026
status: complete
date: "2026-10-24"
---
Implemented the administrator **Docker daemon access** checkbox for desktop creation and Settings. Image authoring has been removed from the supervisor; standalone Dockerfiles remain a manual way to build images that can be added to the catalog. See [[Architecture/Lifecycle]], [[Architecture/Desktop runtime]] and [[Architecture/Engine adapter]].

# What was tested

The standalone `examples/docker/` folder contains a Dockerfile, allow-list `.dockerignore`, README and three small demo files. Its Dockerfile installs Debian `docker-cli` and `docker-buildx`, without a daemon, and seeds the example into a new home. A separately copied distribution folder built successfully as `deskyard-docker:example`, ARM64, image ID `sha256:40f9ff2a901858694227a6c885ef769d9bf7a48906a574eb4fe747d857a245a2`.

The full base rebuild was stopped during a slow unrelated 140 MB mise download. The tested base instead used the previously verified fresh XFCE image `sha256:82b644bec8c7336016f5cf040492e7cee54137d1440833f49b8a6980d8d798e1`, overlaid with the exact current `workspace-init` and `workspace-docker-access` startup files. That focused base was tagged `deskyard-debian-xfce:docker-access`; the example was built with `--build-arg BASE_IMAGE=deskyard-debian-xfce:docker-access`. This verifies the changed startup behavior, not a complete rebuild of every current base dependency.

An ownership-scoped `vpnintegration` preview supervisor ran the real server, UI and lifecycle against OrbStack without production-main reconciliation. Its fresh private data directory had no `docker-demo` workspace before creation. The existing `retroarch-fresh` desktop's shared Chromium reached it through `deskyard forward`, and Playwright attached through `deskyard browser connect`. The UI checkbox was initially unchecked, explicitly enabled, and the desktop created from the new example image. Settings returned the saved enabled value.

![Explicit Docker permission in the shared desktop browser](Docker%20access%202026-10-24/checkbox.png)

`deskyard exec` ran `id`, `docker version` and `sh ~/docker-demo/build-and-run.sh` as UID 1000. Docker client 26.1.5 connected to OrbStack Engine 29.8.2. The demo built a uniquely tagged Alpine-based image from workspace files, ran it, checked `Hello from an image built inside a Deskyard desktop!`, and removed its own tag. The independently checked `docker exec -u user` path also reached the daemon. The actual supervisord-started XFCE session ran as UID 1000 with socket GID 0 in its supplementary groups on this host.

Using Deskyard keyboard control, Ctrl+Alt+T opened a native shared terminal, and the same demo passed there with exit 0. Screenshots were captured using `deskyard screenshot`.

![Docker build and run from the native desktop terminal](Docker%20access%202026-10-24/terminal.png)

The Settings checkbox was then unchecked and saved. After recreation, `/var/run/docker.sock` was absent, Docker could not connect, and a workspace marker remained intact. The checkbox was enabled again and the restored desktop could reach the daemon. [CLI build/run log](Docker%20access%202026-10-24/cli.log).

# Automated verification and limits

- Full untagged Go race suite passed, including image-authoring removal and local-prebuilt catalog tests; tagged scoped-fixture compilation passed.
- Focused lifecycle/API race tests cover default-off behavior, administrator restrictions, grant/revoke, omitted-field preservation, unavailable socket preflight before stop, rollback and VPN setting preservation.
- Four small image tests use inert synthetic sockets in disposable networkless containers. They check existing/new/GID-0 group membership, repeated setup, preserved user groups and unchanged socket metadata. They never expose the host daemon or build images.
- Fourteen mock browser tests, 60 frontend unit tests, TypeScript checking and the full supervisor/UI build passed. Browser tests exercise checkbox state and request payloads with mocks.
- The real example image build, native terminal demonstration and socket revocation check are manual acceptance work. They are not added to the normal E2E suite and do not download RetroArch, games or large application runtimes.
- SilverBullet metadata queries and documentation/diff checks passed. Full production-main integration tests were not run on this shared engine because they can reconcile unrelated desktops.

Runtime evidence is macOS/ARM64 with OrbStack. Native Linux/amd64 runtime behavior and registry publication remain unverified. Only daemon-side `/var/run/docker.sock` is supported; custom/rootless socket paths fail explicitly. Docker access grants control of the shared host daemon to every user and program on the desktop, including the ability to mount host files and bypass desktop network isolation. Disabling prevents future access through this desktop; it does not undo host changes or remove containers previously launched through the daemon.

# RetroArch build from the enabled desktop

A follow-up manual acceptance builds the cleaned `examples/retroarch/` folder from the native terminal in the Docker-enabled desktop as UID 1000. Test-only files (`test-image.py`, `verify-cores.c`, the copied seccomp profile and its license) were moved to `images/manual/retroarch/`, outside both the distributed example and normal E2E collection. The Dockerfile no longer compiles or ships the ABI probe. This uses a separate output tag, `deskyard-retroarch:from-desktop`, and the same previously verified local fresh base override. The native-terminal build completed with exit 0. All five source archives passed their pinned SHA-256 checks and all five cores compiled in 75.8 seconds. This was not merely a cached final-image retag: Flatpak freshly downloaded the graphics/KDE runtimes and RetroArch application, and Docker exported the resulting image. The application/runtime installation took most of the roughly 21-minute build. This remains manual acceptance, outside normal E2E coverage.

The new ARM64 image is `sha256:655e0d2be6652c0da83d1b78b70373df43660e1145bf455d3d26bdfce0711094` (6,313,624,340 bytes), labelled `deskyard.example.build-origin=docker-desktop`. A subsequent `docker run --rm --network none` from the same desktop confirmed system RetroArch 1.22.2, all five `.so` cores in the home template, and no shipped ABI probe, probe source or test-profile license. This follow-up checks build output contents; it does not repeat the earlier GUI/gameplay acceptance against the new image.

![Successful RetroArch build from the Docker-enabled desktop terminal](Docker%20access%202026-10-24/retroarch-build.png)

[Concise build log](Docker%20access%202026-10-24/retroarch-build.log). The full log remains at `/workspace/image-builds/retroarch-build.log` in the Docker-enabled desktop. The cleaned build context remains at `/workspace/image-builds/retroarch/`, and the example distribution contains ten files. The manual checker is now documented separately in `images/manual/retroarch/README.md`.

# Landing verification

Final checks before committing passed: `make -C supervisor test typecheck build` (Go race suite, 60 UI unit tests, TypeScript and production binaries/UI), the 14 Docker-free browser tests in `test_ui_image_catalog.py` and `test_ui_images.py`, and all four synthetic socket tests against the existing tested base. Shell syntax, Python parsing, staged diff checks and SilverBullet page lint passed. No heavy build was added to E2E. The full production-main supervisor integration fixture was deliberately excluded on this shared engine to protect unrelated desktops; manual scoped Docker/RetroArch evidence above covers the requested real build flow.
