"""The desktop's screen through the supervisor: Selkies' client from the image,
our additions, the proxied stream, and mic badges."""

import asyncio
import json
import re
import threading
import time
import uuid

import pytest
import websockets

import netchange
from conftest import docker
from helpers import FAKE_MEDIA, make_account, open_desktop, read_events, sign_in, signed_in, wait_loaded, wait_until, ws_handshake


def ws_url(client, name, tab=None):
    url = client.base.replace("http", "ws") + f"/{name}/screen/api/websockets"
    return url + (f"?tab={tab}" if tab else "")


def cookie_header(client):
    return "; ".join(f"{k}={v}" for k, v in client.cookies.items())


class Stream:
    """A stream WebSocket held open on a thread, like a viewer's page."""

    def __init__(self, client, name, tab=None):
        self.url = ws_url(client, name, tab)
        self.tab = tab or uuid.uuid4().hex[:12]
        self.headers = {"Cookie": cookie_header(client), "Origin": client.base}
        self.opened = threading.Event()
        self.done = threading.Event()
        self.frames = 0
        self.video = 0  # of the frames, the picture's (0x03 JPEG, 0x04 video stripes)
        self.error = None
        self.send_queue: list[str] = []
        self.thread = threading.Thread(target=lambda: asyncio.run(self.run()))

    async def run(self):
        try:
            async with websockets.connect(self.url, additional_headers=self.headers, max_size=None) as ws:
                # Like Selkies' page: with a tabId, a second page joins beside the first
                # instead of taking the display over.
                await ws.send("SETTINGS," + json.dumps({"displayId": "primary", "tabId": self.tab}))
                self.opened.set()
                while not self.done.is_set():
                    while self.send_queue:
                        await ws.send(self.send_queue.pop(0))
                    try:
                        msg = await asyncio.wait_for(ws.recv(), 0.2)
                        if isinstance(msg, bytes):
                            self.frames += 1
                            if msg[:1] in (b"\x03", b"\x04"):
                                self.video += 1
                    except asyncio.TimeoutError:
                        pass
        except Exception as e:  # noqa: BLE001 — reported by the test
            self.error = e
            self.opened.set()

    def start(self):
        self.thread.start()
        return self

    def wait_open(self):
        assert self.opened.wait(15) and self.error is None, self.error

    def __enter__(self):
        self.start().wait_open()
        return self

    def __exit__(self, *exc):
        self.done.set()
        self.thread.join(10)


def test_stream_needs_session_access_and_same_origin(admin, desktop, name, supervisor):
    path = f"/{name}/screen/api/websockets"
    assert ws_handshake(admin.base, path, origin=admin.base) == 401
    user = make_account(admin, [])
    try:
        anna = supervisor.client().login(user, "pw")
        assert ws_handshake(anna.base, path, anna.cookies, origin=anna.base) == 404
    finally:
        admin.delete(f"/api/accounts/{user}")
    assert ws_handshake(admin.base, path, admin.cookies, origin="https://evil.example") == 403
    assert ws_handshake(admin.base, path, admin.cookies) == 403
    assert ws_handshake(admin.base, path, admin.cookies, origin=admin.base) == 101


def test_stream_shows_the_desktop_and_counts_as_viewing(admin, desktop, name):
    with Stream(admin, name) as s:
        assert wait_until(lambda: admin.desktop(name)["viewers"] == ["admin"])
        # Everyone watching the desktops hears of it.
        assert read_events(admin, lambda es: any(k in ("viewers", "desktop") and d["name"] == name and d["viewers"] == ["admin"]
                                                 for k, d in es))
        assert wait_until(lambda: s.frames > 0, 15), "no picture from Selkies through the supervisor"
    assert wait_until(lambda: admin.desktop(name)["viewers"] == [])


def test_viewers_opening_together_all_get_the_picture(admin, desktop, name):
    # Selkies refuses a stream within 500 ms of the last from one address, and
    # every viewer comes from the desktop's Caddy: the supervisor spaces them.
    streams = [Stream(admin, name, f"tab{i}") for i in range(3)]
    for s in streams:
        s.start()
    try:
        for s in streams:
            s.wait_open()
        assert wait_until(lambda: all(s.frames > 0 for s in streams), 20), [(s.frames, s.error) for s in streams]
        assert all(s.error is None for s in streams), [s.error for s in streams]
    finally:
        for s in streams:
            s.__exit__()


@pytest.mark.serial  # a picture within 5 s
def test_stopped_stream_restarts_for_a_new_viewer(supervisor, admin, desktop, name):
    # A page that stops the desktop's stream (as Selkies' client does when the
    # only viewer's tab goes to the background, without our additions): the
    # next viewer to connect still gets the picture within 5 s.
    with Stream(admin, name, "hidden") as a:
        assert wait_until(lambda: a.video > 0, 15)
        a.send_queue.append("STOP_VIDEO")
        logs = lambda: (lambda r: r.stdout + r.stderr)(docker("logs", f"deskyard-{name}"))  # noqa: E731
        assert wait_until(lambda: "Received STOP_VIDEO for 'primary'. Stopping stream." in logs(), 10), logs()[-3000:]
        time.sleep(1)
        with Stream(admin, name, "joiner") as b:
            joined = time.time()
            assert wait_until(lambda: b.video > 0, 5), "the joining viewer got no picture"
            print(f"first picture {time.time() - joined:.2f} s after joining")
        assert "restarting it" in supervisor.logs()


