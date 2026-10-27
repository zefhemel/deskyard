"""The deskyard command against a real supervisor, as an agent elsewhere uses it."""

import json
import socket
import subprocess
import time

import pytest
import requests
from playwright.sync_api import sync_playwright

from conftest import free_port
from helpers import make_credential, wait_until


def test_status_and_exec(remote):
    assert remote.name in remote("-e", remote.name, "status").stdout.decode()
    r = remote("-e", remote.name, "exec", "--", "id", "-u")
    assert r.stdout.strip() == b"1000"
    r = remote("-e", remote.name, "exec", "--", "sh", "-c", "exit 5", check=False)
    assert r.returncode == 5


def test_environment_flag_and_saved_server(cli, remote, tmp_path):
    """-e names the environment; the old -d is an unknown option."""
    assert remote.name in remote("--environment", remote.name, "status").stdout.decode()
    assert remote.name in remote(f"-e={remote.name}", "status").stdout.decode()
    r = remote("-d", remote.name, "status", check=False)
    assert r.returncode == 2 and r.stderr.decode().startswith("deskyard:"), r.stderr
    saved = json.loads((tmp_path / "config" / "deskyard" / "servers.json").read_text())
    entry = saved["servers"][saved["default"]]
    assert entry["environment"] == remote.name and entry["username"] == remote.username, saved
    assert entry["credential"].startswith("dyc_") and "desktop" not in entry, saved


def test_cp_round_trip(remote, tmp_path):
    src = tmp_path / "hello.txt"
    src.write_bytes(b"hello from the host\n")
    remote("cp", str(src), f"{remote.name}:/workspace/hello.txt")
    assert remote("-e", remote.name, "exec", "--", "stat", "-c", "%u", "/workspace/hello.txt").stdout.strip() == b"1000"
    back = tmp_path / "back.txt"
    remote("cp", f"{remote.name}:/workspace/hello.txt", str(back))
    assert back.read_bytes() == src.read_bytes()


def test_screenshot(remote, tmp_path):
    out = tmp_path / "s.png"
    remote("-e", remote.name, "screenshot", str(out))
    assert out.read_bytes()[:4] == b"\x89PNG"
    assert remote("-e", remote.name, "size").stdout.split() == [b"1280", b"800"]


def test_browser_connect(cli, remote):
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    p = subprocess.Popen([cli, "-e", remote.name, "browser", "connect", "--port", str(port)], env=remote.env,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def listening():
        with socket.socket() as s:
            return s.connect_ex(("127.0.0.1", port)) == 0

    try:
        assert wait_until(listening, 10, 0.1), "browser connect isn't listening"
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
    assert b'"exec"' in r.stdout and (b'"browser connect"' in r.stdout or b'"browser"' in r.stdout), r.stdout[:500]
    assert b"/workspace" in subprocess.run([cli, "guide"], capture_output=True, check=True).stdout


# ---- forward


def desktop_curl(remote, port, name=None):
    return remote("-e", name or remote.name, "exec", "--", "curl", "-s", "-m", "10", f"http://127.0.0.1:{port}/", check=False)


def desktop_http_server(run, port, name):
    """Starts python3 -m http.server PORT in a desktop, in the background."""
    run("-e", name, "exec", "--", "bash", "-lc", f"setsid -f python3 -m http.server {port} --bind 127.0.0.1 >/dev/null 2>&1")
    for _ in range(50):
        if run("-e", name, "exec", "--", "curl", "-sf", f"http://127.0.0.1:{port}/", check=False).returncode == 0:
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
    want = [{"token": remote.client, "direction": "in", "port": p}, {"token": remote.client, "direction": "out", "port": 8765}]
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
    assert admin.post(f"/api/environments/{remote.name}/restart").status_code == 204
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
        t = make_credential(admin, other, client="other")
        env = {**remote.env, "DESKYARD_URL": f"{admin.base}/{other}", "DESKYARD_CREDENTIAL": t["credential"]}

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
        admin.delete(f"/api/environments/{other}?files=1")
