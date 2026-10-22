import socket

from sandbox import Sandbox


def test_user_and_home(sandbox):
    out = sandbox.exec("id -un; echo $HOME; echo $DISPLAY").stdout.split()
    assert out == ["workspace", "/workspace", ":1"]


def test_xvnc_listening(sandbox):
    dims = sandbox.exec("xdpyinfo -display :1 | grep dimensions").stdout
    assert "1280x800" in dims
    with socket.create_connection(("127.0.0.1", sandbox.port(5901)), timeout=5) as s:
        assert s.recv(12).startswith(b"RFB 003.")


def test_resolution_env(make_sandbox):
    sb = make_sandbox(env={"WORKSPACE_PASSWORD": "test-password", "WORKSPACE_RESOLUTION": "1024x768"})
    assert "1024x768" in sb.exec("xdpyinfo -display :1 | grep dimensions").stdout


def test_restart_with_stale_x_lock(sandbox):
    sandbox.restart()
    sandbox.restart()
    sandbox.exec("xdpyinfo -display :1 >/dev/null")


def test_puid_change_keeps_home_writable(make_sandbox):
    a = make_sandbox(env={"WORKSPACE_PASSWORD": "test-password", "PUID": "1000", "PGID": "1000"})
    a.exec("echo hi > /workspace/a")
    a.remove()
    b = make_sandbox(env={"WORKSPACE_PASSWORD": "test-password", "PUID": "1001", "PGID": "1001"})
    b.exec("touch /workspace/a /workspace/b")
    assert b.exec("id -u").stdout.strip() == "1001"


def test_passwordless_sudo_without_flatpak(make_sandbox):
    sandbox = make_sandbox(flatpak=False)
    sandbox.exec("sudo -n true")
