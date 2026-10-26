"""The web UI in a real browser, against the real supervisor and a real desktop:
signing in, and the desktop page's bar. The bar's tests run in Chromium,
Firefox and WebKit (see conftest.py for when Firefox and WebKit run)."""

import re
import time

import pytest
from playwright.sync_api import expect, sync_playwright

from conftest import docker
from helpers import (BROWSERS, PICTURE, SCREEN, SHOWING, audio_switch, fits, frame_size, in_desktop, open_desktop,
                     screen_size, signed_in, video_settings_lines, wait_until)


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
        # The picture is scaled to fit the frame; the remote screen keeps its size.
        assert wait_until(lambda: fits(page.evaluate(PICTURE))), page.evaluate(PICTURE)
        time.sleep(1)  # an unlocked page would have asked for its frame's size by now
        assert screen_size(name) == size

        # Remembered in this browser.
        page.reload()
        expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)
        page.get_by_role("button", name="More", exact=True).click()
        lock = page.get_by_role("menuitemcheckbox", name="Lock size", exact=True)
        expect(lock).to_have_attribute("aria-checked", "true")
        page.wait_for_function(SHOWING, timeout=30000)
        assert wait_until(lambda: fits(page.evaluate(PICTURE))), page.evaluate(PICTURE)
        time.sleep(1)  # an unlocked page would have asked for its frame's size by now
        assert screen_size(name) == size

        # Unticked through the compact toolbar's More menu, the screen follows again.
        lock.click()
        page.get_by_role("button", name="More", exact=True).click()
        expect(lock).to_have_attribute("aria-checked", "false")
        page.keyboard.press("Escape")
        frame = frame_size(page)
        follows = lambda: all(abs(a - b) <= 8 for a, b in zip(screen_size(name), frame))  # noqa: E731
        assert wait_until(follows), (screen_size(name), frame)
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
    admin.post(f"/api/environments/{name}/stop")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name)
        page.goto(f"{supervisor.base}/{name}/")
        expect(page.get_by_text("is stopped")).to_be_visible(timeout=15000)
        expect(page.locator("iframe")).to_have_count(0)
        page.get_by_role("button", name="Start").click()
        expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=90000)
        page.wait_for_function(SHOWING, timeout=30000)
        browser.close()


def test_no_webcodecs_streams_jpeg(supervisor, desktop, name):
    # A browser without WebCodecs (as over plain http) gets Selkies' JPEG
    # stream instead of a refusal, and the Quality presets are JPEG's.
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.add_init_script("delete window.VideoDecoder")
        open_desktop(page, supervisor.base, name)
        page.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        # Secure here: none of the plain-http notes.
        expect(page.get_by_text("Not secure: some features off")).to_have_count(0)
        before = len(video_settings_lines(name))
        page.get_by_role("button", name="Quality", exact=True).click()
        page.get_by_role("menuitemcheckbox", name=re.compile("^Low")).click()
        applied = lambda: any("Applying video settings" in line for line in video_settings_lines(name)[before:])  # noqa: E731
        assert wait_until(applied), video_settings_lines(name)[before:]
        assert any(" jpeg " in line for line in video_settings_lines(name)), video_settings_lines(name)[-3:]
        browser.close()


@pytest.mark.parametrize("viewport", [{"width": 390, "height": 760}, {"width": 744, "height": 1133},
                                      {"width": 874, "height": 402}, {"width": 375, "height": 600}])
