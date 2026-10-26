"""Focused helper regressions; no engine or production conftest required."""

import json
import os
import pathlib
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import vpn_e2e as fixture
import vpn_e2e_traffic as traffic


class OwnedEngine:
    def __init__(self, scope, supervisor_alive, mode):
        self.scope = scope
        self.supervisor_alive = supervisor_alive
        self.mode = mode
        # Adversarial order: a recovering dependency precedes the supervisor.
        self.containers = ["gateway-id"]
        if mode == "container":
            self.containers.append("supervisor-id")
        self.networks = ["network-id"]
        self.removed = []

    def docker(self, *args, **_kwargs):
        kind, op = args[:2]
        if op == "ls":
            return "\n".join(self.containers if kind == "container" else self.networks)
        rid = args[-1]
        if op == "inspect":
            if "--format" in args:
                return self.scope
            return json.dumps(
                [
                    {
                        "Id": rid,
                        "Name": "/fixture-supervisor"
                        if rid == "supervisor-id"
                        else "/gateway",
                        "Config": {"Labels": {fixture.LABEL: self.scope}},
                        "State": {"Running": self.supervisor_alive()},
                    }
                ]
            )
        if op == "stop":
            assert rid == "supervisor-id", "must stop exact checked supervisor ID"
            self.stop_supervisor()
            return rid
        if op == "rm":
            assert not self.supervisor_alive(), (
                "deletion raced active supervisor recovery"
            )
            self.removed.append(rid)
            (self.containers if kind == "container" else self.networks).remove(rid)
            return rid
        raise AssertionError(args)


class CleanupTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.d = pathlib.Path(self.tmp.name)
        self.alive = True
        self.pending_exit = None
        self.command = f"{self.d}/supervisor.test -test.run ^TestVPNEndToEnd$ -test.v"
        self.started = "Fri Oct 23 12:00:00 2026"
        self.identity = {
            "uid": os.getuid(),
            "started": self.started,
            "command": self.command,
        }
        fixture.write(self.d / "native-process.json", self.identity)
        (self.d / "pid").write_text("1234")

    def configure(self, mode):
        fixture.write(
            self.d / "fixture.json",
            {
                "mode": mode,
                "Scope": "owned",
                "SelfID": "fixture-supervisor",
            },
        )
        self.engine = OwnedEngine("owned", lambda: self.alive, mode)
        self.engine.stop_supervisor = self.stop

    def stop(self):
        self.alive = False

    def ps(self, args, **_kwargs):
        self.assertEqual(args[0], "ps")
        if self.pending_exit is not None:
            self.pending_exit -= 1
            if self.pending_exit == 0:
                self.stop()
        line = f"{os.getuid()} S {self.started} {self.command}\n" if self.alive else ""
        return subprocess.CompletedProcess(args, 0 if self.alive else 1, line, "")

    def kill(self, pid, sig):
        self.assertEqual(pid, 1234)
        self.assertIn(sig, (signal.SIGTERM, signal.SIGKILL))
        self.pending_exit = 2  # Signal delivery alone does not mean exit.

    def test_failed_native_phase_waits_for_exit_before_resource_deletion(self):
        self.configure("direct")
        with (
            patch.object(fixture, "docker", self.engine.docker),
            patch.object(fixture.subprocess, "run", self.ps),
            patch.object(fixture.os, "kill", self.kill),
        ):
            fixture.cleanup(self.d)
        self.assertFalse(self.alive)
        self.assertEqual(self.engine.removed, ["gateway-id", "network-id"])

    def test_reused_native_pid_aborts_without_signal_or_resource_deletion(self):
        self.configure("direct")
        self.started = "Fri Oct 23 12:01:00 2026"
        with (
            patch.object(fixture, "docker", self.engine.docker),
            patch.object(fixture.subprocess, "run", self.ps),
            patch.object(
                fixture.os, "kill", side_effect=AssertionError("foreign signal")
            ),
            self.assertRaisesRegex(RuntimeError, "ownership"),
        ):
            fixture.cleanup(self.d)
        self.assertTrue(self.alive)
        self.assertEqual(self.engine.removed, [])

    def test_native_shutdown_deadline_preserves_resources(self):
        self.configure("direct")
        ticks = iter(range(100))
        signals = []
        with (
            patch.object(fixture, "docker", self.engine.docker),
            patch.object(fixture.subprocess, "run", self.ps),
            patch.object(
                fixture.os, "kill", side_effect=lambda _pid, sig: signals.append(sig)
            ),
            patch.object(fixture.time, "monotonic", side_effect=lambda: next(ticks)),
            patch.object(fixture.time, "sleep"),
            self.assertRaisesRegex(RuntimeError, "shutdown deadline"),
        ):
            fixture.cleanup(self.d)
        self.assertEqual(signals, [signal.SIGTERM, signal.SIGKILL])
        self.assertEqual(self.engine.removed, [])

    def test_missing_native_identity_preserves_live_process_and_resources(self):
        self.configure("direct")
        (self.d / "native-process.json").unlink()
        with (
            patch.object(fixture, "docker", self.engine.docker),
            patch.object(fixture.subprocess, "run", self.ps),
            patch.object(
                fixture.os, "kill", side_effect=AssertionError("unverified signal")
            ),
            self.assertRaisesRegex(RuntimeError, "ownership"),
        ):
            fixture.cleanup(self.d)
        self.assertEqual(self.engine.removed, [])

    def test_real_process_identity_parser_and_exit(self):
        # Exercise platform ps output without an engine, credentials or a server.
        p = subprocess.Popen([sys.executable, "-c", "import time; print(\"ready\", flush=True); time.sleep(30)"], stdout=subprocess.PIPE)
        try:
            self.assertEqual(p.stdout.readline(), b"ready\n")
            identity = fixture.native_process(p.pid)
            self.assertEqual(identity["uid"], os.getuid())
            self.assertIn("time.sleep(30)", identity["command"])
            self.assertTrue(identity["started"])
        finally:
            p.terminate()
            p.wait(timeout=5)
            p.stdout.close()
        self.assertIsNone(fixture.native_process(p.pid))

    def test_failed_container_phase_stops_supervisor_before_gateway_deletion(self):
        self.configure("container")
        with patch.object(fixture, "docker", self.engine.docker):
            fixture.cleanup(self.d)
        self.assertFalse(self.alive)
        self.assertEqual(
            self.engine.removed, ["gateway-id", "supervisor-id", "network-id"]
        )

    def test_container_stop_without_exit_preserves_resources(self):
        self.configure("container")
        self.engine.stop_supervisor = lambda: None
        with (
            patch.object(fixture, "docker", self.engine.docker),
            self.assertRaisesRegex(RuntimeError, "did not stop"),
        ):
            fixture.cleanup(self.d)
        self.assertEqual(self.engine.removed, [])

    def test_container_ownership_change_aborts_before_stop_or_deletion(self):
        self.configure("container")
        self.engine.scope = "foreign"
        self.engine.stop_supervisor = lambda: self.fail("foreign stop")
        with (
            patch.object(fixture, "docker", self.engine.docker),
            self.assertRaisesRegex(RuntimeError, "ownership"),
        ):
            fixture.cleanup(self.d)
        self.assertEqual(self.engine.removed, [])


