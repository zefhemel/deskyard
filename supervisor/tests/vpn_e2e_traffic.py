"""Traffic and lifecycle phases for vpn_e2e.py's owned fixture."""

import json
import pathlib
import subprocess
import sys
import time

from vpn_e2e import client, docker, wait_file, write


def request(container, url):
    p = subprocess.run(
        [
            "docker",
            "exec",
            "-u",
            "1000",
            container,
            "curl",
            "-kfsS",
            "--connect-timeout",
            "1",
            "--max-time",
            "4",
            url,
        ],
        capture_output=True,
        check=False,
        timeout=10,
    )
    return p.returncode == 0


def eventually(fn, label, seconds=90):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if fn():
            return
        time.sleep(1)
    raise RuntimeError("deadline: " + label)


def traffic(d):
    cfg = json.loads((d / "fixture.json").read_text())
    r = wait_file(d / "ready.json")
    _, req = client(d)
    endpoint = cfg["Endpoint"]
    # Observe ingress direction, without accepting/dropping any packets. When
    # wg0 goes down, its connected route disappears; late public-server replies
    # to 10.77/24 can follow the endpoint default route. Those are not desktop
    # egress. Account for every broad match and separately require zero requests
    # entering from either desktop bridge, regardless of their source address.
    chain = "DYE2EOBS"
    docker("exec", endpoint, "iptables", "-N", chain, check=False)
    docker("exec", endpoint, "iptables", "-F", chain)
    docker("exec", endpoint, "iptables", "-D", "FORWARD", "-j", chain, check=False)
    docker("exec", endpoint, "iptables", "-I", "FORWARD", "1", "-j", chain)
    interfaces = json.loads(docker("exec", endpoint, "ip", "-j", "address"))
    bridges = [
        i["ifname"]
        for i in interfaces
        if i["ifname"].startswith("eth") and i["ifname"] != "eth0"
    ]
    assert len(bridges) == 2, "fixture needs both desktop bridge interfaces"
    for interface in bridges:
        docker(
            "exec",
            endpoint,
            "iptables",
            "-A",
            chain,
            "-i",
            interface,
            "-o",
            "eth0",
            "-m",
            "comment",
            "--comment",
            "desktop-direct",
        )
    docker(
        "exec",
        endpoint,
        "iptables",
        "-A",
        chain,
        "-i",
        "eth0",
        "-o",
        "eth0",
        "-d",
        "10.77.0.0/24",
        "-m",
        "comment",
        "--comment",
        "late-tunnel-return",
    )
    docker(
        "exec",
        endpoint,
        "iptables",
        "-A",
        chain,
        "!",
        "-i",
        "wg0",
        "-o",
        "eth0",
        "-m",
        "comment",
        "--comment",
        "all-direct-forwards",
    )

    def counters():
        lines = docker("exec", endpoint, "iptables", "-nvxL", chain).splitlines()
        return {
            kind: sum(int(line.split()[0]) for line in lines if kind in line)
            for kind in ["desktop-direct", "late-tunnel-return", "all-direct-forwards"]
        }

    evidence = {"initialBlocked": True}
    # Container mode starts with its endpoint down to prove pre-tunnel access.
    if cfg["mode"] == "container":
        for c in r["desktops"]:
            assert not request(c, "https://1.1.1.1/cdn-cgi/trace"), "startup escaped"
        for name in r["names"]:
            assert req("/" + name + "/screen/api/websockets") == b""
    else:
        evidence["initialBlocked"] = False
    docker("exec", endpoint, "ip", "link", "set", "wg0", "up")
    for c in r["desktops"]:
        eventually(
            lambda c=c: request(c, "https://1.1.1.1/cdn-cgi/trace"),
            "tunneled public HTTPS",
        )
    transfers = docker("exec", endpoint, "wg", "show", "wg0", "transfer").splitlines()
    assert len(transfers) == 2 and all(
        int(line.split()[2]) > 1000 for line in transfers
    )
    for i, c in enumerate(r["desktops"]):
        g = json.loads(docker("inspect", r["gateways"][1 - i]))[0]
        address = next(iter(g["NetworkSettings"]["Networks"].values()))["IPAddress"]
        assert not request(c, "http://" + address + ":8080"), (
            "cross-desktop access escaped"
        )
    docker("exec", endpoint, "ip", "link", "set", "wg0", "down")
    for _ in range(3):
        for c in r["desktops"]:
            assert not request(c, "https://1.1.1.1/cdn-cgi/trace"), "loss escaped"
        for name in r["names"]:
            assert req("/" + name + "/screen/api/websockets") == b""
    evidence["lossBlockedAttempts"] = 6
    evidence["localDuringLoss"] = True
    docker("exec", endpoint, "ip", "link", "set", "wg0", "up")
    for c in r["desktops"]:
        eventually(
            lambda c=c: request(c, "https://1.1.1.1/cdn-cgi/trace"), "tunnel recovery"
        )
    counts = counters()
    write(d / "forward-counts.json", counts)
    assert counts["desktop-direct"] == 0, "desktop bridge traffic bypassed tunnel"
    assert counts["all-direct-forwards"] == counts["late-tunnel-return"], (
        "unclassified direct forwarded packets"
    )

    evidence.update(
        twoIndependentPeers=True,
        tunnelRecovery=True,
        crossDesktopDenied=True,
        directForwardedPackets=counts["desktop-direct"],
        accountedLateReplies=counts["late-tunnel-return"],
    )
    write(d / "traffic-evidence.json", evidence)
    print(
        "PASS two peer tunnels, cross-desktop denial, six loss egress attempts blocked, local proxy available, recovery, zero direct-forward packets"
    )


