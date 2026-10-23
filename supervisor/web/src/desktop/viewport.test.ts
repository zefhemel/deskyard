import { test } from "node:test";
import assert from "node:assert/strict";
import { visibleViewport } from "./viewport.ts";

test("standalone iOS restores the top inset omitted by its viewport metrics", () => {
  assert.deepEqual(visibleViewport({ height: 812, offsetTop: 0 }, 812, 874), { height: 874, top: 0, keyboard: false });
  assert.deepEqual(visibleViewport({ height: 505, offsetTop: 0 }, 812, 874), { height: 567, top: 0, keyboard: true });
});
test("regular browser viewports and keyboard offsets stay unchanged", () => {
  assert.deepEqual(visibleViewport({ height: 600, offsetTop: 20 }, 874), { height: 600, top: 20, keyboard: true });
  assert.deepEqual(visibleViewport({ height: 390, offsetTop: 0 }, 390, 390), { height: 390, top: 0, keyboard: false });
});
