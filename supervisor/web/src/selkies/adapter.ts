/** Supervisor-owned adapter for images advertising the reviewed Selkies v1 contract. */
import type {
  Capture,
  ConnectionState,
  DeskyardScreen,
  Display,
  Media,
  StreamSample,
} from "./screen.ts";
import {
  captureGate,
  displayMessage,
  type DisplayRequest,
  shouldSend,
  type ClientHooks,
  type ClientInput,
  type Gesture,
  type Transport,
} from "./contract.ts";
import { installWheelScrolling } from "./wheel.ts";
import { playbackWorklets } from "./worklet.ts";

declare global {
  interface Window {
    __deskyardHooks: ClientHooks;
    deskyardScreen: DeskyardScreen;
    /** The pinned client's public presentation statistics. */
    stream_stats?: { latest: StreamSample | null };
  }
}

const tab = Array.from(crypto.getRandomValues(new Uint8Array(12)), (byte) =>
  byte.toString(16).padStart(2, "0"),
).join("");
// Per-load identity and supervisor-owned stream preferences. These storage
// keys are part of the pinned client contract, not a browser interception.
try {
  sessionStorage.removeItem("selkies_tab_id");
  const saved =
    /_(encoder|framerate|video_crf|video_fullcolor|video_10bit|video_streaming_mode|jpeg_quality|paint_over_jpeg_quality|use_paint_over_quality|video_paintover_crf|video_paintover_burst_frames|video_bitrate|rate_control_mode|use_cpu|cpu_encode_threading|scaling_dpi|manual_resolution|manual_width|manual_height|scaleLocallyManual)(_pick)?$/;
  for (const key of Object.keys(localStorage))
    if (/^https?___.*_screen__/.test(key) && saved.test(key))
      localStorage.removeItem(key);
  for (const key of Object.keys(sessionStorage))
    if (/_(display_settings_tab|pick_tab)$/.test(key))
      sessionStorage.removeItem(key);
} catch {
  /* Private browsing can disable storage. */
}

let removeWheelScrolling: (() => void) | undefined;

const listeners = new Map<string, Set<(data: unknown) => void>>();
function emit(type: string, data: unknown) {
  listeners.get(type)?.forEach((fn) => {
    try {
      fn(data);
    } catch (error) {
      console.warn("Deskyard listener:", error);
    }
  });
}
function on(type: string, fn: (data: never) => void): () => void {
  let set = listeners.get(type);
  if (!set) {
    set = new Set();
    listeners.set(type, set);
  }
  const listener = fn as (data: unknown) => void;
  set.add(listener);
  return () => {
    set.delete(listener);
  };
}
let state: ConnectionState = "connecting";
let display: Display = { owner: null, width: 0, height: 0, settings: null };
let media: Media = {
  sound: false,
  mic: false,
  playing: false,
  soundAt: 0,
};
function setState(value: ConnectionState) {
  if (state !== value) {
    state = value;
    emit("deskyard.state", state);
  }
}
function setDisplay(value: Partial<Display>) {
  display = { ...display, ...value };
  emit("deskyard.display", display);
}
function setMedia(value: Partial<Media>) {
  media = { ...media, ...value };
  emit("deskyard.media", media);
}
const post = (message: object) => window.postMessage(message, location.origin);
const gate = captureGate();
let transport: Transport | null = null;
let input: ClientInput | null = null;
let requestedDisplay: DisplayRequest | null = null;
let requestedTouchMode: boolean | null = null;
let zoom = 1;
let playback: AudioContext | null = null;
let playbackOutput: AudioNode | null = null;
let microphone = "";
let microphoneStream: MediaStream | null = null;
let pendingFocus: { element: HTMLElement; options?: FocusOptions } | null =
  null;
