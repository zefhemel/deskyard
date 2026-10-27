/*
 * Driving Selkies' web client in the desktop page's frame.
 *
 * The supervisor serves the client at /<name>/screen/ with our additions
 * (the typed selkies/adapter.ts, or screen_additions.js for old images), which give the frame
 * `window.deskyardScreen`: `tab`, `post(msg)`, `on(type, fn)`, `state()` and
 * `video()`. Both pages are on the supervisor's origin, so we reach it
 * through `frame.contentWindow`.
 *
 * Messages to the client (`post`), read from its pinned source (selkies-core,
 * the websockets-mode `window.postMessage` handler):
 * - {type: "clipboardUpdateFromUI", text}: puts text on the desktop's clipboard.
 * - {type: "settings", settings: {encoder, framerate, video_bitrate,
 *   rate_control_mode, video_fullcolor, …}}: applies them to the stream (live
 *   where it can, else it restarts the capture) and persists them in the
 *   client's own storage. They are the desktop's display settings, so they
 *   apply to everyone watching it.
 * - {type: "setScaleLocally", value: true} then {type: "setManualResolution",
 *   width, height}: the remote screen keeps that size and the picture is
 *   scaled to fit the frame. {type: "resetResolutionToWindow"} undoes it.
 *   `window.manual_resolution` in the frame tells which mode it is in.
 *
 * The display has an owner, the first page to connect: it sets the screen's
 * size and its stream settings stick. Later pages join beside it: their
 * window size is ignored, so Selkies' client would size its canvas to a
 * window the stream doesn't have (a stretched picture, clicks off target).
 * A page beside the owner is therefore kept scaled to fit at the stream's
 * size, and applies this browser's Quality only when its user picks one.
 * `display()` (from our additions) says which this page is, the stream's
 * size and the display's settings; "deskyard.display" on `on` gets changes.
 *
 * Sound and mic (the Audio panel, audio-panel.tsx):
 * - {type: "setVolume", value}: the playback volume, 0 to 1. It applies once
 *   the client has set its playback up, so the page sends it again when
 *   sound starts.
 * - {type: "pipelineControl", pipeline: "microphone", enabled}: starts or
 *   stops the mic. Our additions refuse the browser's mic unless
 *   `allowCapture` allowed it on this connection, so the client can never
 *   turn it on by itself (it would after a reconnect), and refuse its camera
 *   always.
 * - Sound on and off go through `setSound` (START_AUDIO / STOP_AUDIO on the
 *   stream; see selkies/adapter.ts for why not pipelineControl), devices
 *   through `setOutput` and `setMic`.
 * - `media()` (and "deskyard.media" on `on`) says what the client is doing,
 *   from its own status messages; "deskyard.alert" brings its capture errors.
 * - `watchSound(fn)` reports each block of sound as it reaches the speakers,
 *   for the ?avsync=1 readout (avsync.ts).
 * Several viewers' mics are mixed: each connection plays its mic into the
 * desktop's one virtual mic as its own stream.
 *
 * From the client (`on`), posted on the frame's own window:
 * - {type: "clipboardContentUpdate", text}: the desktop's clipboard changed
 *   (also once after connecting).
 * - "deskyard.state" (from our additions): the stream's state on each change.
 *   Selkies itself posts no connection event.
 *
 * The client shows the picture in a video element or a canvas (`video()`)
 * with a CSS box at the stream's aspect ratio when scaled to fit, filled
 * (`object-fit: fill`): Lock size's scale-to-fit is that box. The remote
 * size is the stream's (stream_resolution); the canvas buffer, which follows
 * the page's own window, is only a fallback before the stream says.
 */
import type { ScreenGeometry } from "./screen-geometry.ts";

export type ConnectionState = "connecting" | "connected" | "disconnected";

/** The display, as the frame's connection knows it. */
export interface Display {
  /** Whether this page owns the display; null until the server has said. */
  owner: boolean | null;
  /** The size the display streams at; 0 until known. */
  width: number;
  height: number;
  /** The stream settings the display runs with, when the server has told this page. */
  settings: Record<string, unknown> | null;
}