def test_phone_bar(supervisor, desktop, name, viewport):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport=viewport, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        for n in ("Lock size", "Keys", "Quality", "Download"):
            expect(page.get_by_role("button", name=n, exact=True)).to_be_hidden()
        expect(page.get_by_role("button", name="Clipboard", exact=True)).to_be_visible()
        for label in ("Audio", "Clipboard", "More"):
            box = page.get_by_role("button", name=label, exact=True).bounding_box()
            assert box and box["x"] >= 0 and box["x"] + box["width"] <= viewport["width"], (label, box)
        page.get_by_role("button", name="More", exact=True).click()
        menu = page.get_by_role("menu")
        def menu_fits():
            box = menu.bounding_box()
            return box and box["y"] >= 0 and box["y"] + box["height"] <= page.viewport_size["height"]
        assert wait_until(menu_fits, 5), menu.bounding_box()
        # Rotation/browser chrome can shrink an already-open menu's space.
        page.set_viewport_size({"width": viewport["width"], "height": 400})
        assert wait_until(menu_fits, 5), menu.bounding_box()
        for n in ("Lock size", "Send Esc", "Quality: Low", "Download…"):
            expect(menu.get_by_text(n, exact=True)).to_be_visible()
        # The last action stays reachable even on a short landscape screen.
        menu.get_by_role("menuitem", name="Download…", exact=True).click()
        expect(page.get_by_role("dialog")).to_be_visible()
        browser.close()


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
        expect(page.get_by_role("dialog", name="Audio").get_by_text("This machine has no virtual camera for environments")).to_be_visible()
        # An admin can't give it one either.
        page.goto(f"{supervisor.base}/environments/{name}?tab=settings")
        expect(page.get_by_text("This machine has no virtual camera for environments (v4l2loopback).")).to_be_visible(timeout=15000)
        browser.close()


@pytest.mark.parametrize("browser_name", BROWSERS)
def test_touch_clipboard_survives_window_blur(supervisor, desktop, name, browser_name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, browser=browser_name,
                                viewport={"width": 744, "height": 900}, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        page.get_by_role("button", name="Clipboard", exact=True).tap()
        text = page.locator("textarea")
        text.tap()
        text.fill("touch-clipboard-42")
        # A window blur alone does not mean a tap into the remote iframe.
        page.evaluate("async () => { window.dispatchEvent(new Event('blur')); await new Promise(r => setTimeout(r, 0)); }")
        expect(text).to_have_value("touch-clipboard-42")
        page.get_by_role("button", name=f"Paste into {name}").tap()
        clip = lambda: in_desktop(name, "xclip", "-o", "-selection", "clipboard", check=False)
        assert wait_until(lambda: clip() == "touch-clipboard-42"), clip()
        # A real return to the desktop still dismisses the popover.
        page.get_by_role("button", name="Clipboard", exact=True).tap()
        text.tap()
        # The initial load/reconnect can finish while Clipboard is open.
        page.frames[1].goto(page.frames[1].url)
        page.wait_for_function(SHOWING, timeout=30000)
        expect(text).to_be_visible()
        page.locator("iframe").tap(position={"x": 100, "y": 400})
        expect(text).to_be_hidden()
        browser.close()


def test_phone_toolbar_keeps_terminal_and_controls_reachable(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        try:
            # Preserve the document's native loopback address space for the
            # subsequent SPA-to-WebSocket navigation. Bootstrap API routing
            # stays enabled; fulfilling the document makes Chromium block WS.
            page.route(f"{supervisor.base}/{name}/", lambda route: route.continue_())
            open_desktop(page, supervisor.base, name)
            terminal = page.locator(f'a[href="/{name}/terminal/"]')
            for width in (320, 375):
                page.set_viewport_size({"width": width, "height": 750})
                geometry = terminal.evaluate("el => ({width: el.parentElement.clientWidth, scroll: el.parentElement.scrollWidth})")
                assert geometry["scroll"] <= geometry["width"], geometry
                expect(terminal).not_to_be_visible()
                more = page.get_by_role("button", name="More", exact=True)
                expect(more).to_be_visible()
                assert more.bounding_box()["x"] + more.bounding_box()["width"] <= width
                more.click()
                action = page.get_by_role("menuitem", name="Terminal", exact=True)
                expect(action).to_be_visible()
                if width == 320:
                    page.keyboard.press("Escape")
                else:
                    action.click()
                    expect(page.locator('[data-terminal-state="connected"]')).to_be_attached(timeout=20000)
                    assert page.url.endswith(f"/{name}/terminal/")
        finally:
            browser.close()
