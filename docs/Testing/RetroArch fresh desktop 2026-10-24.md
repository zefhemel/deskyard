---
displayName: RetroArch fresh desktop testing — 24 October 2026
date: "2026-10-24"
status: complete
---
A fresh XFCE desktop trial installed RetroArch 1.22.2, installed Snes9x through RetroArch's own Core Downloader and verified movement in the user-selected Super Mario World game. The trial is complete, with an ARM64 caveat: the downloader needed an explicit core-list URL before the GUI installation worked.

# Image build and clean baseline

The host built `deskyard-debian-xfce:retroarch-fresh-20261024-01` from the current repository source before starting the in-desktop trial. The build produced digest `sha256:82b644bec8c7336016f5cf040492e7cee54137d1440833f49b8a6980d8d798e1`, distinct from the previous image identity beginning `sha256:1f03551`. The desktop's CLI reports `deskyard retroarch-fresh-20261024-01`. This host-side image build is setup evidence, separate from the installation and application workflow inside the desktop.

The new desktop is `retroarch-fresh`, with a new workspace. Before the trial, checks found zero installed Flatpak applications and no `Games` folder. This trial does not reuse the existing `retroarch` desktop's installed application or game state.

![Fresh XFCE desktop before RetroArch installation](RetroArch%20fresh%20desktop%202026-10-24/empty.png)

# In-desktop workflow

The Deskyard CLI connected to the desktop's shared Chromium through `browser connect --port 9334`. The browser downloaded the official RetroArch Flatpak reference and the user-selected Super Mario World ZIP to `/workspace/Downloads`, with Chromium's `Browser.setDownloadBehavior` set to allow downloads there.

Flatpak's language selection was set to `en` before installation to avoid downloading unused translations. The user-scope installation completed and RetroArch 1.22.2 launched successfully.

# ARM64 Core Downloader diagnosis

Initially **Online Updater → Core Downloader** silently returned without listing cores. The saved `core_updater_buildbot_cores_url` was empty. RetroArch 1.22.2's Linux default selection has no aarch64 branch, and its updater exits before making a request when the URL is empty. This matches the observed symptom; see the pinned upstream [default configuration](https://github.com/libretro/RetroArch/blob/v1.22.2/config.def.h#L1703-L1712) and [updater task](https://github.com/libretro/RetroArch/blob/v1.22.2/tasks/task_core_updater.c#L263-L303).

Both HTTP and HTTPS core-index requests from the desktop returned status 200 and 10,730 bytes, distinguishing the empty configuration from a connectivity failure. After closing RetroArch, the trial set `core_updater_buildbot_cores_url` to `https://buildbot.libretro.com/nightly/linux/aarch64/latest/` and reopened it. This is a workaround measured with this ARM64 Flatpak/version, not evidence that all RetroArch builds need it.

The reusable diagnostic is to check the configured architecture-specific core-list URL when the downloader silently does nothing, then test that index from the same desktop. Set a missing URL with the application closed so its exit does not overwrite the change. Reopen RetroArch and install through its downloader; a working index does not by itself prove that a core was installed.

# GUI core installation and game launch

In RetroArch, the trial selected **Online Updater → Core Downloader**, pressed `/` to search for `snes9x`, and installed **Nintendo - SNES / SFC (Snes9x)**. No core file was manually downloaded or copied. The resulting core was 2,652,472 bytes, with SHA-256 `c8ce1ec5d5c15f238e4cc0e4bf154b6e2937b8b8fd1f498c4376cd81929630c6`.

![Snes9x installed through RetroArch Core Downloader](RetroArch%20fresh%20desktop%202026-10-24/core-downloaded.png)

The browser-downloaded archive `/workspace/Downloads/Super_Mario_World[romsretro.com].zip` was extracted to `/workspace/Games/Super Mario World/Super Mario World (U) [!].smc`. The game was opened through RetroArch's **Load Content → /workspace → Games → Super Mario World → Super Mario World (U) [!].smc** GUI path and reached its title screen with Snes9x.

![Super Mario World title screen on the fresh desktop](RetroArch%20fresh%20desktop%202026-10-24/title.png)

# Gameplay and handover

Gameplay input was verified inside a level. Holding Right for 0.8 seconds moved Mario from approximately x=210 to x=480 in the 1280px screenshots. A 0.4-second `z` jump input was sent, but the following screenshot shows Mario already on the ground, so these images establish movement rather than an airborne jump. The game was left paused with Enter.

![Mario before movement input](RetroArch%20fresh%20desktop%202026-10-24/gameplay-before.png)

![Mario after movement input](RetroArch%20fresh%20desktop%202026-10-24/gameplay.png)

Controls are arrow keys to move, `z` to jump, `a` to run, Enter to start/pause, and F1 for the RetroArch menu. Movement and pause were exercised; the remaining mappings are provided for handover rather than independently verified here.

A desktop shortcut at `/workspace/Desktop/Super Mario World.desktop` and executable launcher at `/workspace/.local/bin/play-super-mario-world` were created to open the installed Snes9x core and game in fullscreen. The launcher's `sh -n` syntax check passed, and `flatpak list --user --app --columns=application,version` confirmed `org.libretro.RetroArch` version 1.22.2. Shortcut activation was not separately tested; the successful launch above used RetroArch's GUI Load Content flow.

This is a local ARM64 trial on a newly built XFCE image and clean workspace. It does not establish amd64 behavior, native mobile input performance, sustained frame rate or audio quality. The application-specific downloader workaround is recorded here; the generic shipped workspace skills and Deskyard's image defaults were not changed.
