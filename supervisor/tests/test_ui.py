"""The web UI in a real browser, against the real supervisor and a real desktop.

The desktop page's tests run in Chromium, Firefox and WebKit. Firefox is
skipped where Playwright's Firefox can't start on the machine.
"""

import array
import math
import re
import struct
import subprocess
import time
import uuid
import wave

import pytest

from playwright.sync_api import Error as PlaywrightError
from playwright.sync_api import expect, sync_playwright

from conftest import ADMIN_PASSWORD, docker, free_port
from test_cli import Forward, cli, forwards, local_http, remote  # noqa: F401  (fixtures)

BROWSERS = ["chromium", "firefox", "webkit"]

# The frame's hooks (supervisor/internal/server/screen_additions.js).
SCREEN = "document.querySelector('iframe')?.contentWindow?.deskyardScreen"
SHOWING = f"{SCREEN}?.state() === 'connected' && {SCREEN}.video() !== null"


def launch(p, browser="chromium"):
    try:
        return getattr(p, browser).launch()
    except PlaywrightError as e:
        if browser == "firefox" and "Could not find profile folder" in str(e):
            pytest.skip("Playwright's Firefox doesn't start on this machine (Could not find profile folder)")
        raise


def signed_in(p, base, user="admin", password=ADMIN_PASSWORD, browser="chromium", **ctx):
    b = launch(p, browser)
    ctx.setdefault("viewport", {"width": 1280, "height": 860})
    page = b.new_context(**ctx).new_page()
    page.goto(base + "/")
    page.wait_for_url("**/login**")
    page.fill("#u", user)
    page.fill("#p", password)
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    return b, page


def open_desktop(page, base, name, status=True):
    """The desktop page, showing the picture (status: and saying so; a phone doesn't)."""
    page.goto(f"{base}/{name}/")
    if status:
        expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)
    page.wait_for_function(SHOWING, timeout=30000)


def in_desktop(name, *args, check=True) -> str:
    """Runs a command as the desktop's user, on its screen."""
    r = subprocess.run(["docker", "exec", "-u", "user", "-e", "DISPLAY=:1", f"deskyard-{name}", *args],
                       capture_output=True, text=True)
    if check:
        assert r.returncode == 0, r.stderr
    return r.stdout


def screen_size(name) -> tuple[int, int]:
    w, h = in_desktop(name, "xdotool", "getdisplaygeometry").split()
    return int(w), int(h)


def wait_until(f, timeout=15.0, every=0.3):
    deadline = time.time() + timeout
    while True:
        v = f()
        if v or time.time() > deadline:
            return v
        time.sleep(every)


# Where the picture is shown, in page coordinates, and the remote screen's
# size in pixels: the stream's size (stream_resolution), shown in Selkies'
# video element (or a canvas) with its CSS box at that aspect ratio.
PICTURE = """() => {
    const frame = document.querySelector("iframe");
    const w = frame.contentWindow;
    const shown = w.deskyardScreen.video().getBoundingClientRect();
    const d = w.deskyardScreen.display();
    const f = frame.getBoundingClientRect();
    return {left: f.left + frame.clientLeft + shown.left, top: f.top + frame.clientTop + shown.top,
            width: shown.width, height: shown.height, remoteWidth: d.width, remoteHeight: d.height,
            frameWidth: frame.clientWidth, frameHeight: frame.clientHeight};
}"""


def to_page(pic, x, y):
    return (pic["left"] + x * pic["width"] / pic["remoteWidth"], pic["top"] + y * pic["height"] / pic["remoteHeight"])


def mouse_location(name) -> tuple[int, int]:
    out = in_desktop(name, "xdotool", "getmouselocation")
    x, y = re.search(r"x:(\d+) y:(\d+)", out).groups()
    return int(x), int(y)


def fits(pic) -> bool:
    """The picture is scaled to fit the frame at the stream's aspect ratio."""
    return (pic["width"] <= pic["frameWidth"] + 1 and pic["height"] <= pic["frameHeight"] + 1
            and (abs(pic["width"] - pic["frameWidth"]) <= 1 or abs(pic["height"] - pic["frameHeight"]) <= 1)
            and abs(pic["width"] / pic["height"] - pic["remoteWidth"] / pic["remoteHeight"]) < 0.01)