def lifecycle(d):
    r = wait_file(d / "ready.json")
    _, req = client(d)
    (d / "continue").touch()
    wait_file(d / "gateway-stopped")
    for _ in range(3):
        assert not request(r["desktops"][0], "https://1.1.1.1/cdn-cgi/trace"), (
            "stopped gateway escaped"
        )
    (d / "replacement").touch()
    recovered = wait_file(d / "recovered.json")
    eventually(
        lambda: request(recovered["desktop"], "https://1.1.1.1/cdn-cgi/trace"),
        "replaced gateway tunnel",
    )
    assert (
        req("/api/desktops/" + r["names"][0] + "/download?path=/workspace/vpn-e2e.txt")
        == b"vpn-e2e-workspace"
    )
    assert req("/" + r["names"][0] + "/screen/api/websockets") == b""
    write(
        d / "lifecycle-evidence.json",
        {
            "gatewayStoppedBlockedAttempts": 3,
            "recoveryEgress": True,
            "workspaceRetained": True,
            "proxyRecovered": True,
        },
    )
    (d / "finish").touch()
    print(
        "PASS gateway-stop egress blocked, new namespace recovery, retained workspace, proxy restored"
    )


def stream(d):
    import asyncio
    import secrets

    import websockets

    url, req = client(d)
    ready = wait_file(d / "ready.json")

    async def verify():
        evidence = {}
        for name in ready["names"]:
            tab = secrets.token_hex(8)
            target = (
                url.replace("http", "ws") + f"/{name}/screen/api/websockets?tab={tab}"
            )
            async with websockets.connect(
                target,
                additional_headers={"Cookie": req.cookie, "Origin": url},
                max_size=None,
            ) as ws:
                await ws.send(
                    "SETTINGS," + json.dumps({"displayId": "primary", "tabId": tab})
                )
                pictures = total = 0
                async with asyncio.timeout(20):
                    while pictures < 3:
                        msg = await ws.recv()
                        if isinstance(msg, bytes) and msg[:1] in (b"\x03", b"\x04"):
                            pictures += 1
                            total += len(msg)
                evidence[name] = {"pictureFrames": pictures, "pictureBytes": total}
        write(d / "stream-evidence.json", evidence)

    asyncio.run(verify())
    print(
        "PASS actual authenticated supervisor-proxied Selkies picture frames for both desktops"
    )


if __name__ == "__main__":
    globals()[sys.argv[1]](pathlib.Path(sys.argv[2]))
