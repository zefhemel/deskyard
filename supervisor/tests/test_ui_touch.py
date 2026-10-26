"""Touch, magnification and resolution controls against the real desktop and stream."""
import time
from playwright.sync_api import expect, sync_playwright
from helpers import signed_in, open_desktop, in_desktop, wait_until, video_settings_lines, sign_in


def test_general_desktop_touch_controls(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width": 820, "height": 600}, has_touch=True, is_mobile=True)
        open_desktop(page, supervisor.base, name, status=False)
        expect(page.get_by_role("button", name="Tap", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="Trackpad", exact=True)).to_be_visible()
        page.get_by_role("button", name="Show keyboard", exact=True).click()
        page.wait_for_function("document.querySelector('iframe').contentDocument.activeElement?.id === 'keyboard-input-assist'")
        page.get_by_role("button", name="Hide keyboard", exact=True).click()
        page.wait_for_function("document.querySelector('iframe').contentDocument.activeElement?.id !== 'keyboard-input-assist'")
        page.get_by_role("button", name="Trackpad", exact=True).click()
        expect(page.get_by_role("button", name="Trackpad", exact=True)).to_have_attribute("aria-pressed", "true")
        in_desktop(name, "xdotool", "mousemove", "400", "250")
        # A trackpad tap clicks without jumping to the finger's absolute position.
        page.touchscreen.tap(120, 200)
        assert wait_until(lambda: "X=400" in in_desktop(name, "xdotool", "getmouselocation", "--shell"), 5)
        page.get_by_role("button", name="Tap", exact=True).click()
        page.touchscreen.tap(120, 200)
        assert wait_until(lambda: "X=120" in in_desktop(name, "xdotool", "getmouselocation", "--shell"), 5)
        before_size = in_desktop(name, "xdotool", "getdisplaygeometry")
        page.frames[1].evaluate("""() => {
            window.__touchSent=[];
            const socket=window.selkiesTransport, send=socket.send;
            socket.send=function(data) { if(typeof data==='string') window.__touchSent.push(data); return send.call(this,data); };
        }""")
        # Native multi-touch through CDP, so the pinned Selkies handlers take it.
        cdp = page.context.new_cdp_session(page)
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":300,"y":280,"id":1},{"x":420,"y":280,"id":2}]})
        magnifications = []
        for distance in (80,100,130):
            cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":360-distance,"y":280,"id":1},{"x":360+distance,"y":280,"id":2}]})
            time.sleep(0.12)
            magnifications.append(float(page.locator("[data-zoom-viewport]").get_attribute("data-zoom")))
        assert magnifications == sorted(magnifications), magnifications
        # Holding the same pinch distance must not oscillate after a render.
        cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":230,"y":280,"id":1},{"x":490,"y":280,"id":2}]})
        time.sleep(0.12)
        assert abs(float(page.locator("[data-zoom-viewport]").get_attribute("data-zoom"))-magnifications[-1]) < 0.02
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        page.wait_for_function("Number(document.querySelector('[data-zoom-viewport]').dataset.zoom) > 1.2")
        assert in_desktop(name, "xdotool", "getdisplaygeometry") == before_size
        before_pan = page.locator("[data-zoom-viewport]").evaluate("e => e.scrollLeft")
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":300,"y":280,"id":1},{"x":420,"y":280,"id":2}]})
        for offset in (20,40,60):
            cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":300-offset,"y":280,"id":1},{"x":420-offset,"y":280,"id":2}]})
            time.sleep(0.1)
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        assert wait_until(lambda: page.locator("[data-zoom-viewport]").evaluate("e => e.scrollLeft") > before_pan + 50, 5)
        sent = page.frames[1].evaluate("window.__touchSent")
        assert not any(message.startswith(("kd,65507", "kd,65508")) for message in sent), sent
        # Direct taps remain aligned after magnification and panning.
        page.touchscreen.tap(300, 300)
        picture = page.evaluate("""() => { const f=document.querySelector('iframe'),v=f.contentWindow.deskyardScreen.video().getBoundingClientRect(),a=f.getBoundingClientRect(),d=f.contentWindow.deskyardScreen.display();
            return {x:a.left+v.left*a.width/f.clientWidth,width:v.width*a.width/f.clientWidth,remote:d.width}; }""")
        expected_x = round((300-picture["x"])*picture["remote"]/picture["width"])
        assert wait_until(lambda: f"X={expected_x}" in in_desktop(name, "xdotool", "getmouselocation", "--shell"), 5)
        # At the 4x limit, extra pinch events must not leave a stale pan anchor.
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":350,"y":280,"id":1},{"x":370,"y":280,"id":2}]})
        for radius in (60,120,180,240,280):
            cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":360-radius,"y":280,"id":1},{"x":360+radius,"y":280,"id":2}]})
            time.sleep(0.1)
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        expect(page.locator("[data-zoom-viewport]")).to_have_attribute("data-zoom", "4.00")
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":300,"y":280,"id":1},{"x":420,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":270,"y":280,"id":1},{"x":390,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        scroll_at_limit = page.locator("[data-zoom-viewport]").evaluate("e => e.scrollLeft")
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":260,"y":280,"id":1},{"x":460,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":280,"y":280,"id":1},{"x":440,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        page.wait_for_function("Number(document.querySelector('[data-zoom-viewport]').dataset.zoom) < 4")
        result = page.locator("[data-zoom-viewport]").evaluate("e => ({zoom:Number(e.dataset.zoom),scroll:e.scrollLeft})")
        assert abs(result["scroll"] - ((scroll_at_limit+360)*result["zoom"]/4-360)) < 4, (scroll_at_limit,result)
        page.get_by_role("button", name="Fit screen", exact=True).click()
        expect(page.locator("[data-zoom-viewport]")).to_have_attribute("data-zoom", "1.00")
        page.get_by_role("button", name="Trackpad", exact=True).click()
        page.reload()
        page.wait_for_function("document.querySelector('iframe')?.contentWindow?.webrtcInput?._trackpadMode === true")
        expect(page.get_by_role("button", name="Trackpad", exact=True)).to_have_attribute("aria-pressed", "true")
        browser.close()


def test_phone_without_webcodecs_uses_jpeg(supervisor, desktop, name):
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--host-resolver-rules=MAP deskyard.invalid 127.0.0.1", "--no-proxy-server"])
        http_base = supervisor.base.replace("127.0.0.1", "deskyard.invalid")
        page = sign_in(browser, http_base, viewport={"width":390,"height":760}, has_touch=True)
        assert page.evaluate("isSecureContext") is False
        assert page.evaluate("typeof VideoDecoder") == "undefined"
        open_desktop(page, http_base, name, status=False)
        assert wait_until(lambda: any("jpeg" in line.lower() for line in video_settings_lines(name)), 10)
        expect(page.get_by_role("button", name="Tap", exact=True)).to_be_visible()
        page.touchscreen.tap(120,200)
        assert wait_until(lambda: "X=120" in in_desktop(name,"xdotool","getmouselocation","--shell"), 5)
        browser.close()


def test_mobile_remote_resolution_presets(supervisor, desktop, name, tmp_path):
    from helpers import screen_size
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width":390,"height":760}, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        page.wait_for_function("document.querySelector('iframe').contentWindow.deskyardScreen.display().owner === true")
        page.get_by_role("button", name="More", exact=True).click()
        page.screenshot(path=str(tmp_path / "resolution-menu.png"))
        page.get_by_role("menuitemcheckbox", name="Resolution: 1280 × 720", exact=True).click(timeout=3000)
        assert wait_until(lambda: screen_size(name) == (1280,720), 10)
        page.set_viewport_size({"width":760,"height":390})
        time.sleep(1)
        assert screen_size(name) == (1280,720)
        page.reload()
        open_desktop(page, supervisor.base, name, status=False)
        page.get_by_role("button", name="More", exact=True).click()
        expect(page.get_by_role("menuitemcheckbox", name="Resolution: 1280 × 720", exact=True)).to_have_attribute("aria-checked", "true")
        page.get_by_role("menuitemcheckbox", name="Resolution: 600 × 900 (portrait)", exact=True).click()
        assert wait_until(lambda: screen_size(name) == (600,900), 10)
        page.get_by_role("button", name="More", exact=True).click()
        page.get_by_role("menuitemcheckbox", name="Resolution: Auto fit", exact=True).click()
        assert wait_until(lambda: screen_size(name) != (600,900), 10)
        page.set_viewport_size({"width":1280,"height":900})
        page.get_by_role("button", name="Resolution", exact=True).click()
        page.get_by_role("menuitemcheckbox", name="Resolution: 1920 × 1080", exact=True).click()
        assert wait_until(lambda: screen_size(name) == (1920,1080), 10)
        browser.close()


def test_trackpad_follows_cursor_at_view_edges(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width":390,"height":760}, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        in_desktop(name, "xdotool", "mousemove", "100", "150")
        cdp = page.context.new_cdp_session(page)
        def touch(kind, points):
            cdp.send("Input.dispatchTouchEvent", {"type":kind,"touchPoints":[{"x":x,"y":y,"id":i+1} for i,(x,y) in enumerate(points)]})
        touch("touchStart", [(145,320),(245,320)])
        for radius in (70,90,110):
            touch("touchMove", [(195-radius,320),(195+radius,320)])
            time.sleep(.1)
        touch("touchEnd", [])
        view = page.locator("[data-zoom-viewport]")
        page.wait_for_function("Number(document.querySelector('[data-zoom-viewport]').dataset.zoom)>1.5")
        view.evaluate("e => { e.scrollLeft=0; e.scrollTop=0; }")
        page.get_by_role("button", name="Trackpad", exact=True).click()
        page.wait_for_function("document.querySelector('iframe').contentWindow.webrtcInput._echo !== null")
        for _ in range(3):
            touch("touchStart", [(80,300)])
            for x in range(100,331,25):
                touch("touchMove", [(x,300)])
                time.sleep(.04)
            touch("touchEnd", [])
        assert view.evaluate("e => e.scrollLeft") > 40
        # No drift from a stationary finger after the viewport has moved.
        touch("touchStart", [(200,300)])
        touch("touchMove", [(220,300)])
        time.sleep(.2)
        before = in_desktop(name, "xdotool", "getmouselocation", "--shell")
        scroll = view.evaluate("e => e.scrollLeft")
        for _ in range(5):
            touch("touchMove", [(220,300)])
            time.sleep(.08)
        assert in_desktop(name, "xdotool", "getmouselocation", "--shell") == before
        assert abs(view.evaluate("e => e.scrollLeft")-scroll) < 2
        touch("touchEnd", [])
        # Bottom and reverse edges follow too, within the scrollable bounds.
        for _ in range(3):
            touch("touchStart", [(180,250)])
            for y in range(275,551,25):
                touch("touchMove", [(180,y)])
                time.sleep(.04)
            touch("touchEnd", [])
        assert view.evaluate("e => e.scrollTop") > 40
        for _ in range(5):
            touch("touchStart", [(320,550)])
            for delta in range(25,251,25):
                touch("touchMove", [(320-delta,550-delta)])
                time.sleep(.04)
            touch("touchEnd", [])
        assert view.evaluate("e => e.scrollLeft") < 2
        assert view.evaluate("e => e.scrollTop") < 2
        # Another viewer moving the pointer must not take over this viewport.
        scroll = view.evaluate("e => e.scrollLeft")
        in_desktop(name, "xdotool", "mousemove", "0", "0")
        time.sleep(.3)
        assert view.evaluate("e => e.scrollLeft") == scroll
        browser.close()
