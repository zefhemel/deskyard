"""The desktop page's picture: quality, a second viewer beside the first, a
tab in the background, and Selkies' own saved settings."""

import re
import time

import pytest
from playwright.sync_api import expect, sync_playwright

import netchange
from conftest import ADMIN_PASSWORD
from helpers import (BROWSERS, PICTURE, SCREEN, SHOWING, fits, frame_size, in_desktop, mouse_location, open_desktop, screen_size,
                     sign_in, signed_in, to_page, video_settings_lines, wait_loaded, wait_until)


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


def test_second_viewer(cli, remote, supervisor, name):
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
        b.goto(supervisor.base + "/")
        b.wait_for_url("**/login**")
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
            # The 900px second viewer uses the compact toolbar.
            if page is b:
                page.get_by_role("button", name="More", exact=True).click()
                quality = page.get_by_role("menuitemcheckbox", name="Quality: High", exact=True)
                expect(page.get_by_role("menuitemcheckbox", name="Lock size", exact=True)).to_have_count(0)
            else:
                page.get_by_role("button", name="Quality", exact=True).click()
                quality = page.get_by_role("menuitemcheckbox", name=re.compile("^High"))
            expect(quality).to_have_attribute("aria-checked", "true")
            page.keyboard.press("Escape")
        # A wider second viewer shows the disabled direct control too.
        b.set_viewport_size({"width": 1280, "height": 700})
        lock = b.get_by_role("button", name="Lock size", exact=True)
        expect(lock).to_be_disabled()
        expect(lock).to_have_attribute("title", re.compile("first viewer sets the screen size"))

        # The second viewer's window changes; the picture still fits, unstretched.
        b.set_viewport_size({"width": 1000, "height": 650})
        assert wait_until(lambda: fits(b.evaluate(PICTURE))), b.evaluate(PICTURE)
        time.sleep(1)  # an owner's page would have asked for its frame's size by now
        assert screen_size(name) == size
        pic = b.evaluate(PICTURE)
        assert (pic["remoteWidth"], pic["remoteHeight"]) == size, pic
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
        b.get_by_role("button", name="More", exact=True).click()
        lock = b.get_by_role("menuitemcheckbox", name="Lock size", exact=True)
        expect(lock).to_be_enabled()
        expect(lock).to_have_attribute("aria-checked", "true")
        assert wait_until(lambda: screen_size(name) == (640, 480)), screen_size(name)
        lock.click()
        frame = frame_size(b)
        follows = lambda: all(abs(v - w) <= 8 for v, w in zip(screen_size(name), frame))  # noqa: E731
        assert wait_until(follows), (screen_size(name), frame)
        browser.close()


# Counts the frames Selkies' picture shows, from the start (an init script:
# it runs in the frame before Selkies' client). Chromium shows the stream
# in a <video>.
COUNT_FRAMES = """(() => {
    if (window === top) return;
    window.__frames = 0;
    const seen = new WeakSet();
    setInterval(() => {
        const v = window.deskyardScreen && window.deskyardScreen.video();
        if (!(v instanceof HTMLVideoElement) || seen.has(v)) return;
        seen.add(v);
        const count = () => { window.__frames++; v.requestVideoFrameCallback(count); };
        v.requestVideoFrameCallback(count);
    }, 20);
})()"""


FRAMES = "document.querySelector('iframe')?.contentWindow?.__frames ?? 0"


# Puts the page and its frame in the background as a browser does for a
# hidden tab: document.hidden, then visibilitychange.
HIDE = """() => {
    for (const doc of [document, document.querySelector("iframe").contentDocument]) {
        Object.defineProperty(doc, "hidden", {configurable: true, get: () => true});
        Object.defineProperty(doc, "visibilityState", {configurable: true, get: () => "hidden"});
        doc.dispatchEvent(new Event("visibilitychange"));
    }
}"""


