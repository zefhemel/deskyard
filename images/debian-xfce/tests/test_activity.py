import json
import time

import requests

PROBE = (
    "printf '%s' '{\"agent\":\"probe\",\"kind\":\"screen\","
    "\"screen\":{\"action\":\"click\",\"pos\":[1,2]}}' > /dev/udp/127.0.0.1/6092"
)


def test_activity_stream_behind_token(sandbox):
    r = requests.get(sandbox.web_url + "/_activity/events?after=0", timeout=5)
    assert r.status_code == 401


def test_activity_stream_receives_events(sandbox):
    # The service starts with the desktop; give it a moment, then send.
    deadline = time.time() + 30
    while sandbox.exec("grep -qi :17CC /proc/net/udp", check=False).returncode != 0:
        assert time.time() < deadline, "activity-serve never listened"
        time.sleep(0.5)
    sandbox.exec(PROBE)
    with sandbox.get("/_activity/events?after=0", stream=True, timeout=10) as r:
        assert r.status_code == 200
        assert r.headers["X-Activity-Epoch"]
        line = next(l for l in r.iter_lines() if l)
    e = json.loads(line)
    assert e["agent"] == "probe"
    assert e["screen"]["pos"] == [1, 2]
    assert e["seq"] == 1


def test_activity_process_runs_as_user(sandbox):
    out = sandbox.exec("ps -o user=,args= -C deskyard").stdout
    users = [l.split()[0] for l in out.splitlines() if "activity-serve" in l]
    assert users == ["user"], out
