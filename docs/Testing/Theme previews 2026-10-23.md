---
displayName: Desktop theme previews — 23 October 2026
date: "2026-10-23"
tags: "verification"
result: "passed"
scope: "Recorded desktop theme previews and the selected MATE appearance; KDE preview was not selected."
---
Visual previews of a coordinated appearance for the KDE and MATE images from [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]]. These are real 1280×800 screenshots from disposable containers with fresh homes, using the same file-manager content, terminal sample and window placement. The initial preview task did not change runtime defaults. The selection below records the subsequent MATE-only implementation; KDE's themed preview was not selected.

# Direction

Both previews use an original blue [SVG wallpaper](Theme%20previews%202026-10-23/wallpaper.svg), Noto fonts, light application windows, a dark bottom panel and shortcuts to Files, Chromium and the graphical app installer. Compositing remains disabled. KDE keeps Breeze and its native icons, adds Breeze GTK styling and uses 11-point interface text with a 48-pixel panel. MATE uses Arc, Papirus icons and 10-point Noto interface text, replaces its two panels with one 40-pixel panel, and gives Terminal an opaque navy background. During the preview task, theme/font packages were installed only in the temporary containers; the configuration scripts were throwaway preview tooling.

# KDE

Before:

![KDE before](Theme%20previews%202026-10-23/kde-before.png)

After:

![KDE after](Theme%20previews%202026-10-23/kde-after.png)

# MATE

Before:

![MATE before](Theme%20previews%202026-10-23/mate-before.png)

After:

![MATE after](Theme%20previews%202026-10-23/mate-after.png)

# Verification and scope

Started both current test images with the existing image-test sandbox/security configuration. Opened their native file managers and terminals, applied settings through KDE tools and MATE GSettings, and visually inspected the captured screenshots. Fresh baseline containers supplied the original appearance. Theme packages came from Debian trixie (`breeze-gtk-theme`, `arc-theme`, `papirus-icon-theme`, `fonts-noto-core`). The app shortcuts reference their installed desktop entries.

This is a native desktop visual check. Flatpak application theme extensions, LibreOffice appearance, performance effects, settings persistence and default migration for existing homes were not validated as part of the preview. Those need checks if this direction becomes the image default. Existing architecture/specification pages remain accurate because the shipped behavior is unchanged; [[Testing/Desktop variants 2026-10-23|Desktop variants testing — 23 October 2026]] records the image validation separately. No production-code tests or image rebuilds were needed for these disposable previews.

The desktop feature and this preview report were transferred to the managed `desktop-variants/container-computer` worktree on `codex/desktop-variants`; the original checkout was returned to `main` with unrelated work preserved. File checksums, screenshot dimensions, Markdown/wiki links, Python/shell syntax and Bake configuration were checked after transfer. SilverBullet's registered space still points to the original checkout, so this worktree report was not indexed/rendered there.

# Selection — 23 October 2026

The user selected **KDE before** and **MATE after**. The screenshots above remain the historical comparison; only MATE's selected appearance is applied to image defaults. The prototype's imperative settings commands are replaced by native schema defaults and a panel layout, with stylesheet seeding that preserves user files. The MATE target rebuilt successfully. The selected-default suite passed 17 checks covering appearance, retained user preferences, default-browser routing, native/browser restoration, privilege boundaries and streamed rendering/resize. A subsequent appearance suite passed all 3 checks, including a real Files-launcher click and the corrected browser-launcher click. The default test failed with Menta before implementation; the browser-shortcut test also failed before correcting its absolute desktop-entry path.

The browser panel shortcut must use `/usr/local/share/applications/chromium.desktop`, not Debian's `/usr/share/applications/chromium.desktop`. The latter bypasses the shared-profile wrapper. The strengthened browser check compares the human-launched browser's direct CDP target ID with the agent proxy's target ID and checks that there is one visible Chromium window. KDE's source/configuration remains unchanged.

Actual selected defaults from the rebuilt image:

![MATE selected image defaults](Theme%20previews%202026-10-23/mate-selected-defaults.png)

Final shared-browser identity verification passed: the strengthened MATE panel test passed, and an independent native KDE panel click reached the same direct/agent browser target ID with one visible Chromium window. Both use `/workspace/.config/chromium-shared`. KDE's appearance was not changed.
