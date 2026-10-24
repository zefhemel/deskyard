# Third-party notices for the RetroArch example

This folder describes a derived Deskyard image. It does not grant rights to games, BIOS files, trademarks or third-party software beyond their own terms. No games or BIOS files are included.

**Snes9x and Genesis Plus GX restrict commercial use/distribution.** Their inclusion makes this example unsuitable for a claim that the entire image is commercially unrestricted. The licenses of the base desktop, RetroArch, Flatpak runtimes and the other cores still apply independently.

## Exact core sources

[cores.tsv](cores.tsv) identifies each repository, commit and verified archive hash. The build keeps the complete unmodified source archives at `/usr/share/doc/deskyard-retroarch/sources/`. Those archives contain the original copyright notices, license texts and component-specific terms; preserve them when redistributing the image. [build-cores.sh](build-cores.sh), [Dockerfile](Dockerfile) and the manifest describe how the cores are built.

| Source repository | Primary license file | Material terms and additional notices |
| --- | --- | --- |
| `libretro/snes9x` | `LICENSE` | Permission covers non-commercial purposes. The file includes restrictions from the Libretro port and notices for incorporated components. |
| `libretro/libretro-fceumm` | `Copying` | GNU GPLv2 text. Main source headers include GPLv2-or-later grants; incorporated code has its own headers. |
| `libretro/Genesis-Plus-GX` | `LICENSE.txt` | Redistribution may not be sold or used in a commercial product/activity. The file also contains component terms, including LGPL-covered Nuked OPN2 and BSD-style Tremor notices. |
| `libretro/gambatte-libretro` | `COPYING` | GNU GPLv2 text; sampled Gambatte source headers specify version 2. Preserve nested component notices too. |
| `libretro/beetle-pce-fast-libretro` | `COPYING` | GNU GPLv2 text, with GPLv2-or-later and other license headers in the source tree. Bundled libraries and platform-specific files carry additional terms; do not treat the root file as the complete inventory. |

The source archive and build files must remain available as required by the applicable licenses when distributing compiled cores. Merely linking to an upstream homepage does not substitute for providing required corresponding source. The retained core source archives do not settle source obligations for independently installed Flatpak applications or the base image.

## RetroArch, Flatpak and the base desktop

RetroArch is installed from Flathub's stable `org.libretro.RetroArch` ref at build time. The resolved application, runtime and extension refs/commits are recorded in `/usr/share/doc/deskyard-retroarch/flatpak-commits.txt`. RetroArch is GPL-covered; runtimes and extensions contain separately licensed software. Preserve their applicable copyright notices, license texts and required source availability for the exact commits you distribute.

The Deskyard base image also contains third-party software, including Debian packages, Flatpak, Chromium and the streaming stack. This example is an additional inventory, not a complete notice bundle or a determination that all obligations of the base image have been fulfilled. When copied out of the Deskyard repository, this folder remains buildable, but redistributors still need the base image's applicable notices and corresponding source.
