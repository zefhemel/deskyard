"""The web UI in a real browser, against the real supervisor and a real desktop.

The desktop page's tests run in Chromium, Firefox and WebKit. Firefox is
skipped where Playwright's Firefox can't start on the machine.
"""

import re
import subprocess
import time

import pytest

from playwright.sync_api import Error as PlaywrightError
from playwright.sync_api import expect, sync_playwright

from conftest import ADMIN_PASSWORD, free_port
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
