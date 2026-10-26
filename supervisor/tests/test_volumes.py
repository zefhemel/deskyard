"""Named volumes: shared folders granted to environments at /share/<volume>."""

import uuid

from playwright.sync_api import expect, sync_playwright

from conftest import docker
from helpers import in_desktop, signed_in, wait_until


def make_volume(admin, adopted=False) -> str:
    vol = "v-" + uuid.uuid4().hex[:6]
    r = admin.post("/api/volumes", json={"name": vol})
    assert r.status_code == 201, r.text
    assert r.json()["adopted"] is adopted
    return vol


def drop_volume(admin, vol):
    """Removes a test's volume and its files, ungranted from whatever still has it."""
    for v in admin.get("/api/volumes").json()["volumes"]:
        if v["name"] != vol:
            continue
        for env in v["environments"]:
            r = admin.patch(f"/api/environments/{env['name']}", json={"volumes": []})
            if r.status_code == 202:
                admin.wait_job(r.json()["job"])
    admin.delete(f"/api/volumes/{vol}?deleteFiles=true")


def apply(admin, name, **patch) -> dict:
    r = admin.patch(f"/api/environments/{name}", json=patch)
    assert r.status_code == 202, r.text
    state = admin.wait_job(r.json()["job"])
    assert state["status"] == "done", state
    return admin.desktop(name)


def container_id(name) -> str:
    return docker("inspect", "-f", "{{.Id}}", f"deskyard-{name}").stdout.strip()


def user_write(name, path):
    """Appends to path as the desktop's user: the finished process."""
    return docker("exec", "-u", "user", f"deskyard-{name}", "sh", "-c", f"echo more >> {path}", check=False)


def test_shared_volume_modes(admin, desktop, name):
    vol = make_volume(admin)
    a, b = "t" + uuid.uuid4().hex[:6], "t" + uuid.uuid4().hex[:6]
    try:
        admin.create(a, volumes=[{"name": vol, "readOnly": False}])
        admin.create(b, volumes=[{"name": vol, "readOnly": True}])
        assert admin.desktop(b)["settings"]["volumes"] == [{"name": vol, "readOnly": True}]
        listed = next(v for v in admin.get("/api/volumes").json()["volumes"] if v["name"] == vol)
        assert listed["environments"] == [{"name": x, "readOnly": x == b} for x in sorted([a, b])]
        assert in_desktop(a, "sh", "-c", f"echo from-a > /share/{vol}/a.txt") == ""
        assert in_desktop(b, "cat", f"/share/{vol}/a.txt") == "from-a\n"
        attempted = user_write(b, f"/share/{vol}/a.txt")
        assert attempted.returncode != 0 and "Read-only file system" in attempted.stderr
        assert "NoNewPrivs:\t1" in in_desktop(b, "cat", "/proc/1/status")
        # The test's own desktop has no grant, so no /share/<volume>.
        assert docker("exec", f"deskyard-{name}", "test", "-e", f"/share/{vol}", check=False).returncode != 0
    finally:
        for env in (a, b):
            admin.delete(f"/api/environments/{env}?files=1")
        drop_volume(admin, vol)


def test_grant_change_recreates(admin, desktop, name):
    vol = make_volume(admin)
    try:
        before = container_id(name)
        granted = apply(admin, name, volumes=[{"name": vol, "readOnly": False}])
        assert granted["settings"]["volumes"] == [{"name": vol, "readOnly": False}]
        rw = container_id(name)
        assert rw != before
        assert in_desktop(name, "sh", "-c", f"echo rw > /share/{vol}/f.txt") == ""
        flipped = apply(admin, name, volumes=[{"name": vol, "readOnly": True}])
        assert flipped["revision"] == flipped["appliedRevision"] == granted["revision"] + 1
        assert container_id(name) != rw
        assert in_desktop(name, "cat", f"/share/{vol}/f.txt") == "rw\n"
        attempted = user_write(name, f"/share/{vol}/f.txt")
        assert attempted.returncode != 0 and "Read-only file system" in attempted.stderr
    finally:
        drop_volume(admin, vol)


def test_volume_survives_lost_container_and_restart(supervisor, admin, desktop, name):
    vol = make_volume(admin)
    try:
        before = apply(admin, name, volumes=[{"name": vol, "readOnly": False}])
        assert in_desktop(name, "sh", "-c", f"echo kept > /share/{vol}/kept.txt") == ""
        supervisor.stop()
        docker("rm", "-f", f"deskyard-{name}")
        supervisor.start()
        c = supervisor.admin()
        admin.cookies.update(c.cookies)
        d = wait_until(lambda: (d := c.desktop(name))["state"] == "running" and d, 120, 1)
        assert d, c.desktop(name)
        assert d["settings"]["volumes"] == [{"name": vol, "readOnly": False}]
        assert d["revision"] == d["appliedRevision"] == before["revision"]
        assert in_desktop(name, "cat", f"/share/{vol}/kept.txt") == "kept\n"
    finally:
        drop_volume(admin, vol)


