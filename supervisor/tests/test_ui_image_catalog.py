"""The catalog uses prebuilt images and exposes no image authoring workflow."""
from playwright.sync_api import expect, sync_playwright
from test_ui_images import mock_ui, mock_page


def test_catalog_only_imports_prebuilt_images(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/images")
        requests = []
        page.on("request", lambda r: requests.append(r.url))
        page.reload()
        expect(page.get_by_role("button", name="Add image", exact=True)).to_be_visible()
        for name in ("New image", "Edit", "Rebuild", "Customize"):
            expect(page.get_by_role("button", name=name, exact=True)).to_have_count(0)
        assert not any("/api/recipes" in url for url in requests)
        for method, route in (("GET", "/api/recipes"), ("POST", "/api/recipes"),
                              ("GET", "/api/desktops/research/image-scan"),
                              ("POST", "/api/desktops/research/image")):
            assert page.request.fetch(mock_ui + route, method=method).status == 404
        page.get_by_role("button", name="Add image", exact=True).click()
        page.get_by_label("Image reference", exact=True).fill("example.test/desktop:ready")
        page.get_by_role("dialog").get_by_role("button", name="Add image", exact=True).click()
        expect(page.get_by_role("row").filter(has_text="example.test/desktop:ready")).to_be_visible(timeout=15000)
        browser.close()


def test_desktop_menu_has_no_image_creation(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/desktops/research")
        page.get_by_role("button", name="More actions", exact=True).click()
        expect(page.get_by_role("menuitem", name="New image from this desktop…")).to_have_count(0)
        browser.close()


def test_docker_permission_create_and_settings(mock_ui):
    import json
    import uuid
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/")
        page.get_by_role("button", name="New desktop", exact=True).click()
        dialog = page.get_by_role("dialog")
        permission = dialog.get_by_role("checkbox", name="Docker daemon access", exact=True)
        expect(permission).not_to_be_checked()
        expect(dialog.get_by_text("control the host Docker daemon", exact=False)).to_be_visible()
        name = "docker-" + uuid.uuid4().hex[:8]
        dialog.get_by_label("Name", exact=True).fill(name)
        permission.check()
        with page.expect_request(lambda r: r.method == "POST" and r.url.endswith("/api/desktops")) as sent:
            dialog.get_by_role("button", name="Create desktop", exact=True).click()
        assert json.loads(sent.value.post_data)["dockerAccess"] is True
        expect(dialog).to_have_count(0, timeout=15000)
        page.goto(mock_ui + "/desktops/" + name + "?tab=settings")
        permission = page.get_by_role("checkbox", name="Docker daemon access", exact=True)
        expect(permission).to_be_checked()
        permission.uncheck()
        expect(page.get_by_role("button", name="Save and restart", exact=True)).to_be_enabled()
        page.get_by_role("button", name="Undo changes", exact=True).click()
        expect(permission).to_be_checked()
        permission.uncheck()
        with page.expect_request(lambda r: r.method == "PATCH" and r.url.endswith("/api/desktops/" + name)) as sent:
            page.get_by_role("button", name="Save and restart", exact=True).click()
        assert json.loads(sent.value.post_data) == {"dockerAccess": False}
        expect(page.get_by_role("button", name="Save and restart", exact=True)).to_be_disabled(timeout=15000)
        page.reload()
        expect(page.get_by_role("checkbox", name="Docker daemon access", exact=True)).not_to_be_checked()
        browser.close()
