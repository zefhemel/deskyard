import { test } from "node:test";
import assert from "node:assert/strict";
import { AUTO_FIT, initialLock, initialTrackpad, isPhone, phoneSize } from "./display-prefs.ts";

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