def test_volume_deletion_rules(supervisor, admin, name):
    vol = make_volume(admin)
    folder = supervisor.data / "volume" / vol
    try:
        admin.create(name, volumes=[{"name": vol, "readOnly": False}])
        assert in_desktop(name, "sh", "-c", f"echo precious > /share/{vol}/data.txt") == ""
        r = admin.delete(f"/api/volumes/{vol}?deleteFiles=true")
        assert r.status_code == 409, r.text
        assert r.json()["environments"] == [{"id": name, "name": name}]
        assert name in r.json()["error"]
        apply(admin, name, volumes=[])
        assert admin.delete(f"/api/volumes/{vol}").status_code == 204
        assert vol not in [v["name"] for v in admin.get("/api/volumes").json()["volumes"]]
        assert (folder / "data.txt").read_text() == "precious\n"
        # The same name takes the kept folder back as it is.
        r = admin.post("/api/volumes", json={"name": vol})
        assert r.status_code == 201, r.text
        assert r.json()["adopted"] is True and r.json()["volume"]["folder"] == str(folder)
        apply(admin, name, volumes=[{"name": vol, "readOnly": False}])
        assert in_desktop(name, "cat", f"/share/{vol}/data.txt") == "precious\n"
        # Removing the environment with its files leaves the volume's alone.
        assert admin.delete(f"/api/environments/{name}?files=1").status_code == 204
        assert (folder / "data.txt").read_text() == "precious\n"
        assert (supervisor.data / "volume").is_dir()
    finally:
        admin.delete(f"/api/environments/{name}?files=1")
        drop_volume(admin, vol)
    assert not folder.exists()


def test_missing_volume_folder_blocks_start(supervisor, admin, name):
    vol = make_volume(admin)
    folder = supervisor.data / "volume" / vol
    aside = supervisor.data / "volume" / f"{vol}.aside"
    try:
        admin.create(name, volumes=[{"name": vol, "readOnly": True}])
        assert admin.post(f"/api/environments/{name}/stop").status_code == 204
        folder.rename(aside)
        r = admin.post(f"/api/environments/{name}/start")
        assert r.status_code >= 400 and str(folder) in r.text, r.text
        assert not folder.exists()
        d = admin.desktop(name)
        assert d["state"] != "running" and str(folder) in d.get("error", ""), d
        aside.rename(folder)
        r = admin.post(f"/api/environments/{name}/start")
        assert r.status_code == 204, r.text
        d = wait_until(lambda: (d := admin.desktop(name))["state"] == "running" and d, 60, 1)
        assert d and not d.get("error"), admin.desktop(name)
    finally:
        if aside.exists() and not folder.exists():
            aside.rename(folder)
        admin.delete(f"/api/environments/{name}?files=1")
        drop_volume(admin, vol)


def test_volumes_ui(supervisor, admin, desktop, name):
    vol = "v-" + uuid.uuid4().hex[:6]
    try:
        with sync_playwright() as p:
            browser, page = signed_in(p, supervisor.base, viewport={"width": 390, "height": 844})
            page.goto(f"{supervisor.base}/volumes")
            page.get_by_role("button", name="Create volume").first.click()
            dialog = page.get_by_role("dialog")
            dialog.get_by_label("Name", exact=True).fill(vol)
            with page.expect_response(lambda r: r.request.method == "POST" and r.url.endswith("/api/volumes")) as sent:
                dialog.get_by_role("button", name="Create volume").click()
            assert sent.value.status == 201
            expect(page.get_by_role("heading", name=vol, exact=True)).to_be_visible()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            page.goto(f"{supervisor.base}/environments/{name}?tab=settings&section=volumes")
            section = page.get_by_role("region", name="Volumes", exact=True)
            section.get_by_role("button", name="Add volume").click()
            section.get_by_label("Volume", exact=True).select_option(vol)
            expect(section.get_by_role("checkbox", name="Read only", exact=False)).not_to_be_checked()
            section.scroll_into_view_if_needed()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            with page.expect_response(lambda r: r.request.method == "PATCH" and r.url.endswith(f"/api/environments/{name}")) as sent:
                page.get_by_role("button", name="Save and restart", exact=True).click()
            assert sent.value.status == 202
            state = admin.wait_job(sent.value.json()["job"])
            assert state["status"] == "done", state
            assert admin.desktop(name)["settings"]["volumes"] == [{"name": vol, "readOnly": False}]
            assert in_desktop(name, "test", "-d", f"/share/{vol}") == ""
            browser.close()
    finally:
        drop_volume(admin, vol)
