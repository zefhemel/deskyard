"""The desktop page over plain http on a LAN address: an insecure context.

Browsers treat only localhost as secure over http, so Chromium reaches the
test supervisor as http://deskyard.test:<port>/, mapped to 127.0.0.1. There
the browser has no WebCodecs, AudioWorklet, mic, camera or async clipboard:
Selkies' client streams JPEG and decodes sound in WASM, and the page says
which features need https.
"""

import re
import time
from io import BytesIO

import pytest
from PIL import Image, ImageStat
from playwright.sync_api import expect, sync_playwright

import netchange
from conftest import ADMIN_PASSWORD
from helpers import MEDIA, PICTURE, SCREEN, SHOWING, fits, in_desktop, put, screen_size, to_page, wait_loaded, wait_until
from test_ui_audio import play_tone, quiet
from test_ui_pointer_and_drop import DRAG

HOST = "deskyard.test"
NEEDS_HTTPS = "Needs https, see running on a LAN"


def insecure_base(supervisor) -> str:
    return f"http://{HOST}:{supervisor.port}"


def launch(p, *args):
    return p.chromium.launch(args=[f"--host-resolver-rules=MAP {HOST} 127.0.0.1", *args])


def sign_in(browser, base, **ctx):
    ctx.setdefault("viewport", {"width": 1280, "height": 860})
    page = browser.new_context(**ctx).new_page()
    page.goto(base + "/login")
    page.fill("#u", "admin")
    page.fill("#p", ADMIN_PASSWORD)
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    return page


def open_desktop(page, base, name):
    seen = netchange.failures(page)
    page.goto(f"{base}/{name}/")
    wait_loaded(page, f"{SHOWING} && {SCREEN}.display()?.width > 0 && {SCREEN}.display()?.height > 0", seen)
    expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)


def has_picture(page) -> bool:
    """The screen shows the desktop: its light panel along the top, where an
    undecodable stream leaves the canvas black."""
    pic = page.evaluate(PICTURE)
    shot = page.screenshot(clip={"x": pic["left"], "y": pic["top"], "width": pic["width"], "height": 16})
    return ImageStat.Stat(Image.open(BytesIO(shot)).convert("L")).mean[0] > 150


def test_plain_http_screen(supervisor, desktop, name, remote):
    base = insecure_base(supervisor)
    in_desktop(name, "sh", "-c", "setsid xev -name keytest > /tmp/xev.log 2>&1 &")
    win = wait_until(lambda: in_desktop(name, "xdotool", "search", "--name", "^keytest$", check=False).split(), 20)
    assert win, "no xev window"
    with sync_playwright() as p:
        browser = launch(p)
        page = sign_in(browser, base)
        assert page.evaluate("window.isSecureContext") is False
        assert page.evaluate("typeof VideoDecoder") == "undefined"
        open_desktop(page, base, name)
        frame = page.frames[1]
        assert frame.evaluate("deskyardScreen.insecure") is True

        # The bar says so, pointing at the README.
        note = page.get_by_role("link", name="Not secure: some features off")
        expect(note).to_be_visible()
        assert note.get_attribute("href").endswith("#on-a-lan")

        # The picture: JPEG, drawn on Selkies' canvas, showing the desktop.
        pic = page.evaluate(PICTURE)
        assert abs(pic["width"] - pic["frameWidth"]) <= 2 and abs(pic["height"] - pic["frameHeight"]) <= 2, pic
        assert wait_until(lambda: has_picture(page)), "no picture"

        # Input: the pointer lands where it's put, and keys arrive.
        page.mouse.move(*to_page(pic, 400, 300))
        page.mouse.move(*to_page(pic, 420, 310))
        loc = lambda: tuple(int(v) for v in re.search(r"x:(\d+) y:(\d+)", in_desktop(name, "xdotool", "getmouselocation")).groups())  # noqa: E731
        assert wait_until(lambda: max(abs(a - b) for a, b in zip(loc(), (420, 310))) <= 3), loc()
        in_desktop(name, "xdotool", "windowactivate", "--sync", win[-1])
        page.keyboard.press("q")
        assert wait_until(lambda: "keysym 0x71, q" in in_desktop(name, "cat", "/tmp/xev.log")), "no q on the desktop"

        # Clipboard, by the popover: both ways, and it says sync is off.
        page.get_by_role("button", name="Clipboard").click()
        expect(page.get_by_text("automatic clipboard sync is off")).to_be_visible()
        expect(page.get_by_text(NEEDS_HTTPS)).to_be_visible()
        page.locator("textarea").fill("from-the-http-page")
        page.get_by_role("button", name=f"Paste into {name}").click()
        clip = lambda: in_desktop(name, "xclip", "-o", "-selection", "clipboard", check=False)  # noqa: E731
        assert wait_until(lambda: clip() == "from-the-http-page"), clip()
        in_desktop(name, "sh", "-c", "printf from-the-desktop-9 | setsid xclip -selection clipboard")
        if not page.get_by_text("Send to").is_visible():
            page.get_by_role("button", name="Clipboard").click()
        expect(page.get_by_text("from-the-desktop-9")).to_be_visible(timeout=15000)
        page.keyboard.press("Escape")

        # Mic and camera are off, and say why.
        page.get_by_role("button", name="Audio", exact=True).click()
        panel = page.get_by_role("dialog", name="Audio")
        expect(panel.get_by_role("switch", name="Desktop sound")).to_be_enabled()
        expect(panel.get_by_role("switch", name="My mic")).to_be_disabled()
        expect(panel.get_by_role("switch", name="My camera")).to_be_disabled()
        expect(panel.get_by_text(NEEDS_HTTPS)).to_have_count(2)
        page.keyboard.press("Escape")

        # The agent overlay marks an agent's click where it lands.
        remote("-d", name, "click", "200", "150")
        mark = page.locator('[data-mark="click"]')
        expect(mark).to_be_visible(timeout=5000)
        box = mark.bounding_box()
        want = to_page(page.evaluate(PICTURE), 200, 150)
        assert abs(box["x"] + box["width"] / 2 - want[0]) <= 3 and abs(box["y"] + box["height"] / 2 - want[1]) <= 3, (box, want)

        # Lock size: the remote screen keeps its size, scaled to fit.
        size = screen_size(name)
        page.get_by_role("button", name="Lock size", exact=True).click()
        page.set_viewport_size({"width": 900, "height": 700})
        page.wait_for_function(f"{SCREEN}.video().getBoundingClientRect().width <= document.querySelector('iframe').clientWidth + 1")
        time.sleep(1)
        assert screen_size(name) == size
        assert fits(page.evaluate(PICTURE)), page.evaluate(PICTURE)
        browser.close()


