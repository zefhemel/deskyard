from sandbox import DEFAULT_ENV, docker, run_args


def test_user_and_home(sandbox):
    out = sandbox.exec("id -un; id -u; echo $HOME; echo $DISPLAY").stdout.split()
    assert out == ["user", "1000", "/workspace", ":1"]


def test_git_and_ssh_are_installed(sandbox):
    sandbox.exec("git --version && ssh -V")


def test_display_size(sandbox):
    assert "1280x800" in sandbox.exec("xdpyinfo -display :1 | grep dimensions").stdout


def test_resolution_env(make_sandbox):
    sb = make_sandbox(env={**DEFAULT_ENV, "WORKSPACE_RESOLUTION": "1024x768"})
    assert "1024x768" in sb.exec("xdpyinfo -display :1 | grep dimensions").stdout


def test_restart_with_stale_x_lock(sandbox):
    sandbox.restart()
    sandbox.restart()
    sandbox.exec("xdpyinfo -display :1 >/dev/null")


def test_refuses_to_start_without_token(image, volumes):
    name, argv = run_args(image, home=volumes.home, env={})
    docker(*argv)
    try:
        docker("wait", name)
        logs = docker("logs", name).stderr
        assert "only behind the Deskyard" in logs
    finally:
        docker("rm", "-f", name, check=False)


def test_root_owned_home_is_fixed(make_sandbox, volumes):
    """A new host folder may belong to root; the desktop makes it the user's."""
    docker("run", "--rm", "-v", f"{volumes.home}:/w", "alpine", "sh", "-c", "chown 0:0 /w")
    sb = make_sandbox()
    sb.exec("touch /workspace/mine")
