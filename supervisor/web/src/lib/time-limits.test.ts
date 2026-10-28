import assert from "node:assert/strict";
import { test } from "node:test";
import { expiresAt, expiryChange } from "./time-limits.ts";

test("a time limit becomes an expiry", () => {
  const now = Date.parse("2026-10-27T10:00:00Z");
  assert.equal(expiresAt("none", "", now), null);
  assert.equal(expiresAt("hour", "", now)?.toISOString(), "2026-10-27T11:00:00.000Z");
  assert.equal(expiresAt("day", "", now)?.toISOString(), "2026-10-28T10:00:00.000Z");
  assert.equal(expiresAt("week", "", now)?.toISOString(), "2026-11-03T10:00:00.000Z");
  // A date lasts to the end of that day, local time; none picked yet is undefined.
  assert.equal(expiresAt("date", "", now), undefined);
  const d = expiresAt("date", "2026-11-05", now);
  assert.equal(d?.getDate(), 5);
  assert.equal(d?.getHours(), 23);
});

test("an account change carries the picked expiry when it changed", () => {
  const when = new Date("2026-12-01T00:00:00Z");
  assert.equal(expiryChange({ canExpire: true, changeExpiry: true, when, hadExpiry: false }), "2026-12-01T00:00:00.000Z");
  assert.equal(expiryChange({ canExpire: true, changeExpiry: true, when: null, hadExpiry: true }), null);
  assert.equal(expiryChange({ canExpire: true, changeExpiry: false, when, hadExpiry: true }), undefined);
});

test("an account that becomes an admin with a password loses its expiry", () => {
  assert.equal(expiryChange({ canExpire: false, changeExpiry: true, when: new Date(), hadExpiry: true }), null);
  assert.equal(expiryChange({ canExpire: false, changeExpiry: false, when: undefined, hadExpiry: true }), null);
  assert.equal(expiryChange({ canExpire: false, changeExpiry: false, when: undefined, hadExpiry: false }), undefined);
});
