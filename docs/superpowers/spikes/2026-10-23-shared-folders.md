---
displayName: Dynamic shared folders on Mac and Linux investigation
date: "2026-10-23"
tags: "spike"
status: "concluded"
conclusion: "Supervisor-managed live mounts are feasible on tested storage; ACL-only isolation is unsuitable for the tested Mac-backed folders."
features: ["[[Features/Named shared folders]]"]
---
Dynamic named folders are feasible without changing desktop UIDs: a supervisor-controlled helper can attach a read-only or writable mount to a running desktop's mount namespace. This was verified on this Mac with OrbStack, using both Mac-backed folders and Linux-native storage, and between real XFCE and MATE desktops. A global shared root protected only by POSIX permissions/ACLs is unsuitable for the current Mac-backed storage: the tested filesystem rejected ACLs and did not isolate the test UIDs using mode 0700.

This page records investigation evidence; named dynamic sharing remains unimplemented. Per-desktop host-folder mounts were subsequently implemented separately, as recorded below. The original request is named shared folders with explicit desktop membership and read-only/read-write permissions. The subsequent requirement is to investigate dynamic access and support both Mac and Linux hosts. All programs next to this page are disposable diagnostic probes, not production helpers or permanent integration tests.

# Platform evidence

| Environment | Tested | Result |
| --- | --- | --- |
| macOS ARM64, OrbStack Docker Engine 29.8.2, kernel `7.2.9-orbstack-00391-g2b298d05fbd4` | Mac folder bind-mounted into containers | `setfacl` failed with `Operation not supported`. A second UID could read a directory after another UID owned it with mode 0700. Mount-level read-only enforcement worked. |
| Same Linux engine, native Btrfs Docker volume | POSIX ACLs, directory permissions, per-desktop mounts | ACL grants/denials and live additions worked; ownership and retained handles limited policy enforcement. Live read-only mounts worked. |
| Same Linux engine, bind mount of a native Btrfs directory | ACL and UID behavior independently of named-volume mounting | Same results as native volume. |
| Real XFCE and MATE desktops, existing UID 1000 | Mac-backed shared data, live helper attachment | XFCE wrote data read by MATE. MATE's read-only mount denied writes and chmod, including an attempted user-namespace remount. Both desktops remained ready. |
| Real XFCE desktop, experimental UID/GID 21001 | Existing Mac-backed workspace seeded as 1000, remapped account and initialization | Home files migrated, desktop services and Chromium ran as the new UID, writes worked, and identity/files survived restart. The initial readiness failure exposed another UID assumption, fixed only in the throwaway image. |
| Docker Desktop on Mac | Availability checked | Daemon socket unavailable; `/Applications/Docker.app` absent. No Docker Desktop runtime validation claimed. |
| Independently booted native Linux host | Not available in this session | Linux filesystem/kernel behavior was exercised inside OrbStack's Linux engine, not on an independent production host. Native Linux deployment validation remains required. |

There are 60 recorded ACL/storage observations, 44 mount-helper observations, 14 real-desktop sharing observations and 8 corrected UID-desktop observations. Expected permission failures and deliberately demonstrated bypasses are recorded as such; these are not 126 passing product tests. Raw results include stderr and exit codes.

# Shared root with unique desktop identities

A root-owned share directory can grant named desktop UIDs read, write and traversal permissions through ACLs. Changing the ACL takes effect on subsequent permission checks without recreating a desktop. Supplementary group memberships are process credentials: editing a group database does not update running applications. Stable UIDs with changing ACL entries are therefore more useful for live membership than adding/removing Unix groups.

The Linux-native probes verified:

- No access before a grant; a writer can create a file; a reader can read it but cannot append or create files; an unrelated UID cannot read it.
- Granting write access while containers run enables writes immediately. Creating another share under the mounted root is visible without recreating the desktop.
- ACLs survive client restart and recreation; shared data survives removing the writer container.
- A former writer downgraded with a recursive named-user ACL can still write files it owns: the owner entry takes precedence over the named-user entry. Root could also rewrite owner permissions, but the owner can change them again.
- A writer can run `chmod 600` on its own file and prevent another desktop reading it. A program creating a file explicitly with mode 0600 suppresses inherited sharing permissions through the ACL mask. Default ACLs do not override the application's mode argument.
- Removing permission on the share's top directory denies a new absolute-path open. A process holding a descriptor for a deeper subdirectory can still open files relative to that directory when the descendants still permit access. Top-directory gating alone is insufficient for strong revocation.
- Removing access on the file itself denies new opens, but an already-open descriptor still reads it.
- Non-root clients with dropped capabilities could not change identity or chmod the root-owned share gate. Symlinks did not bypass target permissions.

