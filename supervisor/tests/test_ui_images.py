"""The Images page and the image dialogs: one build against the real
supervisor, the rest on Vite's dev server with its fake API."""

import json
import os
import subprocess
import time

import pytest
import requests
from playwright.sync_api import expect, sync_playwright

from conftest import IMAGE, ROOT, free_port
from helpers import signed_in


@pytest.mark.build
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


def test_desktop_list_recovers_after_failed_first_load(mock_ui):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        attempts = []

        def desktops(route):
            attempts.append(route.request.url)
            if len(attempts) == 1:
                route.abort("failed")
            else:
                route.continue_()

        page.route("**/api/desktops", desktops)
        page.goto(mock_ui + "/login")
        page.fill("#u", "zef")
        page.fill("#p", "x")
        page.get_by_role("button", name="Sign in").click()
        expect(page.get_by_text("alice", exact=True).first).to_be_visible(timeout=10000)
        assert len(attempts) >= 2
        browser.close()


def test_desktop_list_ignores_failures_from_replaced_refreshes(mock_ui):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        # Drive reconnection without letting the fake API's event stream
        # create extra refreshes while requests are deliberately held open.
        page.add_init_script("""window.testStreams = [];
            window.EventSource = class extends EventTarget {
                constructor() { super(); window.testStreams.push(this); }
                close() {}
            };""")
        attempts, pending = [], []

        def desktops(route):
            attempts.append(route.request.url)
            if len(attempts) in (1, 3):
                pending.append(route)
            else:
                route.continue_()

        page.route("**/api/desktops", desktops)
        page.goto(mock_ui + "/login")
        page.fill("#u", "zef")
        page.fill("#p", "x")
        page.get_by_role("button", name="Sign in").click()
        expect(page.get_by_text("Loading…", exact=True)).to_be_visible()
        page.evaluate("testStreams[0].onopen(); testStreams[0].onopen()")
        expect(page.get_by_text("alice", exact=True).first).to_be_visible()
        pending[0].abort("failed")
        page.wait_for_timeout(1500)  # past the first retry deadline
        assert len(attempts) == 2

        with page.expect_request("**/api/desktops"):
            page.evaluate("testStreams[0].onopen()")
        # Use client-side navigation so the provider unmounts while its
        # request is still alive in this document.
        page.evaluate("""() => {
            const a = document.createElement('a');
            a.href = '/login'; document.body.append(a); a.click(); a.remove();
        }""")
        expect(page.get_by_role("button", name="Sign in")).to_be_visible()
        pending[1].abort("failed")
        page.wait_for_timeout(1500)
        assert len(attempts) == 3
        browser.close()


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
