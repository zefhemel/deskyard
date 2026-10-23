import io
import time

from PIL import Image
from playwright.sync_api import sync_playwright

from sandbox import TOKEN_HEADER, wait_until

PAGE = """data:text/html,<title>desktop-probe</title>
<body style='margin:0;height:100vh' onclick="document.title='clicked'">
<input id=i autofocus style='font-size:40px;margin:40px'>
</body>"""


def wait_in_front(sandbox, title: str) -> None:
    """Waits until the active window is the Chromium page with this title."""
    active = lambda: sandbox.exec("xdotool getactivewindow getwindowname", check=False).stdout  # noqa: E731
    assert wait_until(lambda: title in active(), 10), active()


def test_screenshot_png(sandbox):
    png = sandbox.exec_bytes("deskyard screenshot")
    assert png.startswith(b"\x89PNG")
    w, h = map(int, sandbox.exec("deskyard size").stdout.split())
    assert Image.open(io.BytesIO(png)).size == (w, h)


def test_screenshot_to_file(sandbox):
    sandbox.exec("deskyard screenshot /tmp/s.png && head -c 4 /tmp/s.png | grep -q PNG")


def test_size_and_cursor(sandbox):
    assert sandbox.exec("deskyard size").stdout.split() == ["1280", "800"]
    assert "1280x800" in sandbox.exec("xdpyinfo -display :1 | grep dimensions").stdout
    sandbox.exec("deskyard move 100 200")
    assert sandbox.exec("deskyard cursor").stdout.split() == ["100", "200"]


def test_click_and_type_roundtrip(sandbox, artifacts_dir):
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_ws(), headers=TOKEN_HEADER)
        page = browser.contexts[0].new_page()
        page.goto(PAGE)
        page.bring_to_front()
        wait_in_front(sandbox, "desktop-probe")
        sandbox.exec("deskyard click 640 500")
        page.wait_for_function("document.title === 'clicked'", timeout=5000)
        page.focus("#i")
        sandbox.exec("deskyard type 'hello world'")
        sandbox.exec("deskyard key ctrl+a BackSpace")
        sandbox.exec("deskyard type hello")
        page.wait_for_function("document.getElementById('i').value === 'hello'", timeout=5000)
        (artifacts_dir / "roundtrip.png").write_bytes(sandbox.exec_bytes("deskyard screenshot"))
        browser.close()


def test_scroll_and_drag_accept_arguments(sandbox):
    sandbox.exec("deskyard scroll 640 400 down 2")
    sandbox.exec("deskyard drag 10 10 20 20")
    sandbox.exec("deskyard double-click 640 400")
    sandbox.exec("deskyard click 640 400 right && deskyard key Escape")


def test_works_without_display_env(sandbox):
    sandbox.exec("env -u DISPLAY deskyard size")


def test_usage_error(sandbox):
    assert sandbox.exec("deskyard bogus", check=False).returncode == 2
    assert sandbox.exec("deskyard click 1", check=False).returncode == 2


def test_zoom_crops_region(sandbox):
    png = sandbox.exec_bytes("deskyard zoom 100 50 420 290")
    assert png.startswith(b"\x89PNG")
    assert Image.open(io.BytesIO(png)).size == (320, 240)
    sandbox.exec("deskyard zoom 0 0 10 10 /tmp/z.png && head -c 4 /tmp/z.png | grep -q PNG")


def test_zoom_rejects_bad_region(sandbox):
    assert sandbox.exec("deskyard zoom 50 50 10 10", check=False).returncode == 2


def test_triple_click_selects_line(sandbox):
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sandbox.cdp_ws(), headers=TOKEN_HEADER)
        page = browser.contexts[0].new_page()
        page.goto("data:text/html,<title>triple-probe</title><p id=p style='font-size:60px;margin:200px 0 0 0'>select this whole line</p>")
        page.bring_to_front()
        wait_in_front(sandbox, "triple-probe")
        box = page.locator("#p").bounding_box()
        # Page coordinates -> screen: find the page's offset from a known click.
        offset_y = page.evaluate("window.screenY + (window.outerHeight - window.innerHeight)")
        offset_x = page.evaluate("window.screenX")
        sandbox.exec(f"deskyard triple-click {int(offset_x + box['x'] + 50)} {int(offset_y + box['y'] + box['height'] / 2)}")
        assert page.evaluate("window.getSelection().toString().trim()") == "select this whole line"
        browser.close()


def test_mouse_down_up_and_hold_key_and_wait(sandbox):
    sandbox.exec("deskyard mouse-down && deskyard mouse-up && deskyard mouse-down right && deskyard mouse-up right")
    sandbox.exec("deskyard hold-key shift 0.2")
    start = time.time()
    sandbox.exec("deskyard wait 1")
    assert time.time() - start >= 1


def test_screenshot_settles_first(sandbox):
    start = time.time()
    sandbox.exec_bytes("DESKYARD_SETTLE=1 deskyard screenshot")
    assert time.time() - start >= 1
