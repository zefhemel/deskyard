"""The supervisor's API: sessions, desktops, access, desktop proxy, isolation."""

import json
import subprocess
import uuid

import requests

from conftest import ADMIN_PASSWORD, docker
from helpers import bearer, in_desktop, make_account, make_credential, put, wait_until, ws_handshake

SELKIES_WEB = "/opt/selkies/lib/python3.13/site-packages/selkies/selkies_web"


def test_every_route_needs_a_session(supervisor):
    anon = requests.Session()
    for path in ["/api/me", "/api/environments", "/api/accounts", "/api/events", "/api/images"]:
        assert anon.get(supervisor.base + path, timeout=5).status_code == 401, path
    r = anon.get(supervisor.base + "/", allow_redirects=False, timeout=5)
    assert r.status_code == 302 and r.headers["Location"].startswith("/login")
    r = anon.get(supervisor.base + "/somedesktop/", allow_redirects=False, timeout=5)
    assert r.status_code == 302
    assert anon.get(supervisor.base + "/login", timeout=5).status_code == 200


def test_login_logout_and_wrong_password(supervisor):
    c = supervisor.client()
    assert c.post("/api/login", json={"username": "admin", "password": "nope"}).status_code == 401
    c.login("admin", ADMIN_PASSWORD)
    assert c.get("/api/me").json() == {"username": "admin", "admin": True}
    assert c.post("/api/logout").status_code == 204
    c.cookies.clear()
    assert c.get("/api/me").status_code == 401


def test_cross_origin_state_change_refused(admin):
    r = requests.post(admin.base + "/api/environments", json={"name": "x1"}, cookies=admin.cookies,
                      headers={"Origin": "https://evil.example"}, timeout=5)
    assert r.status_code == 403


def test_create_desktop_and_lifecycle(admin, desktop, name, supervisor):
    assert desktop["state"] == "running"
    assert admin.get(f"/{name}/").status_code == 200  # the UI's desktop page
    assert admin.get(f"/{name}/screen/").status_code == 200
    # (Who may open its stream: test_screen.test_stream_needs_session_access_and_same_origin.)
    for action, state in [("stop", "stopped"), ("start", "running"), ("restart", "running")]:
        assert admin.post(f"/api/environments/{name}/{action}").status_code == 204
        assert admin.desktop(name)["state"] == state

    def screenshot():
        r = admin.get(f"/api/environments/{name}/screen.png")
        return r if r.status_code == 200 else None

    png = wait_until(screenshot, 60, 1)  # the desktop takes a few seconds after a restart
    assert png and png.content[:4] == b"\x89PNG"
    procs = admin.get(f"/api/environments/{name}/processes").json()
    assert any("Xvfb" in " ".join(r) for r in procs["rows"])


def test_remove_keeps_or_deletes_files(admin, name, supervisor):
    admin.create(name)
    folder = supervisor.data / "home" / name
    assert folder.is_dir()
    assert admin.delete(f"/api/environments/{name}").status_code == 204
    assert folder.is_dir() and admin.get(f"/api/environments/{name}").status_code == 404
    # Creating it again reuses the folder; removing with files deletes it.
    v = admin.get(f"/api/validate-name?name={name}").json()
    assert v["ok"] and v["folderExists"]
    admin.create(name)
    assert admin.delete(f"/api/environments/{name}?files=1").status_code == 204
    assert not folder.exists()


def test_regular_account_sees_only_its_desktops(admin, desktop, name, supervisor):
    user = "u" + uuid.uuid4().hex[:5]
    assert admin.post("/api/accounts", json={"username": user, "password": "pw1", "admin": False, "grants": []}).status_code == 201
    anna = supervisor.client().login(user, "pw1")
    assert anna.get("/api/environments").json() == {"environments": [], "conflicts": []}
    assert anna.get(f"/api/environments/{name}").status_code == 404
    assert anna.get(f"/{name}/screen/").status_code == 404
    assert anna.post(f"/api/environments/{name}/stop").status_code == 404
    assert ws_handshake(anna.base, f"/{name}/screen/api/websockets", anna.cookies, origin=anna.base) == 404
    assert anna.post("/api/environments", json={"name": "nope"}).status_code == 403
    # Grant access: power yes, manage no.
    assert admin.put(f"/api/environments/{name}/access", json={"grants": [{"username": user, "level": "full"}]}).status_code == 204
    assert [c["name"] for c in anna.get("/api/environments").json()["environments"]] == [name]
    assert anna.post(f"/api/environments/{name}/restart").status_code == 204
    assert anna.delete(f"/api/environments/{name}").status_code == 403
    assert anna.patch(f"/api/environments/{name}", json={"memoryBytes": 1}).status_code == 403
    # A password change signs the account out everywhere.
    assert admin.patch(f"/api/accounts/{user}", json={"password": "pw2"}).status_code == 200
    assert anna.get("/api/me").status_code == 401
    admin.delete(f"/api/accounts/{user}")


