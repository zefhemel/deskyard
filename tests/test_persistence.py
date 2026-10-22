import pytest


@pytest.mark.network
def test_installs_survive_image_upgrade(make_sandbox, next_image):
    a = make_sandbox()
    a.exec("mise use -g node@22 jq", timeout=600)
    a.exec("npm i -g cowsay", timeout=600)
    a.exec("uv tool install pycowsay", timeout=600)
    a.exec("echo hi > ~/note")
    a.remove()

    b = make_sandbox(image=next_image)
    assert b.exec("cat /etc/workspace/build-id").stdout.strip() == "next"
    assert b.exec("node --version").stdout.startswith("v22")
    b.exec("cowsay hi")
    b.exec("pycowsay hi")
    b.exec("jq --version")
    assert b.exec("cat ~/note").stdout.strip() == "hi"


@pytest.mark.network
def test_non_login_exec_finds_tools(sandbox):
    sandbox.exec("mise use -g node@22", timeout=600)
    assert sandbox.exec("node --version", login=False).stdout.startswith("v22")


def test_skeleton_seeded_once_and_not_overwritten(make_sandbox):
    a = make_sandbox()
    a.exec("test -f ~/.bashrc && test -f ~/.workspace-seeded")
    a.exec("echo '# mine' >> ~/.bashrc")
    a.restart()
    a.remove()
    b = make_sandbox()
    assert "# mine" in b.exec("cat ~/.bashrc").stdout


def test_user_bin_dirs_on_path(sandbox):
    for login in (True, False):
        path = sandbox.exec("printenv PATH", login=login).stdout
        for d in ("/workspace/.local/bin", "/workspace/.npm-global/bin", "/workspace/.local/share/mise/shims"):
            assert d in path, (login, path)


def test_preinstalled_runtimes(sandbox):
    for login in (True, False):
        assert sandbox.exec("node --version", login=login).stdout.startswith("v24."), login
        assert sandbox.exec("python --version", login=login).stdout.startswith("Python 3.13"), login
        assert sandbox.exec("python3 --version", login=login).stdout.startswith("Python 3.13"), login
    assert sandbox.exec("which node").stdout.strip() == "/opt/runtimes/node/bin/node"


@pytest.mark.network
def test_user_runtime_overrides_preinstalled(make_sandbox):
    sandbox = make_sandbox(env={"WORKSPACE_PASSWORD": "test-password", "WORKSPACE_SUDO": "0"})
    sandbox.exec("mise use -g node@22", timeout=600)
    assert sandbox.exec("node --version", login=False).stdout.startswith("v22")


@pytest.mark.network
def test_uv_tools_do_not_depend_on_image_python(sandbox):
    """uv tools must run on a Python in /workspace, not the image's (which changes on upgrade)."""
    sandbox.exec("uv tool install pycowsay", timeout=600)
    real = sandbox.exec("readlink -f ~/.local/share/uv/tools/pycowsay/bin/python").stdout.strip()
    assert real.startswith("/workspace/"), real
    cfg = sandbox.exec("cat ~/.local/share/uv/tools/pycowsay/pyvenv.cfg").stdout
    assert "/opt/" not in cfg, cfg
