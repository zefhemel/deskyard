"""The desktop's screen: Xvfb as the X server, Selkies streaming it over one
WebSocket behind Caddy's token, with only the features Deskyard offers."""

import asyncio
import io
import json
import time

import pytest
import requests
import websockets
from PIL import Image

from sandbox import TOKEN_HEADER, docker

# Where the Selkies wheel keeps its web client; the supervisor extracts it from
# the image (per image ID).
WEB_CLIENT = "/opt/selkies/lib/python3.13/site-packages/selkies/selkies_web"
# The Origin the supervisor sends with the stream it proxies.
SUPERVISOR_ORIGIN = "http://supervisor.deskyard.invalid"
# Anchored, so it doesn't match the shell running it.
SELKIES_PID = "pgrep -f '^/opt/selkies/bin/python3 /opt/selkies/bin/selkies '"


async def first_binary_frame(url: str, timeout: float) -> bytes:
    async with websockets.connect(url, additional_headers=TOKEN_HEADER, max_size=None,
                                  open_timeout=timeout) as ws:
        # What Selkies' client sends first: its settings, which start the stream.
        await ws.send("SETTINGS," + json.dumps({"displayId": "primary"}))
        deadline = time.monotonic() + timeout
        while True:
            msg = await asyncio.wait_for(ws.recv(), deadline - time.monotonic())
            if isinstance(msg, bytes):
                return msg


def test_stream_behind_token(sandbox):
    assert requests.get(sandbox.web_url + "/", timeout=5).status_code == 401
    assert requests.get(sandbox.web_url + "/api/health", timeout=5).status_code == 401
    assert sandbox.get("/api/health").status_code == 200
    ws_url = sandbox.web_url.replace("http://", "ws://") + "/api/websockets"
    with pytest.raises(websockets.InvalidStatus) as refused:
        asyncio.run(websockets.connect(ws_url, open_timeout=5).__aenter__())
    assert refused.value.response.status_code == 401
    assert len(asyncio.run(first_binary_frame(ws_url, 10))) > 0


def test_stream_origin(sandbox):
    """The Origin the supervisor sends is let in; another site's is refused."""
    ws_url = sandbox.web_url.replace("http://", "ws://") + "/api/websockets"

    async def connect(origin):
        async with websockets.connect(ws_url, additional_headers=TOKEN_HEADER,
                                      origin=origin, open_timeout=5):
            pass

    asyncio.run(connect(SUPERVISOR_ORIGIN))
    with pytest.raises(websockets.InvalidStatus) as refused:
        asyncio.run(connect("http://evil.example"))
    assert refused.value.response.status_code == 403


def test_agent_tools_on_x11(sandbox):
    w, h = map(int, sandbox.exec("deskyard size").stdout.split())
    assert (w, h) == (1280, 800)
    png = sandbox.exec_bytes("deskyard screenshot")
    assert Image.open(io.BytesIO(png)).size == (w, h)
    assert Image.open(io.BytesIO(sandbox.exec_bytes("deskyard zoom 0 0 200 100"))).size == (200, 100)
    sandbox.exec("deskyard click 640 400 && deskyard type 'hi' && deskyard key Escape")
    sandbox.exec("printf deskyard-clip | xclip -selection clipboard")
    assert sandbox.exec("xclip -o -selection clipboard").stdout == "deskyard-clip"


@pytest.mark.parametrize("method,path", [
    ("GET", "/api/screenshot"),
    ("GET", "/api/print/doc.pdf"),
    ("POST", "/api/tokens"),
    ("GET", "/api/files/"),
    ("GET", "/api/recording"),
    ("POST", "/api/recording"),
    ("GET", "/api/sessions"),
    ("POST", "/api/switch"),
    ("GET", "/api/metrics"),
])
def test_disabled_features(sandbox, method, path):
    r = requests.request(method, sandbox.web_url + path, headers=TOKEN_HEADER, json={}, timeout=5)
    assert r.status_code in (403, 404), (path, r.status_code, r.text[:200])


def test_upload_lands_on_desktop(sandbox):
    r = requests.post(sandbox.web_url + "/api/upload", data=b"uploaded",
                      headers={**TOKEN_HEADER, "X-Upload-Path": "up.txt",
                               "Content-Type": "application/octet-stream"}, timeout=10)
    assert r.status_code == 200, r.text
    assert sandbox.exec("cat ~/Desktop/up.txt").stdout == "uploaded"
    assert sandbox.exec("stat -c %U ~/Desktop/up.txt").stdout.strip() == "user"


def test_speaker_default(sandbox):
    deadline = time.time() + 20
    while True:
        out = sandbox.exec("pactl info", check=False).stdout
        if "Default Sink: deskyard-speaker" in out or time.time() > deadline:
            break
        time.sleep(1)
    assert "Default Sink: deskyard-speaker" in out, out


def test_screen_runs_as_user(sandbox):
    for proc in ("Xvfb", "pipewire", "wireplumber"):
        users = sandbox.exec(f"ps -C {proc} -o user= | sort -u", user="root").stdout.split()
        assert users == ["user"], (proc, users)
    users = sandbox.exec(f"ps -o user= -p $({SELKIES_PID})", user="root").stdout.split()
    assert users == ["user"], users


def test_webcam_off_without_v4l2loopback(sandbox):
    env = sandbox.exec(f"tr '\\0' '\\n' < /proc/$({SELKIES_PID})/environ").stdout
    assert "SELKIES_WEBCAM_ENABLED=false|locked" in env.splitlines()


def test_contract(image):
    label = docker("image", "inspect", image, "--format", '{{index .Config.Labels "deskyard.contract"}}')
    assert label.stdout.strip() == "3"


def test_web_client_in_image(image):
    out = docker("run", "--rm", "--entrypoint", "ls", image, WEB_CLIENT).stdout.split()
    assert "index.html" in out
