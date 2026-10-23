"""Regression check for Ctrl-C reaching the whole demo process group."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


class DemoCleanupTest(unittest.TestCase):
    def test_terminal_interrupt_removes_its_desktop(self):
        self.check_interrupt(after_ready=True)

    def test_interrupt_during_creation_removes_its_desktop(self):
        self.check_interrupt(after_ready=False)

    def check_interrupt(self, after_ready):
        state = Path(tempfile.mkdtemp(prefix='zterm-interrupt-check-'))
        log = (state / 'runner.log').open('w')
        proc = subprocess.Popen([sys.executable, str(Path(__file__).with_name('demo.py')),
                                 '--state-dir', str(state)], start_new_session=True,
                                stdout=log, stderr=log)
        desktop = None
        try:
            for _ in range(240):
                record = state / ('access.json' if after_ready else 'owner.json')
                if record.exists():
                    try:
                        owner = json.loads(record.read_text())
                    except json.JSONDecodeError:
                        continue
                    if after_ready or owner.get('job'):
                        desktop = owner['desktop']
                        break
                if proc.poll() is not None:
                    self.fail((state / 'runner.log').read_text())
                time.sleep(0.25)
            self.assertIsNotNone(desktop, 'Demo never became ready')
            os.killpg(proc.pid, signal.SIGINT)
            proc.wait(timeout=30)
            self.assertEqual(proc.returncode, 0, (state / 'runner.log').read_text())
            check = subprocess.run(['docker', 'inspect', 'deskyard-' + desktop], capture_output=True)
            self.assertNotEqual(check.returncode, 0, 'Ctrl-C left the demo desktop running')
        finally:
            if proc.poll() is None:
                os.killpg(proc.pid, signal.SIGTERM)
                proc.wait(timeout=30)
            if desktop:
                subprocess.run(['docker', 'rm', '-f', '-v', 'deskyard-' + desktop], capture_output=True)
                subprocess.run(['docker', 'network', 'rm', 'deskyard-' + desktop], capture_output=True)
            log.close()


if __name__ == '__main__':
    unittest.main()
