"""Custom images: recipe builds, their checks, rebuilds and upgrades, removal
and cancelling. Builds run on the real engine, FROM the test desktop image."""

from __future__ import annotations

import json
import socket
import time
import uuid

import pytest

from conftest import IMAGE, docker

# A small Flathub app on org.freedesktop.Platform (whose runtime is smaller
# than GNOME's): about 0.5 GB to download with its runtime and extensions.
FLATPAK_APP = "net.sourceforge.ExtremeTuxRacer"


def online(host="dl.flathub.org") -> bool:
    try:
        socket.create_connection((host, 443), timeout=5).close()
        return True
    except OSError:
        return False


def follow(admin, job: str, timeout=2400, until=None) -> tuple[dict, list[str]]:
    """Follows a job; returns its last state and every message. With until,
    returns as soon as a message satisfies it."""
    messages = []
    with admin.get(f"/api/jobs/{job}", stream=True, timeout=timeout) as r:
        last = None
        for line in r.iter_lines():
            if not line.startswith(b"data: "):
                continue
            last = json.loads(line[6:])
            messages.append(last["message"])
            if until and until(last["message"]):
                return last, messages
            if last["status"] != "running":
                break
    return last, messages


def tags(slug: str) -> list[str]:
    out = docker("image", "ls", "--format", "{{.Repository}}:{{.Tag}}", f"deskyard-custom/{slug}").stdout
    return out.split()


def user_sh(name: str, script: str) -> str:
    r = docker("exec", "-u", "user", f"deskyard-{name}", "bash", "-lc", script, check=False)
    assert r.returncode == 0, r.stdout + r.stderr
    return r.stdout


@pytest.fixture
def slug(admin):
    s = "t" + uuid.uuid4().hex[:6]
    yield s
    admin.delete(f"/api/recipes/{s}")
    for t in tags(s):
        docker("rmi", "-f", t, check=False)


def build(admin, recipe: dict, timeout=2400) -> tuple[dict, list[str]]:
    r = admin.post("/api/recipes", json={"name": "Test " + recipe["slug"], "base": IMAGE, **recipe})
    assert r.status_code == 202, r.text
    return follow(admin, r.json()["job"], timeout)


def catalog(admin) -> dict:
    r = admin.get("/api/images")
    assert r.status_code == 200, r.text
    return {i["ref"]: i for i in r.json()}


@pytest.mark.network
@pytest.mark.slow
def test_recipe_builds_and_works(admin, slug, name):
    if not online():
        pytest.skip("no network: the build installs from npm, Debian and Flathub")
    start = time.time()
    st, log = build(admin, {
        "slug": slug, "mise": ["npm:cowsay"], "flatpaks": [{"id": FLATPAK_APP}],
        "commands": "apt-get update\napt-get install -y figlet",
    })
    print(f"build took {time.time() - start:.0f} s")
    assert st["status"] == "done", st["error"] + "\n" + "\n".join(log[-40:])
    ref = f"deskyard-custom/{slug}:latest"
    entry = catalog(admin)[ref]
    assert entry["custom"] and entry["recipe"] == slug and not entry["baseUpdated"] and not entry["secrets"]
    labels = json.loads(docker("image", "inspect", "-f", "{{json .Config.Labels}}", ref).stdout)
    assert labels["deskyard.kind"] == "custom" and labels["deskyard.contract"] == "1"
    assert json.loads(labels["deskyard.recipe"])["slug"] == slug

    r = admin.post("/api/desktops", json={"name": name, "image": ref})
    assert r.status_code == 202, r.text
    assert admin.wait_job(r.json()["job"])["status"] == "done"
    try:
        assert "moo" in user_sh(name, "cowsay moo")
        assert user_sh(name, "figlet hi").strip()
        assert FLATPAK_APP in user_sh(name, "flatpak list --columns=application")
        user_sh(name, f"flatpak run --command=true {FLATPAK_APP}")
    finally:
        admin.delete(f"/api/desktops/{name}?files=1")


@pytest.mark.network
def test_build_with_sudo_rejected(admin, slug):
    if not online("deb.debian.org"):
        pytest.skip("no network: the build installs sudo from Debian")
    st, log = build(admin, {"slug": slug, "commands": "apt-get update\napt-get install -y sudo"})
    assert st["status"] == "failed", st
    assert "/usr/bin/sudo" in st["error"], st["error"]
    assert tags(slug) == []
    assert f"deskyard-custom/{slug}:latest" not in catalog(admin)


