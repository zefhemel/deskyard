"""The desktop's screen: Xvfb as the X server, Selkies streaming it over one
WebSocket behind Caddy's token, with only the features Deskyard offers."""

import asyncio
import json
import time
from pathlib import Path
from sandbox import DESKTOP, PANELS

import pytest
import requests
import websockets

from sandbox import TOKEN_HEADER, docker

# Where the Selkies wheel keeps its web client; the supervisor extracts it from
# the image (per image ID).
WEB_CLIENT = "/opt/selkies/lib/python3.13/site-packages/selkies/selkies_web"
# The Origin the supervisor sends with the stream it proxies.
SUPERVISOR_ORIGIN = "http://supervisor.deskyard.invalid"
# Anchored, so it doesn't match the shell running it.
SELKIES_PID = "pgrep -f '^/opt/selkies/bin/python3 /opt/selkies/bin/selkies '"


def test_input_recovers_after_x_connection_failure(sandbox):
    """A failed reconnect must not strand input or revive a disconnected handler."""
    script = Path(__file__).parent / "native" / "selkies-input-recovery.py"
    docker("cp", str(script), f"{sandbox.name}:/tmp/selkies-input-recovery.py")
    sandbox.exec("/opt/selkies/bin/python /tmp/selkies-input-recovery.py", timeout=40)


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


def test_everything_behind_token(sandbox):
    """Caddy lets nothing through without the supervisor's token: the page, Selkies'
    API and stream, CDP."""
    assert requests.get(sandbox.web_url + "/", timeout=5).status_code == 401
    assert requests.get(sandbox.web_url, headers={"X-Deskyard-Token": "nope"}, timeout=5).status_code == 401
    assert requests.get(sandbox.web_url + "/api/health", timeout=5).status_code == 401
    assert requests.get(sandbox.web_url + "/_cdp/json/version", timeout=5).status_code == 401
    assert sandbox.get("/").status_code == 200
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


def test_clipboard_on_x11(sandbox):
    # (The agent tools on Xvfb: test_cli.)
    sandbox.exec("printf deskyard-clip | xclip -selection clipboard")
    assert sandbox.exec("xclip -o -selection clipboard").stdout == "deskyard-clip"


DISABLED = [
    ("GET", "/api/screenshot"),
    ("GET", "/api/print/doc.pdf"),
    ("POST", "/api/tokens"),
    ("GET", "/api/files/"),
    ("GET", "/api/recording"),
    ("POST", "/api/recording"),
    ("GET", "/api/sessions"),
    ("POST", "/api/switch"),
    ("GET", "/api/metrics"),
]


def test_disabled_features(sandbox):
    answers = {}
    for method, path in DISABLED:
        r = requests.request(method, sandbox.web_url + path, headers=TOKEN_HEADER, json={}, timeout=5)
        answers[(method, path)] = (r.status_code, r.text[:200])
    assert all(code in (403, 404) for code, _ in answers.values()), answers


def test_selkies_upload_refused(sandbox):
    """Uploads go through Deskyard's own endpoint; Selkies' is closed in Caddy
    and off in Selkies itself."""
    headers = {**TOKEN_HEADER, "X-Upload-Path": "up.txt", "Content-Type": "application/octet-stream"}
    r = requests.post(sandbox.web_url + "/api/upload", data=b"uploaded", headers=headers, timeout=10)
    assert r.status_code == 404, r.text
    status = sandbox.exec("curl -s -o /dev/null -w '%{http_code}' -X POST -H 'X-Upload-Path: up.txt'"
                          " --data-binary uploaded http://127.0.0.1:6080/api/upload").stdout
    assert status == "403"
    assert sandbox.exec("test -e ~/Desktop/up.txt", check=False).returncode != 0


# Reads /dev/input/js0 through the Input Interposer (preloaded by
# /etc/workspace/gamepads.sh) until button 0 is pressed; prints each js_event.
JS_READER = r"""
import os, struct
fd = os.open('/dev/input/js0', os.O_RDONLY)
print('open', flush=True)
while True:
    _, value, kind, number = struct.unpack('IhBB', os.read(fd, 8))
    print(kind, number, value, flush=True)
    if kind == 1 and number == 0 and value == 1:
        break
"""


