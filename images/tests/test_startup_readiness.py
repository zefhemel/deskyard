"""Local startup-script regressions; no Docker or running desktop required.

Run: python3 -m unittest discover -s images/tests -p test_startup_readiness.py
"""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "common/rootfs/usr/local/bin/start-selkies"


class StartupReadinessTests(unittest.TestCase):
    def check_barrier(self, display_ready, session_ready):
        # Compress the wait, leaving the production branch and command ordering
        # intact. Crossing the readiness barrier must be observable via pactl.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            if display_ready:
                (root / "xvfb-ready").touch()
            commands = root / "bin"
            commands.mkdir()
            for name, body in {
                "seq": "printf '1\\n'",
                "sleep": ":",
                "timeout": 'shift 2; exec "$@"',
                "desktop-ready": "exit " + ("0" if session_ready else "1"),
                "pactl": 'touch "$XDG_RUNTIME_DIR/audio-started"; exit 1',
            }.items():
                command = commands / name
                command.write_text("#!/bin/sh\n" + body + "\n")
                command.chmod(0o755)
            env = {**os.environ, "XDG_RUNTIME_DIR": str(root), "PATH": str(commands) + ":/usr/bin:/bin"}
            result = subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((root / "audio-started").exists(), display_ready and session_ready, result.stderr)

    def test_unready_display_never_starts_streamer_or_audio_setup(self):
        self.check_barrier(False, False)

    def test_ready_x_without_desktop_panel_never_starts_audio_setup(self):
        self.check_barrier(True, False)

    def test_ready_desktop_can_proceed_to_audio_setup(self):
        self.check_barrier(True, True)


if __name__ == "__main__":
    unittest.main()
