"""Agent tokens and the agent API (exec WebSocket, CDP)."""

import asyncio
import datetime
import json
import time
import uuid

import requests
import websockets
from playwright.sync_api import sync_playwright

from helpers import exec_ws, make_account, make_token


def test_status_and_auth(admin, desktop, name):
    t = make_token(admin, name)
    assert t["remoteAdd"].startswith(f"deskyard remote add {name} ") and t["token"] in t["remoteAdd"]
    h = {"Authorization": f"Bearer {t['token']}"}
    st = requests.get(admin.base + "/api/agent/v1", headers=h, timeout=10).json()
    assert st["name"] == name and st["state"] == "running" and st["screen"]["width"] > 0
    assert requests.get(admin.base + "/api/agent/v1", cookies=admin.cookies, timeout=5).status_code == 401
    assert requests.get(admin.base + "/api/agent/v1", headers={"Authorization": "Bearer dyt_nope_x"}, timeout=5).status_code == 401
    # The token can't use the UI API.
    assert requests.get(admin.base + "/api/environments", headers=h, timeout=5).status_code == 401


def test_exec_as_user_with_stdin_and_exit_code(admin, desktop, name):
    t = make_token(admin, name)
    out, err, code = asyncio.run(exec_ws(admin.base, t["token"], ["sh", "-c", "id -u; cat; echo oops >&2; exit 7"], stdin=b"piped"))
    assert out == b"1000\npiped" and err == b"oops\n" and code == 7
    out, _, _ = asyncio.run(exec_ws(admin.base, t["token"], ["sh", "-c", "echo ${WORKSPACE_PROXY_TOKEN:-none}"]))
    assert out == b"none\n"


def test_exec_tty(admin, desktop, name):
    t = make_token(admin, name)
    out, _, code = asyncio.run(exec_ws(admin.base, t["token"], ["sh", "-c", "tty"], tty=True))
    assert out.startswith(b"/dev/pts/") and code == 0


def test_cdp_through_the_agent_api(admin, desktop, name):
    t = make_token(admin, name)
    h = {"Authorization": f"Bearer {t['token']}"}
    v = requests.get(admin.base + "/api/agent/v1/cdp/json/version", headers=h, timeout=90).json()
    assert v["webSocketDebuggerUrl"].startswith(admin.base.replace("http", "ws") + "/api/agent/v1/cdp/devtools/")
    with sync_playwright() as p:
        b = p.chromium.connect_over_cdp(v["webSocketDebuggerUrl"], headers=h)
        page = b.contexts[0].new_page()
        page.goto("data:text/html,<title>agent-cdp</title>")
        assert page.title() == "agent-cdp"
        b.close()


def test_token_is_for_one_desktop_and_revocable(admin, desktop, name):
    other = "o" + uuid.uuid4().hex[:5]
    admin.create(other)
    try:
        t = make_token(admin, other)
        h = {"Authorization": f"Bearer {t['token']}"}
        assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=10).json()["name"] == other
        assert admin.delete(f"/api/environments/{other}/tokens/{t['id']}").status_code == 204
        assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=5).status_code == 401
    finally:
        admin.delete(f"/api/environments/{other}?files=1")


def test_revoking_closes_open_exec(admin, desktop, name):
    t = make_token(admin, name)

    async def run():
        url = admin.base.replace("http", "ws") + "/api/agent/v1/exec"
        async with websockets.connect(url, additional_headers={"Authorization": f"Bearer {t['token']}"}) as ws:
            await ws.send(json.dumps({"argv": ["sleep", "60"]}))
            await asyncio.sleep(1)
            admin.delete(f"/api/environments/{name}/tokens/{t['id']}")
            await asyncio.wait_for(ws.wait_closed(), 10)
    asyncio.run(run())


def test_expired_token_refused(admin, desktop, name):
    soon = (datetime.datetime.now(datetime.UTC) + datetime.timedelta(seconds=2)).isoformat().replace("+00:00", "Z")
    t = make_token(admin, name, expires=soon)
    h = {"Authorization": f"Bearer {t['token']}"}
    assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=10).status_code == 200
    time.sleep(3)
    assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=5).status_code == 401
    listed = {x["id"]: x for x in admin.get(f"/api/environments/{name}/tokens").json()}
    assert listed[t["id"]]["expired"]


def test_regular_account_tokens(admin, desktop, name, supervisor):
    user = make_account(admin, [name])
    anna = supervisor.client().login(user, "pw")
    mine = make_token(anna, name)
    admins = make_token(admin, name)
    assert anna.delete(f"/api/environments/{name}/tokens/{admins['id']}").status_code == 403
    # Losing access revokes the tokens the account created.
    admin.put(f"/api/environments/{name}/access", json={"usernames": []})
    h = {"Authorization": f"Bearer {mine['token']}"}
    assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=5).status_code == 401
    assert requests.get(admin.base + "/api/agent/v1", headers={"Authorization": f"Bearer {admins['token']}"}, timeout=10).status_code == 200
    admin.delete(f"/api/accounts/{user}")


def test_stopped_desktop_answers_409(admin, desktop, name):
    t = make_token(admin, name)
    admin.post(f"/api/environments/{name}/stop")
    h = {"Authorization": f"Bearer {t['token']}"}
    r = requests.get(admin.base + "/api/agent/v1/cdp/json/version", headers=h, timeout=10)
    assert r.status_code == 409 and "stopped" in r.json()["error"]
    admin.post(f"/api/environments/{name}/start")
