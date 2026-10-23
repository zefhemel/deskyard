import { test } from "node:test";
import assert from "node:assert/strict";
import { zoomAt } from "./view-zoom.ts";

test("pinch keeps the point under the fingers anchored", () => {
  assert.deepEqual(zoomAt(1, 2, { x: 100, y: 200 }, { left: 30, top: 40 }),
    { zoom: 2, left: 160, top: 280 });
});
test("view zoom is bounded and resetting returns to the whole screen", () => {
  assert.deepEqual(zoomAt(2, 0.01, { x: 100, y: 200 }, { left: 50, top: 60 }), { zoom: 1, left: 0, top: 0 });
  assert.equal(zoomAt(2, 100, { x: 0, y: 0 }, { left: 0, top: 0 }).zoom, 4);
});
