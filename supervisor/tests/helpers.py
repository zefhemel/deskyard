"""Helpers shared by the supervisor's tests: waiting, the desktop's insides,
agent tokens and exec, events, and the web UI in a browser."""

from __future__ import annotations

import json
import queue
import re
import signal
import subprocess
import threading
import time
import uuid

import pytest
import requests
import websockets
from playwright.sync_api import Error as PlaywrightError
from playwright.sync_api import expect

import netchange

from conftest import ADMIN_PASSWORD, docker


def wait_until(f, timeout: float = 15.0, every: float = 0.3):
    """Polls f until it returns something true or timeout passes; returns its last value."""
    deadline = time.time() + timeout
    while True:
        v = f()
        if v or time.time() > deadline:
            return v
        time.sleep(every)


# --- the desktop's insides ---------------------------------------------------------------------

def in_desktop(name, *args, check=True) -> str:
    """Runs a command as the desktop's user, on its screen."""
    r = subprocess.run(["docker", "exec", "-u", "user", "-e", "DISPLAY=:1", f"deskyard-{name}", *args],
                       capture_output=True, text=True)
    if check:
        assert r.returncode == 0, r.stderr
    return r.stdout


def user_sh(name: str, script: str) -> str:
    """A login shell script as the desktop's user; its output."""
    r = docker("exec", "-u", "user", f"deskyard-{name}", "bash", "-lc", script, check=False)
    assert r.returncode == 0, r.stdout + r.stderr
    return r.stdout


def put(name: str, rel: str, data: bytes) -> None:
    """Writes /workspace/rel in the desktop, as its user. (Not into the
    desktop's folder from here: on Linux that belongs to the desktop's user.)"""
    r = subprocess.run(["docker", "exec", "-i", "-u", "user", f"deskyard-{name}", "sh", "-c",
                        'mkdir -p "$(dirname "$1")" && cat > "$1"', "sh", f"/workspace/{rel}"],
                       input=data, capture_output=True)
    assert r.returncode == 0, r.stderr.decode()


def screen_size(name) -> tuple[int, int]:
    w, h = in_desktop(name, "xdotool", "getdisplaygeometry").split()
    return int(w), int(h)


def mouse_location(name) -> tuple[int, int]:
    out = in_desktop(name, "xdotool", "getmouselocation")
    x, y = re.search(r"x:(\d+) y:(\d+)", out).groups()
    return int(x), int(y)


def video_settings_lines(name) -> list[str]:
    """Selkies' log lines about the primary display's video settings."""
    logs = subprocess.run(["docker", "logs", f"deskyard-{name}"], capture_output=True, text=True)
    return [line for line in (logs.stdout + logs.stderr).splitlines()
            if "Applying video settings for 'primary'" in line or "Video parameters changed for 'primary'" in line
            or "settings applied for 'primary'" in line]


# --- accounts, agent tokens, exec ------------------------------------------------------------

def make_account(admin, desktops, password="pw") -> str:
    user = "u" + uuid.uuid4().hex[:5]
    r = admin.post("/api/accounts", json={"username": user, "password": password, "admin": False, "desktops": desktops})
    assert r.status_code == 201, r.text
    return user


def make_token(client, name, **kw) -> dict:
    r = client.post(f"/api/desktops/{name}/tokens", json={"name": "test", "expires": None, **kw})
    assert r.status_code == 201, r.text
    return r.json()


async def exec_ws(base, token, argv, stdin=b"", tty=False, env=None):
    url = base.replace("http", "ws") + "/api/agent/v1/exec"
    out, err, code = b"", b"", None
    async with websockets.connect(url, additional_headers={"Authorization": f"Bearer {token}"}, max_size=None) as ws:
        await ws.send(json.dumps({"argv": argv, "tty": tty, **({"env": env} if env else {})}))
        if stdin:
            await ws.send(b"\x00" + stdin)
        await ws.send(json.dumps({"eof": True}))
        async for msg in ws:
            if isinstance(msg, bytes):
                if msg[0] == 1:
                    out += msg[1:]
                elif msg[0] == 2:
                    err += msg[1:]
            else:
                m = json.loads(msg)
                if "exit" in m:
                    code = m["exit"]
                elif "error" in m:
                    raise AssertionError(m["error"])
    return out, err, code


# --- the stream and events -------------------------------------------------------------------

def ws_handshake(base, path, cookies=None, origin=None, headers=None) -> int:
    hdrs = {"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
            "Sec-WebSocket-Key": "MDEyMzQ1Njc4OWFiY2RlZg==", "Sec-WebSocket-Protocol": "binary", **(headers or {})}
    if origin:
        hdrs["Origin"] = origin
    try:
        r = requests.get(base + path, headers=hdrs, cookies=cookies, timeout=5, stream=True)
        status = r.status_code
        r.close()
        return status
    except requests.exceptions.ReadTimeout:
        return 101


def read_events(client, until, timeout=60):
    """Collect (event, data) from /api/events until until(events) is true."""
    events = []
    with client.get("/api/events", stream=True, timeout=timeout) as r:
        kind = None
        deadline = time.time() + timeout
        for line in r.iter_lines():
            if line.startswith(b"event: "):
                kind = line[7:].decode()
            elif line.startswith(b"data: "):
                events.append((kind, json.loads(line[6:])))
                kind = None
                if until(events):
                    return events
            if time.time() > deadline:
                break
    raise AssertionError(f"events not seen: {events[-5:]}")


