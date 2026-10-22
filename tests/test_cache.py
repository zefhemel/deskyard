"""Re-downloadable data lives on a separate cache volume, not in the home folder."""

CACHE = "/var/cache/workspace"
LINKED = [".cache", ".npm", ".codex/cache", ".codex/.tmp",
          ".config/chromium-shared/component_crx_cache", ".config/chromium-shared/WasmTtsEngine"]


def test_cache_env(sandbox):
    assert sandbox.exec("printenv XDG_CACHE_HOME", login=False).stdout.strip() == f"{CACHE}/cache"
    assert sandbox.exec("npm config get cache").stdout.strip() == f"{CACHE}/npm"


def test_cache_paths_link_into_cache_volume(sandbox):
    for rel in LINKED:
        assert sandbox.exec(f"readlink ~/{rel}").stdout.strip() == f"{CACHE}/{rel.lstrip('.')}", rel
        sandbox.exec(f"touch ~/{rel}/probe && test -f {CACHE}/{rel.lstrip('.')}/probe")


def test_existing_cache_dir_is_moved_not_lost(sandbox):
    sandbox.exec("rm ~/.codex/cache && mkdir -p ~/.codex/cache/sub && echo keep > ~/.codex/cache/sub/f")
    sandbox.restart()
    assert sandbox.exec("readlink ~/.codex/cache").stdout.strip() == f"{CACHE}/codex/cache"
    assert sandbox.exec("cat ~/.codex/cache/sub/f").stdout.strip() == "keep"


def test_losing_the_cache_volume_is_harmless(make_sandbox):
    a = make_sandbox()
    a.exec("echo x > ~/.cache/probe && echo data > ~/real-file")
    a.remove()  # its anonymous cache volume goes with it
    b = make_sandbox()  # waits for desktop + Chromium
    assert b.exec("cat ~/real-file").stdout.strip() == "data"
    assert b.exec("test -e ~/.cache/probe", check=False).returncode != 0
    assert b.exec("readlink ~/.cache").stdout.strip() == f"{CACHE}/cache"


def test_tool_downloads_go_to_cache(sandbox):
    assert sandbox.exec("uv cache dir").stdout.strip().startswith(CACHE)
    assert sandbox.exec("mise cache path").stdout.strip().startswith(CACHE)


import time

import pytest
import requests

from sandbox import docker


def test_restart_keeps_cache(sandbox):
    sandbox.exec("echo x > ~/.cache/probe")
    sandbox.restart()
    assert sandbox.exec("cat ~/.cache/probe").stdout.strip() == "x"


@pytest.mark.network
def test_fresh_cache_after_real_use(make_sandbox, artifacts_dir):
    """Use tools that fill every cache, then come back with an empty cache volume."""
    a = make_sandbox()
    a.exec("mise use -g jq && npm i -g cowsay && uv tool install pycowsay", timeout=900)
    a.exec("echo '{}' > ~/.codex/cache/catalog.json && echo t > ~/.codex/.tmp/t")
    # Let Chromium populate its profile and component folders.
    time.sleep(5)
    assert a.exec(f"du -s {CACHE} | cut -f1").stdout.strip() != "0"
    a.remove()

    b = make_sandbox()  # new container, new empty cache volume, same home
    # Nothing from the old cache came along (Chromium refills its part right away).
    b.exec("test ! -e ~/.codex/cache/catalog.json && test ! -e ~/.codex/.tmp/t")
    for rel in LINKED:
        b.exec(f"test -d ~/{rel}")  # links resolve to fresh, empty folders
    b.exec("jq --version && cowsay hi && pycowsay hi")  # installs live in /workspace
    b.exec("npm i -g left-pad && uv tool install ruff && mise use -g yq", timeout=900)  # caches refill
    assert requests.get(b.cdp_url + "/json/version", timeout=5).ok
    (artifacts_dir / "after-fresh-cache.png").write_bytes(b.exec_bytes("computer screenshot"))


def test_compose_with_cache_volume_removed(image, tmp_path):
    import os
    import subprocess
    import uuid
    from sandbox import REPO
    name = f"actest{uuid.uuid4().hex[:6]}"
    env = {**os.environ, "WORKSPACE_IMAGE": image, "WORKSPACE_PASSWORD": "pw",
           "WORKSPACE_HOME_DIR": str(tmp_path / "home"), "WORKSPACE_WEB_PORT": "18780",
           "WORKSPACE_VNC_PORT": "15921", "WORKSPACE_CDP_PORT": "19242"}

    def compose(*args, check=True):
        return subprocess.run(["docker", "compose", "-p", name, *args], cwd=REPO, env=env,
                              capture_output=True, text=True, check=check)

    try:
        compose("up", "-d", "--wait", "--no-build")
        compose("exec", "-T", "-u", "workspace", "workspace", "bash", "-lc", "echo x > ~/.cache/probe && echo keep > ~/note")
        compose("down")
        docker("volume", "rm", f"{name}_cache")
        compose("up", "-d", "--wait", "--no-build")
        out = compose("exec", "-T", "-u", "workspace", "workspace", "bash", "-lc",
                      "cat ~/note; test -e ~/.cache/probe && echo cache-survived || echo cache-fresh").stdout
        assert out.split() == ["keep", "cache-fresh"]
        assert requests.get("http://127.0.0.1:19242/json/version", timeout=5).ok
    finally:
        compose("down", "-v", check=False)
