import io
import re

import requests
from PIL import Image
from playwright.sync_api import sync_playwright

from sandbox import AUTH


def test_requires_auth(sandbox):
    assert requests.get(sandbox.web_url, timeout=5).status_code == 401
    assert requests.get(sandbox.web_url, auth=("workspace", "nope"), timeout=5).status_code == 401
    r = requests.get(sandbox.web_url, auth=AUTH, timeout=5, allow_redirects=False)
    assert r.status_code in (200, 302)


def open_desktop(p, web_url: str, username: str, password: str, width=1280, height=800):
    browser = p.chromium.launch()
    ctx = browser.new_context(http_credentials={"username": username, "password": password},
                              viewport={"width": width, "height": height})
    # A British-English browser that also knows Polish: the client used to pick Polish.
    ctx.add_init_script("Object.defineProperty(navigator, 'languages', {get: () => ['en-GB', 'pl']})")
    page = ctx.new_page()
    page.goto(web_url)
    page.wait_for_selector("canvas", state="attached", timeout=30_000)
    page.wait_for_function(
        "() => [...document.querySelectorAll('canvas')].some(c => c.width > 100)", timeout=30_000)
    page.wait_for_timeout(4000)
    return browser, page


def desktop_screenshot(web_url: str, username: str, password: str) -> bytes:
    with sync_playwright() as p:
        browser, page = open_desktop(p, web_url, username, password)
        png = page.screenshot()
        browser.close()
        return png


def distinct_colours(png: bytes) -> int:
    img = Image.open(io.BytesIO(png)).convert("RGB").resize((320, 200))
    return len(img.getcolors(maxcolors=320 * 200))


def test_desktop_renders(sandbox, artifacts_dir):
    png = desktop_screenshot(sandbox.web_url, *AUTH)
    (artifacts_dir / "desktop.png").write_bytes(png)
    assert distinct_colours(png) > 50


def test_desktop_follows_browser_window(sandbox):
    with sync_playwright() as p:
        browser, page = open_desktop(p, sandbox.web_url, *AUTH, width=1000, height=640)
        assert sandbox.exec("computer size").stdout.split() == ["1000", "640"]
        page.set_viewport_size({"width": 1400, "height": 900})
        page.wait_for_timeout(3000)
        assert sandbox.exec("computer size").stdout.split() == ["1400", "900"]
        browser.close()


def test_web_client_in_english(sandbox):
    with sync_playwright() as p:
        browser, page = open_desktop(p, sandbox.web_url, *AUTH)
        menu = page.evaluate("document.body.innerText")
        browser.close()
    assert "Settings" in menu and "Clipboard" in menu, menu[:300]


def test_generated_password(make_sandbox):
    sb = make_sandbox(env={})
    m = re.search(r"WORKSPACE PASSWORD \(generated\): (\S+)", sb.logs())
    assert m, sb.logs()[-2000:]
    pw = m.group(1)
    assert requests.get(sb.web_url, auth=("workspace", pw), timeout=5).status_code in (200, 302)
    sb.restart()
    assert requests.get(sb.web_url, auth=("workspace", pw), timeout=5).status_code in (200, 302)
    assert sb.logs().count("WORKSPACE PASSWORD (generated)") == 1


def test_special_char_password(make_sandbox):
    pw = "p@ss w$rd\"x'y"
    sb = make_sandbox(env={"WORKSPACE_PASSWORD": pw})
    assert requests.get(sb.web_url, auth=("workspace", pw), timeout=5).status_code in (200, 302)


def test_domain_mode_config_valid(sandbox):
    sandbox.exec("WORKSPACE_DOMAIN=example.test start-caddy --validate-only", user="root")


def websocket_handshake(web_url: str, origin: str | None) -> int:
    import base64
    import http.client
    from urllib.parse import urlparse

    u = urlparse(web_url)
    conn = http.client.HTTPConnection(u.hostname, u.port, timeout=10)
    headers = {
        "Authorization": "Basic " + base64.b64encode(":".join(AUTH).encode()).decode(),
        "Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
        "Sec-WebSocket-Key": base64.b64encode(b"0123456789abcdef").decode(),
        "Sec-WebSocket-Protocol": "binary",
    }
    if origin:
        headers["Origin"] = origin
    conn.request("GET", "/websockify", headers=headers)
    status = conn.getresponse().status
    conn.close()
    return status


def test_websocket_rejects_foreign_origin(sandbox):
    assert websocket_handshake(sandbox.web_url, "https://evil.example") == 403


def test_websocket_accepts_own_origin(sandbox):
    assert websocket_handshake(sandbox.web_url, sandbox.web_url) == 101