let recovery: ReturnType<typeof setTimeout> | undefined;
const worklets = playbackWorklets();
const soundWatchers = new Set<(at: number, rms: number) => void>();
const tapped = new WeakSet<AudioNode>();
const touchFrames = new WeakMap<object, { left: number; top: number }>();
const frame = () => window.frameElement as HTMLIFrameElement | null;
function point(x: number, y: number): [number, number] {
  const element = frame();
  if (!element) return [x, y];
  const box = element.getBoundingClientRect();
  return [
    box.left + (x * box.width) / element.clientWidth,
    box.top + (y * box.height) / element.clientHeight,
  ];
}
function gesture(value: Gesture) {
  emit("deskyard.gesture", value);
}
const watchingGestures = () => !!listeners.get("deskyard.gesture")?.size;
function keyboardVisible() {
  emit(
    "deskyard.keyboard",
    document.activeElement?.id === "keyboard-input-assist",
  );
}
function sendOwn(value: string) {
  if (transport?.readyState === WebSocket.OPEN) transport.send(value);
}
function recoverPicture() {
  clearTimeout(recovery);
  const current = transport;
  let attempts = 0;
  const retry = () => {
    if (
      transport !== current ||
      current?.readyState !== WebSocket.OPEN ||
      document.getElementById("status-display")?.classList.contains("hidden")
    )
      return;
    sendOwn("REQUEST_KEYFRAME");
    if (++attempts < 5) recovery = setTimeout(retry, 250);
  };
  recovery = setTimeout(retry, 100);
}
function incoming(text: string) {
  if (text === "MODE websockets" || text === "PIPELINE_RESETTING primary")
    recoverPicture();
  else if (text.startsWith("DISPLAY_OWNER ")) setDisplay({ owner: true });
  else if (text.startsWith("clipboard_reply,") && display.owner === null)
    setDisplay({ owner: true });
  else if (text.startsWith("{")) {
    let message: {
      type?: string;
      displayId?: string;
      width?: number;
      height?: number;
      settings?: Record<string, unknown>;
    };
    try {
      message = JSON.parse(text);
    } catch {
      return;
    }
    if (message.displayId !== "primary") return;
    if (
      message.type === "stream_resolution" &&
      Number(message.width) > 0 &&
      Number(message.height) > 0
    ) {
      setDisplay({ width: message.width!, height: message.height! });
      recoverPicture();
    } else if (message.type === "display_settings")
      setDisplay({
        settings: message.settings || {},
        owner: display.owner ?? false,
      });
  }
}

async function tapSound(node: AudioNode | null) {
  if (!node || tapped.has(node) || !soundWatchers.size) return;
  tapped.add(node);
  const ctx = node.context as AudioContext;
  const code = `registerProcessor('deskyard-sound-tap',class extends AudioWorkletProcessor{process(inputs){const c=inputs[0]?.[0];if(c){let sum=0;for(const v of c)sum+=v*v;this.port.postMessage([currentTime,Math.sqrt(sum/c.length)])}return true}})`;
  const url = URL.createObjectURL(
    new Blob([code], { type: "application/javascript" }),
  );
  try {
    await worklets.addWorklet(ctx, url);
    const tap = worklets.workletNode(ctx, "deskyard-sound-tap");
    tap.port.onmessage = (event: MessageEvent<[number, number]>) => {
      const [time, rms] = event.data;
      const timestamp = ctx.getOutputTimestamp();
      if (!timestamp.performanceTime || timestamp.contextTime === undefined)
        return;
      const at =
        performance.timeOrigin +
        timestamp.performanceTime +
        (time - timestamp.contextTime) * 1000;
      soundWatchers.forEach((fn) => fn(at, rms));
    };
    const silent = ctx.createGain();
    silent.gain.value = 0;
    node.connect(tap);
    tap.connect(silent);
    silent.connect(ctx.destination);
  } catch (error) {
    console.warn("Deskyard sound tap:", error);
  } finally {
    URL.revokeObjectURL(url);
  }
}