class LifecycleCountersTests(unittest.TestCase):
    def test_recovery_cannot_succeed_with_direct_or_unaccounted_packets(self):
        for direct, replies, broad in [(1, 0, 1), (0, 2, 3), (0, 2, 2)]:
            with (
                self.subTest(direct=direct, replies=replies, broad=broad),
                tempfile.TemporaryDirectory() as tmp,
            ):
                d = pathlib.Path(tmp)
                fixture.write(d / "fixture.json", {"Endpoint": "owned-endpoint"})
                ready = {"containers": ["old"], "names": ["desktop"]}
                rows = f"{direct} 0 /* desktop-direct */\n0 0 /* desktop-direct */\n{replies} 0 /* late-tunnel-return */\n{broad} 0 /* all-direct-forwards */"

                def req(path):
                    return b"vpn-e2e-workspace" if "download" in path else b""

                with (
                    patch.object(
                        traffic,
                        "wait_file",
                        side_effect=[ready, {}, {"container": "new"}],
                    ),
                    patch.object(traffic, "client", return_value=("", req)),
                    patch.object(
                        traffic, "request", side_effect=lambda c, _url: c == "new"
                    ),
                    patch.object(traffic, "docker", return_value=rows),
                ):
                    if direct or broad != replies:
                        with self.assertRaises(AssertionError):
                            traffic.lifecycle(d)
                        self.assertFalse((d / "finish").exists())
                        self.assertFalse((d / "lifecycle-evidence.json").exists())
                    else:
                        traffic.lifecycle(d)
                        self.assertTrue((d / "finish").exists())
                    self.assertTrue((d / "post-lifecycle-forward-counts.json").exists())


class ImageSelectionTests(unittest.TestCase):
    def test_current_image_reaches_native_and_container_environments(self):
        with tempfile.TemporaryDirectory() as tmp:
            d=pathlib.Path(tmp)
            fixture.write(d / "fixture.json", {"mode":"container","Scope":"scope","SelfID":"owned-supervisor"})
            commands=[]
            def docker(*args, **kwargs):
                commands.append(args)
                return "54000"
            with patch.dict(os.environ,{"CC_TEST_IMAGE":"current-image:validation"}), patch.object(fixture,"run") as build, patch.object(fixture,"docker",side_effect=docker), patch.object(fixture,"wait_file",return_value={}):
                fixture.start(d)
            self.assertEqual(build.call_args.kwargs["env"]["CC_TEST_IMAGE"],"current-image:validation")
            self.assertIn("CC_TEST_IMAGE=current-image:validation",commands[0])


if __name__ == "__main__":
    unittest.main()
