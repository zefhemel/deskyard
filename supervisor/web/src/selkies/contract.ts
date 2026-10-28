import type { Capture } from "../desktop/selkies.ts";

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