These are properties of discretionary filesystem permissions, not bugs in the probe. An ACL design could serve cooperative Linux users, but it cannot transparently promise consistent administrator-controlled read-only policy for former file owners. Recursively repairing every file also introduces races and cost proportional to share size.

On the Mac-backed folder, even the baseline UID/mode isolation did not hold. This is an observed failure on this OrbStack setup, not a claim about every Mac runtime. It is sufficient to reject host-filesystem ACLs as the portable security boundary. Moving shares into a Linux-native Docker volume repairs filesystem semantics on this engine, but changes Deskyard's plain-host-folder storage and backup model, and does not repair ownership/handle limitations.

# UID migration impact

Current implementation assumptions are broader than account creation:

- `images/Dockerfile` creates `user` as 1000:1000 and seeds a home template with that ownership.
- `images/common/rootfs/usr/local/bin/workspace-init` recursively fixes the home and creates the runtime directory with numeric 1000 ownership.
- Every `images/debian-{xfce,kde,mate}/rootfs/etc/workspace/desktop-session.sh` readiness check uses `pgrep -u 1000`.
- `supervisor/internal/recipes/dockerfile.go` writes templates with `--chown=1000:1000`.
- Image scanning in `supervisor/internal/server/checks.go` and `supervisor/internal/imagescan/scan.go` assumes the base image's user is UID 1000. Build-time image identity and per-container runtime identity would need an explicit distinction.
- Image and engine tests assert UID 1000. Named `user` exec calls are compatible with remapping, but ownership, custom-image contracts, UID allocation/reuse, restored home directories and any saved numeric ACLs need a migration design.

The throwaway XFCE image changed the named account before user processes started, made initialization ownership dynamic and made the session readiness check use `id -u`. This demonstrated feasibility, not a safe migration implementation. KDE/MATE with remapped UIDs, installed Flatpak application behavior, large homes, symlink/hardlink migration safety and custom images were not validated. UID migration is unnecessary for the mount-based option below.

# Live per-desktop mounts

A second probe tested the Linux mount namespace API directly, avoiding mount propagation and filesystem ACLs:

1. The supervisor starts a short-lived trusted helper with only the selected source folder mounted into it, no network, `SYS_ADMIN` in addition to Docker's default capabilities, and the target container's PID namespace (`--pid=container:<id>`). Test desktop clients receive no extra capabilities.
2. The helper clones the source into a detached mount with `open_tree(OPEN_TREE_CLONE)`.
3. For a reader it sets read-only, nosuid and nodev on the detached mount with `mount_setattr` before the mount is exposed. This avoids a transient writable attachment.
4. It joins PID 1's mount namespace with `setns`, adopts the target root and uses `move_mount` to attach at `/shared/project`.
5. The helper exits and is removed. The mount remains in the desktop namespace. No host PID namespace, host root mount, Docker socket in a desktop, propagation configuration or per-desktop UID is needed.

The tests used the same UID 1000 for the reader and writer. The kernel denied the reader writes with `Read-only file system` on both the Mac-backed mount and the Linux-native mount. An unselected desktop had no such mount. The reader could not remount it. Real MATE also failed to make the mount writable inside an unprivileged user namespace.

# Permission changes, revocation and recovery

The mount probes established the following behavior on both tested backends:

| Operation | Observed behavior | Required product behavior |
| --- | --- | --- |
| Attach writable or read-only while desktop runs | Works; source data is immediately shared | Apply live after validating the source and destination. |
| Change writable mount to read-only while a writable file descriptor is open | `EBUSY`; existing writer remains usable | Report that the restriction was not applied. Require applications to close files, or recreate/restart with the desired settings. |
| Change writable mount to read-only after writer closes | Works; subsequent writes return `EROFS` | Commit applied policy only after success and verification. |
| Normal unmount while a reader holds a file open | `EBUSY` | Treat removal as pending/failed, not revoked. |
| Lazy unmount with that reader open | Path disappears, but retained descriptor still reads | Do not use lazy unmount as proof of access revocation. |
| Normal unmount after file closes | Works; subsequent path access fails | Complete detachment. |
| Restart container | Manually attached mounts disappear; data persists | Store desired policy durably and reconcile after startup/restart/recreation. |
| Reapply the saved attachment after restart | Works and previous data is visible | Retry transient helper failures and expose desired versus applied status. |

Replacing a writable mount with a new read-only mount would not revoke access held through the old mount. Refusing an unsafe transition is preferable. Applications can also retain data already read, and no filesystem sharing design can recall those copies.

