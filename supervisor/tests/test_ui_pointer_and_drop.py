"""The pointer on the desktop page, and files dropped on it: copied into
~/Desktop through POST /api/computers/{name}/upload (test_upload.py)."""

import re
import time
from pathlib import Path

import pytest
from playwright.sync_api import expect, sync_playwright

from helpers import (BROWSERS, PICTURE, SHOWING, frame_size, in_desktop, mouse_location, open_desktop, screen_size,
                     signed_in, to_page, wait_until)


def desk(supervisor, desktop) -> Path:
    """The desktop's ~/Desktop: the API names its folder as the host path."""
    return Path(desktop["folder"]) / "Desktop"


@pytest.mark.parametrize("touch", [False, True], ids=["mouse", "touch"])
def test_drag_lands_right(supervisor, desktop, name, touch):
    # Inside our frame Chromium's pointerrawupdate reports positions relative
    # to the top-level page, so drags landed off by the bar's height until
    # the served client additions hid it.
    in_desktop(name, "sh", "-c", "setsid xfce4-terminal --title dragme --geometry 60x12+300+250 >/dev/null 2>&1 &")
    win = wait_until(lambda: in_desktop(name, "xdotool", "search", "--name", "^dragme$", check=False).split(), 20)
    assert win, "no terminal window"
    win = win[0]

    def position():
        # The client area's top left on the screen. (xdotool getwindowgeometry
        # counts the window manager's frame twice.)
        out = in_desktop(name, "xwininfo", "-id", win)
        x = re.search(r"Absolute upper-left X:\s+(\d+)", out).group(1)
        y = re.search(r"Absolute upper-left Y:\s+(\d+)", out).group(1)
        return int(x), int(y)

    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, has_touch=touch,
                                       viewport={"width": 744, "height": 900} if touch else {"width": 1280, "height": 860})
        open_desktop(page, supervisor.base, name, status=not touch)
        # The screen has taken the frame's size (which may move the window).
        frame = frame_size(page)
        assert wait_until(lambda: all(abs(v - w) <= 8 for v, w in zip(screen_size(name), frame)), 10), (screen_size(name), frame)
        x0, y0 = position()
        top = int(in_desktop(name, "xprop", "-id", win, "_NET_FRAME_EXTENTS").split("=")[1].split(",")[2])
        a = (x0 + 150, y0 - top // 2)  # on the title bar
        b = (a[0] + 180, a[1] + 120)
        pic = page.evaluate(PICTURE)
        pa, pb = to_page(pic, *a), to_page(pic, *b)
        cdp = page.context.new_cdp_session(page) if touch else None

        def finger(kind, point=None):
            cdp.send("Input.dispatchTouchEvent", {"type": kind, "touchPoints":
                     [] if point is None else [{"x": point[0], "y": point[1], "id": 1}]})

        if touch:
            finger("touchStart", pa)
        else:
            page.mouse.move(*pa)
            page.mouse.down()
        for i in range(1, 21):
            point = (pa[0] + (pb[0] - pa[0]) * i / 20, pa[1] + (pb[1] - pa[1]) * i / 20)
            if touch:
                finger("touchMove", point)
            else:
                page.mouse.move(*point)
            time.sleep(0.02)
        if touch:
            finger("touchEnd")
        else:
            page.mouse.up()
        # Touch starts a drag after Selkies' movement threshold; that first
        # small movement is consumed while distinguishing a drag from a tap.
        tolerance = 15 if touch else 4
        near = lambda p, q, limit=4: abs(p[0] - q[0]) <= limit and abs(p[1] - q[1]) <= limit  # noqa: E731
        assert wait_until(lambda: near(mouse_location(name), b), 5), (mouse_location(name), b)
        moved = (x0 + b[0] - a[0], y0 + b[1] - a[1])
        assert wait_until(lambda: near(position(), moved, tolerance), 5), ((x0, y0), position(), a, b)
        browser.close()


def test_pointerrawupdate_hidden(supervisor, desktop, name):
    # Inside our frame Chrome reports pointerrawupdate's position relative to
    # the top-level page, off by the bar's height. Our additions hide the
    # event from Selkies; a synthetic one, offset as Chrome's would be, must
    # not move the desktop's pointer.
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        open_desktop(page, supervisor.base, name)
        pic = page.evaluate(PICTURE)
        page.mouse.move(*to_page(pic, 400, 300))
        assert wait_until(lambda: mouse_location(name) == (400, 300), 5), mouse_location(name)
        bar = page.evaluate("document.querySelector('iframe').getBoundingClientRect().top")
        assert bar > 30
        page.frames[1].evaluate("""([x, y]) => {
            const target = document.getElementById('overlayInput');
            for (let i = 0; i < 5; i++)
                target.dispatchEvent(new PointerEvent('pointerrawupdate', {pointerType: 'mouse', clientX: x, clientY: y, bubbles: true}));
        }""", [400, 300 + bar])
        time.sleep(1)
        assert mouse_location(name) == (400, 300)
        browser.close()


# A drag of files over the screen, as the browser fires it: in the frame, on
# Selkies' input layer (#overlayInput, under the pointer), or on our overlay.
DRAG = """([target, type, files]) => {
  const dt = new DataTransfer();
  for (const f of files) dt.items.add(new File([f.content], f.name, {type: 'text/plain'}));
  const el = target === 'overlay' ? document.querySelector('[data-testid=drop-overlay]') : document.getElementById(target);
  const ev = new DragEvent(type, {dataTransfer: dt, bubbles: true, cancelable: true});
  el.dispatchEvent(ev);
  return ev.defaultPrevented;
}"""


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_drop_upload(supervisor, desktop, name, browser_name):
    # Dropping files on Selkies' screen copies them into ~/Desktop through
    # POST /api/computers/{name}/upload. The first event of a hover, in the
    # frame on Selkies' input layer, shows our overlay; Selkies' own upload
    # (closed in the image) never runs, and nothing navigates the frame.
    files = [{"name": "dropped.txt", "content": "dropped content\n"}, {"name": "second.txt", "content": "2\n"}]
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        selkies_uploads = []
        page.on("request", lambda r: selkies_uploads.append(r.url) if r.url.split("?")[0].endswith("/api/upload") else None)
        open_desktop(page, supervisor.base, name)
        frame = page.frames[1]
        frame_url = frame.url
        overlay = page.get_by_text(f"Drop to copy into {name}")

        assert frame.evaluate(DRAG, ["overlayInput", "dragenter", files]) is True
        expect(overlay).to_be_visible()
        assert page.evaluate(DRAG, ["overlay", "drop", files]) is True
        expect(page.get_by_text("Copied dropped.txt")).to_be_visible(timeout=20000)
        expect(page.get_by_text("Copied second.txt")).to_be_visible(timeout=20000)
        expect(overlay).to_be_hidden()

        # A drop that reaches Selkies' input layer before the overlay is up.
        assert frame.evaluate(DRAG, ["overlayInput", "dragover", [files[0]]]) is True
        assert frame.evaluate(DRAG, ["overlayInput", "drop", [{"name": "direct.txt", "content": "direct\n"}]]) is True
        expect(page.get_by_text("Copied direct.txt")).to_be_visible(timeout=20000)

        assert frame.url == frame_url
        assert page.evaluate(SHOWING), "the frame was reloaded or navigated"
        assert selkies_uploads == []
        browser.close()
    out = in_desktop(name, "sh", "-c", "cd /workspace/Desktop && stat -c '%n %u:%g' dropped.txt second.txt direct.txt && cat dropped.txt second.txt direct.txt; ls -A")
    lines = out.split("\n")
    assert lines[:3] == ["dropped.txt 1000:1000", "second.txt 1000:1000", "direct.txt 1000:1000"], out
    assert "\n".join(lines[3:6]) == "dropped content\n2\ndirect", out
    assert sorted(lines[6:-1]) == ["direct.txt", "dropped.txt", "second.txt"], out


def test_drop_upload_browser_drag(supervisor, desktop, name, tmp_path):
    # The same through Chromium's own drag input (as a drag from the file
    # manager arrives): it enters straight over the screen, so the frame
    # gets the first events, and must show the overlay.
    src = tmp_path / "native.txt"
    src.write_text("from a real drag\n")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        open_desktop(page, supervisor.base, name)
        cdp = page.context.new_cdp_session(page)
        box = page.evaluate("(() => { const r = document.querySelector('iframe').getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()")
        data = {"items": [], "files": [str(src)], "dragOperationsMask": 1}

        def drag(kind, x, y):
            cdp.send("Input.dispatchDragEvent", {"type": kind, "x": x, "y": y, "data": data})

        drag("dragEnter", *box)
        drag("dragOver", *box)
        expect(page.get_by_text(f"Drop to copy into {name}")).to_be_visible(timeout=2000)
        drag("dragOver", box[0] + 5, box[1] + 5)
        drag("drop", box[0] + 5, box[1] + 5)
        expect(page.get_by_text("Copied native.txt")).to_be_visible(timeout=20000)
        assert page.evaluate(SHOWING)
        browser.close()
    assert in_desktop(name, "cat", "/workspace/Desktop/native.txt") == "from a real drag\n"


DRAG_JS = """([type, files]) => {
  const dt = new DataTransfer();
  for (const f of files) dt.items.add(new File([f.content], f.name, {type: 'text/plain'}));
  const target = type === 'drop' && document.querySelector('[data-testid=drop-overlay]') || document;
  const ev = new DragEvent(type, {dataTransfer: dt, bubbles: true, cancelable: true});
  target.dispatchEvent(ev);
  return ev.defaultPrevented;
}"""


def open_desktop_frame(p, supervisor, name):
    browser, page = signed_in(p, supervisor.base)
    page.goto(f"{supervisor.base}/{name}/")
    expect(page.get_by_role("button", name="More")).to_be_visible(timeout=15000)
    frame = page.query_selector("iframe").content_frame()
    frame.wait_for_load_state("load")
    return browser, page, frame


def test_drag_inside_the_frame_shows_overlay_and_drop_uploads(supervisor, desktop, name):
    files = [{"name": "from-frame.txt", "content": "via the iframe\n"}]
    with sync_playwright() as p:
        browser, page, frame = open_desktop_frame(p, supervisor, name)
        overlay = page.get_by_text(f"Drop to copy into {name}")
        # The events fire in the frame's own document, as a real drag over the screen does.
        assert frame.evaluate(DRAG_JS, ["dragenter", files]) is True  # default prevented: the frame won't navigate
        frame.evaluate(DRAG_JS, ["dragover", files])
        expect(overlay).to_be_visible()
        page.evaluate(DRAG_JS, ["drop", files])  # the overlay now covers the frame and takes the drop
        expect(page.get_by_text("Copied from-frame.txt")).to_be_visible(timeout=20000)
        expect(overlay).to_be_hidden()
        assert (desk(supervisor, desktop) / "from-frame.txt").read_text() == "via the iframe\n"

        # The frame is reloaded (reconnect): listeners come back.
        frame.evaluate("window.__beforeReload = true")
        page.get_by_role("button", name="Reconnect").click() if page.get_by_role("button", name="Reconnect").count() else frame.evaluate("location.reload()")
        # Wait for the new document: until then the old one may still answer.
        page.wait_for_function("(() => { const w = document.querySelector('iframe')?.contentWindow;"
                               " return !!w && !w.__beforeReload && w.document.readyState === 'complete'; })()")
        frame = page.query_selector("iframe").content_frame()
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        # A drop that lands in the frame itself (before the overlay) uploads too.
        frame.evaluate(DRAG_JS, ["drop", [{"name": "direct.txt", "content": "d"}]])
        expect(page.get_by_text("Copied direct.txt")).to_be_visible(timeout=20000)
        browser.close()


def test_drag_that_leaves_hides_the_overlay(supervisor, desktop, name):
    files = [{"name": "never.txt", "content": "x"}]
    with sync_playwright() as p:
        browser, page, frame = open_desktop_frame(p, supervisor, name)
        overlay = page.get_by_text(f"Drop to copy into {name}")
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        expect(overlay).to_be_hidden(timeout=5000)  # no more dragover: the drag is over
        # dragend and a click also clear it.
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        page.evaluate("window.dispatchEvent(new DragEvent('dragend'))")
        expect(overlay).to_be_hidden()
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        page.mouse.move(300, 300)
        expect(overlay).to_be_hidden()
        # A drag that enters over the bar, outside the frame, shows it too.
        page.evaluate("""(files) => {
            const dt = new DataTransfer();
            for (const f of files) dt.items.add(new File([f.content], f.name, {type: 'text/plain'}));
            window.dispatchEvent(new DragEvent('dragenter', {dataTransfer: dt, bubbles: true}));
        }""", files)
        expect(overlay).to_be_visible()
        expect(overlay).to_be_hidden(timeout=5000)
        assert not (desk(supervisor, desktop).exists() and any(desk(supervisor, desktop).iterdir()))
        browser.close()


def test_drop_on_the_top_bar_is_not_navigated_away(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page, _ = open_desktop_frame(p, supervisor, name)
        prevented = page.evaluate(
            """() => { const dt = new DataTransfer(); dt.items.add(new File(['x'], 'x.txt'));
               const ev = new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true});
               document.querySelector('header, div').dispatchEvent(ev); return ev.defaultPrevented; }""")
        assert prevented
        browser.close()