/** Sound and mic, as the frame's client has them. */
export interface Media {
  /** The desktop sends this page its sound. */
  sound: boolean;
  /** The mic is captured and sent into the desktop. */
  mic: boolean;
  /** The playback is running (a browser holds it back until a click). */
  playing: boolean;
  /** When sound last arrived (performance.now() in the frame; 0 for never). */
  soundAt: number;
}

export type Capture = "microphone";

/** What our additions put on the frame's window. */
export interface DeskyardScreen {
  tab: string;
  keyboard?(show: boolean): void;
  setTouchMode?(trackpad: boolean): void;
  setViewZoom?(zoom: number): void;
  on(type: "deskyard.keyboard", fn: (visible: boolean) => void): () => void;
  on(type: "deskyard.gesture", fn: (gesture: { kind: "zoom"; factor: number; x: number; y: number } | { kind: "pan"; dx: number; dy: number } | { kind: "cursor"; x: number; y: number }) => void): () => void;
  /** The frame is an insecure context (plain http). */
  insecure: boolean;
  post(msg: object): void;
  on(type: "deskyard.state", fn: (state: ConnectionState) => void): () => void;
  on(type: "deskyard.display", fn: (display: Display) => void): () => void;
  on(type: "deskyard.media", fn: (media: Media) => void): () => void;
  on(type: "deskyard.alert", fn: (alert: { kind: Capture; text: string }) => void): () => void;
  on(type: string, fn: (data: Record<string, unknown>) => void): () => void;
  state(): ConnectionState;
  display(): Display;
  media(): Media;
  setSound(on: boolean): void;
  resumeSound(): void;
  allowCapture(kind: Capture, on: boolean): void;
  setMic(deviceId: string): void;
  setOutput(deviceId: string): void;
  watchSound(fn: (at: number, rms: number) => void): () => void;
  micLevel(): number;
  video(): HTMLVideoElement | HTMLCanvasElement | null;
}

type ScreenWindow = Window & { deskyardScreen?: DeskyardScreen; manual_resolution?: boolean };

/** The frame's URL for a desktop: Selkies' client, served by the supervisor. */
export function frameSrc(name: string): string {
  return `/${name}/screen/`;
}

/**
 * The frame's permissions policy: an observer's frame gets no microphone or
 * clipboard. A frame keeps the policy it loaded with, so a new level needs a
 * new frame.
 */
export function frameAllow(observing: boolean): string {
  return observing ? "autoplay; speaker-selection" : "clipboard-read; clipboard-write; autoplay; microphone; speaker-selection";
}

/**
 * Whether this page is an insecure context: reached over plain http on an
 * address other than localhost. The browser then has no AudioWorklet, mic
 * or async clipboard, and Chromium and Firefox no WebCodecs: Selkies'
 * client streams JPEG and decodes sound in WASM (the adapter permits playback fallback).
 */
export const insecureContext = typeof window !== "undefined" && window.isSecureContext === false;

/** Selkies' client pins the stream to JPEG where there is no WebCodecs VideoDecoder. */
export const jpegOnly = typeof VideoDecoder === "undefined";

/** Where "Needs https" points: running Deskyard on a LAN. */
export const httpsHelp = "https://github.com/zefhemel/deskyard#on-a-lan";

function frameWindow(frame: HTMLIFrameElement | null): ScreenWindow | null {
  try {
    return (frame?.contentWindow as ScreenWindow | null) ?? null;
  } catch {
    return null;
  }
}

/** The frame's hooks, once our additions have run in it (null on an error page). */
export function screenOf(frame: HTMLIFrameElement | null): DeskyardScreen | null {
  try {
    return frameWindow(frame)?.deskyardScreen ?? null;
  } catch {
    return null;
  }
}

export function setVolume(screen: DeskyardScreen, volume: number): void {
  screen.post({ type: "setVolume", value: volume });
}