def test_reconnect_probe_and_other_selkies_api(admin, desktop, name):
    # The client probes with a plain GET before reconnecting.
    assert wait_until(lambda: admin.get(f"/{name}/screen/api/websockets").status_code == 204, 30)
    for method, path in [("GET", "api/status"), ("POST", "api/upload"), ("GET", "api/files/"), ("GET", "api/screenshot"),
                         ("GET", "api/recording"), ("GET", "api/health"), ("POST", "api/switch"), ("GET", "api/sessions")]:
        assert admin.request(method, f"/{name}/screen/{path}").status_code == 404, path


def test_client_comes_from_the_image_not_the_desktop(admin, desktop, name):
    # With Selkies stopped inside the desktop, the client still loads: it was
    # never proxied.
    docker("exec", f"deskyard-{name}", "supervisorctl", "stop", "selkies")
    try:
        assert wait_until(lambda: admin.get(f"/{name}/screen/api/websockets").status_code == 503)
        page = admin.get(f"/{name}/screen/")
        assert page.status_code == 200 and page.headers["Cache-Control"] == "no-store"
        assert page.text.index('<script src="deskyard.js"></script>') < page.text.index('<script type="module"')
        asset = re.search(r'src="\./(assets/index-[^"]+\.js)"', page.text).group(1)
        assert admin.get(f"/{name}/screen/{asset}").status_code == 200
        js = admin.get(f"/{name}/screen/deskyard.js")
        assert js.status_code == 200 and js.headers["Cache-Control"] == "no-store" and "deskyardScreen" in js.text
    finally:
        docker("exec", f"deskyard-{name}", "supervisorctl", "start", "selkies")


def test_mic_badge_follows_the_stream(admin, desktop, name):
    tab = uuid.uuid4().hex[:12]
    assert admin.desktop(name)["mics"] == []
    for bad in ["", "a b", "x" * 65]:
        assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": bad}).status_code == 400, bad
    with Stream(admin, name, tab):
        assert wait_until(lambda: admin.desktop(name)["viewers"] == ["admin"])
        assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": "not-this-one"}).status_code == 409
        assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": tab}).status_code == 204
        assert admin.desktop(name)["mics"] == ["admin"]
        assert read_events(admin, lambda es: any(k in ("viewers", "desktop") and d["name"] == name and d["mics"] == ["admin"]
                                                 for k, d in es))
    assert wait_until(lambda: admin.desktop(name)["mics"] == [] and admin.desktop(name)["viewers"] == [])
    assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": tab}).status_code == 409



def test_client_additions_in_a_browser(supervisor, admin, desktop, name):
    from playwright.sync_api import sync_playwright

    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        open_desktop(page, supervisor.base, name)
        f = page.frames[1]
        assert f.url == f"{supervisor.base}/{name}/screen/"
        f.wait_for_function("window.deskyardScreen && window.deskyardScreen.state() === 'connected'", timeout=30000)
        f.wait_for_function("window.deskyardScreen.video() !== null", timeout=30000)
        assert f.evaluate("'onpointerrawupdate' in window") is False

        # The tab id is on the stream URL (the supervisor ties the mic to it);
        # the desktop page reads it from the frame.
        tab = page.evaluate("document.querySelector('iframe').contentWindow.deskyardScreen.tab")
        assert tab == f.evaluate("window.deskyardScreen.tab")
        r = page.evaluate("""async (tab) => (await fetch(location.pathname.replace(/\\/$/, '').replace(/^\\//, '/api/desktops/') + '/mic',
            {method: 'POST', body: JSON.stringify({on: true, tab})})).status""", tab)
        assert r == 204
        assert admin.desktop(name)["mics"] == ["admin"]

        # Messages reach on(); STOP_VIDEO never reaches the stream.
        f.evaluate("""() => {
            window.__sent = [];
            const wp = Worker.prototype.postMessage;
            Worker.prototype.postMessage = function (m, ...r) { if (m && m.type === 'send') window.__sent.push(m.data); return wp.call(this, m, ...r); };
            const ws = WebSocket.prototype.send;
            WebSocket.prototype.send = function (d) { window.__sent.push(d); return ws.call(this, d); };
            window.__got = [];
            window.deskyardScreen.on('deskyardTest', (m) => window.__got.push(m));
        }""")
        f.evaluate("window.deskyardScreen.post({type: 'deskyardTest'})")
        f.evaluate("window.deskyardScreen.post({type: 'pipelineControl', pipeline: 'video', enabled: false})")
        f.wait_for_function("window.__got.length === 1", timeout=10000)
        f.wait_for_function("window.__sent.length > 0", timeout=10000)  # the spy sees the stream's sends
        assert "STOP_VIDEO" not in f.evaluate("window.__sent")

        # A new load of the client (a reload, or a duplicated browser tab,
        # which copies sessionStorage) gets its own tab id, and the old
        # stream's mic badge goes with it.
        seen = netchange.failures(page)
        page.reload()
        screen = "document.querySelector('iframe')?.contentWindow?.deskyardScreen"
        wait_loaded(page, f"{screen}?.state() === 'connected'", seen)
        assert page.evaluate(f"{screen}.tab") != tab
        assert wait_until(lambda: admin.desktop(name)["mics"] == [])
        browser.close()


