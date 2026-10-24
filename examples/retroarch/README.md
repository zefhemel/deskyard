# RetroArch desktop image

Build a Deskyard XFCE image with RetroArch and five emulator cores already installed. Open RetroArch's menu on a new desktop without downloading an application or core at runtime. No games, BIOS files, saved games, browser profiles or credentials are included.

This example includes **non-commercial cores**: Snes9x and Genesis Plus GX. Use and redistribution must respect their restrictions. It is not a commercially unrestricted image. See [NOTICES.md](NOTICES.md) and the exact source licenses retained in the image.

## Build

You need Docker with network access and enough disk space for the desktop, Flatpak application/runtime and compiler stages. From the repository root:

```sh
docker build -t deskyard-retroarch:example examples/retroarch
```

The default base is `ghcr.io/zefhemel/deskyard-debian-xfce:latest`. To use an existing compatible local image or pin a published base digest:

```sh
docker build \
  --build-arg BASE_IMAGE=deskyard-debian-xfce:dev \
  -t deskyard-retroarch:example examples/retroarch
```

The folder is the complete build context: copy it elsewhere and run `docker build -t deskyard-retroarch:example .` from that copy. The build inherits the base's Deskyard entrypoint and image contract. It does not require a game download, a running supervisor or an existing desktop workspace.

The cores compile natively for the selected Linux build architecture using Debian trixie, the upstream `platform=unix` targets and two compiler jobs. The default build uses your Docker builder's architecture. Selecting another platform does not establish that its runtime was tested.

## Included cores

| Core | Systems | Source license overview |
| --- | --- | --- |
| Snes9x | Super Nintendo / Super Famicom | Non-commercial |
| FCEUmm | Nintendo Entertainment System / Famicom | GPL, including GPLv2-or-later headers |
| Genesis Plus GX | Sega Mega Drive / Genesis, Master System, Game Gear and related systems | Non-commercial; additional component licenses |
| Gambatte | Game Boy / Game Boy Color | GPLv2 |
| Beetle PCE Fast | PC Engine / TurboGrafx-16 | GPL family; additional component licenses |

The exact repositories, commits, archive SHA-256 values, makefiles and output names are in [cores.tsv](cores.tsv). [build-cores.sh](build-cores.sh) verifies each source archive before compiling it. No prebuilt core is downloaded.

The source archives remain at `/usr/share/doc/deskyard-retroarch/sources/` in the final image, including their original license files. Additional application/runtime components retain their own notices and source obligations; this table is not a complete image inventory.

## Create a desktop

Add a locally built image reference, or publish it to a registry you can use and add that reference through the supervisor's **Images → Add image** flow and select it when creating a desktop. For example, tag and push your image under your own registry namespace:

```sh
docker tag deskyard-retroarch:example YOUR_REGISTRY/YOUR_NAMESPACE/deskyard-retroarch:example
docker push YOUR_REGISTRY/YOUR_NAMESPACE/deskyard-retroarch:example
```

Create a **new desktop with an empty workspace**. The base's initialization copies `home-template/` into an unseeded workspace as UID/GID 1000. Previously initialized workspaces are not reseeded, and the template does not overwrite existing files. Switching an existing desktop onto this image therefore does not promise to install its per-user cores or configuration.

RetroArch and its Flatpak runtime are installed system-wide under `/var/lib/flatpak`, so they belong to the image. The seeded cores, application configuration and desktop shortcut belong to `/workspace`, so user changes persist across restarts and image replacements. Rebuilding an image does not replace cores already copied into an initialized workspace.

## Open RetroArch and load content

Open the **RetroArch** desktop shortcut, or run:

```sh
retroarch-deskyard
```

The launcher opens the menu and passes any extra arguments directly to RetroArch. Copy your own game files into the workspace and choose **Load Content** in the menu. Supply any BIOS required by your selected system yourself; none is bundled. Configuration defaults point to these paths:

```text
~/.var/app/org.libretro.RetroArch/config/retroarch/retroarch.cfg
~/.var/app/org.libretro.RetroArch/config/retroarch/cores/
~/.var/app/org.libretro.RetroArch/config/retroarch/system/
```

The cores live in RetroArch's own writable Flatpak application data, avoiding a dependency on host `/usr` or `/opt` visibility. The default menu is Ozone, with fullscreen enabled and smoothing disabled. The build also seeds an architecture-specific HTTPS Core Downloader URL for optional later downloads; the five included cores do not require using that downloader.

## Versions and reproducibility

Core source commits and archive hashes are pinned. The Debian compiler packages, Debian builder tag, default Deskyard base tag, and Flathub stable application/runtime are moving inputs. The image is **not bit-for-bit reproducible**. Pinning `BASE_IMAGE` to a digest fixes only that input.

The [Dockerfile](Dockerfile) records the installed Flatpak refs and full commit IDs, including runtimes/extensions, at `/usr/share/doc/deskyard-retroarch/flatpak-commits.txt`, in a separate layer after [installation](install-retroarch.sh). Retain that record with the image digest when sharing a tested build. A later rebuild may resolve another Flatpak runtime; rerun the offline ABI/menu test to check the source-built cores still load in it.

No application license is replaced by this README. Review [NOTICES.md](NOTICES.md), preserve the exact bundled notices/source material and respect the non-commercial restrictions before distributing an image.
