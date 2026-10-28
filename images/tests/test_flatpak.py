import time

import pytest

from sandbox import docker


def test_graphical_store_has_flatpak_backend_without_system_package_manager(image):
    store = "gnome-software"
    backend = "gnome-software-plugin-flatpak"
    result = docker("run", "--rm", "--entrypoint", "sh", image, "-c",
                    f"command -v {store} && dpkg-query -W -f='${{db:Status-Status}}' {backend}")
    assert result.stdout.splitlines()[-1] == "installed"
    result = docker("run", "--rm", "--entrypoint", "sh", image, "-c", "test -e /usr/libexec/packagekitd || test -e /usr/lib/packagekit/packagekitd", check=False)
    assert result.returncode != 0


@pytest.mark.network
def test_store_opens_as_user(sandbox, artifacts_dir):
    store = "gnome-software"
    sandbox.exec(f"setsid -f {store} >/tmp/software-store.log 2>&1")
    deadline = time.time() + 180
    while time.time() < deadline:
        if sandbox.exec("xdotool search --onlyvisible --class '(gnome-software|org.gnome.Software)'", check=False).returncode == 0:
            break
        time.sleep(1)
    else:
        raise AssertionError(sandbox.exec("cat /tmp/software-store.log").stdout[-3000:])
    assert sandbox.exec(f"ps -C {store} -o user= | sort -u").stdout.split() == ["user"]
    assert sandbox.exec("flatpak remotes --system --columns=name").stdout.strip() == ""
    (artifacts_dir / "software-store.png").write_bytes(sandbox.exec_bytes("deskyard screenshot"))

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


@pytest.mark.parametrize("no_new_privileges", [False, True])
def test_document_portal_shared_namespace(make_sandbox, volumes, no_new_privileges):
    args = ["-v", f"{volumes.home}:/mnt/readonly:ro"]
    if no_new_privileges:
        args += ["--security-opt", "no-new-privileges"]
    sb = make_sandbox(docker_args=args)
    result = sb.exec("dbus-send --session --print-reply --dest=org.freedesktop.portal.Documents "
                     "/org/freedesktop/portal/documents org.freedesktop.portal.Documents.GetMountPoint")
    assert "/run/workspace/doc" in result.stdout
    sb.exec("mountpoint -q /run/workspace/doc && test -d /run/workspace/doc/by-app")
    # Remote execs and graphical desktop services must see the very same mount.
    panel = sb.exec("pgrep -o -x xfce4-panel").stdout.strip()
    sb.exec(f"test /proc/self/ns/mnt -ef /proc/{panel}/ns/mnt")
    sb.exec(BWRAP_PROBE)
    assert "CapEff:\t0000000000000000" in sb.exec("cat /proc/self/status").stdout
    sb.exec("printf before > /workspace/portal-probe.txt")
    exported = sb.exec("flatpak document-export --app=org.deskyard.Probe --allow-read --allow-write /workspace/portal-probe.txt").stdout.strip()
    assert exported.startswith("/run/workspace/doc/")
    assert sb.exec(f"cat {exported}").stdout == "before"
    sb.exec(f"printf after > {exported}")
    assert sb.exec("cat /workspace/portal-probe.txt").stdout == "after"
    assert sb.exec("touch /mnt/readonly/must-not-exist", check=False).returncode != 0
    assert sb.exec("unshare --user --map-current-user --mount --keep-caps mount -o remount,rw /mnt/readonly", check=False).returncode != 0
    # Namespace capabilities must not confer authority over the container's parent.
    assert sb.exec("nsenter -t 1 -U true", check=False).returncode != 0
    if no_new_privileges:
        assert "NoNewPrivs:\t1" in sb.exec("cat /proc/self/status").stdout
