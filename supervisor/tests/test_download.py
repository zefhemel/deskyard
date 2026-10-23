"""Browsing and downloading a desktop's files (limited to /workspace)."""

import io
import os
import uuid
import zipfile

import requests

from conftest import docker
from helpers import make_token, put, user_sh, wait_until


def test_browse_and_download(admin, desktop, name):
    blob = os.urandom(3 * 1024 * 1024 + 17)
    put(name, "docs/report.txt", b"quarterly numbers\n")
    put(name, "docs/deep/notes.md", b"# notes\n")
    put(name, "big.bin", blob)
    user_sh(name, "ln -s /etc /workspace/etc-link")

    r = admin.get(f"/api/desktops/{name}/files")
    assert r.status_code == 200, r.text
    listing = r.json()
    assert listing["path"] == "/workspace"
    by_name = {e["name"]: e for e in listing["entries"]}
    assert by_name["docs"]["dir"] is True
    assert by_name["big.bin"]["dir"] is False and by_name["big.bin"]["size"] == len(blob)
    mods = [e["modified"] for e in listing["entries"]]
    assert mods == sorted(mods, reverse=True)

    r = admin.get(f"/api/desktops/{name}/files", params={"path": "/workspace/docs"})
    assert {e["name"] for e in r.json()["entries"]} == {"report.txt", "deep"}

    r = admin.get(f"/api/desktops/{name}/download", params={"path": "/workspace/big.bin"})
    assert r.status_code == 200
    assert r.content == blob
    assert 'filename=big.bin' in r.headers["Content-Disposition"]

    r = admin.get(f"/api/desktops/{name}/download", params={"path": "docs"})
    assert r.status_code == 200 and r.headers["Content-Type"] == "application/zip"
    z = zipfile.ZipFile(io.BytesIO(r.content))
    assert z.read("report.txt") == b"quarterly numbers\n"
    assert z.read("deep/notes.md") == b"# notes\n"

    # The whole workspace as a zip must not follow the link to /etc.
    r = admin.get(f"/api/desktops/{name}/download", params={"path": "/workspace"})
    assert r.status_code == 200
    names = zipfile.ZipFile(io.BytesIO(r.content)).namelist()
    assert "docs/report.txt" in names and not any(n.startswith("etc-link") for n in names)


def test_paths_outside_workspace_refused(admin, desktop, name):
    user_sh(name, "ln -s /etc /workspace/etc-link")
    for p in ["..", "/", "/etc/passwd", "/workspace/../etc/passwd", "../etc/passwd", "etc-link", "etc-link/passwd"]:
        for route in ["files", "download"]:
            r = admin.get(f"/api/desktops/{name}/{route}", params={"path": p})
            assert r.status_code == 403, (route, p, r.status_code, r.text[:100])
    assert admin.get(f"/api/desktops/{name}/download", params={"path": "nope"}).status_code == 404


def test_access_and_tokens(admin, desktop, name, supervisor):
    put(name, "f.txt", b"hi")
    user = "u" + uuid.uuid4().hex[:6]
    for who, desktops_ in [(user, []), (user + "v", [name])]:
        r = admin.post("/api/accounts", json={"username": who, "password": "pw-" + who, "admin": False, "desktops": desktops_})
        assert r.status_code == 201, r.text
    try:
        stranger = supervisor.client().login(user, "pw-" + user)
        viewer = supervisor.client().login(user + "v", "pw-" + user + "v")
        for route in [f"/api/desktops/{name}/files", f"/api/desktops/{name}/download?path=f.txt"]:
            assert stranger.get(route).status_code == 404
            assert viewer.get(route).status_code == 200
            assert requests.get(supervisor.base + route, timeout=10).status_code == 401
            tok = make_token(admin, name)["token"]
            r = requests.get(supervisor.base + route, headers={"Authorization": f"Bearer {tok}"}, timeout=10)
            assert r.status_code == 401
    finally:
        for who in (user, user + "v"):
            admin.delete(f"/api/accounts/{who}")


def test_download_dialog(supervisor, desktop, name, tmp_path):
    from playwright.sync_api import expect, sync_playwright

    from helpers import signed_in

    put(name, "docs/hello.txt", b"hello from the desktop\n")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, accept_downloads=True)
        page.goto(f"{supervisor.base}/{name}/")
        page.get_by_role("button", name="Download", exact=True).click()
        dialog = page.get_by_role("dialog")
        expect(dialog.get_by_role("button", name="docs")).to_be_visible(timeout=15000)
        dialog.get_by_role("button", name="docs").click()
        expect(dialog.get_by_text("hello.txt")).to_be_visible()
        assert dialog.get_by_label("Folder path").input_value() == "/workspace/docs"
        with page.expect_download() as d:
            dialog.get_by_role("link", name="Download hello.txt").click()
        path = tmp_path / "got.txt"
        d.value.save_as(path)
        assert d.value.suggested_filename == "hello.txt"
        assert path.read_bytes() == b"hello from the desktop\n"
        # A path outside /workspace shows the refusal.
        dialog.get_by_label("Folder path").fill("/etc")
        dialog.get_by_role("button", name="Go").click()
        expect(dialog.get_by_role("alert")).to_contain_text("/workspace")
        page.screenshot(path=str(tmp_path / "download.png"))
        browser.close()


