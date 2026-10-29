import type { Capture } from "./screen.ts";

export const clientContract = 1;
export function shouldSend(data: unknown): boolean {
  return (
    data !== "STOP_VIDEO" && data !== "START_AUDIO" && data !== "STOP_AUDIO"
  );
}

/**
 * Consent is revoked on reconnect and while a browser permission prompt is
 * open. Video is never captured.
 */
export function captureGate() {
  const allowed = { microphone: false };
  let generation = 0;
  const permitted = (c: MediaStreamConstraints) =>
    !c.video && (!c.audio || allowed.microphone);
  const denied = () =>
    new DOMException(
      "Turn capture on in the desktop's Audio panel",
      "NotAllowedError",
    );
  return {
    allowed(kind: Capture) {
      return allowed[kind];
    },
    allow(kind: Capture, on: boolean) {
      allowed[kind] = on;
      if (!on) generation++;
    },
    reset() {
      allowed.microphone = false;
      generation++;
    },
    async capture(
      c: MediaStreamConstraints,
      acquire: (constraints: MediaStreamConstraints) => Promise<MediaStream>,
    ) {
      if (!permitted(c)) throw denied();
      const token = generation;
      const stream = await acquire(c);
      if (token !== generation || !permitted(c)) {
        stream.getTracks().forEach((track) => track.stop());
        throw denied();
      }
      return stream;
    },
  };
}

export interface Transport {
  readyState: number;
  send(data: string | ArrayBuffer | ArrayBufferView): void;
  addEventListener(type: string, listener: (event: MessageEvent) => void): void;
}
export interface ClientInput {
  /** The input surface, supplied by the pinned v1 client. */
  element?: HTMLElement;
  setTrackpadMode(trackpad: boolean): void;
}
export type Gesture =
  | { kind: "zoom"; factor: number; x: number; y: number }
  | { kind: "pan"; dx: number; dy: number }
  | { kind: "cursor"; x: number; y: number };

/** Calls supplied by the supervisor adapter and invoked at reviewed client call sites. */
export interface ClientHooks {
  version: 1;
  allowInsecure: boolean;
  pointerMoveOnly: boolean;
  transport(value: Transport | null): void;
  url(url: string): string;
  send(
    transport: Transport,
    data: string | ArrayBuffer | ArrayBufferView,
  ): void;
  capture(constraints: MediaStreamConstraints): Promise<MediaStream>;
  input(value: ClientInput | null): void;
  touchMode(value: boolean): boolean;
  points(
    x1: number,
    y1: number,
    x2: number,
    y2: number,
  ): [number, number, number, number];
  zoom(notches: number, x: number, y: number): boolean;
  pan(delta: number, vertical: boolean): boolean;
  trackpadOrigin(touch: object): [number, number];
  cursor(x: number, y: number): void;
  focus(element: HTMLElement, options?: FocusOptions): void;
  playback(context: AudioContext): void;
  playbackOutput(node: AudioNode): void;
  audioTick(): void;
  addWorklet(context: AudioContext, url: string): Promise<void>;
  workletNode(
    context: AudioContext,
    name: string,
    options?: AudioWorkletNodeOptions,
  ): AudioWorkletNode | ScriptWorkletNode;
  alert(text: string): void;
}
export interface ScriptWorkletNode extends ScriptProcessorNode {
  port: MessagePort;
}

export interface DisplayRequest { width: number; height: number; dpi: number }

/** Keep delayed upstream auto-sizing messages from replacing the owner's policy. */
export function displayMessage<T>(data: T, owner: boolean | null, requested: DisplayRequest | null): T | string | null {
  if (typeof data !== "string") return data;
  const separator = data.indexOf(",", 2);
  const suffix = separator < 0 ? "" : data.slice(separator);
  if (data.startsWith("r,"))
    return owner && requested ? `r,${requested.width}x${requested.height}${suffix}` : null;
  if (data.startsWith("s,"))
    return owner && requested ? `s,${requested.dpi}${suffix}` : null;
  if (!data.startsWith("SETTINGS,")) return data;
  let settings: Record<string, unknown>;
  try { settings = JSON.parse(data.slice(9)); } catch { return data; }
  if (owner && requested) {
    Object.assign(settings, { manual_resolution: true, manual_width: requested.width, manual_height: requested.height, scaling_dpi: requested.dpi });
  } else {
    // The first SETTINGS completes the ownership handshake. Let it through,
    // but preserve the server's geometry until the parent supplies its policy.
    // Initial client dimensions otherwise resize a reconnect to CSS pixels.
    delete settings.scaling_dpi;
    delete settings.manual_resolution;
    delete settings.manual_width;
    delete settings.manual_height;
  }
  delete settings.initialClientWidth;
  delete settings.initialClientHeight;
  return `SETTINGS,${JSON.stringify(settings)}`;
}
