# Manual RetroArch image acceptance

These optional checks are outside the distributable example and normal E2E collection. The desktop image contains no test script or ABI probe. No games are bundled.

From the repository root, build a probe for the same architecture as the desktop image, then run the offline empty-workspace, five-core, menu and persistence check:

```sh
docker build -f images/manual/retroarch/Dockerfile.probe \
  --output type=local,dest=/tmp/retroarch-probe images/manual/retroarch
python3 images/manual/retroarch/test-image.py deskyard-retroarch:example \
  --probe /tmp/retroarch-probe/verify-cores
```

The probe build needs internet access. Acceptance uses a fresh, networkless desktop and copies the probe into its temporary container using Docker's file-transfer API. It does not mount paths from the invoking desktop into sibling containers. `--manifest PATH` selects a copied example's `cores.tsv`; `--artifacts PATH` selects the results folder. An optional `--rom PATH` checks a user-supplied SNES game after baseline checks. The test removes its own container and volume.

`flatpak-seccomp.json` is copied from Deskyard's generated supervisor profile, based on [moby/profiles seccomp/v0.2.4](https://github.com/moby/profiles/blob/seccomp/v0.2.4/seccomp/default.json), under Apache License 2.0; see `LICENSE.seccomp`. Modification notice: Deskyard's `scripts/gen-seccomp.py` removes conflicting argument-filtered rules for `clone`, `clone3`, `unshare`, `setns`, `mount`, `umount2` and `pivot_root`, then permits those calls so Flatpak can create its sandbox. This copied profile grants no `CAP_SYS_ADMIN` and changes no daemon configuration.
