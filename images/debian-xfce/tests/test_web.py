import io

import requests
from PIL import Image
from playwright.sync_api import sync_playwright

from sandbox import TOKEN_HEADER


def test_requires_token(sandbox):
    assert requests.get(sandbox.web_url, timeout=5).status_code == 401
    assert requests.get(sandbox.web_url, headers={"X-Deskyard-Token": "nope"}, timeout=5).status_code == 401
    assert sandbox.get("/").status_code == 200
    assert requests.get(sandbox.web_url + "/_cdp/json/version", timeout=5).status_code == 401


def open_desktop(p, web_url: str, width=1280, height=800):
    browser = p.chromium.launch()
    ctx = browser.new_context(extra_http_headers=TOKEN_HEADER, viewport={"width": width, "height": height})
    # A British-English browser that also knows Polish: the client used to pick Polish.
    ctx.add_init_script("Object.defineProperty(navigator, 'languages', {get: () => ['en-GB', 'pl']})")
    page = ctx.new_page()
    page.goto(web_url)
    page.wait_for_selector("canvas", state="attached", timeout=30_000)
    page.wait_for_function(
        "() => [...document.querySelectorAll('canvas')].some(c => c.width > 100)", timeout=30_000)
    page.wait_for_timeout(4000)
    return browser, page


def desktop_screenshot(web_url: str) -> bytes:
    with sync_playwright() as p:
        browser, page = open_desktop(p, web_url)
        png = page.screenshot()
        browser.close()
        return png


def distinct_colours(png: bytes) -> int:
    img = Image.open(io.BytesIO(png)).convert("RGB").resize((320, 200))
    return len(img.getcolors(maxcolors=320 * 200))


def test_desktop_renders(sandbox, artifacts_dir):
    png = desktop_screenshot(sandbox.web_url)
    (artifacts_dir / "desktop.png").write_bytes(png)
    assert distinct_colours(png) > 50


def test_desktop_follows_browser_window(sandbox):
    with sync_playwright() as p:
        browser, page = open_desktop(p, sandbox.web_url, width=1000, height=640)
        assert sandbox.exec("deskyard size").stdout.split() == ["1000", "640"]
        page.set_viewport_size({"width": 1400, "height": 900})
        page.wait_for_timeout(3000)
        assert sandbox.exec("deskyard size").stdout.split() == ["1400", "900"]
        browser.close()


def test_web_client_in_english(sandbox):
    with sync_playwright() as p:
        browser, page = open_desktop(p, sandbox.web_url)
        menu = page.evaluate("document.body.innerText")
        browser.close()
    assert "Settings" in menu and "Clipboard" in menu, menu[:300]


def websocket_handshake(web_url: str, token: str | None) -> int:
    import base64
    import http.client
    from urllib.parse import urlparse

    u = urlparse(web_url)
    conn = http.client.HTTPConnection(u.hostname, u.port, timeout=10)
    headers = {
        "Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
        "Sec-WebSocket-Key": base64.b64encode(b"0123456789abcdef").decode(),
        "Sec-WebSocket-Protocol": "binary",
    }
    if token:
        headers["X-Deskyard-Token"] = token
    headers["Origin"] = web_url  # browsers always send one; KasmVNC refuses without
    conn.request("GET", "/websockify", headers=headers)
    status = conn.getresponse().status
    conn.close()
    return status


def test_websocket_needs_token(sandbox):
    assert websocket_handshake(sandbox.web_url, None) == 401


def test_websocket_with_token(sandbox):
    assert websocket_handshake(sandbox.web_url, TOKEN_HEADER["X-Deskyard-Token"]) == 101
