"""The desktop comes back after a container restart: browser tabs, terminals, windows."""

import time

import requests


def restart_gracefully(sandbox):
    from sandbox import docker
    docker("restart", "-t", "30", sandbox.name)
    sandbox.wait_ready()
    time.sleep(8)  # session restore and autostart settle


def test_chromium_tabs_restored(sandbox):
    sandbox.exec("echo '<title>restore-probe</title><h1>still here</h1>' > ~/probe.html")
    requests.put(sandbox.cdp_url + "/json/new?file:///workspace/probe.html", timeout=10)
    time.sleep(3)
    restart_gracefully(sandbox)
    titles = [t["title"] for t in requests.get(sandbox.cdp_url + "/json/list", timeout=10).json()]
    assert "restore-probe" in titles, titles
    windows = sandbox.exec("xdotool search --onlyvisible --class chromium | wc -l").stdout.strip()
    assert windows == "1", f"{windows} Chromium windows after restore"


def terminal_cwds(sandbox):
    out = sandbox.exec("for t in $(pgrep -x xfce4-terminal); do for b in $(pgrep -P $t -x bash); do readlink /proc/$b/cwd; done; done",
                       check=False).stdout
    return sorted(out.split())


def test_terminals_restored_with_their_folders(sandbox, artifacts_dir):
    sandbox.exec("mkdir -p ~/project && setsid -f xfce4-terminal --working-directory=/workspace/project >/dev/null 2>&1")
    time.sleep(3)
    before = terminal_cwds(sandbox)
    assert "/workspace/project" in before, before
    restart_gracefully(sandbox)
    after = terminal_cwds(sandbox)
    assert after == before, (before, after)  # same terminals, no extra autostart one
    (artifacts_dir / "after-restart.png").write_bytes(sandbox.exec_bytes("computer screenshot"))


def test_fresh_desktop_still_gets_a_terminal(sandbox):
    assert terminal_cwds(sandbox) == ["/workspace"]
