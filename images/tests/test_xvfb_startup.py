"""Startup barrier and failure cleanup, without Docker or the user's display.

Run: python3 -m unittest discover -s images/tests -p test_xvfb_startup.py
"""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "common/rootfs/usr/local/bin/start-xvfb"


class XvfbStartupTests(unittest.TestCase):
    def run_failure(self, failure):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            commands = root / "bin"
            commands.mkdir()
            bodies = {
                "Xvfb": '[ "$FAILURE" != ignore_term ] || trap "" TERM; echo $$ > "$XDG_RUNTIME_DIR/x-pid"; exec /bin/sleep 30',
                "seq": "printf '1\\n'",
                "sleep": ":",
                "xdpyinfo": '[ "$FAILURE" != hung ] || exec /bin/sleep 30; [ "$FAILURE" != handshake ]',
                "cvt": 'echo "Modeline \\"1280x800_60.00\\" 83.50 1280 1352 1480 1680 800 803 809 831 -hsync +vsync"',
                "xrandr": '''
case "$*" in
  *--output*) [ "$FAILURE" != mode ] && [ "$FAILURE" != ignore_term ] || exit 1;;
  *--current*)
    if [ "$FAILURE" = dimensions ]; then
      echo 'Screen 0: minimum 1 x 1, current 3840 x 2160, maximum 3840 x 2160'
    else
      echo 'Screen 0: minimum 1 x 1, current 1280 x 800, maximum 3840 x 2160'
    fi;;
esac
''',
                # The portable runner compresses deadlines, while executing
                # actual blocking children. Native GNU timeout is exercised in
                # the isolated Linux container check.
                "timeout": None,
                "touch": 'echo published > "$XDG_RUNTIME_DIR/published"; exec /usr/bin/touch "$@"',
            }
            for name, body in bodies.items():
                command = commands / name
                if name == "timeout":
                    command.write_text(f"#!{sys.executable}\n" + '''
import os, subprocess, sys
from pathlib import Path
args = sys.argv[1:]
Path(os.environ["XDG_RUNTIME_DIR"], "deadlines").open("a").write(" ".join(args) + "\\n")
if args[0].startswith("--kill-after="):
    args.pop(0)
args.pop(0)
p = subprocess.Popen(args)
try:
    sys.exit(p.wait(timeout=0.5))
except subprocess.TimeoutExpired:
    p.kill()
    p.wait()
    sys.exit(124)
''')
                else:
                    command.write_text("#!/bin/sh\n" + body + "\n")
                command.chmod(0o755)
            env = {**os.environ, "DISPLAY": ":1", "XDG_RUNTIME_DIR": str(root),
                   "PATH": str(commands) + ":/usr/bin:/bin", "FAILURE": failure}
            process = subprocess.Popen(["bash", str(SCRIPT)], env=env,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                _, stderr = process.communicate(timeout=10)
                self.assertNotEqual(process.returncode, 0, stderr)
                expected = {"mode": "selecting mode", "ignore_term": "selecting mode",
                            "dimensions": "was not applied", "handshake": "display handshake",
                            "hung": "display handshake"}[failure]
                self.assertIn(expected, stderr)
                self.assertFalse((root / "published").exists(), stderr)
                self.assertFalse((root / "xvfb-ready").exists(), stderr)
                self.assertTrue((root / "deadlines").exists(), "X operations require deadlines")
                pid = int((root / "x-pid").read_text())
                with self.assertRaises(ProcessLookupError):
                    os.kill(pid, 0)
            finally:
                if process.poll() is None:
                    process.kill()
                if (root / "x-pid").exists():
                    try:
                        os.kill(int((root / "x-pid").read_text()), 9)
                    except ProcessLookupError:
                        pass
                process.communicate()

    def test_failed_mode_never_publishes_readiness_and_reaps_x(self):
        self.run_failure("mode")

    def test_wrong_dimensions_never_publish_readiness_and_reap_x(self):
        self.run_failure("dimensions")

    def test_failed_start_kills_x_even_when_it_ignores_term(self):
        self.run_failure("ignore_term")

    def test_hung_handshake_hits_deadline_and_reaps_x(self):
        self.run_failure("hung")

    def test_failed_handshake_never_publishes_readiness_and_reaps_x(self):
        self.run_failure("handshake")


if __name__ == "__main__":
    unittest.main()
