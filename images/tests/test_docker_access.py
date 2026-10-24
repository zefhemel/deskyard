"""Socket permission setup, using disposable containers and synthetic sockets."""
import os
from pathlib import Path
import subprocess
import pytest

SCRIPT = Path(__file__).resolve().parents[1] / "common/rootfs/usr/local/bin/workspace-docker-access"
IMAGE = os.environ.get("AC_IMAGE", "deskyard-debian-xfce:test")


def run_setup(command):
    return subprocess.run(["docker", "run", "--rm", "--network=none", "--entrypoint", "bash",
                           "--mount", f"type=bind,source={SCRIPT},target=/check-docker-access,readonly",
                           IMAGE, "-lc", command], capture_output=True, text=True, timeout=60)


@pytest.mark.parametrize("gid", [0, 1000, 65530])
def test_docker_access_preserves_groups_and_socket_permissions(gid):
    # This is an inert socket in the disposable container, never a host socket.
    command = f'''set -eu
before=$(id -G user)
python3 -c 'import socket,os; s=socket.socket(socket.AF_UNIX); s.bind("/var/run/docker.sock"); os.chown("/var/run/docker.sock",0,{gid}); os.chmod("/var/run/docker.sock",0o660)'
metadata=$(stat -c '%u:%g:%a' /var/run/docker.sock)
DESKYARD_DOCKER_ACCESS=1 bash /check-docker-access
for group in $before; do id -G user | tr ' ' '\\n' | grep -qx "$group"; done
runuser -u user -- sh -c 'id -G | tr " " "\\n" | grep -qx {gid}; test -w /var/run/docker.sock'
test "$metadata" = "$(stat -c '%u:%g:%a' /var/run/docker.sock)"
DESKYARD_DOCKER_ACCESS=1 bash /check-docker-access
'''
    result = run_setup(command)
    assert result.returncode == 0, result.stdout + result.stderr


def test_docker_access_requires_opt_in_and_real_socket():
    result = run_setup('''set -eu
before=$(id -G user)
bash /check-docker-access
test "$before" = "$(id -G user)"
mkdir -p /var/run/docker.sock
if DESKYARD_DOCKER_ACCESS=1 bash /check-docker-access; then exit 1; fi
test "$before" = "$(id -G user)"
''')
    assert result.returncode == 0, result.stdout + result.stderr
    assert "requires a Unix socket" in result.stderr
