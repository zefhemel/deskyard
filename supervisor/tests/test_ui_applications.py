"""Mobile application prototype against the real helper, desktop and stream."""
import time
from playwright.sync_api import expect, sync_playwright
import json
from conftest import docker
from helpers import signed_in, open_desktop, in_desktop, wait_until, video_settings_lines, sign_in


def test_mobile_launch_focus_and_return(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width": 390, "height": 760}, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        page.get_by_role("button", name="Apps", exact=True).click()
        expect(page.get_by_role("dialog")).to_be_visible()
        page.get_by_placeholder("Search applications and windows").fill("terminal")
        page.get_by_role("button", name="Launch Xfce Terminal", exact=True).click()
        expect(page.get_by_role("button", name="Full desktop", exact=True)).to_be_visible(timeout=20000)
        expect(page.locator("[data-window-viewport]")).to_have_attribute("data-focused", "true")
        page.screenshot(path=str(tmp_path / "mobile-terminal.png"))
        # Input traverses the translated/scaled iframe and reaches the real terminal.
        box = page.locator("[data-window-viewport]").bounding_box()
        page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2)
        page.keyboard.type("printf focused > /tmp/mobile-focused")
        page.keyboard.press("Enter")
        assert wait_until(lambda: in_desktop(name, "cat", "/tmp/mobile-focused", check=False).strip() == "focused", 10)
        page.get_by_role("button", name="Full desktop", exact=True).click()
        expect(page.locator("[data-window-viewport]")).to_have_attribute("data-focused", "false")
        # Chromium uses the shared-browser launcher registered by the image.
        response = page.request.post(f"{supervisor.base}/api/desktops/{name}/applications",
            data={"action": "launch", "id": "chromium.desktop"}, headers={"Origin": supervisor.base})
        assert response.ok, response.text()
        launched = response.json()["id"]
        assert launched, response.text()
        inventory = page.request.get(f"{supervisor.base}/api/desktops/{name}/applications?windows=1").json()
        assert any(w["id"] == launched and "chromium" in w["app"].lower() for w in inventory["windows"])
        browser.close()


PROBE = r"""
import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, GLib
import json
window = Gtk.Window(title="Mobile crop probe")
window.set_default_size(560, 420)
window.move(180, 120)
fixed = Gtk.Fixed()
window.add(fixed)
hit = Gtk.Button(label="Hit target")
hit.set_size_request(120, 60)
hit.connect("clicked", lambda *_: open("/tmp/mobile-hit", "w").write("yes"))
fixed.put(hit, 100, 100)
def dialog(*_):
    d = Gtk.Dialog(title="Mobile child dialog", transient_for=window, modal=True)
    d.set_default_size(300, 180)
    d.add_button("Done", Gtk.ResponseType.OK)
    d.connect("response", lambda d, *_: d.destroy())
    d.show_all()
show = Gtk.Button(label="Open dialog")
show.connect("clicked", dialog)
fixed.put(show, 100, 220)
window.show_all()
def positions():
    def center(widget):
        a = widget.get_allocation()
        return window.get_window().get_root_coords(a.x + a.width // 2, a.y + a.height // 2)
    targets = {"hit": center(hit), "dialog": center(show)}
    open("/tmp/mobile-targets.json", "w").write(json.dumps(targets))
    return False
window.connect("configure-event", lambda *_: GLib.idle_add(positions))
GLib.idle_add(positions)
Gtk.main()
"""


