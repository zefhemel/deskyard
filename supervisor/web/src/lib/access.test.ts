import assert from "node:assert/strict";
import { test } from "node:test";
import { cliLoginId, expiresAt, expiryChange, grantsLine, levelLabel, parseLinkFragment, safeNext } from "./access.ts";

test("a link's code is the whole fragment when it is one", () => {
  assert.equal(parseLinkFragment("#dyl_ab_cd"), "dyl_ab_cd");
  assert.equal(parseLinkFragment("#dyl_0a1b2c3d_Jx3vT9pLr7-KzA1_mYc5"), "dyl_0a1b2c3d_Jx3vT9pLr7-KzA1_mYc5");
});

test("any other fragment is no code", () => {
  for (const hash of ["", "#", "dyl_ab_cd", "#dyc_ab_cd", "#dyl_ab", "#dyl__cd", "#dyl_ab_cd&x=1", "#dyl_ab_cd ", "#xdyl_ab_cd"]) {
    assert.equal(parseLinkFragment(hash), "", hash);
  }
});

test("a CLI login request's id is the fragment", () => {
  assert.equal(cliLoginId("#k3j9QxZ"), "k3j9QxZ");
  assert.equal(cliLoginId(""), "");
  assert.equal(cliLoginId("#"), "");
  assert.equal(cliLoginId("#a/b"), "");
});

test("levels have labels", () => {
  assert.equal(levelLabel("full"), "Full access");
  assert.equal(levelLabel("observe"), "Observe only");
});

test("grants read as a line", () => {
  assert.equal(grantsLine([]), "");
  assert.equal(grantsLine([{ computer: "alice", level: "observe" }]), "observe alice");
  assert.equal(
    grantsLine([
      { computer: "alice", level: "full" },
      { computer: "notes", level: "observe" },
    ]),
    "use alice and observe notes",
  );
});

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

test("only paths on this origin are places to go next", () => {
  assert.equal(safeNext(null), "/");
  assert.equal(safeNext("/cli-login#abc"), "/cli-login#abc");
  assert.equal(safeNext("//evil.example"), "/");
  assert.equal(safeNext("/\\evil.example"), "/");
  assert.equal(safeNext("https://evil.example/"), "/");
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