# Recommendation for Mac and Linux

Implement named shares as supervisor-owned host directories, mounting only the selected folders into each desktop at `/shared/<name>`. Keep the image contract and UID 1000. Avoid exposing the complete shared root to every desktop and avoid relying on host ACL support.

Use live mount helpers only after a functional engine capability probe verifies attachment, read-only enforcement and removal on a disposable target. Linux supplies the same mount APIs on both native Linux and Mac container engines; this is why the approach works on the tested Mac without macOS mount privileges. Do not infer availability solely from host OS or kernel version. Docker Desktop Enhanced Container Isolation, user-namespace remapping, rootless engines, LSM policies and older kernels can restrict these operations.

Keep recreation-based bind mounts as a portable fallback and as the explicit path for a busy read-only downgrade or removal. This uses the repository's existing storage and lifecycle model. The UI must clearly distinguish a live change from one needing restart and must never claim a busy permission restriction succeeded.

A production implementation still needs:

- An optional engine capability for managed mount operations, with PID namespace/capability controls isolated in the Docker adapter. Existing `engine.ContainerSpec` has neither PID-namespace sharing nor capability fields.
- A fixed, preferably static helper with bounded runtime/resources, no network, no user-provided command/env, a read-only helper root filesystem and the minimum capabilities proven necessary. The Python probe uses default helper capabilities plus `SYS_ADMIN`; it is not hardened production code.
- Trusted source-directory resolution and a root-owned, non-user-writable `/shared` target; descriptor-based resolution, symlink rejection and verification of the target namespace/container identity before and after mutation. Avoid following user-controlled paths during privileged work. Apply nosuid/nodev to writable as well as read-only attachments.
- Durable desired shares and per-desktop read-only flags, serialized against power/image/remove operations; reconciliation after supervisor/container/engine restart. Docker's configured mounts/labels alone do not record live helper mutations.
- Handling for a desktop restarting during a helper operation, partially applied multi-share changes, cancellation, helper crashes and rollback. Fail closed by leaving unconfigured shares unavailable and reporting failed/pending restrictions.
- A Mac and native Linux CI matrix for live and fallback paths, including adversarial paths, restart recovery, open handles and user namespaces. Extend the real desktop test through the supervisor API and UI.

This is enough evidence to select the mount-based architecture. It is not enough to advertise every Docker Desktop configuration or native Linux deployment as tested. Docker Desktop and an independent Linux host must run the same probes before those runtime-specific claims are made; the functional capability check and recreation fallback keep the product usable where live helpers are unavailable.

# Reproduction and artifacts

All changes are in the `named-shared-folders` worktree. Run Docker-related commands outside the sandbox. From the artifact directory next to this page:

```sh
docker build -t deskyard-sharing-probe:investigation .
python3 probe.py
python3 mount-probe.py
python3 real-mount-probe.py
docker build -f Desktop.Dockerfile -t deskyard-sharing-desktop-probe:xfce .
python3 desktop-probe.py
python3 verify-results.py
```

The real desktop probes require the local `deskyard-debian-xfce:test` and `deskyard-debian-mate:test` images built from this repository. The UID Dockerfile defaults to the XFCE image. Do not run multiple copies of a script from the same artifact directory simultaneously: result filenames are fixed. Each run uses unique container and volume names and removes its own resources. The initial UID probe hit a readiness failure and a cleanup race; corrected scripts remove desktops before deleting their homes. Initial observations are preserved separately. Probe-built images are removed at the end of this investigation; reproduce by building them again.

- [ACL/storage probe and results](2026-10-23-shared-folders/probe.py), [raw results](2026-10-23-shared-folders/results.json).
- [Mount helper](2026-10-23-shared-folders/mount.py), [driver](2026-10-23-shared-folders/mount-probe.py), [raw results](2026-10-23-shared-folders/mount-results.json).
- [Real desktop mount probe](2026-10-23-shared-folders/real-mount-probe.py), [raw results](2026-10-23-shared-folders/real-mount-results.json).
- [UID desktop probe](2026-10-23-shared-folders/desktop-probe.py), [corrected results](2026-10-23-shared-folders/desktop-results.json), [initial observations](2026-10-23-shared-folders/desktop-initial-results.json).

# Documentation and verification

All four saved probes were rerun successfully from this worktree's artifact directory. `verify-results.py` independently checked the recorded outcomes, including expected permission denials, the Mac ACL failure and the ownership/open-handle limitations. Python sources parsed successfully; local document links and referenced implementation paths were checked. SilverBullet indexed this report with its intended title and returned no lint issues for the report, index and both changed architecture pages. `git diff --check` was clean. Temporary probe containers and volumes were absent after cleanup; the two investigation image tags were removed without pruning unrelated images or build cache.

