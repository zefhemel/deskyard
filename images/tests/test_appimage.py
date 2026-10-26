"""AppImages run without FUSE, and ~/Applications puts them in the applications menu."""

import subprocess
import time

import pytest

from sandbox import REPO, docker

LAUNCHER = "~/.local/share/applications/appimage-probe.desktop"


def test_every_home_gets_applications_folder_with_readme(make_sandbox):
    sb = make_sandbox()
    assert "Put AppImages here" in sb.exec("cat ~/Applications/README.txt").stdout
    assert sb.exec("stat -c %U ~/Applications/README.txt").stdout.strip() == "user"
    # Deleted, it stays deleted across restarts.
    sb.exec("rm ~/Applications/README.txt")
    sb.restart()
    assert sb.exec("ls -A ~/Applications").stdout.strip() == ""


@pytest.fixture(scope="session")
def probe_appimage(tmp_path_factory):
    out = tmp_path_factory.mktemp("appimage")
    subprocess.run(["docker", "build", "-q", "-f", str(REPO / "fixtures" / "appimage.Dockerfile"),
                    "--output", f"type=local,dest={out}", str(REPO / "fixtures")],
                   check=True, stdout=subprocess.DEVNULL)
    return out / "probe.AppImage"


def put(sandbox, src, dest):
    docker("cp", str(src), f"{sandbox.name}:{dest}")
    sandbox.exec(f"chown user:user {dest}", user="root")


def wait_for(sandbox, cmd, timeout=30):
    deadline = time.monotonic() + timeout
    while sandbox.exec(cmd, check=False).returncode != 0:
        assert time.monotonic() < deadline, f"timed out waiting for: {cmd}"
        time.sleep(0.5)


# The fixture AppImage is built with Debian packages and the upstream runtime.
@pytest.mark.network
def test_appimage_runs_without_fuse(sandbox, probe_appimage):
    assert sandbox.exec("test -e /dev/fuse", check=False).returncode != 0
    put(sandbox, probe_appimage, "/tmp/probe.AppImage")
    sandbox.exec("/tmp/probe.AppImage hello")
    assert sandbox.exec("cat ~/appimage-probe.out").stdout.strip() == "hello"


@pytest.mark.network
def test_applications_folder_adds_and_removes_launchers(sandbox, probe_appimage):
    # Copied in without the executable bit, as a browser download would be.
    put(sandbox, probe_appimage, "/workspace/Applications/.probe.part")
    sandbox.exec("chmod -x ~/Applications/.probe.part && mv ~/Applications/.probe.part ~/Applications/probe.AppImage")
    wait_for(sandbox, f"test -f {LAUNCHER}")
    launcher = sandbox.exec(f"cat {LAUNCHER}").stdout
    assert "Name=Probe App" in launcher
    assert 'Exec="/workspace/Applications/probe.AppImage" --probe %U' in launcher
    icon = next(l.split("=", 1)[1] for l in launcher.splitlines() if l.startswith("Icon="))
    assert "<svg" in sandbox.exec(f"cat '{icon}'").stdout

    # Launched from the menu the way the desktop does it.
    sandbox.exec(f"exo-open {LAUNCHER}")
    wait_for(sandbox, "grep -qx -- --probe ~/appimage-probe.out")

    sandbox.exec("rm ~/Applications/probe.AppImage")
    wait_for(sandbox, f"! test -e {LAUNCHER} && ! test -e '{icon}'")


@pytest.mark.network
def test_thunar_action_moves_appimage_into_applications(sandbox, probe_appimage):
    action = sandbox.exec("cat /etc/xdg/Thunar/uca.xml").stdout
    assert "<command>deskyard-appimage add %F</command>" in action
    sandbox.exec("mkdir -p ~/Downloads")
    put(sandbox, probe_appimage, "/workspace/Downloads/probe.AppImage")
    sandbox.exec("deskyard-appimage add ~/Downloads/probe.AppImage")
    assert sandbox.exec("test -x ~/Applications/probe.AppImage && ! test -e ~/Downloads/probe.AppImage",
                        check=False).returncode == 0
    sandbox.exec(f"test -f {LAUNCHER}")


def test_thunar_action_reaches_existing_homes(sandbox):
    # Homes with their own uca.xml get the action once, keeping their actions.
    sandbox.exec("rm -f ~/.config/workspace/thunar-appimage-action && mkdir -p ~/.config/Thunar"
                 " && printf '<actions>\\n<action><unique-id>mine</unique-id></action>\\n</actions>\\n'"
                 " > ~/.config/Thunar/uca.xml && workspace-migrate-thunar")
    saved = sandbox.exec("cat ~/.config/Thunar/uca.xml").stdout
    assert "<unique-id>mine</unique-id>" in saved and saved.count("deskyard-appimage-add") == 1
    # Removed by the user, it stays removed.
    sandbox.exec("printf '<actions>\\n</actions>\\n' > ~/.config/Thunar/uca.xml && workspace-migrate-thunar")
    assert "deskyard-appimage" not in sandbox.exec("cat ~/.config/Thunar/uca.xml").stdout
