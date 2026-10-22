"""Dropping files onto a desktop: POST /api/desktops/{name}/upload."""

import asyncio
import http.client
import os
import socket
import time
import uuid
from pathlib import Path
from urllib.parse import quote, urlparse

import requests

from test_agent import exec_ws, make_token


def up(client, name, fname, data, **kw):
    return client.post(f"/api/desktops/{name}/upload", params={"name": fname}, data=data, **kw)


def desk(desktop) -> Path:
    return Path(desktop["folder"]) / "Desktop"


def test_two_files_land_owned_by_user_and_a_clash_gets_a_number(admin, desktop, name):
    a, b = os.urandom(5 * 1024 * 1024 + 3), b"second file\n"
    r1, r2 = up(admin, name, "one.bin", a), up(admin, name, "two.txt", b)
    assert r1.status_code == 200 and r2.status_code == 200, (r1.text, r2.text)
    assert r1.json() == {"path": "/workspace/Desktop/one.bin", "bytes": len(a)}
    assert (desk(desktop) / "one.bin").read_bytes() == a
    assert (desk(desktop) / "two.txt").read_bytes() == b
    r = up(admin, name, "two.txt", b"again")
    assert r.json()["path"] == "/workspace/Desktop/two (2).txt"
    assert up(admin, name, "two.txt", b"x").json()["path"] == "/workspace/Desktop/two (3).txt"
    assert (desk(desktop) / "two (2).txt").read_bytes() == b"again"
    tok = make_token(admin, name)["token"]
    out, _, _ = asyncio.run(exec_ws(admin.base, tok, ["sh", "-c", "stat -c '%u:%g' ~/Desktop/one.bin ~/Desktop/'two (2).txt'; ls -A ~/Desktop"]))
    lines = out.decode().split("\n")
    assert lines[:2] == ["1000:1000", "1000:1000"]
    assert not any(n.endswith(".part") for n in lines[2:])


def test_bad_names_and_oversize_are_refused(admin, desktop, name):
    for n in ["", "a/b", "../x", "..", "a\x00b", "x" * 300]:
        assert up(admin, name, n, b"x").status_code == 400, repr(n)
    # Declared over 2 GB: refused before any byte is sent.
    u = urlparse(admin.base)
    conn = http.client.HTTPConnection(u.hostname, u.port, timeout=10)
    cookie = "; ".join(f"{k}={v}" for k, v in admin.cookies.items())
    conn.putrequest("POST", f"/api/desktops/{name}/upload?name=big")
    for k, v in {"Origin": admin.base, "Cookie": cookie, "Content-Length": str(3 * 1024**3)}.items():
        conn.putheader(k, v)
    conn.endheaders()
    assert conn.getresponse().status == 413
    conn.close()
    assert not desk(desktop).exists() or list(desk(desktop).iterdir()) == []


def test_aborted_upload_leaves_nothing(admin, desktop, name):
    assert up(admin, name, "seed.txt", b"s").status_code == 200
    u = urlparse(admin.base)
    cookie = "; ".join(f"{k}={v}" for k, v in admin.cookies.items())
    s = socket.create_connection((u.hostname, u.port), timeout=10)
    s.sendall((f"POST /api/desktops/{name}/upload?name=cut.bin HTTP/1.1\r\nHost: {u.netloc}\r\nOrigin: {admin.base}\r\n"
               f"Cookie: {cookie}\r\nContent-Length: 104857600\r\n\r\n").encode())
    s.sendall(os.urandom(2 * 1024 * 1024))
    time.sleep(1)
    s.close()
    deadline = time.time() + 15
    while time.time() < deadline and sorted(p.name for p in desk(desktop).iterdir()) != ["seed.txt"]:
        time.sleep(0.5)
    assert sorted(p.name for p in desk(desktop).iterdir()) == ["seed.txt"]


def test_access_and_tokens(admin, desktop, name, supervisor):
    user = "u" + uuid.uuid4().hex[:6]
    for who, ds in [(user, []), (user + "v", [name])]:
        assert admin.post("/api/accounts", json={"username": who, "password": "pw-" + who, "admin": False, "desktops": ds}).status_code == 201
    try:
        stranger = supervisor.client().login(user, "pw-" + user)
        viewer = supervisor.client().login(user + "v", "pw-" + user + "v")
        assert up(stranger, name, "n.txt", b"x").status_code == 404
        assert up(viewer, name, "v.txt", b"x").status_code == 200
        assert requests.post(f"{supervisor.base}/api/desktops/{name}/upload?name=a", data=b"x", headers={"Origin": supervisor.base}, timeout=10).status_code == 401
        tok = make_token(admin, name)["token"]
        r = requests.post(f"{supervisor.base}/api/desktops/{name}/upload?name=a", data=b"x",
                          headers={"Authorization": f"Bearer {tok}", "Origin": supervisor.base}, timeout=10)
        assert r.status_code == 401
        assert requests.post(f"{supervisor.base}/api/desktops/{name}/upload?name=a", data=b"x", cookies=admin.cookies,
                             headers={"Origin": "https://evil.example"}, timeout=10).status_code == 403
        assert sorted(p.name for p in desk(desktop).iterdir()) == ["v.txt"]
    finally:
        for who in (user, user + "v"):
            admin.delete(f"/api/accounts/{who}")


def test_drop_a_file_onto_the_desktop_page(supervisor, desktop, name, tmp_path):
    from playwright.sync_api import expect, sync_playwright

    from test_ui import signed_in

    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        expect(page.get_by_role("button", name="More")).to_be_visible(timeout=15000)

        def drop(files):
            page.evaluate(
                """(files) => {
                  const dt = new DataTransfer();
                  for (const f of files) dt.items.add(new File([f.content], f.name, {type: 'text/plain'}));
                  window.dispatchEvent(new DragEvent('dragenter', {dataTransfer: dt, bubbles: true}));
                }""", files)
            expect(page.get_by_text(f"Drop to copy into {name}")).to_be_visible()
            page.evaluate(
                """(files) => {
                  const dt = new DataTransfer();
                  for (const f of files) dt.items.add(new File([f.content], f.name, {type: 'text/plain'}));
                  document.querySelector('[data-testid=drop-overlay]').dispatchEvent(new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true}));
                }""", files)

        drop([{"name": "dropped.txt", "content": "dropped content\n"}, {"name": "second.txt", "content": "2\n"}])
        expect(page.get_by_text("Copied dropped.txt")).to_be_visible(timeout=20000)
        expect(page.get_by_text("Copied second.txt")).to_be_visible(timeout=20000)
        assert (desk(desktop) / "dropped.txt").read_text() == "dropped content\n"
        page.screenshot(path=str(tmp_path / "dropped.png"))
        browser.close()
