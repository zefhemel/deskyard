"""The deskyard command against a real supervisor, as an agent elsewhere uses it."""

import http.server
import os
import queue
import signal
import socket
import subprocess
import threading
import time

import pytest
import requests
from playwright.sync_api import sync_playwright

from conftest import ROOT, free_port


@pytest.fixture(scope="session")
def cli(tmp_path_factory) -> str:
    out = tmp_path_factory.mktemp("bin") / "deskyard"
    subprocess.run(["go", "build", "-o", str(out), "./cmd/deskyard"], cwd=ROOT, check=True)
    return str(out)


@pytest.fixture
def remote(cli, admin, desktop, name, tmp_path):
    t = admin.post(f"/api/desktops/{name}/tokens", json={"name": "cli test", "expires": None}).json()
    env = {**os.environ, "XDG_CONFIG_HOME": str(tmp_path / "config"), "HOME": str(tmp_path)}
    env.pop("DESKYARD_REMOTE", None)

    def run(*args, input=None, check=True):
        r = subprocess.run([cli, *args], capture_output=True, env=env, input=input, timeout=120)
        if check and r.returncode != 0:
            raise AssertionError(f"deskyard {args} failed ({r.returncode}): {r.stderr.decode()}")
        return r

    run("remote", "add", name, f"{admin.base}/{name}", "--token", t["token"])
    run.name = name
    run.env = env
    return run


def test_status_and_exec(remote):
    assert remote.name in remote("-d", remote.name, "status").stdout.decode()
    r = remote("-d", remote.name, "exec", "--", "id", "-u")
    assert r.stdout.strip() == b"1000"
    r = remote("-d", remote.name, "exec", "--", "sh", "-c", "exit 5", check=False)
    assert r.returncode == 5


def test_cp_round_trip(remote, tmp_path):
    src = tmp_path / "hello.txt"
    src.write_bytes(b"hello from the host\n")
    remote("cp", str(src), f"{remote.name}:/workspace/hello.txt")
    assert remote("-d", remote.name, "exec", "--", "stat", "-c", "%u", "/workspace/hello.txt").stdout.strip() == b"1000"
    back = tmp_path / "back.txt"
    remote("cp", f"{remote.name}:/workspace/hello.txt", str(back))
    assert back.read_bytes() == src.read_bytes()


def test_screenshot(remote, tmp_path):
    out = tmp_path / "s.png"
    remote("-d", remote.name, "screenshot", str(out))
    assert out.read_bytes()[:4] == b"\x89PNG"
    assert remote("-d", remote.name, "size").stdout.split() == [b"1280", b"800"]


