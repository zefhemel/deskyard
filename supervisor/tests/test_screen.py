"""The desktop's screen through the supervisor: Selkies' client from the image,
our additions, the proxied stream, and mic badges."""

import asyncio
import json
import re
import threading
import time
import uuid

import websockets

from conftest import docker
from test_api import ws_handshake
from test_monitoring import read_events


def ws_url(client, name, tab=None):
    url = client.base.replace("http", "ws") + f"/{name}/screen/api/websockets"
    return url + (f"?tab={tab}" if tab else "")


def cookie_header(client):
    return "; ".join(f"{k}={v}" for k, v in client.cookies.items())


class Stream:
    """A stream WebSocket held open on a thread, like a viewer's page."""

    def __init__(self, client, name, tab=None):
        self.url = ws_url(client, name, tab)
        self.headers = {"Cookie": cookie_header(client), "Origin": client.base}
        self.opened = threading.Event()
        self.done = threading.Event()
        self.frames = 0
        self.error = None
        self.thread = threading.Thread(target=lambda: asyncio.run(self.run()))

    async def run(self):
        try:
            async with websockets.connect(self.url, additional_headers=self.headers, max_size=None) as ws:
                await ws.send("SETTINGS," + json.dumps({"displayId": "primary"}))
                self.opened.set()
                while not self.done.is_set():
                    try:
                        msg = await asyncio.wait_for(ws.recv(), 0.2)
                        if isinstance(msg, bytes):
                            self.frames += 1
                    except asyncio.TimeoutError:
                        pass
        except Exception as e:  # noqa: BLE001 — reported by the test
            self.error = e
            self.opened.set()

    def __enter__(self):
        self.thread.start()
        assert self.opened.wait(15) and self.error is None, self.error
        return self

    def __exit__(self, *exc):
        self.done.set()
        self.thread.join(10)


def wait_for(fn, timeout=10):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if v := fn():
            return v
        time.sleep(0.25)
    return fn()


def test_stream_needs_session_access_and_same_origin(admin, desktop, name, supervisor):
    path = f"/{name}/screen/api/websockets"
    assert ws_handshake(admin.base, path, origin=admin.base) == 401
    user = "u" + uuid.uuid4().hex[:5]
    assert admin.post("/api/accounts", json={"username": user, "password": "pw", "admin": False, "desktops": []}).status_code == 201
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
        assert wait_for(lambda: admin.desktop(name)["viewers"] == ["admin"])
        assert wait_for(lambda: s.frames > 0, 15), "no picture from Selkies through the supervisor"
    assert wait_for(lambda: admin.desktop(name)["viewers"] == [])


def test_reconnect_probe_and_other_selkies_api(admin, desktop, name):
    # The client probes with a plain GET before reconnecting.
    assert wait_for(lambda: admin.get(f"/{name}/screen/api/websockets").status_code == 204, 30)
    for method, path in [("GET", "api/status"), ("POST", "api/upload"), ("GET", "api/files/"), ("GET", "api/screenshot"),
                         ("GET", "api/recording"), ("GET", "api/health"), ("POST", "api/switch"), ("GET", "api/sessions")]:
        assert admin.request(method, f"/{name}/screen/{path}").status_code == 404, path


def test_client_comes_from_the_image_not_the_desktop(admin, desktop, name):
    # With Selkies stopped inside the desktop, the client still loads: it was
    # never proxied.
    docker("exec", f"deskyard-{name}", "supervisorctl", "stop", "selkies")
    try:
        assert wait_for(lambda: admin.get(f"/{name}/screen/api/websockets").status_code == 503)
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
        assert wait_for(lambda: admin.desktop(name)["viewers"] == ["admin"])
        assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": "not-this-one"}).status_code == 409
        assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": tab}).status_code == 204
        assert admin.desktop(name)["mics"] == ["admin"]
        assert read_events(admin, lambda es: any(k in ("viewers", "desktop") and d["name"] == name and d["mics"] == ["admin"]
                                                 for k, d in es))
    assert wait_for(lambda: admin.desktop(name)["mics"] == [] and admin.desktop(name)["viewers"] == [])
    assert admin.post(f"/api/desktops/{name}/mic", json={"on": True, "tab": tab}).status_code == 409



def test_client_additions_in_a_browser(supervisor, admin, desktop, name):
    from playwright.sync_api import sync_playwright

    from test_ui import signed_in

    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        frame = page.frame_locator("iframe")
        frame.locator("body").wait_for(state="attached", timeout=30000)
        f = page.frames[1]
        assert f.url == f"{supervisor.base}/{name}/screen/"
        f.wait_for_function("window.deskyardScreen && window.deskyardScreen.state() === 'connected'", timeout=30000)
        f.wait_for_function("window.deskyardScreen.video() !== null", timeout=30000)
        assert f.evaluate("'onpointerrawupdate' in window") is False

        # The tab id is on the stream URL (the supervisor ties the mic to it),
        # and shared with the desktop page through sessionStorage.
        tab = f.evaluate("window.deskyardScreen.tab")
        assert page.evaluate("sessionStorage.getItem('deskyard.tab')") == tab
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
        browser.close()
    assert wait_for(lambda: admin.desktop(name)["mics"] == [])