@pytest.mark.network
@pytest.mark.parametrize("commands,finding", [
    ("apt-get update\napt-get install -y libcap2-bin\nsetcap cap_setuid+ep \"$(readlink -f /usr/bin/python3)\"",
     "(file capabilities)"),
    ("passwd -d root", "account root (UID 0 with no password)"),
])
def test_build_with_root_path_rejected(admin, slug, commands, finding):
    if "apt-get" in commands and not online("deb.debian.org"):
        pytest.skip("no network: the build installs libcap2-bin from Debian")
    st, log = build(admin, {"slug": slug, "commands": commands})
    assert st["status"] == "failed", st
    assert finding in st["error"], st["error"]
    assert tags(slug) == []


def test_rebuild_and_upgrade_keeps_home(admin, slug, name):
    st, log = build(admin, {"slug": slug, "commands": "echo built > /opt/marker"})
    assert st["status"] == "done", st
    ref = f"deskyard-custom/{slug}:latest"
    r = admin.post("/api/desktops", json={"name": name, "image": ref})
    assert r.status_code == 202, r.text
    assert admin.wait_job(r.json()["job"])["status"] == "done"
    try:
        user_sh(name, "echo mine > ~/mine")
        before = admin.desktop(name)
        old_id = docker("inspect", "-f", "{{.Image}}", f"deskyard-{name}").stdout.strip()
        assert before["updateAvailable"] is None
        # The version is the build time to the minute.
        while time.strftime("%Y-%m-%d %H:%M") == before["imageVersion"]:
            time.sleep(2)

        r = admin.post(f"/api/recipes/{slug}/rebuild")
        assert r.status_code == 202, r.text
        st, log = follow(admin, r.json()["job"])
        assert st["status"] == "done", st
        assert admin.desktop(name)["updateAvailable"] is not None
        assert len(tags(slug)) == 3  # the desktop still uses the old build

        r = admin.post(f"/api/desktops/{name}/upgrade")
        assert r.status_code == 202, r.text
        assert admin.wait_job(r.json()["job"])["status"] == "done"
        after = admin.desktop(name)
        assert after["imageVersion"] != before["imageVersion"] and after["updateAvailable"] is None
        assert docker("inspect", "-f", "{{.Image}}", f"deskyard-{name}").stdout.strip() != old_id
        assert user_sh(name, "cat ~/mine").strip() == "mine"
    finally:
        admin.delete(f"/api/desktops/{name}?files=1")


def test_delete_recipe_in_use_refused(admin, slug, name):
    st, _ = build(admin, {"slug": slug})
    assert st["status"] == "done", st
    ref = f"deskyard-custom/{slug}:latest"
    r = admin.post("/api/desktops", json={"name": name, "image": ref})
    assert r.status_code == 202, r.text
    assert admin.wait_job(r.json()["job"])["status"] == "done"
    try:
        r = admin.delete(f"/api/recipes/{slug}")
        assert r.status_code == 409 and name in r.json()["error"]
        assert admin.delete(f"/api/images?ref={ref}").status_code == 409
    finally:
        admin.delete(f"/api/desktops/{name}?files=1")
    assert admin.delete(f"/api/recipes/{slug}").status_code == 204
    assert tags(slug) == [] and ref not in catalog(admin)
    assert slug not in [x["slug"] for x in admin.get("/api/recipes").json()]


def build_containers() -> list[str]:
    """Containers of a build: they carry the build's deskyard-build-… host."""
    out = []
    for cid in docker("ps", "-aq").stdout.split():
        hosts = docker("inspect", "-f", "{{json .HostConfig.ExtraHosts}}", cid, check=False).stdout
        if "deskyard-build-" in hosts:
            out.append(cid)
    return out


def test_cancel_build(admin, slug):
    others = set(build_containers())  # other builds on this machine
    r = admin.post("/api/recipes", json={"slug": slug, "name": "Sleeper", "base": IMAGE, "commands": "sleep 600"})
    assert r.status_code == 202, r.text
    job = r.json()["job"]
    st, log = follow(admin, job, timeout=300, until=lambda m: "+ sleep 600" in m)
    assert st["status"] == "running", (st, log[-20:])
    time.sleep(1)
    assert admin.post(f"/api/jobs/{job}/cancel").status_code == 204
    t0 = time.time()
    st, _ = follow(admin, job, timeout=15)
    assert st["status"] == "failed" and st["error"] == "cancelled", st
    assert time.time() - t0 < 15
    deadline = time.time() + 15
    while set(build_containers()) - others and time.time() < deadline:
        time.sleep(1)
    assert set(build_containers()) - others == set()
    assert tags(slug) == []
