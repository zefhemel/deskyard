import assert from "node:assert/strict";
import { test } from "node:test";
import { type ScreenGeometry, toViewport } from "./screen-geometry.ts";

// Node has no DOMRect; toViewport reads only left, top, width and height.
const geometry = (displayed: { left: number; top: number; width: number; height: number }): ScreenGeometry => ({
  remoteWidth: 1920,
  remoteHeight: 1080,
  displayed: displayed as DOMRect,
});

test("a remote screen shown at its own size", () => {
  const g = geometry({ left: 0, top: 46, width: 1920, height: 1080 });
  assert.deepEqual(toViewport(g, 100, 200), { left: 100, top: 246 });
});

test("a remote screen scaled down and letterboxed", () => {
  const g = geometry({ left: 40, top: 46, width: 960, height: 540 });
  assert.deepEqual(toViewport(g, 100, 200), { left: 90, top: 146 });
});

test("points outside the remote screen", () => {
  const g = geometry({ left: 0, top: 46, width: 1920, height: 1080 });
  assert.equal(toViewport(g, 1920, 10), null);
  assert.equal(toViewport(g, -1, 10), null);
  assert.equal(toViewport(g, 10, 1080), null);
  assert.equal(toViewport(g, 10, -1), null);
});