window.__deskyardHooks = {
  version: 1,
  allowInsecure: true,
  pointerMoveOnly: true,
  ...worklets,
  url(value) {
    const url = new URL(value, location.href);
    url.searchParams.set("tab", tab);
    return url.href;
  },
  send(socket, data) {
    if (!shouldSend(data)) return;
    const next = displayMessage(data, display.owner, requestedDisplay);
    if (next !== null) socket.send(next);
  },
  transport(value) {
    clearTimeout(recovery);
    transport = value;
    requestedDisplay = null;
    gate.reset();
    if (!value) {
      setState("disconnected");
      return;
    }
    setDisplay({ owner: null, width: 0, height: 0, settings: null });
    if (media.mic)
      post({ type: "pipelineControl", pipeline: "microphone", enabled: false });
    setState(value.readyState === WebSocket.OPEN ? "connected" : "connecting");
    value.addEventListener("open", () => {
      if (transport === value) setState("connected");
    });
    value.addEventListener("close", () => {
      if (transport === value) {
        clearTimeout(recovery);
        gate.reset();
        setState("disconnected");
      }
    });
    value.addEventListener("message", (event) => {
      if (transport !== value) return;
      if (typeof event.data === "string") incoming(event.data);
      else if (
        event.data instanceof ArrayBuffer &&
        event.data.byteLength > 1 &&
        new Uint8Array(event.data, 0, 1)[0] === 1
      )
        media.soundAt = performance.now();
    });
  },
  async capture(constraints) {
    const selected = { ...constraints };
    if (selected.audio) {
      selected.audio = {
        ...(typeof selected.audio === "object" ? selected.audio : {}),
      };
      if (microphone) selected.audio.deviceId = { exact: microphone };
      else delete selected.audio.deviceId;
    }
    const stream = await gate.capture(selected, (value) =>
      navigator.mediaDevices.getUserMedia(value),
    );
    if (selected.audio) microphoneStream = stream;
    return stream;
  },
  input(value) {
    removeWheelScrolling?.();
    removeWheelScrolling = value?.element ? installWheelScrolling(value.element) : undefined;
    input = value;
    if (value && requestedTouchMode !== null)
      value.setTrackpadMode(requestedTouchMode);
  },
  touchMode(value) {
    return requestedTouchMode ?? value;
  },
  points(x1, y1, x2, y2) {
    return [...point(x1, y1), ...point(x2, y2)];
  },
  zoom(notches, x, y) {
    if (!watchingGestures()) return false;
    if (notches)
      gesture({ kind: "zoom", factor: Math.pow(1.1, notches), x, y });
    return true;
  },
  pan(delta, vertical) {
    if (!watchingGestures() || zoom <= 1.001) return false;
    gesture({
      kind: "pan",
      dx: vertical ? 0 : delta,
      dy: vertical ? delta : 0,
    });
    return true;
  },
  trackpadOrigin(touch) {
    const element = frame();
    if (!element) return [0, 0];
    const rect = element.getBoundingClientRect(),
      previous = touchFrames.get(touch);
    touchFrames.set(touch, { left: rect.left, top: rect.top });
    return previous
      ? [
          ((previous.left - rect.left) * element.clientWidth) / rect.width,
          ((previous.top - rect.top) * element.clientHeight) / rect.height,
        ]
      : [0, 0];
  },
  cursor(x, y) {
    if (watchingGestures()) {
      const [px, py] = point(x, y);
      gesture({ kind: "cursor", x: px, y: py });
    }
  },
  focus(element, options) {
    if (document.hasFocus()) element.focus(options);
    else pendingFocus = { element, options };
  },
  playback(context) {
    playback = context;
    context.addEventListener("statechange", () => {
      if (playback === context)
        setMedia({ playing: context.state === "running" });
    });
    setMedia({ playing: context.state === "running" });
  },
  playbackOutput(node) {
    playbackOutput = node;
    void tapSound(node);
  },
  audioTick() {
    media.soundAt = performance.now();
  },
  alert(text) {
    if (text.startsWith("Microphone") && gate.allowed("microphone"))
      emit("deskyard.alert", { kind: "microphone", text });
  },
};

window.addEventListener("focus", () => {
  const pending = pendingFocus;
  pendingFocus = null;
  if (pending?.element.isConnected) pending.element.focus(pending.options);
});
document.addEventListener("focusin", keyboardVisible);
document.addEventListener("focusout", () => queueMicrotask(keyboardVisible));
window.addEventListener("message", (event) => {
  if (
    event.source !== window ||
    event.origin !== location.origin ||
    !event.data ||
    typeof event.data !== "object"
  )
    return;
  const message = event.data as Record<string, unknown>;
  if (
    message.type === "sidebarButtonStatusUpdate" ||
    message.type === "pipelineStatusUpdate"
  ) {
    const next: Partial<Media> = {};
    if (typeof message.audio === "boolean") next.sound = message.audio;
    if (typeof message.microphone === "boolean") next.mic = message.microphone;
    setMedia(next);
  }
  if (typeof message.type === "string") emit(message.type, message);
});

