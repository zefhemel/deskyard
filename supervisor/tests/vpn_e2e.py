"""Opt-in scoped end-to-end fixture; never starts production main.
prepare prints a private directory; start/check/lifecycle/cleanup consume it.
Run each phase separately to permit a real browser screen inspection at ready.
"""

import argparse
import http.cookiejar
import json
import os
import pathlib
import secrets
import signal
import subprocess
import tempfile
import time
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
LABEL = "deskyard.vpn.e2e-fixture"


def run(*args, input=None, env=None, check=True):
    p = subprocess.run(
        args,
        input=input,
        text=True,
        capture_output=True,
        env=env,
        timeout=240,
        check=False,
    )
    if check and p.returncode:
        raise RuntimeError(f"{args[0]} {args[1]} failed: {p.stderr[-1500:]}")
    return p.stdout.strip()


def docker(*args, **kw):
    return run("docker", *args, **kw)


def write(path, value):
    path.write_text(json.dumps(value, indent=2))
    path.chmod(0o600)


def wait_file(path, seconds=240):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if path.exists():
            return json.loads(path.read_text())
        time.sleep(1)
    raise RuntimeError("fixture phase timed out: " + path.name)


def prepare(mode):
    if mode not in ("direct", "container", "preview"):
        raise ValueError("mode must be direct, container, or preview")
    d = pathlib.Path(tempfile.mkdtemp(prefix="deskyard-vpn-e2e-", dir="/private/tmp"))
    scope = secrets.token_hex(8)
    prefix = "dy-e2e-" + scope[:10]
    cfg = {
        "Scope": scope,
        "Endpoint": prefix + "-endpoint",
        "Listen": "127.0.0.1:0",
        "Keys": [],
        "mode": mode,
    }
    write(d / "fixture.json", cfg)
    (d / "admin-password").write_text(secrets.token_urlsafe(24))
    (d / "admin-password").chmod(0o600)
    if mode == "preview":
        cfg["Production"] = True
        write(d / "fixture.json", cfg)
        print(d)
        return
    try:
        docker("network", "create", "--label", f"{LABEL}={scope}", prefix + "-net")
        docker(
            "run",
            "-d",
            "--name",
            cfg["Endpoint"],
            "--label",
            f"{LABEL}={scope}",
            "--network",
            prefix + "-net",
            "--cap-add=NET_ADMIN",
            "--sysctl=net.ipv4.ip_forward=1",
            "alpine:3.22",
            "sh",
            "-ec",
            "apk add --no-cache wireguard-tools iptables iproute2 python3 tcpdump >/tmp/setup.log 2>&1; touch /tmp/ready; sleep infinity",
        )
        for _ in range(90):
            p = subprocess.run(
                ["docker", "exec", cfg["Endpoint"], "test", "-f", "/tmp/ready"],
                capture_output=True,
                check=False,
                timeout=10,
            )
            if p.returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError("endpoint setup deadline")
        sk = docker("exec", cfg["Endpoint"], "wg", "genkey")
        cfg["EndpointGateway"] = docker(
            "inspect",
            "--format",
            "{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}",
            cfg["Endpoint"],
        )
        cfg["PublicKey"] = docker(
            "exec", "-i", cfg["Endpoint"], "wg", "pubkey", input=sk + "\n"
        )
        docker(
            "exec",
            "-i",
            cfg["Endpoint"],
            "sh",
            "-ec",
            "umask 077; cat > /tmp/server.key",
            input=sk + "\n",
        )
        peers = []
        for i in range(2):
            key = docker("exec", cfg["Endpoint"], "wg", "genkey")
            keypath = d / f"peer-{i}.key"
            keypath.write_text(key)
            keypath.chmod(0o600)
            cfg["Keys"].append(str(keypath))
            public = docker(
                "exec", "-i", cfg["Endpoint"], "wg", "pubkey", input=key + "\n"
            )
            peers.extend(["peer", public, "allowed-ips", f"10.77.0.{i + 2}/32"])
        docker("exec", cfg["Endpoint"], "ip", "link", "add", "wg0", "type", "wireguard")
        docker(
            "exec",
            cfg["Endpoint"],
            "ip",
            "address",
            "add",
            "10.77.0.1/24",
            "dev",
            "wg0",
        )
        docker(
            "exec",
            cfg["Endpoint"],
            "wg",
            "set",
            "wg0",
            "private-key",
            "/tmp/server.key",
            "listen-port",
            "51820",
            *peers,
        )
        docker(
            "exec",
            cfg["Endpoint"],
            "sh",
            "-ec",
            "ip link set wg0 up; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE; iptables -I FORWARD 1 -i wg0 -p udp --dport 53 -j ACCEPT",
        )
        docker(
            "exec",
            cfg["Endpoint"],
            "iptables",
            "-I",
            "FORWARD",
            "1",
            "!",
            "-i",
            "wg0",
            "-o",
            "eth0",
            "-j",
            "NFLOG",
            "--nflog-group",
            "5",
            "--nflog-prefix",
            "forward-observation",
        )
        docker(
            "exec",
            "-d",
            cfg["Endpoint"],
            "sh",
            "-ec",
            "tcpdump -l -nn -i nflog:5 >/tmp/forward-observation.txt 2>&1",
        )
        if mode == "container":
            docker("exec", cfg["Endpoint"], "ip", "link", "set", "wg0", "down")
            cfg["SelfID"] = prefix + "-supervisor"
            cfg["Listen"] = "0.0.0.0:8080"
        write(d / "fixture.json", cfg)
        print(d)
    except BaseException:
        cleanup(d)
        raise


