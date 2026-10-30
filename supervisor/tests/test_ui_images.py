"""Image catalog and desktop settings on Vite's Docker-free mock API."""

import os
import re
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
@pytest.mark.parametrize("outcome", ["done", "failed", "disconnected"])
def test_settings_reattaches_visible_image_pull_progress(mock_ui, width, outcome):
    """A settings download stays visible after reload and switching tabs."""
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/computers/alice?tab=settings")
        page.set_viewport_size({"width": width, "height": 900})
        desktops = page.request.get(mock_ui + "/api/computers").json()
        alice = next(c for c in desktops["computers"] if c["name"] == "alice")
        alice.update(state="upgrading", job="settings-download")
        page.route("**/api/computers", lambda route: route.fulfill(json=desktops))
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
        expect(page.get_by_role("region", name="Computer operation", exact=True)).to_contain_text("Starting")
        expect(page.get_by_role("radio").first).to_be_disabled()

        def update(status, message, progress):
            page.evaluate("j => window.testJobs[0].onmessage({data: JSON.stringify(j)})",
                          {"status": status, "message": message, "progress": progress, "computer": "alice"})

        update("running", "Pulling image, 120 of 820 MB", 0.146)
        panel = page.get_by_role("region", name="Computer operation", exact=True)
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
        if outcome == "disconnected":
            page.evaluate("window.testJobs[0].onerror()")
            expect(page.get_by_text("Lost the connection to the job", exact=True)).to_be_visible()
        else:
            update(outcome, "Download failed" if outcome == "failed" else "Done", None)
            if outcome == "failed":
                expect(page.get_by_text("Download failed", exact=True)).to_be_visible()
        expect(panel).to_have_count(0)
        expect(page.get_by_role("radio").first).to_be_enabled()
        browser.close()


