"""Integration tests: a real supervisor against the local container engine.

SUPERVISOR_TEST_MODE=direct (default) runs the binary on this machine;
SUPERVISOR_TEST_MODE=container runs the supervisor image with the socket and
the data folder mounted at the same path. CC_TEST_IMAGE is the computer image
(default container-computer:test, built from images/debian-xfce).
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import time
import uuid
from pathlib import Path

import pytest
import requests

ROOT = Path(__file__).resolve().parent.parent
REPO = ROOT.parent
IMAGE = os.environ.get("CC_TEST_IMAGE", "container-computer:test")
MODE = os.environ.get("SUPERVISOR_TEST_MODE", "direct")
ADMIN_PASSWORD = "admin-password"


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def docker(*args, check=True) -> subprocess.CompletedProcess:
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=check)


class Client(requests.Session):
    """A signed-in API client (same-origin headers like a browser's fetch)."""

    def __init__(self, base: str):
        super().__init__()
        self.base = base
        self.headers["Origin"] = base

    def request(self, method, url, *a, **kw):
        kw.setdefault("timeout", 60)
        if url.startswith("/"):
            url = self.base + url
        return super().request(method, url, *a, **kw)

    def login(self, username: str, password: str) -> "Client":
        r = self.post("/api/login", json={"username": username, "password": password})
        assert r.status_code == 200, r.text
        return self

    def wait_job(self, job: str, timeout=300) -> dict:
        with self.get(f"/api/jobs/{job}", stream=True, timeout=timeout) as r:
            last = None
            for line in r.iter_lines():
                if line.startswith(b"data: "):
                    last = json.loads(line[6:])
                    if last["status"] != "running":
                        return last
        return last

    def create(self, name: str, **kw) -> dict:
        r = self.post("/api/computers", json={"name": name, "image": IMAGE, **kw})
        assert r.status_code == 202, r.text
        st = self.wait_job(r.json()["job"])
        assert st["status"] == "done", st
        return self.computer(name)

    def computer(self, name: str) -> dict:
        r = self.get(f"/api/computers/{name}")
        assert r.status_code == 200, r.text
        return r.json()


class Supervisor:
    def __init__(self, tmp: Path):
        self.data = tmp / "data"
        self.data.mkdir(parents=True)
        self.port = free_port()
        self.base = f"http://127.0.0.1:{self.port}"
        self.proc = None
        self.container = None

    def start(self):
        env = {
            "SUPERVISOR_DATA_DIR": str(self.data), "SUPERVISOR_ADMIN_PASSWORD": ADMIN_PASSWORD,
            "SUPERVISOR_IMAGE": IMAGE, "SUPERVISOR_SUBNET": "10.215.0.0/20",
        }
        if MODE == "container":
            self.container = f"cc-test-supervisor-{uuid.uuid4().hex[:6]}"
            args = ["run", "-d", "--name", self.container, "-p", f"127.0.0.1:{self.port}:8080",
                    "-v", "/var/run/docker.sock:/var/run/docker.sock", "-v", f"{self.data}:{self.data}"]
            for k, v in env.items():
                args += ["-e", f"{k}={v}"]
            docker(*args, "container-computer-supervisor:test")
        else:
            binary = ROOT / "bin" / "supervisor-test"
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/supervisor"], cwd=ROOT, check=True)
            self.log = open(self.data.parent / "supervisor.log", "w")
            self.proc = subprocess.Popen([str(binary)], env={**os.environ, **env, "SUPERVISOR_LISTEN": f"127.0.0.1:{self.port}"},
                                         stdout=self.log, stderr=subprocess.STDOUT)
        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                requests.get(self.base + "/healthz", timeout=1)
                return
            except requests.RequestException:
                time.sleep(0.3)
        raise TimeoutError("supervisor didn't start:\n" + self.logs())

    def logs(self) -> str:
        if self.container:
            r = docker("logs", self.container, check=False)
            return r.stdout + r.stderr
        return (self.data.parent / "supervisor.log").read_text()

    def stop(self):
        if self.proc:
            self.proc.terminate()
            self.proc.wait(10)
        if self.container:
            docker("rm", "-f", self.container, check=False)

    def client(self) -> Client:
        return Client(self.base)

    def admin(self) -> Client:
        return self.client().login("admin", ADMIN_PASSWORD)


def remove_managed():
    ids = docker("ps", "-aq", "--filter", "label=container-computer.managed=true",
                 "--filter", "network=bridge", check=False).stdout.split()
    return ids


@pytest.fixture(scope="session")
def supervisor(tmp_path_factory):
    # A folder the container engine can mount (on macOS: under the home folder).
    base = Path.home() / ".cache" / "cc-supervisor-tests" / uuid.uuid4().hex[:8]
    base.mkdir(parents=True)
    sup = Supervisor(base)
    if MODE == "container":
        docker("build", "-q", "-t", "container-computer-supervisor:test", str(ROOT))
    sup.start()
    yield sup
    sup.stop()
    # Remove what the tests created: computers carry the test supervisor's data path.
    for name in docker("ps", "-a", "--format", "{{.Names}}", "--filter", "label=container-computer.managed=true", check=False).stdout.split():
        mounts = docker("inspect", "-f", "{{range .Mounts}}{{.Source}} {{end}}", name, check=False).stdout
        if str(sup.data) in mounts:
            docker("rm", "-f", name, check=False)
            docker("network", "rm", name, check=False)
    docker("run", "--rm", "-v", f"{base}:/b", "alpine:3.22", "rm", "-rf", "/b/data", check=False)
    shutil.rmtree(base, ignore_errors=True)


@pytest.fixture(scope="session")
def admin(supervisor) -> Client:
    return supervisor.admin()


@pytest.fixture
def name() -> str:
    return "t" + uuid.uuid4().hex[:6]


@pytest.fixture
def computer(admin, name):
    """A running computer, removed (with its files) afterwards."""
    c = admin.create(name)
    yield c
    admin.delete(f"/api/computers/{name}?files=1")
