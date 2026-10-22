"""Dropping files onto a desktop: POST /api/desktops/{name}/upload."""

import asyncio
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


def test_bad_names_are_refused(admin, desktop, name):
    for n in ["", "a/b", "../x", "..", "a\x00b", "x" * 300]:
        assert up(admin, name, n, b"x").status_code == 400, repr(n)
    assert not desk(desktop).exists() or list(desk(desktop).iterdir()) == []


def raw_post(admin, name, fname, length):
    """An upload's request line and headers, declaring `length` bytes; the body is the caller's."""
    u = urlparse(admin.base)
    cookie = "; ".join(f"{k}={v}" for k, v in admin.cookies.items())
    s = socket.create_connection((u.hostname, u.port), timeout=10)
    s.sendall((f"POST /api/desktops/{name}/upload?name={quote(fname)} HTTP/1.1\r\nHost: {u.netloc}\r\nOrigin: {admin.base}\r\n"
               f"Cookie: {cookie}\r\nContent-Length: {length}\r\nConnection: close\r\n\r\n").encode())
    return s


def test_upload_limits(admin, desktop, name):
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
    left = lambda: sorted(p.name for p in desk(desktop).iterdir())  # noqa: E731
    deadline = time.time() + 15
    while time.time() < deadline and left() != ["seed.txt"]:
        time.sleep(0.5)
    assert left() == ["seed.txt"]  # dot files too: no .part left behind


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


DRAG_JS = """([type, files]) => {
  const dt = new DataTransfer();
  for (const f of files) dt.items.add(new File([f.content], f.name, {type: 'text/plain'}));
  const target = type === 'drop' && document.querySelector('[data-testid=drop-overlay]') || document;
  const ev = new DragEvent(type, {dataTransfer: dt, bubbles: true, cancelable: true});
  target.dispatchEvent(ev);
  return ev.defaultPrevented;
}"""


def open_desktop_frame(p, supervisor, name):
    from playwright.sync_api import expect

    from test_ui import signed_in

    browser, page = signed_in(p, supervisor.base)
    page.goto(f"{supervisor.base}/{name}/")
    expect(page.get_by_role("button", name="More")).to_be_visible(timeout=15000)
    frame = page.query_selector("iframe").content_frame()
    frame.wait_for_load_state("load")
    return browser, page, frame


def test_drag_inside_the_frame_shows_overlay_and_drop_uploads(supervisor, desktop, name):
    from playwright.sync_api import expect, sync_playwright

    files = [{"name": "from-frame.txt", "content": "via the iframe\n"}]
    with sync_playwright() as p:
        browser, page, frame = open_desktop_frame(p, supervisor, name)
        overlay = page.get_by_text(f"Drop to copy into {name}")
        # The events fire in the frame's own document, as a real drag over the screen does.
        assert frame.evaluate(DRAG_JS, ["dragenter", files]) is True  # default prevented: the frame won't navigate
        frame.evaluate(DRAG_JS, ["dragover", files])
        expect(overlay).to_be_visible()
        page.evaluate(DRAG_JS, ["drop", files])  # the overlay now covers the frame and takes the drop
        expect(page.get_by_text("Copied from-frame.txt")).to_be_visible(timeout=20000)
        expect(overlay).to_be_hidden()
        assert (desk(desktop) / "from-frame.txt").read_text() == "via the iframe\n"

        # The frame is reloaded (reconnect): listeners come back.
        page.get_by_role("button", name="Reconnect").click() if page.get_by_role("button", name="Reconnect").count() else frame.evaluate("location.reload()")
        frame = page.query_selector("iframe").content_frame()
        frame.wait_for_load_state("load")
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        # A drop that lands in the frame itself (before the overlay) uploads too.
        frame.evaluate(DRAG_JS, ["drop", [{"name": "direct.txt", "content": "d"}]])
        expect(page.get_by_text("Copied direct.txt")).to_be_visible(timeout=20000)
        browser.close()


def test_drag_that_leaves_hides_the_overlay(supervisor, desktop, name):
    from playwright.sync_api import expect, sync_playwright

    files = [{"name": "never.txt", "content": "x"}]
    with sync_playwright() as p:
        browser, page, frame = open_desktop_frame(p, supervisor, name)
        overlay = page.get_by_text(f"Drop to copy into {name}")
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        expect(overlay).to_be_hidden(timeout=5000)  # no more dragover: the drag is over
        # dragend and a click also clear it.
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        page.evaluate("window.dispatchEvent(new DragEvent('dragend'))")
        expect(overlay).to_be_hidden()
        frame.evaluate(DRAG_JS, ["dragenter", files])
        expect(overlay).to_be_visible()
        page.mouse.move(300, 300)
        expect(overlay).to_be_hidden()
        assert not (desk(desktop).exists() and any(desk(desktop).iterdir()))
        browser.close()


def test_drop_on_the_top_bar_is_not_navigated_away(supervisor, desktop, name):
    from playwright.sync_api import sync_playwright

    with sync_playwright() as p:
        browser, page, _ = open_desktop_frame(p, supervisor, name)
        prevented = page.evaluate(
            """() => { const dt = new DataTransfer(); dt.items.add(new File(['x'], 'x.txt'));
               const ev = new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true});
               document.querySelector('header, div').dispatchEvent(ev); return ev.defaultPrevented; }""")
        assert prevented
        browser.close()
