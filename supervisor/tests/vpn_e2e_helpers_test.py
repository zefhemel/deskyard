"""Focused helper regressions; no engine or production conftest required."""

import json
import os
import pathlib
import tempfile
import unittest
from unittest.mock import patch

import vpn_e2e as fixture
import vpn_e2e_traffic as traffic


class OwnedEngine:
    def __init__(self, scope, supervisor_alive):
        self.scope = scope
        self.supervisor_alive = supervisor_alive
        # Adversarial order: a recovering dependency precedes the supervisor.
        self.containers = ["gateway-id", "supervisor-id"]
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

    def configure(self):
        fixture.write(
            self.d / "fixture.json",
            {
                "mode": "fixture",
                "Scope": "owned",
                "SelfID": "fixture-supervisor",
            },
        )
        self.engine = OwnedEngine("owned", lambda: self.alive)
        self.engine.stop_supervisor = self.stop

    def stop(self):
        self.alive = False

    def test_failed_container_phase_stops_supervisor_before_gateway_deletion(self):
        self.configure()
        with patch.object(fixture, "docker", self.engine.docker):
            fixture.cleanup(self.d)
        self.assertFalse(self.alive)
        self.assertEqual(
            self.engine.removed, ["gateway-id", "supervisor-id", "network-id"]
        )

    def test_container_stop_without_exit_preserves_resources(self):
        self.configure()
        self.engine.stop_supervisor = lambda: None
        with (
            patch.object(fixture, "docker", self.engine.docker),
            self.assertRaisesRegex(RuntimeError, "did not stop"),
        ):
            fixture.cleanup(self.d)
        self.assertEqual(self.engine.removed, [])

    def test_container_ownership_change_aborts_before_stop_or_deletion(self):
        self.configure()
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

                def req(_path):
                    return b""

                def docker(*args):
                    # The workspace file is read with docker exec; the rest are counters.
                    return "vpn-e2e-workspace" if "cat" in args else rows

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
                    patch.object(traffic, "docker", side_effect=docker),
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


class StartTests(unittest.TestCase):
    def start(self, goarch="arm64"):
        with tempfile.TemporaryDirectory() as tmp:
            d = pathlib.Path(tmp)
            fixture.write(d / "fixture.json", {"mode": "fixture", "Scope": "scope", "SelfID": "owned-supervisor"})
            commands = []

            def make(*args, **kwargs):
                commands.append(args)
                if args[0] == "engine-goarch":
                    if not goarch:
                        raise RuntimeError("make -s failed: the engine's architecture riscv64 is not supported")
                    return goarch
                return ""

            with (
                patch.dict(os.environ, {"CC_TEST_IMAGE": "current-image:validation"}),
                patch.object(fixture, "run") as build,
                patch.object(fixture, "make", side_effect=make),
                patch.object(fixture, "wait_file", return_value={}),
            ):
                fixture.start(d)
            return d, build, commands

    def test_runs_the_fixture_binary_with_make_run_and_data_at_slash_data(self):
        d, build, commands = self.start()
        self.assertEqual(build.call_args.kwargs["env"]["GOARCH"], "arm64")
        self.assertEqual(build.call_args.kwargs["env"]["GOOS"], "linux")
        self.assertIn(("cli-linux", "base"), commands)
        run = next(c for c in commands if c[0] == "run")
        self.assertIn("NAME=owned-supervisor", run)
        self.assertIn(f"DATA={d / 'data'}", run)
        self.assertIn(f"BIN={d / 'supervisor.test'}", run)
        run_args = next(a for a in run if a.startswith("RUN_ARGS="))
        self.assertIn("-e CC_TEST_IMAGE=current-image:validation", run_args)
        self.assertIn(f"-v {d}:{fixture.INSIDE}", run_args)
        self.assertIn(f"-e DESKYARD_VPN_E2E_DIR={fixture.INSIDE}", run_args)
        self.assertIn("--label deskyard.vpn.e2e-fixture=scope", run_args)
        self.assertNotIn(f"{d}:{d}", run_args)

    def test_refuses_an_unknown_engine_architecture(self):
        with self.assertRaisesRegex(RuntimeError, "architecture"):
            self.start(goarch="")


if __name__ == "__main__":
    unittest.main()
