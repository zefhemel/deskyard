"""Real Docker terminal attachment lifetime and immediate-input regressions."""
import json
import subprocess
import threading
import time
import uuid

import pytest
from contextlib import contextmanager

from websockets.sync.client import connect
from conftest import docker


def shell(name, command):
    return docker("exec", "-u", "user", "deskyard-" + name, "bash", "-lc", command).stdout.strip()


def eventually(read, expected, seconds=10):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        actual = read()
        if actual == expected:
            return
        time.sleep(.05)
    raise AssertionError((actual, expected))


def client_count(name):
    return shell(name, "tmux -S /run/workspace/deskyard-terminal/tmux.sock list-clients | wc -l")


@contextmanager
def attachment(admin, name):
    cookie = "; ".join(f"{key}={value}" for key, value in admin.cookies.items())
    with connect(admin.base.replace("http:", "ws:") + f"/api/computers/{name}/terminal?session=main&create=1",
                 origin=admin.base, additional_headers={"Cookie": cookie}) as ws:
        ws.send(json.dumps({"cols": 80, "rows": 24}))
        ready = json.loads(ws.recv(timeout=10))
        assert ready.get("ready"), ready
        # Consume actual output and return renderer credit while the test drives
        # the shell through independently checked files, never echoed input.
        def drain():
            try:
                for data in ws:
                    if isinstance(data, bytes):
                        ws.send(json.dumps({"ack": len(data)}))
            except Exception:
                pass  # closing the attachment interrupts this reader
        reader = threading.Thread(target=drain, daemon=True)
        reader.start()
        try:
            yield ws
        finally:
            ws.close()
            reader.join(5)
            assert not reader.is_alive(), "terminal reader did not stop"


def test_browser_terminal_lease_lifetime(admin, desktop, name):
    nonce = uuid.uuid4().hex
    with attachment(admin, name) as first:
        # This is deliberately the first action after ready: no prompt, redraw,
        # sleep or other heuristic is allowed before sending initial input.
        first.send(f'export NONCE={nonce}; printf "%s" "$$" > /workspace/lease-pid\r'.encode())
        eventually(lambda: shell(name, 'test -s /workspace/lease-pid && printf ready || true'), "ready")
        pid = shell(name, 'cat /workspace/lease-pid')
        with attachment(admin, name) as second:
            second.send(b'printf "%s" "$NONCE" > /workspace/lease-shared; (sleep 1; printf survived > /workspace/lease-job) &\r')
            eventually(lambda: shell(name, 'cat /workspace/lease-shared 2>/dev/null || true'), nonce)
            eventually(lambda: client_count(name), "2")
            first.close()
            eventually(lambda: client_count(name), "1")
            second.send(b'printf alive > /workspace/lease-other\r')
            eventually(lambda: shell(name, 'cat /workspace/lease-other 2>/dev/null || true'), "alive")
    eventually(lambda: client_count(name), "0")
    eventually(lambda: shell(name, 'cat /workspace/lease-job 2>/dev/null || true'), "survived")
    assert shell(name, 'tmux -S /run/workspace/deskyard-terminal/tmux.sock list-sessions -F "#{session_name}"') == "main"
    assert shell(name, 'tmux -S /run/workspace/deskyard-terminal/tmux.sock display-message -p -t =main: "#{pane_pid}"') == pid
    # Repeat immediate input on fresh attaches to catch raw-mode startup flushing.
    for i in range(8):
        with attachment(admin, name) as ws:
            ws.send(f'printf %s {i} > /workspace/lease-immediate\r'.encode())
            eventually(lambda: shell(name, 'cat /workspace/lease-immediate 2>/dev/null || true'), str(i))
        eventually(lambda: client_count(name), "0")

    # Close during preparation, before a ready frame can be observed.
    cookie = "; ".join(f"{key}={value}" for key, value in admin.cookies.items())
    for _ in range(8):
        with connect(admin.base.replace("http:", "ws:") + f"/api/computers/{name}/terminal?session=main",
                     origin=admin.base, additional_headers={"Cookie": cookie}) as ws:
            ws.send(json.dumps({"cols":80,"rows":24}))
    eventually(lambda: client_count(name), "0")
    eventually(lambda: shell(name, 'tmux -S /run/workspace/deskyard-terminal/tmux.sock list-sessions -F "#{session_name}"'), "main")


def test_remote_terminal_disconnect_and_eof(cli, remote, name):
    for mode in ("disconnect", "eof"):
        process = subprocess.Popen([cli, "-e", name, "terminal"], env=remote.env,
                                   stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        try:
            process.stdin.write(f'printf {mode} > /workspace/lease-cli\r'.encode())
            process.stdin.flush()
            eventually(lambda: shell(name, 'cat /workspace/lease-cli 2>/dev/null || true'), mode)
            if mode == "disconnect":
                process.terminate()
            else:
                process.stdin.close()
            process.wait(10)
            eventually(lambda: client_count(name), "0")
            assert shell(name, 'tmux -S /run/workspace/deskyard-terminal/tmux.sock list-sessions -F "#{session_name}"') == "main"
            if mode == "eof":
                assert process.returncode == 0, process.stderr.read().decode()
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(5)
            process.stderr.close()


@pytest.mark.serial
def test_terminal_lease_survives_supervisor_crash(supervisor, admin, desktop, name):
    if supervisor.pool:
        supervisor.pool.settle()
    try:
        with attachment(admin,name) as ws:
            ws.send(b'printf "%s" "$$" > /workspace/crash-pid\r')
            eventually(lambda:shell(name,'test -s /workspace/crash-pid && printf ready || true'),'ready')
            pid=shell(name,'cat /workspace/crash-pid')
            supervisor.kill()
            eventually(lambda:client_count(name),'0')
            assert shell(name,'tmux -S /run/workspace/deskyard-terminal/tmux.sock list-sessions -F "#{session_name}"')=='main'
            assert shell(name,'tmux -S /run/workspace/deskyard-terminal/tmux.sock display-message -p -t =main: "#{pane_pid}"')==pid
    finally:
        if not supervisor.running():
            supervisor.start()
