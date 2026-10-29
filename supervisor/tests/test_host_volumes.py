"""Host volumes survive lifecycle operations and expose only chosen access."""

from playwright.sync_api import expect, sync_playwright

from conftest import docker
from helpers import in_desktop, signed_in


def source_folder(tmp_path):
    source = tmp_path.resolve() / "host files"
    source.mkdir(mode=0o777)
    source.chmod(0o777)
    (source / "kept.txt").write_text("host data\n")
    (source / "kept.txt").chmod(0o666)
    return source


def wait_settings(admin, name, response):
    assert response.status_code == 202, response.text
    state = admin.wait_job(response.json()["job"])
    assert state["status"] == "done", state
    return admin.desktop(name)


def host_volume(admin, volume, source):
    created = admin.post("/api/volumes", json={"name": volume, "hostPath": str(source)})
    assert created.status_code == 201, created.text
    assert created.json()["volume"]["kind"] == "host"
    return created.json()["volume"]


def test_host_volume_ui(supervisor, admin, desktop, name, tmp_path):
    source = source_folder(tmp_path)
    volume = f"{name}-ext"
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width": 390, "height": 844})
        page.goto(f"{supervisor.base}/volumes")
        page.get_by_role("button", name="Create volume").click()
        page.get_by_label("Name", exact=True).fill(volume)
        page.get_by_role("radio", name="Host folder").click()
        page.get_by_label("Path", exact=True).fill(str(source))
        page.get_by_role("button", name="Create volume").last.click()
        expect(page.get_by_role("heading", name=volume)).to_be_visible()
        page.goto(f"{supervisor.base}/environments/{name}?tab=settings&section=volumes")
        page.get_by_role("button", name="Add volume").click()
        page.get_by_label("Volume", exact=True).select_option(volume)
        page.get_by_label("Path in the environment", exact=True).fill("/mnt/external")
        page.get_by_role("checkbox", name="Read only", exact=False).check()
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        with page.expect_response(lambda r: r.request.method == "PATCH" and r.url.endswith(f"/api/computers/{name}")) as sent:
            page.get_by_role("button", name="Save and restart", exact=True).click()
        assert sent.value.status == 202
        state = admin.wait_job(sent.value.json()["job"])
        assert state["status"] == "done", state
        assert admin.desktop(name)["settings"]["volumes"] == [{"name": volume, "path": "/mnt/external", "readOnly": True}]
        assert in_desktop(name, "cat", "/mnt/external/kept.txt") == "host data\n"
        assert "NoNewPrivs:\t1" in in_desktop(name, "cat", "/proc/self/status")
        # Flatpak's user-namespace sandbox must remain usable with privilege escalation disabled.
        assert in_desktop(name, "bwrap", "--unshare-user", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "true") == ""
        attempted = docker("exec", "-u", "user", f"deskyard-{name}", "sh", "-c", "echo no >> /mnt/external/kept.txt", check=False)
        assert attempted.returncode != 0 and "Read-only file system" in attempted.stderr
        browser.close()
    changed = wait_settings(admin, name, admin.patch(f"/api/computers/{name}", json={"volumes": []}))
    assert changed["settings"]["volumes"] == []
    assert docker("exec", "-u", "user", f"deskyard-{name}", "test", "-e", "/mnt/external/kept.txt", check=False).returncode != 0
    assert admin.delete(f"/api/volumes/{volume}").status_code == 204
    assert (source / "kept.txt").read_text() == "host data\n"


def test_host_volume_create_preserve_and_keep_host_files(admin, name, tmp_path):
    source = source_folder(tmp_path)
    volume = f"{name}-host"
    host_volume(admin, volume, source)
    grants = [{"name": volume, "path": "/mnt/host", "readOnly": False}]
    try:
        desktop = admin.create(name, volumes=grants)
        assert desktop["settings"]["volumes"] == grants
        assert in_desktop(name, "sh", "-c", "echo desktop-data > /mnt/host/new.txt") == ""
        assert (source / "new.txt").read_text() == "desktop-data\n"
        # Unrelated settings must carry the grant into the replacement container.
        changed = wait_settings(admin, name, admin.patch(f"/api/computers/{name}", json={"memoryBytes": 2 << 30}))
        assert changed["settings"]["volumes"] == grants and changed["created"] == desktop["created"]
        assert changed["revision"] == changed["appliedRevision"] == desktop["revision"] + 1
        assert in_desktop(name, "cat", "/mnt/host/new.txt") == "desktop-data\n"
        assert admin.post(f"/api/computers/{name}/stop").status_code == 204
        changed = wait_settings(admin, name, admin.patch(f"/api/computers/{name}", json={"volumes": [{**grants[0], "readOnly": True}]}))
        assert changed["state"] == "stopped"
        assert admin.post(f"/api/computers/{name}/start").status_code == 204
        attempted = docker("exec", "-u", "user", f"deskyard-{name}", "sh", "-c", "echo no >> /mnt/host/new.txt", check=False)
        assert attempted.returncode != 0 and "Read-only file system" in attempted.stderr
    finally:
        admin.delete(f"/api/computers/{name}?files=1")
        admin.delete(f"/api/volumes/{volume}?deleteFiles=true")  # refused for a host volume
        admin.delete(f"/api/volumes/{volume}")
    assert (source / "kept.txt").read_text() == "host data\n"
    assert (source / "new.txt").read_text() == "desktop-data\n"


def test_missing_host_folder_does_not_create_it_or_stop_desktop(admin, desktop, name, tmp_path):
    # A folder that never existed: OrbStack keeps serving a shared macOS folder
    # for a while after it is deleted, so a deleted one can't stand in for it.
    source = tmp_path.resolve() / "never-existed"
    created = admin.post("/api/volumes", json={"name": f"{name}-gone", "hostPath": str(source)})
    assert created.status_code == 400 and f"{name}-gone" in created.text, created.text
    assert not source.exists()
    assert admin.get("/api/volumes").json()["volumes"] == [] or all(v["name"] != f"{name}-gone" for v in admin.get("/api/volumes").json()["volumes"])
    current = admin.desktop(name)
    assert current["state"] == "running" and current["revision"] == current["appliedRevision"] == desktop["revision"]


def test_host_folders_are_refused(admin, desktop, name):
    result = admin.patch(f"/api/computers/{name}", json={"mounts": [{"source": "/tmp", "target": "/mnt/tmp", "readOnly": True}]})
    assert result.status_code == 400 and "host volume" in result.text
