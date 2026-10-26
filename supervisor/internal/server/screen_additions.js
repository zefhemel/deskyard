// Legacy compatibility bridge for desktop images without the v1 hook contract.
// New images use supervisor/web/src/selkies/adapter.ts. Keep this bridge only
// until support for pre-contract Selkies 2.1.0 images is explicitly retired.
// Deskyard's additions to Selkies' web client. The supervisor serves this as
// /<name>/screen/deskyard.js and loads it before any of Selkies' scripts.
//
// - Hides pointerrawupdate: inside an iframe Chrome reports its clientX/Y
//   relative to the top-level page, so drags landed off by our bar's height.
//   Without it Selkies falls back to pointermove.
// - Never sends STOP_VIDEO: a tab going to the background must not stop the
//   desktop's stream for everyone else. Never sends Selkies' own
//   START_AUDIO or STOP_AUDIO either: the desktop page turns sound on or off.
// - Adds ?tab=<id> to the stream URL, so the supervisor can tie this tab's
//   mic badge to its stream. The id is new on every load of the client.
// - Makes every load of the client its own Selkies client: Selkies keeps
//   its tab id in sessionStorage, which a duplicated browser tab copies, and
//   a page with the owner's tab id takes the display over from it.
// - Forgets Selkies' own saved stream settings and screen size at load: the
//   desktop page applies this browser's choices itself, and only where they
//   should apply (the display's owner).
// - Follows the display: whether this page owns it (Selkies' first page sets
//   the screen size and keeps its encoding; later pages join beside it), the
//   size it streams at, and the settings it streams with.
// - Lets Selkies capture the mic only while the desktop page has asked for
//   it, on the current connection: Selkies' client turns it on by itself in
//   places (it restores it after a reconnect, and on a desktop app's
//   demand), and a mic must never go live unseen. It never captures video.
// - Follows sound and mic (media()), and turns Selkies' alert()s about the
//   mic into messages for the page.
// - Lets the desktop page's ?avsync=1 readout hear the sound as it reaches
//   the speakers (watchSound()).
// - Keeps Selkies from taking the focus out of the desktop page: it focuses
//   its text-input host when a stream attaches, which blurred the page and
//   closed its open menus. That focus waits until this frame has the focus.
// - Lets Selkies' client run over plain http (an insecure context), on its
//   JPEG and WASM fallbacks, with its sound played without an AudioWorklet.
// - window.deskyardScreen, the hooks the desktop page drives Selkies with.
(() => {
  "use strict";

  // --- plain http ---------------------------------------------------------------
  // Reached over plain http on a LAN address, the page isn't a secure context:
  // the browser has no AudioWorklet, mic or async clipboard, and no
  // WebCodecs either in Chromium and Firefox (WebKit keeps them).
  // Selkies' client then refuses to start ("requires a secure connection"),
  // although it has a fallback for each of the first two: with no
  // VideoDecoder it pins the stream to JPEG, and with no AudioDecoder it
  // decodes Opus with libopus in WASM. So it's told the context is secure
  // (its other checks of isSecureContext also need navigator.clipboard,
  // which isn't there), and its sound playback's AudioWorklet runs on a
  // ScriptProcessorNode (below). The page reads deskyardScreen.insecure.
  const insecure = window.isSecureContext === false;
  if (insecure) {
    try { Object.defineProperty(window, "isSecureContext", { configurable: true, get: () => true }); } catch {}
  }

  // An AudioWorklet stand-in for a context that has none: addModule runs the
  // module's code on the page, with the worklet scope's globals, and an
  // AudioWorkletNode is a ScriptProcessorNode that calls its processor's
  // process() for each 128-frame block, its port one end of a MessageChannel.
  // Enough for Selkies' playback (one output, messages on the port) and our
  // sound tap; it plays on the page's main thread, so a busy page can make
  // it stutter.
  const Base = window.BaseAudioContext || window.AudioContext;
  if (Base && !("audioWorklet" in Base.prototype) && typeof Base.prototype.createScriptProcessor === "function") {
    const registered = new WeakMap(); // context -> Map(name -> processor class)
    let port = null; // the port of the processor being made
    class AudioWorkletProcessor {
      constructor() { this.port = port; }
    }
    Object.defineProperty(Base.prototype, "audioWorklet", {
      configurable: true,
      get() {
        const ctx = this;
        return {
          async addModule(url) {
            const code = await (await fetch(url)).text();
            if (!registered.has(ctx)) registered.set(ctx, new Map());
            const scope = {
              AudioWorkletProcessor,
              registerProcessor: (name, cls) => registered.get(ctx).set(name, cls),
              sampleRate: ctx.sampleRate,
              get currentTime() { return ctx.currentTime; },
              get currentFrame() { return Math.round(ctx.currentTime * ctx.sampleRate); },
            };
            new Function("scope", `with (scope) {\n${code}\n}`)(scope);
          },
        };
      },
    });
    window.AudioWorkletNode = function AudioWorkletNode(ctx, name, options = {}) {
      const cls = registered.get(ctx)?.get(name);
      if (!cls) throw new DOMException(`No AudioWorkletProcessor "${name}"`, "InvalidStateError");
      const channels = (options.outputChannelCount && options.outputChannelCount[0]) || 2;
      const node = ctx.createScriptProcessor(1024, 1, channels);
      const ch = new MessageChannel();
      port = ch.port2;
      let proc;
      try { proc = new cls(options); } finally { port = null; }
      node.port = ch.port1;
      const BLOCK = 128;
      const ins = [new Float32Array(BLOCK)];
      const outs = Array.from({ length: channels }, () => new Float32Array(BLOCK));
      let running = true;
      node.onaudioprocess = (e) => {
        const input = e.inputBuffer.getChannelData(0);
        for (let at = 0; at < e.outputBuffer.length; at += BLOCK) {
          ins[0].set(input.subarray(at, at + BLOCK));
          for (const o of outs) o.fill(0);
          if (running) {
            try { running = proc.process([ins], [outs], {}) !== false; } catch (err) { running = false; console.warn("Deskyard: worklet stand-in:", err); }
          }
          for (let c = 0; c < channels; c++) e.outputBuffer.getChannelData(c).set(outs[c], at);
        }
      };
      return node;
    };
  }

  // --- Selkies' tab id ----------------------------------------------------------
  // Gone before Selkies' code reads it, so it makes a new one for this load.
  // (A plain reload's old connection closes on unload anyway.)
  try { sessionStorage.removeItem("selkies_tab_id"); } catch {}

  // --- Selkies' saved settings ---------------------------------------------------
  // Selkies keeps its stream settings and manual screen size in localStorage
  // under a prefix made from the page's URL, and sends them in its first
  // SETTINGS, and a page beside the owner saves the display's settings as its
  // own. Gone before Selkies' code reads them; the desktop page sends what
  // this browser chose once it knows whether it owns the display.
  const saved = /_(encoder|framerate|video_crf|video_fullcolor|video_10bit|video_streaming_mode|jpeg_quality|paint_over_jpeg_quality|use_paint_over_quality|video_paintover_crf|video_paintover_burst_frames|video_bitrate|rate_control_mode|use_cpu|cpu_encode_threading|manual_resolution|manual_width|manual_height|scaleLocallyManual)(_pick)?$/;
  try {
    // Selkies' prefix for this page, e.g. http___127.0.0.1_8080_dev_screen__.
    const ours = (key) => /^https?___.*_screen__/.test(key);
    for (const key of Object.keys(localStorage)) {
      if (ours(key) && saved.test(key)) localStorage.removeItem(key);
    }
    for (const key of Object.keys(sessionStorage)) {
      if (/_(display_settings_tab|pick_tab)$/.test(key)) sessionStorage.removeItem(key);
    }
  } catch {}

  // --- pointerrawupdate -------------------------------------------------------
  try { delete Window.prototype.onpointerrawupdate; } catch {}
  try { delete window.onpointerrawupdate; } catch {}
  const addListener = EventTarget.prototype.addEventListener;
  EventTarget.prototype.addEventListener = function (type, ...rest) {
    if (type === "pointerrawupdate") return;
    return addListener.call(this, type, ...rest);
  };

  // --- focus ------------------------------------------------------------------
  // An element's focus() while this frame isn't focused would pull the focus
  // out of the desktop page; it's applied when the frame gets the focus (the
  // page's focusFrame, or a click into the screen).
  const focusElement = HTMLElement.prototype.focus;
  let pendingFocus = null;
  HTMLElement.prototype.focus = function (...args) {
    if (document.hasFocus()) return focusElement.apply(this, args);
    pendingFocus = { el: this, args };
  };
  addListener.call(window, "focus", () => {
    const p = pendingFocus;
    pendingFocus = null;
    if (p && p.el.isConnected) focusElement.apply(p.el, p.args);
  });

  // --- the tab id -------------------------------------------------------------
  // New on every load of the client, so a duplicated browser tab (which
  // copies sessionStorage) never shares it. The desktop page reads it as
  // frame.contentWindow.deskyardScreen.tab.
  const tab = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, "0")).join("");

  const withTab = (url) => {
    try {
      const u = new URL(url, location.href);
      if (!/\/api\/websockets\/?$/.test(u.pathname)) return url;
      u.searchParams.set("tab", tab);
      return u.href;
    } catch {
      return url;
    }
  };

  // Selkies opens the stream in a worker ({type: "open", url}) or, without
  // workers, on the page.
  const workerPost = Worker.prototype.postMessage;
  const streamWorkers = new WeakSet();
  Worker.prototype.postMessage = function (msg, ...rest) {
    if (msg && msg.type === "open" && typeof msg.url === "string") {
      msg = { ...msg, url: withTab(msg.url) };
      if (!streamWorkers.has(this)) {
        streamWorkers.add(this);
        addListener.call(this, "message", (e) => {
          const m = e.data;
          const sound = m && (m.type === "audioTick" ||
            (m.type === "message" && m.data instanceof ArrayBuffer && m.data.byteLength > 1 && new Uint8Array(m.data, 0, 1)[0] === 1));
          if (sound) media.soundAt = performance.now();
        });
      }
    }
    return workerPost.call(this, msg, ...rest);
  };
  const PageWebSocket = window.WebSocket;
  window.WebSocket = class extends PageWebSocket {
    constructor(url, ...rest) {
      super(withTab(String(url)), ...rest);
    }
  };

  // --- sound and mic ------------------------------------------------------------
  // What Selkies' client is doing, from its own status messages (posted on
  // this window): sound is its audio pipeline for this page, mic its
  // capture. playing: its playback AudioContext is running (a browser
  // holds it back until the user clicks). soundAt: when the stream last
  // brought this page sound (performance.now(); 0 for never), from the
  // stream's worker, which passes sound straight to the decoder and tells
  // the page about once a second.
  let media = { sound: false, mic: false, playing: false, soundAt: 0 };
  const mediaListeners = new Set();
  const setMedia = (m) => {
    const next = { ...media, ...m };
    if (next.sound === media.sound && next.mic === media.mic && next.playing === media.playing) {
      media = next;
      return;
    }
    media = next;
    for (const fn of mediaListeners) try { fn(media); } catch {}
  };
  addListener.call(window, "message", (e) => {
    const m = e.data;
    if (e.source !== window || !m || typeof m !== "object") return;
    if (m.type === "sidebarButtonStatusUpdate" || m.type === "pipelineStatusUpdate") {
      const d = {};
      if (typeof m.audio === "boolean") d.sound = m.audio;
      if (typeof m.microphone === "boolean") d.mic = m.microphone;
      setMedia(d);
    }
  });

  // Selkies' AudioContexts: its playback one, and the ones it captures the
  // mic with (they take a MediaStreamSource). Ours, for the level meter, is
  // neither.
  const contexts = new Set();
  const capturing = new WeakSet();
  let makingMeter = false; // while our own meter's context is made
  const playback = () => {
    let last = null;
    for (const c of contexts) {
      if (c.state === "closed") contexts.delete(c);
      else if (!capturing.has(c)) last = c;
    }
    return last;
  };
  const notePlaying = () => setMedia({ playing: playback()?.state === "running" });
  for (const name of ["AudioContext", "webkitAudioContext"]) {
    const Base = window[name];
    if (typeof Base !== "function") continue;
    window[name] = class extends Base {
      constructor(...args) {
        super(...args);
        if (makingMeter) return;
        contexts.add(this);
        addListener.call(this, "statechange", notePlaying);
        notePlaying();
      }
      createMediaStreamSource(...args) {
        capturing.add(this);
        notePlaying();
        return super.createMediaStreamSource(...args);
      }
    };
  }

  // The sound as it reaches the speakers, for the ?avsync=1 readout: Selkies
  // plays through a GainNode into its playback context's destination. Once
  // something watches, a worklet beside that node reports each block's level.
  const soundWatchers = new Set();
  const tapped = new WeakSet();
  let playbackOut = null;
  const tapCode = `registerProcessor("deskyard-sound-tap", class extends AudioWorkletProcessor {
    process(inputs) {
      const ch = inputs[0] && inputs[0][0];
      if (ch) {
        let s = 0;
        for (let i = 0; i < ch.length; i++) s += ch[i] * ch[i];
        this.port.postMessage([currentTime, Math.sqrt(s / ch.length)]);
      }
      return true;
    }
  });`;
  const connectNode = AudioNode.prototype.connect;
  const tapSound = (node) => {
    if (!node || tapped.has(node) || soundWatchers.size === 0) return;
    tapped.add(node);
    const ctx = node.context;
    const url = URL.createObjectURL(new Blob([tapCode], { type: "application/javascript" }));
    ctx.audioWorklet.addModule(url).then(() => {
      URL.revokeObjectURL(url);
      const tap = new AudioWorkletNode(ctx, "deskyard-sound-tap");
      tap.port.onmessage = (e) => {
        const [t, rms] = e.data;
        const ts = ctx.getOutputTimestamp();
        if (!ts.performanceTime) return;
        // When this block reaches the speakers, in ms since the epoch.
        const at = performance.timeOrigin + ts.performanceTime + (t - ts.contextTime) * 1000;
        for (const fn of soundWatchers) try { fn(at, rms); } catch {}
      };
      // The worklet runs only while its output goes somewhere: silently.
      const silent = ctx.createGain();
      silent.gain.value = 0;
      connectNode.call(node, tap);
      connectNode.call(tap, silent);
      connectNode.call(silent, ctx.destination);
    }, (err) => console.warn("Deskyard: no sound tap:", err));
  };
  AudioNode.prototype.connect = function (dest, ...rest) {
    const r = connectNode.call(this, dest, ...rest);
    if (this instanceof GainNode && dest instanceof AudioDestinationNode) {
      playbackOut = this;
      tapSound(this);
    }
    return r;
  };

  // Captures: only the mic, when the page allowed it, on this connection.
  const allowed = { microphone: false };
  let micDevice = "";
  let micStream = null;
  const md = navigator.mediaDevices;
  if (md && typeof md.getUserMedia === "function") {
    const getUserMedia = md.getUserMedia.bind(md);
    md.getUserMedia = async (constraints = {}) => {
      const c = { ...constraints };
      if (c.video || (c.audio && !allowed.microphone)) {
        throw new DOMException("The desktop page hasn't turned this on", "NotAllowedError");
      }
      if (c.audio) {
        // Selkies asks with echo cancellation and noise suppression on; kept.
        c.audio = { ...(typeof c.audio === "object" ? c.audio : {}) };
        if (micDevice) c.audio.deviceId = { exact: micDevice };
        else delete c.audio.deviceId;
      }
      const stream = await getUserMedia(c);
      if (c.audio) micStream = stream;
      return stream;
    };
  }
  const forbid = () => {
    allowed.microphone = false;
  };

  // Selkies tells capture failures with alert(); the page gets them as
  // {kind, text}, and only for a capture it asked for.
  const alerts = new Set();
  window.alert = (text) => {
    text = String(text);
    console.warn("Selkies:", text);
    const kind = /^Microphone/.test(text) ? "microphone" : null;
    if (!kind || !allowed[kind]) return;
    for (const fn of alerts) try { fn({ kind, text }); } catch {}
  };

  let meter = null;
  let meterContext = null;
  const micLevel = () => {
    if (!media.mic || !micStream || micStream.getAudioTracks().every((t) => t.readyState !== "live")) return 0;
    try {
      if (!meter || meter.stream !== micStream) {
        if (meter) meter.source.disconnect();
        if (!meterContext) {
          makingMeter = true;
          try { meterContext = new AudioContext(); } finally { makingMeter = false; }
        }
        if (meterContext.state === "suspended") meterContext.resume().catch(() => {});
        const analyser = meterContext.createAnalyser();
        analyser.fftSize = 1024;
        const source = meterContext.createMediaStreamSource(micStream);
        source.connect(analyser);
        meter = { stream: micStream, source, analyser, data: new Float32Array(analyser.fftSize) };
      }
      meter.analyser.getFloatTimeDomainData(meter.data);
      let sum = 0;
      for (const v of meter.data) sum += v * v;
      // On a decibel scale: -60 dB (a quiet room) to 0.
      const db = 10 * Math.log10(sum / meter.data.length || 1e-12);
      return Math.min(1, Math.max(0, (db + 60) / 60));
    } catch {
      return 0;
    }
  };

  // --- the transport: STOP_VIDEO and the connection state --------------------
  let state = "connecting";
  const stateListeners = new Set();
  const setState = (s) => {
    if (s === state) return;
    state = s;
    for (const fn of stateListeners) try { fn(s); } catch {}
  };

  // The display, as this connection learns it from Selkies' messages:
  // - owner: true once this page owns the display, false beside its owner,
  //   null until known. After the page's first SETTINGS the server answers a
  //   page joining beside the owner with display_settings, and handles the
  //   "cr" the page sends right after SETTINGS only then, so a clipboard
  //   reply with no display_settings before it means this page owns it.
  //   DISPLAY_OWNER hands the display to a page beside the owner.
  // - width, height: the size the display streams at (stream_resolution).
  // - settings: the stream settings it streams with, from display_settings
  //   (a page beside the owner gets them on joining, the owner when a page
  //   beside it changes them); null until then.
  let display = { owner: null, width: 0, height: 0, settings: null };
  const displayListeners = new Set();
  const setDisplay = (d) => {
    display = { ...display, ...d };
    for (const fn of displayListeners) try { fn(display); } catch {}
  };
  // The socket worker can deliver the join IDR to the decoder before the
  // page processes PIPELINE_RESETTING. If that first picture is lost, ask
  // again promptly instead of waiting for Selkies' three-second watchdog.
  let firstPictureTimer = null;
  const awaitFirstPicture = () => {
    clearTimeout(firstPictureTimer);
    const current = transport;
    let attempts = 0;
    const retry = () => {
      firstPictureTimer = null;
      if (transport !== current || !current || current.readyState !== PageWebSocket.OPEN) return;
      // Selkies creates a visible placeholder canvas before decoding. Its
      // status marker is hidden by the first decoded/presented frame on
      // every rendering path, including video and offscreen canvas.
      if (document.getElementById("status-display")?.classList.contains("hidden")) return;
      sendOwn("REQUEST_KEYFRAME");
      if (++attempts < 5) firstPictureTimer = setTimeout(retry, 250);
    };
    firstPictureTimer = setTimeout(retry, 100);
  };
  const onText = (text) => {
    if (text === "MODE websockets" || text === "PIPELINE_RESETTING primary") {
      awaitFirstPicture();
    } else if (text.startsWith("DISPLAY_OWNER ")) {
      setDisplay({ owner: true });
    } else if (text.startsWith("clipboard_reply,")) {
      if (display.owner === null) setDisplay({ owner: true });
    } else if (text.startsWith("{")) {
      let m;
      try { m = JSON.parse(text); } catch { return; }
      if (m.type === "stream_resolution" && m.displayId === "primary" && m.width > 0 && m.height > 0) {
        setDisplay({ width: m.width, height: m.height });
        awaitFirstPicture();
      } else if (m.type === "display_settings" && m.displayId === "primary") {
        setDisplay({ settings: m.settings || {}, owner: display.owner === null ? false : display.owner });
      }
    }
  };
  let transport = null;
  let sendOwn = null; // the transport's send, past our filter
  Object.defineProperty(window, "selkiesTransport", {
    configurable: true,
    get: () => transport,
    set: (t) => {
      clearTimeout(firstPictureTimer);
      transport = t;
      if (!t) {
        sendOwn = null;
        forbid();
        setState("disconnected");
        return;
      }
      const send = t.send;
      t.send = function (data) {
        if (data === "STOP_VIDEO") return;
        // Sound is the desktop page's to turn on or off (setSound); Selkies'
        // client would ask for it on every connection.
        if (data === "START_AUDIO" || data === "STOP_AUDIO") return;
        return send.call(this, data);
      };
      sendOwn = (data) => send.call(t, data);
      setDisplay({ owner: null, width: 0, height: 0, settings: null });
      forbid();
      // A mic still live from the last connection stops: this one's page
      // hasn't asked for it, and the last stream took its badge.
      if (media.mic) window.postMessage({ type: "pipelineControl", pipeline: "microphone", enabled: false }, location.origin);
      setState(t.readyState === PageWebSocket.OPEN ? "connected" : "connecting");
      t.addEventListener("open", () => transport === t && setState("connected"));
      t.addEventListener("close", () => {
        if (transport !== t) return;
        clearTimeout(firstPictureTimer);
        forbid();
        setState("disconnected");
      });
      t.addEventListener("message", (e) => {
        if (transport !== t) return;
        if (typeof e.data === "string") onText(e.data);
      });
    },
  });

  // --- local touch view --------------------------------------------------------
  // Reuse the pinned client's gesture classification and complete touch lifecycle.
  // Its pinch normally sends Ctrl+wheel to the app. Route only touch pinches to
  // the local view; mouse Ctrl+wheel and two-finger scrolling at fit stay intact.
  let viewZoom = 1;
  let currentInput;
  let requestedTouchMode = null;
  const gestures = new Set();
  const emitGesture = (g) => { for (const fn of gestures) fn(g); };
  Object.defineProperty(window, "webrtcInput", {
    configurable: true,
    get: () => currentInput,
    set(input) {
      currentInput = input;
      if (!input || input.__deskyardGestures) return;
      input.__deskyardGestures = true;
      // A transform changes child client coordinates during a gesture. Keep
      // the gesture's stored positions in the stable top-level viewport instead.
      const points = (x1, y1, x2, y2) => {
        const frame = window.frameElement;
        if (!frame) return [x1, y1, x2, y2];
        const rect = frame.getBoundingClientRect();
        const sx = rect.width / frame.clientWidth, sy = rect.height / frame.clientHeight;
        return [rect.left + x1 * sx, rect.top + y1 * sy, rect.left + x2 * sx, rect.top + y2 * sy];
      };
      for (const name of ["_twoFingerStart", "_twoFingerMove"]) {
        const original = input[name];
        if (typeof original === "function") input[name] = function (...args) {
          return original.apply(this, points(...args));
        };
      }
      // Follow this finger's relative cursor at the parent's viewport edges.
      // Echo coordinates include the client's prediction before the server reply.
      const placeCursor = input._placeEchoCursor;
      if (typeof placeCursor === "function") input._placeEchoCursor = function (...args) {
        const result = placeCursor.apply(this, args);
        const echo = this._echo, box = this._streamBox?.(), frame = window.frameElement;
        if (gestures.size && this._trackpadMode && this._trackpadTouches?.size === 1 &&
            (this._trackpadGestureMode === "moving" || this._trackpadGestureMode === "dragging") &&
            echo && echo.display === this.displayId && box?.scaleX > 0 && box?.scaleY > 0 && frame) {
          let x = echo.x, y = echo.y;
          for (const [, dx, dy] of this._echoDeltas) [x, y] = this._echoStep(x, y, dx, dy);
          const rect = frame.getBoundingClientRect();
          emitGesture({ kind: "cursor", x: rect.left + (box.left + x / box.scaleX) * rect.width / frame.clientWidth,
            y: rect.top + (box.top + y / box.scaleY) * rect.height / frame.clientHeight });
        }
        return result;
      };
      // Parent scrolling moves the transformed iframe underneath the finger.
      // Compensate the stored child coordinates so that isn't relative motion.
      const move = input._trackpadMove;
      const touchFrames = new WeakMap();
      if (typeof move === "function") input._trackpadMove = function (touch, x, y, time) {
        const frame = window.frameElement;
        if (frame) {
          const rect = frame.getBoundingClientRect(), previous = touchFrames.get(touch);
          if (previous) {
            touch.lastX += (previous.left - rect.left) * frame.clientWidth / rect.width;
            touch.lastY += (previous.top - rect.top) * frame.clientHeight / rect.height;
          }
          touchFrames.set(touch, { left: rect.left, top: rect.top });
        }
        return move.call(this, touch, x, y, time);
      };
      const mode = input.setTrackpadMode;
      if (typeof mode === "function") input.setTrackpadMode = function (value) {
        return mode.call(this, requestedTouchMode ?? value);
      };
      const zoom = input._sendZoomNotches;
      const scroll = input._drainScrollNotches;
      if (typeof zoom === "function") input._sendZoomNotches = function (notches) {
        if (gestures.size && this._twoFinger?.kind === "pinch") {
          if (notches) emitGesture({ kind: "zoom", factor: Math.pow(1.1, notches), x: this._twoFinger.x0, y: this._twoFinger.y0 });
          return;
        }
        return zoom.call(this, notches);
      };
      if (typeof scroll === "function") input._drainScrollNotches = function (delta, vertical) {
        if (gestures.size && viewZoom > 1.001 && this._twoFinger?.kind === "scroll") {
          emitGesture({ kind: "pan", dx: vertical ? 0 : delta, dy: vertical ? delta : 0 });
          return 0;
        }
        return scroll.call(this, delta, vertical);
      };
    },
  });
  // Actual focus, rather than a guessed toggle, follows keyboard dismissal too.
  const keyboardListeners = new Set();
  const reportKeyboard = () => {
    const visible = document.activeElement?.id === "keyboard-input-assist";
    for (const fn of keyboardListeners) fn(visible);
  };
  addListener.call(document, "focusin", reportKeyboard);
  addListener.call(document, "focusout", () => queueMicrotask(reportKeyboard));

  // --- hooks for the desktop page ----------------------------------------------
  const videoIds = ["stream", "videoStream", "videoCanvas", "videoWorkerCanvas"];
  window.deskyardScreen = {
    /** This tab's id, as on the stream URL; the mic endpoint takes it. */
    tab,
    setTouchMode(trackpad) {
      requestedTouchMode = !!trackpad;
      currentInput?.setTrackpadMode?.(requestedTouchMode);
    },
    setViewZoom(value) { viewZoom = Math.max(1, Math.min(4, value)); },
    keyboard(show) {
      const input = document.getElementById("keyboard-input-assist");
      if (!input) return;
      pendingFocus = null;
      if (show) {
        input.value = "";
        // Bypass our automatic-focus guard, synchronously in the user's tap.
        focusElement.call(input, { preventScroll: true });
      } else input.blur();
      reportKeyboard();
    },
    /**
     * Whether the page is an insecure context (plain http): no mic or
     * clipboard access, and a JPEG stream where there's no WebCodecs.
     */
    insecure,
    /** Sends a message to Selkies' client (e.g. {type: "pipelineControl", …}). */
    post(msg) {
      window.postMessage(msg, location.origin);
    },
    /**
     * Calls fn with each message of that type from Selkies' client; returns a
     * function that stops it. The type "deskyard.state" gets each new state(),
     * "deskyard.display" each new display(), "deskyard.media" each new
     * media() and "deskyard.alert" Selkies' capture errors as {kind, text}
     * (kind "microphone").
     */
    on(type, fn) {
      if (type === "deskyard.gesture") {
        gestures.add(fn);
        return () => gestures.delete(fn);
      }
      if (type === "deskyard.keyboard") {
        keyboardListeners.add(fn);
        return () => keyboardListeners.delete(fn);
      }
      if (type === "deskyard.state") {
        stateListeners.add(fn);
        return () => stateListeners.delete(fn);
      }
      if (type === "deskyard.display") {
        displayListeners.add(fn);
        return () => displayListeners.delete(fn);
      }
      if (type === "deskyard.media") {
        mediaListeners.add(fn);
        return () => mediaListeners.delete(fn);
      }
      if (type === "deskyard.alert") {
        alerts.add(fn);
        return () => alerts.delete(fn);
      }
      const listener = (e) => {
        if (e.source === window && e.origin === location.origin && e.data && e.data.type === type) fn(e.data);
      };
      addListener.call(window, "message", listener);
      return () => window.removeEventListener("message", listener);
    },
    /** The stream's state: "connecting" | "connected" | "disconnected". */
    state: () => state,
    /**
     * The display: {owner, width, height, settings} (see above). The type
     * "deskyard.display" on on() gets each change.
     */
    display: () => display,
    /**
     * Sound and mic: {sound, mic, playing, soundAt}
     * (see above). The type "deskyard.media" on on() gets each change.
     */
    media: () => media,
    /**
     * Turns this page's sound on or off; the page says on every connection.
     * The desktop answers AUDIO_STARTED or AUDIO_STOPPED, which Selkies'
     * client follows. (Its pipelineControl message would go by the client's
     * own idea of the sound, which is "on" from the start, also for a page
     * beside the display's owner that the desktop isn't sending sound to.)
     * Call it from the click that turns sound on: a browser lets the
     * playback start then.
     */
    setSound(on) {
      if (transport && sendOwn && transport.readyState === PageWebSocket.OPEN) sendOwn(on ? "START_AUDIO" : "STOP_AUDIO");
      if (on) this.resumeSound();
    },
    /** Starts Selkies' playback if the browser held it back. */
    resumeSound() {
      const c = playback();
      if (c && c.state === "suspended") c.resume().then(notePlaying, () => {});
    },
    /**
     * Lets Selkies capture the mic ("microphone"), until the page says
     * otherwise or the connection ends.
     */
    allowCapture(kind, on) {
      if (!(kind in allowed)) return;
      // Only on an open stream: the page's badge is on that stream.
      allowed[kind] = !!on && !!transport && transport.readyState === PageWebSocket.OPEN;
    },
    /**
     * The mic to capture ("" for the browser's default). A live mic moves to
     * it: Selkies restarts its capture for audioDeviceSelected.
     */
    setMic(deviceId) {
      micDevice = String(deviceId || "");
      if (media.mic) window.postMessage({ type: "audioDeviceSelected", context: "input", deviceId: micDevice || "default" }, location.origin);
    },
    /**
     * Where this page plays the desktop's sound ("" for the system default).
     * Selkies takes a chosen device; it has no way back to the default, which
     * is set here.
     */
    setOutput(deviceId) {
      const id = String(deviceId || "");
      window.postMessage({ type: "audioDeviceSelected", context: "output", deviceId: id || "default" }, location.origin);
      const c = playback();
      if (!id && c && typeof c.setSinkId === "function") c.setSinkId("").catch(() => {});
    },
    /**
     * Calls fn(at, rms) for each block of sound this page plays: when it
     * reaches the speakers (ms since the epoch) and its level. For the
     * ?avsync=1 readout; returns a function that stops it.
     */
    watchSound(fn) {
      soundWatchers.add(fn);
      tapSound(playbackOut);
      return () => soundWatchers.delete(fn);
    },
    /** How loud the mic is now, 0 to 1 (0 while it's off). */
    micLevel,
    /** The element showing the picture, if any. */
    video() {
      for (const id of videoIds) {
        const el = document.getElementById(id);
        if (el && (el instanceof HTMLVideoElement || el instanceof HTMLCanvasElement) && el.getClientRects().length > 0 &&
            getComputedStyle(el).display !== "none") {
          return el;
        }
      }
      return null;
    },
  };
})();
