// Deskyard's additions to Selkies' web client. The supervisor serves this as
// /<name>/screen/deskyard.js and loads it before any of Selkies' scripts.
//
// - Hides pointerrawupdate: inside an iframe Chrome reports its clientX/Y
//   relative to the top-level page, so drags landed off by our bar's height.
//   Without it Selkies falls back to pointermove.
// - Never sends STOP_VIDEO: a tab going to the background must not stop the
//   desktop's stream for everyone else.
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
  Worker.prototype.postMessage = function (msg, ...rest) {
    if (msg && msg.type === "open" && typeof msg.url === "string") msg = { ...msg, url: withTab(msg.url) };
    return workerPost.call(this, msg, ...rest);
  };
  const PageWebSocket = window.WebSocket;
  window.WebSocket = class extends PageWebSocket {
    constructor(url, ...rest) {
      super(withTab(String(url)), ...rest);
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
  Object.defineProperty(window, "selkiesTransport", {
    configurable: true,
    get: () => transport,
    set: (t) => {
      transport = t;
      if (!t) {
        setState("disconnected");
        return;
      }
      const send = t.send;
      t.send = function (data) {
        if (data === "STOP_VIDEO") return;
        return send.call(this, data);
      };
      setDisplay({ owner: null, width: 0, height: 0, settings: null });
      setState(t.readyState === PageWebSocket.OPEN ? "connected" : "connecting");
      t.addEventListener("open", () => transport === t && setState("connected"));
      t.addEventListener("close", () => transport === t && setState("disconnected"));
      t.addEventListener("message", (e) => {
        if (transport === t && typeof e.data === "string") onText(e.data);
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
     * "deskyard.display" each new display().
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
