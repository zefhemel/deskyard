"""The desktop comes back after a container restart: browser tabs, terminals, windows."""

import time



def restart_gracefully(sandbox):
    from sandbox import docker
    docker("restart", "-t", "30", sandbox.name)
    sandbox.wait_ready()
    time.sleep(8)  # session restore and autostart settle


def test_chromium_tabs_restored(sandbox):
    sandbox.exec("echo '<title>restore-probe</title><h1>still here</h1>' > ~/probe.html")
    sandbox.exec("curl -fs --max-time 60 -X PUT 'http://127.0.0.1:9221/json/new?file:///workspace/probe.html'")
    time.sleep(3)
    restart_gracefully(sandbox)
    titles = [t["title"] for t in sandbox.cdp_json("/json/list", timeout=60)]
    assert "restore-probe" in titles, titles
    windows = sandbox.exec("xdotool search --onlyvisible --class chromium | wc -l").stdout.strip()
    assert windows == "1", f"{windows} Chromium windows after restore"


def terminal_cwds(sandbox):
    out = sandbox.exec("for t in $(pgrep -x xfce4-terminal); do for b in $(pgrep -P $t -x bash); do readlink /proc/$b/cwd; done; done",
                       check=False).stdout
    return sorted(out.split())


def test_terminals_restored_with_their_folders(sandbox, artifacts_dir):
    # Like a person opening it from the panel: inside the desktop session, so the
    # session manager knows it (SESSION_MANAGER from the panel's environment).
    sandbox.exec("mkdir -p ~/project && export $(tr '\\0' '\\n' < /proc/$(pgrep -o -x xfce4-panel)/environ | grep ^SESSION_MANAGER=)"
                 " && setsid -f xfce4-terminal --working-directory=/workspace/project >/dev/null 2>&1")
    time.sleep(3)
    before = terminal_cwds(sandbox)
    assert "/workspace/project" in before, before
    restart_gracefully(sandbox)
    after = terminal_cwds(sandbox)
    assert after == before, (before, after)
    (artifacts_dir / "after-restart.png").write_bytes(sandbox.exec_bytes("deskyard screenshot"))


def test_fresh_desktop_opens_nothing(sandbox):
    assert terminal_cwds(sandbox) == []
