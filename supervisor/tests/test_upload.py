"""Copying files onto a desktop: POST /api/computers/{name}/upload."""

import asyncio
import os
import socket
import time
import uuid
from pathlib import Path
from urllib.parse import quote, urlparse

import requests

from helpers import bearer, exec_ws, make_credential


def up(client, name, fname, data, **kw):
    return client.post(f"/api/computers/{name}/upload", params={"name": fname}, data=data, **kw)


def desk(supervisor, desktop) -> Path:
    """The desktop's ~/Desktop: the API names its folder as the host path."""
    return Path(desktop["folder"]) / "Desktop"


def test_two_files_land_owned_by_user_and_a_clash_gets_a_number(supervisor, admin, desktop, name):
    a, b = os.urandom(5 * 1024 * 1024 + 3), b"second file\n"
    r1, r2 = up(admin, name, "one.bin", a), up(admin, name, "two.txt", b)
    assert r1.status_code == 200 and r2.status_code == 200, (r1.text, r2.text)
    assert r1.json() == {"path": "/workspace/Desktop/one.bin", "bytes": len(a)}
    assert (desk(supervisor, desktop) / "one.bin").read_bytes() == a
    assert (desk(supervisor, desktop) / "two.txt").read_bytes() == b
    r = up(admin, name, "two.txt", b"again")
    assert r.json()["path"] == "/workspace/Desktop/two (2).txt"
    assert up(admin, name, "two.txt", b"x").json()["path"] == "/workspace/Desktop/two (3).txt"
    assert (desk(supervisor, desktop) / "two (2).txt").read_bytes() == b"again"
    t = make_credential(admin, name)
    out, _, _ = asyncio.run(exec_ws(admin.base, name, t["credential"], ["sh", "-c", "stat -c '%u:%g' ~/Desktop/one.bin ~/Desktop/'two (2).txt'; ls -A ~/Desktop"]))
    lines = out.decode().split("\n")
    assert lines[:2] == ["1000:1000", "1000:1000"]
    assert not any(n.endswith(".part") for n in lines[2:])


def test_bad_names_are_refused(supervisor, admin, desktop, name):
    for n in ["", "a/b", "../x", "..", "a\x00b", "x" * 300]:
        assert up(admin, name, n, b"x").status_code == 400, repr(n)
    assert not desk(supervisor, desktop).exists() or list(desk(supervisor, desktop).iterdir()) == []


def raw_post(admin, name, fname, length):
    """An upload's request line and headers, declaring `length` bytes; the body is the caller's."""
    u = urlparse(admin.base)
    cookie = "; ".join(f"{k}={v}" for k, v in admin.cookies.items())
    s = socket.create_connection((u.hostname, u.port), timeout=10)
    s.sendall((f"POST /api/computers/{name}/upload?name={quote(fname)} HTTP/1.1\r\nHost: {u.netloc}\r\nOrigin: {admin.base}\r\n"
               f"Cookie: {cookie}\r\nContent-Length: {length}\r\nConnection: close\r\n\r\n").encode())
    return s


def test_upload_limits(supervisor, admin, desktop, name):
    # 2 GB + 1 byte: refused with a message before any byte is sent.
    s = raw_post(admin, name, "big.bin", 2 * 1024**3 + 1)
    reply = s.makefile("rb").read().decode(errors="replace")
    s.close()
    assert reply.startswith("HTTP/1.1 413"), reply
    assert "over the 2 GB limit" in reply, reply

    # An upload cut off halfway leaves nothing under its name, and no .part.
    assert up(admin, name, "seed.txt", b"s").status_code == 200
    s = raw_post(admin, name, "cut.bin", 100 * 1024 * 1024)
    s.sendall(os.urandom(2 * 1024 * 1024))
    time.sleep(1)
    s.close()
    left = lambda: sorted(p.name for p in desk(supervisor, desktop).iterdir())  # noqa: E731
    deadline = time.time() + 15
    while time.time() < deadline and left() != ["seed.txt"]:
        time.sleep(0.5)
    assert left() == ["seed.txt"]  # dot files too: no .part left behind


def test_access_and_credentials(admin, desktop, name, supervisor):
    user = "u" + uuid.uuid4().hex[:6]
    for who, ds in [(user, []), (user + "v", [name])]:
        grants = [{"computer": e, "level": "full"} for e in ds]
        assert admin.post("/api/accounts", json={"username": who, "password": "pw-" + who, "admin": False, "grants": grants}).status_code == 201
    # A client credential whose account has no grant of the desktop.
    t = make_credential(admin, name)
    assert admin.patch(f"/api/accounts/{t['username']}", json={"grants": []}).status_code == 200
    try:
        stranger = supervisor.client().login(user, "pw-" + user)
        viewer = supervisor.client().login(user + "v", "pw-" + user + "v")
        assert up(stranger, name, "n.txt", b"x").status_code == 404
        assert up(viewer, name, "v.txt", b"x").status_code == 200
        assert requests.post(f"{supervisor.base}/api/computers/{name}/upload?name=a", data=b"x", headers={"Origin": supervisor.base}, timeout=10).status_code == 401
        r = requests.post(f"{supervisor.base}/api/computers/{name}/upload?name=a", data=b"x",
                          headers={**bearer(t), "Origin": supervisor.base}, timeout=10)
        assert r.status_code == 404
        assert requests.post(f"{supervisor.base}/api/computers/{name}/upload?name=a", data=b"x", cookies=admin.cookies,
                             headers={"Origin": "https://evil.example"}, timeout=10).status_code == 403
        assert sorted(p.name for p in desk(supervisor, desktop).iterdir()) == ["v.txt"]
    finally:
        for who in (user, user + "v"):
            admin.delete(f"/api/accounts/{who}")


# (Dropping files on the desktop page: test_ui_pointer_and_drop.py.)
