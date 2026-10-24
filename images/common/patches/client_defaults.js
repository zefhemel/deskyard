// A direct image client has no supervisor adapter. Keep Selkies' native
// behavior in that case; a supervisor-supplied v1 contract takes precedence.
// These calls are local extension points, never changes to browser primitives.
window.__deskyardHooks ??= {
  version: 1,
  allowInsecure: false,
  pointerMoveOnly: false,
  transport() {},
  url: (url) => url,
  send: (transport, data) => transport.send(data),
  capture: (constraints) => navigator.mediaDevices.getUserMedia(constraints),
  input() {},
  touchMode: (trackpad) => trackpad,
  points: (x1, y1, x2, y2) => [x1, y1, x2, y2],
  zoom: () => false,
  pan: () => false,
  trackpadOrigin: () => [0, 0],
  cursor() {},
  focus: (element, options) => element.focus(options),
  playback() {},
  playbackOutput() {},
  audioTick() {},
  addWorklet: (context, url) => context.audioWorklet.addModule(url),
  workletNode: (context, name, options) => new AudioWorkletNode(context, name, options),
  alert: (text) => window.alert(text),
};