def test_browser_connect(cli, remote):
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    p = subprocess.Popen([cli, "-d", remote.name, "browser", "connect", "--port", str(port)], env=remote.env,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        time.sleep(1.5)
        with sync_playwright() as pw:
            b = pw.chromium.connect_over_cdp(f"http://127.0.0.1:{port}", timeout=90_000)
            page = b.contexts[0].new_page()
            page.goto("data:text/html,<title>cli-cdp</title>")
            assert page.title() == "cli-cdp"
            b.close()
    finally:
        p.terminate()
        p.wait(5)


def test_self_description(cli):
    r = subprocess.run([cli, "describe", "--json"], capture_output=True, check=True)
    assert b'"exec"' in r.stdout and b'"browser connect"' in r.stdout or b'"browser"' in r.stdout
    assert b"/workspace" in subprocess.run([cli, "guide"], capture_output=True, check=True).stdout


# ---- forward


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


@pytest.fixture
def forwards():
    started = []

    def start(*a, **kw):
        f = Forward(*a, **kw)
        started.append(f)
        return f

    yield start
    for f in started:
        f.stop()


@pytest.fixture
def local_http():
    """A local HTTP server on a free port answering with a known body."""
    body = b"hello from the agent's machine"

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass

    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    srv.body = body
    srv.port = srv.server_address[1]
    yield srv
    srv.shutdown()


def desktop_curl(remote, port, name=None):
    return remote("-d", name or remote.name, "exec", "--", "curl", "-s", "-m", "10", f"http://127.0.0.1:{port}/", check=False)


def desktop_http_server(run, port, name):
    """Starts python3 -m http.server PORT in a desktop, in the background."""
    run("-d", name, "exec", "--", "bash", "-lc", f"setsid -f python3 -m http.server {port} --bind 127.0.0.1 >/dev/null 2>&1")
    for _ in range(50):
        if run("-d", name, "exec", "--", "curl", "-sf", f"http://127.0.0.1:{port}/", check=False).returncode == 0:
            return
        time.sleep(0.2)
    raise AssertionError(f"http.server {port} didn't start in {name}")


def test_forward_in(cli, remote, forwards, local_http):
    p = local_http.port
    f = forwards(cli, remote, str(p))
    f.wait_for(f"in {p} {p}")
    assert f"Linked to {remote.name}: {p} → {remote.name}:{p}" in "".join(f.err)
    r = desktop_curl(remote, p)
    assert r.returncode == 0 and r.stdout == local_http.body


def test_forward_out(cli, remote, forwards):
    desktop_http_server(remote, 8765, remote.name)
    q = free_port()
    f = forwards(cli, remote, "-R", f"8765:{q}")
    f.wait_for(f"out 8765 127.0.0.1:{q}")
    r = requests.get(f"http://127.0.0.1:{q}/", timeout=20)
    assert r.status_code == 200 and "Directory listing" in r.text


def test_forward_listed(cli, remote, forwards, local_http, admin):
    desktop_http_server(remote, 8765, remote.name)
    p, q = local_http.port, free_port()
    f = forwards(cli, remote, str(p), "-R", f"8765:{q}")
    f.wait_for(f"in {p} {p}")
    f.wait_for(f"out 8765 127.0.0.1:{q}")
    want = [{"token": "cli test", "direction": "in", "port": p}, {"token": "cli test", "direction": "out", "port": 8765}]
    assert admin.desktop(remote.name)["forwards"] == want
    assert f.stop() == 0
    deadline = time.time() + 2
    while admin.desktop(remote.name)["forwards"] != [] and time.time() < deadline:
        time.sleep(0.1)
    assert admin.desktop(remote.name)["forwards"] == []


def test_forward_port_in_use(cli, remote, forwards, local_http):
    desktop_http_server(remote, 8765, remote.name)
    with socket.socket() as taken:
        taken.bind(("127.0.0.1", 0))
        taken.listen()
        q = taken.getsockname()[1]
        p = local_http.port
        f = forwards(cli, remote, "-R", f"8765:{q}", str(p))
        f.wait_for(f"in {p} {p}")
        f.wait_for(f"127.0.0.1:{q}: port in use")
        assert desktop_curl(remote, p).stdout == local_http.body


def test_forward_low_port(cli, remote, forwards):
    f = forwards(cli, remote, "80:80")
    f.wait_for(f"{remote.name}:80: port below 1024")


def test_forward_survives_restart(cli, remote, forwards, local_http, admin):
    p = local_http.port
    f = forwards(cli, remote, str(p))
    f.wait_for(f"in {p} {p}")
    assert desktop_curl(remote, p).stdout == local_http.body
    assert admin.post(f"/api/desktops/{remote.name}/restart").status_code == 204
    deadline = time.time() + 60
    while time.time() < deadline:
        if desktop_curl(remote, p).stdout == local_http.body:
            break
        time.sleep(1)
    else:
        raise AssertionError("not forwarding 60 s after the restart:\n" + "".join(f.out + f.err))
    assert f.p.poll() is None
    assert any(line.startswith("Lost ") for line in f.err)


def test_forward_other_desktop_refused(cli, remote, forwards, admin):
    other = remote.name + "b"
    admin.create(other)
    try:
        t = admin.post(f"/api/desktops/{other}/tokens", json={"name": "other", "expires": None}).json()
        env = {**remote.env, "DESKYARD_URL": f"{admin.base}/{other}", "DESKYARD_TOKEN": t["token"]}

        def run_other(*args, check=True):
            r = subprocess.run([cli, *args], capture_output=True, env=env, timeout=120)
            assert not check or r.returncode == 0, r.stderr
            return r

        desktop_http_server(run_other, 8766, other)
        q = free_port()
        f = forwards(cli, remote, "-R", f"8766:{q}")
        f.wait_for(f"out 8766 127.0.0.1:{q}")
        with pytest.raises(requests.exceptions.ConnectionError):
            requests.get(f"http://127.0.0.1:{q}/", timeout=20)
    finally:
        admin.delete(f"/api/desktops/{other}?files=1")
