"""Agent tokens and the agent API (exec WebSocket, CDP)."""

import asyncio
import datetime
import json
import time
import uuid

import requests
import websockets
from playwright.sync_api import sync_playwright


def make_token(client, name, **kw) -> dict:
    r = client.post(f"/api/computers/{name}/tokens", json={"name": "test", "expires": None, **kw})
    assert r.status_code == 201, r.text
    return r.json()


async def exec_ws(base, token, argv, stdin=b"", tty=False):
    url = base.replace("http", "ws") + "/api/agent/v1/exec"
    out, err, code = b"", b"", None
    async with websockets.connect(url, additional_headers={"Authorization": f"Bearer {token}"}, max_size=None) as ws:
        await ws.send(json.dumps({"argv": argv, "tty": tty}))
        if stdin:
            await ws.send(b"\x00" + stdin)
        await ws.send(json.dumps({"eof": True}))
        async for msg in ws:
            if isinstance(msg, bytes):
                if msg[0] == 1:
                    out += msg[1:]
                elif msg[0] == 2:
                    err += msg[1:]
            else:
                m = json.loads(msg)
                if "exit" in m:
                    code = m["exit"]
                elif "error" in m:
                    raise AssertionError(m["error"])
    return out, err, code


def test_status_and_auth(admin, computer, name):
    t = make_token(admin, name)
    assert t["remoteAdd"].startswith(f"computer remote add {name} ") and t["token"] in t["remoteAdd"]
    h = {"Authorization": f"Bearer {t['token']}"}
    st = requests.get(admin.base + "/api/agent/v1", headers=h, timeout=10).json()
    assert st["name"] == name and st["state"] == "running" and st["screen"]["width"] > 0
    assert requests.get(admin.base + "/api/agent/v1", cookies=admin.cookies, timeout=5).status_code == 401
    assert requests.get(admin.base + "/api/agent/v1", headers={"Authorization": "Bearer cct_nope_x"}, timeout=5).status_code == 401
    # The token can't use the UI API.
    assert requests.get(admin.base + "/api/computers", headers=h, timeout=5).status_code == 401


def test_exec_as_user_with_stdin_and_exit_code(admin, computer, name):
    t = make_token(admin, name)
    out, err, code = asyncio.run(exec_ws(admin.base, t["token"], ["sh", "-c", "id -u; cat; echo oops >&2; exit 7"], stdin=b"piped"))
    assert out == b"1000\npiped" and err == b"oops\n" and code == 7
    out, _, _ = asyncio.run(exec_ws(admin.base, t["token"], ["sh", "-c", "echo ${WORKSPACE_PROXY_TOKEN:-none}"]))
    assert out == b"none\n"


def test_exec_tty(admin, computer, name):
    t = make_token(admin, name)
    out, _, code = asyncio.run(exec_ws(admin.base, t["token"], ["sh", "-c", "tty"], tty=True))
    assert out.startswith(b"/dev/pts/") and code == 0


def test_cdp_through_the_agent_api(admin, computer, name):
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


def test_token_is_for_one_computer_and_revocable(admin, computer, name):
    other = "o" + uuid.uuid4().hex[:5]
    admin.create(other)
    try:
        t = make_token(admin, other)
        h = {"Authorization": f"Bearer {t['token']}"}
        assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=10).json()["name"] == other
        assert admin.delete(f"/api/computers/{other}/tokens/{t['id']}").status_code == 204
        assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=5).status_code == 401
    finally:
        admin.delete(f"/api/computers/{other}?files=1")


def test_revoking_closes_open_exec(admin, computer, name):
    t = make_token(admin, name)

    async def run():
        url = admin.base.replace("http", "ws") + "/api/agent/v1/exec"
        async with websockets.connect(url, additional_headers={"Authorization": f"Bearer {t['token']}"}) as ws:
            await ws.send(json.dumps({"argv": ["sleep", "60"]}))
            await asyncio.sleep(1)
            admin.delete(f"/api/computers/{name}/tokens/{t['id']}")
            await asyncio.wait_for(ws.wait_closed(), 10)
    asyncio.run(run())


def test_expired_token_refused(admin, computer, name):
    soon = (datetime.datetime.now(datetime.UTC) + datetime.timedelta(seconds=2)).isoformat().replace("+00:00", "Z")
    t = make_token(admin, name, expires=soon)
    h = {"Authorization": f"Bearer {t['token']}"}
    assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=10).status_code == 200
    time.sleep(3)
    assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=5).status_code == 401
    listed = {x["id"]: x for x in admin.get(f"/api/computers/{name}/tokens").json()}
    assert listed[t["id"]]["expired"]


def test_regular_account_tokens(admin, computer, name, supervisor):
    user = "u" + uuid.uuid4().hex[:5]
    admin.post("/api/accounts", json={"username": user, "password": "pw", "admin": False, "computers": [name]})
    anna = supervisor.client().login(user, "pw")
    mine = make_token(anna, name)
    admins = make_token(admin, name)
    assert anna.delete(f"/api/computers/{name}/tokens/{admins['id']}").status_code == 403
    # Losing access revokes the tokens the account created.
    admin.put(f"/api/computers/{name}/access", json={"usernames": []})
    h = {"Authorization": f"Bearer {mine['token']}"}
    assert requests.get(admin.base + "/api/agent/v1", headers=h, timeout=5).status_code == 401
    assert requests.get(admin.base + "/api/agent/v1", headers={"Authorization": f"Bearer {admins['token']}"}, timeout=10).status_code == 200
    admin.delete(f"/api/accounts/{user}")


def test_stopped_computer_answers_409(admin, computer, name):
    t = make_token(admin, name)
    admin.post(f"/api/computers/{name}/stop")
    h = {"Authorization": f"Bearer {t['token']}"}
    r = requests.get(admin.base + "/api/agent/v1/cdp/json/version", headers=h, timeout=10)
    assert r.status_code == 409 and "stopped" in r.json()["error"]
    admin.post(f"/api/computers/{name}/start")
