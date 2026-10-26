"""The desktop session and the everyday apps and tools a casual user expects."""

import time

from playwright.sync_api import sync_playwright

from sandbox import PANEL, TOKEN_HEADER


def test_desktop_identity_and_readiness(sandbox):
    assert sandbox.exec("echo $WORKSPACE_DESKTOP").stdout.strip() == "xfce"
    sandbox.exec("desktop-ready")
    assert sandbox.exec(f"ps -C {PANEL} -o user= | sort -u").stdout.split() == ["user"]


def test_default_browser_opens_shared_chromium(sandbox):
    for mime in ("x-scheme-handler/http", "x-scheme-handler/https", "text/html"):
        assert sandbox.exec(f"xdg-mime query default {mime}").stdout.strip() == "chromium.desktop"
    sandbox.exec("echo '<title>default-browser-probe</title>' > ~/browser-probe.html"
                 " && setsid -f xdg-open file:///workspace/browser-probe.html >/tmp/xdg-open.log 2>&1")
    deadline = time.monotonic() + 30
    while sandbox.exec("xdotool search --onlyvisible --name default-browser-probe", check=False).returncode != 0:
        assert time.monotonic() < deadline, sandbox.exec("cat /tmp/xdg-open.log").stdout
        time.sleep(0.5)
    assert any(t["title"] == "default-browser-probe" for t in sandbox.cdp_json("/json/list", timeout=30))



CLI_BASICS = ("vi", "vim", "nano", "less", "file", "htop", "wget", "rsync", "jq", "zip", "unzip",
              "xz", "bzip2", "7z", "ping", "dig", "nc", "lsof")

# Which desktop file opens each kind of file.
DEFAULT_APPS = {"image/png": "org.xfce.ristretto.desktop", "image/jpeg": "org.xfce.ristretto.desktop",
                "text/plain": "org.xfce.mousepad.desktop", "application/pdf": "org.gnome.Evince.desktop",
                "application/zip": "xarchiver.desktop"}


def test_cli_basics_installed(sandbox):
    missing = sandbox.exec(f"for c in {' '.join(CLI_BASICS)}; do command -v $c >/dev/null || echo $c; done").stdout
    assert missing.split() == []


# Chromium plays video and audio; there is no separate media player.
MEDIA = ("video/mp4", "video/webm", "audio/mpeg", "audio/ogg")


def test_everyday_apps_open_their_files(sandbox):
    expected = DEFAULT_APPS | {mime: "chromium.desktop" for mime in MEDIA}
    defaults = {mime: sandbox.exec(f"xdg-mime query default {mime}").stdout.strip() for mime in expected}
    assert defaults == expected


def test_chromium_decodes_common_media(sandbox):
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_ws(), headers=TOKEN_HEADER)
        page = browser.contexts[0].new_page()
        support = page.evaluate("""() => Object.fromEntries([
            'video/mp4; codecs="avc1.42E01E, mp4a.40.2"', 'video/webm; codecs="vp9, opus"',
            'audio/mpeg', 'audio/ogg; codecs="vorbis"', 'audio/flac',
        ].map(t => [t, document.createElement('video').canPlayType(t)]))""")
        page.close()
    assert all(support.values()), support


def test_image_opens_in_viewer(sandbox):
    sandbox.exec("desktop-ready")
    sandbox.exec("maim ~/viewer-probe.png && setsid -f xdg-open ~/viewer-probe.png >/tmp/xdg-open.log 2>&1")
    deadline = time.monotonic() + 30
    while sandbox.exec("xdotool search --onlyvisible --name viewer-probe", check=False).returncode != 0:
        assert time.monotonic() < deadline, sandbox.exec("cat /tmp/xdg-open.log").stdout
        time.sleep(0.5)
    assert sandbox.exec("pgrep -x ristretto", check=False).returncode == 0


def test_files_move_to_trash(sandbox):
    """Thunar's Delete moves to the Trash (gvfs), through its own D-Bus API."""
    sandbox.exec("desktop-ready && echo bye > ~/trash-probe.txt"
                 " && dbus-send --session --print-reply --dest=org.xfce.FileManager /org/xfce/FileManager"
                 " org.xfce.Trash.MoveToTrash array:string:file:///workspace/trash-probe.txt string: string:")
    deadline = time.monotonic() + 15
    while sandbox.exec("test -f ~/.local/share/Trash/files/trash-probe.txt", check=False).returncode != 0:
        assert time.monotonic() < deadline, sandbox.exec("ls -la ~ ~/.local/share/Trash 2>&1", check=False).stdout
        time.sleep(0.5)
    assert sandbox.exec("test -e ~/trash-probe.txt", check=False).returncode != 0
    sandbox.exec("rm -rf ~/.local/share/Trash")


def test_arc_dark_look(sandbox):
    sandbox.exec("desktop-ready")
    assert sandbox.exec("xfconf-query -c xsettings -p /Net/ThemeName").stdout.strip() == "Arc-Dark"
    assert sandbox.exec("xfconf-query -c xsettings -p /Net/IconThemeName").stdout.strip() == "Papirus-Dark"
    assert sandbox.exec("xfconf-query -c xfwm4 -p /general/theme").stdout.strip() == "Arc-Dark"
    # The alternatives offered in Settings > Appearance.
    for theme in ("/usr/share/themes/Greybird", "/usr/share/themes/Greybird-dark", "/usr/share/icons/Papirus"):
        assert sandbox.exec(f"test -d {theme}", check=False).returncode == 0, theme


def test_emoji_and_office_fonts(sandbox):
    families = sandbox.exec("fc-list : family").stdout
    for family in ("Noto Color Emoji", "Liberation Sans", "Liberation Serif", "Noto Sans"):
        assert family in families, family