def test_sign_in_and_see_the_wall(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        expect(page.get_by_text(name, exact=True).first).to_be_visible(timeout=15000)
        expect(page.get_by_text("Deskyard").first).to_be_visible()
        page.screenshot(path=str(tmp_path / "wall.png"))
        browser.close()


def test_wrong_password_shows_an_error(supervisor):
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        page.goto(supervisor.base + "/login")
        page.fill("#u", "admin")
        page.fill("#p", "wrong")
        page.get_by_role("button", name="Sign in").click()
        expect(page.get_by_text("wrong", exact=False).first).to_be_visible()
        assert "/login" in page.url
        browser.close()


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_desktop_page_shows_stream(supervisor, desktop, name, browser_name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        open_desktop(page, supervisor.base, name)
        assert page.frames[1].url == f"{supervisor.base}/{name}/screen/"
        # The picture fills the frame, at the frame's size.
        pic = page.evaluate(PICTURE)
        assert abs(pic["width"] - pic["frameWidth"]) <= 2 and abs(pic["height"] - pic["frameHeight"]) <= 2, pic
        # Selkies' own sidebar and controls are nowhere; ours are, in the spec's order.
        visible = page.frames[1].evaluate("""() => [...document.querySelectorAll('[class*="sidebar"], [id*="sidebar"]')]
            .filter(e => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(e).visibility !== 'hidden'; })
            .map(e => String(e.className))""")
        assert visible == []
        xs = []
        for n in ("Audio", "Lock size", "Clipboard", "Keys", "Quality", "Download", "More"):
            b = page.get_by_role("button", name=n, exact=True)
            expect(b).to_be_visible()
            xs.append(b.bounding_box()["x"])
        assert xs == sorted(xs), xs
        page.screenshot(path=str(tmp_path / f"desktop-{browser_name}.png"))
        browser.close()


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_clipboard_both_ways(supervisor, desktop, name, browser_name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        open_desktop(page, supervisor.base, name)

        # Page → desktop.
        page.get_by_role("button", name="Clipboard").click()
        page.locator("textarea").fill("from-the-page-42")
        page.get_by_role("button", name=f"Paste into {name}").click()
        clip = lambda: in_desktop(name, "xclip", "-o", "-selection", "clipboard", check=False)  # noqa: E731
        assert wait_until(lambda: clip() == "from-the-page-42"), clip()

        # Desktop → page.
        in_desktop(name, "sh", "-c", "printf from-the-desktop-7 | setsid xclip -selection clipboard")
        if not page.get_by_text("Send to").is_visible():
            page.get_by_role("button", name="Clipboard").click()
        expect(page.get_by_text("from-the-desktop-7")).to_be_visible(timeout=15000)
        browser.close()


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_lock_size(supervisor, desktop, name, browser_name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        open_desktop(page, supervisor.base, name)
        lock = page.get_by_role("button", name="Lock size", exact=True)
        expect(lock).to_have_attribute("aria-pressed", "false")
        size = screen_size(name)

        lock.click()
        expect(lock).to_have_attribute("aria-pressed", "true")
        page.set_viewport_size({"width": 900, "height": 700})
        time.sleep(2)
        # The remote screen keeps its size; the picture is scaled to fit the frame.
        assert screen_size(name) == size
        pic = page.evaluate(PICTURE)
        assert pic["width"] <= pic["frameWidth"] + 1 and pic["height"] <= pic["frameHeight"] + 1, pic
        assert abs(pic["width"] - pic["frameWidth"]) <= 1 or abs(pic["height"] - pic["frameHeight"]) <= 1, pic
        assert abs(pic["width"] / pic["height"] - pic["remoteWidth"] / pic["remoteHeight"]) < 0.01, pic

        # Remembered in this browser.
        page.reload()
        expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)
        expect(lock).to_have_attribute("aria-pressed", "true")
        time.sleep(2)
        assert screen_size(name) == size

        # Unticked, the remote screen follows the frame again.
        lock.click()
        expect(lock).to_have_attribute("aria-pressed", "false")
        frame = page.evaluate("[document.querySelector('iframe').clientWidth, document.querySelector('iframe').clientHeight]")
        follows = lambda: all(abs(a - b) <= 8 for a, b in zip(screen_size(name), frame))  # noqa: E731
        assert wait_until(follows), (screen_size(name), frame)
        browser.close()


def test_drag_lands_right(supervisor, desktop, name):
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
        browser, page = signed_in(p, supervisor.base)
        open_desktop(page, supervisor.base, name)
        time.sleep(1)
        x0, y0 = position()
        top = int(in_desktop(name, "xprop", "-id", win, "_NET_FRAME_EXTENTS").split("=")[1].split(",")[2])
        a = (x0 + 150, y0 - top // 2)  # on the title bar
        b = (a[0] + 180, a[1] + 120)
        pic = page.evaluate(PICTURE)
        pa, pb = to_page(pic, *a), to_page(pic, *b)
        page.mouse.move(*pa)
        page.mouse.down()
        for i in range(1, 21):
            page.mouse.move(pa[0] + (pb[0] - pa[0]) * i / 20, pa[1] + (pb[1] - pa[1]) * i / 20)
            time.sleep(0.02)
        page.mouse.up()
        time.sleep(1)
        mouse = in_desktop(name, "xdotool", "getmouselocation")
        mx, my = (int(v) for v in re.search(r"x:(\d+) y:(\d+)", mouse).groups())
        assert abs(mx - b[0]) <= 4 and abs(my - b[1]) <= 4, (mouse, b)
        x1, y1 = position()
        assert abs((x1 - x0) - (b[0] - a[0])) <= 4 and abs((y1 - y0) - (b[1] - a[1])) <= 4, ((x0, y0), (x1, y1), a, b)
        browser.close()


def video_settings_lines(name) -> list[str]:
    """Selkies' log lines about the primary display's video settings."""
    logs = subprocess.run(["docker", "logs", f"deskyard-{name}"], capture_output=True, text=True)
    return [line for line in (logs.stdout + logs.stderr).splitlines()
            if "Applying video settings for 'primary'" in line or "Video parameters changed for 'primary'" in line
            or "settings applied for 'primary'" in line]


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_quality_preset_applies(supervisor, desktop, name, browser_name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        open_desktop(page, supervisor.base, name)
        time.sleep(1)
        before = len(video_settings_lines(name))
        page.get_by_role("button", name="Quality", exact=True).click()
        page.get_by_role("menuitemcheckbox", name=re.compile("^Low")).click()
        # The desktop's stream takes it live.
        applied = lambda: any("Applying video settings" in line for line in video_settings_lines(name)[before:])  # noqa: E731
        assert wait_until(applied), video_settings_lines(name)[before:]
        # A new load of the page streams at it too: Low is 2 Mbit/s at 30 frames a second.
        page.reload()
        expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)
        latest = lambda: (video_settings_lines(name) or [""])[-1]  # noqa: E731
        assert wait_until(lambda: latest().endswith("2000 kbps, 30 fps.")), video_settings_lines(name)[-3:]
        page.get_by_role("button", name="Quality", exact=True).click()
        expect(page.get_by_role("menuitemcheckbox", name=re.compile("^Low"))).to_have_attribute("aria-checked", "true")

        # Lossless, live: full colour restarts the capture, and the stream
        # climbs to 64 Mbit/s with no reload. Selkies' congestion control
        # raises the rate as the path proves it (CC_RATE), which takes a
        # moving picture: a terminal listing files over and over.
        page.wait_for_function(SHOWING, timeout=30000)
        page.evaluate(f"""() => {{ const t = document.querySelector('iframe').contentWindow.selkiesTransport;
            window.__rates = []; t.addEventListener('message', (e) => {{
                if (typeof e.data === 'string' && e.data.startsWith('CC_RATE ')) window.__rates.push(Number(e.data.slice(8))); }}); }}""")
        in_desktop(name, "sh", "-c", "setsid xfce4-terminal --maximize -e \"sh -c 'while :; do ls -lR /usr; done'\" >/dev/null 2>&1 &")
        before = len(video_settings_lines(name))
        page.get_by_role("menuitemcheckbox", name=re.compile("^Lossless")).click()
        restarted = lambda: any("Video parameters changed" in line for line in video_settings_lines(name)[before:])  # noqa: E731
        assert wait_until(restarted), video_settings_lines(name)[before:]
        rates = lambda: page.evaluate("window.__rates")  # noqa: E731
        assert wait_until(lambda: any(r >= 0.9 * 64000 for r in rates()), 60, 1), rates()
        in_desktop(name, "pkill", "-f", "ls -lR", check=False)
        browser.close()


def test_second_viewer(cli, remote, supervisor, name):  # noqa: F811
    # The first page on a desktop owns its screen: its size, and its quality.
    # A second page joins beside it, scaled to fit at the stream's size,
    # however its own window changes, so clicks and marks land right; its
    # stored choices don't touch the first viewer's.
    with sync_playwright() as p:
        browser, a = signed_in(p, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        a.get_by_role("button", name="Quality", exact=True).click()
        a.get_by_role("menuitemcheckbox", name=re.compile("^High")).click()
        size = screen_size(name)
        time.sleep(1)
        before = len(video_settings_lines(name))

        ctx = browser.new_context(viewport={"width": 900, "height": 700})
        ctx.add_init_script("try { localStorage.setItem('deskyard.quality', 'low'); "
                            "localStorage.setItem('deskyard.lockSize', JSON.stringify({width: 640, height: 480})); } catch {}")
        b = ctx.new_page()
        b.goto(supervisor.base + "/login")
        b.fill("#u", "admin")
        b.fill("#p", ADMIN_PASSWORD)
        b.get_by_role("button", name="Sign in").click()
        b.wait_for_url(lambda u: "/login" not in u)
        open_desktop(b, supervisor.base, name)
        b.wait_for_function(f"{SCREEN}.display().owner === false", timeout=15000)
        time.sleep(2)
        # The first viewer's screen and quality stay.
        assert screen_size(name) == size
        assert video_settings_lines(name)[before:] == []
        for page in (a, b):
            page.get_by_role("button", name="Quality", exact=True).click()
            expect(page.get_by_role("menuitemcheckbox", name=re.compile("^High"))).to_have_attribute("aria-checked", "true")
            page.keyboard.press("Escape")
        lock = b.get_by_role("button", name="Lock size", exact=True)
        expect(lock).to_be_disabled()
        expect(lock).to_have_attribute("title", re.compile("first viewer sets the screen size"))

        # The second viewer's window changes; the picture still fits, unstretched.
        b.set_viewport_size({"width": 1000, "height": 650})
        time.sleep(2)
        assert screen_size(name) == size
        pic = b.evaluate(PICTURE)
        assert (pic["remoteWidth"], pic["remoteHeight"]) == size, pic
        assert fits(pic), pic
        # Its pointer lands where it points.
        b.mouse.move(*to_page(pic, 600, 400))
        assert wait_until(lambda: all(abs(v - w) <= 4 for v, w in zip(mouse_location(name), (600, 400))), 5), mouse_location(name)
        # So do the agents' marks.
        remote("-d", name, "click", "300", "200")
        mark = b.locator('[data-mark="click"]')
        expect(mark).to_be_visible(timeout=5000)
        box = mark.bounding_box()
        want = to_page(b.evaluate(PICTURE), 300, 200)
        assert abs(box["x"] + box["width"] / 2 - want[0]) <= 3 and abs(box["y"] + box["height"] / 2 - want[1]) <= 3, (box, want)

        # The first viewer leaves: the second owns the screen now, and its
        # browser's Lock size (640x480) applies; unlocked, the screen follows its window.
        a.close()
        b.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        expect(lock).to_be_enabled()
        expect(lock).to_have_attribute("aria-pressed", "true")
        assert wait_until(lambda: screen_size(name) == (640, 480)), screen_size(name)
        lock.click()
        frame = b.evaluate("[document.querySelector('iframe').clientWidth, document.querySelector('iframe').clientHeight]")
        follows = lambda: all(abs(v - w) <= 8 for v, w in zip(screen_size(name), frame))  # noqa: E731
        assert wait_until(follows), (screen_size(name), frame)
        browser.close()


def test_selkies_saved_settings_ignored(supervisor, desktop, name):
    # Selkies' own saved stream settings and manual size are this page's
    # business only through our bar: gone at load, so they never apply.
    prefix = f"http___127.0.0.1_{supervisor.port}_{name}_screen__"
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        # In every frame before its scripts run, so the client's page has them too.
        page.add_init_script(f"""try {{ for (const [k, v] of [['manual_resolution', 'true'], ['manual_width', '640'],
                ['manual_height', '480'], ['video_bitrate', '500'], ['framerate', '10']])
            localStorage.setItem('{prefix}' + k, v); }} catch {{}}""")
        open_desktop(page, supervisor.base, name)
        time.sleep(2)
        frame = page.evaluate("[document.querySelector('iframe').clientWidth, document.querySelector('iframe').clientHeight]")
        assert all(abs(v - w) <= 8 for v, w in zip(screen_size(name), frame)), (screen_size(name), frame)
        assert not (video_settings_lines(name) or [""])[-1].endswith("500 kbps, 10 fps."), video_settings_lines(name)[-2:]
        # (Selkies saves what it streams with again as it goes.)
        kept = page.evaluate(f"""() => ['manual_resolution', 'manual_width', 'manual_height', 'video_bitrate', 'framerate']
            .map(k => localStorage.getItem('{prefix}' + k))""")
        assert kept[0] != "true" and "640" not in kept and "500" not in kept and "10" not in kept, kept
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
    # POST /api/desktops/{name}/upload. The first event of a hover, in the
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


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_keys_menu(supervisor, desktop, name, browser_name):
    in_desktop(name, "sh", "-c", "setsid xev -name keytest > /tmp/xev.log 2>&1 &")
    win = wait_until(lambda: in_desktop(name, "xdotool", "search", "--name", "^keytest$", check=False).split(), 20)
    assert win, "no xev window"
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        open_desktop(page, supervisor.base, name)
        in_desktop(name, "xdotool", "windowactivate", "--sync", win[-1])
        page.get_by_role("button", name="Keys", exact=True).click()
        page.get_by_role("menuitem", name="Esc", exact=True).click()
        assert wait_until(lambda: "Escape" in in_desktop(name, "cat", "/tmp/xev.log")), "no Escape on the desktop"
        # The keyboard goes back to the desktop.
        page.wait_for_function("document.activeElement === document.querySelector('iframe')", timeout=5000)
        browser.close()


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_stopped_desktop_page(supervisor, admin, desktop, name, browser_name):
    admin.post(f"/api/desktops/{name}/stop")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        page.goto(f"{supervisor.base}/{name}/")
        expect(page.get_by_text("is stopped")).to_be_visible(timeout=15000)
        expect(page.locator("iframe")).to_have_count(0)
        page.get_by_role("button", name="Start").click()
        expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=90000)
        page.wait_for_function(SHOWING, timeout=30000)
        browser.close()


def test_unsupported_browser_message(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.add_init_script("delete window.VideoDecoder")
        page.goto(f"{supervisor.base}/{name}/")
        message = "This browser can't show Deskyard desktops; use a current Chrome, Firefox or Safari"
        expect(page.get_by_text(message)).to_be_visible(timeout=15000)
        expect(page.locator("iframe")).to_have_count(0)
        browser.close()


def test_phone_bar(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width": 390, "height": 760}, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        for n in ("Lock size", "Keys", "Quality", "Download"):
            expect(page.get_by_role("button", name=n, exact=True)).to_be_hidden()
        expect(page.get_by_role("button", name="Clipboard", exact=True)).to_be_visible()
        page.get_by_role("button", name="More", exact=True).click()
        menu = page.get_by_role("menu")
        for n in ("Lock size", "Send Esc", "Quality: Low", "Download…"):
            expect(menu.get_by_text(n, exact=True)).to_be_visible()
        browser.close()


def test_forwards_shown(cli, remote, forwards, supervisor, name):  # noqa: F811
    p = free_port()
    f = forwards(cli, remote, str(p))
    f.wait_for(f"in {p} {p}")
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        chip = page.get_by_role("button", name="Viewers and port mappings")
        expect(chip).to_be_visible(timeout=30000)
        chip.click()
        expect(page.get_by_text(f"cli test: {p} in")).to_be_visible()

        page.goto(f"{supervisor.base}/desktops/{name}")
        expect(page.get_by_text(f"cli test: {p} in")).to_be_visible(timeout=15000)

        page.goto(f"{supervisor.base}/{name}/")
        expect(chip).to_be_visible(timeout=30000)
        assert f.stop() == 0
        # The button stays for the viewers (this page is one); the mapping count goes.
        expect(chip.get_by_text("⇄")).to_have_count(0, timeout=3000)
        browser.close()


def test_agent_presence_and_summary(cli, remote, supervisor, name):  # noqa: F811
    remote("-d", name, "click", "100", "100")
    remote("-d", name, "exec", "--", "true")
    title = "cli test · admin's agent"
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        chip = page.get_by_role("button", name="Viewers and port mappings")
        expect(chip).to_be_visible(timeout=30000)
        expect(chip.locator("[data-agent]")).to_have_attribute("title", title)
        chip.click()
        expect(page.get_by_role("heading", name="Agents")).to_be_visible()
        row = page.get_by_role("button", name=title)
        expect(row).to_be_visible()
        row.hover()
        expect(page.get_by_text("Recently")).to_be_visible()
        expect(page.get_by_text("clicked", exact=True)).to_be_visible()
        expect(page.get_by_text("true · exit 0")).to_be_visible()

        page.goto(f"{supervisor.base}/desktops/{name}")
        expect(page.locator("[data-agent]").first).to_be_visible(timeout=15000)

        page.goto(f"{supervisor.base}/")
        expect(page.locator("[data-agent]").first).to_be_visible(timeout=15000)
        browser.close()


def test_overlay_marks(cli, remote, supervisor, name):  # noqa: F811
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        open_desktop(page, supervisor.base, name)
        time.sleep(1)  # the screen-events stream is open

        remote("-d", name, "click", "200", "150")
        click = page.locator('[data-mark="click"]')
        expect(click).to_be_visible(timeout=5000)
        expect(click).to_contain_text("cli test")
        box = click.bounding_box()
        want = to_page(page.evaluate(PICTURE), 200, 150)
        assert abs(box["x"] + box["width"] / 2 - want[0]) <= 3, (box, want)
        assert abs(box["y"] + box["height"] / 2 - want[1]) <= 3, (box, want)

        # Locked and scaled down, the marks still land on the spot.
        page.get_by_role("button", name="Lock size", exact=True).click()
        page.set_viewport_size({"width": 800, "height": 700})
        page.wait_for_function(f"(() => {{ const f = document.querySelector('iframe'); "
                               f"return {SCREEN}.video().getBoundingClientRect().width <= f.clientWidth + 1; }})()")
        time.sleep(1)
        expect(page.locator("[data-mark]")).to_have_count(0, timeout=5000)
        remote("-d", name, "click", "600", "400")
        expect(click).to_be_visible(timeout=5000)
        box = click.bounding_box()
        pic = page.evaluate(PICTURE)
        assert pic["width"] < pic["remoteWidth"] - 50, pic
        want = to_page(pic, 600, 400)
        assert abs(box["x"] + box["width"] / 2 - want[0]) <= 3, (box, want)
        assert abs(box["y"] + box["height"] / 2 - want[1]) <= 3, (box, want)

        remote("-d", name, "type", "secret-text")
        typing = page.locator('[data-mark="typing"]')
        expect(typing).to_have_text("cli test is typing…", timeout=5000)
        expect(typing).to_have_count(0, timeout=3000)

        remote("-d", name, "key", "ctrl+l")
        expect(page.locator('[data-mark="key"]')).to_have_text("⌃L", timeout=5000)

        marks = page.locator("[data-mark]")
        expect(marks).to_have_count(0, timeout=5000)
        remote("-d", name, "move", "10", "10")
        time.sleep(1.5)
        expect(marks).to_have_count(0)

        assert "secret-text" not in page.content()
        browser.close()


# --- The Audio panel: sound, mic, webcam and mic badges ---------------------

# Selkies' sound, mic and webcam in the frame, from our additions.
MEDIA = f"{SCREEN}?.media()"

FAKE_MEDIA = ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"]


def tone_file(path, freq, seconds=60, rate=48000):
    """A WAV of beeps at freq: a quarter second on, a quarter off, so the
    browser's noise suppression keeps it."""
    frames = bytearray()
    for i in range(seconds * rate):
        on = (i // (rate // 4)) % 2 == 0
        v = int(12000 * math.sin(2 * math.pi * freq * i / rate)) if on else 0
        frames += struct.pack("<h", v)
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(bytes(frames))
    return str(path)


def mic_browser(p, tone):
    """Chromium whose mic hears the tone file, and grants the mic without asking."""
    return p.chromium.launch(args=[*FAKE_MEDIA, f"--use-file-for-fake-audio-capture={tone}"])


def sign_in(browser, base, user="admin", password=ADMIN_PASSWORD, **ctx):
    ctx.setdefault("viewport", {"width": 1280, "height": 860})
    page = browser.new_context(**ctx).new_page()
    page.goto(base + "/login")
    page.fill("#u", user)
    page.fill("#p", password)
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    return page


def record_mic(name, seconds=3, rate=24000) -> list[float]:
    """What an app on the desktop hears from its default microphone."""
    r = subprocess.run(["docker", "exec", "-u", "user", f"deskyard-{name}", "bash", "-lc",
                        f"timeout {seconds} parecord --raw --format=s16le --rate={rate} --channels=1 -d @DEFAULT_SOURCE@; true"],
                       capture_output=True, check=True)
    samples = array.array("h", r.stdout[: len(r.stdout) // 2 * 2])
    return [v / 32768 for v in samples]


def tone_level(samples, freq, rate=24000) -> float:
    """The amplitude of freq in samples (Goertzel)."""
    n = len(samples)
    if n == 0:
        return 0.0
    c = 2 * math.cos(2 * math.pi * round(n * freq / rate) / n)
    s1 = s2 = 0.0
    for x in samples:
        s1, s2 = x + c * s1 - s2, s1
    return 2 * math.sqrt(max(0.0, s1 * s1 + s2 * s2 - c * s1 * s2)) / n


def hears(name, *freqs, absent=()) -> bool:
    samples = record_mic(name)
    levels = {f: tone_level(samples, f) for f in (*freqs, *absent)}
    ok = all(levels[f] > 0.005 for f in freqs) and all(levels[f] < 0.001 for f in absent)
    if not ok:
        print("mic levels", levels)
    return ok


def mic_badge(page, user):
    return page.locator(f'[data-avatar="{user}"] [data-mic-badge]')


def audio_switch(page, label):
    """The Audio panel's switch, with the panel open."""
    if not page.get_by_role("dialog", name="Audio").is_visible():
        page.get_by_role("button", name="Audio", exact=True).click()
    return page.get_by_role("dialog", name="Audio").get_by_role("switch", name=label)


def test_sound_toggle(supervisor, desktop, name):
    # A tone plays on the desktop. Sound is off on a first visit; turned on,
    # the page plays the desktop's sound; it's remembered, with the volume.
    play_tone(name)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--autoplay-policy=no-user-gesture-required"])
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        sound = audio_switch(page, "Desktop sound")
        expect(sound).to_have_attribute("aria-checked", "false")
        time.sleep(3)
        assert quiet(page) > 2.5  # nothing comes while it's off

        sound.click()
        expect(sound).to_have_attribute("aria-checked", "true")
        page.wait_for_function(f"{MEDIA}.sound && {MEDIA}.playing", timeout=15000)
        assert wait_until(lambda: quiet(page) < 1.5, 10), quiet(page)
        page.get_by_role("slider", name="Volume").fill("0.3")
        assert page.evaluate("JSON.parse(localStorage.getItem('deskyard.sound')).volume") == 0.3

        # Remembered: on again after a reload.
        page.reload()
        open_desktop(page, supervisor.base, name)
        page.wait_for_function(f"{MEDIA}.sound && {MEDIA}.playing", timeout=15000)
        sound = audio_switch(page, "Desktop sound")
        expect(sound).to_have_attribute("aria-checked", "true")
        expect(page.get_by_role("slider", name="Volume")).to_have_value("0.3")

        # Off: the desktop stops sending this page its sound.
        sound.click()
        page.wait_for_function(f"!{MEDIA}.sound", timeout=10000)
        time.sleep(4)
        assert quiet(page) > 2.5, quiet(page)
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)


def play_tone(name):
    """A tone on the desktop's speaker for two minutes."""
    in_desktop(name, "bash", "-lc", "python3 -c \"import math,struct,sys; sys.stdout.buffer.write(b''.join("
               "struct.pack('<h', int(8000*math.sin(2*math.pi*440*i/48000))) for i in range(48000*120)))\" > /tmp/tone.raw")
    subprocess.Popen(["docker", "exec", "-u", "user", f"deskyard-{name}", "bash", "-lc",
                      "paplay --raw --format=s16le --rate=48000 --channels=1 /tmp/tone.raw"])


def quiet(page) -> float:
    """How long ago the stream last brought the page sound, in seconds."""
    return page.evaluate(f"(performance.now() - {MEDIA}.soundAt) / 1000")


def test_sound_per_viewer(supervisor, desktop, name):
    # Sound is each viewer's own: the display's owner and a page beside it
    # turn theirs on and off without touching the other's.
    play_tone(name)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--autoplay-policy=no-user-gesture-required"])
        a = sign_in(browser, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        b = sign_in(browser, supervisor.base)
        open_desktop(b, supervisor.base, name)
        b.wait_for_function(f"{SCREEN}.display().owner === false", timeout=15000)

        audio_switch(a, "Desktop sound").click()
        assert wait_until(lambda: quiet(a) < 1.5, 10), quiet(a)
        time.sleep(3)
        assert quiet(b) > 2.5, quiet(b)

        audio_switch(b, "Desktop sound").click()
        assert wait_until(lambda: quiet(b) < 1.5, 10), quiet(b)
        audio_switch(a, "Desktop sound").click()
        a.wait_for_function(f"!{MEDIA}.sound", timeout=10000)
        time.sleep(4)
        assert quiet(a) > 2.5, quiet(a)
        assert quiet(b) < 1.5, quiet(b)
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)


def test_joiner_keeps_owner_sound(supervisor, desktop, name):
    # Pages joining beside the owner with their sound off (first visits)
    # don't turn the owner's sound off.
    play_tone(name)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--autoplay-policy=no-user-gesture-required"])
        a = sign_in(browser, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        audio_switch(a, "Desktop sound").click()
        assert wait_until(lambda: quiet(a) < 1.5, 10), quiet(a)
        for _ in range(2):
            b = sign_in(browser, supervisor.base)
            open_desktop(b, supervisor.base, name)
            time.sleep(4)
            assert quiet(a) < 1.5 and a.evaluate(f"{MEDIA}.sound"), quiet(a)
            assert quiet(b) > 2.5, quiet(b)
            b.context.close()
            time.sleep(2)
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)


def test_mic_badge_and_reload(supervisor, admin, desktop, name, tmp_path):
    # The mic goes into the desktop, where apps hear it, and everyone sees
    # the badge. After a reload it's off, and the badge is gone.
    tone = tone_file(tmp_path / "tone.wav", 700)
    with sync_playwright() as p:
        browser = mic_browser(p, tone)
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        wall = sign_in(browser, supervisor.base)
        expect(wall.get_by_text(name, exact=True).first).to_be_visible(timeout=15000)

        mic = audio_switch(page, "My mic")
        expect(mic).to_have_attribute("aria-checked", "false")
        expect(page.get_by_text(f"Everyone on {name} sees when your mic is on")).to_be_visible()
        expect(mic_badge(page, "admin")).to_have_count(0)
        mic.click()
        expect(mic).to_have_attribute("aria-checked", "true")
        page.wait_for_function(f"{MEDIA}.mic", timeout=15000)
        expect(mic_badge(page, "admin")).to_be_visible(timeout=10000)
        expect(mic_badge(wall, "admin")).to_be_visible(timeout=10000)
        assert admin.desktop(name)["mics"] == ["admin"]
        assert wait_until(lambda: hears(name, 700), 20, 1)
        # The meter moves, and the bar's Audio button says the mic is live.
        assert wait_until(lambda: page.evaluate(f"{SCREEN}.micLevel()") > 0.01, 10, 0.1)
        expect(page.get_by_role("button", name="Audio", exact=True)).to_have_attribute("title", re.compile("mic is on"))

        page.reload()
        open_desktop(page, supervisor.base, name)
        expect(mic_badge(page, "admin")).to_have_count(0, timeout=10000)
        expect(mic_badge(wall, "admin")).to_have_count(0, timeout=10000)
        assert admin.desktop(name)["mics"] == []
        expect(audio_switch(page, "My mic")).to_have_attribute("aria-checked", "false")
        time.sleep(2)
        assert page.evaluate(f"{MEDIA}.mic") is False
        assert admin.desktop(name)["mics"] == []
        browser.close()


def test_mic_quick_off_on_keeps_badge(supervisor, admin, desktop, name, tmp_path):
    # Off and straight back on (a double click): the mic ends up live, and
    # so does its badge. The badge always matches the live mic.
    tone = tone_file(tmp_path / "t.wav", 700)
    with sync_playwright() as p:
        browser = mic_browser(p, tone)
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        mic = audio_switch(page, "My mic")
        mic.click()
        page.wait_for_function(f"{MEDIA}.mic", timeout=15000)
        expect(mic_badge(page, "admin")).to_be_visible(timeout=10000)
        outcomes = []
        for delay in (0, 50, 150):
            mic.click()
            page.wait_for_timeout(delay)
            mic.click()
            time.sleep(4)
            outcomes.append((delay, page.evaluate(f"{MEDIA}.mic"), admin.desktop(name)["mics"], mic.get_attribute("aria-checked")))
        assert all(live and mics == ["admin"] and on == "true" for _, live, mics, on in outcomes), outcomes
        # And on and straight back off: off, with no badge.
        mic.click()
        page.wait_for_function(f"!{MEDIA}.mic", timeout=10000)
        expect(mic_badge(page, "admin")).to_have_count(0, timeout=10000)
        mic.click()
        mic.click()
        time.sleep(4)
        assert page.evaluate(f"{MEDIA}.mic") is False
        assert admin.desktop(name)["mics"] == []
        browser.close()


# Watches, in the page every 30 ms, that a mic live before and after a look
# at the supervisor has its badge there.
BADGE_WATCH = """(name) => {
  window.__viol = []; window.__n = 0;
  const live = () => document.querySelector('iframe')?.contentWindow?.deskyardScreen?.media?.().mic === true;
  window.__w = setInterval(async () => {
    const before = live();
    let d;
    try { d = await (await fetch('/api/desktops/' + name)).json(); } catch { return; }
    window.__n++;
    if (before && live() && !d.mics.includes('admin')) window.__viol.push(d.mics);
  }, 30);
}"""


def test_mic_badge_on_a_slow_network(supervisor, admin, desktop, name, tmp_path):
    # Badge reports take 400 ms. Off, then on-off-on within one round trip:
    # the mic never goes live while the supervisor says it's off.
    with sync_playwright() as p:
        browser = mic_browser(p, tone_file(tmp_path / "t.wav", 700))
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        mic = audio_switch(page, "My mic")
        mic.click()
        page.wait_for_function(f"{MEDIA}.mic", timeout=15000)
        time.sleep(1)
        page.evaluate("""() => { const f = window.fetch; window.fetch = async (u, o) => {
            if (/\\/(mic|cam)$/.test(String(u))) await new Promise((r) => setTimeout(r, 400)); return f(u, o); }; }""")
        page.evaluate(BADGE_WATCH, name)
        mic.click()
        page.wait_for_function(f"!{MEDIA}.mic", timeout=5000)
        page.wait_for_timeout(50)
        mic.click()
        mic.click()
        mic.click()
        time.sleep(3)
        violations, checks = page.evaluate("[window.__viol, window.__n]")
        assert checks > 30 and violations == [], (violations, checks)
        assert page.evaluate(f"{MEDIA}.mic") is True and admin.desktop(name)["mics"] == ["admin"]
        browser.close()


def test_cam_badge(supervisor, admin, desktop, name):
    # Whose camera is on shows like the mic, from the page's report on its
    # stream. (Numbered past the page's own reports, which come first.)
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        open_desktop(page, supervisor.base, name)
        tab = page.evaluate(f"{SCREEN}.tab")
        r = page.evaluate("""async (tab) => (await fetch(location.pathname.replace(/\\/$/, '').replace(/^\\//, '/api/desktops/') + '/cam',
            {method: 'POST', body: JSON.stringify({on: true, tab, seq: 1e9})})).status""", tab)
        assert r == 204
        expect(page.locator('[data-avatar="admin"] [data-cam-badge]')).to_be_visible(timeout=10000)
        assert admin.desktop(name)["cams"] == ["admin"]
        page.reload()
        open_desktop(page, supervisor.base, name)
        expect(page.locator('[data-avatar="admin"] [data-cam-badge]')).to_have_count(0, timeout=10000)
        assert admin.desktop(name)["cams"] == []
        browser.close()


def test_two_mics(supervisor, admin, desktop, name, tmp_path):
    # Two viewers' mics are mixed into the desktop's one. One turning theirs
    # off, or leaving, never takes the other's away.
    user = "u" + uuid.uuid4().hex[:5]
    r = admin.post("/api/accounts", json={"username": user, "password": "pw-" + user, "admin": False, "desktops": [name]})
    assert r.status_code == 201, r.text
    try:
        with sync_playwright() as p:
            ba = mic_browser(p, tone_file(tmp_path / "a.wav", 500))
            bb = mic_browser(p, tone_file(tmp_path / "b.wav", 1300))
            a = sign_in(ba, supervisor.base)
            open_desktop(a, supervisor.base, name)
            b = sign_in(bb, supervisor.base, user, "pw-" + user)
            open_desktop(b, supervisor.base, name)

            audio_switch(a, "My mic").click()
            a.wait_for_function(f"{MEDIA}.mic", timeout=15000)
            audio_switch(b, "My mic").click()
            b.wait_for_function(f"{MEDIA}.mic", timeout=15000)
            for page in (a, b):
                expect(mic_badge(page, "admin")).to_be_visible(timeout=10000)
                expect(mic_badge(page, user)).to_be_visible(timeout=10000)
            assert admin.desktop(name)["mics"] == sorted(["admin", user])
            assert wait_until(lambda: hears(name, 500, 1300), 20, 1)

            # One turns theirs off: the other's badge and sound stay.
            audio_switch(a, "My mic").click()
            expect(audio_switch(a, "My mic")).to_have_attribute("aria-checked", "false")
            for page in (a, b):
                expect(mic_badge(page, "admin")).to_have_count(0, timeout=10000)
                expect(mic_badge(page, user)).to_be_visible()
            assert admin.desktop(name)["mics"] == [user]
            assert b.evaluate(f"{MEDIA}.mic") is True
            assert wait_until(lambda: hears(name, 1300, absent=[500]), 20, 1)

            # The first viewer leaves: the other's mic still reaches the desktop.
            ba.close()
            assert wait_until(lambda: admin.desktop(name)["viewers"] == [user])
            time.sleep(2)
            assert admin.desktop(name)["mics"] == [user]
            assert b.evaluate(f"{MEDIA}.mic") is True
            if not wait_until(lambda: hears(name, 1300), 20, 1):
                # (A desktop whose sound server came up before PipeWire did this: fixed in the image.)
                state = docker("exec", "-u", "user", f"deskyard-{name}", "bash", "-lc",
                               "pactl info | grep Default; pactl list short sinks; pactl list short sources; pactl list short sink-inputs").stdout
                raise AssertionError(f"B's mic no longer reaches the desktop; B: {b.evaluate(f'JSON.stringify({MEDIA})')}\n{state}")
            bb.close()
    finally:
        admin.delete(f"/api/accounts/{user}")


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_webcam_disabled_without_device(supervisor, admin, desktop, name, browser_name):
    # This machine has no v4l2loopback device, so desktops get no camera.
    assert admin.desktop(name)["webcam"] is False
    assert docker("inspect", "-f", "{{json .HostConfig.Devices}}", f"deskyard-{name}").stdout.strip() in ("null", "[]")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        open_desktop(page, supervisor.base, name)
        camera = audio_switch(page, "My camera")
        expect(camera).to_be_disabled()
        expect(page.get_by_role("dialog", name="Audio").get_by_text("This machine has no virtual camera for desktops")).to_be_visible()
        # An admin can't give it one either.
        page.goto(f"{supervisor.base}/desktops/{name}?tab=settings")
        expect(page.get_by_text("This machine has no virtual camera for desktops (v4l2loopback).")).to_be_visible(timeout=15000)
        browser.close()
