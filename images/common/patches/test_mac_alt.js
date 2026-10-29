// Exercise the pinned client's real key handlers and wire output. Browser/key
// decoding and unrelated UI/timer behavior are supplied at their boundaries.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');
function method(start, end) {
  const at = source.indexOf(start);
  assert.ok(at >= 0, start);
  const stop = source.indexOf(end, at + start.length);
  assert.ok(stop > at, end);
  return source.slice(at, stop);
}
const methods = [
  method('_handleKeyDown(t){', '_escapeHatch(e){'),
  method('_handleKeyUp(e){', '_updateCompositionText(e){'),
  method('_sendKeyEvent(e,t,n){', '_startKeyHeartbeat(){'),
  method('_sendMomentaryKey(e){', '_noteChordKey(e){'),
].join('\n');
const P = { XK_Alt_L: 65513, XK_Alt_R: 65514, XK_Mode_switch: 65406,
  XK_ISO_Level3_Shift: 65027, XK_Super_L: 65515, XK_Super_R: 65516 };
for (const platform of ['Mac', 'Linux', 'Windows', 'IOS']) {
  const Input = vm.runInNewContext(`(class { ${methods} })`, {
    P, e: { macCmdAsCtrl: false }, Mt: {}, Nt: {}, j: '',
    xt: { AltLeft: 'Alt', AltRight: 'Alt', ControlLeft: 'Control', ShiftLeft: 'Shift' },
    I: { isMac: () => platform === 'Mac', isIOS: () => platform === 'IOS',
      isWindows: () => platform === 'Windows', isChrome: () => true },
    R: { getKeyCode: e => e.code, getKey: e => e.key, getKeysym: e => e.keysym },
    Tt: e => e.isTrusted !== false, Pt() {}, console,
  });
  for (const [code, keysym, key] of [
    ['AltLeft', 65513, 'Alt'], ['AltRight', 65514, 'Alt'],
    ['ControlLeft', 65507, 'Control'], ['ShiftLeft', 65505, 'Shift'],
  ]) {
    // Windows Control has an unrelated AltGr deferral; leave that path alone.
    if (platform === 'Windows' && key === 'Control') continue;
    const input = new Input();
    const sent = [];
    Object.assign(input, {
      _keyDownList: {}, _altKeysymByCode: new Map(),
      send: message => sent.push(message),
      _escapeHatch: () => false, _targetHasClass: () => false,
      _guac_markEvent: () => true, _releaseDesyncedModifiers() {},
      _composesText: () => false, _noteChordKey() {},
      _startKeyHeartbeat() {}, _stopKeyHeartbeat() {},
    });
    const event = { code, keysym, key, isTrusted: true };
    input._handleKeyDown(event);
    assert.deepEqual(sent, [`kd,${keysym}`], `${platform} ${code} press`);
    assert.equal(input._keyDownList[code], keysym, `${platform} ${code} stays held for mouse input`);
    input._handleKeyUp(event);
    assert.deepEqual(sent, [`kd,${keysym}`, `ku,${keysym}`], `${platform} ${code} release`);
    assert.equal(Object.keys(input._keyDownList).length, 0);
  }
}
