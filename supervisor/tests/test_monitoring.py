"""Events, stats and who's viewing."""

import asyncio
import json
import threading
import time

import websockets


def read_events(client, until, timeout=60):
    """Collect (event, data) from /api/events until until(events) is true."""
    events = []
    with client.get("/api/events", stream=True, timeout=timeout) as r:
        kind = None
        deadline = time.time() + timeout
        for line in r.iter_lines():
            if line.startswith(b"event: "):
                kind = line[7:].decode()
            elif line.startswith(b"data: "):
                events.append((kind, json.loads(line[6:])))
                kind = None
                if until(events):
                    return events
            if time.time() > deadline:
                break
    raise AssertionError(f"events not seen: {events[-5:]}")


def test_events_list_and_stats(admin, desktop, name):
    evs = read_events(admin, lambda es: any(k == "stats" and d["name"] == name for k, d in es), timeout=90)
    assert any(k == "desktop" and d["name"] == name for k, d in evs)
    st = [d for k, d in evs if k == "stats" and d["name"] == name][-1]
    assert 0 <= st["cpuPercent"] <= 100 and st["memBytes"] > 0 and st["memLimit"] >= st["memBytes"]
    hist = admin.get(f"/api/desktops/{name}/stats").json()
    assert hist["history"] and hist["memLimit"] > 0


def test_viewers_are_tracked(admin, desktop, name):
    cookie = "; ".join(f"{k}={v}" for k, v in admin.cookies.items())
    opened = threading.Event()
    done = threading.Event()

    async def view():
        url = admin.base.replace("http", "ws") + f"/{name}/websockify"
        async with websockets.connect(url, additional_headers={"Cookie": cookie, "Origin": admin.base}, subprotocols=["binary"]):
            opened.set()
            while not done.is_set():
                await asyncio.sleep(0.2)

    t = threading.Thread(target=lambda: asyncio.run(view()))
    t.start()
    try:
        assert opened.wait(10)
        evs = read_events(admin, lambda es: any(k == "viewers" and d["name"] == name and d["viewers"] == ["admin"] for k, d in es)
                          or any(k == "desktop" and d["name"] == name and d["viewers"] == ["admin"] for k, d in es))
        assert evs
    finally:
        done.set()
        t.join(10)
    for _ in range(20):
        if admin.desktop(name)["viewers"] == []:
            break
        time.sleep(0.5)
    assert admin.desktop(name)["viewers"] == []


def test_keys_and_full_screenshot(admin, desktop, name):
    assert admin.post(f"/api/desktops/{name}/keys", json={"keys": "Escape"}).status_code == 204
    r = admin.get(f"/api/desktops/{name}/screen.png?full=1")
    assert r.status_code == 200 and r.content[:4] == b"\x89PNG" and "attachment" in r.headers["Content-Disposition"]
