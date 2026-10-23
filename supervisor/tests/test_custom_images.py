"""Custom images: recipe builds, their checks, rebuilds and upgrades, removal
and cancelling. Builds run on the real engine, FROM the test desktop image."""

from __future__ import annotations

import json
import socket
import time
import uuid

import pytest

from conftest import IMAGE, docker
from helpers import custom_tags as tags
from helpers import user_sh

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


def build(admin, recipe: dict, timeout=2400) -> tuple[dict, list[str]]:
    r = admin.post("/api/recipes", json={"name": "Test " + recipe["slug"], "base": IMAGE, **recipe})
    assert r.status_code == 202, r.text
    return follow(admin, r.json()["job"], timeout)


def catalog(admin) -> dict:
    r = admin.get("/api/images")
    assert r.status_code == 200, r.text
    return {i["ref"]: i for i in r.json()}


@pytest.mark.build
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
    assert labels["deskyard.kind"] == "custom" and labels["deskyard.contract"] == "3"
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


@pytest.mark.build
@pytest.mark.network
def test_build_with_sudo_rejected(admin, slug):
    if not online("deb.debian.org"):
        pytest.skip("no network: the build installs sudo from Debian")
    st, log = build(admin, {"slug": slug, "commands": "apt-get update\napt-get install -y sudo"})
    assert st["status"] == "failed", st
    assert "/usr/bin/sudo" in st["error"], st["error"]
    assert tags(slug) == []
    assert f"deskyard-custom/{slug}:latest" not in catalog(admin)


@pytest.mark.build
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


@pytest.mark.build
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
        # The version is the build time to the minute, in the supervisor's time zone
        # (UTC in container mode, local when run directly).
        while before["imageVersion"] in (time.strftime("%Y-%m-%d %H:%M"), time.strftime("%Y-%m-%d %H:%M", time.gmtime())):
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


@pytest.mark.build
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


@pytest.mark.build
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


# --- images from a desktop ------------------------------------------------------------------

@pytest.fixture
def slugs(admin):
    """Makes recipe slugs; removes their recipes and tags afterwards, newest
    first (an image may be the base of a later one)."""
    made = []

    def make() -> str:
        made.append("t" + uuid.uuid4().hex[:6])
        return made[-1]

    yield make
    for s in reversed(made):
        admin.delete(f"/api/recipes/{s}")
        for t in tags(s):
            docker("rmi", "-f", t, check=False)


def from_desktop(admin, desktop: str, slug: str, groups: list[str], include=()) -> tuple[dict, list[str]]:
    r = admin.post(f"/api/desktops/{desktop}/image",
                   json={"name": "From " + desktop, "slug": slug, "groups": groups, "include": list(include)})
    assert r.status_code == 202, r.text
    return follow(admin, r.json()["job"])


def new_desktop(admin, image: str) -> str:
    n = "t" + uuid.uuid4().hex[:6]
    r = admin.post("/api/desktops", json={"name": n, "image": image})
    assert r.status_code == 202, r.text
    st = admin.wait_job(r.json()["job"])
    assert st["status"] == "done", st
    return n


def exists(name: str, path: str) -> bool:
    return docker("exec", "-u", "user", f"deskyard-{name}", "test", "-e", path, check=False).returncode == 0


@pytest.mark.build
@pytest.mark.network
@pytest.mark.slow
def test_image_from_desktop(admin, desktop, name, slugs):
    if not online("registry.npmjs.org"):
        pytest.skip("no network: the desktop installs cowsay from npm")
    user_sh(name, "mkdir -p ~/.config/app ~/.ssh ~/.codex"
                  " && echo '{\"theme\": \"dark\"}' > ~/.config/app/settings.json"
                  " && echo private > ~/.ssh/id_test && echo token > ~/.codex/auth.json"
                  " && mise use -g npm:cowsay && cowsay hi")

    r = admin.get(f"/api/desktops/{name}/image-scan")
    assert r.status_code == 200, r.text
    scan = r.json()
    groups = {g["name"]: g for g in scan["groups"]}
    assert list(groups) == ["tools", "flatpak", "settings", "dotfiles", "browser", "other"]
    assert ".ssh" in groups["dotfiles"]["secrets"] and ".codex/auth.json" in groups["dotfiles"]["secrets"]
    assert ".local/share/mise" in groups["tools"]["paths"] and groups["tools"]["bytes"] > 0
    assert ".config/app" in groups["settings"]["paths"]
    assert scan["totalBytes"] >= sum(g["bytes"] for g in scan["groups"])
    assert scan["flatpaks"] == [] and isinstance(scan["remotes"], list)

    # Settings and tools only: no secrets.
    plain = slugs()
    st, log = from_desktop(admin, name, plain, ["settings", "tools"])
    assert st["status"] == "done", st["error"] + "\n" + "\n".join(log[-40:])
    ref = f"deskyard-custom/{plain}:latest"
    assert not catalog(admin)[ref]["secrets"]
    labels = json.loads(docker("image", "inspect", "-f", "{{json .Config.Labels}}", ref).stdout)
    assert "deskyard.secrets" not in labels
    rec = json.loads(labels["deskyard.recipe"])
    assert rec["homeFrom"] == "image" and rec["base"] == IMAGE and not rec.get("secrets")
    other = new_desktop(admin, ref)
    try:
        assert json.loads(user_sh(other, "cat ~/.config/app/settings.json")) == {"theme": "dark"}
        assert "moo" in user_sh(other, "cowsay moo")
        assert not exists(other, "/workspace/.ssh/id_test")
        assert not exists(other, "/workspace/.codex/auth.json")
    finally:
        admin.delete(f"/api/desktops/{other}?files=1")

    # Again, with auth.json ticked: the image contains secrets.
    secret = slugs()
    st, log = from_desktop(admin, name, secret, ["settings", "tools"], include=[".codex/auth.json"])
    assert st["status"] == "done", st["error"] + "\n" + "\n".join(log[-40:])
    ref = f"deskyard-custom/{secret}:latest"
    assert catalog(admin)[ref]["secrets"]
    other = new_desktop(admin, ref)
    try:
        assert user_sh(other, "cat ~/.codex/auth.json").strip() == "token"
        assert not exists(other, "/workspace/.ssh/id_test")
    finally:
        admin.delete(f"/api/desktops/{other}?files=1")