def test_gamepad_reaches_apps(sandbox):
    """A pad the page sends over the stream shows up as /dev/input/js0 to the
    desktop's apps, which run with the Input Interposer preloaded."""
    import base64
    import subprocess

    ws_url = sandbox.web_url.replace("http://", "ws://") + "/api/websockets"
    reader: dict = {}

    async def drive():
        async with websockets.connect(ws_url, additional_headers=TOKEN_HEADER, max_size=None) as ws:
            await ws.send("SETTINGS," + json.dumps({"displayId": "primary"}))
            name = base64.b64encode(b"Deskyard test pad").decode()
            await ws.send(f"js,c,0,{name},4,16")
            await asyncio.sleep(2)
            proc = subprocess.Popen(
                ["docker", "exec", "-u", "user", sandbox.name, "bash", "-lc",
                 ". /etc/workspace/gamepads.sh && exec timeout 20 /usr/bin/python3 -c \"$0\"", JS_READER],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            reader["proc"] = proc
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline and proc.poll() is None:
                await asyncio.sleep(0.5)
                await ws.send("js,b,0,0,1")
                await asyncio.sleep(0.2)
                await ws.send("js,b,0,0,0")

    asyncio.run(drive())
    proc = reader["proc"]
    out, err = proc.communicate(timeout=20)
    assert proc.returncode == 0, (out, err)
    assert out.splitlines()[-1] == "1 0 1", out


def test_session_apps_preload_gamepads(sandbox):
    """XFCE's processes and the shared Chromium run with the gamepad libraries
    loaded; Selkies doesn't. (Chromium rewrites its environ, so read the maps.)"""
    sandbox.cdp_json(timeout=60)  # starts Chromium

    def loaded(pid_cmd: str) -> str:
        return sandbox.exec(f"grep -oE 'selkies_input_interposer.so|fake-udev/libudev.so.1' /proc/$({pid_cmd})/maps"
                            " | sort -u", check=False).stdout

    for pid_cmd in (f"pgrep -o -x {PANELS[DESKTOP]}", "pgrep -o -x chromium"):
        assert loaded(pid_cmd).split() == ["fake-udev/libudev.so.1", "selkies_input_interposer.so"], pid_cmd
    assert loaded(SELKIES_PID) == ""
    sandbox.exec("test -d /dev/input")


def test_virtual_mic_at_start(sandbox):
    # The desktop's speaker is the default output. Selkies' virtual mic is there before any viewer's mic, so no viewer's
    # connection owns it (and takes it away from the others when it ends);
    # it's the default mic, and the speaker stays the default output.
    deadline = time.time() + 20
    while True:
        sources = sandbox.exec("pactl list short sources", check=False).stdout
        if "SelkiesVirtualMic" in sources or time.time() > deadline:
            break
        time.sleep(1)
    assert "SelkiesVirtualMic" in sources, sources
    assert "input" in sandbox.exec("pactl list short sinks").stdout.split()
    info = sandbox.exec("pactl info").stdout
    assert "Default Sink: deskyard-speaker" in info
    assert "Default Source: output.SelkiesVirtualMic" in info, info


def test_sound_server_ran_its_commands(sandbox):
    # pipewire-pulse waits for PipeWire, so the virtual mic in its pulse.cmd
    # is made by it (not by start-selkies' fallback, nor by Selkies).
    sandbox.exec("pactl list short sources | grep -q SelkiesVirtualMic")
    logs = docker("logs", sandbox.name).stdout + docker("logs", sandbox.name).stderr
    assert "can't run command" not in logs, [l for l in logs.splitlines() if "can't run command" in l]
    assert "virtual mic is missing" not in logs


def test_screen_runs_as_user(sandbox):
    for proc in ("Xvfb", "pipewire", "wireplumber"):
        users = sandbox.exec(f"ps -C {proc} -o user= | sort -u", user="root").stdout.split()
        assert users == ["user"], (proc, users)
    users = sandbox.exec(f"ps -o user= -p $({SELKIES_PID})", user="root").stdout.split()
    assert users == ["user"], users


def test_selkies_settings(sandbox):
    env = sandbox.exec(f"tr '\\0' '\\n' < /proc/$({SELKIES_PID})/environ").stdout.splitlines()
    # No webcam without v4l2loopback.
    assert "SELKIES_WEBCAM_ENABLED=false|locked" in env
    # Turbo encodes every frame: about a quarter of a core per idle viewer.
    assert "SELKIES_VIDEO_STREAMING_MODE=false|locked" in env
    # The audio gate clips the start of every sound after silence (patches/README.md).
    assert "DESKYARD_AUDIO_IDLE_S=0" in env


def test_contract(image):
    label = docker("image", "inspect", image, "--format", '{{index .Config.Labels "deskyard.contract"}}')
    assert label.stdout.strip() == "3"


def test_web_client_in_image(image):
    out = docker("run", "--rm", "--entrypoint", "ls", image, WEB_CLIENT).stdout.split()
    assert "index.html" in out
