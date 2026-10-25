"""Throwaway diagnostic: real Docker suspend/resume, not a product feature.

Uses host networking only for a counter process with no listening sockets.
Run outside the sandbox against an engine with experimental checkpoint support.
"""
import subprocess, time, json, uuid
name = 'deskyard-criu-probe-' + uuid.uuid4().hex[:8]
def docker(*args):
    return subprocess.check_output(['docker', *args], text=True).strip()
def count():
    return int(docker('logs', '--tail', '1', name))
try:
    docker('run', '-d', '--name', name, '--network=host', '--restart=no',
           'alpine:3.22', 'sh', '-c',
           'n=0; while true; do echo "$n"; n=$((n+1)); sleep 1; done')
    deadline = time.monotonic() + 10
    while not docker('logs', '--tail', '1', name):
        assert time.monotonic() < deadline, 'counter did not start'
        time.sleep(.2)
    # Advance beyond zero so a cold restart cannot satisfy the restore check.
    while count() < 2:
        assert time.monotonic() < deadline, 'counter did not advance'
        time.sleep(.2)
    before = count()
    docker('checkpoint', 'create', name, 'verified-suspend')
    assert docker('inspect', '-f', '{{.State.Status}}', name) == 'exited'
    frozen = count()
    frozen_log = docker('logs', name).splitlines()
    time.sleep(3)
    assert count() == frozen
    docker('start', '--checkpoint', 'verified-suspend', name)
    deadline = time.monotonic() + 10
    while count() <= frozen:
        assert time.monotonic() < deadline, 'counter did not resume'
        time.sleep(.2)
    after = count()
    restored_log = docker('logs', name).splitlines()
    assert restored_log[:len(frozen_log)] == frozen_log
    assert int(restored_log[len(frozen_log)]) == frozen + 1, 'counter restarted'
    assert after == frozen + 1, 'counter did not continue at the next value'
    assert after > frozen >= before
    assert docker('inspect', '-f', '{{.State.Status}}', name) == 'running'
    print(json.dumps(dict(before=before, suspended=frozen, after=after,
                         frozen_seconds=3, result='PASS')))
finally:
    subprocess.run(['docker', 'rm', '-f', '-v', name],
                   stdout=subprocess.DEVNULL, check=False)
