import { test } from "node:test";
import assert from "node:assert/strict";
import { configureDisplay, frameAllow, qualitySettings, sendQuality } from "./selkies.ts";
import type { Display } from "../selkies/screen.ts";

test("plain HTTP browsers can use JPEG and are never asked for H.264", () => {
  const decoder = Object.getOwnPropertyDescriptor(globalThis, "VideoDecoder");
  const bitmap = Object.getOwnPropertyDescriptor(globalThis, "createImageBitmap");
  try {
    Reflect.deleteProperty(globalThis, "VideoDecoder");
    Object.defineProperty(globalThis, "createImageBitmap", { configurable: true, value: () => {} });
    assert.equal(qualitySettings("medium", true)?.encoder, "jpeg");
    let sent: unknown;
    const frame = { contentWindow: { deskyardScreen: { post: (value: unknown) => { sent = value; } } } } as unknown as HTMLIFrameElement;
    sendQuality(frame, "medium");
    assert.equal((sent as {settings: {encoder: string}}).settings.encoder, "jpeg");
  } finally {
    if (decoder) Object.defineProperty(globalThis, "VideoDecoder", decoder); else Reflect.deleteProperty(globalThis, "VideoDecoder");
    if (bitmap) Object.defineProperty(globalThis, "createImageBitmap", bitmap); else Reflect.deleteProperty(globalThis, "createImageBitmap");
  }
});

test("only a full user's frame may use the microphone and the clipboard", () => {
  const full = frameAllow(false).split("; ");
  const observer = frameAllow(true).split("; ");
  for (const p of ["microphone", "clipboard-read", "clipboard-write"]) {
    assert.ok(full.includes(p), p);
    assert.ok(!observer.includes(p), p);
  }
  assert.ok(observer.includes("autoplay"));
});

function frameWith(screen: object): HTMLIFrameElement {
  return { contentWindow: { deskyardScreen: screen } } as unknown as HTMLIFrameElement;
}

test("configureDisplay hands an adapter the size and DPI, every time", () => {
  const calls: unknown[] = [];
  const frame = frameWith({ configureDisplay: (size: object, dpi: number) => calls.push([size, dpi]) });
  configureDisplay(frame, { width: 2560, height: 1600 }, 2);
  configureDisplay(frame, { width: 2560, height: 1600 }, 2);
  assert.deepEqual(calls, [[{ width: 2560, height: 1600 }, 192], [{ width: 2560, height: 1600 }, 192]]);
});

test("configureDisplay applies legacy additions once per ownership, and again after a reconnect", () => {
  let display: Display = { owner: null, width: 0, height: 0, settings: null };
  const posted: unknown[] = [];
  const frame = frameWith({ display: () => display, post: (m: unknown) => posted.push(m) });
  const size = { width: 1280, height: 800 };
  configureDisplay(frame, size, 1);
  assert.deepEqual(posted, [], "not before this page owns the display");
  display = { ...display, owner: true };
  configureDisplay(frame, size, 1);
  configureDisplay(frame, size, 1);
  const applied = [
    { type: "settings", settings: { scaling_dpi: 96 } },
    { type: "setScaleLocally", value: true },
    { type: "setManualResolution", width: 1280, height: 800 },
  ];
  assert.deepEqual(posted, applied);
  configureDisplay(frame, size, 2);
  assert.equal(posted.length, 6, "a new scale applies");
  // A reconnect: the display is nobody's until the server says.
  display = { ...display, owner: null };
  configureDisplay(frame, size, 2);
  display = { ...display, owner: true };
  configureDisplay(frame, size, 2);
  assert.equal(posted.length, 9);
});
