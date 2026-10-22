"""Agent activity: the activity API, Desktop.agents and the screen-events stream."""

import asyncio
import json
import contextlib
import subprocess
import threading
import time
import uuid

import requests

from conftest import free_port
from test_agent import exec_ws, make_token
from test_cli import cli, remote, forwards  # noqa: F401  (fixtures)


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


# ---- outside agents are recorded


def run(admin, token, argv, **kw):
    return asyncio.run(exec_ws(admin.base, token["token"], argv, **kw))


def summary(admin, name, token_id=None):
    agents = admin.get(f"/api/desktops/{name}/activity").json()["agents"]
    if token_id:
        agents = [a for a in agents if a["id"] == token_id]
    assert len(agents) == 1, agents
    return agents[0]


def wait_for(fn, timeout=5, what="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        v = fn()
        if v:
            return v
        time.sleep(0.25)
    raise AssertionError(f"timed out waiting for {what}")


def has_recent(sum_, **want):
    return any(all(it.get(k) == v for k, v in want.items()) for it in sum_["recent"])


def test_outside_exec_click_type(admin, desktop, name):
    t = admin.post(f"/api/desktops/{name}/tokens", json={"name": "deploy-bot", "expires": None}).json()
    stream, opened = [], threading.Event()

    def watch():
        with admin.get(f"/api/desktops/{name}/screen-events", stream=True, timeout=60) as r:
            opened.set()
            for line in r.iter_lines():
                stream.append(line.decode())

    threading.Thread(target=watch, daemon=True).start()
    assert opened.wait(10)
    time.sleep(0.5)
    _, _, code = run(admin, t, ["sh", "-c", "exit 3"])
    assert code == 3
    run(admin, t, ["deskyard", "click", "100", "120"])
    run(admin, t, ["deskyard", "type", "hunter2-typed"])
    time.sleep(1)

    agents = admin.desktop(name)["agents"]
    assert len(agents) == 1
    a = agents[0]
    assert {k: a[k] for k in ("id", "name", "owner", "inside")} == {"id": t["id"], "name": "deploy-bot", "owner": "admin", "inside": False}
    raw = admin.get(f"/api/desktops/{name}/activity").text
    s = json.loads(raw)["agents"][0]
    assert has_recent(s, kind="command", text="sh -c 'exit 3'", exit=3), s
    assert has_recent(s, kind="screen", action="click", count=1), s
    assert has_recent(s, kind="screen", action="type"), s
    assert s["now"] == []
    text = "\n".join(stream)
    events = [json.loads(l[5:]) for l in stream if l.startswith("data:")]
    assert any(e["action"] == "click" and e["pos"] == [100, 120] for e in events), events
    assert any(e["action"] == "type" for e in events), events
    for where in (raw, json.dumps(admin.desktop(name)), text):
        assert "hunter2-typed" not in where


def test_token_on_command_line_is_masked(admin, desktop, name):
    t = make_token(admin, name)
    run(admin, t, ["echo", "--token", "abc123"])
    raw = admin.get(f"/api/desktops/{name}/activity").text
    assert "abc123" not in raw
    assert has_recent(summary(admin, name, t["id"]), text="echo --token •••")


def test_disconnected_command(admin, desktop, name):
    t = make_token(admin, name)

    async def go():
        task = asyncio.create_task(exec_ws(admin.base, t["token"], ["sleep", "30"]))
        for _ in range(40):
            agents = admin.get(f"/api/desktops/{name}/activity").json()["agents"]
            if agents and any(i["kind"] == "command" and i["text"] == "sleep 30" for i in agents[0]["now"]):
                break
            await asyncio.sleep(0.25)
        else:
            raise AssertionError("sleep 30 never showed under now")
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass

    asyncio.run(go())
    wait_for(lambda: has_recent(summary(admin, name, t["id"]), text="sleep 30", disconnected=True), 5, "disconnected line")


def test_agent_cannot_turn_inside_recording_on(admin, desktop, name):
    t = make_token(admin, name)
    out, _, _ = run(admin, t, ["sh", "-c", "echo $DESKYARD_ACTIVITY"], env={"DESKYARD_ACTIVITY": "on"})
    assert out.decode().strip() == "off"


def test_browser_presence(admin, desktop, name):
    t = make_token(admin, name)
    r = requests.get(admin.base + "/api/agent/v1/cdp/json/version", headers={"Authorization": f"Bearer {t['token']}"}, timeout=20)
    assert r.status_code == 200, r.text
    s = summary(admin, name, t["id"])
    assert any(i["kind"] == "browser" for i in s["now"]), s


def test_cp_recorded(cli, remote, admin, name, tmp_path):  # noqa: F811
    f = tmp_path / "f.bin"
    f.write_bytes(b"x" * 1000)
    remote("-d", name, "cp", str(f), f"{name}:/workspace/f.bin")
    s = summary(admin, name)
    assert has_recent(s, kind="copy", direction="in", path="/workspace/f.bin", size=1000), s
    back = tmp_path / "back.bin"
    remote("-d", name, "cp", f"{name}:/workspace/f.bin", str(back))
    assert back.stat().st_size == 1000
    s = summary(admin, name)
    assert has_recent(s, kind="copy", direction="out", path="/workspace/f.bin", size=1000), s


def test_forward_in_now(cli, remote, forwards, admin, name):  # noqa: F811
    f = forwards(cli, remote, "-R", f"5173:{free_port()}")
    f.wait_for("out 5173")
    s = summary(admin, name)
    assert {"kind": "forward", "text": "5173 out"} == {k: v for k, v in next(i for i in s["now"] if i["kind"] == "forward").items() if k != "since"}, s
    f.stop()


def test_revoke_removes_presence(admin, desktop, name):
    t = make_token(admin, name)
    run(admin, t, ["true"])
    assert len(admin.desktop(name)["agents"]) == 1
    assert admin.delete(f"/api/desktops/{name}/tokens/{t['id']}").status_code == 204
    assert admin.desktop(name)["agents"] == []


def test_presence_expires(admin, desktop, name):
    t = make_token(admin, name)
    run(admin, t, ["true"])
    start = time.time()
    assert len(admin.desktop(name)["agents"]) == 1
    while time.time() - start < 8:
        time.sleep(1)
    assert len(admin.desktop(name)["agents"]) == 1, "gone too early"
    while time.time() - start < 20:
        if admin.desktop(name)["agents"] == []:
            return
        time.sleep(1)
    raise AssertionError("agent still listed after 20 s")
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


def wait_inside(fn, seconds=15):
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
        agents = wait_inside(lambda: inside_agents(admin, name))
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
    # Sent before this test looks. Earlier tests may still keep the reader
    # running (it lingers after the last look), so don't assert it's unseen.
    inside_click(name)
    with events_open(admin):
        agents = wait_inside(lambda: inside_agents(admin, name))
        assert [a["id"] for a in agents] == ["inside:a terminal"]
