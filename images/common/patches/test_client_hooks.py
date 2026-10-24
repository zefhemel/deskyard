"""Run against the pinned wheel: python3 -m unittest discover -s images/common/patches -p 'test_client_hooks.py'."""
import importlib.util
import pathlib
import os
import tempfile
import unittest
import zipfile

ROOT = pathlib.Path(__file__).parent

class ClientHooksTest(unittest.TestCase):
    def test_pinned_client_patch_and_reject_changed_client(self):
        spec = importlib.util.spec_from_file_location('client_hooks', ROOT / 'client_hooks.py')
        self.assertTrue(spec.origin and pathlib.Path(spec.origin).exists(), 'the versioned hook patcher must exist')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        wheel = pathlib.Path(os.environ.get('SELKIES_WHEEL', '/private/tmp/selkies-2.1.0-py3-none-any.whl'))
        if not wheel.exists():
            self.skipTest('set SELKIES_WHEEL or cache the pinned wheel for the package contract test')
        with tempfile.TemporaryDirectory() as tmp:
            with zipfile.ZipFile(wheel) as archive:
                archive.extractall(tmp)
            client = pathlib.Path(tmp) / 'selkies/selkies_web'
            module.patch(client)
            self.assertIn('deskyard-client-contract', (client / 'index.html').read_text())
            source = (client / 'src/selkies-core.js').read_text()
            self.assertIn('window.__deskyardHooks.transport(u)', source)
            self.assertNotIn('navigator.mediaDevices.getUserMedia(', source)
            self.assertIn('!window.__deskyardHooks.pointerMoveOnly&&', source)
            with self.assertRaisesRegex(ValueError, 'SHA-256'):
                module.patch(client)