PANEL_TYPES = ("p() { xfconf-query -c xfce4-panel \"$@\"; }"
               " && for i in $(seq 60); do pgrep -x xfce4-panel >/dev/null && break; sleep 1; done"
               " && for id in $(p -p /panels/panel-1/plugin-ids | grep -E '^[0-9]+$'); do p -p /plugins/plugin-$id; done")


@pytest.mark.build
def test_panel_changes_stay_in_image_from_desktop(admin, desktop, name, slugs):
    """The panel migrations' run-once markers travel with the settings: a
    pager the user removed doesn't come back in desktops made from the image."""
    assert "pager" in user_sh(name, PANEL_TYPES).split()
    user_sh(name, "p() { xfconf-query -c xfce4-panel \"$@\"; }; args=()"
                  "; for id in $(p -p /panels/panel-1/plugin-ids | grep -E '^[0-9]+$'); do"
                  " [ \"$(p -p /plugins/plugin-$id)\" = pager ] || args+=(-t int -s $id); done"
                  "; p -p /panels/panel-1/plugin-ids --force-array \"${args[@]}\"")
    assert "pager" not in user_sh(name, PANEL_TYPES).split()
    # Stopping logs out of XFCE, which saves the panel settings.
    r = admin.post(f"/api/desktops/{name}/stop")
    assert r.status_code == 204, r.text
    scan = {g["name"]: g for g in admin.get(f"/api/desktops/{name}/image-scan").json()["groups"]}
    assert ".config/workspace" in scan["settings"]["paths"] and ".config/xfce4" in scan["settings"]["paths"], scan["settings"]

    s = slugs()
    st, log = from_desktop(admin, name, s, ["settings"])
    assert st["status"] == "done", st["error"] + "\n" + "\n".join(log[-40:])
    other = new_desktop(admin, f"deskyard-custom/{s}:latest")
    try:
        types = user_sh(other, PANEL_TYPES).split()
        assert "pager" not in types and "clock" in types, types
    finally:
        admin.delete(f"/api/desktops/{other}?files=1")


@pytest.mark.build
def test_secret_flag_passes_down(admin, desktop, name, slugs):
    user_sh(name, "mkdir -p ~/.codex && echo token > ~/.codex/auth.json")
    secret = slugs()
    st, log = from_desktop(admin, name, secret, [], include=[".codex/auth.json"])
    assert st["status"] == "done", st["error"] + "\n" + "\n".join(log[-40:])
    parent = f"deskyard-custom/{secret}:latest"
    assert catalog(admin)[parent]["secrets"]

    child = slugs()
    st, log = build(admin, {"slug": child, "base": parent, "commands": "true"})
    assert st["status"] == "done", st["error"] + "\n" + "\n".join(log[-40:])
    assert catalog(admin)[f"deskyard-custom/{child}:latest"]["secrets"]


def test_image_from_desktop_refuses_non_secrets_as_include(admin, desktop, name, slugs):
    r = admin.post(f"/api/desktops/{name}/image",
                   json={"name": "x", "slug": slugs(), "groups": [], "include": [".bashrc"]})
    assert r.status_code == 400 and ".bashrc" in r.json()["error"], r.text


@pytest.mark.network
def test_tools_show_overrides(supervisor, admin, desktop, name):
    """The base image keeps node in /opt/mise; a desktop that installs its own
    node lists both, with where each one comes from."""
    if not online("registry.npmjs.org"):
        pytest.skip("no network: the desktop installs node")
    r = admin.get(f"/api/desktops/{name}/tools")
    assert r.status_code == 200, r.text
    before = r.json()
    image_node = [t["version"] for t in before if t["tool"] == "node" and t["source"] == "image"]
    assert image_node and not [t for t in before if t["source"] == "desktop"], before

    out = user_sh(name, "mise use -g node@22 >/dev/null 2>&1; mise ls --json node | grep -c install_path")
    assert int(out) >= 1
    tools = admin.get(f"/api/desktops/{name}/tools").json()
    nodes = {t["source"]: t["version"] for t in tools if t["tool"] == "node"}
    assert nodes["desktop"].startswith("22.") and nodes["image"] == image_node[0], tools

    from playwright.sync_api import expect, sync_playwright

    from helpers import signed_in
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.goto(f"{supervisor.base}/desktops/{name}")
        line = page.get_by_text(f"{nodes['desktop']} (this desktop) · {nodes['image']} in the image")
        expect(line.first).to_be_visible(timeout=15000)
        browser.close()

    # Stopped: nothing to show, and not an error.
    assert admin.post(f"/api/desktops/{name}/stop").status_code in (200, 202, 204)
    r = admin.get(f"/api/desktops/{name}/tools")
    assert r.status_code == 200 and r.json() == []
