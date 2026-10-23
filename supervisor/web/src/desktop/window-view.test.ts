import { test } from "node:test";
import assert from "node:assert/strict";
import { windowRegion } from "./window-view.ts";

const windows = [
  { id: "1", parent: null, x: 100, y: 80, width: 800, height: 600 },
  { id: "2", parent: "1", x: 700, y: 500, width: 400, height: 300 },
  { id: "3", parent: null, x: 0, y: 0, width: 1920, height: 1080 },
];
test("focus includes child dialogs but excludes unrelated windows", () => {
  assert.deepEqual(windowRegion(windows, "1", 1280, 800), { x: 100, y: 80, width: 1000, height: 720 });
});
test("focus clips offscreen windows and drops missing windows", () => {
  assert.deepEqual(windowRegion([{ id: "1", parent: null, x: -30, y: -20, width: 100, height: 100 }], "1", 1280, 800), { x: 0, y: 0, width: 70, height: 80 });
  assert.equal(windowRegion(windows, "closed", 1280, 800), null);
});