def test_shared_screen_crop_touch_and_dialog(supervisor, desktop, name, tmp_path):
    in_desktop(name, "python3", "-c", "import sys; open('/tmp/mobile-probe.py','w').write(sys.argv[1])", PROBE)
    with sync_playwright() as p:
        laptop, large = signed_in(p, supervisor.base, viewport={"width": 1280, "height": 900})
        open_desktop(large, supervisor.base, name)
        phone, page = signed_in(p, supervisor.base, viewport={"width": 390, "height": 760}, has_touch=True)
        open_desktop(page, supervisor.base, name, status=False)
        page.get_by_role("button", name="More", exact=True).click()
        expect(page.get_by_role("menuitem", name="Resolution: controlled by another viewer", exact=True)).to_be_disabled()
        page.get_by_role("button", name="More", exact=True).click()
        docker("exec", "-d", "-u", "user", f"deskyard-{name}", "/usr/bin/python3", "/tmp/mobile-probe.py")
        page.get_by_role("button", name="Apps", exact=True).click()
        page.get_by_role("button", name="Switch to Mobile crop probe", exact=True).click(timeout=15000)
        expect(page.locator("[data-window-viewport]")).to_have_attribute("data-focused", "true")
        page.wait_for_function("""() => {
            const f=document.querySelector('iframe'), d=f.contentWindow.deskyardScreen.display();
            const v=f.contentWindow.deskyardScreen.video()?.getBoundingClientRect();
            return v && Math.abs(v.width-d.width)<3 && Math.abs(v.height-d.height)<3;
        }""", timeout=10000)
        inventory = page.request.get(f"{supervisor.base}/api/desktops/{name}/applications?windows=1").json()
        selected = next(w for w in inventory["windows"] if w["title"] == "Mobile crop probe")
        xid = selected["id"]
        def tap_target(target):
            # XFCE can configure/reparent the GTK window after its first idle.
            targets = json.loads(in_desktop(name, "cat", "/tmp/mobile-targets.json"))
            point = page.evaluate("""({x,y}) => {
                const f=document.querySelector('iframe');
                const picture=f.contentWindow.deskyardScreen.video();
                const a=f.getBoundingClientRect(), b=picture.getBoundingClientRect();
                const d=f.contentWindow.deskyardScreen.display();
                const sx=a.width/f.clientWidth, sy=a.height/f.clientHeight;
                return {x:a.left+(b.left+x*b.width/d.width)*sx,
                        y:a.top+(b.top+y*b.height/d.height)*sy};
            }""", {"x": targets[target][0], "y": targets[target][1]})
            page.screenshot(path=str(tmp_path / "before-tap.png"))
            page.touchscreen.tap(point["x"], point["y"])
            page.screenshot(path=str(tmp_path / "after-tap.png"))
        tap_target("hit")
        assert wait_until(lambda: in_desktop(name, "cat", "/tmp/mobile-hit", check=False).strip() == "yes", 10)
        tap_target("dialog")
        assert wait_until(lambda: any(w["title"] == "Mobile child dialog" and w["parent"] == xid
            for w in page.request.get(f"{supervisor.base}/api/desktops/{name}/applications?windows=1").json()["windows"]), 10)
        page.screenshot(path=str(tmp_path / "mobile-dialog.png"))
        # A second viewer changes the stream aspect ratio: focused input must stay aligned.
        large.set_viewport_size({"width": 1100, "height": 720})
        assert wait_until(lambda: page.evaluate("""() => {
            const f=document.querySelector('iframe'),d=f.contentWindow.deskyardScreen.display();
            return d.width > 0 && Math.abs(f.clientWidth/d.width-f.clientHeight/d.height)<0.01;
        }"""), 10)
        page.get_by_role("button", name="Full desktop", exact=True).click()
        expect(page.locator("[data-window-viewport]")).to_have_attribute("data-focused", "false")
        phone.close()
        laptop.close()


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
            magnifications.append(float(page.locator("[data-window-viewport]").get_attribute("data-zoom")))
        assert magnifications == sorted(magnifications), magnifications
        # Holding the same pinch distance must not oscillate after a render.
        cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":230,"y":280,"id":1},{"x":490,"y":280,"id":2}]})
        time.sleep(0.12)
        assert abs(float(page.locator("[data-window-viewport]").get_attribute("data-zoom"))-magnifications[-1]) < 0.02
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        page.wait_for_function("Number(document.querySelector('[data-window-viewport]').dataset.zoom) > 1.2")
        assert in_desktop(name, "xdotool", "getdisplaygeometry") == before_size
        before_pan = page.locator("[data-window-viewport]").evaluate("e => e.scrollLeft")
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":300,"y":280,"id":1},{"x":420,"y":280,"id":2}]})
        for offset in (20,40,60):
            cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":300-offset,"y":280,"id":1},{"x":420-offset,"y":280,"id":2}]})
            time.sleep(0.1)
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        assert wait_until(lambda: page.locator("[data-window-viewport]").evaluate("e => e.scrollLeft") > before_pan + 50, 5)
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
        expect(page.locator("[data-window-viewport]")).to_have_attribute("data-zoom", "4.00")
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":300,"y":280,"id":1},{"x":420,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":270,"y":280,"id":1},{"x":390,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        scroll_at_limit = page.locator("[data-window-viewport]").evaluate("e => e.scrollLeft")
        cdp.send("Input.dispatchTouchEvent", {"type":"touchStart", "touchPoints":[{"x":260,"y":280,"id":1},{"x":460,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchMove", "touchPoints":[{"x":280,"y":280,"id":1},{"x":440,"y":280,"id":2}]})
        cdp.send("Input.dispatchTouchEvent", {"type":"touchEnd", "touchPoints":[]})
        page.wait_for_function("Number(document.querySelector('[data-window-viewport]').dataset.zoom) < 4")
        result = page.locator("[data-window-viewport]").evaluate("e => ({zoom:Number(e.dataset.zoom),scroll:e.scrollLeft})")
        assert abs(result["scroll"] - ((scroll_at_limit+360)*result["zoom"]/4-360)) < 4, (scroll_at_limit,result)
        page.get_by_role("button", name="Fit screen", exact=True).click()
        expect(page.locator("[data-window-viewport]")).to_have_attribute("data-zoom", "1.00")
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
        view = page.locator("[data-window-viewport]")
        page.wait_for_function("Number(document.querySelector('[data-window-viewport]').dataset.zoom)>1.5")
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
