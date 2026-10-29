import { test } from "node:test";
import assert from "node:assert/strict";
import { displayScale, scaledSize, parseScaling, AUTO_FIT, initialLock, initialTrackpad, isPhone, phoneSize } from "./display-prefs.ts";

test("a screen with a short side below 640 is a phone, in either orientation", () => {
  assert.equal(isPhone({ width: 390, height: 844 }), true);
  assert.equal(isPhone({ width: 932, height: 430 }), true);
  assert.equal(isPhone({ width: 744, height: 1133 }), false);
  assert.equal(isPhone({ width: 1920, height: 1080 }), false);
});

test("a phone starts locked at 1280×720 with the trackpad", () => {
  assert.deepEqual(initialLock(null, true), phoneSize);
  assert.deepEqual(phoneSize, { width: 1280, height: 720 });
  assert.equal(initialTrackpad(null, true), true);
});

test("anything else starts with Auto fit and Tap", () => {
  assert.equal(initialLock(null, false), null);
  assert.equal(initialTrackpad(null, false), false);
});

test("a stored choice wins over the default", () => {
  assert.equal(initialLock(AUTO_FIT, true), null);
  assert.deepEqual(initialLock(JSON.stringify({ width: 600, height: 900 }), true), { width: 600, height: 900 });
  assert.deepEqual(initialLock(JSON.stringify({ width: 1024, height: 768 }), false), { width: 1024, height: 768 });
  assert.equal(initialTrackpad("false", true), false);
  assert.equal(initialTrackpad("true", false), true);
});

test("an unreadable stored value falls back to the default", () => {
  assert.deepEqual(initialLock("{", true), phoneSize);
  assert.equal(initialLock("{", false), null);
  assert.equal(initialTrackpad("yes", true), true);
});


test("HiDPI auto detection and explicit overrides", () => {
  assert.equal(displayScale("auto", 1), 1);
  assert.equal(displayScale("auto", 1.25), 1);
  assert.equal(displayScale("auto", 1.5), 2);
  assert.equal(displayScale("auto", 2), 2);
  assert.equal(displayScale("auto", 3), 2);
  assert.equal(displayScale("1", 2), 1);
  assert.equal(displayScale("2", 1), 2);
  assert.equal(parseScaling("invalid"), "auto");
  assert.equal(parseScaling(null), "auto");
});

test("logical resolution becomes even physical pixels within the framebuffer", () => {
  assert.deepEqual(scaledSize({ width: 1280, height: 800 }, 2), { width: 2560, height: 1600 });
  assert.deepEqual(scaledSize({ width: 1920, height: 1080 }, 2), { width: 3840, height: 2160 });
  assert.deepEqual(scaledSize({ width: 1001, height: 701 }, 1), { width: 1000, height: 700 });
  assert.deepEqual(scaledSize({ width: 3000, height: 2000 }, 2), { width: 3240, height: 2160 });
  assert.deepEqual(scaledSize({ width: 600, height: 900 }, 2), { width: 1200, height: 1800 });
});
