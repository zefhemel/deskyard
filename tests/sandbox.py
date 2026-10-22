"""Helpers for driving workspace sandbox containers through the docker CLI."""

from __future__ import annotations

import shlex
import subprocess
import time
import uuid
from pathlib import Path

import requests

REPO = Path(__file__).resolve().parent.parent
SECCOMP = REPO / "seccomp" / "flatpak.json"
DEFAULT_ENV = {"WORKSPACE_PASSWORD": "test-password"}
AUTH = ("workspace", "test-password")


def docker(*args: str, check: bool = True, input: str | None = None) -> subprocess.CompletedProcess:
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=check, input=input)


class Sandbox:
    def __init__(self, name: str):
        self.name = name

    def exec(self, cmd: str, user: str = "workspace", login: bool = True, check: bool = True,
             timeout: float = 300) -> subprocess.CompletedProcess:
        argv = ["docker", "exec", "-u", user, self.name]
        argv += ["bash", "-lc", cmd] if login else shlex.split(cmd)
        res = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        if check and res.returncode != 0:
            raise AssertionError(f"{cmd!r} failed ({res.returncode}):\n{res.stdout}\n{res.stderr}")
        return res

    def exec_bytes(self, cmd: str, user: str = "workspace") -> bytes:
        res = subprocess.run(["docker", "exec", "-u", user, self.name, "bash", "-lc", cmd],
                             capture_output=True, check=True)
        return res.stdout

    def port(self, container_port: int) -> int:
        out = docker("port", self.name, f"{container_port}/tcp").stdout.strip().splitlines()[0]
        return int(out.rsplit(":", 1)[1])

    @property
    def web_url(self) -> str:
        return f"http://127.0.0.1:{self.port(8080)}"

    @property
    def cdp_url(self) -> str:
        return f"http://127.0.0.1:{self.port(9222)}"

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
        r = requests.get(self.web_url, timeout=3)
        assert r.status_code == 401, r.status_code
        r = requests.get(self.cdp_url + "/json/version", timeout=3)
        assert r.status_code == 200, r.status_code

    def stop(self) -> None:
        docker("stop", "-t", "10", self.name)

    def remove(self) -> None:
        docker("rm", "-f", "-v", self.name, check=False)


def start_sandbox(image: str, *, home: str, system: str, flatpak: bool = True,
                  env: dict[str, str] | None = None, name: str | None = None,
                  wait: bool = True) -> Sandbox:
    name = name or f"ac-test-{uuid.uuid4().hex[:8]}"
    argv = ["run", "-d", "--name", name, "--shm-size", "1g", "--memory", "2g",
            "-v", f"{home}:/workspace", "-v", f"{system}:/var/lib/workspace-system",
            "-p", "127.0.0.1::8080", "-p", "127.0.0.1::5901", "-p", "127.0.0.1::9222"]
    if flatpak:
        argv += ["--security-opt", f"seccomp={SECCOMP}", "--security-opt", "apparmor=unconfined",
                 "--security-opt", "systempaths=unconfined"]
    merged = dict(DEFAULT_ENV if env is None else env)
    for k, v in merged.items():
        argv += ["-e", f"{k}={v}"]
    docker(*argv, image)
    sb = Sandbox(name)
    if wait:
        sb.wait_ready()
    return sb
