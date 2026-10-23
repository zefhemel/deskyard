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
// - Lets Selkies capture the mic or the webcam only while the desktop page
//   has asked for it, on the current connection: Selkies' client turns them
//   on by itself in places (it restores them after a reconnect, and on a
//   desktop app's demand), and a mic must never go live unseen.
// - Follows sound, mic and webcam (media()), and turns Selkies' alert()s
//   about them into messages for the page.
// - Lets the desktop page's ?avsync=1 readout hear the sound as it reaches
//   the speakers (watchSound()).
// - Keeps Selkies from taking the focus out of the desktop page: it focuses
//   its text-input host when a stream attaches, which blurred the page and
//   closed its open menus. That focus waits until this frame has the focus.
// - window.deskyardScreen, the hooks the desktop page drives Selkies with.
(() => {
  "use strict";

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

  // --- sound, mic and webcam --------------------------------------------------
  // What Selkies' client is doing, from its own status messages (posted on
  // this window): sound is its audio pipeline for this page, mic and webcam
  // its captures. playing: its playback AudioContext is running (a browser
  // holds it back until the user clicks). soundAt: when the stream last
  // brought this page sound (performance.now(); 0 for never), from the
  // stream's worker, which passes sound straight to the decoder and tells
  // the page about once a second.
  let media = { sound: false, mic: false, webcam: false, playing: false, soundAt: 0 };
  const mediaListeners = new Set();
  const setMedia = (m) => {
    const next = { ...media, ...m };
    if (next.sound === media.sound && next.mic === media.mic && next.webcam === media.webcam && next.playing === media.playing) {
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
      if (typeof m.webcam === "boolean") d.webcam = m.webcam;
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

  // Captures: only what the page allowed, on this connection.
  const allowed = { microphone: false, webcam: false };
  let camera = "";
  let micDevice = "";
  let micStream = null;
  const md = navigator.mediaDevices;
  if (md && typeof md.getUserMedia === "function") {
    const getUserMedia = md.getUserMedia.bind(md);
    md.getUserMedia = async (constraints = {}) => {
      const c = { ...constraints };
      if ((c.audio && !allowed.microphone) || (c.video && !allowed.webcam)) {
        throw new DOMException("The desktop page hasn't turned this on", "NotAllowedError");
      }
      if (c.video) {
        c.video = { ...(typeof c.video === "object" ? c.video : {}) };
        if (camera) c.video.deviceId = { exact: camera };
        else delete c.video.deviceId;
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
    allowed.webcam = false;
  };

  // Selkies tells capture failures with alert(); the page gets them as
  // {kind, text}, and only for a capture it asked for.
  const alerts = new Set();
  window.alert = (text) => {
    text = String(text);
    console.warn("Selkies:", text);
    const kind = /^Microphone/.test(text) ? "microphone" : /^Webcam/.test(text) ? "webcam" : null;
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
  const onText = (text) => {
    if (text.startsWith("DISPLAY_OWNER ")) {
      setDisplay({ owner: true });
    } else if (text.startsWith("clipboard_reply,")) {
      if (display.owner === null) setDisplay({ owner: true });
    } else if (text.startsWith("{")) {
      let m;
      try { m = JSON.parse(text); } catch { return; }
      if (m.type === "stream_resolution" && m.displayId === "primary" && m.width > 0 && m.height > 0) {
        setDisplay({ width: m.width, height: m.height });
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
      // A capture still live from the last connection stops: this one's
      // page hasn't asked for it, and the last stream took its badge.
      for (const [kind, live] of [["microphone", media.mic], ["webcam", media.webcam]]) {
        if (live) window.postMessage({ type: "pipelineControl", pipeline: kind, enabled: false }, location.origin);
      }
      setState(t.readyState === PageWebSocket.OPEN ? "connected" : "connecting");
      t.addEventListener("open", () => transport === t && setState("connected"));
      t.addEventListener("close", () => {
        if (transport !== t) return;
        forbid();
        setState("disconnected");
      });
      t.addEventListener("message", (e) => {
        if (transport !== t) return;
        if (typeof e.data === "string") onText(e.data);
      });
    },
  });

  // --- hooks for the desktop page ----------------------------------------------
  const videoIds = ["stream", "videoStream", "videoCanvas", "videoWorkerCanvas"];
  window.deskyardScreen = {
    /** This tab's id, as on the stream URL; the mic endpoint takes it. */
    tab,
    /** Sends a message to Selkies' client (e.g. {type: "pipelineControl", …}). */
    post(msg) {
      window.postMessage(msg, location.origin);
    },
    /**
     * Calls fn with each message of that type from Selkies' client; returns a
     * function that stops it. The type "deskyard.state" gets each new state(),
     * "deskyard.display" each new display(), "deskyard.media" each new
     * media() and "deskyard.alert" Selkies' capture errors as {kind, text}
     * (kind "microphone" or "webcam").
     */
    on(type, fn) {
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
     * Sound, mic and webcam: {sound, mic, webcam, playing, soundAt}
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
     * Lets Selkies capture the mic ("microphone") or the camera ("webcam"),
     * until the page says otherwise or the connection ends.
     */
    allowCapture(kind, on) {
      if (!(kind in allowed)) return;
      // Only on an open stream: the page's badge is on that stream.
      allowed[kind] = !!on && !!transport && transport.readyState === PageWebSocket.OPEN;
    },
    /**
     * The camera the webcam captures from ("" for the browser's default), from
     * its next start.
     */
    setCamera(deviceId) {
      camera = String(deviceId || "");
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
