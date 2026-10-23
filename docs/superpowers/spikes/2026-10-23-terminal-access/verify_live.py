"""Verify a running demo through its real local CLI PTY, not Docker exec.

Run with the supervisor test Python environment and the demo's access.json.
A browser must first set BROWSER_NONCE=browser-session-alive in the shared shell.
"""
import fcntl
import configparser
import shlex
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import sys
import termios
import time

state = json.loads(Path(sys.argv[1]).read_text())
env = {**{k: v for k, v in os.environ.items() if not k.startswith('DESKYARD_')},
       **state['cli_env'], 'TERM': 'xterm-256color'}
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 100, 0, 0))
proc = subprocess.Popen([state['cli'], 'exec', '--tty', '--', 'shared-terminal', 'main'],
                        stdin=slave, stdout=slave, stderr=slave, env=env)
os.close(slave)
output = bytearray()


def drain(seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if select.select([master], [], [], 0.1)[0]:
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            output.extend(data)
            # Answer the terminal identity queries made by the native Zellij client.
            if b'\x1b[c' in data:
                os.write(master, b'\x1b[?1;2c')
            if b'\x1b[6n' in data:
                os.write(master, b'\x1b[1;1R')


try:
    drain(2)
    os.write(master, b'''printf '\\nCLI_SEES:%s\\n' "$BROWSER_NONCE"; export CLI_NONCE=cli-session-alive\r''')
    drain(2)
    assert b'CLI_SEES:browser-session-alive' in output, 'CLI did not see browser shell memory'
    print('PASS: local CLI attached to the browser shell and read its environment variable')
    # Normal Zellij detach: unlock (Ctrl-g), session mode (Ctrl-o), d.
    os.write(master, b'\x07\x0fd')
    drain(1)
    proc.wait(timeout=5)
    assert proc.returncode == 0, f'CLI detach exited with {proc.returncode}'
    print('PASS: native CLI detached naturally with exit status 0')
finally:
    proc.terminate() if proc.poll() is None else None
    proc.wait(timeout=10)
    os.close(master)


def remote(*argv):
    return subprocess.run([state['cli'], 'exec', '--', *argv], env=env,
                          capture_output=True, text=True, check=True, timeout=20).stdout


panes = json.loads(remote('zellij', '--session', 'main', 'action', 'list-panes', '--json'))
pane = 'terminal_' + str(next(p['id'] for p in panes if not p['is_plugin']))
# Start a job in the same shell after the CLI client detached.
remote('rm', '-f', '/workspace/continuity-proof')
remote('zellij', '--session', 'main', 'action', 'write-chars', '--pane-id', pane,
       'sleep 2; printf "%s:%s\\n" "$BROWSER_NONCE" "$CLI_NONCE" > /workspace/continuity-proof')
remote('zellij', '--session', 'main', 'action', 'write', '--pane-id', pane, '13')
# The regular XFCE application launcher must create a native client of the same session.
entry = configparser.ConfigParser(interpolation=None)
entry.read_string(remote('cat', '/usr/local/share/applications/shared-terminal.desktop'))
remote('setsid', '-f', *shlex.split(entry['Desktop Entry']['Exec']))
for _ in range(30):
    clients = remote('zellij', '--session', 'main', 'action', 'list-clients')
    if len(clients.strip().splitlines()) >= 3:
        break
    time.sleep(0.2)
assert len(clients.strip().splitlines()) >= 3, 'Expected browser and XFCE clients'
print('PASS: the XFCE application launcher attached another client to main')
for _ in range(30):
    result = subprocess.run([state['cli'], 'exec', '--', 'cat', '/workspace/continuity-proof'],
                            env=env, capture_output=True, text=True, timeout=10)
    if result.stdout.strip() == 'browser-session-alive:cli-session-alive':
        break
    time.sleep(0.2)
assert result.stdout.strip() == 'browser-session-alive:cli-session-alive', result.stdout
print('PASS: the same shell retained both browser and CLI variables and completed its job')
