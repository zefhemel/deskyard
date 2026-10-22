"""Root inside a Flatpak-mode container can write host kernel settings, so no
agent-reachable or internet-facing process may run as (or become) root there."""

import time


def test_no_sudo_in_flatpak_mode(sandbox):
    assert sandbox.exec("sudo -n true", check=False).returncode != 0


def test_sudo_opt_in_in_flatpak_mode(make_sandbox):
    sb = make_sandbox(env={"WORKSPACE_PASSWORD": "test-password", "WORKSPACE_SUDO": "1"})
    sb.exec("sudo -n true")


def test_sudo_without_flatpak_mode(make_sandbox):
    make_sandbox(flatpak=False).exec("sudo -n true")


def test_sudo_forced_off(make_sandbox):
    sb = make_sandbox(flatpak=False, env={"WORKSPACE_PASSWORD": "test-password", "WORKSPACE_SUDO": "0"})
    assert sb.exec("sudo -n true", check=False).returncode != 0


def test_caddy_not_root(sandbox):
    users = sandbox.exec("ps -C caddy -o user= | sort -u", user="root").stdout.split()
    assert users and "root" not in users, users


def test_root_never_runs_user_binaries(sandbox):
    """A binary planted in the user's bin dirs must not run as root (healthcheck, init)."""
    names = ["curl", "xdpyinfo", "caddy", "chown", "install", "rm", "runuser", "flatpak", "dbus-uuidgen"]
    script = "#!/bin/sh\\n[ \"$(id -u)\" = 0 ] && touch /tmp/pwned-$(basename $0)\\nPATH=/usr/local/bin:/usr/bin:/bin exec $(basename $0) \"$@\"\\n"
    for d in ("~/.local/bin", "~/.npm-global/bin"):
        sandbox.exec(f"mkdir -p {d}")
        for n in names:
            sandbox.exec(f"printf '{script}' > {d}/{n} && chmod +x {d}/{n}")
    time.sleep(25)  # > two healthcheck intervals
    sandbox.restart()
    time.sleep(12)
    assert sandbox.exec("ls /tmp/pwned-* 2>/dev/null", check=False).stdout.strip() == ""
