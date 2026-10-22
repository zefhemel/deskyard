"""The computer command against a real supervisor, as an agent elsewhere uses it."""

import os
import socket
import subprocess
import time

import pytest
from playwright.sync_api import sync_playwright

from conftest import ROOT


@pytest.fixture(scope="session")
def cli(tmp_path_factory) -> str:
    out = tmp_path_factory.mktemp("bin") / "computer"
    subprocess.run(["go", "build", "-o", str(out), "./cmd/computer"], cwd=ROOT, check=True)
    return str(out)


@pytest.fixture
def remote(cli, admin, computer, name, tmp_path):
    t = admin.post(f"/api/computers/{name}/tokens", json={"name": "cli test", "expires": None}).json()
    env = {**os.environ, "XDG_CONFIG_HOME": str(tmp_path / "config"), "HOME": str(tmp_path)}
    env.pop("COMPUTER_REMOTE", None)

    def run(*args, input=None, check=True):
        r = subprocess.run([cli, *args], capture_output=True, env=env, input=input, timeout=120)
        if check and r.returncode != 0:
            raise AssertionError(f"computer {args} failed ({r.returncode}): {r.stderr.decode()}")
        return r

    run("remote", "add", name, f"{admin.base}/{name}", "--token", t["token"])
    run.name = name
    run.env = env
    return run


def test_status_and_exec(remote):
    assert remote.name in remote("-c", remote.name, "status").stdout.decode()
    r = remote("-c", remote.name, "exec", "--", "id", "-u")
    assert r.stdout.strip() == b"1000"
    r = remote("-c", remote.name, "exec", "--", "sh", "-c", "exit 5", check=False)
    assert r.returncode == 5


def test_cp_round_trip(remote, tmp_path):
    src = tmp_path / "hello.txt"
    src.write_bytes(b"hello from the host\n")
    remote("cp", str(src), f"{remote.name}:/workspace/hello.txt")
    assert remote("-c", remote.name, "exec", "--", "stat", "-c", "%u", "/workspace/hello.txt").stdout.strip() == b"1000"
    back = tmp_path / "back.txt"
    remote("cp", f"{remote.name}:/workspace/hello.txt", str(back))
    assert back.read_bytes() == src.read_bytes()


def test_screenshot(remote, tmp_path):
    out = tmp_path / "s.png"
    remote("-c", remote.name, "screenshot", str(out))
    assert out.read_bytes()[:4] == b"\x89PNG"
    assert remote("-c", remote.name, "size").stdout.split() == [b"1280", b"800"]


def test_browser_connect(cli, remote):
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    p = subprocess.Popen([cli, "-c", remote.name, "browser", "connect", "--port", str(port)], env=remote.env,
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
