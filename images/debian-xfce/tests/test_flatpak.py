import time

import pytest

BWRAP_PROBE = "bwrap --unshare-user --unshare-pid --ro-bind / / --proc /proc --dev /dev true"
SILVERBULLET_REF = "https://release.silverbullet.md/flatpak/silverbullet-edge.flatpakref"
SILVERBULLET_ID = "plus.silverbullet.desktop"

def test_bwrap_works_in_flatpak_mode(sandbox):
    sandbox.exec(BWRAP_PROBE)

def test_chromium_sandboxed_in_flatpak_mode(sandbox):
    sandbox.exec("curl -fs --max-time 60 -o /dev/null http://127.0.0.1:9221/json/version")
    assert "--no-sandbox" not in sandbox.exec("ps -eo args | grep -m1 '[c]hromium-shared'").stdout

def test_flathub_remote_configured(sandbox):
    assert "flathub" in sandbox.exec("flatpak remotes --user").stdout

def test_flatpak_exports_on_data_dirs(sandbox):
    assert "/workspace/.local/share/flatpak/exports/share" in sandbox.exec("printenv XDG_DATA_DIRS").stdout

@pytest.mark.slow
@pytest.mark.network
def test_silverbullet_edge_flatpak(make_sandbox, artifacts_dir):
    sb = make_sandbox()
    sb.exec(f"flatpak install -y --noninteractive {SILVERBULLET_REF}", timeout=1800)  # no --user, as people type it
    sb.exec(f"setsid -f flatpak run {SILVERBULLET_ID} >/tmp/sb.log 2>&1")
    deadline = time.time() + 90
    while time.time() < deadline:
        if sb.exec("xdotool search --onlyvisible --name SilverBullet", check=False).returncode == 0:
            break
        time.sleep(2)
    else:
        raise AssertionError(sb.exec("cat /tmp/sb.log", check=False).stdout[-3000:])
    time.sleep(5)
    (artifacts_dir / "silverbullet.png").write_bytes(sb.exec_bytes("deskyard screenshot"))
    sb.remove()
    sb2 = make_sandbox()  # new container: fresh cache volume, same home
    assert SILVERBULLET_ID in sb2.exec("flatpak list --user --app --columns=application").stdout
    sb2.exec(f"setsid -f flatpak run {SILVERBULLET_ID} >/tmp/sb.log 2>&1")
    deadline = time.time() + 90
    while sb2.exec("xdotool search --onlyvisible --name SilverBullet", check=False).returncode != 0:
        assert time.time() < deadline, sb2.exec("cat /tmp/sb.log", check=False).stdout[-3000:]
        time.sleep(2)

def test_plain_flatpak_commands_default_to_user(sandbox):
    """Without --user, flatpak would write the system installation (needs root/PolicyKit)."""
    res = sandbox.exec("flatpak remote-add --if-not-exists probe /etc/workspace/flathub.flatpakrepo", check=False)
    assert res.returncode == 0, res.stderr
    assert "probe" in sandbox.exec("flatpak remotes --user").stdout
    # No warning that the system installation's exports aren't on the data dirs.
    assert "is not in the search path" not in res.stderr + res.stdout
    # Explicit --system is left alone (and fails without root, as expected).
    assert sandbox.exec("flatpak remote-add --system probe2 /etc/workspace/flathub.flatpakrepo", check=False).returncode != 0
