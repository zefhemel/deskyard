"""The manual memory action through the real UI, API and Docker engine."""

import uuid

import pytest
import requests
from playwright.sync_api import expect, sync_playwright

from helpers import make_account, make_token, signed_in


def test_reclaim_button_preserves_browser_state(admin, supervisor, desktop, name, tmp_path):
    support = admin.get("/api/host").json()["memoryReclaim"]
    assert "memory reclamation " in supervisor.logs()
    if not support["enabled"]:
        with sync_playwright() as pw:
            browser, page = signed_in(pw, supervisor.base)
            page.goto(f"{supervisor.base}/environments/{name}")
            expect(page.get_by_role("button", name="Reclaim memory", exact=True)).to_be_disabled(timeout=15000)
            expect(page.get_by_text("Unavailable:", exact=False)).to_be_visible()
            browser.close()
        assert admin.post(f"/api/environments/{name}/reclaim-memory").status_code == 501
        pytest.skip("boot probe disabled reclamation: " + support["reason"])
    assert "memory reclamation enabled" in supervisor.logs()
    token = make_token(admin, name)
    headers = {"Authorization": f"Bearer {token['token']}"}
    version = requests.get(admin.base + "/api/agent/v1/cdp/json/version", headers=headers, timeout=90).json()
    nonce = uuid.uuid4().hex
    with sync_playwright() as pw:
        remote = pw.chromium.connect_over_cdp(version["webSocketDebuggerUrl"], headers=headers)
        tab = remote.contexts[0].new_page()
        tab.goto("data:text/html,<title>memory-test</title><textarea></textarea>")
        tab.evaluate("value => window.memoryProbe = value", nonce)
        tab.locator("textarea").fill("unsaved state")
        remote.close()  # Disconnect; leave the browser and state alive.

        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/environments/{name}")
        button = page.get_by_role("button", name="Reclaim memory", exact=True)
        expect(button).to_be_visible(timeout=15000)
        with page.expect_response(lambda r: r.url.endswith("/reclaim-memory") and r.request.method == "POST", timeout=60000) as response:
            button.click()
        reply = response.value
        if reply.status == 501:
            expect(page.get_by_text(reply.json()["error"], exact=True)).to_be_visible()
            browser.close()
            pytest.skip("test engine does not support memory reclamation: " + reply.json()["error"])
        assert reply.status == 200, reply.text()
        result = reply.json()
        assert result["requestedBytes"] == 128 * 1024 * 1024
        assert result["beforeBytes"] > 0 and result["afterBytes"] > 0
        expect(page.get_by_text("resident memory", exact=False)).to_be_visible(timeout=15000)
        expect(button).to_be_enabled()
        page.screenshot(path=str(tmp_path / "reclaimed.png"))
        page.set_viewport_size({"width": 390, "height": 844})
        expect(button).to_be_visible()
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        page.screenshot(path=str(tmp_path / "reclaimed-mobile.png"))
        remote = pw.chromium.connect_over_cdp(version["webSocketDebuggerUrl"], headers=headers)
        tab = next(p for p in remote.contexts[0].pages if p.title() == "memory-test")
        assert tab.evaluate("window.memoryProbe") == nonce
        assert tab.locator("textarea").input_value() == "unsaved state"
        tab.locator("textarea").fill("still editable")
        assert tab.locator("textarea").input_value() == "still editable"
        remote.close()
        assert admin.desktop(name)["state"] == "running"

        # Regular accounts may use the desktop, but cannot invoke host controls.
        user = make_account(admin, [name])
        regular = supervisor.client().login(user, "pw")
        assert regular.post(f"/api/environments/{name}/reclaim-memory").status_code == 403
        regular_browser, regular_page = signed_in(pw, supervisor.base, user=user, password="pw")
        regular_page.goto(f"{supervisor.base}/environments/{name}")
        expect(regular_page.get_by_role("heading", name=name, exact=True)).to_be_visible(timeout=15000)
        expect(regular_page.get_by_role("button", name="Reclaim memory", exact=True)).to_have_count(0)
        regular_browser.close()

        assert admin.post(f"/api/environments/{name}/stop").status_code == 204
        assert admin.post(f"/api/environments/{name}/reclaim-memory").status_code == 409
        page.reload()
        expect(page.get_by_role("button", name="Start", exact=True).first).to_be_visible(timeout=15000)
        expect(page.get_by_role("button", name="Reclaim memory", exact=True)).to_have_count(0)
        browser.close()


def test_reclaim_button_disabled_for_unsupported_host(supervisor, desktop, name):
    """Keep the UI honest when the host reports a failed boot probe."""
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.route("**/api/host", lambda route: route.fulfill(json={
            "cpus": 4, "memBytes": 8 * 1024**3, "memUsed": 0, "running": 1,
            "total": 1,
            "memoryReclaim": {"enabled": False, "reason": "The Linux host has no swap."},
        }))
        page.goto(f"{supervisor.base}/environments/{name}")
        expect(page.get_by_role("button", name="Reclaim memory", exact=True)).to_be_disabled(timeout=15000)
        expect(page.get_by_text("Unavailable: The Linux host has no swap.", exact=True)).to_be_visible()
        browser.close()


def test_automatic_memory_opt_out_saves_without_restart(admin, supervisor, desktop, name, tmp_path):
    before = admin.desktop(name)
    assert before["memoryReclaim"] is True
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/environments/{name}?tab=settings")
        toggle = page.get_by_role("checkbox", name="Automatically reclaim idle memory")
        expect(toggle).to_be_checked(timeout=15000)
        with page.expect_response(lambda r: r.url.endswith("/memory-policy") and r.request.method == "PUT") as response:
            toggle.uncheck()
        assert response.value.status == 204
        expect(toggle).not_to_be_checked()
        after = admin.desktop(name)
        assert after["memoryReclaim"] is False
        assert after["state"] == "running"
        assert after["startedAt"] == before["startedAt"]
        assert after["revision"] == before["revision"] and after["appliedRevision"] == before["appliedRevision"]
        page.reload()
        expect(toggle).not_to_be_checked(timeout=15000)
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        toggle.scroll_into_view_if_needed()
        page.screenshot(path=str(tmp_path / "memory-policy-mobile.png"))
        with page.expect_response(lambda r: r.url.endswith("/memory-policy") and r.request.method == "PUT") as response:
            toggle.check()
        assert response.value.status == 204
        expect(toggle).to_be_checked()
        assert admin.desktop(name)["memoryReclaim"] is True
        browser.close()
