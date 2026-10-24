"""Image catalog and desktop settings on Vite's Docker-free mock API."""

import json
import os
import subprocess
import time

import pytest
import requests
from playwright.sync_api import expect, sync_playwright

from conftest import ROOT, free_port


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


@pytest.mark.parametrize("width", [390, 1280])
def test_settings_reattaches_visible_image_pull_progress(mock_ui, width):
    """A settings download stays visible after reload and switching tabs."""
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/alice?tab=settings")
        page.set_viewport_size({"width": width, "height": 900})
        desktops = page.request.get(mock_ui + "/api/desktops").json()
        alice = next(c for c in desktops if c["name"] == "alice")
        alice.update(state="upgrading", job="settings-download")
        page.route("**/api/desktops", lambda route: route.fulfill(json=desktops))
        # Control only the job transport, keeping its connection open until
        # completion. The real LiveProvider owns/replays the received state.
        page.add_init_script("""(() => {
            const NativeEventSource = window.EventSource;
            window.testJobs = [];
            window.EventSource = class extends NativeEventSource {
                constructor(url) {
                    if (!url.includes('/api/jobs/')) return new NativeEventSource(url);
                    return window.testJobs[window.testJobs.push({close() {}}) - 1];
                }
            };
        })();""")
        page.reload()
        page.wait_for_function("window.testJobs.length === 1")

        def update(status, message, progress):
            page.evaluate("j => window.testJobs[0].onmessage({data: JSON.stringify(j)})",
                          {"status": status, "message": message, "progress": progress, "desktop": "alice"})

        update("running", "Pulling image, 120 of 820 MB", 0.146)
        panel = page.get_by_role("region", name="Desktop operation", exact=True)
        expect(panel).to_be_visible()
        expect(panel).to_contain_text("120 of 820 MB")
        expect(panel).to_contain_text("15%")
        assert panel.bounding_box()["y"] < page.get_by_role("radiogroup").first.bounding_box()["y"]
        assert panel.bounding_box()["y"] + panel.bounding_box()["height"] <= 900
        expect(page.get_by_role("radio").first).to_be_disabled()
        page.get_by_role("tab", name="Overview", exact=True).click()
        page.get_by_role("tab", name="Settings", exact=True).click()
        expect(panel).to_contain_text("120 of 820 MB")
        expect(page.get_by_role("button", name="Saving…")).to_be_disabled()
        update("running", "Preparing image", None)
        expect(panel).to_contain_text("Preparing image")
        expect(panel).not_to_contain_text("%")
        update("done", "Done", None)
        expect(panel).to_have_count(0)
        expect(page.get_by_role("radio").first).to_be_enabled()
        browser.close()


def test_settings_save_brings_pending_progress_into_view(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/alice?tab=settings")
        page.set_viewport_size({"width": 390, "height": 900})
        pending = []
        page.route("**/api/desktops/alice", lambda route: pending.append(route)
                   if route.request.method == "PATCH" else route.continue_())
        page.get_by_role("radio", name="Debian KDE Plasma", exact=False).check()
        page.locator('form button[type="submit"]').click()
        panel = page.get_by_role("region", name="Desktop operation", exact=True)
        expect(panel).to_contain_text("Starting")
        expect(panel).to_be_in_viewport()
        assert len(pending) == 1
        pending[0].abort()
        browser.close()


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


def test_loopback_ui_survives_browser_network_loss(mock_ui):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context()
        page = context.new_page()
        logins = []
        page.on("request", lambda request: logins.append(request) if
                request.method == "POST" and request.url.endswith("/api/login") else None)
        page.goto(mock_ui + "/login")
        # Documents must stay native to preserve the WebSocket network address
        # space. Exercise the application's API client while offline, then
        # restore the network for the full document load after authentication.
        context.set_offline(True)
        bootstrapped = page.evaluate("""async () => {
            const {api} = await import('/src/api.ts');
            await api.login('zef', 'x');
            return {me: await api.me(), desktops: await api.desktops()};
        }""")
        assert bootstrapped["me"]["username"] == "zef"
        assert any(desktop["name"] == "alice" for desktop in bootstrapped["desktops"])
        assert len(logins) == 1
        context.set_offline(False)
        page.goto(mock_ui + "/")
        expect(page.get_by_text("alice", exact=True).first).to_be_visible(timeout=10000)
        browser.close()


def test_desktop_list_ignores_failures_from_replaced_refreshes(mock_ui):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        # Drive reconnection without letting the fake API's event stream
        # create extra refreshes while requests are deliberately held open.
        page.add_init_script("""window.testStreams = [];
            const NativeWebSocket = window.WebSocket;
            window.WebSocket = class extends EventTarget {
                constructor(url, protocols) {
                    super();
                    if (new URL(url, location.href).pathname !== '/api/events') {
                        return new NativeWebSocket(url, protocols);
                    }
                    window.testStreams.push(this);
                }
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
        page.wait_for_function("typeof testStreams[0]?.onopen === 'function'")
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
