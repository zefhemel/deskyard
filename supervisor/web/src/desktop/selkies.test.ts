import { test } from "node:test";
import assert from "node:assert/strict";
import { frameAllow, qualitySettings, sendQuality } from "./selkies.ts";

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