def start(d):
    cfg = json.loads((d / "fixture.json").read_text())
    env = os.environ.copy()
    env.update(
        DESKYARD_VPN_E2E_DIR=str(d), CC_TEST_IMAGE="deskyard-debian-xfce:selkies"
    )
    buildenv = env.copy()
    if cfg["mode"] == "container":
        buildenv.update(GOOS="linux", GOARCH="arm64", CGO_ENABLED="0")
    run(
        "go",
        "test",
        "-c",
        "-tags",
        "vpnintegration",
        "-o",
        str(d / "supervisor.test"),
        "./internal/server",
        env=buildenv,
    )
    if cfg["mode"] == "container":
        docker(
            "run",
            "-d",
            "--name",
            cfg["SelfID"],
            "--label",
            f"{LABEL}={cfg['Scope']}",
            "-v",
            f"{d}:{d}",
            "-v",
            "/Users/zef/.orbstack/run/docker.sock:/var/run/docker.sock",
            "-e",
            f"DESKYARD_VPN_E2E_DIR={d}",
            "-e",
            "DOCKER_HOST=unix:///var/run/docker.sock",
            "-e",
            "CC_TEST_IMAGE=deskyard-debian-xfce:selkies",
            "-p",
            "127.0.0.1::8080",
            "alpine:3.22",
            str(d / "supervisor.test"),
            "-test.run",
            "^TestVPNEndToEnd$",
            "-test.v",
        )
    else:
        with (d / "supervisor.log").open("w") as log:
            p = subprocess.Popen(
                [
                    str(d / "supervisor.test"),
                    "-test.run",
                    "^TestVPNEndToEnd$",
                    "-test.v",
                ],
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        (d / "pid").write_text(str(p.pid))
        identity = native_process(p.pid)
        if identity is None:
            raise RuntimeError("fixture supervisor exited during startup")
        write(d / "native-process.json", identity)
    ready = wait_file(d / "ready.json")
    if cfg["mode"] == "container":
        port = docker(
            "inspect",
            "--format",
            '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}',
            cfg["SelfID"],
        )
        url = "http://127.0.0.1:" + port
    else:
        url = "http://" + ready["listen"]
    (d / "url").write_text(url)
    print(url)


def client(d):
    url = (d / "url").read_text()
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def req(path, body=None, method=None):
        data = None if body is None else json.dumps(body).encode()
        r = urllib.request.Request(
            url + path,
            data=data,
            method=method,
            headers={"Origin": url, "Content-Type": "application/json"},
        )
        with op.open(r, timeout=30) as response:
            return response.read()

    req(
        "/api/login",
        {"username": "admin", "password": (d / "admin-password").read_text().strip()},
    )
    req.cookie = "; ".join(f"{c.name}={c.value}" for c in jar)
    return url, req


def check(d):
    ready = wait_file(d / "ready.json")
    url, req = client(d)
    evidence = {}
    for name in ready["names"]:
        token = json.loads(
            req(
                "/api/desktops/" + name + "/tokens",
                {"name": "VPN end-to-end verification"},
            )
        )["token"]
        env = os.environ.copy()
        env.update(DESKYARD_URL=url + "/" + name, DESKYARD_TOKEN=token)
        cli = str(ROOT / "bin/deskyard")
        result = run(
            cli,
            "exec",
            "--",
            "sh",
            "-ec",
            "printf vpn-e2e-workspace > /workspace/vpn-e2e.txt; id -u",
            env=env,
        )
        assert result == "1000", result
        files = json.loads(req("/api/desktops/" + name + "/files"))
        assert any(x["name"] == "vpn-e2e.txt" for x in files["entries"])
        assert (
            req("/api/desktops/" + name + "/download?path=/workspace/vpn-e2e.txt")
            == b"vpn-e2e-workspace"
        )
        png = subprocess.run(
            [cli, "screenshot"], env=env, capture_output=True, check=True
        ).stdout
        assert png.startswith(b"\x89PNG")
        (d / (name + ".png")).write_bytes(png)
        activity = json.loads(req("/api/desktops/" + name + "/activity"))
        assert activity["agents"], activity
        # Production screen proxy performs authenticated Caddy dialing.
        assert req("/" + name + "/screen/api/websockets") == b""
        # Agent CDP access reaches the desktop's shared Chromium through supervisor.
        port = 19320 + ready["names"].index(name)
        with (d / (name + "-cdp.log")).open("w") as log:
            proc = subprocess.Popen(
                [cli, "browser", "connect", "--port", str(port)],
                env=env,
                stdout=log,
                stderr=log,
            )
        try:
            for _ in range(30):
                try:
                    with urllib.request.urlopen(
                        f"http://127.0.0.1:{port}/json/version", timeout=2
                    ) as r:
                        version = json.load(r)
                    break
                except (OSError, urllib.error.URLError):
                    time.sleep(1)
            else:
                raise RuntimeError("CDP tunnel deadline")
            cenv = os.environ.copy()
            cenv["CDP"] = version["webSocketDebuggerUrl"]
            run("node", str(ROOT / "tests/vpn_e2e_browser.mjs"), env=cenv)
        finally:
            proc.terminate()
            proc.wait(timeout=10)
        evidence[name] = {
            "execUid": 1000,
            "files": True,
            "screenshotBytes": len(png),
            "activity": True,
            "authenticatedProxy": True,
            "browserViaCDP": True,
        }
    write(d / "api-evidence.json", evidence)
    print("PASS real exec/files/screenshot/activity/proxy/browser for two desktops")


def native_process(pid):
    # Never inspect environment variables: the command/start identity contains
    # only this private fixture binary path and Go test flags.
    result = subprocess.run(
        [
            "ps",
            "-ww",
            "-p",
            str(pid),
            "-o",
            "uid=",
            "-o",
            "stat=",
            "-o",
            "lstart=",
            "-o",
            "command=",
        ],
        capture_output=True,
        text=True,
        check=False,
        timeout=5,
    )
    if (
        result.returncode == 1
        and not result.stdout.strip()
        and not result.stderr.strip()
    ):
        return None
    if result.returncode:
        raise RuntimeError("cannot verify fixture process ownership")
    fields = result.stdout.strip().split(None, 7)
    if len(fields) != 8:
        raise RuntimeError("cannot parse fixture process ownership")
    if fields[1].startswith("Z"):
        return None
    return {
        "uid": int(fields[0]),
        "started": " ".join(fields[2:7]),
        "command": fields[7],
    }


def stop_native(d):
    if not (d / "pid").exists():
        return  # Preparation or compilation failed before a process was started.
    pid = int((d / "pid").read_text())
    if pid <= 1:
        raise RuntimeError("invalid fixture process ownership")
    current = native_process(pid)
    if current is None:
        return
    identity_path = d / "native-process.json"
    if not identity_path.exists():
        raise RuntimeError(
            "missing fixture process ownership record; stop the original process first"
        )
    expected = json.loads(identity_path.read_text())
    command = f"{d}/supervisor.test -test.run ^TestVPNEndToEnd$ -test.v"
    if expected.get("uid") != os.getuid() or expected.get("command") != command:
        raise RuntimeError("fixture process ownership mismatch")
    for sig, seconds in ((signal.SIGTERM, 10), (signal.SIGKILL, 5)):
        # Revalidate before each signal and throughout the wait. A reused PID
        # aborts cleanup rather than receiving a signal or permitting deletion.
        current = native_process(pid)
        if current is None:
            return
        if current != expected:
            raise RuntimeError("fixture process ownership changed")
        try:
            os.kill(pid, sig)
        except ProcessLookupError:
            return
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            current = native_process(pid)
            if current is None:
                return
            if current != expected:
                raise RuntimeError("fixture process ownership changed while waiting")
            time.sleep(0.1)
    raise RuntimeError("fixture supervisor shutdown deadline; resources preserved")


def stop_container_supervisor(cfg):
    scope = cfg["Scope"]
    for rid in docker(
        "container", "ls", "--no-trunc", "-aq", "--filter", f"label={LABEL}={scope}"
    ).split():
        observed = json.loads(docker("container", "inspect", rid))[0]
        if observed["Id"] != rid or observed["Config"]["Labels"].get(LABEL) != scope:
            raise RuntimeError("supervisor container ownership changed")
        if observed["Name"].lstrip("/") != cfg.get("SelfID") and rid != cfg.get(
            "SelfID"
        ):
            continue
        docker("container", "stop", "--time", "10", rid)
        stopped = json.loads(docker("container", "inspect", rid))[0]
        if stopped["Id"] != rid or stopped["State"]["Running"]:
            raise RuntimeError("fixture supervisor did not stop; resources preserved")


def cleanup(d):
    cfg = json.loads((d / "fixture.json").read_text())
    scope = cfg["Scope"]
    if cfg["mode"] == "container":
        stop_container_supervisor(cfg)
    else:
        stop_native(d)
    for kind in ("container", "network"):
        args = [kind, "ls", "--no-trunc", "-q", "--filter", f"label={LABEL}={scope}"]
        if kind == "container":
            args.append("-a")
        for rid in docker(*args).split():
            observed = docker(
                kind,
                "inspect",
                "--format",
                '{{index .Labels "' + LABEL + '"}}'
                if kind == "network"
                else '{{index .Config.Labels "' + LABEL + '"}}',
                rid,
            )
            if observed != scope:
                raise RuntimeError("ownership changed")
            docker(kind, "rm", *(["-f"] if kind == "container" else []), rid)
        assert not docker(*args), f"{kind} leftovers"
    print("PASS cleanup: zero owned containers (all statuses), zero owned networks")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("action", choices=["prepare", "start", "check", "cleanup"])
    ap.add_argument("value")
    a = ap.parse_args()
    if a.action == "prepare":
        prepare(a.value)
    else:
        globals()[a.action](pathlib.Path(a.value))
