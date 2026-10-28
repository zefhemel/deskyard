/*
 * The parent↔frame screen API: what our additions in the desktop frame
 * (selkies/adapter.ts, or the legacy screen_additions.js for images that
 * predate the client contract) put on the frame's window as
 * `window.deskyardScreen`, for the desktop page (desktop/) to drive.
 *
 * contract.ts is the other boundary: the hooks the pinned upstream client
 * calls into the adapter. desktop/selkies.ts holds the parent-side helpers.
 */
import type { Gesture } from "./contract.ts";

export type ConnectionState = "connecting" | "connected" | "disconnected";

export interface Size {
  width: number;
  height: number;
}

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

/** Presentation statistics from the pinned client (Selkies 2.1.0's stream_stats). */
export interface StreamSample {
  /** When it was taken (Date.now()). */
  t: number;
  /** Frames shown per second. */
  fps: number;
  /** Frames decoded but never shown, counted since the stream started. */
  frames_not_shown: number;
  present_ms?: number;
}

/** What our additions put on the frame's window. */
export interface DeskyardScreen {
  tab: string;
  keyboard?(show: boolean): void;
  setTouchMode?(trackpad: boolean): void;
  setViewZoom?(zoom: number): void;
  /**
   * The display this page wants when it owns it: size in remote pixels and
   * DPI, the picture scaled to fit. Idempotent; kept across reconnects and
   * applied whenever this page (again) owns the display. Not in the legacy
   * additions (desktop/selkies.ts's configureDisplay covers them).
   */
  configureDisplay?(size: Size, dpi: number): void;
  /** The client's latest presentation statistics; null before any. Not in the legacy additions. */
  stats?(): StreamSample | null;
  /** Opens or closes the client's statistics (its statsOpen message), which keep stats() current. */
  showStats?(open: boolean): void;
  on(type: "deskyard.keyboard", fn: (visible: boolean) => void): () => void;
  on(type: "deskyard.gesture", fn: (gesture: Gesture) => void): () => void;
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
