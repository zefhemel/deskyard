"""Opt-in scoped end-to-end fixture; never starts production main.
prepare prints a private directory; start/check/lifecycle/cleanup consume it.
Run each phase separately to permit a real browser screen inspection at ready.
Like the supervisor, the fixture runs only in a container: the vpnintegration
test binary, cross-compiled for the engine and mounted into deskyard:base, with
the data folder at /data and the private directory at /e2e.
"""

import argparse
import http.cookiejar
import json
import os
import pathlib
import secrets
import subprocess
import tempfile
import time
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
LABEL = "deskyard.vpn.e2e-fixture"
SUPERVISOR_IMAGE = "deskyard:base"  # as conftest.py: the base image, binary mounted
INSIDE = "/e2e"  # the private directory inside the fixture supervisor's container
GOARCH = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


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
    if mode not in ("fixture", "preview"):
        raise ValueError("mode must be fixture or preview")
    d = pathlib.Path(tempfile.mkdtemp(prefix="deskyard-vpn-e2e-", dir="/private/tmp"))
    (d / "data").mkdir()
    scope = secrets.token_hex(8)
    prefix = "dy-e2e-" + scope[:10]
    cfg = {
        "Scope": scope,
        "Endpoint": prefix + "-endpoint",
        "SelfID": prefix + "-supervisor",
        "Listen": "0.0.0.0:7343",
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
        # The tunnel starts down, to verify initial blocked internet access.
        docker("exec", cfg["Endpoint"], "ip", "link", "set", "wg0", "down")
        write(d / "fixture.json", cfg)
        print(d)
    except BaseException:
        cleanup(d)
        raise


def start(d):
    cfg = json.loads((d / "fixture.json").read_text())
    image = os.environ.get("CC_TEST_IMAGE", "deskyard-debian-xfce:selkies")
    arch = docker("info", "-f", "{{.Architecture}}")
    if arch not in GOARCH:
        raise RuntimeError(f"unsupported engine architecture {arch}")
    buildenv = os.environ.copy()
    buildenv.update(GOOS="linux", GOARCH=GOARCH[arch], CGO_ENABLED="0")
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
    docker("build", "-q", "--target", "base", "-t", SUPERVISOR_IMAGE, str(ROOT))
    docker(
        "run",
        "-d",
        "--name",
        cfg["SelfID"],
        "--label",
        f"{LABEL}={cfg['Scope']}",
        "-v",
        "/var/run/docker.sock:/var/run/docker.sock",
        "-v",
        f"{d / 'data'}:/data",
        "-v",
        f"{d}:{INSIDE}",
        "-e",
        f"DESKYARD_VPN_E2E_DIR={INSIDE}",
        "-e",
        "DOCKER_HOST=unix:///var/run/docker.sock",
        "-e",
        f"CC_TEST_IMAGE={image}",
        "-p",
        "127.0.0.1::7343",
        "--entrypoint",
        f"{INSIDE}/supervisor.test",
        SUPERVISOR_IMAGE,
        "-test.run",
        "^TestVPNEndToEnd$",
        "-test.v",
    )
    wait_file(d / "ready.json")
    port = docker(
        "inspect",
        "--format",
        '{{(index (index .NetworkSettings.Ports "7343/tcp") 0).HostPort}}',
        cfg["SelfID"],
    )
    url = "http://127.0.0.1:" + port
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
        # A token account with full access, signed in as `deskyard auth` does.
        user = "e2e" + secrets.token_hex(4)
        req("/api/accounts", {"username": user, "grants": [{"computer": name, "level": "full"}]})
        link = json.loads(req("/api/accounts/" + user + "/link", {}))["link"]
        credential = json.loads(
            req(
                "/api/links/redeem",
                {"link": link.split("#", 1)[1], "client": "cli", "name": "VPN end-to-end verification"},
            )
        )["credential"]
        env = os.environ.copy()
        env.update(DESKYARD_URL=url + "/" + name, DESKYARD_CREDENTIAL=credential)
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
        assert run(cli, "exec", "--", "cat", "/workspace/vpn-e2e.txt", env=env) == "vpn-e2e-workspace"
        png = subprocess.run(
            [cli, "screenshot"], env=env, capture_output=True, check=True
        ).stdout
        assert png.startswith(b"\x89PNG")
        (d / (name + ".png")).write_bytes(png)
        activity = json.loads(req("/api/environments/" + name + "/activity"))
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
    stop_container_supervisor(cfg)
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