Documentation updated: this report and artifacts, [[index]], [[Architecture/Workspace folder]] and [[Architecture/Engine adapter]]. README, API contract and built component statuses remain accurate because no production sharing feature or interface has been added. No spec or implementation plan is marked approved or complete by this investigation. Go/UI suites were not run for these documentation and disposable-probe changes.

# References

- [Linux ACL permission checks, owner precedence and default ACL inheritance](https://man7.org/linux/man-pages/man5/acl.5.html).
- [Process identity and inherited supplementary groups](https://man7.org/linux/man-pages/man7/credentials.7.html).
- [Path lookup relative to working directories and directory descriptors](https://man7.org/linux/man-pages/man7/path_resolution.7.html).
- [Detached mount creation](https://man7.org/linux/man-pages/man2/open_tree.2.html), [mount attributes and busy read-only changes](https://man7.org/linux/man-pages/man2/mount_setattr.2.html), [mount movement](https://man7.org/linux/man-pages/man2/move_mount.2.html).
- [OrbStack host mounts versus Linux-side volumes](https://docs.orbstack.dev/docker/file-sharing), [Docker-managed volume lifecycle](https://docs.docker.com/engine/storage/volumes/).

# Subsequent implementation: per-desktop host folders

The user subsequently requested per-desktop mounts from the host environment and accepted restart to activate them. This bounded Settings/lifecycle extension is implemented in the same worktree: admin-configured `{source, target, readOnly}` mounts of existing engine-host folders at paths below `/mnt/`. New UI entries default to read-only. Changes use ordinary container recreation; the live mount helper above remains experimental and is not used by this feature.

Configuration is recovered from container labels and survives image upgrades and unrelated settings changes. A networkless helper checks source directories with read-only strict binds before the current desktop stops; absent folders are never created. Source folders are neither chowned nor chmodded. Detachment leaves sources untouched, and desktop deletion remains scoped to its private workspace. A regression found during this work also corrected rollback so a previously running desktop is restarted after its old configuration is restored. README, HTTP API and affected architecture pages describe the implemented behavior. The earlier verification section records only the investigation, before this feature was added.

Code review identified the need to require recursive read-only enforcement for nested source mounts; strict binds now fail when that enforcement is unsupported, including during source preflight. Desktops with host mounts also disable new privileges through setuid executables/file capabilities. A real engine regression checks a nested Linux tmpfs; browser integration verifies the kernel privilege flag and Bubblewrap user-namespace compatibility. This static feature needs a Linux kernel with recursive read-only support even on Mac (inside its runtime).

# Host-folder implementation verification

The per-desktop host-folder feature passed `make test` from `supervisor/` (all Go packages with the race detector), `make typecheck` (TypeScript plus 23 UI unit tests), and `make build` (UI, scanner and both binaries). Targeted real-supervisor coverage passed four tests with the supervisor running directly and three with it running in a container; the latter used a separate source path not mounted into the supervisor itself. Coverage includes mobile settings add/remove, read-only defaults and kernel enforcement, writable source changes, preservation through unrelated settings changes, stopped-state preservation, missing-source rejection without creating a folder or stopping the desktop, and retaining external data after desktop deletion. A mobile screenshot was visually inspected.

The Docker engine regression passed against a nested Linux tmpfs and verified forced recursive read-only enforcement and `NoNewPrivs`. A simulated older daemon test verified rejection of API 1.43 before container creation. Independent review found no remaining confirmed implementation issues after the recursive-read-only fix; its compatibility documentation request is included. SilverBullet lint and local link/path checks passed for the affected documentation. Tests used isolated desktops and a dedicated supervisor image/subnet; helper containers and the dedicated image were cleaned up.

Final landing verification ran the repository-root default `make test` against the feature integrated with main (`6715d2b`), using a dedicated desktop-image tag and subnet. Go vet/race tests, 23 UI unit tests and engine integration tests passed. Desktop-image coverage passed 92 parallel and one serial test, with three MATE-only skips; supervisor coverage passed 134 parallel and six serial tests, with nine Firefox skips because Playwright could not start Firefox on this host. The default suite excludes slow, network, build, performance and WebKit tests.

Runtime coverage is this Mac with OrbStack and its Linux engine. Native Linux and Docker Desktop were not independently run. This feature's user-namespace compatibility was checked with Bubblewrap inside a real mounted desktop. The earlier live-sharing probes and their limitations remain separate from this implemented restart-based feature.
