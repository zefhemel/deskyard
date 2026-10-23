"""Host folders survive lifecycle operations and expose only chosen access."""

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


def test_host_folders_ui(supervisor, admin, desktop, name, tmp_path):
    source = source_folder(tmp_path)
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={"width": 390, "height": 844})
        page.goto(f"{supervisor.base}/desktops/{name}?tab=settings")
        page.get_by_role("button", name="Add host folder").click()
        page.get_by_label("Host folder", exact=True).fill(str(source))
        page.get_by_label("Desktop path", exact=True).fill("/mnt/external")
        expect(page.get_by_role("checkbox", name="Read only", exact=False)).to_be_checked()
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        page.get_by_role("region", name="Host folders", exact=True).scroll_into_view_if_needed()
        # Optional visual artifact outside the repository.
        import os
        if os.environ.get("HOST_MOUNT_SCREENSHOT"):
            page.screenshot(path=os.environ["HOST_MOUNT_SCREENSHOT"], full_page=True)
        with page.expect_response(lambda r: r.request.method == "PATCH" and r.url.endswith(f"/api/desktops/{name}")) as sent:
            page.get_by_role("button", name="Save and restart", exact=True).click()
        assert sent.value.status == 202
        state = admin.wait_job(sent.value.json()["job"])
        assert state["status"] == "done", state
        expected = [{"source": str(source), "target": "/mnt/external", "readOnly": True}]
        assert admin.desktop(name)["settings"]["mounts"] == expected
        assert in_desktop(name, "cat", "/mnt/external/kept.txt") == "host data\n"
        assert "NoNewPrivs:\t1" in in_desktop(name, "cat", "/proc/self/status")
        # Flatpak's user-namespace sandbox must remain usable with privilege escalation disabled.
        assert in_desktop(name, "bwrap", "--unshare-user", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "true") == ""
        attempted = docker("exec", "-u", "user", f"deskyard-{name}", "sh", "-c", "echo no >> /mnt/external/kept.txt", check=False)
        assert attempted.returncode != 0 and "Read-only file system" in attempted.stderr
        # Refresh the saved settings before removing the entry.
        page.reload()
        page.get_by_role("button", name="Remove host folder 1", exact=True).click()
        with page.expect_response(lambda r: r.request.method == "PATCH" and r.url.endswith(f"/api/desktops/{name}")) as sent:
            page.get_by_role("button", name="Save and restart", exact=True).click()
        state = admin.wait_job(sent.value.json()["job"])
        assert state["status"] == "done", state
        assert admin.desktop(name)["settings"]["mounts"] == []
        assert docker("exec", "-u", "user", f"deskyard-{name}", "test", "-e", "/mnt/external/kept.txt", check=False).returncode != 0
        assert (source / "kept.txt").read_text() == "host data\n"
        browser.close()


def test_host_mounts_create_preserve_and_keep_host_files(admin, name, tmp_path):
    source = source_folder(tmp_path)
    mounts = [{"source": str(source), "target": "/mnt/host", "readOnly": False}]
    try:
        desktop = admin.create(name, mounts=mounts)
        assert desktop["settings"]["mounts"] == mounts
        assert in_desktop(name, "sh", "-c", "echo desktop-data > /mnt/host/new.txt") == ""
        assert (source / "new.txt").read_text() == "desktop-data\n"
        # Unrelated settings must carry the mount into the replacement container.
        changed = wait_settings(admin, name, admin.patch(f"/api/desktops/{name}", json={"memoryBytes": 2 << 30}))
        assert changed["settings"]["mounts"] == mounts and changed["id"] == desktop["id"]
        assert in_desktop(name, "cat", "/mnt/host/new.txt") == "desktop-data\n"
        assert admin.post(f"/api/desktops/{name}/stop").status_code == 204
        changed = wait_settings(admin, name, admin.patch(f"/api/desktops/{name}", json={"mounts": [{**mounts[0], "readOnly": True}]}))
        assert changed["state"] == "stopped"
        assert admin.post(f"/api/desktops/{name}/start").status_code == 204
        attempted = docker("exec", "-u", "user", f"deskyard-{name}", "sh", "-c", "echo no >> /mnt/host/new.txt", check=False)
        assert attempted.returncode != 0 and "Read-only file system" in attempted.stderr
    finally:
        admin.delete(f"/api/desktops/{name}?files=1")
    assert (source / "kept.txt").read_text() == "host data\n"
    assert (source / "new.txt").read_text() == "desktop-data\n"


def test_missing_host_source_does_not_create_it_or_stop_desktop(admin, desktop, name, tmp_path):
    source = tmp_path.resolve() / "does-not-exist"
    result = admin.patch(f"/api/desktops/{name}", json={"mounts": [{"source": str(source), "target": "/mnt/missing", "readOnly": True}]})
    assert result.status_code == 202
    state = admin.wait_job(result.json()["job"])
    assert state["status"] == "failed"
    assert not source.exists()
    current = admin.desktop(name)
    assert current["state"] == "running" and current["id"] == desktop["id"]
    assert current["settings"]["mounts"] == []