def test_folder_zip_with_hard_links_and_unreadable_files(admin, desktop, name):
    put(name, "mix/one.txt", b"one")
    put(name, "mix/locked.txt", b"secret")
    user_sh(name, "cd /workspace/mix && ln one.txt one-link.txt && chmod 000 locked.txt")
    r = admin.get(f"/api/desktops/{name}/download", params={"path": "mix"})
    assert r.status_code == 200
    z = zipfile.ZipFile(io.BytesIO(r.content))
    assert z.read("one.txt") == b"one" and z.read("one-link.txt") == b"one"
    assert "locked.txt" not in z.namelist()


def test_client_abort_leaves_no_tar_or_cat_running(admin, desktop, name):
    user_sh(name, "mkdir -p /workspace/huge && for i in 0 1 2 3; do head -c 64M /dev/urandom > /workspace/huge/f$i.bin; done")
    container = docker("ps", "-q", "--filter", f"label=deskyard.name={name}").stdout.split()[0]

    def running():
        out = docker("exec", container, "ps", "-u", "user", "-o", "comm=").stdout.split()
        return [c for c in out if c in ("tar", "cat")]

    for path in ["huge", "huge/f0.bin"]:
        r = admin.get(f"/api/desktops/{name}/download", params={"path": path}, stream=True)
        assert r.status_code == 200
        next(r.iter_content(65536))
        assert running()  # still streaming
        r.close()
        assert wait_until(lambda: not running(), 10, 0.5), f"{path}: {running()} left running after the client left"


LUMINANCE_JS = """(rgb) => {
  const c = rgb.match(/[\\d.]+/g).slice(0, 3).map(Number).map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}"""


def test_download_dialog_names_are_readable_and_hidden_files_toggle(supervisor, desktop, name):
    from playwright.sync_api import expect, sync_playwright

    from helpers import signed_in

    for rel in ["Documents/a.txt", "visible.txt", ".secret/x", ".hidden.txt"]:
        put(name, rel, b"x")
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.evaluate("localStorage.removeItem('deskyard.showHidden')")
        page.goto(f"{supervisor.base}/{name}/")
        page.get_by_role("button", name="Download", exact=True).click()
        dialog = page.get_by_role("dialog")
        folder = dialog.get_by_role("button", name="Documents")
        expect(folder).to_be_visible(timeout=15000)

        def contrast(locator):
            fg = locator.evaluate("e => getComputedStyle(e).color")
            bg = dialog.evaluate("e => getComputedStyle(e).backgroundColor")
            assert fg != bg
            lf, lb = page.evaluate(LUMINANCE_JS, fg), page.evaluate(LUMINANCE_JS, bg)
            return (max(lf, lb) + 0.05) / (min(lf, lb) + 0.05)

        assert contrast(folder) >= 4.5
        assert contrast(dialog.get_by_text("visible.txt", exact=True)) >= 4.5
        folder.hover()
        assert contrast(folder) >= 4.5
        folder.focus()
        assert contrast(folder) >= 4.5

        # Hidden entries are off by default, listed after the visible ones when on, and remembered.
        toggle = dialog.get_by_label("Show hidden files", exact=False)
        assert not toggle.is_checked()
        expect(dialog.get_by_text(".hidden.txt", exact=True)).to_have_count(0)
        toggle.check()
        names = [t.strip() for t in dialog.locator("ul li").all_inner_texts()]
        order = [n for n in names if any(k in n for k in ("Documents", "visible.txt", ".secret", ".hidden.txt"))]
        flat = " ".join(order)
        assert flat.index("Documents") < flat.index("visible.txt") < flat.index(".secret") < flat.index(".hidden.txt"), order
        assert contrast(dialog.get_by_text(".hidden.txt", exact=True)) >= 4.5
        assert page.evaluate("localStorage.getItem('deskyard.showHidden')") == "1"
        page.reload()
        page.get_by_role("button", name="Download", exact=True).click()
        expect(page.get_by_role("dialog").get_by_label("Show hidden files", exact=False)).to_be_checked(timeout=15000)
        browser.close()