# --- deskyard forward ------------------------------------------------------------------------

class Forward:
    """A running `deskyard forward`, with its output collected as it comes."""

    def __init__(self, cli, remote, *specs, name=None):
        self.p = subprocess.Popen([cli, "-d", name or remote.name, "forward", *specs], env=remote.env,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.out, self.err = [], []
        self.lines = queue.Queue()
        for stream, sink in ((self.p.stdout, self.out), (self.p.stderr, self.err)):
            threading.Thread(target=self._read, args=(stream, sink), daemon=True).start()

    def _read(self, stream, sink):
        for line in stream:
            sink.append(line)
            self.lines.put(line)

    def wait_for(self, text, timeout=60):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if any(text in line for line in self.out + self.err):
                return
            if self.p.poll() is not None:
                break
            time.sleep(0.1)
        raise AssertionError(f"no {text!r} from forward (exit {self.p.poll()}):\n{''.join(self.out)}{''.join(self.err)}")

    def stop(self):
        if self.p.poll() is None:
            self.p.send_signal(signal.SIGINT)
            try:
                self.p.wait(10)
            except subprocess.TimeoutExpired:
                self.p.kill()
        return self.p.returncode


# --- the web UI in a browser -----------------------------------------------------------------

# The desktop page's tests run in each; WebKit only where -m lets webkit in.
BROWSERS = ["chromium", "firefox", pytest.param("webkit", marks=pytest.mark.webkit)]

# The frame's hooks (supervisor/internal/server/screen_additions.js).
SCREEN = "document.querySelector('iframe')?.contentWindow?.deskyardScreen"
SHOWING = f"{SCREEN}?.state() === 'connected' && {SCREEN}.video() !== null"
# Selkies' sound, mic and webcam in the frame, from our additions.
MEDIA = f"{SCREEN}?.media()"
FAKE_MEDIA = ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"]


def sign_in(browser, base, user="admin", password=ADMIN_PASSWORD, **ctx):
    """A new page in its own context of browser, signed in."""
    ctx.setdefault("viewport", {"width": 1280, "height": 860})
    page = browser.new_context(**ctx).new_page()
    page.goto(base + "/")
    page.wait_for_url("**/login**")
    page.fill("#u", user)
    page.fill("#p", password)
    page.get_by_role("button", name="Sign in").click()
    page.wait_for_url(lambda u: "/login" not in u)
    return page


def signed_in(p, base, user="admin", password=ADMIN_PASSWORD, browser="chromium", **ctx):
    """Launches browser (a Playwright browser type's name) and signs in: (browser, page)."""
    b = getattr(p, browser).launch()
    return b, sign_in(b, base, user, password, **ctx)


def wait_loaded(page, expression: str, since: int, timeout=30000):
    """page.wait_for_function(expression), loading the page again if Chromium
    failed one of its loads after `since` (netchange.failures, read before the
    page was opened): what a page loads after its goto, such as the screen's
    frame, isn't retried by goto."""
    for attempt in range(1, 4):
        try:
            return page.wait_for_function(expression, timeout=timeout)
        except PlaywrightError:
            if netchange.failures(page) == since or attempt == 3:
                raise
            print(f"wait_loaded: {netchange.CHANGED} on the way; loading the page again")
            since = netchange.failures(page)
            page.reload()


def open_desktop(page, base, name, status=True):
    """The desktop page, showing the picture (status: and saying so; a phone doesn't).
    Opened again if Chromium failed one of its loads (netchange.py): the screen's
    frame loads after the page."""
    for attempt in range(1, 4):
        seen = netchange.failures(page)
        page.goto(f"{base}/{name}/")
        try:
            if status:
                expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)
            page.wait_for_function(SHOWING, timeout=30000)
            # And the remote screen's size is known (PICTURE scales by it).
            page.wait_for_function(f"{SCREEN}.display()?.width > 0 && {SCREEN}.display()?.height > 0", timeout=30000)
            return
        except (AssertionError, PlaywrightError):
            if netchange.failures(page) == seen or attempt == 3:
                raise
            print(f"open_desktop: {netchange.CHANGED} on the way; opening it again")


def audio_switch(page, label):
    """The Audio panel's switch, with the panel open."""
    if not page.get_by_role("dialog", name="Audio").is_visible():
        page.get_by_role("button", name="Audio", exact=True).click()
    return page.get_by_role("dialog", name="Audio").get_by_role("switch", name=label)


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


def fits(pic) -> bool:
    """The picture is scaled to fit the frame at the stream's aspect ratio."""
    return (pic["width"] <= pic["frameWidth"] + 1 and pic["height"] <= pic["frameHeight"] + 1
            and (abs(pic["width"] - pic["frameWidth"]) <= 1 or abs(pic["height"] - pic["frameHeight"]) <= 1)
            and abs(pic["width"] / pic["height"] - pic["remoteWidth"] / pic["remoteHeight"]) < 0.01)


def frame_size(page) -> list[int]:
    return page.evaluate("[document.querySelector('iframe').clientWidth, document.querySelector('iframe').clientHeight]")