def test_revoking_access_closes_the_desktop(admin, desktop, name, supervisor):
    import asyncio
    import websockets

    user = make_account(admin, [name])
    anna = supervisor.client().login(user, "pw")
    cookie = "; ".join(f"{k}={v}" for k, v in anna.cookies.items())

    async def run():
        url = anna.base.replace("http", "ws") + f"/{name}/screen/api/websockets"
        async with websockets.connect(url, additional_headers={"Cookie": cookie, "Origin": anna.base},
                                      subprotocols=["binary"]) as ws:
            await asyncio.sleep(1)
            assert user in admin.desktop(name)["viewers"]
            admin.put(f"/api/environments/{name}/access", json={"grants": []})
            try:
                await asyncio.wait_for(ws.wait_closed(), 10)
            except asyncio.TimeoutError:
                raise AssertionError("desktop connection not closed after revoking access")
    asyncio.run(run())
    admin.delete(f"/api/accounts/{user}")


def test_last_admin_protected(admin):
    assert admin.delete("/api/accounts/admin").status_code == 409
    assert admin.patch("/api/accounts/admin", json={"admin": False}).status_code == 409


def test_desktop_publishes_only_8080_and_is_isolated(admin, desktop, name, supervisor):
    other = "o" + uuid.uuid4().hex[:5]
    admin.create(other)
    try:
        ports = docker("port", f"deskyard-{name}").stdout.split()
        if supervisor.container:
            assert ports == []
        else:
            assert ports[0] == "8080/tcp" and ports[2].startswith("127.0.0.1:") and len(ports) == 3, ports
        ip = json.loads(docker("inspect", f"deskyard-{other}").stdout)[0]["NetworkSettings"]["Networks"][f"deskyard-{other}"]["IPAddress"]
        r = subprocess.run(["docker", "exec", "-u", "user", f"deskyard-{name}", "curl", "-s", "-m", "3", "-o", "/dev/null",
                            "-w", "%{http_code}", f"http://{ip}:8080/"], capture_output=True, text=True)
        # Unreachable where the engine isolates bridge networks (Docker on Linux);
        # where it doesn't (OrbStack, Docker Desktop), the per-desktop token refuses it.
        assert r.stdout in ("000", "", "401"), f"{name} reached {other}: {r.stdout}"
    finally:
        admin.delete(f"/api/environments/{other}?files=1")


def test_replaced_web_server_isnt_served(admin, desktop, name):
    """An agent that replaces the desktop's web content can't get it served on the supervisor's origin."""
    before = admin.get(f"/{name}/screen/").text
    subprocess.run(["docker", "exec", "-u", "root", f"deskyard-{name}", "sh", "-c",
                    f"echo '<script>evil()</script>' > {SELKIES_WEB}/index.html"], check=True)
    assert admin.get(f"/{name}/screen/").text == before
    assert admin.get(f"/{name}/anything-else").status_code == 404


def test_image_without_contract_refused(admin, name):
    r = admin.post("/api/environments", json={"name": name, "image": "alpine:3.22"})
    st = admin.wait_job(r.json()["job"])
    assert st["status"] == "failed" and "Deskyard desktop image" in st["error"]
    assert admin.get(f"/api/environments/{name}").status_code == 404


