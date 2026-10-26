"""Events, stats and who's viewing. (One viewer: test_screen.test_stream_shows_the_desktop_and_counts_as_viewing.)"""

import asyncio
import threading

import websockets

from helpers import make_account, read_events, wait_until


def test_events_list_and_stats(admin, desktop, name):
    evs = read_events(admin, lambda es: any(k == "stats" and d["name"] == name for k, d in es), timeout=90)
    assert any(k == "environment" and d["name"] == name for k, d in evs)
    st = [d for k, d in evs if k == "stats" and d["name"] == name][-1]
    assert 0 <= st["cpuPercent"] <= 100 and st["memBytes"] > 0 and st["memLimit"] >= st["memBytes"]
    hist = admin.get(f"/api/environments/{name}/stats").json()
    assert hist["history"] and hist["memLimit"] > 0


def test_two_viewers_see_each_other(admin, desktop, name, supervisor):
    user = make_account(admin, [name])
    anna = supervisor.client().login(user, "pw")
    opened = threading.Barrier(3)
    done = threading.Event()

    def view(client):
        async def run():
            cookie = "; ".join(f"{k}={v}" for k, v in client.cookies.items())
            url = client.base.replace("http", "ws") + f"/{name}/screen/api/websockets"
            async with websockets.connect(url, additional_headers={"Cookie": cookie, "Origin": client.base}, subprotocols=["binary"]):
                opened.wait(10)
                while not done.is_set():
                    await asyncio.sleep(0.2)
        asyncio.run(run())

    threads = [threading.Thread(target=view, args=(c,)) for c in (admin, anna)]
    for t in threads:
        t.start()
    try:
        opened.wait(10)
        both = sorted(["admin", user])
        # Each sees the other, on the desktop and in the event stream.
        for client in (admin, anna):
            assert wait_until(lambda: sorted(client.desktop(name)["viewers"]) == both, 10, 0.5), client.desktop(name)["viewers"]
        assert read_events(anna, lambda es: any(k == "environment" and d["name"] == name and sorted(d["viewers"]) == both for k, d in es))
    finally:
        done.set()
        for t in threads:
            t.join(10)
        admin.delete(f"/api/accounts/{user}")


def test_keys_and_full_screenshot(admin, desktop, name):
    assert admin.post(f"/api/environments/{name}/keys", json={"keys": "Escape"}).status_code == 204
    r = admin.get(f"/api/environments/{name}/screen.png?full=1")
    assert r.status_code == 200 and r.content[:4] == b"\x89PNG" and "attachment" in r.headers["Content-Disposition"]
