"""The desktop comes back after a container restart: browser tabs, terminals, windows.
(That a new desktop opens nothing: test_desktop.test_nothing_opens_at_start.)"""

import time
from sandbox import DESKTOP, PANELS

TERMINAL = {"xfce": "xfce4-terminal", "mate": "mate-terminal"}[DESKTOP]

import pytest

from sandbox import docker, wait_until


def restart_gracefully(sandbox):
    """A restart that lets the session save itself (docker stop's grace period)."""
    docker("restart", "-t", "30", sandbox.name)
    sandbox.wait_ready()


@pytest.mark.serial  # flaky under load: Chromium sometimes starts without its session
def test_chromium_tabs_restored(sandbox):
    sandbox.exec("echo '<title>restore-probe</title><h1>still here</h1>' > ~/probe.html")
    sandbox.exec("curl -fs --max-time 60 -X PUT 'http://127.0.0.1:9221/json/new?file:///workspace/probe.html'")
    # Chromium writes its session a few seconds after a change: wait for the tab in it.
    saved = "grep -qs probe.html ~/.config/chromium-shared/Default/Sessions/*"
    assert wait_until(lambda: sandbox.exec(saved, check=False).returncode == 0, 30, 0.5), "the tab never reached the session"
    restart_gracefully(sandbox)
    # The session brings it back by itself (before any CDP call here would start it).
    assert wait_until(lambda: sandbox.exec("pgrep -f '[c]hromium-shared'", check=False).returncode == 0, 30, 0.5), \
        "Chromium didn't come back"
    titles = lambda: [t["title"] for t in sandbox.cdp_json("/json/list", timeout=60)]  # noqa: E731
    assert wait_until(lambda: "restore-probe" in titles(), 30, 1), titles()
    time.sleep(2)  # a second window would be up by now
    windows = sandbox.exec("xdotool search --onlyvisible --class chromium | wc -l").stdout.strip()
    assert windows == "1", f"{windows} Chromium windows after restore"


def terminal_cwds(sandbox):
    out = sandbox.exec("tmux -S /run/workspace/deskyard-terminal/tmux.sock list-panes -a -F '#{pane_current_path}'", check=False).stdout
    return sorted(out.split())


def test_terminals_restored_with_their_folders(sandbox, artifacts_dir):
    # Like a person opening it from the panel: inside the desktop session, so the
    # session manager knows and authenticates it.
    sandbox.exec(f"mkdir -p ~/project && export $(tr '\\0' '\\n' < /proc/$(pgrep -o -x {PANELS[DESKTOP]})/environ | grep -E '^(SESSION_MANAGER|ICEAUTHORITY)=')"
                 f" && setsid -f {TERMINAL} --working-directory=/workspace/project >/dev/null 2>&1")
    before = wait_until(lambda: terminal_cwds(sandbox) if "/workspace/project" in terminal_cwds(sandbox) else None, 15)
    assert before, terminal_cwds(sandbox)
    time.sleep(2)  # the session manager knows the terminal
    restart_gracefully(sandbox)
    assert wait_until(lambda: terminal_cwds(sandbox) == before, 30, 1), (before, terminal_cwds(sandbox))
    (artifacts_dir / "after-restart.png").write_bytes(sandbox.exec_bytes("deskyard screenshot"))
