import assert from "node:assert/strict";
import { test } from "node:test";
import { accountCan, can, mayManageShare } from "./permissions.ts";

const zef = { username: "zef", admin: true, kind: "password", share: false } as const;
const anna = { username: "anna", admin: false, kind: "password", share: false } as const;
const sanne = { username: "sanne", admin: false, kind: "token", share: true } as const;
const env = (level: "full" | "observe", webdav = false) => ({ level, webdav });

test("full access uses an environment and shares it; observing does neither", () => {
  const full = can(anna, env("full"));
  assert.equal(full.use, true);
  assert.equal(full.observeOnly, false);
  assert.equal(full.share, true);
  assert.equal(full.seeShares, true);
  assert.equal(full.configure, false);
  const watching = can(anna, env("observe"));
  assert.equal(watching.use, false);
  assert.equal(watching.observeOnly, true);
  assert.equal(watching.share, false);
  assert.equal(watching.seeShares, false);
});

test("admins configure; shares use but can't share further", () => {
  assert.equal(can(zef, env("full")).configure, true);
  const share = can(sanne, env("full"));
  assert.equal(share.use, true);
  assert.equal(share.share, false);
  assert.equal(share.seeShares, true);
});

test("the Access tab shows admins the WebDAV switch, and others with full access the address while it's on", () => {
  assert.equal(can(zef, env("full")).webdav, "switch");
  assert.equal(can(zef, env("full", true)).webdav, "switch");
  assert.equal(can(anna, env("full", true)).webdav, "address");
  assert.equal(can(anna, env("full")).webdav, null);
  assert.equal(can(anna, env("observe", true)).webdav, null);
  assert.equal(can({ ...anna, kind: "token" }, env("full", true)).webdav, null);
  assert.equal(can({ ...anna, share: true }, env("full", true)).webdav, null);
});

test("an admin manages every share, anyone else the shares they made", () => {
  const byAnna = { sharedBy: { username: "anna", id: "1" } };
  assert.equal(mayManageShare(zef, byAnna), true);
  assert.equal(mayManageShare(anna, byAnna), true);
  assert.equal(mayManageShare({ username: "sam", admin: false }, byAnna), false);
  assert.equal(can(anna, env("full")).manageShare(byAnna), true);
});

test("only password accounts that aren't shares make a WebDAV password", () => {
  assert.deepEqual(accountCan(zef), { administer: true, webdavPassword: true });
  assert.deepEqual(accountCan(anna), { administer: false, webdavPassword: true });
  assert.deepEqual(accountCan(sanne), { administer: false, webdavPassword: false });
  assert.equal(accountCan({ admin: false, kind: "token", share: false }).webdavPassword, false);
});