let meter: {
  stream: MediaStream;
  source: MediaStreamAudioSourceNode;
  analyser: AnalyserNode;
  data: Float32Array<ArrayBuffer>;
} | null = null;
let meterContext: AudioContext | null = null;
function micLevel() {
  if (
    !media.mic ||
    !microphoneStream ||
    microphoneStream
      .getAudioTracks()
      .every((track) => track.readyState !== "live")
  )
    return 0;
  try {
    if (!meter || meter.stream !== microphoneStream) {
      meter?.source.disconnect();
      meterContext ??= new AudioContext();
      if (meterContext.state === "suspended") void meterContext.resume();
      const analyser = meterContext.createAnalyser();
      analyser.fftSize = 1024;
      const source = meterContext.createMediaStreamSource(microphoneStream);
      source.connect(analyser);
      meter = {
        stream: microphoneStream,
        source,
        analyser,
        data: new Float32Array(analyser.fftSize),
      };
    }
    meter.analyser.getFloatTimeDomainData(meter.data);
    const mean =
      meter.data.reduce((sum, value) => sum + value * value, 0) /
      meter.data.length;
    return Math.min(1, Math.max(0, (10 * Math.log10(mean || 1e-12) + 60) / 60));
  } catch {
    return 0;
  }
}
function resumeSound() {
  if (playback?.state === "suspended") void playback.resume().catch(() => {});
}
window.deskyardScreen = {
  tab,
  insecure: window.isSecureContext === false,
  post,
  on,
  state: () => state,
  display: () => display,
  media: () => media,
  stats: () => window.stream_stats?.latest ?? null,
  showStats(open) {
    post({ type: "statsOpen", open });
  },
  configureDisplay(size, dpi) {
    if (!display.owner) return;
    requestedDisplay = { ...size, dpi };
    post({ type: "settings", settings: { scaling_dpi: dpi, manual_resolution: true, manual_width: size.width, manual_height: size.height } });
  },
  setTouchMode(value) {
    requestedTouchMode = value;
    input?.setTrackpadMode(value);
  },
  setViewZoom(value) {
    zoom = Math.max(1, Math.min(4, value));
  },
  keyboard(show) {
    const element = document.getElementById(
      "keyboard-input-assist",
    ) as HTMLInputElement | null;
    if (!element) return;
    pendingFocus = null;
    if (show) {
      element.value = "";
      element.focus({ preventScroll: true });
    } else element.blur();
    keyboardVisible();
  },
  setSound(value) {
    sendOwn(value ? "START_AUDIO" : "STOP_AUDIO");
    if (value) resumeSound();
  },
  resumeSound,
  allowCapture(kind: Capture, value) {
    gate.allow(kind, value && transport?.readyState === WebSocket.OPEN);
  },
  setMic(value) {
    microphone = value;
    if (media.mic)
      post({
        type: "audioDeviceSelected",
        context: "input",
        deviceId: value || "default",
      });
  },
  setOutput(value) {
    post({
      type: "audioDeviceSelected",
      context: "output",
      deviceId: value || "default",
    });
    const ctx = playback as
      (AudioContext & { setSinkId?: (id: string) => Promise<void> }) | null;
    if (!value && ctx?.setSinkId) void ctx.setSinkId("").catch(() => {});
  },
  watchSound(fn) {
    soundWatchers.add(fn);
    void tapSound(playbackOutput);
    return () => {
      soundWatchers.delete(fn);
    };
  },
  micLevel,
  video() {
    for (const id of [
      "stream",
      "videoStream",
      "videoCanvas",
      "videoWorkerCanvas",
    ]) {
      const element = document.getElementById(id);
      if (
        (element instanceof HTMLVideoElement ||
          element instanceof HTMLCanvasElement) &&
        element.getClientRects().length &&
        getComputedStyle(element).display !== "none"
      )
        return element;
    }
    return null;
  },
};
