"""Agent activity: the activity API, Desktop.agents and the screen-events stream."""

import threading
import time
import uuid

import requests

from test_agent import make_token


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
