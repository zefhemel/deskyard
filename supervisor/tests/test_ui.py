"""The web UI in a real browser, against the real supervisor and a real desktop."""

import json
import os
import subprocess
import time

import pytest
import requests

from playwright.sync_api import expect, sync_playwright

from conftest import ADMIN_PASSWORD, IMAGE, ROOT, free_port
from test_cli import Forward, cli, forwards, local_http, remote  # noqa: F401  (fixtures)
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


def mock_page(p, base, path):
    browser = p.chromium.launch()
    page = browser.new_context(viewport={"width": 1280, "height": 900}).new_page()
    page.goto(base + "/login")
    page.fill("#u", "zef")
    page.fill("#p", "x")
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    page.goto(base + path)
    return browser, page


def test_image_from_desktop_dialog(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/alice")
        page.get_by_role("button", name="More actions").click()
        page.get_by_role("menuitem", name="New image from this desktop…").click()
        dialog = page.get_by_role("dialog")
        expect(dialog.get_by_text("New image from alice")).to_be_visible()
        settings = dialog.get_by_role("checkbox", name="App settings")
        expect(settings).to_be_checked(timeout=10000)
        # Ticked, with its secret carved out: partly ticked.
        assert settings.evaluate("e => e.indeterminate")
        assert not dialog.get_by_role("checkbox", name="Installed tools").evaluate("e => e.indeterminate")
        secret = dialog.get_by_role("checkbox", name="~/.config/gh/hosts.yml")
        expect(secret).not_to_be_checked()
        expect(dialog.get_by_role("checkbox", name="Other files")).not_to_be_checked()
        expect(dialog.get_by_text("org.gnome.Builder", exact=False)).to_be_visible()
        # Running: offers to stop it first.
        expect(dialog.get_by_role("button", name="Stop it first")).to_be_visible()

        # Over 5 GB: a warning (no limit).
        expect(dialog.get_by_text("more than 5 GB", exact=False)).to_have_count(0)
        dialog.get_by_role("checkbox", name="Other files").check()
        expect(dialog.get_by_text("more than 5 GB", exact=False)).to_be_visible()
        dialog.get_by_role("checkbox", name="Other files").uncheck()

        # Ticking a secret marks the image.
        expect(dialog.get_by_text("This image will contain secrets")).to_have_count(0)
        dialog.get_by_role("checkbox", name="~/.codex/auth.json").check()
        expect(dialog.get_by_text("This image will contain secrets")).to_be_visible()

        with page.expect_request(lambda r: r.method == "POST" and r.url.endswith("/api/desktops/alice/image")) as sent:
            dialog.get_by_role("button", name="Build", exact=True).click()
        body = json.loads(sent.value.post_data)
        assert body == {"name": "alice image", "slug": "alice-image", "groups": ["tools", "flatpak", "settings", "dotfiles", "browser"],
                        "include": [".codex/auth.json"]}, body
        expect(page.get_by_role("dialog").get_by_text("Building alice image")).to_be_visible()
        browser.close()


def test_access_to_secret_image_asks_first(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/work?tab=access")
        page.get_by_role("checkbox", name="sam").check()
        requests_sent = []
        page.on("request", lambda r: requests_sent.append(r) if r.method == "PUT" and r.url.endswith("/access") else None)
        page.get_by_role("button", name="Save").click()
        dialog = page.get_by_role("dialog")
        expect(dialog.get_by_text("Give sam access to work?")).to_be_visible()
        expect(dialog.get_by_text("Work, signed in")).to_be_visible()
        dialog.get_by_role("button", name="Cancel").click()
        expect(page.get_by_role("dialog")).to_have_count(0)
        assert requests_sent == []
        page.get_by_role("button", name="Save").click()
        with page.expect_request(lambda r: r.method == "PUT" and r.url.endswith("/access")) as sent:
            page.get_by_role("dialog").get_by_role("button", name="Give access").click()
        assert json.loads(sent.value.post_data) == {"usernames": ["sam"]}
        browser.close()


def test_new_desktop_on_secret_image_asks_first(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/")
        page.get_by_role("button", name="New desktop").first.click()
        dialog = page.get_by_role("dialog")
        dialog.locator("#nc-name").fill("signed-in")
        expect(dialog.get_by_text("Its desktop will be at", exact=False)).to_be_visible()
        dialog.get_by_role("radio", name="Work, signed in", exact=False).check()
        dialog.get_by_role("checkbox", name="anna").check()
        posts = []
        page.on("request", lambda r: posts.append(r) if r.method == "POST" and r.url.endswith("/api/desktops") else None)
        dialog.get_by_role("button", name="Create desktop").click()
        confirm = page.get_by_role("dialog").filter(has_text="Give anna access to signed-in?")
        expect(confirm).to_be_visible()
        expect(confirm.get_by_text("Work, signed in")).to_be_visible()
        confirm.get_by_role("button", name="Cancel").click()
        expect(confirm).to_have_count(0)
        assert posts == []
        dialog.get_by_role("button", name="Create desktop").click()
        with page.expect_request(lambda r: r.method == "POST" and r.url.endswith("/api/desktops")) as sent:
            page.get_by_role("dialog").filter(has_text="Give anna access").get_by_role("button", name="Give access").click()
        assert json.loads(sent.value.post_data)["access"] == ["anna"]
        browser.close()


def test_account_given_secret_desktop_asks_first(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/accounts")
        page.get_by_role("row").filter(has_text="ci").get_by_role("button", name="Edit").click()
        dialog = page.get_by_role("dialog")
        dialog.get_by_role("checkbox", name="work").check()
        dialog.get_by_role("button", name="Save").click()
        confirm = page.get_by_role("dialog").filter(has_text="Give ci access to work?")
        expect(confirm).to_be_visible()
        with page.expect_request(lambda r: r.method == "PATCH" and r.url.endswith("/api/accounts/ci")) as sent:
            confirm.get_by_role("button", name="Give access").click()
        assert json.loads(sent.value.post_data)["desktops"] == ["work"]
        browser.close()


def test_upgrade_onto_secret_image_asks_first(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/work")
        before = page.request.get(mock_ui + "/api/desktops/work").json()["access"]
        set_access = lambda who: page.request.put(mock_ui + "/api/desktops/work/access", data={"usernames": who})  # noqa: E731
        try:
            # Only admins: a plain upgrade.
            set_access([])
            page.reload()
            page.get_by_role("button", name="Upgrade…").click()
            dialog = page.get_by_role("dialog").filter(has_text="Upgrade work?")
            expect(dialog.get_by_role("button", name="Upgrade work")).to_be_visible()
            expect(dialog.get_by_text("contains secrets", exact=False)).to_have_count(0)
            dialog.get_by_role("button", name="Cancel").click()

            set_access(["sam"])
            page.reload()
            page.get_by_role("button", name="Upgrade…").click()
            dialog = page.get_by_role("dialog").filter(has_text="Upgrade work?")
            expect(dialog.get_by_text("contains secrets", exact=False)).to_be_visible()
            expect(dialog.get_by_text("sam can use those sign-ins")).to_be_visible()
            with page.expect_request(lambda r: r.method == "POST" and r.url.endswith("/api/desktops/work/upgrade")) as sent:
                dialog.get_by_role("button", name="Upgrade work").click()
            assert json.loads(sent.value.post_data) == {"confirmSecrets": True}
        finally:
            set_access(before)
        browser.close()


def test_image_change_onto_secret_image_asks_first(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/alice?tab=settings")
        page.get_by_role("radio", name="Work, signed in", exact=False).check()
        # Seen, not sent: alice stays on her image for the other tests.
        page.route("**/api/desktops/alice", lambda route: route.abort() if route.request.method == "PATCH" else route.continue_())
        patches = []
        page.on("request", lambda r: patches.append(r) if r.method == "PATCH" else None)
        page.get_by_role("button", name="Save").click()
        confirm = page.get_by_role("dialog").filter(has_text="Move alice onto Work, signed in?")
        expect(confirm).to_be_visible()
        expect(confirm.get_by_text("anna can use those sign-ins")).to_be_visible()
        confirm.get_by_role("button", name="Cancel").click()
        expect(confirm).to_have_count(0)
        assert patches == []
        page.get_by_role("button", name="Save").click()
        with page.expect_request(lambda r: r.method == "PATCH" and r.url.endswith("/api/desktops/alice")) as sent:
            page.get_by_role("dialog").get_by_role("button", name="Move alice").click()
        body = json.loads(sent.value.post_data)
        assert body["confirmSecrets"] is True and body["image"] == "deskyard-custom/work-signed-in:latest", body
        browser.close()


def test_image_from_desktop_saves_a_recipe_on_top(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/research")
        page.get_by_role("button", name="More actions").click()
        page.get_by_role("menuitem", name="New image from this desktop…").click()
        dialog = page.get_by_role("dialog")
        expect(dialog.get_by_role("checkbox", name="App settings")).to_be_checked(timeout=10000)
        dialog.get_by_role("checkbox", name="Also save a recipe on the new image").check()
        expect(dialog.get_by_text("deskyard-custom/research-image-tools:latest")).to_be_visible()
        with page.expect_request(lambda r: r.method == "POST" and r.url.endswith("/api/desktops/research/image")) as sent:
            dialog.get_by_role("button", name="Build", exact=True).click()
        assert json.loads(sent.value.post_data)["recipe"] == {"slug": "research-image-tools", "name": "research image tools"}
        page.goto(mock_ui + "/images")
        expect(page.get_by_role("row").filter(has_text="research image tools").get_by_role("button", name="Edit and build")).to_be_visible()
        browser.close()


def test_forwards_shown(cli, remote, forwards, supervisor, name):  # noqa: F811
    p = free_port()
    f = forwards(cli, remote, str(p))
    f.wait_for(f"in {p} {p}")
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        chip = page.get_by_role("button", name="Viewers and port mappings")
        expect(chip).to_be_visible(timeout=30000)
        chip.click()
        expect(page.get_by_text(f"cli test: {p} in")).to_be_visible()

        page.goto(f"{supervisor.base}/desktops/{name}")
        expect(page.get_by_text(f"cli test: {p} in")).to_be_visible(timeout=15000)

        page.goto(f"{supervisor.base}/{name}/")
        expect(chip).to_be_visible(timeout=30000)
        assert f.stop() == 0
        # The button stays for the viewers (this page is one); the mapping count goes.
        expect(chip.get_by_text("⇄")).to_have_count(0, timeout=3000)
        browser.close()


def test_agent_presence_and_summary(cli, remote, supervisor, name):  # noqa: F811
    remote("-d", name, "click", "100", "100")
    remote("-d", name, "exec", "--", "true")
    title = "cli test · admin's agent"
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        chip = page.get_by_role("button", name="Viewers and port mappings")
        expect(chip).to_be_visible(timeout=30000)
        expect(chip.locator("[data-agent]")).to_have_attribute("title", title)
        chip.click()
        expect(page.get_by_role("heading", name="Agents")).to_be_visible()
        row = page.get_by_role("button", name=title)
        expect(row).to_be_visible()
        row.hover()
        expect(page.get_by_text("Recently")).to_be_visible()
        expect(page.get_by_text("clicked", exact=True)).to_be_visible()
        expect(page.get_by_text("true · exit 0")).to_be_visible()

        page.goto(f"{supervisor.base}/desktops/{name}")
        expect(page.locator("[data-agent]").first).to_be_visible(timeout=15000)

        page.goto(f"{supervisor.base}/")
        expect(page.locator("[data-agent]").first).to_be_visible(timeout=15000)
        browser.close()


# toViewport (desktop/screen-geometry.ts) worked out in the page from the frame's canvas.
TO_VIEWPORT = """([x, y]) => {
    const frame = document.querySelector("iframe");
    const canvas = frame.contentDocument.querySelector('canvas[tabindex="-1"]');
    const f = frame.getBoundingClientRect(), c = canvas.getBoundingClientRect();
    return {left: f.left + frame.clientLeft + c.left + x * c.width / canvas.width,
            top: f.top + frame.clientTop + c.top + y * c.height / canvas.height};
}"""


def test_overlay_marks(cli, remote, supervisor, name):  # noqa: F811
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        expect(page.get_by_text("Connected")).to_be_visible(timeout=30000)
        page.frame_locator("iframe").locator('canvas[tabindex="-1"]').wait_for(state="attached", timeout=30000)
        time.sleep(1)  # the screen-events stream is open

        remote("-d", name, "click", "200", "150")
        click = page.locator('[data-mark="click"]')
        expect(click).to_be_visible(timeout=5000)
        expect(click).to_contain_text("cli test")
        box = click.bounding_box()
        want = page.evaluate(TO_VIEWPORT, [200, 150])
        assert abs(box["x"] + box["width"] / 2 - want["left"]) <= 3, (box, want)
        assert abs(box["y"] + box["height"] / 2 - want["top"]) <= 3, (box, want)

        remote("-d", name, "type", "secret-text")
        typing = page.locator('[data-mark="typing"]')
        expect(typing).to_have_text("cli test is typing…", timeout=5000)
        expect(typing).to_have_count(0, timeout=3000)

        remote("-d", name, "key", "ctrl+l")
        expect(page.locator('[data-mark="key"]')).to_have_text("⌃L", timeout=5000)

        marks = page.locator("[data-mark]")
        expect(marks).to_have_count(0, timeout=5000)
        remote("-d", name, "move", "10", "10")
        time.sleep(1.5)
        expect(marks).to_have_count(0)

        assert "secret-text" not in page.content()
        browser.close()
