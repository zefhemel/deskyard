"""Install metadata and viewport behavior; native Home Screen checks live in docs."""
from io import BytesIO
import pytest
from PIL import Image
from playwright.sync_api import expect, sync_playwright
from helpers import open_desktop


def test_pwa_install_assets_are_public(supervisor):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        page.goto(supervisor.base + "/login")
        manifest_url = page.locator('link[rel="manifest"]').get_attribute("href", timeout=3000)
        response = page.request.get(supervisor.base + manifest_url)
        assert response.ok
        assert "manifest+json" in response.headers["content-type"]
        manifest = response.json()
        assert manifest["display"] == "standalone"
        assert manifest["scope"] == "/"
        assert manifest["start_url"] == "/"
        assert "viewport-fit=cover" in page.locator('meta[name="viewport"]').get_attribute("content")
        assert page.locator('meta[name="apple-mobile-web-app-capable"]').get_attribute("content") == "yes"
        assert page.locator('meta[name="apple-mobile-web-app-status-bar-style"]').get_attribute("content") == "black-translucent"
        icons = manifest["icons"] + [{"src":page.locator('link[rel="apple-touch-icon"]').get_attribute("href"), "sizes":"180x180"}]
        for icon in icons:
            response = page.request.get(supervisor.base + icon["src"])
            assert response.ok
            image = Image.open(BytesIO(response.body()))
            assert image.size == tuple(map(int, icon["sizes"].split("x")))
        browser.close()


@pytest.mark.webkit
def test_pwa_viewport_keeps_controls_visible(supervisor, desktop, name):
    with sync_playwright() as p:
        browser = p.webkit.launch()
        page = browser.new_page(viewport={"width":402,"height":874},has_touch=True,is_mobile=True)
        page.goto(supervisor.base + "/login")
        page.fill("#u", "admin")
        from conftest import ADMIN_PASSWORD
        page.fill("#p", ADMIN_PASSWORD)
        page.get_by_role("button",name="Sign in",exact=True).click()
        page.wait_for_url(lambda u:'/login' not in u)
        open_desktop(page,supervisor.base,name,status=False)
        for width,height in [(402,874),(874,402),(402,430),(402,874)]:
            page.set_viewport_size({"width":width,"height":height})
            expect(page.get_by_role("button",name="Trackpad",exact=True)).to_be_visible()
            expect(page.get_by_role("button",name="More",exact=True)).to_be_visible()
            page.wait_for_function("Math.abs(document.querySelector('[data-desktop-page]').getBoundingClientRect().height - visualViewport.height) < 2")
            assert page.evaluate("document.documentElement.scrollHeight <= innerHeight+1")
        # Simulate a notched standalone viewport, including its native blur buffer.
        page.add_style_tag(content=":root{--safe-top:62px;--standalone-edge:16px;--safe-bottom:34px}")
        page.get_by_role("button",name="Audio",exact=True).click()
        dialog = page.get_by_role("dialog",name="Audio")
        expect(dialog).to_be_visible()
        assert dialog.bounding_box()["y"] >= 78
        browser.close()
