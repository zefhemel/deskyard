import json
import os
import subprocess
import time
import uuid

import pytest
import requests

from sandbox import REPO

DISABLED = "apps can't run here"


def compose(project, env, *args, check=True):
    return subprocess.run(["docker", "compose", "-p", project, *args], cwd=REPO, env=env,
                          capture_output=True, text=True, check=check)


@pytest.fixture
def project(image, tmp_path):
    name = f"actest{uuid.uuid4().hex[:6]}"
    env = {**os.environ, "WORKSPACE_IMAGE": image, "WORKSPACE_HOME_DIR": str(tmp_path / "workspace"), "WORKSPACE_PASSWORD": "compose-pw",
           "WORKSPACE_WEB_PORT": "18480", "WORKSPACE_VNC_PORT": "15901", "WORKSPACE_CDP_PORT": "19222"}
    yield name, env
    compose(name, env, "down", "-v", check=False)


def up(name, env):
    res = compose(name, env, "up", "-d", "--wait", "--no-build", check=False)
    assert res.returncode == 0, res.stdout + res.stderr


def run(name, env, cmd):
    return compose(name, env, "exec", "-T", "-u", "workspace", "workspace", "bash", "-lc", cmd, check=False)


def test_compose_default_has_flatpak(project):
    name, env = project
    up(name, env)
    r = requests.get("http://127.0.0.1:18480/", auth=("workspace", "compose-pw"), timeout=5,
                     allow_redirects=False)
    assert r.status_code == 200
    assert requests.get("http://127.0.0.1:19222/json/version", timeout=5).ok
    res = run(name, env, "flatpak run org.example.App")
    assert DISABLED not in res.stderr


def test_compose_flatpak_off(project):
    name, env = project
    env = {**env, "COMPOSE_FILE": "compose.yaml:compose.no-flatpak.yaml"}
    up(name, env)
    res = run(name, env, "flatpak run org.example.App")
    assert res.returncode == 1 and DISABLED in res.stderr


def test_compose_ports_bound_to_localhost(project):
    name, env = project
    up(name, env)
    ports = compose(name, env, "ps", "--format", "{{.Ports}}").stdout
    for port in ("18480", "15901", "19222"):
        assert f"127.0.0.1:{port}->" in ports, ports


def test_password_from_env_file_with_special_chars(project, tmp_path):
    name, env = project
    env = {k: v for k, v in env.items() if k != "WORKSPACE_PASSWORD"}
    password = 'pa$sw "x" y'
    example = (REPO / ".env.example").read_text()
    assert "WORKSPACE_PASSWORD=''" in example  # documented quoting
    env_file = tmp_path / "env"
    env_file.write_text(example.replace("WORKSPACE_PASSWORD=''", f"WORKSPACE_PASSWORD='{password}'"))
    res = compose(name, env, "--env-file", str(env_file), "up", "-d", "--wait", "--no-build", check=False)
    assert res.returncode == 0, res.stdout + res.stderr
    r = requests.get("http://127.0.0.1:18480/", auth=("workspace", password), timeout=5, allow_redirects=False)
    assert r.status_code == 200


def test_image_exposes_only_web_port(image):
    out = subprocess.run(["docker", "image", "inspect", "-f", "{{json .Config.ExposedPorts}}", image],
                         capture_output=True, text=True, check=True).stdout
    assert json.loads(out) == {"8080/tcp": {}}


def test_workspace_is_a_local_folder(project, tmp_path):
    name, env = project
    home = tmp_path / "home"
    env = {**env, "WORKSPACE_HOME_DIR": str(home)}
    up(name, env)
    assert run(name, env, "echo from-sandbox > ~/hello.txt").returncode == 0
    assert (home / "hello.txt").read_text().strip() == "from-sandbox"
    compose(name, env, "down", "-v")
    assert (home / "hello.txt").exists()  # `down -v` leaves the folder alone


def test_default_workspace_folder_is_next_to_compose_file():
    import yaml
    cfg = yaml.safe_load((REPO / "compose.yaml").read_text())
    assert "${WORKSPACE_HOME_DIR:-./workspace}:/workspace" in cfg["services"]["workspace"]["volumes"]


def compose_config(extra_env=None):
    env = {k: v for k, v in os.environ.items() if not k.startswith("WORKSPACE_")}
    env.update(extra_env or {})
    out = subprocess.run(["docker", "compose", "config", "--format", "json"], cwd=REPO, env=env,
                         capture_output=True, text=True, check=True).stdout
    return json.loads(out)["services"]["workspace"]


def test_compose_up_rebuilds_image_by_default():
    assert compose_config()["pull_policy"] == "build"


def test_compose_can_use_published_image():
    cfg = compose_config({"WORKSPACE_IMAGE": "ghcr.io/zefhemel/container-computer:latest",
                          "WORKSPACE_PULL_POLICY": "always"})
    assert cfg["image"] == "ghcr.io/zefhemel/container-computer:latest"
    assert cfg["pull_policy"] == "always"
