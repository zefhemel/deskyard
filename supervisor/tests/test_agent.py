"""Client credentials and the remote access API (status, exec WebSocket, CDP)."""

import asyncio
import datetime
import json
import time
import uuid

import requests
import websockets
from playwright.sync_api import sync_playwright

from helpers import bearer, exec_ws, make_credential


def test_status_and_auth(admin, desktop, name):
    t = make_credential(admin, name)
    h = bearer(t)
    st = requests.get(f"{admin.base}/api/environments/{name}/status", headers=h, timeout=10).json()
    assert st["name"] == name and st["state"] == "running" and st["screen"]["width"] > 0
    r = requests.get(f"{admin.base}/api/environments/{name}/status", cookies=admin.cookies, timeout=5)
    assert r.status_code == 401 and "client credential" in r.json()["error"]
    assert requests.get(f"{admin.base}/api/environments/{name}/status", headers={"Authorization": "Bearer dyc_nope_x"}, timeout=5).status_code == 401
    # The credential reaches the rest of the API as its account.
    listed = requests.get(admin.base + "/api/environments", headers=h, timeout=5).json()["environments"]
    assert [e["name"] for e in listed] == [name]


def test_exec_as_user_with_stdin_and_exit_code(admin, desktop, name):
    t = make_credential(admin, name)
    out, err, code = asyncio.run(exec_ws(admin.base, name, t["credential"], ["sh", "-c", "id -u; cat; echo oops >&2; exit 7"], stdin=b"piped"))
    assert out == b"1000\npiped" and err == b"oops\n" and code == 7
    out, _, _ = asyncio.run(exec_ws(admin.base, name, t["credential"], ["sh", "-c", "echo ${WORKSPACE_PROXY_TOKEN:-none}"]))
    assert out == b"none\n"


def test_exec_tty(admin, desktop, name):
    t = make_credential(admin, name)
    out, _, code = asyncio.run(exec_ws(admin.base, name, t["credential"], ["sh", "-c", "tty"], tty=True))
    assert out.startswith(b"/dev/pts/") and code == 0


def test_cdp_through_the_remote_api(admin, desktop, name):
    t = make_credential(admin, name)
    h = bearer(t)
    v = requests.get(f"{admin.base}/api/environments/{name}/cdp/json/version", headers=h, timeout=90).json()
    assert v["webSocketDebuggerUrl"].startswith(admin.base.replace("http", "ws") + f"/api/environments/{name}/cdp/devtools/")
    with sync_playwright() as p:
        b = p.chromium.connect_over_cdp(v["webSocketDebuggerUrl"], headers=h)
        page = b.contexts[0].new_page()
        page.goto("data:text/html,<title>agent-cdp</title>")
        assert page.title() == "agent-cdp"
        b.close()


def test_credential_reaches_its_grants_and_is_revocable(admin, desktop, name):
    other = "o" + uuid.uuid4().hex[:5]
    admin.create(other)
    try:
        t = make_credential(admin, other)
        h = bearer(t)
        assert requests.get(f"{admin.base}/api/environments/{other}/status", headers=h, timeout=10).json()["name"] == other
        assert requests.get(f"{admin.base}/api/environments/{name}/status", headers=h, timeout=10).status_code == 404
        assert admin.delete(f"/api/accounts/{t['username']}/credentials/{t['id']}").status_code == 204
        assert requests.get(f"{admin.base}/api/environments/{other}/status", headers=h, timeout=5).status_code == 401
    finally:
        admin.delete(f"/api/environments/{other}?files=1")


def test_revoking_closes_open_exec(admin, desktop, name):
    t = make_credential(admin, name)

    async def run():
        url = admin.base.replace("http", "ws") + f"/api/environments/{name}/exec"
        async with websockets.connect(url, additional_headers=bearer(t)) as ws:
            await ws.send(json.dumps({"argv": ["sleep", "60"]}))
            await asyncio.sleep(1)
            admin.delete(f"/api/accounts/{t['username']}/credentials/{t['id']}")
            await asyncio.wait_for(ws.wait_closed(), 10)
    asyncio.run(run())


def test_expired_account_refused(admin, desktop, name):
    soon = (datetime.datetime.now(datetime.UTC) + datetime.timedelta(seconds=3)).isoformat().replace("+00:00", "Z")
    t = make_credential(admin, name, expires=soon)
    h = bearer(t)
    assert requests.get(f"{admin.base}/api/environments/{name}/status", headers=h, timeout=10).status_code == 200
    time.sleep(4)
    assert requests.get(f"{admin.base}/api/environments/{name}/status", headers=h, timeout=5).status_code == 401


def test_removing_access_closes_and_refuses(admin, desktop, name):
    t = make_credential(admin, name)
    h = bearer(t)

    async def run():
        url = admin.base.replace("http", "ws") + f"/api/environments/{name}/exec"
        async with websockets.connect(url, additional_headers=h) as ws:
            await ws.send(json.dumps({"argv": ["sleep", "60"]}))
            await asyncio.sleep(1)
            assert admin.patch(f"/api/accounts/{t['username']}", json={"grants": []}).status_code == 200
            await asyncio.wait_for(ws.wait_closed(), 10)
    asyncio.run(run())
    assert requests.get(f"{admin.base}/api/environments/{name}/status", headers=h, timeout=5).status_code == 404


def test_stopped_desktop_answers_409(admin, desktop, name):
    t = make_credential(admin, name)
    admin.post(f"/api/environments/{name}/stop")
    r = requests.get(f"{admin.base}/api/environments/{name}/cdp/json/version", headers=bearer(t), timeout=10)
    assert r.status_code == 409 and "stopped" in r.json()["error"]
    admin.post(f"/api/environments/{name}/start")