/** Starts or stops the mic (allowed first, refused after). */
export function setCapture(screen: DeskyardScreen, kind: Capture, on: boolean): void {
  if (on) screen.allowCapture(kind, true);
  screen.post({ type: "pipelineControl", pipeline: kind, enabled: on });
  if (!on) screen.allowCapture(kind, false);
}

/** Whether this browser can choose where sound plays. */
export const canChooseOutput =
  (typeof AudioContext !== "undefined" && "setSinkId" in AudioContext.prototype) ||
  (typeof HTMLMediaElement !== "undefined" && "setSinkId" in HTMLMediaElement.prototype);

export function sendClipboard(frame: HTMLIFrameElement | null, text: string): void {
  screenOf(frame)?.post({ type: "clipboardUpdateFromUI", text });
}

/** Calls fn with the desktop's clipboard text each time it changes. */
export function onClipboard(screen: DeskyardScreen, fn: (text: string) => void): () => void {
  return screen.on("clipboardContentUpdate", (m) => {
    if (typeof m.text === "string") fn(m.text);
  });
}

export type QualityId = "low" | "medium" | "high" | "extreme" | "lossless";

/**
 * The Quality presets, as Selkies stream settings. All are H.264 (x264 on
 * the CPU) at a constant bitrate, in kbit/s; Medium is Selkies' own default
 * (8 Mbit/s at 60 fps). A still screen is cleaned up to the paint-over
 * quality whatever the bitrate. Lossless is the closest H.264 gets: full 4:4:4
 * colour at a high bitrate (a browser without a 4:4:4 decoder gets 4:2:0).
 *
 * A browser that streams JPEG (no WebCodecs, as over plain http) gets each
 * preset's `jpeg`: a JPEG quality and frame rate, with a still screen
 * re-sent at the paint-over quality (Selkies' default 90; 100 for Lossless).
 */
export const qualities: {
  id: QualityId;
  label: string;
  hint: string;
  bitrate: number;
  framerate: number;
  encoder: string;
  fullcolor: boolean;
  jpeg: { quality: number; framerate: number; paintOver: number };
}[] = [
  { id: "low", label: "Low", hint: "for slow connections", bitrate: 2000, framerate: 30, encoder: "h264enc", fullcolor: false, jpeg: { quality: 30, framerate: 15, paintOver: 90 } },
  { id: "medium", label: "Medium", hint: "", bitrate: 8000, framerate: 60, encoder: "h264enc", fullcolor: false, jpeg: { quality: 50, framerate: 30, paintOver: 90 } },
  { id: "high", label: "High", hint: "", bitrate: 16000, framerate: 60, encoder: "h264enc", fullcolor: false, jpeg: { quality: 70, framerate: 30, paintOver: 90 } },
  { id: "extreme", label: "Extreme", hint: "", bitrate: 32000, framerate: 60, encoder: "h264enc", fullcolor: false, jpeg: { quality: 80, framerate: 60, paintOver: 90 } },
  { id: "lossless", label: "Lossless", hint: "for fast networks", bitrate: 64000, framerate: 60, encoder: "h264enc", fullcolor: true, jpeg: { quality: 90, framerate: 60, paintOver: 100 } },
];

export const defaultQuality: QualityId = "medium";

export function isQualityId(v: unknown): v is QualityId {
  return qualities.some((q) => q.id === v);
}

/** The preset a display's stream settings match, if any (H.264 or JPEG). */
export function qualityOf(settings: Record<string, unknown>): QualityId | null {
  const encoder = String(settings.encoder);
  const q = qualities.find((q) =>
    encoder === "jpeg"
      ? Number(settings.jpeg_quality) === q.jpeg.quality && Number(settings.framerate) === q.jpeg.framerate
      : Number(settings.video_bitrate) === q.bitrate &&
        Number(settings.framerate) === q.framerate &&
        encoder === q.encoder &&
        Boolean(settings.video_fullcolor) === q.fullcolor &&
        (settings.rate_control_mode === undefined || settings.rate_control_mode === "cbr"),
  );
  return q?.id ?? null;
}