def test_settings_save_brings_pending_progress_into_view(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/computers/alice?tab=settings")
        page.set_viewport_size({"width": 390, "height": 900})
        pending = []
        page.route("**/api/computers/alice", lambda route: pending.append(route)
                   if route.request.method == "PATCH" else route.continue_())
        page.get_by_role("radio", name="Python lab", exact=False).check()
        page.locator('form button[type="submit"]').click()
        panel = page.get_by_role("region", name="Computer operation", exact=True)
        expect(panel).to_contain_text("Starting")
        expect(panel).to_be_in_viewport()
        assert len(pending) == 1
        pending[0].abort()
        browser.close()


@pytest.mark.parametrize("event_before_snapshot", [True, False])
def test_desktop_list_preserves_events_during_initial_load(mock_ui, event_before_snapshot):
    """A stale initial or reconnect snapshot must not erase a terminal event."""
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
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
        pending = []
        page.route("**/api/computers", lambda route: pending.append(route))
        page.goto(mock_ui + "/login")
        page.fill("#u", "zef")
        page.fill("#p", "x")
        page.get_by_role("button", name="Sign in").click()
        expect(page.get_by_text("Loading…", exact=True)).to_be_visible()
        page.wait_for_function("typeof testStreams[0]?.onmessage === 'function'")
        snapshot = page.request.get(mock_ui + "/api/computers").json()
        computer = next(c for c in snapshot["computers"] if c["name"] == "alice")
        computer["terminals"] = [
            {"name": f"session-{n}", "clients": 0, "command": "bash"} for n in range(5)
        ]
        # Save the updated event separately from the stale HTTP snapshot.
        page.evaluate("computer => window.updatedComputer = computer", computer)
        computer["terminals"] = []
        emit = "testStreams[0].onmessage({data: JSON.stringify({type: 'computer', data: updatedComputer})})"
        if event_before_snapshot:
            page.evaluate(emit)
            pending[0].fulfill(json=snapshot)
        else:
            pending[0].fulfill(json=snapshot)
            expect(page.get_by_text("alice", exact=True).first).to_be_visible()
            with page.expect_request("**/api/computers"):
                page.evaluate("testStreams[0].onopen(); testStreams[0].onopen()")
            page.evaluate(emit)
            expect(page.get_by_role("link", name="+2 more", exact=True)).to_be_visible()
            # A separate snapshot field proves the delayed body was applied.
            snapshot["conflicts"][0]["message"] = "Race snapshot applied"
            pending[1].fulfill(json=snapshot)
            expect(page.get_by_text("Race snapshot applied", exact=False)).to_be_visible()
        expect(page.get_by_role("link", name="+2 more", exact=True)).to_be_visible()
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

        page.route("**/api/computers", desktops)
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
            return {me: await api.me(), computers: (await api.computers()).computers};
        }""")
        assert bootstrapped["me"]["username"] == "zef"
        assert any(desktop["name"] == "alice" for desktop in bootstrapped["computers"])
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

        page.route("**/api/computers", desktops)
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

        with page.expect_request("**/api/computers"):
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



@pytest.mark.parametrize("width", [390, 1280])
def test_tile_screen_opens_overview_and_button_opens_desktop(mock_ui, width):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/")
        page.set_viewport_size({"width": width, "height": 900})
        page.get_by_title("alice's overview").click()
        page.wait_for_url(mock_ui + "/computers/alice")
        page.go_back()
        page.get_by_title("Open alice's desktop").click()
        page.wait_for_url(mock_ui + "/alice/")
        browser.close()


def test_volumes_page_shows_size_and_tiles_link_volumes(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/volumes")
        card = page.locator("#volume-photos")
        expect(card.get_by_label("Size")).to_contain_text("19 GB")
        expect(card.get_by_label("Size")).to_contain_text("212 GB free of 460 GB")
        users = card.get_by_role("list", name="Computers using photos")
        expect(users.get_by_role("link", name=re.compile(r"\balice.*share.photos.*Read & write"))).to_have_attribute("href", "/computers/alice?tab=settings&section=volumes")
        expect(users.get_by_role("link", name=re.compile(r"\bresearch.*mnt.photos.*Read only"))).to_be_visible()
        page.goto(mock_ui + "/")
        page.get_by_role("article").filter(has_text="alice").get_by_label("Volumes").get_by_role("link", name="photos").click()
        page.wait_for_url(mock_ui + "/volumes#volume-photos")
        browser.close()


@pytest.mark.parametrize("width", [390, 1280])
def test_settings_sections_keep_unsaved_changes(mock_ui, width):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/computers/alice?tab=settings")
        page.set_viewport_size({"width": width, "height": 900})
        nav = page.get_by_role("navigation", name="Settings sections")
        expect(nav.get_by_role("button", name="General")).to_have_attribute("aria-current", "page")
        nav.get_by_role("button", name="Volumes").click()
        page.wait_for_url("**/computers/alice?tab=settings&section=volumes")
        page.get_by_label("Path in the computer").fill("/srv/photos")
        nav.get_by_role("button", name="General").click()
        expect(nav.get_by_role("button", name="Volumes").get_by_title("Unsaved changes")).to_be_visible()
        nav.get_by_role("button", name="Volumes").click()
        expect(page.get_by_label("Path in the computer")).to_have_value("/srv/photos")
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        browser.close()


def test_computer_bar_shares(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/alice/")
        bar = page.locator("[data-computer-bar]")
        bar.get_by_role("button", name="Share", exact=True).click()
        dialog = page.get_by_role("dialog").filter(has=page.get_by_role("heading", name="Share alice"))
        dialog.get_by_label("Name").fill("guest")
        dialog.get_by_role("button", name="Share", exact=True).click()
        link = page.get_by_role("dialog").filter(has=page.get_by_role("heading", name="alice is shared with guest"))
        expect(link.get_by_role("button", name="Copy link")).to_be_visible()
        browser.close()


def test_viewers_panel_revokes_a_share(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/alice/")
        toggle = page.locator("[data-viewers-toggle]")
        toggle.click()
        panel = page.locator("h3", has_text="Viewing").locator("..")
        expect(panel).to_be_visible()
        # The panel opens under its button, at the bar's right.
        box, at = panel.bounding_box(), toggle.bounding_box()
        assert box["x"] + box["width"] >= at["x"] + at["width"] - 20, (box, at)
        # anna has full access through a grant, not a share: nothing to revoke.
        expect(panel.get_by_role("button", name="Revoke anna")).to_have_count(0)
        panel.get_by_role("button", name="Revoke sanne").click()
        panel.get_by_role("button", name="Revoke now").click()
        expect(page.get_by_text("Revoked sanne")).to_be_visible()
        browser.close()


def test_access_tab_has_accounts_and_shares(mock_ui):
    with sync_playwright() as p:
        # An old Sharing link opens Access, which now holds both.
        browser, page = mock_page(p, mock_ui, "/computers/alice?tab=sharing")
        expect(page.get_by_role("heading", name="Accounts")).to_be_visible()
        expect(page.get_by_role("heading", name="Shares")).to_be_visible()
        expect(page.get_by_role("button", name="Share", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="Sharing", exact=True)).to_have_count(0)
        expect(page.get_by_role("tab", name="Sharing", exact=True)).to_have_count(0)
        browser.close()
