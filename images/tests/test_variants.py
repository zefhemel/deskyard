"""Every desktop image must provide the same runtime and its own session."""

import time

from sandbox import DESKTOP, PANELS


def test_desktop_identity_and_readiness(sandbox):
    assert sandbox.exec("echo $WORKSPACE_DESKTOP").stdout.strip() == DESKTOP
    sandbox.exec("desktop-ready")
    panel = PANELS[DESKTOP]
    assert sandbox.exec(f"ps -C {panel} -o user= | sort -u").stdout.split() == ["user"]


def test_other_desktops_are_not_installed(sandbox):
    for name, panel in PANELS.items():
        if name != DESKTOP:
            assert sandbox.exec(f"command -v {panel}", check=False).returncode != 0


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