/** A preset as the stream settings this browser sends: JPEG where it has no WebCodecs. */
export function qualitySettings(id: QualityId, jpeg = jpegOnly): Record<string, unknown> | null {
  const q = qualities.find((x) => x.id === id);
  if (!q) return null;
  if (jpeg) {
    return { encoder: "jpeg", jpeg_quality: q.jpeg.quality, framerate: q.jpeg.framerate, paint_over_jpeg_quality: q.jpeg.paintOver, use_paint_over_quality: true };
  }
  return { encoder: q.encoder, rate_control_mode: "cbr", video_bitrate: q.bitrate, framerate: q.framerate, video_fullcolor: q.fullcolor };
}

export function sendQuality(frame: HTMLIFrameElement | null, id: QualityId): void {
  const settings = qualitySettings(id);
  if (settings) screenOf(frame)?.post({ type: "settings", settings });
}

export interface Size {
  width: number;
  height: number;
}

/** The size the display streams at, in remote pixels; null before it's known. */
export function remoteSize(frame: HTMLIFrameElement | null): Size | null {
  const d = screenOf(frame)?.display();
  if (d && d.width > 0 && d.height > 0) return { width: d.width, height: d.height };
  try {
    // The frame's own HTMLCanvasElement, so no instanceof across the realms.
    const canvas = frameWindow(frame)?.document.getElementById("videoCanvas") as HTMLCanvasElement | null | undefined;
    return canvas && canvas.width > 0 && canvas.height > 0 ? { width: canvas.width, height: canvas.height } : null;
  } catch {
    return null;
  }
}

/**
 * Lock size: the remote screen stays at size and is scaled to fit the frame.
 * Also how a page beside the owner shows the stream, at the stream's size.
 */
export function lockSize(frame: HTMLIFrameElement | null, size: Size): void {
  const s = screenOf(frame);
  s?.post({ type: "setScaleLocally", value: true });
  s?.post({ type: "setManualResolution", width: size.width, height: size.height });
}

/** Back to the remote screen following the frame's size, if it isn't already. */
export function unlockSize(frame: HTMLIFrameElement | null): void {
  if (frameWindow(frame)?.manual_resolution) screenOf(frame)?.post({ type: "resetResolutionToWindow" });
}

/** Keys the browser would otherwise take, in xdotool syntax for /keys. */
export const keyCombos = [
  { label: "Ctrl+Alt+Del", keys: "ctrl+alt+Delete" },
  { label: "Super", keys: "super" },
  { label: "Alt+Tab", keys: "alt+Tab" },
  { label: "Ctrl+W", keys: "ctrl+w" },
  { label: "Esc", keys: "Escape" },
  { label: "F11", keys: "F11" },
];

/** Gives keyboard focus back to the desktop: Selkies listens for keys on its window. */
export function focusFrame(frame: HTMLIFrameElement | null): void {
  if (!frame) return;
  frame.focus();
  try {
    frame.contentWindow?.focus();
  } catch {
    // an error page
  }
}

/** The activity overlay's ScreenGeometrySource for Selkies' client in the frame. */
export function selkiesScreenGeometry(frame: HTMLIFrameElement | null): ScreenGeometry | null {
  const shown = screenOf(frame)?.video();
  const remote = remoteSize(frame);
  if (!frame || !shown || !remote) return null;
  const s = shown.getBoundingClientRect();
  if (s.width === 0 || s.height === 0) return null;
  // The shown element's rect is in the frame's viewport; the frame's border
  // (clientLeft/Top) sits between that and the frame's own rect.
  const f = frame.getBoundingClientRect();
  const displayed = new DOMRect(f.left + frame.clientLeft + s.left, f.top + frame.clientTop + s.top, s.width, s.height);
  return { remoteWidth: remote.width, remoteHeight: remote.height, displayed };
}
