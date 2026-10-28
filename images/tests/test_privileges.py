"""Root inside a desktop can write host kernel settings (Flatpak support), so no
agent-reachable or internet-facing process may run as (or become) root."""

import time
from sandbox import PANEL


def test_no_sudo(sandbox):
    assert sandbox.exec("command -v sudo", check=False).returncode != 0


def test_caddy_not_root(sandbox):
    users = sandbox.exec("ps -C caddy -o user= | sort -u", user="root").stdout.split()
    assert users == ["caddy"], users


def test_desktop_does_not_start_extra_root_services(sandbox):
    time.sleep(3)
    # Inspect from the outer namespace, where container root is mapped.
    commands = set(sandbox.exec("ps -u root -o comm=", join_session=False).stdout.split())
    assert commands <= {"supervisord", "dbus-daemon"}, commands


def test_token_not_visible_to_the_user(sandbox):
    """The desktop's processes don't carry the supervisor's token, and its file is Caddy's alone.
    (The engine's exec gets the container's environment; the supervisor overrides it there.)"""
    panel = sandbox.exec(f"pgrep -o -x {PANEL}").stdout.strip()
    assert "test-token" not in sandbox.exec(f"tr '\\0' '\\n' < /proc/{panel}/environ").stdout
    assert sandbox.exec("cat /run/caddy/token", check=False).returncode != 0


def test_only_8080_on_the_network(sandbox):
    """Nothing but Caddy listens beyond localhost (not Selkies on 6080, not CDP on 9222)."""
    out = sandbox.exec("awk 'FNR>1 && $4==\"0A\" {print $2}' /proc/net/tcp /proc/net/tcp6").stdout.split()
    loopback = {"0100007F", "00000000000000000000000001000000"}
    ports = {int(a.split(":")[1], 16) for a in out if a.split(":")[0] not in loopback}
    assert ports == {8080}, ports


def test_root_never_runs_user_binaries(make_sandbox):
    """A binary planted in the user's bin dirs must not run as root (healthcheck, init)."""
    sandbox = make_sandbox(docker_args=["--health-interval=2s"])
    names = ["curl", "xdpyinfo", "caddy", "chown", "install", "rm", "runuser", "flatpak", "dbus-uuidgen"]
    script = "#!/bin/sh\\n[ \"$(id -u)\" = 0 ] && touch /tmp/pwned-$(basename $0)\\nPATH=/usr/local/bin:/usr/bin:/bin exec $(basename $0) \"$@\"\\n"
    for d in ("~/.local/bin", "~/.npm-global/bin"):
        sandbox.exec(f"mkdir -p {d}")
        for n in names:
            sandbox.exec(f"printf '{script}' > {d}/{n} && chmod +x {d}/{n}")
    time.sleep(5)  # > two healthcheck intervals
    sandbox.restart()
    time.sleep(5)
    assert sandbox.exec("ls /tmp/pwned-* 2>/dev/null", check=False).stdout.strip() == ""
