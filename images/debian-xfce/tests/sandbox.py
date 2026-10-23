"""Helpers for driving desktop containers through the docker CLI, the way the
supervisor runs them: Flatpak security options, a proxy token, only 8080."""

from __future__ import annotations

import shlex
import subprocess
import time
import uuid
from pathlib import Path
from urllib.parse import urlparse

import requests

REPO = Path(__file__).resolve().parent.parent  # images/debian-xfce
ROOT = REPO.parent.parent
SECCOMP = ROOT / "supervisor" / "internal" / "engine" / "docker" / "flatpak-seccomp.json"
TOKEN = "test-token"
DEFAULT_ENV = {"WORKSPACE_PROXY_TOKEN": TOKEN}
TOKEN_HEADER = {"X-Deskyard-Token": TOKEN}


def docker(*args: str, check: bool = True, input: str | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=check, input=input)


class Sandbox:
    def __init__(self, name: str):
        self.name = name

    def exec(self, cmd: str, user: str = "user", login: bool = True, check: bool = True,
             timeout: float = 300) -> subprocess.CompletedProcess:
        argv = ["docker", "exec", "-u", user, self.name]
        argv += ["bash", "-lc", cmd] if login else shlex.split(cmd)
        res = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        if check and res.returncode != 0:
            raise AssertionError(f"{cmd!r} failed ({res.returncode}):\n{res.stdout}\n{res.stderr}")
        return res

    def exec_bytes(self, cmd: str, user: str = "user") -> bytes:
        res = subprocess.run(["docker", "exec", "-u", user, self.name, "bash", "-lc", cmd],
                             capture_output=True, check=True)
        return res.stdout

    def port(self, container_port: int) -> int:
        out = docker("port", self.name, f"{container_port}/tcp").stdout.strip().splitlines()[0]
        return int(out.rsplit(":", 1)[1])

    @property
    def web_url(self) -> str:
        return f"http://127.0.0.1:{self.port(8080)}"

    def get(self, path: str, **kw) -> requests.Response:
        """A request to Caddy with the supervisor's token."""
        kw.setdefault("timeout", 5)
        return requests.get(self.web_url + path, headers=TOKEN_HEADER, **kw)

    def cdp_json(self, path: str = "/json/version", timeout: float = 5):
        return self.get("/_cdp" + path, timeout=timeout).json()

    def cdp_ws(self, timeout: float = 60) -> str:
        """The browser's CDP WebSocket through Caddy, for Playwright's connect_over_cdp
        (pass headers=TOKEN_HEADER)."""
        ws = self.cdp_json(timeout=timeout)["webSocketDebuggerUrl"]
        u = urlparse(self.web_url)
        return f"ws://{u.hostname}:{u.port}/_cdp{urlparse(ws).path}"

    def logs(self) -> str:
        res = docker("logs", self.name)
        return res.stdout + res.stderr

    def restart(self) -> None:
        docker("restart", "-t", "5", self.name)
        self.wait_ready()

    def wait_ready(self, timeout: float = 90) -> None:
        deadline = time.time() + timeout
        last = None
        while time.time() < deadline:
            try:
                self._check_ready()
                return
            except Exception as e:  # noqa: BLE001 - keep polling until timeout
                last = e
                time.sleep(1)
        raise TimeoutError(f"sandbox {self.name} not ready: {last}\n{self.logs()[-3000:]}")

    def _check_ready(self) -> None:
        self.exec("xdpyinfo -display :1 >/dev/null && pgrep -x xfce4-panel >/dev/null"
                  " && xfconf-query -c xfce4-panel -p /panels/panel-2/position >/dev/null", timeout=10)
        assert self.get("/", timeout=3).status_code == 200

    def stop(self) -> None:
        docker("stop", "-t", "10", self.name)

    def remove(self) -> None:
        docker("rm", "-f", "-v", self.name, check=False)


def wait_until(f, timeout: float = 15.0, every: float = 0.3):
    """Polls f until it returns something true or timeout passes; returns its last value."""
    deadline = time.time() + timeout
    while True:
        v = f()
        if v or time.time() > deadline:
            return v
        time.sleep(every)


def run_args(image: str, *, home: str, env: dict[str, str] | None = None,
             name: str | None = None, docker_args: list[str] = ()) -> tuple[str, list[str]]:
    name = name or f"ac-test-{uuid.uuid4().hex[:8]}"
    argv = ["run", "-d", "--name", name, "--shm-size", "1g", *docker_args,
            "-v", f"{home}:/workspace", "-p", "127.0.0.1::8080",
            "--security-opt", f"seccomp={SECCOMP}", "--security-opt", "apparmor=unconfined",
            "--security-opt", "systempaths=unconfined"]
    for k, v in (DEFAULT_ENV if env is None else env).items():
        argv += ["-e", f"{k}={v}"]
    return name, argv + [image]


def start_sandbox(image: str, *, home: str, env: dict[str, str] | None = None,
                  name: str | None = None, wait: bool = True, docker_args: list[str] = ()) -> Sandbox:
    name, argv = run_args(image, home=home, env=env, name=name, docker_args=docker_args)
    docker(*argv)
    sb = Sandbox(name)
    if wait:
        sb.wait_ready()
    return sb
