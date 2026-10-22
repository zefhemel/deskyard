import io
import time

from PIL import Image
from playwright.sync_api import sync_playwright

PAGE = """data:text/html,<title>computer-probe</title>
<body style='margin:0;height:100vh' onclick="document.title='clicked'">
<input id=i autofocus style='font-size:40px;margin:40px'>
</body>"""


def test_screenshot_png(sandbox):
    png = sandbox.exec_bytes("computer screenshot")
    assert png.startswith(b"\x89PNG")
    w, h = map(int, sandbox.exec("computer size").stdout.split())
    assert Image.open(io.BytesIO(png)).size == (w, h)


def test_screenshot_to_file(sandbox):
    sandbox.exec("computer screenshot /tmp/s.png && head -c 4 /tmp/s.png | grep -q PNG")


def test_size_and_cursor(sandbox):
    assert sandbox.exec("computer size").stdout.split() == ["1280", "800"]
    sandbox.exec("computer move 100 200")
    assert sandbox.exec("computer cursor").stdout.split() == ["100", "200"]


def test_click_and_type_roundtrip(sandbox, artifacts_dir):
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_url)
        page = browser.contexts[0].new_page()
        page.goto(PAGE)
        page.bring_to_front()
        time.sleep(1)
        sandbox.exec("computer click 640 500")
        page.wait_for_function("document.title === 'clicked'", timeout=5000)
        page.focus("#i")
        sandbox.exec("computer type 'hello world'")
        sandbox.exec("computer key ctrl+a BackSpace")
        sandbox.exec("computer type hello")
        page.wait_for_function("document.getElementById('i').value === 'hello'", timeout=5000)
        (artifacts_dir / "roundtrip.png").write_bytes(sandbox.exec_bytes("computer screenshot"))
        browser.close()


def test_scroll_and_drag_accept_arguments(sandbox):
    sandbox.exec("computer scroll 640 400 down 2")
    sandbox.exec("computer drag 10 10 20 20")
    sandbox.exec("computer double-click 640 400")
    sandbox.exec("computer click 640 400 right && computer key Escape")


def test_works_without_display_env(sandbox):
    sandbox.exec("env -u DISPLAY computer size")


def test_usage_error(sandbox):
    assert sandbox.exec("computer bogus", check=False).returncode == 2
    assert sandbox.exec("computer click 1", check=False).returncode == 2


def test_zoom_crops_region(sandbox):
    png = sandbox.exec_bytes("computer zoom 100 50 420 290")
    assert png.startswith(b"\x89PNG")
    assert Image.open(io.BytesIO(png)).size == (320, 240)
    sandbox.exec("computer zoom 0 0 10 10 /tmp/z.png && head -c 4 /tmp/z.png | grep -q PNG")


def test_zoom_rejects_bad_region(sandbox):
    assert sandbox.exec("computer zoom 50 50 10 10", check=False).returncode == 2


def test_triple_click_selects_line(sandbox):
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_url)
        page = browser.contexts[0].new_page()
        page.goto("data:text/html,<p id=p style='font-size:60px;margin:200px 0 0 0'>select this whole line</p>")
        page.bring_to_front()
        time.sleep(1)
        box = page.locator("#p").bounding_box()
        # Page coordinates -> screen: find the page's offset from a known click.
        offset_y = page.evaluate("window.screenY + (window.outerHeight - window.innerHeight)")
        offset_x = page.evaluate("window.screenX")
        sandbox.exec(f"computer triple-click {int(offset_x + box['x'] + 50)} {int(offset_y + box['y'] + box['height'] / 2)}")
        assert page.evaluate("window.getSelection().toString().trim()") == "select this whole line"
        browser.close()


def test_mouse_down_up_and_hold_key_and_wait(sandbox):
    sandbox.exec("computer mouse-down && computer mouse-up && computer mouse-down right && computer mouse-up right")
    sandbox.exec("computer hold-key shift 0.2")
    import time as _t
    start = _t.time()
    sandbox.exec("computer wait 1")
    assert _t.time() - start >= 1


def test_screenshot_settles_first(sandbox):
    import time as _t
    start = _t.time()
    sandbox.exec_bytes("COMPUTER_SETTLE=1 computer screenshot")
    assert _t.time() - start >= 1
