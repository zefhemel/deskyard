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
            self.assertTrue(source.startswith((ROOT / 'client_defaults.js').read_text()), 'every executable bundle needs the bare-client contract before its hook call sites')
            self.assertIn('window.__deskyardHooks.transport(u)', source)
            import subprocess
            for name in module.CORE_FILES:
                subprocess.run(['node', str(ROOT / 'test_mac_alt.js'), str(client / name)], check=True)
            self.assertEqual(source.count('navigator.mediaDevices.getUserMedia('), 1, 'only the native default may acquire capture directly')
            self.assertIn('!window.__deskyardHooks.pointerMoveOnly&&', source)
            with self.assertRaisesRegex(ValueError, 'SHA-256'):
                module.patch(client)

    def test_bare_client_defaults_preserve_native_behavior_and_existing_adapter(self):
        import subprocess
        defaults = ROOT / 'client_defaults.js'
        self.assertTrue(defaults.exists(), 'bare clients need native v1 hook defaults')
        subprocess.run(['node', '-e', r'''
const assert = require('node:assert/strict');
const vm = require('node:vm');
const source = require('node:fs').readFileSync(process.argv[1], 'utf8');
const calls = [];
const stream = { native: 'capture-result' };
class NativeWorklet { constructor(...args) { this.args = args; } }
const native = {
  navigator: { mediaDevices: { getUserMedia(c) { calls.push(['capture', c]); return stream; } } },
  AudioWorkletNode: NativeWorklet,
  alert(text) { calls.push(['alert', text]); },
};
native.window = native;
const snapshot = { ...native };
vm.runInNewContext(source, native);
const hooks = native.__deskyardHooks;
assert.equal(hooks.version, 1);
assert.equal(hooks.allowInsecure, false);
assert.equal(hooks.pointerMoveOnly, false);
const url = 'ws://example/api/websockets?token=original';
assert.equal(hooks.url(url), url);
for (const data of ['STOP_VIDEO', 'START_AUDIO', 'STOP_AUDIO', 'anything', new ArrayBuffer(3)]) {
  let sent;
  hooks.send({ send(value) { sent = value; } }, data);
  assert.equal(sent, data);
}
const constraints = { audio: true, video: { width: 720 } };
assert.equal(hooks.capture(constraints), stream);
assert.equal(calls.at(-1)[1], constraints);
const options = { preventScroll: true };
hooks.focus({ focus(value) { calls.push(['focus', value]); } }, options);
assert.equal(calls.at(-1)[1], options);
assert.equal(hooks.touchMode(true), true);
assert.equal(hooks.touchMode(false), false);
assert.deepEqual(Array.from(hooks.points(1,2,3,4)), [1,2,3,4]);
assert.deepEqual(Array.from(hooks.trackpadOrigin({})), [0,0]);
assert.equal(hooks.zoom(2,3,4), false);
assert.equal(hooks.pan(30,true), false);
const ctx = { audioWorklet: { addModule(url) { return url; } } };
assert.equal(hooks.addWorklet(ctx,'processor.js'), 'processor.js');
const node = hooks.workletNode(ctx,'processor',options);
assert.ok(node instanceof NativeWorklet);
assert.deepEqual(node.args, [ctx,'processor',options]);
hooks.alert('message');
assert.deepEqual(calls.at(-1), ['alert','message']);
for (const key of ['transport','input','cursor','playback','playbackOutput','audioTick']) assert.doesNotThrow(() => hooks[key]());
for (const [key,value] of Object.entries(snapshot)) assert.equal(native[key], value);
const existing = { supervisor: true };
native.__deskyardHooks = existing;
vm.runInNewContext(source, native);
assert.equal(native.__deskyardHooks, existing);
''', str(defaults)], check=True)