@pytest.mark.perf
@pytest.mark.serial  # the first frame within 0.5 s
@pytest.mark.parametrize("lose_initial_frame", [False, True])
def test_background_tab_doesnt_stall(supervisor, desktop, name, lose_initial_frame):
    # The only viewer's tab goes to the background. Its client would stop the
    # desktop's stream; a viewer joining after that must still get the picture.
    in_desktop(name, "sh", "-c", "setsid xfce4-terminal >/dev/null 2>&1 &")
    with sync_playwright() as p:
        browser = p.chromium.launch()
        a = sign_in(browser, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        # Selkies' client takes it as hidden (and would send STOP_VIDEO).
        with a.expect_console_message(lambda m: "Tab is hidden, stopping video" in m.text, timeout=5000):
            a.evaluate(HIDE)
        a.wait_for_timeout(3000)

        b = sign_in(browser, supervisor.base)
        b.context.add_init_script(COUNT_FRAMES)
        if lose_initial_frame:
            # Make the missed join frame deterministic: use the page socket
            # and withhold video until the client requests a replacement.
            b.context.route("**/screen/", lambda route: route.fulfill(
                status=302, headers={"Location": route.request.url + "?socket_worker=false"}))
            b.context.add_init_script("""(() => {
                if (window === top) return;
                const Socket = window.WebSocket;
                window.WebSocket = class extends Socket {
                    constructor(...args) {
                        super(...args);
                        this.waitingForKeyframeRequest = true;
                        this.addEventListener('message', event => {
                            if (!this.waitingForKeyframeRequest || !(event.data instanceof ArrayBuffer)) return;
                            const kind = new Uint8Array(event.data)[0];
                            if (kind === 3 || kind === 4) event.stopImmediatePropagation();
                        });
                    }
                    send(data) {
                        if (data === 'REQUEST_KEYFRAME') this.waitingForKeyframeRequest = false;
                        return super.send(data);
                    }
                };
            })();""")
        # Selkies' client asks for a key frame itself after 3 s without a frame.
        asked = []
        events = []
        b.on("console", lambda m: events.append(m.text))
        b.on("pageerror", lambda error: events.append(str(error)))
        b.on("console", lambda m: "keyframe" in m.text and asked.append(m.text))
        seen = netchange.failures(b)
        b.goto(f"{supervisor.base}/{name}/")
        wait_loaded(b, f"{SCREEN}?.state() === 'connected'", seen)
        joined = time.time()
        b.wait_for_function(f"{FRAMES} > 0", timeout=5000)
        first = time.time() - joined
        print(f"first frame {first:.2f} s after joining")
        if first >= 0.5:
            print("\n".join(event for event in events if "Deskyard" in event or "reset" in event.lower() or "defined" in event))
        # At once, not at the idle capture's next keepalive grab (1 s).
        assert first < 0.5, (first, asked, events[-60:])
        # And the picture keeps coming as the screen changes.
        shown = b.evaluate(FRAMES)
        in_desktop(name, "xdotool", "type", "--delay", "50", "still streaming")
        assert wait_until(lambda: b.evaluate(FRAMES) > shown, 5), b.evaluate(FRAMES)
        browser.close()


# In the client's frame: the messages that drop the client's video decoders
# as a page joins beside the owner (the display's settings, the desktop
# page's size lock) wait until its first frame shows, as on a slow machine
# where the join's key frame comes first.
LATE_JOIN = """(() => {
    if (window === top) return;
    const held = [];
    let shown = false;
    window.addEventListener("message", (e) => {
        if (shown || e.source !== window || !e.data) return;
        if (!["serverSettings", "setScaleLocally", "setManualResolution"].includes(e.data.type)) return;
        e.stopImmediatePropagation();
        held.push(e.data);
    }, true);
    const t = setInterval(() => {
        if (!(window.__frames > 0)) return;
        clearInterval(t);
        shown = true;
        window.__released = window.__frames;
        for (const m of held) window.postMessage(m, location.origin);
    }, 5);
})()"""


def test_late_join_still_gets_a_key_frame(supervisor, desktop, name):
    # The join's key frame came before the page dropped its decoders: Selkies
    # takes the page's own request for another only a second after the
    # join's, and the screen is still, so nothing else would come.
    with sync_playwright() as p:
        browser = p.chromium.launch()
        a = sign_in(browser, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        b = sign_in(browser, supervisor.base)
        b.context.add_init_script(COUNT_FRAMES)
        b.context.add_init_script(LATE_JOIN)
        seen = netchange.failures(b)
        b.goto(f"{supervisor.base}/{name}/")
        released = "document.querySelector('iframe')?.contentWindow?.__released"
        wait_loaded(b, f"{released} !== undefined", seen)
        t0 = time.time()
        b.wait_for_function(f"({FRAMES}) > {released}", timeout=5000)
        # At once; without it, 3 s or never. (Loose: it runs beside other tests.)
        assert time.time() - t0 < 1, time.time() - t0
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
        frame = frame_size(page)
        follows = lambda: all(abs(v - w) <= 8 for v, w in zip(screen_size(name), frame))  # noqa: E731
        assert wait_until(follows, 10), (screen_size(name), frame)
        assert not (video_settings_lines(name) or [""])[-1].endswith("500 kbps, 10 fps."), video_settings_lines(name)[-2:]
        # (Selkies saves what it streams with again as it goes.)
        kept = page.evaluate(f"""() => ['manual_resolution', 'manual_width', 'manual_height', 'video_bitrate', 'framerate']
            .map(k => localStorage.getItem('{prefix}' + k))""")
        assert kept[0] != "true" and "640" not in kept and "500" not in kept and "10" not in kept, kept
        browser.close()
