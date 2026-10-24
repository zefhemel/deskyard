#!/usr/bin/env python3
"""Offline acceptance test. Creates and removes its own empty Docker volume."""
import argparse
import json
from pathlib import Path
import shlex
import subprocess
import time
import uuid


def docker(*args, check=True):
    result = subprocess.run(["docker", *args], capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"docker {args[0]} failed ({result.returncode}):\n"
                           + result.stderr.decode(errors="replace"))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image")
    parser.add_argument("--probe", type=Path, required=True, help="compiled verify-cores executable for the image architecture")
    parser.add_argument("--manifest", type=Path, default=Path(__file__).resolve().parents[3] / "examples/retroarch/cores.tsv")
    parser.add_argument("--rom", type=Path, help="optional local SNES game; copied only after the empty-home checks")
    parser.add_argument("--artifacts", type=Path, default=Path("/tmp/retroarch-image-test"))
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    name = "retroarch-example-test-" + uuid.uuid4().hex[:12]
    volume = name + "-home"
    mount = f"type=volume,source={volume},target=/workspace,volume-nocopy"
    here = Path(__file__).resolve().parent

    def execute(command, check=True):
        return docker("exec", "-u", "user", name, "bash", "-lc", command, check=check)

    def start():
        docker("run", "-d", "--name", name, "--network=none", "--shm-size=1g",
               "--mount", mount, "--security-opt", f"seccomp={here / 'flatpak-seccomp.json'}",
               "--security-opt", "apparmor=unconfined", "--security-opt", "systempaths=unconfined",
               "-e", "WORKSPACE_PROXY_TOKEN=offline-example-test", args.image)

    def ready():
        deadline = time.monotonic() + 120
        while execute("desktop-ready", check=False).returncode:
            if time.monotonic() > deadline:
                raise AssertionError("desktop did not become ready")
            time.sleep(1)

    try:
        docker("volume", "create", volume)
        # volume-nocopy prevents Docker from populating it from the image.
        baseline = docker("run", "--rm", "--network=none", "--mount", mount,
                          "--entrypoint", "sh", args.image, "-c",
                          "find /workspace -mindepth 1 -print -quit")
        assert not baseline.stdout.strip(), "workspace was not empty before first boot"
        start()
        ready()
        docker("exec", "-u", "root", name, "mkdir", "-p", "/opt/deskyard/manual-check")
        docker("cp", str(args.probe), name + ":/opt/deskyard/manual-check/verify-cores")
        docker("exec", "-u", "root", name, "chmod", "755", "/opt/deskyard/manual-check/verify-cores")

        installed = execute("flatpak list --system --app --columns=application").stdout.decode().splitlines()
        assert "org.libretro.RetroArch" in installed, "RetroArch missing from image"
        inventory = execute("cat /usr/share/doc/deskyard-retroarch/flatpak-commits.txt").stdout.decode()
        commits = [line.split("\t")[1] for line in inventory.splitlines()]
        assert commits and all(len(value) == 64 and set(value) <= set("0123456789abcdef")
                               for value in commits), "inventory must contain full commit IDs"
        (args.artifacts / "flatpak-commits.txt").write_text(inventory)
        assert not execute("flatpak list --user --app --columns=application").stdout.strip(), "unexpected user installation"
        execute("test -x /workspace/.local/bin/retroarch-deskyard && test -f /workspace/Desktop/RetroArch.desktop")
        execute("test ! -e /workspace/Games && test ! -e /workspace/Downloads")
        execute("test -z \"$(find /workspace/.var/app/org.libretro.RetroArch -not -user user -print -quit)\"")
        probe = execute("flatpak run --filesystem=/opt/deskyard/manual-check:ro "
                        "--command=/opt/deskyard/manual-check/verify-cores org.libretro.RetroArch "
                        "/workspace/.var/app/org.libretro.RetroArch/config/retroarch/cores/*.so")
        loaded = probe.stdout.decode().splitlines()
        expected = {line.split()[0] + ".so" for line in args.manifest.read_text().splitlines()
                    if line.strip() and not line.startswith("#")}
        actual = {line.split(":", 1)[0].rsplit("/", 1)[-1] for line in loaded}
        assert actual == expected and len(loaded) == len(expected), f"core mismatch: {loaded}"
        (args.artifacts / "cores.txt").write_bytes(probe.stdout)
        execute("setsid -f /workspace/.local/bin/retroarch-deskyard --verbose > /tmp/retroarch-example.log 2>&1")
        deadline = time.monotonic() + 60
        while execute("xdotool search --onlyvisible --class retroarch", check=False).returncode:
            if time.monotonic() > deadline:
                raise AssertionError("RetroArch window did not open")
            time.sleep(1)
        (args.artifacts / "menu.png").write_bytes(execute("deskyard screenshot").stdout)
        execute("flatpak kill org.libretro.RetroArch")
        if args.rom:
            rom = args.rom.resolve(strict=True)
            destination = "/workspace/test-game" + rom.suffix
            docker("cp", str(rom), name + ":" + destination)
            docker("exec", "-u", "root", name, "chown", "1000:1000", destination)
            execute("setsid -f /workspace/.local/bin/retroarch-deskyard --verbose "
                    "-L /workspace/.var/app/org.libretro.RetroArch/config/retroarch/cores/snes9x_libretro.so "
                    + shlex.quote(destination) + " > /tmp/retroarch-game.log 2>&1")
            deadline = time.monotonic() + 30
            while b"SET_GEOMETRY" not in execute("cat /tmp/retroarch-game.log").stdout:
                if time.monotonic() > deadline:
                    raise AssertionError("game did not initialize video")
                time.sleep(0.5)
            execute("deskyard wait 6")
            (args.artifacts / "game.png").write_bytes(execute("deskyard screenshot").stdout)
            (args.artifacts / "game.log").write_bytes(execute("cat /tmp/retroarch-game.log").stdout)
            execute("flatpak kill org.libretro.RetroArch")
        # Recreating the container must preserve a user's edited default.
        execute("printf '\n# retained-user-setting\n' >> /workspace/.var/app/org.libretro.RetroArch/config/retroarch/retroarch.cfg")
        docker("rm", "-f", name)
        start()
        ready()
        execute("grep -q retained-user-setting /workspace/.var/app/org.libretro.RetroArch/config/retroarch/retroarch.cfg")
        print(json.dumps({"image": args.image, "empty_workspace": True, "offline": True,
                          "loaded_cores": loaded, "user_setting_retained": True,
                          "game_loaded": bool(args.rom),
                          "artifacts": str(args.artifacts.resolve())}, indent=2))
    except BaseException:
        (args.artifacts / "container.log").write_bytes(docker("logs", name, check=False).stdout)
        (args.artifacts / "retroarch.log").write_bytes(execute("cat /tmp/retroarch-example.log", check=False).stdout)
        raise
    finally:
        docker("rm", "-f", "-v", name, check=False)
        docker("volume", "rm", volume, check=False)


if __name__ == "__main__":
    main()