def test_two_pages_stay_side_by_side(admin, desktop, name):
    # Two pages with their own Selkies tab ids join beside each other; neither
    # supersedes the other.
    docker("exec", "-d", "-u", "user", "-e", "DISPLAY=:1", f"deskyard-{name}", "xfce4-terminal")
    assert wait_until(lambda: docker("exec", "-u", "user", "-e", "DISPLAY=:1", f"deskyard-{name}",
                                   "xdotool", "search", "--onlyvisible", "--class", "xfce4-terminal", check=False).returncode == 0)
    with Stream(admin, name, "pageA") as a:
        assert wait_until(lambda: a.frames > 0, 15)
        with Stream(admin, name, "pageB") as b:
            assert wait_until(lambda: b.frames > 0, 15)
            before = (a.frames, b.frames)
            time.sleep(3)
            # Something changes on screen: both pages get the new picture.
            for _ in range(5):
                docker("exec", "-u", "user", "-e", "DISPLAY=:1", f"deskyard-{name}", "xdotool", "type", "--delay", "50", "hello ")
            time.sleep(1)
            assert a.error is None and b.error is None, (a.error, b.error)
            assert a.frames > before[0] and b.frames > before[1], (before, a.frames, b.frames)


def test_duplicated_tab_and_reload_keep_the_picture(supervisor, admin, desktop, name):
    from playwright.sync_api import sync_playwright

    screen = "document.querySelector('iframe')?.contentWindow?.deskyardScreen"
    showing = f"{screen}?.state() === 'connected' && {screen}.video() !== null"

    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        seen = netchange.failures(page)
        page.goto(f"{supervisor.base}/{name}/")
        wait_loaded(page, showing, seen)

        # A duplicated tab (window.open copies sessionStorage, as duplicating does).
        seen = netchange.failures(page)
        with page.context.expect_page() as popup_info:
            page.evaluate("window.open(location.href)")
        dup = popup_info.value
        wait_loaded(dup, showing, seen)
        time.sleep(5)
        for pg in (page, dup):
            assert pg.evaluate(f"{screen}.state()") == "connected"
            assert pg.evaluate(f"{screen}.video() !== null")
        assert wait_until(lambda: admin.desktop(name)["viewers"] == ["admin"])
        dup.close()

        # A reload gets the picture back promptly.
        start = time.time()
        page.reload()
        page.wait_for_function(showing, timeout=15000)
        assert time.time() - start < 15
        browser.close()


def test_mic_turns_on_only_for_the_page(supervisor, admin, desktop, name):
    # Selkies' client would turn the mic on by itself: after a reconnect it
    # restores what was on. Our additions let it capture only once the
    # desktop page has asked, for the current connection. (The client on its
    # own here, without the desktop page around it.)
    from playwright.sync_api import sync_playwright

    screen = "window.deskyardScreen"
    with sync_playwright() as p:
        browser = p.chromium.launch(args=FAKE_MEDIA)
        page = sign_in(browser, supervisor.base)
        dialogs = []
        page.on("dialog", lambda d: (dialogs.append(d.message), d.dismiss()))
        page.goto(f"{supervisor.base}/{name}/screen/")
        page.wait_for_function(f"{screen}?.state() === 'connected'", timeout=30000)

        page.evaluate(f"{screen}.post({{type: 'pipelineControl', pipeline: 'microphone', enabled: true}})")
        time.sleep(3)
        assert page.evaluate(f"{screen}.media().mic") is False

        page.evaluate(f"{screen}.allowCapture('microphone', true)")
        page.evaluate(f"{screen}.post({{type: 'pipelineControl', pipeline: 'microphone', enabled: true}})")
        page.wait_for_function(f"{screen}.media().mic", timeout=15000)

        # The stream drops; Selkies reconnects and would restore the mic.
        page.evaluate("window.selkiesTransport.close()")
        page.wait_for_function(f"{screen}.state() !== 'connected'", timeout=10000)
        page.wait_for_function(f"{screen}.state() === 'connected'", timeout=30000)
        time.sleep(4)
        assert page.evaluate(f"{screen}.media().mic") is False
        assert dialogs == []
        browser.close()
