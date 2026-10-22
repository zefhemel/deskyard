"""Agent activity: the activity API, Desktop.agents and the screen-events stream."""

import asyncio
import contextlib
import subprocess
import threading
import time
import uuid

import requests

from test_agent import exec_ws, make_token


def make_account(admin, desktops):
    user = "u" + uuid.uuid4().hex[:5]
    r = admin.post("/api/accounts", json={"username": user, "password": "pw", "admin": False, "desktops": desktops})
    assert r.status_code == 201, r.text
    return user


def test_activity_api_shape(admin, desktop, name):
    r = admin.get(f"/api/desktops/{name}/activity")
    assert r.status_code == 200, r.text
    assert r.json() == {"name": name, "agents": []}
    assert admin.desktop(name)["agents"] == []


def test_activity_api_access(admin, desktop, name, supervisor):
    user = make_account(admin, [])
    try:
        other = supervisor.client().login(user, "pw")
        for path in ("activity", "screen-events"):
            assert other.get(f"/api/desktops/{name}/{path}", timeout=10).status_code == 404, path
        t = make_token(admin, name)
        for path in ("activity", "screen-events"):
            r = requests.get(f"{supervisor.base}/api/desktops/{name}/{path}",
                             headers={"Authorization": f"Bearer {t['token']}"}, timeout=10)
            assert r.status_code == 401, (path, r.status_code)
    finally:
        admin.delete(f"/api/accounts/{user}")


def test_screen_events_stop_after_revoke(admin, desktop, name, supervisor):
    user = make_account(admin, [name])
    try:
        viewer = supervisor.client().login(user, "pw")
        opened, ended = threading.Event(), threading.Event()

        def watch():
            with viewer.get(f"/api/desktops/{name}/screen-events", stream=True, timeout=60) as r:
                assert r.status_code == 200
                opened.set()
                for _ in r.iter_lines():
                    pass
            ended.set()

        th = threading.Thread(target=watch, daemon=True)
        th.start()
        assert opened.wait(10), "stream didn't open"
        time.sleep(0.5)
        assert not ended.is_set()
        assert admin.put(f"/api/desktops/{name}/access", json={"usernames": []}).status_code == 204
        assert ended.wait(5), "screen-events stream still open after revoking access"
    finally:
        admin.delete(f"/api/accounts/{user}")


# --- inside activity: the supervisor reads each desktop's /_activity/ stream ---------------


def container_of(name):
    out = subprocess.run(["docker", "ps", "--filter", f"label=deskyard.name={name}", "--format", "{{.Names}}"],
                         capture_output=True, text=True, check=True).stdout.split()
    assert out, f"no container for {name}"
    return out[0]


@contextlib.contextmanager
def events_open(admin):
    """Keep /api/events open: the supervisor only reads inside streams while someone looks."""
    stop = threading.Event()
    opened = threading.Event()

    def run():
        with admin.get("/api/events", stream=True, timeout=120) as r:
            opened.set()
            for _ in r.iter_lines():
                if stop.is_set():
                    break

    th = threading.Thread(target=run, daemon=True)
    th.start()
    assert opened.wait(10), "events stream didn't open"
    try:
        yield
    finally:
        stop.set()


def inside_click(name):
    subprocess.run(["docker", "exec", "-t", "-u", "user", container_of(name), "bash", "-ic", "deskyard click 10 10; true"],
                   capture_output=True, text=True, timeout=60)


def inside_agents(admin, name):
    return [a for a in admin.desktop(name)["agents"] if a["inside"]]


def wait_for(fn, seconds=15):
    end = time.time() + seconds
    while time.time() < end:
        v = fn()
        if v:
            return v
        time.sleep(0.5)
    return fn()


def test_inside_click_is_a_terminal(admin, desktop, name):
    with events_open(admin):
        inside_click(name)
        agents = wait_for(lambda: inside_agents(admin, name))
        assert [(a["id"], a["name"], a["inside"], a["owner"]) for a in agents] == [("inside:a terminal", "a terminal", True, "")]
        act = admin.get(f"/api/desktops/{name}/activity").json()
        mine = [a for a in act["agents"] if a["id"] == "inside:a terminal"][0]
        assert any(i["kind"] == "screen" and i["action"] == "click" for i in mine["recent"]), mine


def test_nested_deskyard_not_counted_inside(admin, desktop, name, supervisor):
    t = make_token(admin, name)
    with events_open(admin):
        asyncio.run(exec_ws(supervisor.base, t["token"], ["sh", "-c", "deskyard click 5 5"]))
        time.sleep(5)
        agents = admin.desktop(name)["agents"]
        assert [a["id"] for a in agents] == [t["id"]], agents
        act = admin.get(f"/api/desktops/{name}/activity").json()["agents"][0]
        items = act["now"] + act["recent"]
        assert any(i["kind"] == "command" and i["text"] == "sh -c 'deskyard click 5 5'" for i in items), items
        assert not any(i["kind"] == "screen" for i in items), items


def test_housekeeping_not_counted(admin, desktop, name, supervisor):
    t = make_token(admin, name)
    with events_open(admin):
        assert admin.get(f"/api/desktops/{name}/screen.png", timeout=60).status_code == 200
        assert admin.post(f"/api/desktops/{name}/keys", json={"keys": "Escape"}).status_code < 300
        r = requests.get(f"{supervisor.base}/api/agent/v1", headers={"Authorization": f"Bearer {t['token']}"}, timeout=30)
        assert r.status_code == 200, r.text
        time.sleep(5)
        assert inside_agents(admin, name) == []


def test_inside_backfill(admin, desktop, name):
    inside_click(name)  # nobody is looking: nothing reads the stream yet
    time.sleep(1)
    assert inside_agents(admin, name) == []
    with events_open(admin):
        agents = wait_for(lambda: inside_agents(admin, name))
        assert [a["id"] for a in agents] == ["inside:a terminal"]
