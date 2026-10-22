/*
 * Driving Selkies' web client in the desktop page's frame.
 *
 * The supervisor serves the client at /<name>/screen/ with our additions
 * (supervisor/internal/server/screen_additions.js), which give the frame
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
 * From the client (`on`), posted on the frame's own window:
 * - {type: "clipboardContentUpdate", text}: the desktop's clipboard changed
 *   (also once after connecting).
 * - "deskyard.state" (from our additions): the stream's state on each change.
 *   Selkies itself posts no connection event.
 *
 * The client keeps its canvas (#videoCanvas) buffer at the size it streams,
 * in remote pixels, and shows the picture in a video element or a canvas
 * (`video()`) with the same CSS box: no letterboxing inside the element, and
 * Lock size's scale-to-fit is that box's size and position.
 */
import type { ScreenGeometry } from "./screen-geometry.ts";

export type ConnectionState = "connecting" | "connected" | "disconnected";

/** What our additions put on the frame's window. */
export interface DeskyardScreen {
  tab: string;
  post(msg: object): void;
  on(type: "deskyard.state", fn: (state: ConnectionState) => void): () => void;
  on(type: string, fn: (data: Record<string, unknown>) => void): () => void;
  state(): ConnectionState;
  video(): HTMLVideoElement | HTMLCanvasElement | null;
}

type ScreenWindow = Window & { deskyardScreen?: DeskyardScreen; manual_resolution?: boolean };

/** The frame's URL for a desktop: Selkies' client, served by the supervisor. */
export function frameSrc(name: string): string {
  return `/${name}/screen/`;
}

/** Whether this browser can show the stream: Selkies decodes with WebCodecs. */
export function browserSupported(): boolean {
  return typeof VideoDecoder !== "undefined";
}

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
 */
export const qualities: { id: QualityId; label: string; hint: string; bitrate: number; framerate: number; encoder: string; fullcolor: boolean }[] = [
  { id: "low", label: "Low", hint: "for slow connections", bitrate: 2000, framerate: 30, encoder: "h264enc", fullcolor: false },
  { id: "medium", label: "Medium", hint: "", bitrate: 8000, framerate: 60, encoder: "h264enc", fullcolor: false },
  { id: "high", label: "High", hint: "", bitrate: 16000, framerate: 60, encoder: "h264enc", fullcolor: false },
  { id: "extreme", label: "Extreme", hint: "", bitrate: 32000, framerate: 60, encoder: "h264enc", fullcolor: false },
  { id: "lossless", label: "Lossless", hint: "for fast networks", bitrate: 64000, framerate: 60, encoder: "h264enc", fullcolor: true },
];

export const defaultQuality: QualityId = "medium";

export function isQualityId(v: unknown): v is QualityId {
  return qualities.some((q) => q.id === v);
}

export function sendQuality(frame: HTMLIFrameElement | null, id: QualityId): void {
  const q = qualities.find((x) => x.id === id);
  if (!q) return;
  screenOf(frame)?.post({
    type: "settings",
    settings: { encoder: q.encoder, rate_control_mode: "cbr", video_bitrate: q.bitrate, framerate: q.framerate, video_fullcolor: q.fullcolor },
  });
}

export interface Size {
  width: number;
  height: number;
}

/** The size the frame's client streams at, in remote pixels; null before it knows. */
export function remoteSize(frame: HTMLIFrameElement | null): Size | null {
  try {
    // The frame's own HTMLCanvasElement, so no instanceof across the realms.
    const canvas = frameWindow(frame)?.document.getElementById("videoCanvas") as HTMLCanvasElement | null | undefined;
    return canvas && canvas.width > 0 && canvas.height > 0 ? { width: canvas.width, height: canvas.height } : null;
  } catch {
    return null;
  }
}

/** Lock size: the remote screen stays at size and is scaled to fit the frame. */
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