def test_plain_http_upload_and_download(supervisor, desktop, name, tmp_path):
    base = insecure_base(supervisor)
    put(name, "docs/hello.txt", b"hello over http\n")
    with sync_playwright() as p:
        browser = launch(p)
        page = sign_in(browser, base, accept_downloads=True)
        open_desktop(page, base, name)

        # Upload: a drop on the screen lands in ~/Desktop.
        files = [{"name": "dropped-http.txt", "content": "dropped over http\n"}]
        assert page.frames[1].evaluate(DRAG, ["overlayInput", "dragenter", files]) is True
        expect(page.get_by_text(f"Drop to copy into {name}")).to_be_visible()
        assert page.evaluate(DRAG, ["overlay", "drop", files]) is True
        expect(page.get_by_text("Copied dropped-http.txt")).to_be_visible(timeout=20000)

        # Download.
        page.get_by_role("button", name="Download", exact=True).click()
        dialog = page.get_by_role("dialog")
        dialog.get_by_role("button", name="docs").click()
        with page.expect_download() as d:
            dialog.get_by_role("link", name="Download hello.txt").click()
        path = tmp_path / "got.txt"
        d.value.save_as(path)
        assert path.read_bytes() == b"hello over http\n"
        browser.close()
    assert in_desktop(name, "cat", "/workspace/Desktop/dropped-http.txt") == "dropped over http\n"


@pytest.mark.serial  # seconds of quiet, timed
def test_plain_http_sound_and_quality(supervisor, desktop, name):
    base = insecure_base(supervisor)
    play_tone(name)
    with sync_playwright() as p:
        browser = launch(p, "--autoplay-policy=no-user-gesture-required")
        # A secure page owns the display, streaming H.264. An http page
        # beside it can't decode that, so it asks for JPEG, which both show.
        secure = sign_in(browser, supervisor.base)
        seen = netchange.failures(secure)
        secure.goto(f"{supervisor.base}/{name}/")
        wait_loaded(secure, f"{SHOWING} && {SCREEN}.display().owner === true", seen)
        page = sign_in(browser, base)
        open_desktop(page, base, name)
        page.wait_for_function(f"{SCREEN}.display().owner === false && {SCREEN}.display().settings?.encoder === 'jpeg'", timeout=15000)
        assert wait_until(lambda: has_picture(page) and has_picture(secure)), "no picture"

        # Sound plays: Opus in WASM, through the AudioWorklet stand-in.
        page.get_by_role("button", name="Audio", exact=True).click()
        page.get_by_role("dialog", name="Audio").get_by_role("switch", name="Desktop sound").click()
        page.wait_for_function(f"{MEDIA}.sound && {MEDIA}.playing", timeout=15000)
        assert wait_until(lambda: quiet(page) < 1.5, 10), quiet(page)
        assert wait_until(lambda: page.frames[1].evaluate("window.currentAudioLevel || 0") > 5, 10), "nothing played"
        page.keyboard.press("Escape")

        # Quality: the presets are a JPEG quality and frame rate here.
        page.get_by_role("button", name="Quality", exact=True).click()
        page.get_by_role("menuitemcheckbox", name=re.compile("^Low")).click()
        beside = sign_in(browser, base)
        open_desktop(beside, base, name)
        beside.wait_for_function(f"{SCREEN}.display().settings?.framerate === 15", timeout=15000)
        s = beside.evaluate(f"{SCREEN}.display().settings")
        assert (s["encoder"], s["jpeg_quality"], s["framerate"]) == ("jpeg", 30, 15), s
        beside.get_by_role("button", name="Quality", exact=True).click()
        expect(beside.get_by_role("menuitemcheckbox", name=re.compile("^Low"))).to_have_attribute("aria-checked", "true")
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)
