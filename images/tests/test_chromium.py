import time

import requests
from playwright.sync_api import sync_playwright

from sandbox import TOKEN_HEADER


def test_chromium_not_started_with_the_desktop(sandbox):
    assert not chromium_running(sandbox)


def test_stop_shared_browser_preserves_other_profiles(sandbox):
    sandbox.exec("setsid -f /usr/bin/chromium --user-data-dir=/tmp/other-profile"
                 " --no-first-run --no-default-browser-check --password-store=basic"
                 " >/tmp/other-chromium.log 2>&1")
    sandbox.exec("for i in $(seq 60); do pgrep -x chromium && exit 0; sleep 0.5; done; exit 1")
    other_pid = sandbox.exec("pgrep -o -x chromium").stdout.strip()
    open_chromium(sandbox)
    sandbox.exec("stop-chromium")
    time.sleep(2)
    state = sandbox.exec(f"ps -p {other_pid} -o stat=", check=False).stdout.strip()
    assert state and not state.startswith("Z"), state


def test_cdp_version_through_caddy(sandbox):
    info = sandbox.cdp_json(timeout=60)
    assert info["Browser"].startswith("Chrome/")


def test_cdp_page_on_shared_display(sandbox, artifacts_dir):
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_ws(), headers=TOKEN_HEADER)
        page = browser.contexts[0].new_page()
        page.goto("data:text/html,<title>cdp-probe-123</title><h1 style='font-size:80px'>Hello from CDP</h1>")
        page.bring_to_front()
        time.sleep(1)
        sandbox.exec("xdotool search --name cdp-probe-123")
        (artifacts_dir / "chromium.png").write_bytes(
            sandbox.exec_bytes("maim"))
        browser.close()


def cdp_up(sandbox) -> bool:
    try:
        return sandbox.get("/_cdp/json/version", timeout=2).ok
    except requests.RequestException:
        return False


def open_chromium(sandbox):
    """Start Chromium the way a person or agent would: by connecting to its CDP port."""
    sandbox.exec("curl -fs --max-time 60 -o /dev/null http://127.0.0.1:9221/json/version")
    sandbox.exec("for i in $(seq 60); do xdotool search --onlyvisible --class chromium && exit 0; sleep 0.5; done; exit 1")


def wait_cdp(sandbox, timeout=30) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if cdp_up(sandbox):
            return True
        time.sleep(1)
    return False


def close_chromium_like_a_person(sandbox, timeout=30):
    """Alt+F4 on the Chromium window (what the window's close button does), once the WM is up."""
    open_chromium(sandbox)
    time.sleep(2)  # a person doesn't close it within a second of it appearing
    deadline = time.time() + timeout
    while time.time() < deadline:
        if sandbox.exec("xdotool search --onlyvisible --class chromium", check=False).returncode != 0:
            return
        sandbox.exec("xdotool search --onlyvisible --class chromium windowactivate --sync"
                     " && xdotool getactivewindow && xdotool key alt+F4", check=False)
        time.sleep(2)
    raise AssertionError("could not close the Chromium window")


def chromium_running(sandbox) -> bool:
    return sandbox.exec("pgrep -f '[c]hromium-shared'", check=False).returncode == 0


def test_closed_chromium_stays_closed(sandbox):
    close_chromium_like_a_person(sandbox)
    time.sleep(25)  # longer than two healthcheck intervals: nothing may reopen it
    assert not chromium_running(sandbox)


def test_cdp_starts_chromium_on_demand_from_host(sandbox):
    close_chromium_like_a_person(sandbox)
    time.sleep(3)
    assert not chromium_running(sandbox)
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_ws(), headers=TOKEN_HEADER, timeout=60_000)
        page = browser.contexts[0].new_page()
        page.goto("data:text/html,<title>on-demand-probe</title>")
        sandbox.exec("xdotool search --onlyvisible --name on-demand-probe")
        browser.close()


def test_cdp_starts_chromium_on_demand_inside(sandbox):
    close_chromium_like_a_person(sandbox)
    time.sleep(3)
    out = sandbox.exec("curl -s --max-time 60 http://127.0.0.1:9221/json/version").stdout
    assert '"Browser"' in out


def test_parallel_requests_start_one_browser(sandbox):
    close_chromium_like_a_person(sandbox)
    time.sleep(3)
    sandbox.exec("for i in 1 2 3 4 5; do curl -s --max-time 60 -o /dev/null http://127.0.0.1:9221/json/version & done; wait")
    time.sleep(3)
    assert sandbox.exec("xdotool search --onlyvisible --class chromium | wc -l").stdout.strip() == "1"


def test_chromium_command_brings_it_back(sandbox):
    close_chromium_like_a_person(sandbox)
    time.sleep(3)
    assert not chromium_running(sandbox)
    sandbox.exec("setsid -f chromium >/dev/null 2>&1")
    assert wait_cdp(sandbox), "CDP did not come back after running `chromium`"


def test_recreate_on_same_volume(make_sandbox):
    a = make_sandbox()
    a.remove()
    make_sandbox()  # waits until CDP answers


def test_open_chromium_comes_back_after_restart(sandbox):
    open_chromium(sandbox)
    sandbox.restart()  # (no CDP calls after this: they would start it themselves)
    sandbox.exec("for i in $(seq 60); do pgrep -f '[c]hromium-shared' && exit 0; sleep 1; done; exit 1")


def test_closed_chromium_stays_closed_after_restart(sandbox):
    close_chromium_like_a_person(sandbox)
    time.sleep(3)
    sandbox.restart()
    time.sleep(10)
    assert not chromium_running(sandbox)
