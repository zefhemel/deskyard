"""The web UI in a real browser, against the real supervisor and a real desktop."""

import json
import os
import subprocess
import time

import pytest
import requests

from playwright.sync_api import expect, sync_playwright

from conftest import ADMIN_PASSWORD, IMAGE, ROOT, free_port
from test_custom_images import slug  # noqa: F401  (fixture: removes the recipe and its tags)


def signed_in(p, base, user="admin", password=ADMIN_PASSWORD, **ctx):
    browser = p.chromium.launch()
    page = browser.new_context(viewport={"width": 1280, "height": 860}, **ctx).new_page()
    page.goto(base + "/")
    page.wait_for_url("**/login**")
    page.fill("#u", user)
    page.fill("#p", password)
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    return browser, page


def test_sign_in_and_see_the_wall(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        expect(page.get_by_text(name, exact=True).first).to_be_visible(timeout=15000)
        expect(page.get_by_text("Deskyard").first).to_be_visible()
        page.screenshot(path=str(tmp_path / "wall.png"))
        browser.close()


def test_wrong_password_shows_an_error(supervisor):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        page.goto(supervisor.base + "/login")
        page.fill("#u", "admin")
        page.fill("#p", "wrong")
        page.get_by_role("button", name="Sign in").click()
        expect(page.get_by_text("wrong", exact=False).first).to_be_visible()
        assert "/login" in page.url
        browser.close()


def test_desktop_page_frames_the_desktop_without_kasm_controls(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, permissions=["clipboard-read", "clipboard-write"])
        page.goto(f"{supervisor.base}/{name}/")
        frame = page.frame_locator("iframe")
        frame.locator("canvas").first.wait_for(state="attached", timeout=30000)
        expect(page.get_by_text("Connected")).to_be_visible(timeout=30000)
        time.sleep(2)
        # KasmVNC's own control bar is hidden; ours is there.
        assert not frame.locator("#noVNC_control_bar").is_visible()
        expect(page.get_by_role("button", name="Clipboard")).to_be_visible()
        page.screenshot(path=str(tmp_path / "desktop.png"))

        # Page → desktop.
        page.get_by_role("button", name="Clipboard").click()
        page.locator("textarea").fill("from-the-page-42")
        page.get_by_role("button", name=f"Paste into {name}").click()
        deadline = time.time() + 15
        got = ""
        while time.time() < deadline:
            got = subprocess.run(["docker", "exec", "-u", "user", f"deskyard-{name}", "xclip", "-o", "-selection", "clipboard"],
                                 capture_output=True, text=True).stdout
            if got == "from-the-page-42":
                break
            time.sleep(0.5)
        assert got == "from-the-page-42"

        # Desktop → page.
        subprocess.run(["docker", "exec", "-u", "user", f"deskyard-{name}", "sh", "-c",
                        "printf from-the-desktop-7 | setsid xclip -selection clipboard"], check=True)
        if not page.get_by_text("Send to").is_visible():
            page.get_by_role("button", name="Clipboard").click()
        expect(page.get_by_text("from-the-desktop-7")).to_be_visible(timeout=15000)
        browser.close()


def test_stopped_desktop_page(supervisor, admin, desktop, name):
    admin.post(f"/api/desktops/{name}/stop")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        expect(page.get_by_text("is stopped")).to_be_visible(timeout=15000)
        page.get_by_role("button", name="Start").click()
        page.frame_locator("iframe").locator("canvas").first.wait_for(state="attached", timeout=60000)
        browser.close()


def test_recipe_dialog_builds(supervisor, admin, slug):
    """New image: fill the dialog, watch the build log, then Rebuild opens the log again (and Cancel stops it)."""
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.goto(supervisor.base + "/images")
        page.get_by_role("button", name="New image").click()
        page.fill("#rc-name", slug)
        page.select_option("#rc-base", IMAGE)
        expect(page.get_by_text("Don't put tokens here", exact=False)).to_be_visible()
        page.fill("#rc-commands", "true")
        page.get_by_role("button", name="Build", exact=True).click()

        log = page.get_by_role("dialog")
        expect(log.get_by_text("Step 1/", exact=False).first).to_be_visible(timeout=60000)
        expect(log.get_by_role("button", name="Cancel build")).to_be_visible()
        expect(log.get_by_text("is ready to use").first).to_be_visible(timeout=300000)
        log.get_by_role("button", name="Close").click()

        row = page.locator("tr", has_text=slug)
        expect(row.get_by_role("button", name="Rebuild")).to_be_visible(timeout=15000)
        row.get_by_role("button", name="Rebuild").click()
        log = page.get_by_role("dialog")
        expect(log.get_by_text("Step 1/", exact=False).first).to_be_visible(timeout=60000)
        log.get_by_role("button", name="Cancel build").click()
        expect(log.get_by_text("Build cancelled").first).to_be_visible(timeout=60000)
        browser.close()


@pytest.fixture(scope="module")
def mock_ui():
    """The web UI on Vite's dev server with its fake API (no Docker, no supervisor)."""
    port = free_port()
    env = {k: v for k, v in os.environ.items() if k != "DESKYARD_API"}
    proc = subprocess.Popen(["npm", "run", "dev", "--", "--port", str(port), "--strictPort", "--host", "127.0.0.1"],
                            cwd=ROOT / "web", env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port}"
    try:
        deadline = time.time() + 60
        while True:
            try:
                requests.get(base + "/healthz", timeout=1)
                break
            except requests.RequestException:
                assert time.time() < deadline and proc.poll() is None, "the dev server didn't start"
                time.sleep(0.3)
        yield base
    finally:
        proc.terminate()
        proc.wait(timeout=20)


def mock_new_image(p, base, name, commands=""):
    browser = p.chromium.launch()
    page = browser.new_context(viewport={"width": 1280, "height": 900}).new_page()
    page.goto(base + "/login")
    page.fill("#u", "zef")
    page.fill("#p", "x")
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    page.goto(base + "/images")
    page.get_by_role("button", name="New image").click()
    page.fill("#rc-name", name)
    if commands:
        page.fill("#rc-commands", commands)
    return browser, page


def test_recipe_dialog_sends_the_mise_tools(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_new_image(p, mock_ui, "Cow tools")
        page.fill("#rc-mise", "npm:cowsay\nnode@24\n")
        with page.expect_request(lambda r: r.method == "POST" and r.url.endswith("/api/recipes")) as sent:
            page.get_by_role("button", name="Build", exact=True).click()
        body = json.loads(sent.value.post_data)
        assert body["slug"] == "cow-tools"
        assert body["mise"] == ["npm:cowsay", "node@24"]
        browser.close()


def test_invalid_name_says_why(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_new_image(p, mock_ui, "x")
        expect(page.get_by_text("at least 2 letters or digits")).to_be_visible()
        expect(page.get_by_text("deskyard-custom/x:latest")).to_have_count(0)
        expect(page.get_by_role("button", name="Build", exact=True)).to_be_disabled()
        browser.close()


def test_build_log_survives_a_dropped_connection(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_new_image(p, mock_ui, "Drop test", commands="drop")
        page.get_by_role("button", name="Build", exact=True).click()
        log = page.get_by_role("dialog")
        expect(log.get_by_text("Step 1/3", exact=False).first).to_be_visible(timeout=10000)
        # The stream is cut mid-build: the build still shows as running, with Cancel.
        expect(log.get_by_text("Lost the connection", exact=False)).to_be_visible(timeout=10000)
        expect(log.get_by_role("button", name="Cancel build")).to_be_visible()
        assert log.get_by_text("Failed in").count() == 0
        # After the replay it finishes; the replayed log isn't doubled.
        expect(log.get_by_text("is ready to use")).to_be_visible(timeout=30000)
        assert log.get_by_text("Step 1/3 : FROM").count() == 1
        browser.close()
