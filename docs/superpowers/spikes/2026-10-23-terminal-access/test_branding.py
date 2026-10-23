"""Check the presentation served by a running demo; pass its private access.json."""
import base64
import hashlib
import json
import os
import subprocess
from pathlib import Path
import re
import sys
import urllib.request

state = json.loads(Path(sys.argv[1]).read_text())
base = state["terminal"].rsplit("/", 1)[0]

def get(path):
    with urllib.request.urlopen(base + path, timeout=10) as response:
        return response.read()

with urllib.request.urlopen(base + "/", timeout=10) as response:
    assert response.geturl() == base + "/main", "Root must enter the shared terminal, not an engine welcome screen"
page = get("/main").decode()
assert "Zellij" not in page and "Deskyard Terminal" in page, "Browser page still carries engine branding"
manifest = json.loads(get("/assets/manifest.webmanifest"))
assert manifest["name"] == "Deskyard Terminal" and manifest["short_name"] == "Deskyard"
assert "zellij" not in json.dumps(manifest).lower()
assert b"<svg" in get("/assets/deskyard.svg")
app = get("/assets/app.js")
assert b"Hi from Zellij!" not in app and b'document.title = "Zellij"' not in app
expected = "sha384-" + base64.b64encode(hashlib.sha384(app).digest()).decode()
assert re.search(r'assets/app.js" integrity="' + re.escape(expected) + '"', page), "Patched script integrity does not match"
# Auth boundaries must still be enforced through the presentation proxy.
try:
    get("/session-list")
except urllib.error.HTTPError as error:
    assert error.code == 401, error.code
else:
    raise AssertionError("Session API became accessible without authentication")
print("PASS: Deskyard title, icons, manifest, welcome text, script integrity and authentication boundary")

probe = """from pathlib import Path
listeners = []
for path in [Path('/proc/net/tcp'), Path('/proc/net/tcp6')]:
    for line in path.read_text().splitlines()[1:]:
        fields = line.split()
        if fields[3] == '0A' and fields[1].endswith(':1F92'):
            listeners.append(fields[1])
assert listeners and set(listeners) == {'0100007F:1F92'}, listeners
print('PASS: presentation listener is IPv4 loopback only')
"""
result = subprocess.run([state['cli'], 'exec', '--', 'python3', '-c', probe],
                        env={**os.environ, **state['cli_env']}, capture_output=True,
                        text=True, timeout=20)
assert result.returncode == 0, result.stderr
print(result.stdout.strip())
