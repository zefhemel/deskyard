"""Integration checks for the disposable terminal image (requires Docker)."""
import os
import json
import subprocess
import time
import unittest
import uuid

IMAGE = os.environ.get("TERMINAL_IMAGE", "deskyard-terminal-prototype:local")


def docker(*args, check=True):
    return subprocess.run(["docker", *args], capture_output=True, text=True, check=check)


class TerminalImageTest(unittest.TestCase):
    def test_background_session_survives_client_and_shares_state(self):
        name = "zterm-check-" + uuid.uuid4().hex[:8]
        docker("run", "-d", "--name", name, "--entrypoint", "sleep", IMAGE, "120")
        try:
            # No desktop boot needed: exercise the session boundary as UID 1000.
            def run(*args, check=True):
                return docker("exec", "-u", "user", "-e", "TERM=xterm-256color", "-e", "XDG_RUNTIME_DIR=/tmp", name, *args, check=check)

            result = run("shared-terminal", "--background", "probe", check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            # A second invocation must attach to the existing session, preserving it.
            before = run("zellij", "list-sessions", "--short", "--no-formatting").stdout
            self.assertIn("probe", before)
            again = run("shared-terminal", "--background", "probe", check=False)
            self.assertEqual(again.returncode, 0, again.stderr)
            panes = json.loads(run("zellij", "--session", "probe", "action", "list-panes", "--json").stdout)
            self.assertFalse(any(p["is_plugin"] and not p["is_suppressed"] for p in panes), "The shared terminal must not render an engine status-bar plugin")
            layout = run("zellij", "--session", "probe", "action", "dump-layout").stdout
            self.assertNotIn("zellij:tab-bar", layout)
            self.assertNotIn("zellij:status-bar", layout)
            pane = "terminal_" + str(next(p["id"] for p in panes if not p["is_plugin"]))
            run("zellij", "--session", "probe", "action", "write-chars", "--pane-id", pane, "export PROTOTYPE_NONCE=still-here; echo $PROTOTYPE_NONCE > /workspace/probe-result")
            run("zellij", "--session", "probe", "action", "write", "--pane-id", pane, "13")
            for _ in range(40):
                result = run("cat", "/workspace/probe-result", check=False)
                if result.stdout.strip() == "still-here":
                    break
                time.sleep(0.25)
            self.assertEqual(result.stdout.strip(), "still-here")
            self.assertEqual(before, run("zellij", "list-sessions", "--short", "--no-formatting").stdout)
            run("zellij", "--session", "probe", "action", "new-tab")
            layout = run("zellij", "--session", "probe", "action", "dump-layout").stdout
            self.assertNotIn("zellij:tab-bar", layout)
            self.assertNotIn("zellij:status-bar", layout)
            bad = run("shared-terminal", "bad/session", check=False)
            self.assertNotEqual(bad.returncode, 0)
        finally:
            docker("rm", "-f", "-v", name, check=False)


if __name__ == "__main__":
    unittest.main()