def test_settings_change_recreates_and_keeps_folder(admin, desktop, name, supervisor):
    put(name, "probe.txt", b"hi")
    revision = desktop["revision"]
    assert desktop["created"] and desktop["job"] is None
    size = admin.get(f"/api/environments/{name}/folder").json()
    assert size["path"].endswith("/" + name) and size["bytes"] >= 2
    user = make_account(admin, [name])
    anna = supervisor.client().login(user, "pw")
    r = admin.patch(f"/api/environments/{name}", json={"memoryBytes": 2 << 30, "nanoCpus": 2_000_000_000})
    job = r.json()["job"]
    # Someone else who can see the desktop sees the job and can follow it.
    assert anna.desktop(name)["job"] == job
    st = anna.wait_job(job)
    assert st["status"] == "done", st
    admin.delete(f"/api/accounts/{user}")
    c = admin.desktop(name)
    assert c["name"] == name and c["state"] == "running"
    assert c["revision"] == c["appliedRevision"] == revision + 1
    assert c["created"] == desktop["created"] and c["job"] is None
    assert c["settings"] == {
        "memoryBytes": 2 << 30, "nanoCpus": 2_000_000_000, "volumes": [],
        "vpnConnectionId": None, "lanExceptions": [], "dockerAccess": False,
    }
    assert in_desktop(name, "cat", "/workspace/probe.txt") == "hi"


def test_restart_rediscovers_desktops(supervisor, admin, desktop, name):
    supervisor.stop()
    supervisor.start()
    c = supervisor.admin()
    assert c.desktop(name)["state"] == "running"
    assert ws_handshake(c.base, f"/{name}/screen/api/websockets", c.cookies, origin=c.base) == 101
    admin.cookies.update(c.cookies)


def test_old_desktop_routes_gone(admin, desktop, name):
    for path in ["/api/desktops", f"/api/desktops/{name}", f"/api/desktops/{name}/stats"]:
        assert admin.get(path).status_code == 404, path
    assert admin.post(f"/api/desktops/{name}/stop").status_code == 404


def test_visible_data_layout_and_labels(admin, desktop, name, supervisor):
    data = supervisor.data
    assert not (data / ".deskyard").exists()
    for d in ("secret", "state"):
        assert (data / d).stat().st_mode & 0o777 == 0o700, d
    assert (data / "home" / name).is_dir()
    defs = json.loads(supervisor.read(data / "environments.json"))
    assert defs["version"] == 1
    mine = [e for e in defs["environments"] if e["name"] == name]
    assert len(mine) == 1 and mine[0]["power"] == "running" and mine[0]["revision"] == desktop["revision"]
    labels = json.loads(docker("inspect", f"deskyard-{name}").stdout)[0]["Config"]["Labels"]
    assert labels["deskyard.name"] == name and labels["deskyard.instance"] == supervisor.instance() != ""
    assert labels["deskyard.revision"] == str(desktop["revision"])
    assert not [k for k in labels if k == "deskyard.id" or k.startswith("deskyard.setting.")], labels


def test_restart_recreates_a_lost_container(supervisor, admin, desktop, name):
    """The definition outlives its container: the supervisor builds it again."""
    put(name, "kept.txt", b"still here")
    t = make_credential(admin, name)
    supervisor.stop()
    docker("rm", "-f", f"deskyard-{name}")
    supervisor.start()
    c = supervisor.admin()
    admin.cookies.update(c.cookies)
    d = wait_until(lambda: (d := c.desktop(name))["state"] == "running" and d, 120, 1)
    assert d, c.desktop(name)
    assert d["created"] == desktop["created"] and d["revision"] == d["appliedRevision"] == desktop["revision"]
    assert in_desktop(name, "cat", "/workspace/kept.txt") == "still here"
    assert requests.get(f"{c.base}/api/environments/{name}/status", headers=bearer(t), timeout=10).status_code == 200


def test_recreated_name_does_not_revive_grants(admin, name, supervisor):
    admin.create(name)
    user = make_account(admin, [name])
    t = make_credential(admin, name)
    assert admin.delete(f"/api/environments/{name}?files=1").status_code == 204
    admin.create(name)
    try:
        assert admin.desktop(name)["access"] == []
        anna = supervisor.client().login(user, "pw")
        assert anna.get(f"/api/environments/{name}").status_code == 404
        assert requests.get(f"{admin.base}/api/environments/{name}/status", headers=bearer(t), timeout=10).status_code == 404
    finally:
        admin.delete(f"/api/environments/{name}?files=1")
        admin.delete(f"/api/accounts/{user}")
