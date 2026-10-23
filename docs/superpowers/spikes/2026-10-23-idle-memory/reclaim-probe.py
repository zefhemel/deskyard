"""Disposable OrbStack/cgroup-v2 diagnostic; no production desktop changes.

From the repository root, outside the sandbox:
uv run --project images/tests python \
  docs/superpowers/spikes/2026-10-23-idle-memory/reclaim-probe.py

Requires local deskyard-debian-xfce:test and python:3.13-alpine images.
The temporary privileged helper has no network and writes only the fresh
desktop's memory.reclaim. Its host PID/cgroup access is diagnostic, not a
proposed production security boundary. Containers and volume are removed.
"""

import json
from pathlib import Path
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "images/tests"))
from sandbox import TOKEN_HEADER, docker, start_sandbox
from playwright.sync_api import sync_playwright

suffix = uuid.uuid4().hex[:8]
name = f"deskyard-reclaim-probe-{suffix}"
helper = name + "-helper"
volume = name + "-home"
result = {"desktop": name, "rounds": []}


def emit(label, value):
    print(json.dumps({label: value}), flush=True)


def host(code):
    return json.loads(docker("exec", "-i", helper, "python", "-", input=code).stdout)


def snapshot():
    return host(f"""
import json
from pathlib import Path
p = Path({cgroup!r})
out = {{}}
for key in ['memory.current', 'memory.swap.current', 'memory.stat', 'memory.events']:
    text = (p / key).read_text()
    out[key] = int(text) if key.endswith('current') else dict(
        (k, int(v)) for k, v in (line.split() for line in text.splitlines()))
out['zram_mm_stat'] = Path('/sys/block/zram0/mm_stat').read_text().strip()
print(json.dumps(out))
""")


def browser_check(p):
    start = time.monotonic()
    browser = p.chromium.connect_over_cdp(sb.cdp_ws(), headers=TOKEN_HEADER, timeout=30_000)
    page = next(page for page in browser.contexts[0].pages if page.url.startswith('data:text/html'))
    state = page.evaluate("({nonce: window.probeNonce, text: document.querySelector('textarea').value, ticks: window.probeTicks})")
    first_ms = (time.monotonic() - start) * 1000
    assert state['nonce'] == nonce and state['text'] == 'unsaved text survives reclaim', state
    start = time.monotonic()
    page.locator('textarea').fill('edited after reclaim')
    assert page.locator('textarea').input_value() == 'edited after reclaim'
    page.locator('textarea').fill('unsaved text survives reclaim')
    edit_ms = (time.monotonic() - start) * 1000
    start = time.monotonic()
    png = page.screenshot(timeout=30_000)
    screenshot_ms = (time.monotonic() - start) * 1000
    assert png.startswith(b'\x89PNG')
    browser.close()  # CDP disconnect; Chromium remains running.
    return dict(state=state, reconnect_and_read_ms=first_ms, edit_and_restore_ms=edit_ms,
                screenshot_ms=screenshot_ms)


sb = None
try:
    result['engine'] = json.loads(docker('info', '--format', '{{json .}}').stdout)
    # Keep only non-personal platform information in the saved evidence.
    result['engine'] = {k: result['engine'][k] for k in
                        ['ServerVersion', 'KernelVersion', 'CgroupVersion', 'MemoryLimit', 'SwapLimit']}
    result['image'] = docker('image', 'inspect', 'deskyard-debian-xfce:test', '--format', '{{.Id}}').stdout.strip()
    sb = start_sandbox('deskyard-debian-xfce:test', home=volume, name=name, wait=False)
    sb.wait_ready()
    cid = docker('inspect', name, '--format', '{{.Id}}').stdout.strip()
    pid = int(docker('inspect', name, '--format', '{{.State.Pid}}').stdout)
    docker('run', '-d', '--name', helper, '--network=none', '--privileged',
           '--pid=host', '--cgroupns=host', '-v', '/sys/fs/cgroup:/host-cgroup:rw',
           'python:3.13-alpine', 'sleep', '600')
    cgroup = host(f"""
import json
from pathlib import Path
line = Path('/proc/{pid}/cgroup').read_text().strip()
assert line.startswith('0::'), line
p = (Path('/host-cgroup') / line[3:].lstrip('/')).resolve()
assert p.is_relative_to('/host-cgroup') and {cid!r} in str(p), str(p)
assert (p / 'memory.reclaim').exists()
print(json.dumps(str(p)))
""")
    result['swaps'] = sb.exec('cat /proc/swaps').stdout
    result['cgroup'] = cgroup
    emit('platform', {k: result[k] for k in ['engine', 'image', 'swaps', 'cgroup']})
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(sb.cdp_ws(), headers=TOKEN_HEADER, timeout=60_000)
        page = browser.contexts[0].new_page()
        page.goto('data:text/html,<title>reclaim probe</title><h1>Memory reclamation probe</h1><textarea></textarea>')
        nonce = uuid.uuid4().hex
        page.evaluate("nonce => { window.probeNonce = nonce; window.probeTicks = 0; setInterval(() => window.probeTicks++, 1000); }", nonce)
        page.locator('textarea').fill('unsaved text survives reclaim')
        browser.close()
        time.sleep(20)
        result['baseline_response'] = browser_check(p)
        emit('baseline_response', result['baseline_response'])
        for mib in [128, 256]:
            time.sleep(15)
            before = snapshot()
            reclaim = host(f"""
import errno, json, time
start = time.monotonic()
try:
    with open({cgroup!r} + '/memory.reclaim', 'w') as f:
        f.write('{mib}M swappiness=100')
    error = None
except OSError as e:
    error = {{'errno': e.errno, 'message': str(e)}}
print(json.dumps({{'error': error, 'elapsed_ms': (time.monotonic()-start)*1000}}))
""")
            immediate = snapshot()
            time.sleep(5)
            idle = snapshot()
            response = browser_check(p)
            awake = snapshot()
            round_result = dict(requested_mib=mib, before=before, reclaim=reclaim,
                                immediate=immediate, after_5s=idle, response=response, awake=awake)
            result['rounds'].append(round_result)
            emit('round', dict(requested_mib=mib, reclaim=reclaim, response=response,
                               before_bytes=before['memory.current'],
                               idle_bytes=idle['memory.current'],
                               awake_bytes=awake['memory.current'],
                               swap_bytes=idle['memory.swap.current']))
        assert all(r['reclaim']['error'] is None for r in result['rounds'])
        assert all(r['after_5s']['memory.current'] < r['before']['memory.current'] for r in result['rounds'])
        assert all(r['after_5s']['memory.swap.current'] > r['before']['memory.swap.current'] for r in result['rounds'])
        assert all(r['awake']['memory.events']['oom_kill'] == 0 for r in result['rounds'])
        assert result['rounds'][-1]['response']['state']['ticks'] > result['baseline_response']['state']['ticks']
    result['result'] = 'PASS'
finally:
    if sb:
        sb.remove()
    docker('rm', '-f', '-v', helper, check=False)
    docker('volume', 'rm', volume, check=False)
    result['cleanup'] = {
        'desktop_removed': docker('inspect', name, check=False).returncode != 0,
        'helper_removed': docker('inspect', helper, check=False).returncode != 0,
        'volume_removed': docker('volume', 'inspect', volume, check=False).returncode != 0,
    }
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else Path('/tmp/deskyard-reclaim-result.json')
    output.write_text(json.dumps(result, indent=2) + '\n')
    emit('result_file', str(output))
    emit('cleanup', result['cleanup'])
    assert all(result['cleanup'].values()), result['cleanup']
