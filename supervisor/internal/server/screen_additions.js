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
// - window.deskyardScreen, the hooks the desktop page drives Selkies with.
(() => {
  "use strict";

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
      setState(t.readyState === PageWebSocket.OPEN ? "connected" : "connecting");
      t.addEventListener("open", () => transport === t && setState("connected"));
      t.addEventListener("close", () => transport === t && setState("disconnected"));
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
     * function that stops it. The type "deskyard.state" gets each new state().
     */
    on(type, fn) {
      if (type === "deskyard.state") {
        stateListeners.add(fn);
        return () => stateListeners.delete(fn);
      }
      const listener = (e) => {
        if (e.origin === location.origin && e.data && e.data.type === type) fn(e.data);
      };
      addListener.call(window, "message", listener);
      return () => window.removeEventListener("message", listener);
    },
    /** The stream's state: "connecting" | "connected" | "disconnected". */
    state: () => state,
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
