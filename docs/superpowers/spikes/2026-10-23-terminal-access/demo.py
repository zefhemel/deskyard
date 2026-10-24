#!/usr/bin/env python3
"""Throwaway, loopback-only Zellij demo. Ctrl-C removes only its own desktop.

Requires the normal supervisor build (`cd supervisor && make build`) and
Docker. Does not change the user's CLI configuration or existing desktops.
"""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[3]
BIN = REPO / "supervisor/bin"
IMAGE = "deskyard-terminal-prototype:local"


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_for(probe, timeout=90):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            if result := probe():
                return result
        except (OSError, urllib.error.URLError) as error:
            last = error
        time.sleep(0.25)
    raise TimeoutError(f"Demo did not become ready: {last}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", default=IMAGE)
    parser.add_argument("--build-image", action="store_true")
    parser.add_argument("--state-dir", type=Path)
    args = parser.parse_args()
    if args.build_image:
        subprocess.run(["docker", "build", "-t", args.image, str(HERE)], check=True)
    for name in ("deskyard", "deskyard-supervisor"):
        if not (BIN / name).is_file():
            parser.error("Build the worktree binaries first: cd supervisor && make build")

    state = args.state_dir or Path(tempfile.mkdtemp(prefix="deskyard-terminal-"))
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    if (state / "data").exists():
        parser.error("Use a fresh state directory for each demo")
    os.chmod(state, 0o700)
    desktop = "zterm-" + secrets.token_hex(4)
    port, web_port = free_port(), free_port()
    while web_port == port:
        web_port = free_port()
    base = f"http://127.0.0.1:{port}"
    terminal = f"http://127.0.0.1:{web_port}"
    password = secrets.token_urlsafe(24)
    # Do not inherit configuration pointing at somebody's real supervisor.
    env = {k: v for k, v in os.environ.items() if not k.startswith("DESKYARD_")}
    env.update(DESKYARD_DATA_DIR=str(state / "data"),
               DESKYARD_LISTEN=f"127.0.0.1:{port}", DESKYARD_IMAGE=args.image,
               DESKYARD_SUBNET=f"10.239.{secrets.randbelow(254) + 1}.0/24",
               DESKYARD_ADMIN_USER="admin", DESKYARD_ADMIN_PASSWORD=password)
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def api(path, body=None, method=None):
        req = urllib.request.Request(base + path, method=method,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers={"Origin": base, "Content-Type": "application/json"})
        with opener.open(req, timeout=15) as response:
            data = response.read()
            return json.loads(data) if data else None

    processes = []
    log = (state / "demo.log").open("w")
    created = False
    access = None
    interrupted = False
    # Record ownership before creation so an interrupted startup is recoverable.
    (state / "owner.json").write_text(json.dumps({"desktop": desktop, "pid": os.getpid()}))

    def stop(_signum, _frame):
        nonlocal interrupted
        interrupted = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        supervisor = subprocess.Popen([str(BIN / "deskyard-supervisor")], env=env, stdout=log, stderr=log, start_new_session=True)
        processes.append(supervisor)
        print(f"Starting demo supervisor; logs: {state / 'demo.log'}", flush=True)
        def supervisor_ready():
            with urllib.request.urlopen(base + "/healthz", timeout=2) as response:
                return response.status == 204
        wait_for(supervisor_ready)
        api("/api/login", {"username": "admin", "password": password})
        job = api("/api/desktops", {"name": desktop, "image": args.image})["job"]
        created = True
        (state / "owner.json").write_text(json.dumps({"desktop": desktop, "pid": os.getpid(), "job": job}))
        # Jobs are server-sent events; wait for a terminal status.
        with opener.open(base + "/api/jobs/" + job, timeout=180) as response:
            for line in response:
                if line.startswith(b"data: "):
                    update = json.loads(line[6:])
                    if update["status"] != "running":
                        if update["status"] != "done":
                            raise RuntimeError(f"Desktop creation failed: {update}")
                        break
        if interrupted:
            raise KeyboardInterrupt
        token = api(f"/api/desktops/{desktop}/tokens", {"name": "terminal prototype", "expires": None})["token"]
        cli_env = {**env, "DESKYARD_URL": base + "/" + desktop, "DESKYARD_TOKEN": token}

        def remote(*argv):
            result = subprocess.run([str(BIN / "deskyard"), "exec", "--", *argv],
                                    env=cli_env, capture_output=True, text=True, timeout=30)
            if result.returncode:
                raise RuntimeError(result.stderr)
            return result.stdout

        remote("shared-terminal", "--background", "main")
        output = remote("zellij", "web", "--create-token")
        match = re.search(r"^\S+: ([0-9a-f-]{36})$", output, re.MULTILINE)
        if not match:
            raise RuntimeError("Unexpected Zellij token output (not logged to protect the token)")
        forward = subprocess.Popen([str(BIN / "deskyard"), "forward", "-R", f"8082:{web_port}"],
                                   env=cli_env, stdout=log, stderr=log, start_new_session=True)
        processes.append(forward)

        def ready():
            with urllib.request.urlopen(terminal + "/info/version", timeout=2) as response:
                return response.read().decode() == "0.45.1"

        wait_for(ready)
        access = {"desktop": desktop, "supervisor": base, "terminal": terminal + "/main",
                  "username": "admin", "password": password, "zellij_token": match[1],
                  "cli": str(BIN / "deskyard"), "cli_env": {"DESKYARD_URL": base + "/" + desktop, "DESKYARD_TOKEN": token},
                  "state_dir": str(state), "pid": os.getpid()}
        access_path = state / "access.json"
        fd = os.open(access_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as file:
            json.dump(access, file, indent=2)
        attach = state / "attach"
        attach.write_text("#!/bin/sh\n" + "\n".join(f"export {key}={shlex.quote(value)}" for key, value in access["cli_env"].items())
                          + "\nexec " + shlex.quote(access["cli"]) + " exec --tty -- shared-terminal main\n")
        attach.chmod(0o700)
        print(f"Terminal: {terminal}/main\nDesktop: {base}/{desktop}/\n"
              f"Private login details: {access_path}\nCLI attach: {attach}\n"
              "Ctrl-C stops the demo and removes its desktop. Workspace files remain in the private state directory.", flush=True)
        while True:
            if interrupted:
                raise KeyboardInterrupt
            if any(p.poll() is not None for p in processes):
                raise RuntimeError(f"A demo process exited; inspect {state / 'demo.log'}")
            time.sleep(1)
    except KeyboardInterrupt:
        print("Stopping demo…", flush=True)
    finally:
        # Stop forwarding first; keep the supervisor up long enough to remove its desktop.
        for proc in reversed(processes[1:]):
            proc.terminate()
            proc.wait(timeout=10)
        if created:
            deadline = time.monotonic() + 30
            while True:
                try:
                    api(f"/api/desktops/{desktop}", method="DELETE")
                    break
                except urllib.error.HTTPError as error:
                    if error.code == 404:
                        break
                    if error.code == 409 and time.monotonic() < deadline:
                        time.sleep(0.25)
                        continue
                    print(f"Cleanup failed for {desktop}: {error}; ownership: {state / 'owner.json'}", flush=True)
                    break
                except (OSError, urllib.error.URLError) as error:
                    print(f"Cleanup failed for {desktop}: {error}; ownership: {state / 'owner.json'}", flush=True)
                    break
        for proc in processes[:1]:
            proc.terminate()
            proc.wait(timeout=10)
        if access:
            (state / "access.json").unlink(missing_ok=True)
            (state / "attach").unlink(missing_ok=True)
        log.close()


if __name__ == "__main__":
    main()
